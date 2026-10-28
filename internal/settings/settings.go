// Package settings reads and edits Claude Code settings.json files without
// disturbing keys it does not own. Top-level key order is preserved; the
// hooks and permissions subtrees are rewritten with 2-space indentation.
package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// File is one settings file.
type File struct {
	Path    string
	Existed bool
	keys    []string
	vals    map[string]json.RawMessage
}

// UserSettingsPath is ~/.claude/settings.json (honours CLAUDE_CONFIG_DIR).
func UserSettingsPath() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "settings.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".claude", "settings.json")
	}
	return filepath.Join(home, ".claude", "settings.json")
}

// ProjectSettingsPath is <root>/.claude/settings.json.
func ProjectSettingsPath(root string) string {
	return filepath.Join(root, ".claude", "settings.json")
}

// LocalSettingsPath is <root>/.claude/settings.local.json.
func LocalSettingsPath(root string) string {
	return filepath.Join(root, ".claude", "settings.local.json")
}

// Load parses a settings file. A missing file loads as empty; an unparseable
// file is an error (we never overwrite what we could not read).
func Load(path string) (*File, error) {
	f := &File{Path: path, vals: map[string]json.RawMessage{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return nil, err
	}
	f.Existed = true
	if err := f.parse(b); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

func (f *File) parse(b []byte) error {
	if len(bytes.TrimSpace(b)) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return errors.New("top level is not an object")
	}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := kt.(string)
		if !ok {
			return errors.New("bad key")
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return err
		}
		if _, dup := f.vals[key]; !dup {
			f.keys = append(f.keys, key)
		}
		f.vals[key] = raw
	}
	if _, err := dec.Token(); err != nil {
		return err
	}
	return nil
}

// Get returns the raw value of a top-level key (nil if absent).
func (f *File) Get(key string) json.RawMessage { return f.vals[key] }

// Set stores a top-level key, keeping its position if it already exists.
func (f *File) Set(key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, ok := f.vals[key]; !ok {
		f.keys = append(f.keys, key)
	}
	f.vals[key] = b
	return nil
}

// Delete removes a top-level key.
func (f *File) Delete(key string) {
	if _, ok := f.vals[key]; !ok {
		return
	}
	delete(f.vals, key)
	out := f.keys[:0]
	for _, k := range f.keys {
		if k != key {
			out = append(out, k)
		}
	}
	f.keys = out
}

// Bytes renders the file.
func (f *File) Bytes() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("{\n")
	for i, k := range f.keys {
		kb, _ := json.Marshal(k)
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, f.vals[k], "  ", "  "); err != nil {
			return nil, err
		}
		buf.WriteString("  ")
		buf.Write(kb)
		buf.WriteString(": ")
		buf.Write(pretty.Bytes())
		if i < len(f.keys)-1 {
			buf.WriteString(",")
		}
		buf.WriteString("\n")
	}
	buf.WriteString("}\n")
	return buf.Bytes(), nil
}

// Save writes the file atomically, backing up the original once per day into
// backupDir (when non-empty and the file existed).
func (f *File) Save(backupDir string) error {
	b, err := f.Bytes()
	if err != nil {
		return err
	}
	if f.Existed && backupDir != "" {
		if orig, err := os.ReadFile(f.Path); err == nil {
			_ = os.MkdirAll(backupDir, 0o755)
			name := strings.ReplaceAll(strings.TrimSuffix(filepath.Base(f.Path), ".json"), string(filepath.Separator), "_")
			bp := filepath.Join(backupDir, fmt.Sprintf("%s.%s.json", name, time.Now().Format("20060102")))
			if _, err := os.Stat(bp); errors.Is(err, os.ErrNotExist) {
				_ = os.WriteFile(bp, orig, 0o644)
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
		return err
	}
	tmp := f.Path + ".yessir.tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, f.Path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	f.Existed = true
	return nil
}

// ---- hooks ----------------------------------------------------------------

// Handler is one command hook entry.
type Handler struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Timeout int      `json:"timeout,omitempty"`
}

// IsYessir reports whether a handler object was written by yessir.
func IsYessir(h map[string]any) bool {
	cmd, _ := h["command"].(string)
	base := strings.ToLower(filepath.Base(strings.ReplaceAll(cmd, "\\", "/")))
	if !strings.HasPrefix(base, "yessir") {
		return false
	}
	args, _ := h["args"].([]any)
	return len(args) == 1 && args[0] == "hook"
}

func (f *File) hooks() (map[string]any, error) {
	raw := f.vals["hooks"]
	hooks := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &hooks); err != nil {
			return nil, fmt.Errorf("hooks is not an object: %w", err)
		}
	}
	return hooks, nil
}

