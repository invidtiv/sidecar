# Plan: Agent-facing "show this to the user" CLI, and a real CLI help system

**Research snapshot:** 2026-08-14
**Status:** proposed
**Scope:** `internal/cli`, a new request bus package, the two pane hosts
(`internal/plugins/workspace`, `internal/overview`), `cmd/sidecar/main.go`, docs.

## Decision first

Two decisions, one plan.

**1. A new non-interactive verb lets an agent put a file or a ticket in front of
the user, as a split pane in the workspace it is already working in.**

```bash
sidecar open internal/cli/cli.go            # file, in a split beside the terminal
sidecar open internal/cli/cli.go:88         # file at a line
sidecar open td-348d88                      # td issue
sidecar open --json --split below README.md # structured result for the agent
```

The command carries no target selector. Like `sidecar shell rename`, it acts on
the Sidecar shell that contains the calling process — the strongest, least
ambiguous handle an agent already holds. It reaches the running Sidecar instance
over a small file-based request bus in the state tree, and the instance opens the
pane through **exactly the code path a clicked terminal link already uses**.

**2. The CLI gets a command registry and a generated help system**, because
`open` is the second of many. Every command is declared once (name, summary,
usage, flags, exit codes, examples) and help, `--json` discovery, the reference
doc, and the agent instructions are all projections of that one declaration.

Correction to the premise: `shell name` and `shell rename` **do** have help today
(`sidecar shell --help`, `sidecar shell name --help`, `sidecar shell rename
--help`, `internal/cli/cli.go:191-241`). What is actually missing is
*discoverability and machine-readability*: `sidecar --help` prints only TUI flags
and never mentions `shell` (`cmd/sidecar/main.go:334`), there is no `sidecar
help`, and nothing emits the command tree as JSON. An agent that does not already
know the word "shell" cannot find these commands.

### Why this belongs in Sidecar's CLI at all

Sidecar's standing rule is that it is a presentation layer over tools that own
their own data, so it owes no CLI parity: an agent that wants to move a file runs
`mv`. Pane layout is the exception, on the same ground the shell-rename CLI was
granted: **Sidecar owns the pane tree.** There is no underlying tool to call.
Without Sidecar, a split showing `README.md` next to a live agent terminal does
not exist. The same is true of the shell display name, and the argument that
carried `sidecar shell rename` carries this.

Note what stays out: the agent does not get a way to *read* a file, *fetch* an
issue, or *open* an editor. It gets one thing Sidecar owns — a pane.

## Outcome and agent journey

1. An agent working in a Sidecar project shell writes a design doc, or is about
   to explain a ticket it is working from.
2. It runs `sidecar open docs/plans/active/thing.md` (or `sidecar open td-9f21c4`).
3. The command resolves the calling tmux session to one registered Sidecar shell,
   validates the target against that shell's workspace root, and writes a request
   into the state tree.
4. Every live Sidecar instance hosting that shell picks the request up within
   ~100ms. If the user is already looking at that shell, a document (or issue)
   pane opens beside the terminal — the same pane a click on that path in the
   terminal output produces. If they are somewhere else, the shell's row gets a
   badge saying something is waiting, and the pane opens when they go there.
   Nothing moves the user's selection or focus.
5. Each instance writes an acknowledgement. The command prints where it landed
   and exits 0; if no instance is watching, it exits 3 and says so, so the agent
   can fall back to "the file is at …" instead of claiming it showed something.
6. The user is now looking at the thing and can scroll, tab, and collaborate on
   it while the agent keeps working in the terminal beside it.

Human output:

```text
Opened docs/plans/active/thing.md in a split beside "shell rename implementation".
```

or, when the user is elsewhere:

```text
Queued docs/plans/active/thing.md for "shell rename implementation"; it opens when the user selects that shell.
```

Structured output:

```json
{
  "action": "open",
  "target": { "kind": "file", "value": "docs/plans/active/thing.md", "line": 0 },
  "shell": "sidecar-sh-sidecar-3",
  "name": "shell rename implementation",
  "delivered": 1,
  "results": [
    { "instance": "hostname-48120", "status": "opened", "pane": 4, "surface": "shell:sidecar-sh-sidecar-3" }
  ]
}
```

Repeating the same open is a successful no-op that retargets the existing pane —
the same behaviour a second link click has today — so retries are harmless.

## What exists now

