// Package hook implements the PermissionRequest decision engine.
//
// It is pure: no file system, no clock. The CLI assembles a Policy from
// config files and the environment, calls Decide, and prints Output.
package hook

import (
	"bytes"
	"encoding/json"
)

// EventName is the only hook event yessir answers.
const EventName = "PermissionRequest"

// Input is the JSON Claude Code writes to the hook's stdin.
type Input struct {
	SessionID             string          `json:"session_id"`
	TranscriptPath        string          `json:"transcript_path"`
	CWD                   string          `json:"cwd"`
	HookEventName         string          `json:"hook_event_name"`
	PermissionMode        string          `json:"permission_mode"`
	ToolName              string          `json:"tool_name"`
	ToolInput             json.RawMessage `json:"tool_input"`
	PermissionSuggestions json.RawMessage `json:"permission_suggestions"`
}

// ToolInputMap decodes tool_input as a generic object (nil if absent/invalid).
func (in Input) ToolInputMap() map[string]any {
	if len(in.ToolInput) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(in.ToolInput, &m); err != nil {
		return nil
	}
	return m
}

// Policy is everything the decision depends on, already resolved.
type Policy struct {
	KillSwitch bool // YESSIR_OFF=1
	Paused     bool // ~/.yessir/paused in the future

	Global        bool   // config.global
	MarkerRoot    string // folder root whose marker enables yessir ("" if none)
	MarkerEnabled bool

	NeverApprove []string
	Ask          []string // global + marker lists, already concatenated
	Deny         []string
	DenyMessage  string
	Learn        bool
}

// Outcome of a decision.
const (
	Allow  = "allow"
	Deny   = "deny"
	AskOut = "ask"    // stay silent so the user decides
	Silent = "silent" // out of scope / paused / unknown event
)

// Decision is what the engine concluded, with a reason for the log.
type Decision struct {
	Outcome string
	Reason  string
	Scope   string // "global", "folder:<root>" or ""
}

// Decide applies the algorithm from SPEC.md §6.
func Decide(in Input, p Policy) Decision {
	if p.KillSwitch {
		return Decision{Outcome: Silent, Reason: "YESSIR_OFF is set"}
	}
	if p.Paused {
		return Decision{Outcome: Silent, Reason: "paused"}
	}
	scope := ""
	switch {
	case p.Global:
		scope = "global"
	case p.MarkerEnabled && p.MarkerRoot != "":
		scope = "folder:" + p.MarkerRoot
	default:
		return Decision{Outcome: Silent, Reason: "not enabled for this folder"}
	}
	if in.HookEventName != "" && in.HookEventName != EventName {
		return Decision{Outcome: Silent, Reason: "not a PermissionRequest event", Scope: scope}
	}
	if in.ToolName == "" {
		return Decision{Outcome: Silent, Reason: "no tool_name", Scope: scope}
	}
	ti := in.ToolInputMap()

	never, _ := ParseRules(p.NeverApprove)
	for _, r := range never {
		if r.Match(in.ToolName, ti, in.CWD).Matched {
			return Decision{Outcome: Silent, Reason: "neverApprove: " + r.Raw, Scope: scope}
		}
	}
	deny, _ := ParseRules(p.Deny)
	for _, r := range deny {
		m := r.Match(in.ToolName, ti, in.CWD)
		if m.Matched {
			return Decision{Outcome: Deny, Reason: "deny: " + r.Raw, Scope: scope}
		}
		if m.Unparseable {
			return Decision{Outcome: AskOut, Reason: "command could not be parsed; left to you", Scope: scope}
		}
	}
	ask, _ := ParseRules(p.Ask)
	for _, r := range ask {
		m := r.Match(in.ToolName, ti, in.CWD)
		if m.Matched {
			return Decision{Outcome: AskOut, Reason: "ask: " + r.Raw, Scope: scope}
		}
		if m.Unparseable {
			return Decision{Outcome: AskOut, Reason: "command could not be parsed; left to you", Scope: scope}
		}
	}
	return Decision{Outcome: Allow, Reason: "approved", Scope: scope}
}

// Output renders the JSON Claude Code expects on stdout. It returns nil when
// the hook should stay silent (print nothing, exit 0).
func Output(in Input, d Decision, p Policy) []byte {
	type decision struct {
		Behavior           string          `json:"behavior"`
		Message            string          `json:"message,omitempty"`
		UpdatedInput       json.RawMessage `json:"updatedInput,omitempty"`
		UpdatedPermissions json.RawMessage `json:"updatedPermissions,omitempty"`
	}
	type hso struct {
		HookEventName string   `json:"hookEventName"`
		Decision      decision `json:"decision"`
	}
	type out struct {
		HookSpecificOutput hso `json:"hookSpecificOutput"`
	}
	switch d.Outcome {
	case Allow:
		dec := decision{Behavior: "allow"}
		if in.ToolName == "ExitPlanMode" && len(in.ToolInput) > 0 && !bytes.Equal(bytes.TrimSpace(in.ToolInput), []byte("null")) {
			// Claude Code only honours an ExitPlanMode allow that echoes updatedInput.
			dec.UpdatedInput = in.ToolInput
		}
		if p.Learn && len(in.PermissionSuggestions) > 0 && !bytes.Equal(bytes.TrimSpace(in.PermissionSuggestions), []byte("null")) {
			dec.UpdatedPermissions = in.PermissionSuggestions
		}
		b, _ := json.Marshal(out{hso{EventName, dec}})
		return b
	case Deny:
		msg := p.DenyMessage
		if msg == "" {
			msg = "blocked by yessir config"
		}
		b, _ := json.Marshal(out{hso{EventName, decision{Behavior: "deny", Message: msg + " (" + d.Reason + ")"}}})
		return b
	default:
		return nil
	}
}

// Summary gives a short, log-friendly description of the tool call.
func Summary(in Input) string {
	ti := in.ToolInputMap()
	s := ""
	switch in.ToolName {
	case "Bash", "PowerShell":
		s, _ = ti["command"].(string)
	case "Edit", "Write", "Read", "MultiEdit", "NotebookEdit":
		s, _ = ti["file_path"].(string)
		if s == "" {
			s, _ = ti["notebook_path"].(string)
		}
	case "WebFetch":
		s, _ = ti["url"].(string)
	case "WebSearch":
		s, _ = ti["query"].(string)
	default:
		if len(in.ToolInput) > 0 {
			s = string(in.ToolInput)
		}
	}
	s = string(bytes.Join(bytes.Fields([]byte(s)), []byte(" ")))
	if len(s) > 200 {
		s = s[:197] + "..."
	}
	return s
}