// EnsureHook registers yessir under hooks.<event> (empty matcher, exec form).
// It returns true when the file changed.
func (f *File) EnsureHook(event string, h Handler) (bool, error) {
	hooks, err := f.hooks()
	if err != nil {
		return false, err
	}
	groups, _ := hooks[event].([]any)
	// Is there already an up-to-date yessir handler?
	for _, g := range groups {
		gm, _ := g.(map[string]any)
		hs, _ := gm["hooks"].([]any)
		for _, x := range hs {
			hm, _ := x.(map[string]any)
			if IsYessir(hm) {
				if hm["command"] == h.Command {
					return false, nil
				}
				hm["command"] = h.Command
				hm["timeout"] = float64(h.Timeout)
				hooks[event] = groups
				return true, f.Set("hooks", hooks)
			}
		}
	}
	groups = append(groups, map[string]any{
		"matcher": "",
		"hooks":   []any{map[string]any{"type": h.Type, "command": h.Command, "args": h.Args, "timeout": h.Timeout}},
	})
	hooks[event] = groups
	return true, f.Set("hooks", hooks)
}

// RemoveHook drops every yessir handler under hooks.<event>; empty groups and
// an empty hooks object are removed too. Returns true when the file changed.
func (f *File) RemoveHook(event string) (bool, error) {
	hooks, err := f.hooks()
	if err != nil {
		return false, err
	}
	groups, _ := hooks[event].([]any)
	changed := false
	var keep []any
	for _, g := range groups {
		gm, _ := g.(map[string]any)
		hs, _ := gm["hooks"].([]any)
		var khs []any
		for _, x := range hs {
			hm, _ := x.(map[string]any)
			if IsYessir(hm) {
				changed = true
				continue
			}
			khs = append(khs, x)
		}
		if len(khs) == 0 {
			if len(hs) > 0 {
				changed = true
			}
			continue
		}
		gm["hooks"] = khs
		keep = append(keep, gm)
	}
	if !changed {
		return false, nil
	}
	if len(keep) == 0 {
		delete(hooks, event)
	} else {
		hooks[event] = keep
	}
	if len(hooks) == 0 {
		f.Delete("hooks")
		return true, nil
	}
	return true, f.Set("hooks", hooks)
}

// HasYessirHook reports whether a yessir handler is registered, and its command.
func (f *File) HasYessirHook(event string) (bool, string) {
	hooks, err := f.hooks()
	if err != nil {
		return false, ""
	}
	groups, _ := hooks[event].([]any)
	for _, g := range groups {
		gm, _ := g.(map[string]any)
		hs, _ := gm["hooks"].([]any)
		for _, x := range hs {
			hm, _ := x.(map[string]any)
			if IsYessir(hm) {
				cmd, _ := hm["command"].(string)
				return true, cmd
			}
		}
	}
	return false, ""
}

// ---- permissions ----------------------------------------------------------

func (f *File) permissions() (map[string]any, error) {
	raw := f.vals["permissions"]
	perms := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &perms); err != nil {
			return nil, fmt.Errorf("permissions is not an object: %w", err)
		}
	}
	return perms, nil
}

// Rules returns permissions.<list> as strings.
func (f *File) Rules(list string) []string {
	perms, err := f.permissions()
	if err != nil {
		return nil
	}
	arr, _ := perms[list].([]any)
	var out []string
	for _, x := range arr {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// PermissionValue returns permissions.<key> as a string ("" if absent).
func (f *File) PermissionValue(key string) string {
	perms, err := f.permissions()
	if err != nil {
		return ""
	}
	switch v := perms[key].(type) {
	case string:
		return v
	case bool:
		if v {
			return "true"
		}
		return "false"
	}
	return ""
}

// TopBool returns a top-level boolean key.
func (f *File) TopBool(key string) (val bool, present bool) {
	raw := f.vals[key]
	if len(raw) == 0 {
		return false, false
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil {
		return false, true
	}
	return b, true
}

// TopString returns a top-level string key.
func (f *File) TopString(key string) string {
	raw := f.vals[key]
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

// AddRules appends rules to permissions.<list>, skipping ones already there.
// It returns the rules it actually added.
func (f *File) AddRules(list string, rules []string) ([]string, error) {
	perms, err := f.permissions()
	if err != nil {
		return nil, err
	}
	arr, _ := perms[list].([]any)
	have := map[string]bool{}
	for _, x := range arr {
		if s, ok := x.(string); ok {
			have[s] = true
		}
	}
	var added []string
	for _, r := range rules {
		if have[r] {
			continue
		}
		arr = append(arr, r)
		have[r] = true
		added = append(added, r)
	}
	if len(added) == 0 {
		return nil, nil
	}
	perms[list] = arr
	return added, f.Set("permissions", perms)
}

// RemoveRules deletes the given rules from permissions.<list>.
func (f *File) RemoveRules(list string, rules []string) (bool, error) {
	perms, err := f.permissions()
	if err != nil {
		return false, err
	}
	arr, _ := perms[list].([]any)
	drop := map[string]bool{}
	for _, r := range rules {
		drop[r] = true
	}
	var keep []any
	changed := false
	for _, x := range arr {
		if s, ok := x.(string); ok && drop[s] {
			changed = true
			continue
		}
		keep = append(keep, x)
	}
	if !changed {
		return false, nil
	}
	if len(keep) == 0 {
		delete(perms, list)
	} else {
		perms[list] = keep
	}
	if len(perms) == 0 {
		f.Delete("permissions")
		return true, nil
	}
	return true, f.Set("permissions", perms)
}
