# Plan: Agent-facing shell renaming

**Research snapshot:** 2026-08-10  
**Tracking:** `td-348d88`

## Decision first

Add a narrow, non-interactive CLI command:

```bash
sidecar shell rename "API planning"
sidecar shell rename --json "API planning"
```

With no target flag, the command renames only the Sidecar project shell that contains the calling process. That is the complete v1 agent journey. It does not accept a display name, tmux session name, index, project path, or arbitrary manifest path as a target. An agent working in a Sidecar shell already has the strongest and least ambiguous handle available: the current tmux session and server.

The command updates Sidecar's persisted display name, not tmux's session name or pane title. A running Sidecar instance observes the existing atomic `shells.json` replacement and updates its Workspaces view through the existing manifest watcher. The default output is one concise human-readable line; `--json` returns a stable object for agents that need to verify the result.

This should be a CLI rather than an HTTP or MCP service. The caller and state are local, the operation is one deterministic mutation, and starting a daemon or teaching agents a tool protocol would add no useful capability. Sidecar owns the persisted display name, so this is an intentional exception to Sidecar's usual presentation-only parity rule: without Sidecar, this name and its cross-instance synchronization do not exist.

## Outcome and agent journey

1. An agent begins a distinct piece of work in a Sidecar-managed project shell.
2. The repository's `AGENTS.md` tells it to run, for example, `sidecar shell rename "shell rename planning"` when its working context materially changes.
3. The command asks tmux for the caller's current session identity and socket, locates the one matching shell definition in Sidecar's state tree, validates the new display name, and atomically updates that definition.
4. Every open Sidecar instance sharing the project manifest observes the write. The same stable shell remains selected and running; only its displayed name changes.
5. The command prints the stable shell identity plus old and new display names, so the agent can tell that it changed the intended shell.

Example human output:

```text
Renamed current Sidecar shell "opus planning" to "shell rename planning".
```

Example structured output:

```json
{
  "shell": "sidecar-sh-sidecar-3",
  "oldName": "opus planning",
  "name": "shell rename planning",
  "changed": true
}
```

Repeating the same rename is a successful no-op with `changed: false`. This makes prompt retries harmless.

## What exists now

| Current seam | Current behavior | Required change |
| --- | --- | --- |
| `internal/plugins/workspace/keys.go` | The Rename Shell modal trims the name, enforces non-empty / 50-byte maximum, and rejects duplicate visible names | Move the domain validation into a view-independent rename operation used by both TUI and CLI |
| `internal/plugins/workspace/update.go` | `RenameShellDoneMsg` mutates the in-memory shell, calls `UpdateShell`, discards its error, then persists selection state | Apply the shared operation asynchronously, surface failures, and update memory only after persistence succeeds |
| `internal/plugins/workspace/shell_manifest.go` | Project-scoped `shells.json` writes use an advisory lock, read-before-write merge, temp file, and atomic rename | Preserve this concurrency contract behind a small state/store package rather than duplicating JSON writes in the CLI |
| `internal/plugins/workspace/shell_watcher.go` | Other Sidecar instances detect atomic manifest replacement and reconcile display names | Reuse unchanged; it is the live propagation path and needs an end-to-end test |
| `internal/projectdir` | Resolves project roots to directories under `$XDG_STATE_HOME/sidecar/projects/` using `meta.json` | Add a read-only inventory helper, or a shell-state locator beside it, that can search registered manifests without creating directories |
| `cmd/sidecar/main.go` | A single global `flag` set always proceeds toward TUI startup | Dispatch the `shell` subcommand before TUI initialization and keep existing flag-only invocation compatible |
| `AGENTS.md` | Explains tmux/state safety but not contextual shell naming | Add short lifecycle guidance only when the command has shipped and its installed behavior is proven |

The persistence comment in `shell_manifest.go` still says `~/.config/sidecar/projects`; the live resolver uses `$XDG_STATE_HOME/sidecar/projects` (normally `~/.local/state/sidecar/projects`). Correct that comment during implementation so agents are not sent to the wrong tree.

## Contract and refusal rules

### Command syntax

```text
sidecar shell rename [--json] <display-name>
```

