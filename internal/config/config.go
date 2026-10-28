// Package config owns everything under ~/.yessir and the per-folder marker.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Config is ~/.yessir/config.json.
type Config struct {
	Version      int      `json:"version"`
	Global       bool     `json:"global"`
	Log          bool     `json:"log"`
	Learn        bool     `json:"learn"`
	NeverApprove []string `json:"neverApprove"`
	Ask          []string `json:"ask"`
	Deny         []string `json:"deny"`
	DenyMessage  string   `json:"denyMessage"`
}

// Marker is <root>/.claude/yessir.local.json.
type Marker struct {
	Enabled bool     `json:"enabled"`
	Ask     []string `json:"ask,omitempty"`
	Deny    []string `json:"deny,omitempty"`
}

// State is ~/.yessir/state.json: exactly what yessir wrote elsewhere, so that
// off/uninstall remove only their own lines.
type State struct {
	HookCommand      string              `json:"hookCommand,omitempty"`
	HookRegisteredAt string              `json:"hookRegisteredAt,omitempty"`
	HookSettingsFile string              `json:"hookSettingsFile,omitempty"`
	AllowRules       map[string][]string `json:"allowRules,omitempty"` // settings file -> rules written
	DenyRules        map[string][]string `json:"denyRules,omitempty"`
	Markers          []string            `json:"markers,omitempty"` // marker files written
}

const (
	MarkerRel  = ".claude/yessir.local.json"
	configName = "config.json"
	stateName  = "state.json"
	pausedName = "paused"
	LogName    = "log.jsonl"
)

// Dir returns ~/.yessir (or $YESSIR_HOME).
func Dir() string {
	if d := os.Getenv("YESSIR_HOME"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".yessir"
	}
	return filepath.Join(home, ".yessir")
}

// Default is the config written on first use.
func Default() *Config {
	return &Config{
		Version:      1,
		Global:       false,
		Log:          true,
		Learn:        false,
		NeverApprove: []string{"AskUserQuestion"},
		Ask: []string{
			"Bash(git push --force *)",
			"Bash(git push -f *)",
			"Bash(git push * --force)",
			"Bash(rm -rf /*)",
			"Bash(rm -rf ~*)",
			"Bash(sudo *)",
		},
		Deny:        []string{},
		DenyMessage: "blocked by yessir config",
	}
}

func readJSON(path string, v any) (bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return false, nil
	}
	return true, json.Unmarshal(b, v)
}

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Load reads config.json, returning defaults when it does not exist.
func Load() (*Config, error) {
	c := Default()
	found, err := readJSON(filepath.Join(Dir(), configName), c)
	if err != nil {
		return nil, err
	}
	if !found {
		return c, nil
	}
	if c.NeverApprove == nil {
		c.NeverApprove = []string{}
	}
	return c, nil
}

// Save writes config.json.
func (c *Config) Save() error { return writeJSON(filepath.Join(Dir(), configName), c) }

// Exists reports whether config.json has been written.
func Exists() bool {
	_, err := os.Stat(filepath.Join(Dir(), configName))
	return err == nil
}

// LoadState reads state.json (empty state if missing).
func LoadState() (*State, error) {
	s := &State{}
	if _, err := readJSON(filepath.Join(Dir(), stateName), s); err != nil {
		return nil, err
	}
	if s.AllowRules == nil {
		s.AllowRules = map[string][]string{}
	}
	if s.DenyRules == nil {
		s.DenyRules = map[string][]string{}
	}
	return s, nil
}

// Save writes state.json.
func (s *State) Save() error { return writeJSON(filepath.Join(Dir(), stateName), s) }

// AddMarker records a marker file path once.
func (s *State) AddMarker(p string) {
	for _, m := range s.Markers {
		if m == p {
			return
		}
	}
	s.Markers = append(s.Markers, p)
}

// RemoveMarker forgets a marker file path.
func (s *State) RemoveMarker(p string) {
	out := s.Markers[:0]
	for _, m := range s.Markers {
		if m != p {
			out = append(out, m)
		}
	}
	s.Markers = out
}

// ---- pause ----------------------------------------------------------------

func pausedPath() string { return filepath.Join(Dir(), pausedName) }

// PausedUntil returns the pause deadline, or zero when not paused.
func PausedUntil() time.Time {
	b, err := os.ReadFile(pausedPath())
	if err != nil {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(b)))
	if err != nil {
		return time.Time{}
	}
	return t
}

// IsPaused reports whether a pause is in effect now.
func IsPaused(now time.Time) bool {
	u := PausedUntil()
	return !u.IsZero() && now.Before(u)
}

// Pause writes the pause deadline.
func Pause(until time.Time) error {
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(pausedPath(), []byte(until.UTC().Format(time.RFC3339)+"\n"), 0o644)
}

// Resume clears a pause.
func Resume() error {
	err := os.Remove(pausedPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// ---- folder marker --------------------------------------------------------

// FindMarker walks from dir upward and returns the first marker found, its
// root directory, or ("", nil, nil) when there is none.
func FindMarker(dir string) (root string, m *Marker, err error) {
	dir = filepath.Clean(dir)
	for {
		p := filepath.Join(dir, filepath.FromSlash(MarkerRel))
		var mk Marker
		found, rerr := readJSON(p, &mk)
		if rerr != nil {
			return "", nil, rerr
		}
		if found {
			return dir, &mk, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil, nil
		}
		dir = parent
	}
}

// MarkerPath is the marker file for a root.
func MarkerPath(root string) string { return filepath.Join(root, filepath.FromSlash(MarkerRel)) }

// WriteMarker creates or updates the marker for root.
func WriteMarker(root string, m Marker) error { return writeJSON(MarkerPath(root), m) }

// RemoveMarkerFile deletes the marker for root (no error if absent).
func RemoveMarkerFile(root string) error {
	err := os.Remove(MarkerPath(root))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// GitRoot walks up from dir to the nearest directory containing .git.
// It returns "" when dir is not inside a git repository.
func GitRoot(dir string) string {
	dir = filepath.Clean(dir)
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// ExcludeFromGit adds rel to <root>/.git/info/exclude (idempotent). It is a
// no-op when root is not a git repository or .git is a file (worktree).
func ExcludeFromGit(root, rel string) error {
	gitDir := filepath.Join(root, ".git")
	st, err := os.Stat(gitDir)
	if err != nil || !st.IsDir() {
		return nil
	}
	p := filepath.Join(gitDir, "info", "exclude")
	b, _ := os.ReadFile(p)
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == rel {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if len(b) > 0 && !strings.HasSuffix(string(b), "\n") {
		if _, err := f.WriteString("\n"); err != nil {
			return err
		}
	}
	_, err = f.WriteString(rel + "\n")
	return err
}
