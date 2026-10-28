# yessir

Answer Claude Code's permission prompts with **yes** — everywhere, or only inside one folder — without `bypassPermissions` or `auto` mode.

Built for machines where an organisation has switched those modes off (`disableAutoMode`, `disableBypassPermissionsMode`) but still lets you configure hooks. yessir registers one [`PermissionRequest` hook](https://code.claude.com/docs/en/hooks-guide.md#auto-approve-specific-permission-prompts), which Claude Code runs exactly when it is about to ask you something, and answers it.

It never touches managed (enterprise) settings and cannot override your organisation's `deny` or `ask` rules — those win over any hook by design. `yessir doctor` tells you up front whether it can work on your machine and what will still prompt.

Windows first (PowerShell, Git Bash, Windows Terminal), then macOS and Linux. One static binary, no runtime.

## Install

**Windows (PowerShell)**

```powershell
irm https://raw.githubusercontent.com/rlgdev/yessir/main/scripts/install.ps1 | iex
```

**macOS / Linux**

```bash
curl -fsSL https://raw.githubusercontent.com/rlgdev/yessir/main/scripts/install.sh | sh
```

**npm** (any OS; works behind corporate proxies that only let the npm registry through)

```bash
npm install -g @rlgdev/yessir
```

The package ships the native binary for your platform as an optional dependency (`@rlgdev/yessir-win32-x64`, `-darwin-arm64`, …); nothing is downloaded from GitHub at install time, and Node is not involved when the hook runs. Install globally — `yessir on` refuses to register a copy run through `npx`, because npm's cache can disappear under it. Don't install with `--omit=optional`.

**From the source ZIP** (when release downloads are blocked)

You need [Go](https://go.dev/dl/) 1.21 or newer: the `.msi` installs without admin rights if you choose "install for me only", or run `winget install GoLang.Go`. yessir uses only Go's standard library, so the build downloads nothing.

1. On https://github.com/rlgdev/yessir click **Code → Download ZIP** and unzip it, e.g. to `C:\src\yessir-main`.
2. In PowerShell, from the unzipped folder:

   ```powershell
   cd C:\src\yessir-main
   powershell -ExecutionPolicy Bypass -File .\scripts\install-from-source.ps1
   ```

   The script builds `yessir.exe` into `%USERPROFILE%\.yessir\bin` (the hook will point there, so keep it) and adds that folder to your user PATH. Options: `-Dest <folder>` to install elsewhere, `-NoPath` to leave PATH alone.
3. Open a new terminal, then:

   ```powershell
   yessir version     # yessir 0.1.0-src (windows/amd64)
   yessir doctor
   yessir on --global # or: cd <repo>; yessir on
   ```

If scripts are blocked entirely, the same build by hand:

```powershell
cd C:\src\yessir-main
$env:GOTOOLCHAIN='local'; $env:GOPROXY='off'; $env:CGO_ENABLED='0'
New-Item -ItemType Directory -Force "$env:USERPROFILE\.yessir\bin" | Out-Null
go build -trimpath -o "$env:USERPROFILE\.yessir\bin\yessir.exe" ./cmd/yessir
& "$env:USERPROFILE\.yessir\bin\yessir.exe" version
```

To upgrade, download a fresh ZIP and run the script again (same install folder, so the hook keeps working).

macOS / Linux from the source ZIP: unzip, `cd yessir-main`, then

```bash
mkdir -p ~/.yessir/bin && GOTOOLCHAIN=local GOPROXY=off CGO_ENABLED=0 go build -trimpath -o ~/.yessir/bin/yessir ./cmd/yessir
export PATH="$HOME/.yessir/bin:$PATH"   # add to your shell profile
```

**With `go install`** (needs access to the Go module proxy or github.com)

```bash
go install github.com/rlgdev/yessir/cmd/yessir@latest
```

Binaries for every release are on the [Releases](https://github.com/rlgdev/yessir/releases) page (windows/darwin/linux × amd64/arm64).

## Use

```text
yessir doctor            # can it work here? what will still prompt?
yessir on                # this repository (marker in .claude/yessir.local.json)
yessir on --global       # every session
yessir on --rules        # also write bare-tool allow rules — fastest path, see below
yessir status
yessir log -n 20
yessir pause 30m         # stay silent for a while; `yessir resume` ends it early
yessir off  / off --global
yessir uninstall         # removes everything yessir wrote
```

Start a **new** `claude` session after `yessir on`; hooks load at start-up. In the transcript, approved prompts show as *Allowed by PermissionRequest hook*.

Kill switch: run claude with `YESSIR_OFF=1` in its environment.

## What it approves — and what it leaves to you

Every prompt in scope is answered **allow**, except:

| | default | where to change |
|---|---|---|
| `neverApprove` | `AskUserQuestion` | `~/.yessir/config.json` |
| `ask` (stay silent, you decide) | `git push --force`, `git push -f`, `rm -rf /*`, `rm -rf ~*`, `sudo *` | `~/.yessir/config.json`, or per folder in `.claude/yessir.local.json` |
| `deny` (refuse with a message) | none | same |

Rule syntax is the subset of [Claude's permission-rule syntax](https://code.claude.com/docs/en/permissions.md#permission-rule-syntax) that yessir understands: `Tool`, `Bash(git push *)`, `Bash(npm run build)`, `Edit(src/**)`, `WebFetch(domain:*.corp)`, `mcp__server__*`. Compound commands are split on `&&`, `||`, `;`, `|`, `$(…)`; a command yessir cannot parse is left to you.

The guard is a convenience, not a security boundary. Claude Code's own `deny` rules and sandbox are.

### Things no hook can approve (Claude Code behaviour)

- Calls matched by `deny` or `ask` rules in any settings file, including managed ones.
- MCP tools marked `requiresUserInteraction`, connector tools your org set to *ask*, reads from network (UNC) paths, `rm`/`rmdir` on critical paths.
- In **plan mode**, anything that is not read-only (yessir does approve `ExitPlanMode`).
- `claude -p` headless runs (use `--permission-prompt-tool` there) and `dontAsk` mode.

If your organisation sets `allowManagedHooksOnly` or `disableAllHooks`, user hooks never run and yessir cannot help; `doctor` says so and `on` refuses.

## `--rules` fast path

`yessir on --rules` additionally writes bare-tool allow rules (`Bash`, `Edit`, `Write`, `NotebookEdit`, `WebFetch`, `WebSearch`, `Agent`) into the scope's settings file — `~/.claude/settings.json` for `--global`, `<repo>/.claude/settings.local.json` otherwise. Those calls never reach a prompt, so they never reach yessir's `ask` guard either; your `deny` list is mirrored as real `deny` rules to compensate. MCP tools stay on the hook path (Claude ignores `mcp__*` in allow lists). Not available when `allowManagedPermissionRulesOnly` is enforced.

## `--learn`

With `yessir on --learn`, each approval also passes Claude's own "don't ask again" suggestion back (`updatedPermissions`), so Claude writes its usual rule to `.claude/settings.local.json` and that prompt never comes back — even after `yessir off`.

## Files

| Path | What |
|---|---|
| `~/.yessir/config.json` | global switch + guardrails |
| `~/.yessir/state.json` | exactly what yessir wrote into Claude settings (so `off`/`uninstall` remove only that) |
| `~/.yessir/log.jsonl` | one line per decision, rotated at 5 MB |
| `~/.yessir/backup/` | copy of each settings file before yessir's first write that day |
| `<repo>/.claude/yessir.local.json` | folder marker, added to `.git/info/exclude` |
| `~/.claude/settings.json` | one entry under `hooks.PermissionRequest` (exec form: `command` + `args: ["hook"]`) |

## A note on policy

Disabling bypass/auto mode is usually a deliberate choice by whoever owns your Claude Code policy. yessir only uses what that policy leaves configurable (user hooks and rules), but in effect it is close to bypass mode. Check with them before rolling it out across a team; `yessir doctor` output is a good thing to show.

## Development

```bash
go test ./...
go build ./cmd/yessir
printf '{"hook_event_name":"PermissionRequest","tool_name":"Bash","cwd":"%s","tool_input":{"command":"ls"}}' "$PWD" | ./yessir hook
```

`testdata/permission-request/` holds captured payloads; `cmd/yessir/main_test.go` runs the whole on → hook → off → uninstall lifecycle in a temp home. `scripts/npm-smoke.sh` builds, packs and runs the npm launcher for your platform. Design: [SPEC.md](SPEC.md).

Releasing: bump `VERSION` on main. The release workflow tags it, publishes the GitHub release (GoReleaser) and then the npm packages (`.github/workflows/npm.yml`, needs the `NPM_TOKEN` secret; it can also be run by hand for an existing tag).

MIT © rlgdev
