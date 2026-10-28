// yessir answers Claude Code permission prompts with "yes", globally or per
// folder, through the official PermissionRequest hook. See SPEC.md.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/rlgdev/yessir/internal/config"
	"github.com/rlgdev/yessir/internal/doctor"
	"github.com/rlgdev/yessir/internal/hook"
	"github.com/rlgdev/yessir/internal/logx"
	"github.com/rlgdev/yessir/internal/settings"
)

// version is set by the release build (-ldflags "-X main.version=...").
var version = "dev"

const (
	exitOK     = 0
	exitPolicy = 1
	exitUsage  = 2
	exitFiles  = 3
)

var allowRules = []string{"Bash", "Edit", "Write", "NotebookEdit", "WebFetch", "WebSearch", "Agent"}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stdout)
		return exitUsage
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "hook":
		return cmdHook(stdin, stdout, stderr)
	case "on":
		return cmdOn(rest, stdout, stderr)
	case "off":
		return cmdOff(rest, stdout, stderr)
	case "pause":
		return cmdPause(rest, stdout, stderr)
	case "resume":
		if err := config.Resume(); err != nil {
			fmt.Fprintln(stderr, "yessir:", err)
			return exitFiles
		}
		fmt.Fprintln(stdout, "resumed")
		return exitOK
	case "status":
		return cmdStatus(stdout, stderr)
	case "doctor":
		return cmdDoctor(stdout, stderr)
	case "log":
		return cmdLog(rest, stdout, stderr)
	case "uninstall":
		return cmdUninstall(stdout, stderr)
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "yessir %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
		return exitOK
	case "help", "-h", "--help":
		usage(stdout)
		return exitOK
	default:
		fmt.Fprintf(stderr, "yessir: unknown command %q\n\n", cmd)
		usage(stderr)
		return exitUsage
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `yessir — answer Claude Code permission prompts with "yes"

  yessir on   [--global | --here [<dir>]] [--rules] [--learn]   enable (default: this repo)
  yessir off  [--global | --here [<dir>]]                        disable
  yessir pause [<duration>]                                      stay silent for a while (default 30m)
  yessir resume                                                  end a pause early
  yessir status                                                  what is enabled where
  yessir doctor                                                  can it work here? what will still prompt?
  yessir log  [-n N] [--follow] [--json]                         decision log
  yessir uninstall                                               remove everything yessir wrote
  yessir version

Kill switch: set YESSIR_OFF=1 in the environment of the claude process.
Files: ~/.yessir/config.json (guardrails), <repo>/.claude/yessir.local.json (folder marker).
`)
}

// ---------------------------------------------------------------- hook

func cmdHook(stdin io.Reader, stdout, stderr io.Writer) int {
	start := time.Now()
	raw, err := io.ReadAll(stdin)
	if err != nil {
		return exitOK
	}
	var in hook.Input
	if err := json.Unmarshal(raw, &in); err != nil {
		fmt.Fprintln(stderr, "yessir: cannot parse hook input:", err)
		return exitOK
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(stderr, "yessir: config:", err)
		return exitOK
	}
	p := hook.Policy{
		KillSwitch:   os.Getenv("YESSIR_OFF") == "1",
		Paused:       config.IsPaused(time.Now()),
		Global:       cfg.Global,
		NeverApprove: cfg.NeverApprove,
		Ask:          append([]string{}, cfg.Ask...),
		Deny:         append([]string{}, cfg.Deny...),
		DenyMessage:  cfg.DenyMessage,
		Learn:        cfg.Learn,
	}
	cwd := in.CWD
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	if root, m, err := config.FindMarker(cwd); err == nil && m != nil {
		p.MarkerRoot = root
		p.MarkerEnabled = m.Enabled
		p.Ask = append(p.Ask, m.Ask...)
		p.Deny = append(p.Deny, m.Deny...)
	}
	d := hook.Decide(in, p)
	out := hook.Output(in, d, p)
	if out != nil {
		fmt.Fprintln(stdout, string(out))
	}
	if cfg.Log && d.Scope != "" {
		_ = logx.Append(filepath.Join(config.Dir(), config.LogName), logx.Entry{
			TS:       logx.Now(),
			Session:  in.SessionID,
			CWD:      cwd,
			Scope:    d.Scope,
			Tool:     in.ToolName,
			Summary:  hook.Summary(in),
			Decision: d.Outcome,
			Reason:   d.Reason,
			Ms:       time.Since(start).Milliseconds(),
		})
	}
	return exitOK
}

