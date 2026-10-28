// Package policy discovers the managed (enterprise) settings Claude Code
// applies on this machine, read-only, so doctor can say what is enforced.
package policy

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// Source is one place a managed policy can come from.
type Source struct {
	Name     string         // human label
	Path     string         // file path or registry key
	Present  bool           // something exists there
	Readable bool           // and we could read + parse it
	Err      string         // why not
	Data     map[string]any // parsed JSON (nil when not readable)
}

// SystemDir is the OS-specific managed settings directory.
func SystemDir() string {
	switch runtime.GOOS {
	case "windows":
		pf := os.Getenv("ProgramFiles")
		if pf == "" {
			pf = `C:\Program Files`
		}
		return filepath.Join(pf, "ClaudeCode")
	case "darwin":
		return "/Library/Application Support/ClaudeCode"
	default:
		return "/etc/claude-code"
	}
}

// ManagedSources lists every managed source Claude Code would consult, in
// the order the docs give (server-managed settings are not visible locally).
func ManagedSources() []Source {
	var out []Source
	dir := SystemDir()

	if runtime.GOOS == "darwin" {
		out = append(out, plistSource("/Library/Managed Preferences/com.anthropic.claudecode.plist", "MDM profile (com.anthropic.claudecode)"))
	}
	if runtime.GOOS == "windows" {
		out = append(out, registrySource(`HKLM\SOFTWARE\Policies\ClaudeCode`, "HKLM registry policy"))
	}
	out = append(out, fileSource(filepath.Join(dir, "managed-settings.json"), "managed-settings.json"))
	if entries, err := os.ReadDir(filepath.Join(dir, "managed-settings.d")); err == nil {
		var names []string
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, n := range names {
			out = append(out, fileSource(filepath.Join(dir, "managed-settings.d", n), "managed-settings.d/"+n))
		}
	}
	if runtime.GOOS == "windows" {
		out = append(out, registrySource(`HKCU\SOFTWARE\Policies\ClaudeCode`, "HKCU registry policy (user-writable, lowest)"))
	}
	return out
}

func fileSource(path, name string) Source {
	s := Source{Name: name, Path: path}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s
	}
	s.Present = true
	if err != nil {
		s.Err = err.Error()
		return s
	}
	if strings.TrimSpace(string(b)) == "" {
		s.Readable = true
		s.Data = map[string]any{}
		return s
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		s.Err = "invalid JSON: " + err.Error()
		return s
	}
	s.Readable = true
	s.Data = m
	return s
}

var regLineRe = regexp.MustCompile(`(?m)^\s*Settings\s+REG_(?:EXPAND_)?SZ\s+(.*?)\s*$`)

func registrySource(key, name string) Source {
	s := Source{Name: name, Path: key}
	out, err := exec.Command("reg", "query", key, "/v", "Settings").CombinedOutput()
	if err != nil {
		// reg exits 1 when the key or value is absent
		if strings.Contains(strings.ToLower(string(out)), "unable to find") || strings.Contains(strings.ToLower(string(out)), "cannot find") {
			return s
		}
		if len(out) == 0 {
			s.Err = err.Error()
		}
		return s
	}
	m := regLineRe.FindStringSubmatch(string(out))
	if m == nil {
		return s
	}
	s.Present = true
	var data map[string]any
	if err := json.Unmarshal([]byte(m[1]), &data); err != nil {
		s.Err = "invalid JSON in registry value: " + err.Error()
		return s
	}
	s.Readable = true
	s.Data = data
	return s
}

func plistSource(path, name string) Source {
	s := Source{Name: name, Path: path}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return s
	}
	s.Present = true
	out, err := exec.Command("plutil", "-convert", "json", "-o", "-", path).Output()
	if err != nil {
		s.Err = "cannot convert plist: " + err.Error()
		return s
	}
	var data map[string]any
	if err := json.Unmarshal(out, &data); err != nil {
		s.Err = "invalid plist JSON: " + err.Error()
		return s
	}
	s.Readable = true
	s.Data = data
	return s
}

// Lookup finds a dotted key ("permissions.disableAutoMode") in one source.
func Lookup(data map[string]any, dotted string) (any, bool) {
	cur := any(data)
	for _, part := range strings.Split(dotted, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[part]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// Hit is a value found in a source.
type Hit struct {
	Source string
	Value  any
}

// Find returns every source that sets one of the dotted keys.
func Find(sources []Source, keys ...string) []Hit {
	var hits []Hit
	for _, s := range sources {
		if !s.Readable {
			continue
		}
		for _, k := range keys {
			if v, ok := Lookup(s.Data, k); ok {
				hits = append(hits, Hit{Source: s.Name, Value: v})
			}
		}
	}
	return hits
}

// StringList coerces a JSON array into strings.
func StringList(v any) []string {
	arr, _ := v.([]any)
	var out []string
	for _, x := range arr {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// IsTrue reports whether a JSON value is boolean true.
func IsTrue(v any) bool {
	b, ok := v.(bool)
	return ok && b
}