| Current seam | Current behavior | Required change |
| --- | --- | --- |
| `internal/cli/cli.go` | Hand-rolled dispatch: one `switch` on `args[1]`, three `const` help strings, per-command flag loops | Replace with a declarative command registry; keep `shell name`/`shell rename` behavior and help text byte-identical |
| `cmd/sidecar/main.go:65` | `cli.Run` is consulted before `flag.Parse`, and only for `args[0] == "shell"` | Dispatch any registered command; make `sidecar --help` and bare `sidecar help` list them |
| `internal/shellstate` | Locates the one registered `shells.json` owning the caller's tmux session (`locateCurrentManifest`) | Reuse unchanged for origin identity; add a lookup that also returns the project key and workspace root |
| `internal/plugins/workspace/doc_panes.go:136` | `openDocPaneForSurface(root, surface, rel, line)` opens a document leaf bound to a terminal surface | Reuse unchanged. The request handler is a second caller, not a second implementation |
| `internal/plugins/workspace/issue_panes.go:59` | `openIssuePaneForSurface(root, surface, issueID)` does the same for a td id | Reuse unchanged |
| `internal/overview/preview_links.go:400` | Global Workspaces opens document/issue leaves via `panelayout.PlanOpen` + `SplitLeaf` | Reuse; add the same request handler at this host |
| `internal/plugins/workspace/shell_watcher.go` | fsnotify watcher on `shells.json` with a 100ms debounce, emitting a `tea.Msg` for cross-instance sync | The precedent and the template for the request watcher; generalize rather than copy |
| `internal/plugins/workspace/plugin.go:1122-1194` | `selectedTerminalSurface`, `selectingShell`, `selectNestedShell` decide and change which shell is selected | Read-only use: compare the request's surface against the selected one. Selection is never changed on an agent's behalf |
| `internal/agentstatus/status.go:56`, `internal/workspacelist/list.go:36` | `Presentation.Attention` drives the Needs Attention lane for blocked agents | Do **not** overload. Add a separate pending-view marker and glyph on the shell row |
| `internal/terminallink`, `internal/docview/path.go` | Validate and resolve path/line and td-id spans found in terminal output | Reuse for CLI argument classification and resolution, so a typed path and a clicked path obey one rule |
| `AGENTS.md` | Documents shell naming from `shellstate.NamingInstruction` | Add a generated CLI section; keep one source, as naming already does |

## Command surface

### `sidecar open`

```
Usage: sidecar open [options] <target>

Show a file or a td issue to the user, as a split pane in the Sidecar workspace
containing this shell.

Targets:
  path            A file inside this shell's workspace, optionally "path:line"
  td-xxxxxx       A td issue id

Options:
  --line N        Line to reveal (alternative to "path:line")
  --split auto|right|below
                  Where to place a new pane (default auto)
  --wait DURATION Time to wait for instances to acknowledge (default 1200ms; 0 = fire and forget)
  --json          Write one structured result object to stdout
  -h, --help      Show this help

Exit codes: 0 opened or queued, 1 state failure, 2 usage or validation error,
            3 no running Sidecar instance is showing this shell,
            4 an instance declined (e.g. the window is too small to split).
```

There is no focus flag. The command never moves the user's selection (see
"Routing"), so there is nothing for an agent to override.

Deliberate omissions in v1: no `--shell`/`--project` targeting (the calling shell
is the target, as with `rename`), no URLs, no arbitrary text or stdin panes, no
pane closing. Each is a natural sibling later; none is needed for the journey.

### Siblings this design anticipates

The registry exists so these cost a declaration and a `Run`, not a redesign:

- `sidecar panes [--json]` — what is open in this shell's workspace right now.
- `sidecar close <pane-id>` — put back what you showed.
- `sidecar open <url>` — hand a URL to the existing browser opener.
- `sidecar notify "text"` — get attention without taking the screen.
- `sidecar shell …` — the existing family, unchanged.

### Registry and help

```go
// internal/cli/command.go
type Command struct {
    Name      string
    Summary   string        // one line, shown in listings
    Usage     string        // "sidecar open [options] <target>"
    Long      string        // prose paragraphs
    Flags     []Flag        // name, arg, summary
    Args      ArgSpec       // count/shape, so usage errors are uniform
    ExitCodes []ExitCode
    Examples  []Example
    Sub       []*Command
    Run       func(Env, []string) int
}
```

- `sidecar help`, `sidecar --help`, `sidecar <cmd> --help`, `sidecar help <cmd>
  [<sub>]` all render from the registry. No help string is written twice.
- `sidecar help --json` emits the whole tree — name, summary, usage, flags,
  args, exit codes, examples — so an agent discovers the surface in one call.
  This is the "full help system available to agents" the request asks for.
