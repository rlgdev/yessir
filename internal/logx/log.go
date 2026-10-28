// Package logx is the decision log: one JSON line per hook decision in
// ~/.yessir/log.jsonl, rotated at 5 MB into log.jsonl.1 .. .3.
package logx

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Entry is one logged decision.
type Entry struct {
	TS       string `json:"ts"`
	Session  string `json:"session_id,omitempty"`
	CWD      string `json:"cwd"`
	Scope    string `json:"scope"`
	Tool     string `json:"tool"`
	Summary  string `json:"summary"`
	Decision string `json:"decision"`
	Reason   string `json:"reason,omitempty"`
	Ms       int64  `json:"ms"`
}

const (
	maxBytes = 5 << 20
	keep     = 3
)

// Append writes one entry (best effort: errors are returned but callers
// usually ignore them, the hook must never fail because of logging).
func Append(path string, e Entry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if st, err := os.Stat(path); err == nil && st.Size() > maxBytes {
		rotate(path)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}

func rotate(path string) {
	for i := keep - 1; i >= 1; i-- {
		_ = os.Rename(path+"."+itoa(i), path+"."+itoa(i+1))
	}
	_ = os.Rename(path, path+".1")
}

func itoa(i int) string { return string(rune('0' + i)) }

// Tail returns the last n entries (oldest first).
func Tail(path string, n int) ([]Entry, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if l == "" {
			continue
		}
		lines = append(lines, l)
		if len(lines) > n {
			lines = lines[1:]
		}
	}
	var out []Entry
	for _, l := range lines {
		var e Entry
		if json.Unmarshal([]byte(l), &e) == nil {
			out = append(out, e)
		}
	}
	return out, sc.Err()
}

// Now is the timestamp format used in entries.
func Now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
