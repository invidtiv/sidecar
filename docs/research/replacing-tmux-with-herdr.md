# Replacing Sidecar's tmux integration with Herdr

Status: research proposal, 2026-08-08  
Herdr source inspected: [`herdrdev/herdr`](https://github.com/herdrdev/herdr) at `10974c822d607f03e20e9741ec027910f0c1f93a`  
Herdr binary exercised: `herdr 0.8.0` (installed), API protocol `19`  
Related research: [Herdr session persistence](./herdr-session-persistence.md)

## Decision first

Sidecar should explore Herdr as a **replacement terminal runtime**, not embed or
fork Herdr's TUI and not replace Sidecar as the surrounding product.

The first useful slice is a new, opt-in **Herdr Workspaces** tab that keeps
Sidecar's current workspace experience—sidebar, output preview, interactive
mode, diffs, tasks, merge flow, and worktree actions—but sources terminal and
agent state from a running Herdr server through its socket API. Users should not
need to know whether the pane underneath is tmux or Herdr. An explicit “Open in
Herdr” escape hatch is useful during the trial, but the normal path remains
Sidecar's UI.

Do not begin by deleting `internal/tty` or translating every tmux call. Run the
new backend beside the existing workspace plugin until the real workflows prove
parity. If it succeeds, make Herdr the default, migrate or naturally drain tmux
sessions, then remove tmux. If it fails, deleting the experimental plugin and
adapter returns Sidecar to its current behavior.

This is attractive because Herdr already owns the hard part Sidecar has been
incrementally rebuilding around tmux: persistent PTYs, terminal emulation,
scrollback, reconnectable clients, agent detection, state changes, worktree
provenance, and an agent-facing CLI/socket API. The important caveat is that
Herdr is an executable with versioned CLI/socket surfaces, not a Go library.
Sidecar would be taking a runtime dependency on a fast-moving external product,
but it does not need to reverse-engineer Herdr's private client protocol to get
a faithful terminal stream.

## What “transparent” means

A successful replacement changes installation and recovery behavior, not the
day-to-day Sidecar journey:

1. A user opens Sidecar and sees their shells and worktrees in Workspaces.
2. Creating a shell or agent starts it in the selected checkout with the same
   prompt, agent, environment, and permission choices as today.
3. Output, cursor, keyboard, mouse, paste, selection, search, history, links,
   terminal panels, and native-terminal attach behave at least as well as today.
4. Closing Sidecar, losing SSH, or closing the laptop does not stop the process.
   Restarting Sidecar reconstructs the workspace view from Herdr.
5. Agent state comes from Herdr (`idle`, `working`, `blocked`, `done`, or
   `unknown`) instead of Sidecar inferring activity from tmux output timing.
6. A user can still open the underlying session directly when Sidecar is absent.
7. Agents can inspect and control the same runtime through `herdr`'s CLI/API;
   Sidecar does not create a privileged UI-only capability.

Transparency does **not** mean silently taking over arbitrary Herdr workspaces
on day one. The trial should show only Sidecar-owned Herdr workspaces by default,
with a separate opt-in to adopt existing ones. Ownership must be explicit so
Sidecar never closes a user's unrelated Herdr workspace during cleanup.

## What Herdr actually provides

The inspected Herdr revision is a single Rust binary with two persistent local
interfaces:

- a detached server owns PTYs and application state;
- terminal clients attach over a private framed client protocol;
- a newline-delimited JSON socket API supports automation and event streams;
- a documented `terminal session control` bridge exposes one pane as a live
  ANSI frame stream with bidirectional JSON control;
- `herdr` CLI commands wrap that API.

The API is materially broader than the earlier persistence-only research
suggested. Its generated schema and source include:

- `session.snapshot` for a complete bootstrap model;
- workspace create/list/get/focus/rename/move/close and metadata reporting;
- worktree list/create/open/remove;
- tab create/list/get/focus/rename/move/close;
- pane split/list/get/focus/rename/close, layout operations, resize, process
  information, input, and terminal reads;
- agent list/get/read/explain/focus/start/prompt/wait;
- event subscriptions and waits, including agent-status changes;
- pane metadata and agent-session reporting.

`pane.read` can return visible or recent terminal content, with ANSI retained
when requested. `pane.send_text`, `pane.send_keys`, and `pane.send_input` cover
interactive input. `session.snapshot` includes stable-looking workspace, tab,
pane, layout, agent, worktree, focus, revision, and status records. Herdr's
agent states map closely to Sidecar's status columns.

Most importantly for Sidecar, Herdr 0.8.0 documents
`herdr terminal session control <target> [--takeover] [--cols N] [--rows N]`
for interactive bridge processes. It emits newline-delimited `terminal.frame`
records containing base64-encoded ANSI bytes plus frame sequence, exact width,
height, and full-versus-delta state. It accepts newline-delimited commands for:

- `terminal.input` with UTF-8 text or arbitrary base64 bytes;
- `terminal.resize` with absolute columns, rows, and optional cell pixel size;
- `terminal.scroll` with direction, line count, mouse position, modifiers, and
  wheel-versus-page-key source;
- `terminal.release` for clean controller shutdown.

Herdr has a separate read-only `terminal session observe` stream that permits
multiple observers. Exactly one controller owns input and resize; `--takeover`
replaces it explicitly. This is a supported, documented bridge boundary, not
the private bincode client protocol.

Herdr also implements capabilities Sidecar would otherwise have to maintain:

- direct PTY ownership on Unix and ConPTY support on Windows;
- a real terminal model based on vendored `libghostty-vt` rather than captured
  screen text plus a synthetic cursor;
- daemon/client detach and reconnect, including SSH-oriented remote attach;
- multi-client coordination and resize behavior;
- full-screen agent transcript reading;
- agent detection manifests and native session identity integrations;
- persisted session snapshots and live server handoff during upgrades.

These are strong reasons to integrate rather than port Herdr's internals into
Go. Apache-2.0 permits reuse, but a fork or FFI extraction would create a large
Rust/Go maintenance boundary while discarding the upstream daemon/API seam.

## Verified against a running Herdr 0.8.0

The claims above were re-checked against a real server, started in an isolated
named session (`HERDR_SESSION=sidecar-probe`) so nothing touched a live setup.
Workspaces were created, a `terminal session control` bridge was driven with
input/resize/scroll/release, and the emitted frames were decoded and classified.
Results below supersede the earlier source-only reasoning where they conflict.

### The ANSI frame dialect is a closed cell-blit set, not free-form terminal output

This is the most important finding, and it makes Risk #1 much smaller than the
rest of this document assumed. Every byte Herdr emits over the bridge — during
a shell prompt, during `less` on the alternate screen, during resize, during
`terminal.scroll` — comes from this set and nothing else:

| Sequence | Meaning |
| --- | --- |
| `CSI row;col H` | absolute cursor position before each run of cells |
| `CSI …m` | SGR, fully re-specified per run (no inherited state) |
| `CSI ?25 h/l` | cursor visibility |
| `CSI ?2026 h/l` | synchronized-update begin/end wrapping every frame |
| `OSC 8 ;; ST` | hyperlink set/reset |
| `CSI 2J` | erase display, once, at the start of a `full` frame |

There are no scroll regions, no `IND`/`RI`, no `DECSC`/`DECRC`, no wrap-mode
dependence, no alternate-screen switches, and no relative cursor motion. Herdr
resolves all of that server-side in `libghostty-vt` and ships the resulting grid
as absolutely-positioned blits. Consuming it is a bounded applier over a cell
buffer — Sidecar already depends on `charmbracelet/x/ansi`, `x/cellbuf`, and
`ultraviolet`, which cover parsing, cell storage, and rendering. **Sidecar does
not need to build or vendor a VT emulator.**

`RenderEncoding::SemanticFrame` does already exist in `src/protocol/wire.rs`
alongside `TerminalAnsi`, so the upstream "expose semantic frames through the
bridge" ask remains cheap if it is ever wanted. It is now an optimization, not
a prerequisite.

### Frame cost

| Case | Size |
| --- | --- |
| Full 80×24 frame | ≈ 36 KB (~23 bytes/cell) |
| Full 100×30 frame | ≈ 56 KB |
| Typical delta (a command and its output) | 70–600 bytes |
| Idle | zero frames |

Deltas are cheap and idle cost is genuinely nothing, but **every
`terminal.resize` forces a full frame**. Sidecar's drag-to-resize pane
interaction would emit a full repaint per mouse step; resize must be debounced
and coalesced at the adapter, not passed through.

### `seq` is not a gap detector

Consecutive frames were observed carrying the same `seq` with identical
payloads, so `seq` does not increment per emitted frame and gaps cannot be
inferred from it. This is not a problem: the bridge is an ordered stream over a
Unix socket, so there is no lossy path. Loss means disconnect, which means the
controller process exits. `full: true` is the resync signal; treat `seq` as a
dedupe hint only.

### Errors are process death with an English reason string

Every bridge failure arrives the same way: the server sends `ServerShutdown`,
the CLI prints one `{"type":"terminal.closed","reason":…}` line, and the process
exits. Observed and source-confirmed reasons include `detached`,
`terminal <id> not found`, `terminal <id> already has an attached client; retry
with --takeover`, `terminal <id> has a read in progress; retry`, and
`terminal attach taken over`. Sidecar must respawn and classify by string match.

Two consequences: a concurrent `pane.read` on the same terminal transiently
rejects attach (`pending_alt_screen_reads`, `headless.rs:2608`), so the adapter
needs bounded retry; and a stable machine-readable `code` on `terminal.closed`
is a good, small upstream request.

### Graphics are silently dropped through the bridge

`write_terminal_session_output` matches `ServerMessage::Graphics { .. } => {}`
(`src/client/mod.rs:981`). Kitty/sixel image output produced inside a
Herdr-hosted pane never reaches a bridge client. There is a separate
`pane.graphics.*` API and `api/server/pane_graphics_stream.rs`. Sidecar renders
images outside the pane today (`go-termimg`, `x/mosaic`), so this is limited,
but any pane-hosted image path is not available through the bridge.

### Identity is server-allocated and index-shaped

Observed IDs: `workspace_id` `w1`, `tab_id` `w1:t1`, `pane_id` `w1:p1`, and
`terminal_id` `term_6588bf917ff9e1`. Workspace numbers increase monotonically
within a server lifetime and are not reused after a close (closing `w2` then
creating gave `w3`). `terminal_id` is opaque and random-looking; the composite
`wN:pM` reads as a resolvable handle, not a durable key.

Recommendation: key Sidecar's durable mapping on `terminal_id`, and treat
`wN:pM` as a lookup handle that must be re-resolved from `session.snapshot` on
every startup. Whether either survives a server restart or live handoff is still
open and belongs in Phase 0.

### Ownership metadata does not survive a server restart

`pane.report_metadata` / `workspace.report_metadata` accept `tokens` with **at
most 16 keys**, key pattern `^[A-Za-z0-9_-]{1,32}$`, string values, optional
`ttl_ms` from 1 ms to 24 h, and a `seq` guard capped at 32 distinct `source`
values (`metadata_tokens.rs:15`). Tokens without a TTL persist for the server's
lifetime — but `persist/restore.rs:421` reinitialises `metadata_tokens` to
default on restore.

This closes the open question the document raised: **tokens are volatile.**
Sidecar's own inspectable mapping is authoritative; tokens are a cache Sidecar
re-publishes after each server start so that `herdr` CLI users can see which
panes Sidecar owns.

### Multi-client geometry resolves in Sidecar's favour

The bridge handshakes with `direct_attach_requested = true`, and
`headless.rs:2827` only promotes non-direct-attach clients to foreground, so a
Sidecar controller never becomes the foreground client and never joins shared
size arbitration. Attaching inserts the terminal into
`direct_attach_resize_locks` (`headless.rs:2671`), and Herdr's own UI skips
resizing any locked terminal (`ui/panes.rs:189`, `:206`). The lock is released
on disconnect (`headless.rs:1499`).

So the outcome is not resize thrash: **Sidecar wins the PTY size, and a native
Herdr client displaying the same pane renders a mis-fitted pane for as long as
Sidecar holds it.** That is the correct default for an embedded client, but it
means "Open in Herdr" should release Sidecar's controller first.

### Protocol version churn is already observable

The installed `herdr 0.8.0` server reports `"protocol": 19`. The schema at repo
HEAD (`docs/next/api/herdr-api.schema.json`) declares `"protocol": 20`,
`"schema_version": 1`. The wire protocol was bumped between the released binary
and current `main`, inside one published version number.

Pin and negotiate on the **`protocol` integer**, never the version string.
`cli/protocol_guard.rs` already returns a `protocol_mismatch` error code with
directional guidance (client newer → restart server; client older → upgrade
client), which is exactly the diagnostic Sidecar should surface.

### Session and socket topology

Each Herdr session owns two sockets in its data directory:

- `herdr.sock` — the newline-delimited JSON API;
- `herdr-client.sock` — the framed client protocol the bridge speaks.

The session is chosen by `--session NAME` or `HERDR_SESSION`, overridable
wholesale by `HERDR_SOCKET_PATH`. The default session lives at
`~/.config/herdr`; named sessions at `~/.config/herdr/sessions/<name>`.

**Open decision this document previously skipped:** does Sidecar attach to the
user's default session, or run its own `HERDR_SESSION=sidecar`? A dedicated
session gives perfect ownership isolation and makes cleanup unambiguous, but the
user's plain `herdr` command will not show Sidecar's work, which breaks the
"Open in Herdr" escape hatch and the agent-parity story. Recommendation: use the
default session and rely on explicit ownership tokens plus Sidecar's own
mapping, with a config escape to a named session for people who want separation.

### There is no `herdr server start`

`herdr server` runs the headless server in the foreground. The daemonizing path
(`server/autodetect.rs::spawn_server_daemon`, `setsid` + `/dev/null` stdio) runs
only from a bare `herdr` attach, which needs a TTY. Sidecar must spawn
`herdr server` detached itself and poll for socket readiness. Concrete, small,
but it is a real step 1 rather than a call into an existing command.

### API surface the earlier survey missed

Beyond the methods already listed, these matter for Sidecar:

- `layout.export` / `layout.apply` / `layout.set_split_ratio` — see the argv gap
  below; `layout.apply` is the only way to launch a pane with a command;
- `agent.wait { target, until: [status…], timeout_ms }` — a direct fit for
  "tell me when this agent goes idle or blocked", replacing polling;
- `pane.wait_for_output` and the `pane_output_changed` event;
- `agent.send_keys`, `agent.rename`, `pane.release_agent`,
  `pane.clear_agent_authority` — agent-authority handoff Sidecar will need if it
  ever launches an agent Herdr also detects;
- `pane.zoom`, `pane.swap`, `pane.edges`, `pane.neighbor`, `pane.input.set`;
- `notification.show`, `server.reload_config`, `server.live_handoff`;
- `plugin.link/enable/disable/list` — Herdr has its own pane-level plugin
  system and marketplace. Worth naming only to dismiss: shipping Sidecar as a
  Herdr plugin would put Sidecar inside Herdr's TUI and reintroduce exactly the
  nested-product problem this document rejects.

### The argv gap: panes cannot be created with a command

`workspace.create`, `tab.create`, and `pane.split` accept `cwd`, `env`, `label`,
`focus` (and split geometry) — **and no command**. They start the configured
shell. The only API that accepts a program is `layout.apply`, whose
`LayoutNode::Pane` carries `command: [String] | null` alongside `cwd`, `env`,
and `label`. `agent.start { name, kind, args, pane_id, timeout_ms }` launches a
manifest-known agent into an *existing* pane.

Sidecar launches arbitrary argv today (`tmux new-session -d <cmd>`), and its
prompt/agent launch flow depends on it. So the steel thread's "create a shell"
step must go through `layout.apply`, not `workspace.create`, or accept a
shell-plus-`send_text` shape that is racy, pollutes shell history, and cannot
express a non-shell program. This is the single largest behavioural gap found,
and Phase 0 must prove argv/env/cwd fidelity through `layout.apply` before
anything else is built on top.

### Scrollback cannot be windowed absolutely

`pane.read` takes `source: visible | recent | recent_unwrapped | detection`,
`lines: N`, `format: text | ansi`, `strip_ansi`. `N` counts backwards from the
end (`history_read.rs:89`, `rows.len().saturating_sub(lines)`). There is **no
offset or range**, so "give me lines 5000–5050 of a 100k-line buffer" is not
expressible. Retention is a Herdr config concern
(`scrollback_limit_bytes`, default 10 MB per pane), not Sidecar's.

Sidecar's absolute lazy-history and frozen-selection contracts therefore cannot
be ported directly. History becomes either server-side scrolling via
`terminal.scroll` reflected in ordinary frames, or "last N lines" pulls. This is
the most likely place to lose real behaviour, and it lands squarely in Phase 3.

## The product boundary

Herdr and Sidecar overlap, but they are not substitutes at the product level.

| Concern | Owner after migration |
| --- | --- |
| PTYs, terminal emulation, scrollback, process lifetime | Herdr |
| Workspaces, tabs, panes, agent runtime state | Herdr |
| Git worktree creation/removal | Initially Sidecar; evaluate Herdr later |
| Task linking, prompts, merge flow, diffs, git status | Sidecar |
| File browser, notes, conversations, other plugins | Sidecar |
| Workspace presentation and cross-plugin navigation | Sidecar |
| Runtime CLI/API for agents | Herdr |
| Sidecar-specific metadata and policy | Sidecar, projected onto Herdr IDs |

The deliberate exception is worktree ownership. Herdr has worktree APIs, but
Sidecar already owns a mature lifecycle involving `td`, prompt launch, base
branches, merge/push, cleanup, and cross-plugin navigation. The first version
should preserve that behavior and ask Herdr only to host a terminal in the
resulting checkout. Moving worktree creation to Herdr is a later, separately
evaluated change; combining it with the PTY migration makes failures hard to
attribute and risks losing Sidecar behavior.

## Recommended architecture

Introduce a narrow terminal-runtime seam based on Sidecar's needs, not on tmux
or Herdr nouns:

```text
Workspace plugin
  ├── Sidecar worktree/task/git core (unchanged)
  └── TerminalRuntime
        ├── TmuxRuntime (current behavior)
        └── HerdrRuntime
              └── long-lived Unix-socket JSON client -> Herdr server
```

The interface should cover the behavior Sidecar owns:

```go
type TerminalRuntime interface {
    Ensure(ctx context.Context) error
    Snapshot(ctx context.Context) (RuntimeSnapshot, error)
    Start(ctx context.Context, StartSpec) (TerminalRef, error)
    Read(ctx context.Context, TerminalRef, ReadSpec) (TerminalFrame, error)
    Send(ctx context.Context, TerminalRef, Input) error
    Resize(ctx context.Context, TerminalRef, Size) error
    Close(ctx context.Context, TerminalRef) error
    Events(ctx context.Context) (<-chan RuntimeEvent, error)
}
```

This is illustrative, not an instruction to wrap every existing tmux helper.
`TerminalRef` must be opaque outside the adapter. Sidecar state should store a
runtime kind plus an opaque ID, never assume a tmux session name or Herdr pane
ID. Pure viewport, selection, link, and key-normalization logic can remain in
Sidecar where it expresses Sidecar's experience rather than backend behavior.

The Herdr adapter should use the JSON socket API for bootstrap, lifecycle,
metadata, and events, plus one long-lived `herdr terminal session control`
process for each actively interactive Sidecar terminal. Input is written to the
controller's stdin and frames are read from stdout; this is not one subprocess
per keystroke or frame. Hidden panes can use snapshots/events or read-only
observers according to measured cost.

Do not speak Herdr's private terminal-client protocol. Herdr's documented
terminal-session bridge already adapts that protocol into a JSON-lines/ANSI
process boundary for third-party clients. If Sidecar eventually needs semantic
cell/cursor frames rather than ANSI, request a documented bridge encoding
upstream rather than copying Herdr's Rust/bincode wire types.

## A brand-new Workspaces tab as the steel thread

The safest first release is a separate plugin ID and config flag, for example:

```json
{
  "features": {
    "herdr_workspaces": true
  },
  "plugins": {
    "herdr-workspace": {
      "binary": "herdr",
      "adoptExisting": false
    }
  }
}
```

Its smallest real journey:

1. Detect `herdr` on `PATH`, connect to `herdr.sock`, compare the reported
   `protocol` integer, and — if nothing is listening — spawn `herdr server`
   detached and poll for socket readiness. None of this blocks Sidecar's first
   frame. There is no `herdr server start`; Sidecar owns the daemonizing.
2. Create a Sidecar-managed workspace in the current checkout. For a plain
   shell, `workspace.create` with `cwd`/`env` is enough. For anything with an
   explicit program, this must go through `layout.apply` — `workspace.create`,
   `tab.create`, and `pane.split` take no command.
3. Persist a Sidecar mapping keyed by `terminal_id`; this is the authority.
   Additionally publish `pane.report_metadata` ownership tokens for visibility
   from `herdr`'s own CLI, and re-publish them whenever the server restarts,
   because tokens do not survive restore. Do not identify ownership by labels.
4. Bootstrap via `session.snapshot`, subscribe to events, and display only the
   owned workspace in Sidecar's familiar sidebar.
5. Start `terminal session control` for the selected pane, apply its full/delta
   ANSI frames to an embedded cell buffer, and forward text, raw keys, resize
   (debounced), paste, and scroll commands. Treat `terminal.closed` as process
   death requiring respawn, and classify its `reason` string. Show Herdr agent
   status from the API.
6. Exit Sidecar while a command continues, restart it, and restore the view from
   the Herdr server.
7. Offer “Open in Herdr,” releasing Sidecar's controller first so the native
   client can size the pane itself.

This first slice intentionally omits worktree creation, task linking, merge,
inline file editing, multi-pane Herdr layouts, arbitrary existing Herdr
workspaces, and automatic migration. It still proves the central claim:
Sidecar can be a transparent client of a persistent Herdr-owned PTY.

Once that works, bring over one existing journey at a time rather than cloning
the whole workspace plugin up front:

1. Sidecar-created worktree + agent launch.
2. Restore after Sidecar restart and SSH disconnect.
3. Multiple shells/agents and status-driven sidebar/Kanban.
4. Terminal history, search, selection, links, mouse, and terminal panel.
5. Merge/push/delete flows with ownership-safe cleanup.
6. File-browser inline editor, which also currently depends on `internal/tty`.

## Mapping current tmux behavior to Herdr

| Current Sidecar behavior | Herdr path | Main question |
| --- | --- | --- |
| `tmux new-session -d <cmd>` | `layout.apply` with a `LayoutNode::Pane` carrying `command`/`cwd`/`env`; `workspace.create` only for shell panes | **`workspace.create`/`tab.create`/`pane.split` accept no command.** Which Herdr object is one Sidecar shell? Start with one pane per workspace. |
| Session/pane discovery | `session.snapshot`, list/get calls | Key durable state on `terminal_id`; `wN:pM` is a handle, not a key. Ownership tokens are volatile and must be re-published after each server start. |
| `capture-pane` and control-mode output | `terminal session control` frame stream; `pane.read` for snapshots/history | Measured: idle emits nothing, deltas are 70–600 B, full frames ~36 KB at 80×24. `seq` repeats and cannot detect gaps; `full: true` is the resync signal. |
| `send-keys` / literal input | `terminal.input` text or arbitrary bytes | Bubble Tea keys can be encoded exactly as today; verify IME and Kitty-keyboard cases in Sidecar. |
| Cursor metadata and synthetic cursor | ANSI terminal frames generated from Herdr's cursor-aware model | Cursor position arrives as `CUP` before each run plus `DECSET ?25`; Sidecar applies them to a cell buffer rather than flattening frames as text. |
| Pane/window resize | `terminal.resize` with absolute cols/rows/cell pixels | Confirmed: the controller takes `direct_attach_resize_locks` and owns the PTY size outright. Every resize costs a full frame, so debounce drag-resize in the adapter. |
| Scrollback ranges | `pane.read` recent/recent-unwrapped, or server-side `terminal.scroll` | **No absolute offset exists** — `lines: N` counts back from the end only. Sidecar's absolute lazy-history and frozen-selection contracts cannot port as-is. |
| Mouse forwarding | `terminal.scroll` for wheel/page scrolling; raw `terminal.input` bytes for application mouse events | Sidecar can reuse its existing SGR mouse encoding initially; a structured mouse bridge would be cleaner upstream. |
| Bracketed paste/mode detection | raw `terminal.input` bytes | Sidecar can send the exact bracketed-paste sequence it sends today; Herdr's terminal model still supplies screen fidelity. |
| Agent activity heuristics | `agent_status`, `pane_agent_status_changed`, and `agent.wait` | Map `unknown` conservatively; do not invent certainty. `agent.wait { until, timeout_ms }` replaces polling for "notify me when this goes idle/blocked". |
| Native `tmux attach` | launch/attach Herdr client and focus target | Confirm a stable focus-and-attach command. Sidecar must release its controller first, or Herdr's UI will render the pane at Sidecar's locked size. |
| Orphan/session watcher | snapshot and lifecycle events | Reconcile after event-stream reconnect with a fresh snapshot. |
| `sidecar-edit-*` inline editor sessions | dedicated Herdr workspace/tab/pane | Must preserve temporary ownership and close-on-exit semantics. |
| Geometry lease for multiple clients | Herdr multi-client server | Resolved: bridge clients handshake as direct-attach, never become foreground, and never join shared size arbitration. Sidecar's own geometry lease becomes unnecessary. |

The source, the documented bridge, and the live smoke test resolve the earlier
largest unknowns: **absolute sizing and raw interactive input are directly
supported, the stream is generated from Herdr's cursor-aware terminal renderer,
and the emitted dialect is a closed cell-blit set rather than free-form terminal
output.** Applying full/delta frames to an embedded Bubble Tea viewport is a
bounded applier over `x/ansi` plus a cell buffer, not a second terminal
emulator. The genuinely open items have shifted: argv-on-pane-creation, absolute
scrollback windowing, and ID durability across server restart.

## State and identity

Sidecar currently has several overlapping identities: worktree name/path,
manifest shell ID, tmux session name, pane ID, agent record, and transient UI
selection. A Herdr migration is a chance to stop leaking runtime identifiers
through the workspace model.

Persist only:

- Sidecar workspace/shell identity and ownership;
- runtime kind (`tmux` or `herdr` during migration);
- opaque runtime workspace/pane IDs;
- Sidecar-owned metadata such as linked task and launch specification.

On startup:

1. load Sidecar's durable mappings;
2. fetch `session.snapshot`;
3. reconcile by opaque IDs and ownership metadata;
4. mark missing runtime objects as stopped/orphaned without silently recreating
   or deleting them;
5. subscribe to events;
6. replace the cache with a fresh snapshot after reconnect or sequence gaps.

Herdr's metadata tokens are useful for ownership *advertisement* but not for
ownership *truth*: they are capped at 16 keys per object, optionally TTL'd, and
discarded on server restore (`persist/restore.rs:421`). Keep the authoritative
mapping in Sidecar's existing inspectable state and re-publish tokens after each
Herdr server start so `herdr` CLI users can still see what Sidecar owns. Labels
and cwd are presentation data, not identity.

Prefer `terminal_id` (`term_<hex>`) as the durable key. `wN:pM` pane and tab IDs
are server-allocated and index-shaped; they are stable enough to use as handles
within a server lifetime but should be re-resolved from `session.snapshot` on
every startup, and their behaviour across restart and live handoff is a Phase 0
question.

There is no credible in-place conversion of a live tmux PTY into a Herdr PTY.
The migration policy should therefore be **drain, not convert**:

- new sessions use Herdr after opt-in/default change;
- existing tmux sessions remain visible through `TmuxRuntime` until closed;
- Sidecar can offer a manual “restart in Herdr” action for resumable agents, but
  it must explain that process-local state may be lost;
- removing the final tmux adapter happens only after no supported workflow or
  durable record still depends on it.

## Installation and failure behavior

Transparent product behavior still requires an explicit dependency policy.
Recommended initial policy:

- do not vendor or auto-download Herdr from inside Sidecar;
- discover `herdr` on `PATH` asynchronously and report its exact version and
  protocol in diagnostics;
- provide Homebrew/install guidance when the feature is enabled but missing;
- pin a tested Herdr version range or protocol/schema fingerprint;
- fail the Herdr tab locally, leaving the rest of Sidecar usable;
- never fall back from a requested Herdr workspace to a new tmux workspace
  silently, because that creates two sources of truth;
- keep config and launch state provider-neutral above the adapter.

Before making Herdr the default, decide whether Sidecar's Homebrew formula should
depend on `herdr`. A hard package dependency is simpler for users but couples
Sidecar releases to Herdr packaging. A soft dependency preserves choice but
makes the first-run path less transparent. The trial should remain soft; real
usage data can justify the packaging decision.

Herdr server upgrades are also operationally relevant. Current Herdr includes
protocol checks and live server handoff, but Sidecar must treat protocol mismatch
as a recoverable diagnostic, never stop a running Herdr server automatically.
That server may own work unrelated to Sidecar.

## What eventually disappears

If the migration reaches full parity, much of this code becomes unnecessary:

- tmux process wrappers in `internal/tty/session.go`, capture range, cursor
  query, paste, send queue, and terminal-mode scanning;
- tmux control protocol parsing, control clients, polling fallback, and mailbox
  orchestration;
- tmux geometry leasing and pane-fit compensation;
- tmux-specific workspace shell/agent creation, discovery, watchers, isolation
  tests, and native attach behavior;
- tmux-backed file-browser editor sessions;
- the external `tmux` runtime requirement for product functionality.

Not all of `internal/tty` should disappear. Key normalization, viewport
selection, ANSI-aware slicing, search/link behavior, and Bubble Tea integration
may remain useful, though they should move into backend-neutral packages only
after both implementations expose the correct boundary. Prematurely extracting
them will freeze today's tmux-shaped assumptions into the new adapter.

The repository's tmux-based headless test driver is a separate concern: it hosts
Sidecar itself for automated verification. It does not have to disappear merely
because Sidecar stops using tmux as its embedded terminal runtime.

## Risks and stop conditions

### 1. Sidecar still needs an ANSI-frame consumer — but a small one

*Downgraded after measurement.* ANSI bytes are ideal for a real terminal, and
Sidecar renders a terminal inside a larger Bubble Tea frame, so concatenating
cursor-control sequences into a Lip Gloss string will not be correct. Sidecar
does need a virtual screen consumer.

What changed is its size. The dialect Herdr actually emits is `CUP` + `SGR` +
`DECSET ?25/?2026` + `OSC 8` + one `CSI 2J`, and nothing else, even under
alternate-screen applications — see the verification section. That is an
absolutely-positioned blit into a cell grid, which Sidecar can apply with
`charmbracelet/x/ansi` over `x/cellbuf`/`ultraviolet` buffers it already
depends on.

**Stop condition (unchanged in spirit, unlikely to fire):** if consuming the
bridge ever requires Sidecar to build and maintain a general-purpose terminal
emulator comparable to Herdr's, ask upstream to expose `RenderEncoding::
SemanticFrame` — which already exists in `src/protocol/wire.rs` — through the
bridge. Do not replace one terminal-maintenance burden with another.

### 1b. Scrollback and history are the real fidelity risk

The risk that deserves Risk #1's old weight is history. `pane.read` cannot
window absolutely, so Sidecar's lazy-history, frozen-selection, and search
behaviours have no direct translation. Unlike the ANSI question, this is not
resolved by writing a small adapter — it is either an upstream API addition
(offset/range on `pane.read`) or a deliberate downgrade of Sidecar behaviour.

**Stop condition:** if Phase 3 cannot reproduce history browsing and search
without holding a full parallel scrollback copy in Sidecar, stop and negotiate
the API upstream before continuing to Phase 4.

### 2. API/version churn

The inspected repository is active and the API types are `pub(crate)` inside a
binary crate. A generated JSON schema exists, but Sidecar needs an explicit
compatibility contract rather than compiling against Rust source.

This is not hypothetical: released `0.8.0` speaks protocol `19` while repo HEAD
declares protocol `20`. The wire protocol moved inside one published version.

**Mitigation:** negotiate on the `protocol` integer rather than the version
string, surface `protocol_mismatch` as an actionable diagnostic, keep golden
protocol fixtures generated from a pinned schema, tolerate additive fields, and
test the oldest/newest supported Herdr releases.

### 3. Conflicting concepts and ownership

Both products have workspaces, worktrees, tabs, focus, metadata, shortcuts, and
agent launch flows. Mirroring both models bidirectionally would create loops and
surprising cleanup.

**Mitigation:** Herdr owns runtime topology; Sidecar owns its presentation and
workflow metadata. Sidecar issues commands and consumes authoritative events. It
does not continuously force Herdr to match an independent layout model.

### 4. Dependency and trust expansion

Sidecar would execute an external daemon that owns long-lived shells and agent
credentials. Herdr's Apache-2.0 license is compatible with integration, but
binary provenance, update policy, socket permissions, and command/environment
handling become part of Sidecar's security floor.

**Mitigation:** use the user's installed binary, verify versions, preserve
Herdr's private local socket boundary, pass argv/env structurally, and document
which process owns sessions. Do not expose the socket remotely through Sidecar.

### 5. Feature regression hidden by a prettier architecture

Sidecar's tmux layer contains years of behavior around ordering, stale polls,
selection, geometry, history, multi-client conflicts, and full-screen TUIs.
Deleting it because Herdr has similarly named primitives would repeat those
bugs at the seam.

**Mitigation:** use behavior-parity fixtures and real app journeys, not a file
deletion target, as the migration gate.

## Phased delivery and gates

### Phase 0: compatibility spike

Build a small non-production Go bridge client against Herdr 0.8.0 or newer.
Herdr's existing source/tests, plus the smoke test recorded above, already
establish that its runtime and bridge support the required terminal behaviors.
The Sidecar spike now needs to prove the integration boundary, in this order —
the first two items are the ones that can still kill the plan:

1. **argv fidelity through `layout.apply`.** Launch a real agent command with
   exact argv, env, and cwd; confirm it matches what Sidecar does with
   `tmux new-session -d` today, including quoting and non-shell programs.
2. **history and search.** Determine whether Sidecar's history/search/selection
   journeys can be served by `terminal.scroll` plus `pane.read`-from-the-end,
   or whether they require an upstream offset/range parameter.
3. ID durability across `herdr server` restart and `server.live_handoff`: do
   `terminal_id` and `wN:pM` survive, and does Sidecar's mapping reconcile?
4. Asynchronous server detection, detached spawn, and protocol negotiation
   against a deliberately mismatched protocol integer.
5. Snapshot + event resynchronization after an event-stream disconnect.
6. Correct application of full and delta frames — cursor, wide characters,
   colors, hyperlinks, and alternate-screen apps — inside Sidecar's rectangle.
7. Multiline/bracketed paste, modified keys, Unicode/IME, and mouse.
8. Sidecar exit/restart and SSH disconnect while the process continues.
9. Behaviour with an independently attached Herdr client, given that Sidecar
   holds the resize lock for panes it controls.

Exit gate: a recorded matrix identifies which behaviors work through the
documented terminal-session bridge and which need an upstream change. Herdr's
terminal capability is no longer the open question; command launch, history
windowing, and identity durability are.

### Phase 1: experimental Herdr Workspaces plugin

Ship the one-shell journey behind an explicit feature flag. Keep its files and
state separate from the current plugin. Add clear diagnostics and “Open in
Herdr.” Collect latency, CPU, reconnect, and failure evidence.

Exit gate: daily use can create, interact with, detach from, and restore a shell
without reaching for tmux or Herdr's UI for normal operation.

### Phase 2: Sidecar worktrees and agents

Reuse Sidecar's worktree/task/prompt core, launch agents into Herdr panes, and
project Herdr status/events into list and Kanban views. Preserve all merge and
cleanup rules. Do not yet adopt arbitrary Herdr worktrees.

Exit gate: the full create-agent-review-diff-merge/delete journey passes in the
real app, including crash/restart and ownership-safe cleanup.

### Phase 3: terminal feature parity

Port terminal panel, history/search/selection/links, mouse, native attach, and
file-browser inline editing. Consolidate the two plugins over the runtime seam
only after the Herdr behavior is proven.

Exit gate: the parity matrix is green and tmux has no uniquely supported
product workflow.

### Phase 4: default and drain

Make Herdr the default for new sessions. Continue showing existing tmux-backed
sessions and provide explicit restart/drain tools. Publish dependency,
troubleshooting, downgrade, and recovery documentation.

Exit gate: at least one release cycle shows no need to create new tmux sessions
and rollback remains tested.

### Phase 5: remove the tmux runtime

Delete `TmuxRuntime` and tmux-specific product code, retain any tmux-based
testing harness that is still useful, and remove the runtime dependency from
installation/docs.

Exit gate: clean install, local use, remote SSH use, Sidecar restart, Herdr
restart/handoff, and independently attached Herdr client all pass consumer-level
tests. Removal gets an independent review separate from feature implementation.

## Acceptance matrix for the spike

| Journey | Evidence |
| --- | --- |
| First launch | Sidecar paints before Herdr discovery; missing binary produces actionable UI |
| Shell creation | Correct cwd, argv, env, label, ownership, and returned opaque IDs — argv verified through `layout.apply`, since no create/split call accepts a command |
| Agent launch | Claude Code and Codex receive the exact prompt/flags and report honest state |
| History | Sidecar's scrollback, search, and selection journeys survive with no absolute `pane.read` offset available |
| Interactive typing | Printable, special, modified, rapid, Unicode, IME, and paste input remain ordered |
| Rendering | Cursor, colors, wrapping, resize, alternate screen, and wide characters match direct Herdr |
| Mouse/scroll | Wheel, click, selection, application mouse mode, and history work without cross-pane leakage |
| Persistence | Command survives Sidecar exit and SSH loss; relaunch restores identity, output, and status |
| Multi-client | Sidecar and a Herdr client can observe/control the same pane; releasing Sidecar's controller restores Herdr's own sizing |
| Events | Disconnect/reconnect and missed events converge through a new snapshot |
| Cleanup | Sidecar closes only objects it owns; unrelated Herdr workspaces are untouched |
| Upgrade mismatch | Sidecar reports incompatibility without killing or upgrading the Herdr server |
| Rollback | Disabling the feature leaves current tmux workflows and state intact |

Measure input-to-visible latency, idle CPU, subprocess count, startup latency,
and memory against today's control-mode path. Herdr should not be adopted on
architecture appeal alone; the replacement should make the actual experience
more reliable or simpler while meeting terminal fidelity.

## The case against, stated honestly

This document is written as an advocacy piece, and the counterargument deserves
to be on the page rather than left to the reader.

**The dependency trade is worse than the architecture diagram suggests.** tmux
is fifteen years old, present or one package manager away on every machine
Sidecar runs on, and has a wire contract that has not meaningfully broken in a
decade. Herdr is at 0.8.0, single-vendor, must be installed separately, and
already bumped its wire protocol between the released binary and `main`.
Sidecar's stated design principle is that it is a *presentation-layer* tool that
owns none of its data — which is exactly the kind of tool that should be
conservative about acquiring a fast-moving runtime dependency.

**The code being deleted is not that large.** `internal/tty` is about 5,000
lines of non-test Go, with another 4,000 in tests. It works, it is well
covered, and its bug rate is not the thing holding Sidecar back. Trading 5,000
lines of owned, stable code for a versioned dependency on an external daemon is
not self-evidently a win, and "what eventually disappears" reads more
attractively than the ledger actually is.

**Some of the claimed wins are already had.** Persistence across SSH loss and
laptop close is what tmux is *for*; Sidecar has it today. The genuinely new
wins are narrower than the framing implies: terminal fidelity from a real VT
model instead of `capture-pane` plus a synthetic cursor, and first-class agent
state instead of output-timing heuristics. Those are real and worth something.
They are not "much better persistence."

**And there is a concrete known regression.** Absolute scrollback windowing has
no API, so history browsing and search are, today, a downgrade.

None of this makes the plan wrong. It makes the *framing* wrong: this should be
pursued as "can Herdr give Sidecar a better terminal and honest agent state,"
not as "replace tmux." The tmux removal is a possible consequence several phases
later, not the goal.

## Recommendation

Proceed with Phase 0 and, if its gates pass, a separate Herdr-backed Workspaces
tab. Scope the first epic to **Phase 0 and Phase 1 only**, and do not carry the
"replace tmux" framing into it.

Evidence is now strong enough to start. Herdr publishes the bridge Sidecar
needs, and the frame dialect turns out to be a closed cell-blit set that Sidecar
can consume with libraries it already depends on — the question this document
originally called decisive is largely answered. Embedding the Herdr TUI, or
shipping Sidecar as a Herdr plugin, would both produce a nested product with
conflicting navigation. The correct strategic bet remains Herdr as a
daemon/runtime adapter with the terminal-session controller as the interactive
data plane.

Do **not** commit to deleting tmux, and do not take a hard Homebrew dependency
on `herdr` at any point in Phases 0–3.

The upstream requests worth opening, in descending order of leverage:

1. an offset/range parameter on `pane.read`, so history is not a regression;
2. a `command` (and `env`/`cwd`) parameter on `pane.split`/`tab.create`, so
   launching a program does not require `layout.apply`;
3. a stable machine-readable `code` on `terminal.closed`, so error handling is
   not string matching;
4. an optional `SemanticFrame` encoding for `terminal session control` — already
   present in `src/protocol/wire.rs`, merely not exposed at the bridge. Nice to
   have, no longer load-bearing.

## Sources inspected

- Herdr README, Apache-2.0 license, Cargo manifest, generated API schema, socket
  API and automation documentation, and the `src/api`, `src/server`, `src/pty`,
  `src/protocol`, persistence, workspace, pane, and agent modules at the commit
  recorded above.
- Sidecar's `internal/tty`, workspace terminal/control/history/interactive
  paths, file-browser inline editor, startup guidance, and headless testing docs.
- A live `herdr 0.8.0` server run in an isolated `HERDR_SESSION=sidecar-probe`
  session: workspace create/close, `api snapshot`, and a driven
  `terminal session control` bridge (input, resize, scroll, release) with frames
  decoded and their escape-sequence vocabulary classified. The probe session was
  stopped and its data directory removed afterwards.
- [Herdr repository](https://github.com/herdrdev/herdr)
- [Herdr Socket API](https://herdr.dev/docs/socket-api/)
- [Herdr persistence and remote use](https://herdr.dev/docs/persistence-remote/)
