package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `{
  "model": "opus",
  "hooks": {
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "/x/check.sh"}]}
    ],
    "PermissionRequest": [
      {"matcher": "ExitPlanMode", "hooks": [{"type": "command", "command": "echo '{}'"}]}
    ]
  },
  "permissions": {"allow": ["Bash(npm test)"], "deny": ["Read(./.env)"]},
  "statusLine": {"type": "command", "command": "x"}
}
`

func load(t *testing.T, content string) *File {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	if content != "" {
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestHookRoundTripKeepsOthers(t *testing.T) {
	f := load(t, sample)
	h := Handler{Type: "command", Command: `C:\Users\me\.yessir\bin\yessir.exe`, Args: []string{"hook"}, Timeout: 5}
	changed, err := f.EnsureHook("PermissionRequest", h)
	if err != nil || !changed {
		t.Fatalf("ensure: changed=%v err=%v", changed, err)
	}
	// idempotent
	changed, _ = f.EnsureHook("PermissionRequest", h)
	if changed {
		t.Fatal("second EnsureHook must be a no-op")
	}
	if err := f.Save(""); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(f.Path)
	s := string(b)
	for _, want := range []string{`"model": "opus"`, `"/x/check.sh"`, `"matcher": "ExitPlanMode"`, `yessir.exe`, `"args": [`, `"statusLine"`} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in\n%s", want, s)
		}
	}
	// key order preserved: model before hooks before permissions before statusLine
	if !(strings.Index(s, `"model"`) < strings.Index(s, `"hooks"`) && strings.Index(s, `"hooks"`) < strings.Index(s, `"permissions"`) && strings.Index(s, `"permissions"`) < strings.Index(s, `"statusLine"`)) {
		t.Fatalf("key order changed:\n%s", s)
	}
	ok, cmd := f.HasYessirHook("PermissionRequest")
	if !ok || cmd != h.Command {
		t.Fatalf("HasYessirHook = %v %q", ok, cmd)
	}

	// remove: only ours goes, the ExitPlanMode group stays
	f2, err := Load(f.Path)
	if err != nil {
		t.Fatal(err)
	}
	changed, err = f2.RemoveHook("PermissionRequest")
	if err != nil || !changed {
		t.Fatalf("remove: changed=%v err=%v", changed, err)
	}
	if err := f2.Save(""); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(f.Path)
	s = string(b)
	if strings.Contains(s, "yessir") || !strings.Contains(s, `"matcher": "ExitPlanMode"`) || !strings.Contains(s, `/x/check.sh`) {
		t.Fatalf("remove damaged file:\n%s", s)
	}
}

func TestEmptyAndMissingFile(t *testing.T) {
	f := load(t, "")
	if f.Existed {
		t.Fatal("missing file must not be Existed")
	}
	changed, err := f.EnsureHook("PermissionRequest", Handler{Type: "command", Command: "/usr/local/bin/yessir", Args: []string{"hook"}, Timeout: 5})
	if err != nil || !changed {
		t.Fatal(err)
	}
	if err := f.Save(""); err != nil {
		t.Fatal(err)
	}
	g, err := Load(f.Path)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := g.HasYessirHook("PermissionRequest"); !ok {
		t.Fatal("hook not found after save")
	}
	// removing the only hook leaves no hooks key at all
	if _, err := g.RemoveHook("PermissionRequest"); err != nil {
		t.Fatal(err)
	}
	b, _ := g.Bytes()
	if strings.TrimSpace(string(b)) != "{\n}" {
		t.Fatalf("expected empty object, got %q", b)
	}
}

func TestRules(t *testing.T) {
	f := load(t, sample)
	added, err := f.AddRules("allow", []string{"Bash(npm test)", "Edit", "Write"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(added, ",") != "Edit,Write" {
		t.Fatalf("added = %v", added)
	}
	if got := f.Rules("allow"); strings.Join(got, ",") != "Bash(npm test),Edit,Write" {
		t.Fatalf("allow = %v", got)
	}
	if _, err := f.RemoveRules("allow", added); err != nil {
		t.Fatal(err)
	}
	if got := f.Rules("allow"); strings.Join(got, ",") != "Bash(npm test)" {
		t.Fatalf("after remove allow = %v", got)
	}
	if got := f.Rules("deny"); strings.Join(got, ",") != "Read(./.env)" {
		t.Fatalf("deny = %v", got)
	}
}

func TestBrokenFileIsAnError(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	_ = os.WriteFile(p, []byte(`{"hooks": [`), 0o644)
	if _, err := Load(p); err == nil {
		t.Fatal("expected parse error")
	}
}
