// Package doctor checks whether yessir can work on this machine and what will
// still prompt despite it.
package doctor

import (
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/rlgdev/yessir/internal/policy"
	"github.com/rlgdev/yessir/internal/settings"
)

// Check is one row of the report.
type Check struct {
	OK     bool
	Fatal  bool // a failing fatal check means yessir cannot work
	Name   string
	Detail string
}

// Report is the doctor result.
type Report struct {
	Checks       []Check
	HooksBlocked bool // disableAllHooks / allowManagedHooksOnly
	RulesBlocked bool // allowManagedPermissionRulesOnly
	AskRules     []string
	DenyRules    []string
	Version      string
	Sources      []policy.Source
}

// MinVersion is the first Claude Code release with the PermissionRequest event.
const MinVersion = "2.0.45"

// RecommendedVersion is where updatedInput/deny re-checking became reliable.
const RecommendedVersion = "2.1.110"

// Run performs every check. root may be "" when not inside a project.
func Run(user *settings.File, root string, hookExpectedCmd string) *Report {
	r := &Report{}
	sources := policy.ManagedSources()
	r.Sources = sources

	// --- managed sources overview
	present := 0
	for _, s := range sources {
		if !s.Present {
			continue
		}
		present++
		if s.Readable {
			r.add(true, false, "managed source: "+s.Name, s.Path)
		} else {
			r.add(false, false, "managed source unreadable: "+s.Name, s.Path+" — "+s.Err+" (assuming it is enforced; confirm with /status inside Claude Code)")
		}
	}
	if present == 0 {
		r.add(true, false, "no managed policy found on this machine", "server-managed settings (claude.ai admin console) are not visible locally; /status in Claude Code shows them")
	}

	// --- hooks enabled?
	hooksBlocked := false
	for _, h := range policy.Find(sources, "disableAllHooks") {
		if policy.IsTrue(h.Value) {
			hooksBlocked = true
			r.add(false, true, "disableAllHooks is true", "set by "+h.Source+" — no user hook can run, yessir cannot answer prompts here")
		}
	}
	for _, h := range policy.Find(sources, "allowManagedHooksOnly") {
		if policy.IsTrue(h.Value) {
			hooksBlocked = true
			r.add(false, true, "allowManagedHooksOnly is true", "set by "+h.Source+" — only managed hooks run, yessir cannot answer prompts here")
		}
	}
	if v, ok := user.TopBool("disableAllHooks"); ok && v {
		hooksBlocked = true
		r.add(false, true, "disableAllHooks is true in your user settings", user.Path+" — remove it or yessir cannot run")
	}
	if !hooksBlocked {
		r.add(true, true, "hooks are allowed", "no disableAllHooks / allowManagedHooksOnly in force")
	}
	r.HooksBlocked = hooksBlocked

	// --- rules allowed?
	for _, h := range policy.Find(sources, "allowManagedPermissionRulesOnly") {
		if policy.IsTrue(h.Value) {
			r.RulesBlocked = true
			r.add(false, false, "allowManagedPermissionRulesOnly is true", "set by "+h.Source+" — the --rules fast path is unavailable; the hook path still works")
		}
	}
	if !r.RulesBlocked {
		r.add(true, false, "user/project permission rules are honoured", "--rules fast path available")
	}

	// --- informational: modes disabled
	for _, h := range policy.Find(sources, "permissions.disableAutoMode", "disableAutoMode") {
		r.add(true, false, "auto mode disabled by policy", fmt.Sprintf("%s sets %v — this is the situation yessir is for", h.Source, h.Value))
	}
	for _, h := range policy.Find(sources, "permissions.disableBypassPermissionsMode") {
		r.add(true, false, "bypassPermissions disabled by policy", fmt.Sprintf("%s sets %v — yessir does not need it", h.Source, h.Value))
	}

	// --- ask / deny rules that will still apply
	var asks, denies []string
	for _, h := range policy.Find(sources, "permissions.ask") {
		for _, s := range policy.StringList(h.Value) {
			asks = append(asks, s+"  ["+h.Source+"]")
		}
	}
	for _, h := range policy.Find(sources, "permissions.deny") {
		for _, s := range policy.StringList(h.Value) {
			denies = append(denies, s+"  ["+h.Source+"]")
		}
	}
	for _, s := range user.Rules("ask") {
		asks = append(asks, s+"  [user settings]")
	}
	for _, s := range user.Rules("deny") {
		denies = append(denies, s+"  [user settings]")
	}
	if root != "" {
		for _, p := range []string{settings.ProjectSettingsPath(root), settings.LocalSettingsPath(root)} {
			f, err := settings.Load(p)
			if err != nil {
				r.add(false, false, "cannot parse "+p, err.Error())
				continue
			}
			for _, s := range f.Rules("ask") {
				asks = append(asks, s+"  ["+p+"]")
			}
			for _, s := range f.Rules("deny") {
				denies = append(denies, s+"  ["+p+"]")
			}
		}
	}
	r.AskRules, r.DenyRules = asks, denies
	if len(asks) > 0 {
		r.add(true, false, fmt.Sprintf("%d ask rule(s) will still prompt", len(asks)), strings.Join(asks, "; "))
	}
	if len(denies) > 0 {
		r.add(true, false, fmt.Sprintf("%d deny rule(s) will still be refused", len(denies)), strings.Join(denies, "; "))
	}

	// --- hook registration health
	if ok, cmd := user.HasYessirHook("PermissionRequest"); ok {
		if hookExpectedCmd != "" && !samePath(cmd, hookExpectedCmd) {
			r.add(false, false, "hook registered but points at another binary", cmd+" (this binary: "+hookExpectedCmd+") — run `yessir on` to update")
		} else {
			r.add(true, false, "hook registered in "+user.Path, cmd)
		}
	} else {
		r.add(false, false, "hook not registered", "run `yessir on` (or `yessir on --global`)")
	}

	// --- Claude Code version
	v := claudeVersion()
	r.Version = v
	switch {
	case v == "":
		r.add(false, false, "claude not found on PATH", "cannot verify the version; PermissionRequest needs Claude Code >= "+MinVersion)
	case compareVersions(v, MinVersion) < 0:
		r.add(false, true, "Claude Code "+v+" is too old", "PermissionRequest hooks need >= "+MinVersion)
	case compareVersions(v, RecommendedVersion) < 0:
		r.add(true, false, "Claude Code "+v, "works; >= "+RecommendedVersion+" recommended")
	default:
		r.add(true, false, "Claude Code "+v, "ok")
	}
	return r
}

