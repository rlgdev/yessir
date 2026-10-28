# yessir — auto-answer Claude Code permission prompts

Working name: **yessir** (`"yes, sir"`). Repo: `github.com/rlgdev/yessir`.

Outcome: after `yessir on`, Claude Code CLI stops asking "Do you want to proceed?" — everywhere (`--global`) or only inside a chosen folder tree (`--here`). It works in the default (manual) permission mode, so it does not need `bypassPermissions` or `auto` mode, both of which an organisation can switch off. It never touches managed (enterprise) settings and never overrides an organisation's `deny` or `ask` rules.

Status: implemented (v0.1.0). This document is the design the code follows. Facts below were checked against the Claude Code docs on 2026-10-28.

---

## 1. Mechanism (what the design relies on)

| Fact | Consequence for yessir | Source |
|---|---|---|
| `PermissionRequest` hook event fires when Claude Code is about to show a permission prompt; a hook that prints `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}` answers the prompt. Requires Claude Code ≥ 2.0.45 (events ignored silently on older versions). | One hook = "click yes". It only runs when a prompt would appear, so no latency on normal tool calls. | [hooks-guide](https://code.claude.com/docs/en/hooks-guide.md#auto-approve-specific-permission-prompts), [hooks reference](https://code.claude.com/docs/en/hooks.md) |
| An empty / `.*` matcher on `PermissionRequest` covers every tool prompt (Bash, Edit, Write, WebFetch, MCP tools, ExitPlanMode…). | One registration, scope decided inside the hook. | [hooks-guide](https://code.claude.com/docs/en/hooks-guide.md#auto-approve-specific-permission-prompts) |
| Hooks can be registered in `~/.claude/settings.json` (all projects), `.claude/settings.json` / `.claude/settings.local.json` (one project), managed settings, plugins. | Global install registers once in `~/.claude/settings.json`; per-folder scope is a marker file read by the hook (no second registration needed). | [hooks reference](https://code.claude.com/docs/en/hooks.md) |
| Exec form: `"command": "<abs path>", "args": [...]` spawns the binary directly — no `sh`/Git Bash/PowerShell in between (on Windows, shell form goes through Git Bash or PowerShell and can prepend profile noise to stdout). | Register in exec form; ship a single static binary. | [hooks-guide troubleshooting](https://code.claude.com/docs/en/hooks-guide.md) |
| Hook input (stdin JSON) carries `cwd`, `tool_name`, `tool_input`, `permission_mode`, `session_id`, and for `PermissionRequest` also `permission_suggestions`. Output may include `updatedInput` and `updatedPermissions` (e.g. `{"type":"setMode", …}`). | Scope by `cwd`; deny-pattern guard on `tool_input`; optional "learn" mode via `updatedPermissions`. | [hooks reference](https://code.claude.com/docs/en/hooks.md) |
| Precedence: `deny` rules, then `ask` rules, are evaluated regardless of hook output; a hook cannot approve past them. Managed settings sit above everything. | yessir is policy-compatible by construction: org `deny`/`ask` still win. `doctor` lists the `ask` rules that will keep prompting. | [permissions → precedence among rules, hooks, mods](https://code.claude.com/docs/en/permissions.md#precedence-among-rules-hooks-and-mods) |
| Managed keys that switch the mechanism off: `disableAllHooks: true`, `allowManagedHooksOnly: true` (hooks), `allowManagedPermissionRulesOnly: true` (user/project allow rules). Managed sources: `C:\Program Files\ClaudeCode\managed-settings.json` (+ `managed-settings.d\`), `HKLM\SOFTWARE\Policies\ClaudeCode` / `HKCU\…\ClaudeCode` `Settings` value; `/Library/Application Support/ClaudeCode/`; `/etc/claude-code/`. `/status` shows the active source; `claude doctor` lists dropped keys. | `yessir doctor` reads these and says up front whether the tool can work on this machine. | [managed-settings](https://code.claude.com/docs/en/managed-settings.md), [settings-reference](https://code.claude.com/docs/en/settings-reference.md) |
| `permissions.disableAutoMode: "disable"` and `permissions.disableBypassPermissionsMode: "disable"` remove those modes; neither disables hooks or user allow rules. Allow rules + `PreToolUse`/`PermissionRequest` approvals keep working in `default` mode. | This is exactly the gap yessir fills. | [permission-modes](https://code.claude.com/docs/en/permission-modes.md) |
| "Yes, and don't ask again" writes rules to `<git root>/.claude/settings.local.json`. `Bash(*)` ≡ `Bash` = every Bash command; bare tool names (`Edit`, `Write`, `WebFetch`…) match all uses. The docs themselves suggest: allow `Bash` and block the few you care about with a hook. | Optional fast path (`--rules`) writes bare-tool allow rules into the same file Claude uses. | [permissions](https://code.claude.com/docs/en/permissions.md) |

Things a hook **cannot** do (documented, so out of scope): approve past `deny`/`ask` rules; approve MCP tools marked `requiresUserInteraction`, connector tools an org set to `ask`, reads from network (UNC) paths; approve critical-path `rm`/`rmdir`; approve non-read-only calls while in **plan mode** (only `ExitPlanMode`, and that one needs `updatedInput` echoed back — [issue #90498](https://claudeissues.com/issue/90498-bug-permissionrequest-hook-allow-is-silently-ignored-for-exitplanmode-unless-upd)); fire in top-level `claude -p` headless runs (interactive sessions and their subagents only) or in `dontAsk` mode. Known flakiness: a race where the dialog still shows briefly ([#12176](https://claudeissues.com/issue/12176-bug-permissionrequest-hook-race-condition-dialog-shows-despite-hook-returning-al)) and reports of the VS Code extension ignoring the event ([#13203](https://claudeissues.com/issue/13203-bug-permissionrequest-hook-does-not-trigger-in-vscode-extension)). Primary target is the terminal CLI.

---

## 2. Prior art (why build, not install)

| Tool | Event | Scope | Platform | Gap |
|---|---|---|---|---|
| [psd401/claude-auto-approve](https://github.com/psd401/claude-auto-approve) | `PermissionRequest` | global only | bash + jq (macOS/Linux) | no Windows, no per-folder switch, no on/off CLI, no doctor |
| [froggeric/claude-smart-approval](https://github.com/froggeric/claude-smart-approval) | `PermissionRequest`, Bash only | global | bash + shfmt + jq | Bash only; AI tier adds ~8 s per decision; fails toward prompting (opposite goal) |
| [dylancaponi/claude-code-permissions](https://github.com/dylancaponi/claude-code-permissions) | `PreToolUse` + allow rules + macOS Seatbelt | global | macOS | macOS-centric, Bash-focused |
| [synercoder/ClaudeApprover](https://github.com/synercoder/ClaudeApprover) (.NET) | `PreToolUse` | per project, code-defined rules | .NET 10 SDK | unregistered commands → Ask; `dotnet run` per call; prerelease |
| [oryband/claude-code-auto-approve](https://github.com/oryband/claude-code-auto-approve) | `PreToolUse`, Bash | global | bash | archived 2026-07 with an open security issue; author says use native rules |

None gives: one binary, Windows + macOS + Linux, `on/off` globally or per folder, policy check, log. That is yessir.

---

## 3. Scope

In scope
- Answer every permission prompt with **allow**, when enabled for the current scope.
- Two scopes: global (all sessions) and folder (a directory tree, marker-based).
- Guardrails the user owns: deny patterns (hook stays silent → normal prompt, or returns deny), never-approve tool list, kill switch, pause.
- `doctor`: say whether the machine's managed policy lets this work, and what will still prompt.
- JSONL log of every decision.
- Optional `--rules` fast path: bare-tool allow rules in Claude's own settings files.
- Optional `--learn`: persist the prompt's own `permission_suggestions` as allow rules so the same prompt never reaches the hook again.

Out of scope (never)
- Reading, writing or working around managed settings / registry policy keys.
- Overriding `deny` / `ask` rules from any settings level.
- Keystroke or PTY automation (no "press Enter" bots).
- Answering `AskUserQuestion`, MCP elicitations, login/OAuth dialogs.
- Headless `-p` / Agent SDK (use `--permission-prompt-tool` / `canUseTool` there).

---

## 4. CLI

```
yessir on   [--global | --here [<dir>]] [--rules] [--learn]
yessir off  [--global | --here [<dir>]]
yessir pause [<duration>]        # e.g. 30m; silent hook until then
yessir status                    # scope state, hook registration, last 5 decisions
yessir doctor                    # policy + install check, exit 0/1
yessir log  [-n 50] [--follow] [--json]
yessir hook                      # internal: stdin JSON → stdout decision
yessir uninstall                 # removes everything yessir wrote
yessir version
```

Defaults: `on`/`off` with no flag = `--here` for the git root of the current directory (falls back to cwd outside a repo). `on` registers the hook globally on first use (idempotent) and sets the chosen scope.

Exit codes: 0 ok; 1 policy blocks (doctor); 2 usage; 3 settings file unreadable/unwritable.

---

## 5. Files yessir owns

| Path | Purpose |
|---|---|
| `~/.yessir/config.json` | global state + guardrails (schema below) |
| `~/.yessir/state.json` | exactly what yessir wrote into Claude settings files (hook entry id, rule strings per file) — used by `off`/`uninstall` to remove only its own lines |
| `~/.yessir/log.jsonl` | decision log, rotated at 5 MB × 3 |
| `~/.yessir/paused` | pause-until timestamp |
| `<root>/.claude/yessir.local.json` | folder-scope marker; yessir appends the path to `<root>/.git/info/exclude` (never to `.gitignore`) |
| `~/.claude/settings.json` | **one** hook entry under `hooks.PermissionRequest` (and `permissions.allow` entries only with `--rules --global`) |
| `<root>/.claude/settings.local.json` | `permissions.allow`/`deny` entries only with `--rules --here` |

Hook entry written to `~/.claude/settings.json` (merged, never overwriting other hooks):

```json
{
  "hooks": {
    "PermissionRequest": [
      {
        "matcher": "",
        "hooks": [
          { "type": "command",
            "command": "C:\\Users\\<me>\\.yessir\\bin\\yessir.exe",
            "args": ["hook"],
            "timeout": 5 }
        ]
      }
    ]
  }
}
```

`~/.yessir/config.json`:

```json
{
  "version": 1,
  "global": false,
  "log": true,
  "learn": false,
  "neverApprove": ["AskUserQuestion"],
  "ask":  ["Bash(git push --force *)", "Bash(git push -f *)", "Bash(rm -rf /*)", "Bash(sudo *)"],
  "deny": [],
  "denyMessage": "blocked by yessir config"
}
```

Marker `.claude/yessir.local.json`:

```json
{ "enabled": true, "ask": [], "deny": [] }
```

Pattern syntax = the subset of Claude's permission-rule syntax yessir implements: `Tool`, `Tool(*)`, `Bash(<prefix> *)`, `Bash(<prefix>:*)`, `Edit|Write|Read(<glob>)`, `WebFetch(domain:<host>)`, `mcp__<server>__<tool>`, `mcp__<server>__*`. Folder-marker lists are added to the global ones (never replace).

---

## 6. Hook decision algorithm (`yessir hook`)

Input: one JSON object on stdin. Must finish < 1 s; hard cap 5 s via the registered `timeout`. Any internal error → print nothing, exit 0 (Claude shows the normal prompt). Never exit 2 (not honoured for this event anyway).

1. **Kill switch**: env `YESSIR_OFF=1` or `~/.yessir/paused` in the future → silent.
2. **Scope**: `config.global == true` → in scope. Else walk from `cwd` upward to the filesystem root; first `.claude/yessir.local.json` with `enabled: true` → in scope. Nothing found → silent.
3. **Event sanity**: `hook_event_name == "PermissionRequest"` else silent.
4. **neverApprove**: `tool_name` listed → silent.
5. **deny** patterns match → output `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"deny","message":"<denyMessage>"}}}` and log.
6. **ask** patterns match → silent (user decides) and log `ask`.
7. **allow** → output:

```json
{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}
```

   Special cases: `ExitPlanMode` → add `"updatedInput": <tool_input unchanged>` (required for the allow to take effect). With `learn: true` and `permission_suggestions` present → add `"updatedPermissions": <permission_suggestions>` so Claude writes its own "don't ask again" rule.

8. **Log** one JSONL line: `{"ts","session_id","cwd","scope":"global|folder:<root>","tool","summary","decision":"allow|ask|deny|silent","reason","ms"}`. `summary` = Bash command / file path / URL / MCP tool, truncated to 200 chars.

Bash matching for `ask`/`deny` follows the hook `if`-field semantics loosely: split on `&&`, `||`, `;`, `|`; strip leading `VAR=value` assignments; a segment matching any pattern matches the whole command. Unparseable command → treated as matching `ask` (silent). This guard is a convenience, not a security boundary (Claude's own deny rules and sandboxing are).

---

## 7. `yessir doctor`

Reads, in this order, and prints a table with ✔/✖ and the file that set each key:

1. Managed sources for the OS (file, `managed-settings.d/*.json`, Windows HKLM + HKCU `Settings` value, macOS plist). Unreadable (permission denied) → report "cannot read, assuming enforced; check `/status` inside Claude Code".
2. `~/.claude/settings.json`, `<root>/.claude/settings.json`, `<root>/.claude/settings.local.json`.
3. `claude --version` (need ≥ 2.0.45; recommend ≥ 2.1.110 for correct `updatedInput` re-checking).

Checks and verdicts:

| Check | Fail means |
|---|---|
| `disableAllHooks` not `true` at user/managed level | hook will never run → yessir cannot work |
| `allowManagedHooksOnly` not `true` | user hook ignored → yessir cannot work |
| `allowManagedPermissionRulesOnly` not `true` | `--rules` fast path unavailable (hook still fine) |
| `disableAutoMode` (checked both as `permissions.disableAutoMode` and top-level — the docs show both placements) / `permissions.disableBypassPermissionsMode` | informational — this is the situation yessir is for |
| managed / user `permissions.ask` rules present | listed verbatim: these will still prompt |
| managed `permissions.deny` rules present | listed verbatim: these will be refused regardless |
| hook entry present in `~/.claude/settings.json`, binary path exists, exec form | install health |
| `hooks` block not malformed JSON | Claude would drop the file |

Exit 1 when the first two checks fail; prints the one-line reason ("your organisation enforces `allowManagedHooksOnly`; yessir cannot answer prompts on this machine").

---

## 8. `--rules` fast path

Writes into the scope's settings file (`~/.claude/settings.json` for global, `<root>/.claude/settings.local.json` for folder):

```json
{ "permissions": {
    "allow": ["Bash", "Edit", "Write", "NotebookEdit", "WebFetch", "WebSearch", "Agent"],
    "deny":  [ …config.deny mirrored here… ] } }
```

Bare tool names only: `Write(...)`/`NotebookEdit(...)` path rules never match, and an allow glob for MCP must start with a literal `mcp__<server>__` prefix (`mcp__*` in allow is ignored), so MCP tools stay on the hook path. A call matched by an allow rule never reaches the prompt, so the hook's `ask` guard does not see it; that is why `config.deny` is mirrored as real `deny` rules. `off`/`uninstall` remove exactly the strings recorded in `state.json`.

---

## 9. Implementation

- **Language**: Go 1.24, standard library only. Single static binary, < 10 ms cold start, no runtime dependency on the developer machine (Claude Code's native installer no longer guarantees Node).
- **Layout**

```
yessir/
├── cmd/yessir/main.go            # CLI (stdlib flag; no third-party deps)
│   └── main_test.go             # full on → hook → off → uninstall lifecycle in a temp home
├── internal/hook/               # decision engine (pure, testable)
│   ├── decide.go
│   ├── patterns.go              # rule-syntax subset
│   └── decide_test.go           # table tests
├── internal/config/             # ~/.yessir files, folder marker, pause
├── internal/settings/           # locate + merge-edit Claude settings files (keeps unknown keys and top-level key order)
├── internal/policy/             # managed-source readers (file, managed-settings.d, HKLM/HKCU, plist)
├── internal/doctor/
├── internal/logx/
├── testdata/permission-request/ # real captured payloads: bash, edit, webfetch, mcp, exitplanmode
├── scripts/install.ps1, scripts/install.sh
├── .goreleaser.yaml             # windows/darwin/linux × amd64/arm64
├── .github/workflows/ci.yml     # go vet + go test + hook smoke on windows/macos/ubuntu
├── .github/workflows/release.yml
├── README.md
└── SPEC.md
```

- **Settings edits**: parse → modify → write back with the original indentation; refuse to write if the file fails to parse (exit 3) rather than clobbering it. Back up to `~/.yessir/backup/<name>.<ts>.json` before the first write per file.
- **Windows**: paths via `os.UserHomeDir()`; registry read with `reg query` (no cgo, no extra module); marker lookup stops at drive root; handle `cwd` arriving with forward slashes.
- **Logging**: append-only, `O_APPEND`, no locking needed (one line per process).

Install for users:

```powershell
# Windows
irm https://raw.githubusercontent.com/rlgdev/yessir/main/scripts/install.ps1 | iex
```
```bash
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/rlgdev/yessir/main/scripts/install.sh | sh
# or
go install github.com/rlgdev/yessir/cmd/yessir@latest
```

Then: `yessir doctor && yessir on --global` (or `cd <repo> && yessir on`).

---

## 10. Tests and acceptance

Unit (`go test ./...`): decision table — every combination of scope × neverApprove × ask × deny × tool kind; pattern parser against the documented rule forms; Bash segment splitting; settings merge preserving unrelated keys and other hooks.

Integration (`make e2e`, runs the real binary): pipe each `testdata/permission-request/*.json` into `yessir hook` and assert stdout + log line; `on`/`off`/`uninstall` round-trip leaves `~/.claude/settings.json` byte-identical.

Manual acceptance (one session each, `claude --debug` to see "Allowed by PermissionRequest hook"):

- [ ] `yessir on --global` → a Bash command, a file write, a WebFetch, an MCP tool call, and a plan approval all run without a prompt.
- [ ] `yessir off --global; cd repoA; yessir on` → no prompts in repoA and its subfolders; prompts as usual in repoB.
- [ ] `YESSIR_OFF=1 claude` → prompts as usual; `yessir pause 10m` → same for 10 minutes.
- [ ] A command matching `ask` (e.g. `git push --force`) still prompts; one matching `deny` is refused with the configured message.
- [ ] With a managed `permissions.ask` rule on the machine, that rule still prompts and `yessir doctor` listed it beforehand.
- [ ] `yessir doctor` on a machine with `allowManagedHooksOnly: true` exits 1 with the explanation; `yessir on` refuses to proceed.
- [ ] `yessir uninstall` leaves no hook entry, no rules, no markers; Claude settings files parse.
- [ ] Windows terminal (PowerShell and Git Bash), macOS, Linux: all of the above.

---

## 11. Roadmap after v1

- Plugin packaging (`.claude-plugin/plugin.json` + `hooks/hooks.json`) so teams can install from a marketplace with `claude plugin install yessir@<marketplace>`.
- `yessir on --session` using `updatedPermissions: [{"type":"setMode","mode":"acceptEdits","destination":"session"}]` on first prompt instead of answering each prompt.
- Notification hook integration (`Notification` matcher `permission_prompt`) to show a toast "yessir approved X" on Windows/macOS.
