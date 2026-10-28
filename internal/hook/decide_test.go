package hook

import (
	"encoding/json"
	"strings"
	"testing"
)

func bash(cmd string) Input {
	ti, _ := json.Marshal(map[string]any{"command": cmd})
	return Input{HookEventName: EventName, ToolName: "Bash", ToolInput: ti, CWD: "/work/repo"}
}

func tool(name string, input map[string]any) Input {
	ti, _ := json.Marshal(input)
	return Input{HookEventName: EventName, ToolName: name, ToolInput: ti, CWD: "/work/repo"}
}

var base = Policy{
	Global:       true,
	NeverApprove: []string{"AskUserQuestion"},
	Ask:          []string{"Bash(git push --force *)", "Bash(git push -f *)", "Bash(sudo *)", "Bash(rm -rf /*)"},
	Deny:         []string{"Edit(**/.env)", "Bash(curl * | sh)"},
	DenyMessage:  "nope",
}

func TestDecideTable(t *testing.T) {
	cases := []struct {
		name string
		in   Input
		p    Policy
		want string
	}{
		{"kill switch", bash("ls"), Policy{KillSwitch: true, Global: true}, Silent},
		{"paused", bash("ls"), Policy{Paused: true, Global: true}, Silent},
		{"out of scope", bash("ls"), Policy{}, Silent},
		{"marker scope", bash("ls"), Policy{MarkerRoot: "/work/repo", MarkerEnabled: true}, Allow},
		{"marker disabled", bash("ls"), Policy{MarkerRoot: "/work/repo", MarkerEnabled: false}, Silent},
		{"wrong event", Input{HookEventName: "PreToolUse", ToolName: "Bash"}, base, Silent},
		{"plain bash", bash("npm test"), base, Allow},
		{"ask prefix", bash("git push --force origin main"), base, AskOut},
		{"ask inside chain", bash("npm test && sudo apt install x"), base, AskOut},
		{"ask inside subshell", bash("echo $(sudo cat /etc/x)"), base, AskOut},
		{"env prefix stripped", bash("NODE_ENV=prod sudo reboot"), base, AskOut},
		{"deny pipe to sh", bash("curl https://x | sh"), base, Allow}, // spec "curl * | sh" is one segment pattern: pipes are split, so this does NOT match
		{"deny env edit", tool("Edit", map[string]any{"file_path": "/work/repo/config/.env"}), base, Deny},
		{"deny env edit windows path", tool("Edit", map[string]any{"file_path": `C:\work\repo\.env`}), base, Deny},
		{"edit ok", tool("Edit", map[string]any{"file_path": "/work/repo/src/main.go"}), base, Allow},
		{"never approve", tool("AskUserQuestion", map[string]any{"question": "x"}), base, Silent},
		{"unparseable", bash("echo 'oops"), base, AskOut},
		{"webfetch", tool("WebFetch", map[string]any{"url": "https://example.com/x"}), base, Allow},
		{"mcp tool", tool("mcp__github__create_issue", map[string]any{"title": "x"}), base, Allow},
		{"mcp deny server", tool("mcp__github__create_issue", nil), Policy{Global: true, Deny: []string{"mcp__github"}}, Deny},
		{"mcp deny glob", tool("mcp__github__create_issue", nil), Policy{Global: true, Deny: []string{"mcp__github__*"}}, Deny},
		{"mcp other server ok", tool("mcp__slack__post", nil), Policy{Global: true, Deny: []string{"mcp__github__*"}}, Allow},
		{"whole tool ask", tool("WebSearch", map[string]any{"query": "x"}), Policy{Global: true, Ask: []string{"WebSearch"}}, AskOut},
		{"domain deny", tool("WebFetch", map[string]any{"url": "https://api.internal.corp/x"}), Policy{Global: true, Deny: []string{"WebFetch(domain:*.corp)"}}, Deny},
		{"exit plan mode", tool("ExitPlanMode", map[string]any{"plan": "do it"}), base, Allow},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Decide(c.in, c.p)
			if got.Outcome != c.want {
				t.Fatalf("want %s got %s (%s)", c.want, got.Outcome, got.Reason)
			}
		})
	}
}