// ---------------------------------------------------------------- scope flags

type scope struct {
	global bool
	root   string // folder root when !global
}

func parseScope(fs *flag.FlagSet, args []string, stderr io.Writer) (scope, []string, bool) {
	global := fs.Bool("global", false, "all sessions, everywhere")
	here := fs.Bool("here", false, "this repository (or <dir>) only")
	if err := fs.Parse(args); err != nil {
		return scope{}, nil, false
	}
	if *global && *here {
		fmt.Fprintln(stderr, "yessir: choose --global or --here, not both")
		return scope{}, nil, false
	}
	if *global {
		return scope{global: true}, fs.Args(), true
	}
	dir := ""
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
	}
	if dir == "" {
		dir, _ = os.Getwd()
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		fmt.Fprintln(stderr, "yessir:", err)
		return scope{}, nil, false
	}
	root := config.GitRoot(abs)
	if root == "" {
		root = abs
	}
	return scope{root: root}, fs.Args(), true
}

func hookCommand() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return filepath.Abs(exe)
}

func backupDir() string { return filepath.Join(config.Dir(), "backup") }

// ---------------------------------------------------------------- on

func cmdOn(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("on", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rules := fs.Bool("rules", false, "also write bare-tool allow rules (fast path)")
	learn := fs.Bool("learn", false, "persist Claude's own 'don't ask again' suggestion on each approval")
	force := fs.Bool("force", false, "proceed even when doctor says hooks are blocked")
	sc, _, ok := parseScope(fs, args, stderr)
	if !ok {
		return exitUsage
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(stderr, "yessir: config:", err)
		return exitFiles
	}
	state, err := config.LoadState()
	if err != nil {
		fmt.Fprintln(stderr, "yessir: state:", err)
		return exitFiles
	}
	user, err := settings.Load(settings.UserSettingsPath())
	if err != nil {
		fmt.Fprintln(stderr, "yessir: cannot read", err, "— fix the JSON first, yessir never overwrites a file it cannot parse")
		return exitFiles
	}
	cmd, err := hookCommand()
	if err != nil {
		fmt.Fprintln(stderr, "yessir:", err)
		return exitFiles
	}

	rep := doctor.Run(user, sc.root, cmd)
	if rep.HooksBlocked && !*force {
		fmt.Fprintln(stderr, "yessir: your organisation blocks user hooks on this machine; `yessir on` would do nothing. Details:")
		rep.Print(stderr)
		return exitPolicy
	}

	// 1. hook registration (global, once)
	changed, err := user.EnsureHook(hook.EventName, settings.Handler{Type: "command", Command: cmd, Args: []string{"hook"}, Timeout: 5})
	if err != nil {
		fmt.Fprintln(stderr, "yessir:", err)
		return exitFiles
	}
	if changed {
		if err := user.Save(backupDir()); err != nil {
			fmt.Fprintln(stderr, "yessir: cannot write", user.Path, "—", err)
			return exitFiles
		}
		fmt.Fprintln(stdout, "hook registered in", user.Path)
	}
	state.HookCommand = cmd
	state.HookSettingsFile = user.Path
	if state.HookRegisteredAt == "" {
		state.HookRegisteredAt = time.Now().UTC().Format(time.RFC3339)
	}

	// 2. scope
	if sc.global {
		cfg.Global = true
		fmt.Fprintln(stdout, "enabled globally")
	} else {
		mk := config.Marker{Enabled: true}
		if _, existing, err := config.FindMarker(sc.root); err == nil && existing != nil {
			mk.Ask, mk.Deny = existing.Ask, existing.Deny
		}
		if err := config.WriteMarker(sc.root, mk); err != nil {
			fmt.Fprintln(stderr, "yessir:", err)
			return exitFiles
		}
		_ = config.ExcludeFromGit(sc.root, config.MarkerRel)
		state.AddMarker(config.MarkerPath(sc.root))
		fmt.Fprintln(stdout, "enabled for", sc.root, "(marker:", config.MarkerPath(sc.root)+")")
	}
	if *learn {
		cfg.Learn = true
		fmt.Fprintln(stdout, "learn mode on: each approval also saves Claude's suggested 'don't ask again' rule")
	}
	if err := cfg.Save(); err != nil {
		fmt.Fprintln(stderr, "yessir:", err)
		return exitFiles
	}

	// 3. optional allow-rule fast path
	if *rules {
		if rep.RulesBlocked {
			fmt.Fprintln(stderr, "yessir: --rules skipped: allowManagedPermissionRulesOnly is enforced; the hook path still works")
		} else {
			target := user
			if !sc.global {
				target, err = settings.Load(settings.LocalSettingsPath(sc.root))
				if err != nil {
					fmt.Fprintln(stderr, "yessir:", err)
					return exitFiles
				}
			}
			added, err := target.AddRules("allow", allowRules)
			if err != nil {
				fmt.Fprintln(stderr, "yessir:", err)
				return exitFiles
			}
			denied, err := target.AddRules("deny", cfg.Deny)
			if err != nil {
				fmt.Fprintln(stderr, "yessir:", err)
				return exitFiles
			}
			if len(added) > 0 || len(denied) > 0 {
				if err := target.Save(backupDir()); err != nil {
					fmt.Fprintln(stderr, "yessir: cannot write", target.Path, "—", err)
					return exitFiles
				}
			}
			state.AllowRules[target.Path] = union(state.AllowRules[target.Path], added)
			state.DenyRules[target.Path] = union(state.DenyRules[target.Path], denied)
			fmt.Fprintf(stdout, "allow rules written to %s: %s\n", target.Path, strings.Join(allowRules, ", "))
		}
	}
	if err := state.Save(); err != nil {
		fmt.Fprintln(stderr, "yessir:", err)
		return exitFiles
	}
	if len(rep.AskRules) > 0 {
		fmt.Fprintf(stdout, "note: %d ask rule(s) in your settings/policy will still prompt (see `yessir doctor`)\n", len(rep.AskRules))
	}
	fmt.Fprintln(stdout, "done — start a new claude session for the hook to load")
	return exitOK
}

func union(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range append(append([]string{}, a...), b...) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// ---------------------------------------------------------------- off

func cmdOff(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("off", flag.ContinueOnError)
	fs.SetOutput(stderr)
	sc, _, ok := parseScope(fs, args, stderr)
	if !ok {
		return exitUsage
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(stderr, "yessir:", err)
		return exitFiles
	}
	state, err := config.LoadState()
	if err != nil {
		fmt.Fprintln(stderr, "yessir:", err)
		return exitFiles
	}
	var target string
	if sc.global {
		cfg.Global = false
		if err := cfg.Save(); err != nil {
			fmt.Fprintln(stderr, "yessir:", err)
			return exitFiles
		}
		target = settings.UserSettingsPath()
		fmt.Fprintln(stdout, "disabled globally (hook stays registered but silent; `yessir uninstall` removes it)")
	} else {
		if err := config.RemoveMarkerFile(sc.root); err != nil {
			fmt.Fprintln(stderr, "yessir:", err)
			return exitFiles
		}
		state.RemoveMarker(config.MarkerPath(sc.root))
		target = settings.LocalSettingsPath(sc.root)
		fmt.Fprintln(stdout, "disabled for", sc.root)
	}
	if code := removeRules(state, target, stdout, stderr); code != exitOK {
		return code
	}
	if err := state.Save(); err != nil {
		fmt.Fprintln(stderr, "yessir:", err)
		return exitFiles
	}
	return exitOK
}

func removeRules(state *config.State, path string, stdout, stderr io.Writer) int {
	a, d := state.AllowRules[path], state.DenyRules[path]
	if len(a) == 0 && len(d) == 0 {
		return exitOK
	}
	f, err := settings.Load(path)
	if err != nil {
		fmt.Fprintln(stderr, "yessir:", err)
		return exitFiles
	}
	c1, err := f.RemoveRules("allow", a)
	if err != nil {
		fmt.Fprintln(stderr, "yessir:", err)
		return exitFiles
	}
	c2, err := f.RemoveRules("deny", d)
	if err != nil {
		fmt.Fprintln(stderr, "yessir:", err)
		return exitFiles
	}
	if c1 || c2 {
		if err := f.Save(backupDir()); err != nil {
			fmt.Fprintln(stderr, "yessir:", err)
			return exitFiles
		}
		fmt.Fprintln(stdout, "removed yessir rules from", path)
	}
	delete(state.AllowRules, path)
	delete(state.DenyRules, path)
	return exitOK
}

// ---------------------------------------------------------------- pause

func cmdPause(args []string, stdout, stderr io.Writer) int {
	d := 30 * time.Minute
	if len(args) > 0 {
		var err error
		d, err = time.ParseDuration(args[0])
		if err != nil {
			fmt.Fprintln(stderr, "yessir: bad duration (examples: 30m, 2h):", err)
			return exitUsage
		}
	}
	if d <= 0 {
		if err := config.Resume(); err != nil {
			fmt.Fprintln(stderr, "yessir:", err)
			return exitFiles
		}
		fmt.Fprintln(stdout, "resumed")
		return exitOK
	}
	until := time.Now().Add(d)
	if err := config.Pause(until); err != nil {
		fmt.Fprintln(stderr, "yessir:", err)
		return exitFiles
	}
	fmt.Fprintln(stdout, "paused until", until.Local().Format("2006-01-02 15:04"))
	return exitOK
}

// ---------------------------------------------------------------- status

func cmdStatus(stdout, stderr io.Writer) int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(stderr, "yessir:", err)
		return exitFiles
	}
	cwd, _ := os.Getwd()
	user, uerr := settings.Load(settings.UserSettingsPath())
	fmt.Fprintf(stdout, "yessir %s\n", version)
	fmt.Fprintf(stdout, "global:   %v\n", cfg.Global)
	if root, m, err := config.FindMarker(cwd); err == nil && m != nil {
		fmt.Fprintf(stdout, "folder:   %s (enabled=%v)\n", root, m.Enabled)
	} else {
		fmt.Fprintf(stdout, "folder:   no marker above %s\n", cwd)
	}
	if u := config.PausedUntil(); !u.IsZero() && time.Now().Before(u) {
		fmt.Fprintf(stdout, "paused:   until %s\n", u.Local().Format("2006-01-02 15:04"))
	}
	if os.Getenv("YESSIR_OFF") == "1" {
		fmt.Fprintln(stdout, "kill switch: YESSIR_OFF=1 is set in this shell")
	}
	if uerr != nil {
		fmt.Fprintf(stdout, "hook:     cannot read %s: %v\n", settings.UserSettingsPath(), uerr)
	} else if ok, cmd := user.HasYessirHook(hook.EventName); ok {
		fmt.Fprintf(stdout, "hook:     registered → %s\n", cmd)
	} else {
		fmt.Fprintln(stdout, "hook:     not registered (run `yessir on`)")
	}
	fmt.Fprintf(stdout, "learn:    %v\n", cfg.Learn)
	fmt.Fprintf(stdout, "ask:      %s\n", strings.Join(cfg.Ask, ", "))
	fmt.Fprintf(stdout, "deny:     %s\n", strings.Join(cfg.Deny, ", "))
	fmt.Fprintf(stdout, "config:   %s\n", filepath.Join(config.Dir(), "config.json"))
	entries, _ := logx.Tail(filepath.Join(config.Dir(), config.LogName), 5)
	if len(entries) > 0 {
		fmt.Fprintln(stdout, "last decisions:")
		for _, e := range entries {
			fmt.Fprintf(stdout, "  %s  %-6s %-14s %s\n", e.TS[:19], e.Decision, e.Tool, e.Summary)
		}
	}
	return exitOK
}