- Accept exactly one positional display name. Shell quoting is ordinary CLI behavior; joining arbitrary remaining arguments would conceal quoting bugs.
- Trim surrounding whitespace before validation and output.
- Preserve the current 50-byte limit in v1 for exact TUI compatibility. If the product intends 50 characters instead, change both surfaces together with explicit UTF-8 tests rather than silently changing the CLI alone.
- Reject empty names, invalid UTF-8, control characters (including newline, carriage return, tab, ESC, and C0/C1 controls), and names over the limit. The control-character rule closes a trust-boundary gap in the current modal: persisted names are rendered in a terminal UI and must not be able to inject layout or terminal control sequences.
- Reject a duplicate display name within the located project manifest. This is slightly more conservative than today's visible-shell-only check, but it is deterministic for a headless caller and prevents ambiguous project shell labels across sibling worktrees. Move the TUI to the same rule.
- Do not rename a tmux session, pane, process, agent conversation, or terminal OSC title.

### Current-shell identity

Resolve identity from tmux, not `$PWD`:

1. Require a non-empty `TMUX` environment and query the current client/pane with `tmux display-message -p` for at least `session_name` and `socket_path`.
2. Require the session name to use Sidecar's project-shell namespace (`sidecar-sh-`). A worktree agent session (`sidecar-ws-`), terminal-panel session, ordinary user tmux session, or detached process is not eligible.
3. Canonicalize the socket path and match it against `ShellDefinition.Namespace`.
4. Search already registered project directories read-only and require exactly one manifest entry matching both tmux name and namespace.
5. Refuse zero matches with guidance to run the command from the Sidecar shell. Refuse multiple matches as corrupt/ambiguous state and list no writable path unless `--debug` is deliberately added later.

Do not infer the project from the current directory. Agents routinely `cd` to another repository or subdirectory while retaining the same Sidecar shell, and linked worktrees share one project manifest. Do not scan by display name, which is precisely the mutable and potentially stale value.

Legacy manifest entries may have an empty namespace. The interactive Sidecar already has reconciliation rules for claiming those entries, but a headless mutation must fail closed unless it can prove one unique live session and one unique manifest entry. Do not stamp or claim ambiguous legacy state as a side effect of rename.

### Output and exits

Define the JSON response in a transport-neutral result type:

```go
type RenameResult struct {
    Shell   string `json:"shell"`
    OldName string `json:"oldName"`
    Name    string `json:"name"`
    Changed bool   `json:"changed"`
}
```

- Exit `0` for changed and already-equal outcomes.
- Exit `2` for invocation/validation errors.
- Exit `1` for identity, state lookup, lock, read, or write failures.
- Write exactly one JSON value to stdout in `--json` mode and diagnostics to stderr. Never mix startup traces, logs, or TUI escape sequences into stdout.
- Never start Bubble Tea, initialize plugins, create project directories, open the debug log, start pprof, contact an agent provider, or touch tmux beyond the read-only identity query.

## Shared application boundary

Extract the shell manifest model and single-entry mutation into a small package outside `internal/plugins/workspace`, for example `internal/shellstate`. The exact package name can follow nearby conventions, but its responsibilities should remain narrow:

```go
type RenameRequest struct {
    TmuxName  string
    Namespace string
    Name      string
}

type Store interface {
    RenameCurrent(RenameRequest) (RenameResult, error)
}
```

The concrete local store owns registered-project discovery, manifest decoding, locking, read-before-write merge, and atomic replacement. The operation owns normalization, validation, duplicate policy, exact identity matching, no-op semantics, and typed refusal reasons. Neither layer imports Bubble Tea, CLI flags, or rendered UI types.

Do not add an interface solely to mock a single function. A narrow store seam is justified here because two real adapters (TUI and CLI) invoke the behavior and the filesystem is an independently testable boundary. Preserve the current manifest format and version; renaming requires no migration.

The TUI adapter should call the same operation and translate typed errors into the modal/toast vocabulary. It must stop updating `shell.Name` before the write succeeds and stop discarding manifest failures. The CLI adapter should only parse arguments, resolve current tmux identity through a small command runner, call the operation, and render its result.

## Implementation slices

### 1. Steel thread: current shell to persisted name

- Add a pure name normalizer/validator and typed rename outcomes.
- Add read-only registered-project manifest discovery.
- Implement exact `{namespace, tmuxName}` lookup and one-entry atomic rename by reusing the current locking/read-before-write behavior.
- Add `sidecar shell rename` dispatch that exits before normal Sidecar startup.
- Prove the command against a temporary state tree and isolated tmux server: create one Sidecar-shaped shell session and manifest, invoke the compiled binary from inside that session, and assert the one display name changed.

This slice is useful on its own: an agent can rename its current shell and a fresh Sidecar load will show the new name.

### 2. TUI convergence and live propagation