func (r *Report) add(ok, fatal bool, name, detail string) {
	r.Checks = append(r.Checks, Check{OK: ok, Fatal: fatal, Name: name, Detail: detail})
}

// Fatal reports whether any fatal check failed.
func (r *Report) Fatal() bool {
	for _, c := range r.Checks {
		if !c.OK && c.Fatal {
			return true
		}
	}
	return false
}

// Print writes the report as a table.
func (r *Report) Print(w io.Writer) {
	for _, c := range r.Checks {
		mark := "ok  "
		if !c.OK {
			mark = "FAIL"
			if !c.Fatal {
				mark = "warn"
			}
		}
		fmt.Fprintf(w, "%s  %s\n", mark, c.Name)
		if c.Detail != "" {
			fmt.Fprintf(w, "      %s\n", c.Detail)
		}
	}
	if r.Fatal() {
		fmt.Fprintln(w, "\nverdict: yessir cannot answer prompts on this machine (see FAIL above)")
	} else {
		fmt.Fprintln(w, "\nverdict: yessir can answer prompts here")
	}
}

func samePath(a, b string) bool {
	n := func(s string) string {
		s = strings.ReplaceAll(s, "\\", "/")
		if runtime.GOOS == "windows" {
			s = strings.ToLower(s)
		}
		return s
	}
	return n(a) == n(b)
}

var versionRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

func claudeVersion() string {
	cands := [][]string{{"claude", "--version"}}
	if runtime.GOOS == "windows" {
		cands = append(cands, []string{"cmd", "/c", "claude --version"})
	}
	for _, c := range cands {
		out, err := exec.Command(c[0], c[1:]...).Output()
		if err != nil {
			continue
		}
		if m := versionRe.FindString(string(out)); m != "" {
			return m
		}
	}
	return ""
}

func compareVersions(a, b string) int {
	pa := versionRe.FindStringSubmatch(a)
	pb := versionRe.FindStringSubmatch(b)
	if pa == nil || pb == nil {
		return 0
	}
	for i := 1; i <= 3; i++ {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}