- Conventions enforced by the registry, not by discipline: every command accepts
  `-h/--help` and `--json`; JSON is one object on stdout; diagnostics go to
  stderr; `0/1/2` mean success / state failure / usage error everywhere.
- A test renders the registry to `docs/reference/cli.md` and fails on drift, the
  same shape as the existing keybinding-parity tests.
- `AGENTS.md`'s CLI section is generated from the registry too, next to the
  existing `shellstate.NamingInstruction` pattern — one source, many channels.

## Transport: a request bus in the state tree

The CLI runs in a different process from the TUI. It needs to reach whichever
instances are showing the calling shell — possibly two, on two machines attached
to one tmux server (see `internal/tty/geometry_lease.go`).

**Chosen: a directory inbox with fsnotify delivery and per-instance ack files.**

```
$XDG_STATE_HOME/sidecar/requests/
  01J8Z…-open.json          request, written atomically (tmp + rename)
  01J8Z…-open.acks/
    hostname-48120.json     one ack per instance that saw it
```

Request payload:

```json
{
  "version": 1,
  "id": "01J8Z…",
  "createdAt": "2026-08-14T18:22:03Z",
  "ttlMs": 15000,
  "origin": {
    "tmuxSession": "sidecar-sh-sidecar-3",
    "namespace": "/private/tmp/tmux-501/default",
    "projectKey": "001",
    "workDir": "/Users/marcus/code/sidecar",
    "pid": 91234
  },
  "action": "open",
  "target": { "kind": "file", "value": "docs/plans/active/thing.md", "line": 0 },
  "options": { "split": "auto", "focus": true }
}
```

Ack payload: `{instance, host, pid, status: opened|retargeted|declined|error,
reason, surface, pane, at}`.

Mechanics:

- One inbox for all projects. Each instance watches one directory (fsnotify on
  the dir, 100ms debounce), and each request is self-describing, so an instance
  can decide in-memory whether it hosts `origin.tmuxSession`.
- **Every** instance that hosts the surface acts and acks. Two machines showing
  the same shell both open the pane, which is the correct behavior — the user is
  in front of one of them and it must be the right one.
- The CLI polls the ack directory until `--wait` elapses or an ack arrives, then
  deletes the request and its ack directory. Instances sweep entries older than
  `ttlMs` on startup and on each event, so a killed CLI leaves no litter.
- No daemon, no socket, no port, no new dependency; the files are inspectable,
  greppable, and hand-repairable, which is the same argument that put
  `shells.json` on disk.

Rejected alternatives:

- **Unix socket per instance.** Needs a liveness registry, stale-socket
  reaping, and a connection protocol, and buys only synchronous delivery — which
  ack files already give at file-watch latency. Revisit if Sidecar ever wants a
  real local API surface; the request/ack schema is the same shape either way.
- **tmux user options** (the geometry-lease trick). One opaque slot per session,
  no queue, needs polling. Right for a lease, wrong for a message.
- **Print a link and let the user click it.** Works today via terminal links,
  but it cannot open a split unattended, and the whole point is that the agent
  puts the thing on screen.
- **A `td`-style separate binary or an MCP tool.** The caller and the state are
  local and the operation is one deterministic mutation; a protocol adds nothing.

## Routing and refusal at the host

A request names a *surface* (`shell:<tmuxName>`), never a pane. Each host maps it
to what it already understands.

**The governing rule: an agent never moves the user.** Selection, plugin focus,
and scroll position belong to the person at the keyboard. A request either lands
where the user is already looking, or it waits for them with a visible marker
telling them where to go.

1. **The requesting shell is already the selected surface.** Open immediately:
   `openDocPaneForSurface` / `openIssuePaneForSurface` in the project workspace
   plugin, or the `preview_links.go` open path in global Workspaces. This is the
   click path; splitting, retargeting an already-open document pane, and
   surface-collapse-on-selection-change all come for free. Ack `opened`.
   The user sees a pane appear beside the terminal they are watching, which is
   exactly what they would expect from an agent working in front of them.
2. **The requesting shell is not selected (or Workspaces is not the focused
   plugin).** Open nothing. Record a pending-view marker against that shell and
   render a badge on its row, so the user can see *which* shell wants to show
   them something. Ack `queued`. When the user next selects that shell, the
   queued target opens as in case 1 and the badge clears. A stale queue expires
   with the request TTL, and selecting the shell after expiry opens nothing.
3. **Not this instance's shell.** Ignore silently — no ack, no marker.

The badge is its own signal, **not** `agentstatus.Presentation.Attention`. That
field means "the agent is blocked and needs you" and feeds the Needs Attention
lane (`internal/workspacelist/list.go:36`, `internal/overview/model.go:892`);
overloading it would put a shell in a lane it does not belong in. The
pending-view marker is per-instance in-memory state with its own glyph and its
own count, sitting beside the status icon on the row.