// ---------------------------------------------------------------- doctor

func cmdDoctor(stdout, stderr io.Writer) int {
	user, err := settings.Load(settings.UserSettingsPath())
	if err != nil {
		fmt.Fprintln(stderr, "yessir: cannot parse", err)
		return exitFiles
	}
	cwd, _ := os.Getwd()
	root := config.GitRoot(cwd)
	cmd, _ := hookCommand()
	rep := doctor.Run(user, root, cmd)
	rep.Print(stdout)
	if rep.Fatal() {
		return exitPolicy
	}
	return exitOK
}

// ---------------------------------------------------------------- log

func cmdLog(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("log", flag.ContinueOnError)
	fs.SetOutput(stderr)
	n := fs.Int("n", 50, "number of entries")
	follow := fs.Bool("follow", false, "keep printing new entries")
	asJSON := fs.Bool("json", false, "raw JSON lines")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	path := filepath.Join(config.Dir(), config.LogName)
	print := func(e logx.Entry) {
		if *asJSON {
			b, _ := json.Marshal(e)
			fmt.Fprintln(stdout, string(b))
			return
		}
		fmt.Fprintf(stdout, "%s  %-6s %-14s %-22s %s\n", e.TS[:19], e.Decision, e.Tool, e.Scope, e.Summary)
	}
	entries, err := logx.Tail(path, *n)
	if err != nil {
		fmt.Fprintln(stderr, "yessir:", err)
		return exitFiles
	}
	for _, e := range entries {
		print(e)
	}
	if !*follow {
		return exitOK
	}
	last := len(entries)
	seenTS := ""
	if last > 0 {
		seenTS = entries[last-1].TS
	}
	for {
		time.Sleep(time.Second)
		all, err := logx.Tail(path, 200)
		if err != nil {
			continue
		}
		idx := 0
		for i, e := range all {
			if e.TS == seenTS {
				idx = i + 1
			}
		}
		for _, e := range all[idx:] {
			print(e)
			seenTS = e.TS
		}
	}
}