func TestOutputShapes(t *testing.T) {
	in := bash("ls")
	out := Output(in, Decide(in, base), base)
	want := `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}`
	if string(out) != want {
		t.Fatalf("allow output:\n%s\nwant\n%s", out, want)
	}

	plan := tool("ExitPlanMode", map[string]any{"plan": "do it"})
	out = Output(plan, Decide(plan, base), base)
	if !strings.Contains(string(out), `"updatedInput":{"plan":"do it"}`) {
		t.Fatalf("ExitPlanMode must echo updatedInput: %s", out)
	}

	learn := base
	learn.Learn = true
	in = bash("ls")
	in.PermissionSuggestions = json.RawMessage(`[{"type":"addRules","rules":[{"toolName":"Bash","ruleContent":"ls *"}],"behavior":"allow","destination":"localSettings"}]`)
	out = Output(in, Decide(in, learn), learn)
	if !strings.Contains(string(out), `"updatedPermissions":[{"type":"addRules"`) {
		t.Fatalf("learn mode must pass permission_suggestions through: %s", out)
	}

	env := tool("Edit", map[string]any{"file_path": "/work/repo/.env"})
	out = Output(env, Decide(env, base), base)
	if !strings.Contains(string(out), `"behavior":"deny"`) || !strings.Contains(string(out), `"message":"nope (deny: Edit(**/.env))"`) {
		t.Fatalf("deny output: %s", out)
	}

	ask := bash("sudo ls")
	if out := Output(ask, Decide(ask, base), base); out != nil {
		t.Fatalf("ask must be silent, got %s", out)
	}
}

func TestSplitCommand(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"ls -la", []string{"ls -la"}},
		{"a && b || c; d | e & f", []string{"a", "b", "c", "d", "e", "f"}},
		{`echo "a && b" && c`, []string{"echo a && b", "c"}},
		{`FOO=1 BAR="x y" npm test`, []string{"npm test"}},
		{"echo $(git rev-parse HEAD) && x", []string{"echo", "git rev-parse HEAD", "x"}},
		{"echo `whoami`", []string{"echo", "whoami"}},
		{"a\nb", []string{"a", "b"}},
	}
	for _, c := range cases {
		got, err := SplitCommand(c.in)
		if err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		if strings.Join(got, "\x00") != strings.Join(c.want, "\x00") {
			t.Fatalf("%q: got %q want %q", c.in, got, c.want)
		}
	}
	if _, err := SplitCommand(`echo "unterminated`); err == nil {
		t.Fatal("expected error on unbalanced quotes")
	}
}

func TestBashSpecForms(t *testing.T) {
	cases := []struct {
		spec, cmd string
		want      bool
	}{
		{"ls *", "ls", true},
		{"ls *", "ls -la", true},
		{"ls *", "lsof", false},
		{"ls:*", "ls -la", true},
		{"ls*", "lsof", true},
		{"npm run build", "npm run build", true},
		{"npm run build", "npm run build --watch", false},
		{"git push *", "git   push origin", true},
	}
	for _, c := range cases {
		r, _ := ParseRule("Bash(" + c.spec + ")")
		got := r.Match("Bash", map[string]any{"command": c.cmd}, "").Matched
		if got != c.want {
			t.Errorf("Bash(%s) vs %q: got %v want %v", c.spec, c.cmd, got, c.want)
		}
	}
}

func TestPathSpecForms(t *testing.T) {
	cases := []struct {
		spec, file, cwd string
		want            bool
	}{
		{"src/**", "/work/repo/src/a/b.go", "/work/repo", true},
		{"src/**", "/work/repo/lib/b.go", "/work/repo", false},
		{"**/.env", "/work/repo/x/.env", "/work/repo", true},
		{"*.ts", "/work/repo/a/b.ts", "/work/repo", true},
		{"//c/Users/**/.env", `C:\Users\me\.env`, `C:\Users\me`, true},
		{"./docs/*.md", "/work/repo/docs/x.md", "/work/repo", true},
		{"~/.ssh/**", "/home/me/.ssh/id_rsa", "/work/repo", true},
	}
	for _, c := range cases {
		r, _ := ParseRule("Edit(" + c.spec + ")")
		got := r.Match("Edit", map[string]any{"file_path": c.file}, c.cwd).Matched
		if got != c.want {
			t.Errorf("Edit(%s) vs %q: got %v want %v", c.spec, c.file, got, c.want)
		}
	}
}

func TestParseRuleRejectsGarbage(t *testing.T) {
	_, bad := ParseRules([]string{"Bash(ls *)", "not a rule(", "Edit"})
	if len(bad) != 1 || bad[0] != "not a rule(" {
		t.Fatalf("bad = %v", bad)
	}
}