4. **Cannot split.** `panelayout` already refuses rather than squeezes (Law 2 of
   `workspace-windowing-system.md`). The host acks `declined` with the existing
   `paneFitMessage` reason; the CLI exits 4 and prints it, so the agent tells the
   user "your window is too narrow" instead of assuming success.
5. **Rate.** Repeated opens against one surface retarget the existing leaf; a
   burst is coalesced by the same debounce that already batches manifest events.

## Safety rules

- Request files live under `config.StateDir()` and pass
  `config.AssertIsolatedPath`, so an isolated proof run can never write into the
  real tree (`AGENTS.md`, td-8d18de).
- File targets resolve **inside the origin workspace root**, with the
  `create_operation.go` `openat`/`O_NOFOLLOW` discipline: no `..` escape, no
  symlink escape, no absolute path outside the root, size and type checks before
  a pane is created.
- The host re-validates everything on receipt. A request is data, never a
  command: no shell, no exec, no path the host has not itself resolved.
- Requests whose `origin.tmuxSession` is absent from a registered manifest, whose
  `namespace` does not match, or whose `createdAt` is expired, are dropped.
- Nothing here changes tmux server state, and nothing renames a tmux session.

## Milestones

Each ships something usable on its own.

**M0 — registry and help.** Command registry, generated help, `sidecar help`,
`sidecar help --json`, `sidecar --help` listing commands, `docs/reference/cli.md`
generator + drift test, `shell name`/`shell rename` migrated with output and exit
codes unchanged (existing `internal/cli/cli_test.go` assertions must pass
untouched). *Value alone: agents can find the commands that already exist.*

**M1 — the steel thread.** `internal/uirequest` (schema, atomic write, watch,
ack, sweep) + `sidecar open <path>` + the project workspace host, selected-shell
case only. An agent in the shell the user is watching runs one command and a
split appears beside its terminal. Files only; a request for an unselected shell
is dropped with a `queued` ack that no badge backs yet.

**M2 — completeness.** The pending-view badge and queued-open-on-select path,
`td-` issue targets, the global Workspaces host, `--split`, `--wait`, declined
acks and exit codes 3/4, `AGENTS.md` guidance so agents actually use it.

**M3 — the siblings.** `sidecar panes`, `sidecar close`, URL targets,
`sidecar notify`. Each should be a registry entry plus a `Run`; if any of them
needs new plumbing, M1's seam was drawn wrong.

## Testing

- **Registry/help:** golden render of the full tree, JSON schema shape, and a
  test that every command declares summary, usage, exit codes, and an example.
- **Bus:** encode/decode, atomic write under concurrent writers, TTL sweep,
  ack collection, expired/foreign/malformed requests dropped.
- **Resolution:** table tests that `internal/terminallink`-classified targets and
  CLI-classified targets agree, including `path:line`, absolute paths, escapes,
  and non-existent files.
- **Host parity — the load-bearing test:** a request and a synthetic link click
  for the same target produce **identical pane trees**. This is what keeps the
  CLI from growing a second, drifting open path.
- **Refusal:** a too-small box yields `declined` and no tree mutation.
- **End to end:** `scripts/tmux-drive.sh` with an isolated tmux server *and*
  isolated state tree, running `sidecar open` inside a created shell and
  asserting the captured screen shows the split.

## Decided

- **Verb: `sidecar open`.** Shortest for agents; it does not shadow
  `/usr/bin/open`.
- **Never steal focus.** An open lands only where the user is already looking;
  otherwise it queues behind a badge on the shell's row and opens when they
  choose to go there. No `--focus` escape hatch, because the point is that the
  agent cannot decide this.

## Open questions

1. **Badge shape.** A glyph plus count on the shell row is the minimum. Should
   the pending count also roll up to the Workspaces plugin's own tab/title, so a
   user in the file browser knows something is waiting at all? Recommendation:
   yes in M2, as a plain count, with no lane or sort-order change.
2. **Queue depth.** If an agent opens three files while the user is away, does
   selecting the shell open three panes, the last one, or one tab group?
   Recommendation: one document pane with the targets as tabs (`docview.Tabs`
   already models this), newest selected.
3. **Non-Sidecar callers.** Should `sidecar open` from an ordinary terminal fall
   back to "the most recently focused Sidecar instance", or fail cleanly?
   Recommendation: fail cleanly in v1 (exit 3), consistent with `shell rename`.