- Route the Rename Shell modal through the same validator and rename operation.
- Preserve modal input on a failed write and show the actual actionable error.
- Update the in-memory name only after success; keep selection keyed by `TmuxName` so rename cannot move focus.
- Run a watcher-backed test proving an already-running second plugin instance receives the atomic write and adopts the new display name without restart.
- Add parity tests showing CLI and TUI reject the same empty, long, control, invalid UTF-8, and duplicate names.

### 3. Discoverability and agent guidance

- Add `sidecar shell --help` and `sidecar shell rename --help`, including one quoted multi-word example and the current-shell-only safety rule.
- Add the following concise guidance to the repository `AGENTS.md`, adjusted to the final verified syntax:

  > When working inside a Sidecar project shell, keep its display name aligned
  > with your current task. Run `sidecar shell rename "short context"` when you
  > begin a materially different task or the existing name is stale. This
  > command can rename only the current Sidecar shell; do not edit
  > `shells.json` or rename tmux sessions directly.

- Document that agents should update on meaningful context changes, not every sub-step or status transition. A useful title describes the current outcome (`shell rename implementation`), not the model, harness, or transient action (`Codex running tests`).
- Add the command to the website/workspaces documentation only if the installed CLI is intended as a public user feature; `AGENTS.md` is sufficient for the initial repository-local agent journey.

### 4. Real installed-binary proof

- Run focused unit and integration tests, then `go test ./...`.
- Build the actual command and exercise both human and JSON output inside an isolated tmux socket and temporary `XDG_STATE_HOME`. Confirm paths first and never touch or restart the default tmux server.
- Start an isolated Sidecar through `scripts/tmux-drive.sh`, rename its shell from within that isolated shell, and capture the Workspaces view showing the new name without restart. Confirm both the tmux server and state tree are isolated before the run.
- Verify refusal from an ordinary tmux session, from outside tmux, for a `sidecar-ws-` session, and for an ambiguous/corrupt manifest.
- Run `make install-worktree` only if deliberate local activation is part of the implementation request; planning or tests do not authorize replacing the currently managed Sidecar binary.

## Test matrix

| Layer | Required evidence |
| --- | --- |
| Validation | Trimmed valid name; equal-name no-op; empty; over 50 bytes; multibyte boundary; invalid UTF-8; newline/tab/ESC/C0/C1; duplicate project name |
| Identity | Exact current shell; outside tmux; ordinary tmux session; worktree session; namespace mismatch; missing entry; duplicate match; legacy empty namespace |
| Store | Preserves all non-name fields and unrelated entries; no write on refusal/no-op; lock timeout is surfaced; concurrent peer add/rename is not clobbered; temp replacement remains atomic |
| CLI | Existing `sidecar [options]` still starts normally; nested help and argument errors; human output; single-object JSON; documented exit codes; no TUI/config/log initialization |
| TUI parity | Same accepted/refused names; persistence error leaves in-memory name unchanged and is visible; selection survives successful rename |
| Runtime | Isolated caller renames its own shell; live Sidecar watcher reflects it; default tmux server and real state/config trees remain untouched |

## Deliberate v1 limits

- No arbitrary `--session`, `--project`, `--manifest`, or display-name target. Add explicit targeting only when a real headless administration journey exists and can use a stable identifier plus a preview/confirmation contract.
- No command to list, create, kill, attach to, or send input to shells. Those are separate capabilities with different safety boundaries.
- No HTTP server, MCP tool, socket RPC, or direct messaging into the running TUI. The locked atomic manifest plus watcher is already the correct local seam.
- No automatic title generation from prompts, td tasks, Git branches, process names, or agent telemetry. Agents know when their semantic context changes; inference would create churn and stale or misleading names.
- No background heartbeat requirement. The command is event-driven guidance: rename at task/context boundaries and leave the last meaningful name visible while idle.

## Completion gates

The feature is complete when:

1. an agent inside a Sidecar project shell can deterministically rename only that shell with one non-interactive command;
2. validation, mutation, duplicate handling, and errors are shared with the TUI rather than reimplemented in `cmd/sidecar`;
3. concurrent manifests are merged without losing sibling entries and a failed write cannot create an in-memory/persisted split;
4. a running Sidecar reflects the rename through the existing watcher;
5. JSON output and exit behavior are stable and tested;
6. `AGENTS.md` tells agents when and how to update context without encouraging direct state-file or tmux mutation;
7. focused, full-suite, isolated tmux/state, and real rendered-app evidence is recorded; and
8. a fresh independent reviewer approves the integrated code and documentation.