// ---------------------------------------------------------------- uninstall

func cmdUninstall(stdout, stderr io.Writer) int {
	state, err := config.LoadState()
	if err != nil {
		fmt.Fprintln(stderr, "yessir:", err)
		return exitFiles
	}
	user, err := settings.Load(settings.UserSettingsPath())
	if err != nil {
		fmt.Fprintln(stderr, "yessir:", err)
		return exitFiles
	}
	if changed, err := user.RemoveHook(hook.EventName); err != nil {
		fmt.Fprintln(stderr, "yessir:", err)
		return exitFiles
	} else if changed {
		if err := user.Save(backupDir()); err != nil {
			fmt.Fprintln(stderr, "yessir:", err)
			return exitFiles
		}
		fmt.Fprintln(stdout, "hook removed from", user.Path)
	}
	for _, p := range keys(state.AllowRules, state.DenyRules) {
		if code := removeRules(state, p, stdout, stderr); code != exitOK {
			return code
		}
	}
	for _, m := range append([]string{}, state.Markers...) {
		if err := os.Remove(m); err == nil || errors.Is(err, os.ErrNotExist) {
			state.RemoveMarker(m)
			fmt.Fprintln(stdout, "marker removed:", m)
		} else {
			fmt.Fprintln(stderr, "yessir: cannot remove", m, "—", err)
		}
	}
	state.HookCommand, state.HookRegisteredAt, state.HookSettingsFile = "", "", ""
	if err := state.Save(); err != nil {
		fmt.Fprintln(stderr, "yessir:", err)
		return exitFiles
	}
	if cfg, err := config.Load(); err == nil && cfg.Global {
		cfg.Global = false
		_ = cfg.Save()
	}
	_ = config.Resume()
	fmt.Fprintln(stdout, "uninstalled; ~/.yessir (config, log, backups) kept — delete it by hand if you want")
	return exitOK
}

func keys(ms ...map[string][]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range ms {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	return out
}
