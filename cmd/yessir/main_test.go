package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sandbox points YESSIR_HOME and CLAUDE_CONFIG_DIR at a temp dir so the CLI
// never touches the real ~/.yessir or ~/.claude.
func sandbox(t *testing.T) (home string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("YESSIR_HOME", filepath.Join(home, ".yessir"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("YESSIR_OFF", "")
	return home
}

func cli(t *testing.T, stdin string, args ...string) (code int, out, errOut string) {
	t.Helper()
	var o, e bytes.Buffer
	code = run(args, strings.NewReader(stdin), &o, &e)
	return code, o.String(), e.String()
}

func payload(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "permission-request", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func withCwd(t *testing.T, s string) string {
	// rewrite the sample cwd to a real temp repo path
	return strings.ReplaceAll(s, `"cwd":"/home/me/work/repo"`, `"cwd":`+jsonString(t.TempDir()))
}

func jsonString(s string) string {
	return `"` + strings.ReplaceAll(s, `\`, `\\`) + `"`
}

func TestLifecycle(t *testing.T) {
	home := sandbox(t)
	repo := filepath.Join(home, "work", "repoA")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(home, "work", "repoB")
	_ = os.MkdirAll(other, 0o755)
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	_ = os.MkdirAll(filepath.Dir(settingsPath), 0o755)
	_ = os.WriteFile(settingsPath, []byte(`{"model":"opus","permissions":{"ask":["Bash(gh pr create *)"]}}`), 0o644)

	// doctor before: hook not registered, but not fatal
	if code, out, _ := cli(t, "", "doctor"); code != exitOK || !strings.Contains(out, "hook not registered") {
		t.Fatalf("doctor: %d\n%s", code, out)
	}

	// on --here <repo>
	if code, out, errOut := cli(t, "", "on", "--here", repo); code != exitOK {
		t.Fatalf("on: %d %s %s", code, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(repo, ".claude", "yessir.local.json")); err != nil {
		t.Fatal("marker missing")
	}
	b, _ := os.ReadFile(settingsPath)
	if !strings.Contains(string(b), `"PermissionRequest"`) || !strings.Contains(string(b), `"model": "opus"`) {
		t.Fatalf("settings after on:\n%s", b)
	}
	ex, _ := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if !strings.Contains(string(ex), ".claude/yessir.local.json") {
		t.Fatal("marker not excluded from git")
	}

	in := func(name, cwd string) string {
		s := payload(t, name)
		s = strings.ReplaceAll(s, `"cwd":"/home/me/work/repo"`, `"cwd":`+jsonString(cwd))
		s = strings.ReplaceAll(s, `"cwd":"C:\\Users\\me\\work\\repo"`, `"cwd":`+jsonString(cwd))
		return s
	}

	// in scope → allow
	if _, out, _ := cli(t, in("bash.json", filepath.Join(repo, "src")), "hook"); !strings.Contains(out, `"behavior":"allow"`) {
		t.Fatalf("expected allow, got %q", out)
	}
	// out of scope → silent
	if _, out, _ := cli(t, in("bash.json", other), "hook"); out != "" {
		t.Fatalf("expected silence, got %q", out)
	}
	// ask rule → silent
	if _, out, _ := cli(t, in("force-push.json", repo), "hook"); out != "" {
		t.Fatalf("expected silence for force push, got %q", out)
	}
	// ExitPlanMode echoes updatedInput
	if _, out, _ := cli(t, in("exitplanmode.json", repo), "hook"); !strings.Contains(out, `"updatedInput":{"plan":`) {
		t.Fatalf("ExitPlanMode: %q", out)
	}
	// Windows-style payload
	if _, out, _ := cli(t, in("windows-edit.json", repo), "hook"); !strings.Contains(out, `"behavior":"allow"`) {
		t.Fatalf("windows edit: %q", out)
	}

	// global + rules
	if code, out, errOut := cli(t, "", "on", "--global", "--rules"); code != exitOK {
		t.Fatalf("on --global: %d %s %s", code, out, errOut)
	}
	b, _ = os.ReadFile(settingsPath)
	if !strings.Contains(string(b), `"Bash",`) || !strings.Contains(string(b), `"Agent"`) {
		t.Fatalf("rules not written:\n%s", b)
	}
	if _, out, _ := cli(t, in("edit.json", other), "hook"); !strings.Contains(out, `"behavior":"allow"`) {
		t.Fatalf("global should allow in repoB, got %q", out)
	}

	// pause + kill switch
	if code, _, _ := cli(t, "", "pause", "5m"); code != exitOK {
		t.Fatal("pause")
	}
	if _, out, _ := cli(t, in("edit.json", other), "hook"); out != "" {
		t.Fatalf("paused should be silent, got %q", out)
	}
	cli(t, "", "resume")
	t.Setenv("YESSIR_OFF", "1")
	if _, out, _ := cli(t, in("edit.json", other), "hook"); out != "" {
		t.Fatalf("kill switch should be silent, got %q", out)
	}
	t.Setenv("YESSIR_OFF", "")

	// status + log
	if code, out, _ := cli(t, "", "status"); code != exitOK || !strings.Contains(out, "global:   true") || !strings.Contains(out, "hook:     registered") {
		t.Fatalf("status: %d\n%s", code, out)
	}
	if code, out, _ := cli(t, "", "log", "-n", "3"); code != exitOK || !strings.Contains(out, "allow") {
		t.Fatalf("log: %d\n%s", code, out)
	}

	// off + uninstall leave the user's own settings intact
	if code, _, e := cli(t, "", "off", "--global"); code != exitOK {
		t.Fatal(e)
	}
	if code, _, e := cli(t, "", "off", "--here", repo); code != exitOK {
		t.Fatal(e)
	}
	if code, _, e := cli(t, "", "uninstall"); code != exitOK {
		t.Fatal(e)
	}
	b, _ = os.ReadFile(settingsPath)
	s := string(b)
	if strings.Contains(s, "yessir") || strings.Contains(s, `"Agent"`) || !strings.Contains(s, `"Bash(gh pr create *)"`) || !strings.Contains(s, `"model": "opus"`) {
		t.Fatalf("settings after uninstall:\n%s", s)
	}
	if _, err := os.Stat(filepath.Join(repo, ".claude", "yessir.local.json")); err == nil {
		t.Fatal("marker still present after uninstall")
	}
}

func TestOnRefusesUnparseableSettings(t *testing.T) {
	home := sandbox(t)
	p := filepath.Join(home, ".claude", "settings.json")
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, []byte(`{"hooks": [`), 0o644)
	code, _, errOut := cli(t, "", "on", "--global")
	if code != exitFiles || !strings.Contains(errOut, "never overwrites") {
		t.Fatalf("code=%d err=%s", code, errOut)
	}
	b, _ := os.ReadFile(p)
	if string(b) != `{"hooks": [` {
		t.Fatal("broken file was modified")
	}
}

func TestHookNeverFailsOnGarbage(t *testing.T) {
	sandbox(t)
	for _, in := range []string{"", "not json", "[]", `{"tool_name":"Bash"}`} {
		if code, out, _ := cli(t, in, "hook"); code != exitOK || out != "" {
			t.Fatalf("input %q: code=%d out=%q", in, code, out)
		}
	}
}

func TestUsage(t *testing.T) {
	sandbox(t)
	if code, _, _ := cli(t, ""); code != exitUsage {
		t.Fatal("no args must be a usage error")
	}
	if code, out, _ := cli(t, "", "version"); code != exitOK || !strings.HasPrefix(out, "yessir ") {
		t.Fatal(out)
	}
	if code, _, errOut := cli(t, "", "bogus"); code != exitUsage || !strings.Contains(errOut, "unknown command") {
		t.Fatal(errOut)
	}
	_ = withCwd
}
