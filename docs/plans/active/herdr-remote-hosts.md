# Herdr as Sidecar's remote host runtime

Status: **active, planning**, 2026-08-28

Supersedes: [Replacing Sidecar's tmux integration with Herdr](../deprecated/replacing-tmux-with-herdr.md) (deprecated) and [Herdr session persistence](../deprecated/herdr-session-persistence.md) (deprecated)
Research input: [What Sidecar can learn from Herdr without replacing tmux](../../research/active/lessons-from-herdr.md)
Herdr source inspected: [`herdrdev/herdr`](https://github.com/herdrdev/herdr) at `c2637dc1` (local checkout `~/code/herdr`)

## Decision first

Sidecar keeps tmux as its local terminal runtime, unchanged. Herdr becomes Sidecar's **on-host agent for machines Sidecar is not running on**.

The deliverable is a **Host** concept in the global Sessions browser: alongside `local`, a user can register `remote:mac-mini`, and Sidecar shows that machine's shells, worktrees, and agent states in the same sidebar, with the same status column, and can open a live pane view of any of them. Everything reaches the remote host over SSH through Herdr's documented CLI and socket API. Nothing about the local experience changes.

This is a symbiosis, not a migration. The two products stop overlapping because they operate on different axes: Herdr owns terminals and agent state on a host, Sidecar owns presentation, git, worktrees, tasks, and cross-host aggregation.

## Scope boundary

**In scope**

- A host registry and a host-aware runtime seam.
- Read-only remote observation: host inventory, agent status, live pane content.
- Interactive input to a remote pane.
- Creating remote shells and agents (Phase C, gated on Phase B evidence).

**Explicitly out of scope**

- Replacing tmux locally. `internal/tty` stays. `geometry_lease.go` stays.
- Herdr as a *local* runtime. Reconsidered only if remote earns it (see [Deferred: Herdr locally](#deferred-herdr-locally)).
- Remote git, remote diffs, remote file browsing, remote worktree creation. Phase A shows a remote host's terminals and agents; it does not project a remote repository into Sidecar's git or files plugins.
- Exposing Herdr's socket to a network. SSH is the only transport, always.
- Sidecar as a Herdr plugin, or Herdr's TUI embedded in Sidecar. Both produce a nested product with conflicting navigation.

## Why remote is the right axis

The deprecated plan stalled on two findings, both re-verified and still true:

1. **argv fidelity.** `workspace.create`, `tab.create`, and `pane.split` accept `cwd`/`env`/`label` but no command. Launching a program requires `layout.apply`.
2. **absolute scrollback windowing.** `pane.read` takes `lines: N` counting back from the end, with no offset or range, so Sidecar's lazy-history, frozen-selection, and search contracts have no direct translation.

Both are **local-parity** problems. They only bite when Herdr has to reproduce something Sidecar already does well. Remote observation has no parity bar: Sidecar currently shows *nothing* for another machine, so anything it shows is a gain, and neither blocker is on the Phase A path.

The other half of the argument is what Herdr uniquely supplies. A remote host needs a process on it that knows what the agents are doing. Herdr is exactly that process, and it already exists.

## What Sidecar structurally cannot do today

Sidecar's agent awareness is local by construction, in three independent places:

| Capability | Implementation | Why it does not cross a host boundary |
| --- | --- | --- |
| Which agent sessions exist, and their transcripts | `internal/adapter/*` reads local stores (`~/.claude/projects`, Codex, opencode, Cursor DBs, …) | Reads local disk. Would need the whole store copied or a remote reader. |
| Whether an agent is working, idle, or blocked | `internal/agentactivity` + `process_identity_darwin.go` | Inspects the local process table. |
| Which shells exist and are alive | `internal/shellliveness`, `internal/tmuxserver`, `shells.json` under `XDG_STATE_HOME` | Local state tree plus a local tmux server. |

And the cross-project model already leaks the local runtime: `workspaceinventory.Workspace` carries `TmuxName` and `PaneID` as fields (`internal/workspaceinventory/inventory.go`). A remote workspace has neither.

So "is the agent on the Mac mini blocked?" is not a question Sidecar can answer without a second Sidecar running over there. Herdr computes that answer on the host where the pane lives, from screen state, using its detection manifests, and returns it as JSON.

The dependency ledger is honest here: this buys a capability Sidecar does not have, rather than trading owned code for a dependency.

## The integration surface

Verified against the local checkout at `c2637dc1` unless marked otherwise.

### Transport: SSH, plus Herdr's own precedent

Herdr's socket API is a Unix domain socket at the session data directory, mode `0600`, never network-exposed (`src/server/socket_paths.rs`). That is correct and Sidecar must not change it. The transport is SSH, invoking the `herdr` CLI on the remote host.

Herdr itself already does this for `herdr --remote user@host`: it spawns `ssh` with a ControlMaster socket named `ctl` and pipes its framed client protocol over stdio (`src/remote/attach.rs`). Sidecar should use ControlMaster multiplexing the same way, so a host costs one authenticated connection and N cheap channels.

Sidecar's path is **simpler than Herdr's own remote attach**, because Sidecar speaks the documented CLI/API boundary rather than the private bincode client protocol. Notably, `ensure_remote_server_running` requires the *local* binary's protocol version to match the remote server's (`src/remote/attach.rs`); going through the CLI, only the remote binary's version matters to Sidecar.

### Live pane content: `terminal session observe` and `terminal session control`

Both are plain stdio processes emitting newline-delimited JSON with base64 ANSI frames.

- `herdr terminal session observe <target> [--cols N] [--rows N]` — read-only (`src/client/mod.rs:960`). Server-side, `observe_terminal_client` sets the connection mode and returns (`src/server/headless.rs:1886`). It does **not** enter `direct_attach_resize_locks`, does not become foreground, and does not claim exclusivity. Input from an observing client is dropped explicitly (`headless.rs`, `ClientConnectionMode::TerminalObserve` branch in `ClientInput`). Multiple observers are allowed.
- `herdr terminal session control <target> [--takeover] [--cols N] [--rows N]` — read/write (`src/client/mod.rs:968`). Reads **plain lines from stdin** in a spawned thread, so it needs no TTY and composes with `ssh` without `-t`. Exactly one controller per terminal; `--takeover` displaces the incumbent. Control routes through `attach_terminal_client`, which does take the resize lock.

That split maps cleanly onto the phases: Phase A uses `observe` and can never disturb a Herdr user looking at the same pane; Phase B upgrades the selected pane to `control`.

Frame properties, from the live probe recorded in the deprecated plan (Herdr 0.8.0, protocol 19) and still the best evidence available:

| Property | Value |
| --- | --- |
| Escape vocabulary | `CUP`, `SGR`, `DECSET ?25`, `DECSET ?2026`, `OSC 8`, one `CSI 2J` per full frame — nothing else, even under alternate-screen apps |
| Full 80×24 frame | ~36 KB |
| Full 100×30 frame | ~56 KB |
| Typical delta | 70–600 B |
| Idle | zero frames |
| Resize | forces a full frame |
| `seq` | repeats; not a gap detector. `full: true` is the resync signal |
| Errors | one `{"type":"terminal.closed","reason":…}` line, then process exit. `reason` is an English string, not a code |

The dialect is absolutely-positioned cell blits, appliable with `charmbracelet/x/ansi` over buffers Sidecar already depends on. This is the same shape as the byte-fed screen model already shipped in `internal/tty/screenmodel`, and the applier should live near it rather than being written twice.

The full-frame-per-resize cost is the reason drag-to-resize must be debounced and coalesced in the adapter, not passed through. Over SSH that matters more than it did locally.

### Discovery and agent state: the JSON API

`src/api/schema.rs` exposes the methods Sidecar needs, including `session.snapshot`, `workspace.list`, `tab.list`, `pane.list`/`pane.get`/`pane.read`, `agent.list`/`agent.get`/`agent.read`/`agent.explain`/`agent.wait`, `worktree.list`, `pane.report_metadata`, `events.subscribe`, `events.wait`, and `pane.wait_for_output`.

`agent.wait { target, until: [status…], timeout_ms }` is a direct fit for "tell me when this agent goes idle or blocked" and replaces polling for a specific target. Herdr's state vocabulary (`idle`, `working`, `blocked`, `done`, `unknown`) maps onto what `internal/agentstatus` already models, with `done` meaning "went idle and nobody has looked," the same distinction Sidecar ported in td-48ecf2.

### The transport gap that shapes Phase 0

**`herdr api` exposes only `snapshot` and `schema`** (`src/cli/api.rs`). There is no generic `herdr api call <method> <json>`, and no CLI wrapper for `events.subscribe`. So a remote caller restricted to the CLI cannot hold an event stream.

Three options, to be decided by measurement in Phase 0:

- **A. CLI polling plus targeted blocking waits.** `ssh host herdr api snapshot` on an interval for the host inventory, plus one `ssh host herdr agent wait --until blocked,idle …` channel per pane the user is actually looking at. Needs nothing on the remote host beyond `herdr`. Costs one SSH channel per watched target, which ControlMaster makes cheap.
- **B. Pipe the socket.** `ssh host socat - UNIX-CONNECT:<socket>` gives the full JSON API including `events.subscribe`. Adds a `socat`/`nc -U` dependency on every remote host, which is a real deployment tax.
- **C. Upstream.** Ask for `herdr api call <method> [json]` and a streaming `herdr api events`. This is a small, obviously-useful addition to Herdr, and it removes option B entirely.

**Recommendation: build on A, open C immediately, treat B as a diagnostic escape hatch never enabled by default.** A is sufficient for Phase A and Phase B and keeps the remote-host requirement to "herdr is installed."

### Operational wrinkles

- **PATH.** `ssh host herdr …` runs a non-login, non-interactive shell. `herdr` may not be on `PATH`. Resolve once per host, cache the absolute path, and allow a per-host `binary` override. Herdr's own `HERDR_REMOTE_BINARY` is the precedent.
- **Server autostart.** There is no `herdr server start`; `herdr server` runs in the foreground, and the daemonizing path (`server/autodetect.rs::spawn_server_daemon`) runs only from a bare `herdr` attach, which needs a TTY. Phase A therefore **requires a Herdr server already running on the remote host** and reports "herdr not running on <host>" as a first-class, actionable state. Sidecar spawning a detached remote server is a Phase B decision, not a Phase A assumption.
- **Session selection.** Herdr sessions are chosen by `--session NAME` / `HERDR_SESSION`, default at `~/.config/herdr`. Sidecar should attach to the host's **default** session, so a user running plain `herdr` on that machine sees the same work. A per-host `session` override exists for people who want separation.
- **Protocol.** Negotiate on the `protocol` integer from `herdr api schema` / the status API, never the version string. Released 0.8.0 spoke protocol 19 while `main` declared 20 — the version string lied inside one published release.

## Architecture

```text
Sidecar
├── internal/hosts                    NEW — host registry, health, transport
│     ├── LocalHost                   (implicit, always present)
│     └── SSHHost                     ssh + ControlMaster, per-host config
├── internal/termruntime              NEW — the runtime seam
│     ├── TmuxRuntime                 wraps today's internal/tty paths, local only
│     └── HerdrRuntime                herdr CLI/API over a hosts.Transport
├── internal/workspaceinventory       host-aware: opaque runtime IDs, not TmuxName
├── internal/overview                 Sessions browser gains host grouping
├── internal/paneframe / panelayout   unchanged — remote panes are ordinary leaves
└── internal/livepanes                remote pane kind gets a Binding on both surfaces
```

### The runtime seam

The interface is written from Sidecar's needs, not from tmux or Herdr nouns. `Ref` is opaque outside its adapter; nothing above the seam may parse it.

```go
// internal/termruntime
type Runtime interface {
    Host() hosts.ID
    Probe(ctx context.Context) (Health, error)          // reachable? version? protocol?
    Inventory(ctx context.Context) (Inventory, error)   // shells, worktrees, agents, states
    Observe(ctx context.Context, Ref, Size) (Stream, error)  // read-only frames
    Control(ctx context.Context, Ref, Size) (Session, error) // frames + input + resize
}
```

`Observe` and `Control` are separate methods rather than a flag, because their guarantees differ: `Observe` is non-exclusive and side-effect-free on the host; `Control` takes an exclusive lock and can displace another client. A caller should have to say which one it means.

`TmuxRuntime` initially wraps what already exists. It is not a refactor of `internal/tty`, and it must not become one before `HerdrRuntime` proves the boundary is correct — extracting the seam from one implementation freezes that implementation's assumptions into it.

### Where it binds in the UI

CLAUDE.md's parity rule holds, read precisely:

- **Pane mechanics are shared.** A remote pane is an ordinary `paneframe` leaf. No second compositor, no second border rule, no second divider renderer, no remote-specific chrome. The frame does not know or care that the bytes came over SSH.
- **Live refresh is shared.** The remote pane kind gets one `livepanes.Binding` entry in each surface's `pane_host.go`, exactly like every other content-pane kind. This matters: a remote pane's content is **not on the filesystem**, so `livewatch` cannot drive it — the binding's `Refresh` is fed by the frame stream or the poll seam. A remote pane kind without a binding is a pane that silently stops being true while an agent works, which is the failure the `Resource` leaf documents.
- **The host list is deliberately overview-only.** The project workspace plugin is scoped to one local checkout; a remote host is not a projection of that checkout. This is a scope difference in the model, not a surface that drifted. If a remote host later gains a project identity, it becomes a parity obligation at that point.

### Startup latency

Per CLAUDE.md, nothing here runs in `Init()` or before `Start()` returns its `tea.Cmd`. SSH connection setup, host probing, and `herdr api snapshot` are all subprocess spawns over a network, which is the worst possible thing to put on the first-frame path. Hosts render as "unknown" and resolve asynchronously. `SIDECAR_STARTUP_TRACE=stderr` must show no new phase attributable to hosts.

## Identity and state

Persist per remote workspace:

- `HostID` (the Sidecar-side host name, not the SSH target string, so a host can be re-pointed).
- Runtime kind (`tmux` | `herdr`).
- An opaque runtime ID.
- Sidecar-owned metadata: display name, linked task, launch spec.

Rules, all inherited from findings in the deprecated plan:

- **Key durable state on `terminal_id`** (`term_<hex>`). `wN:pM` workspace/tab/pane IDs are index-shaped handles that must be re-resolved from `session.snapshot` on every startup and after every reconnect.
- **Ownership metadata is advertisement, not truth.** `pane.report_metadata` tokens cap at 16 keys, are optionally TTL'd, and are reinitialized to default on server restore (`persist/restore.rs`). Sidecar's own inspectable state is authoritative; tokens are re-published after each remote server start so `herdr` CLI users on that host can see what Sidecar is watching.
- **Never delete what Sidecar does not own.** Cleanup touches only objects recorded in Sidecar's mapping. A remote host is far more likely than a local one to hold unrelated work.
- **Remove `TmuxName` from the cross-project model.** `workspaceinventory.Workspace` gets `HostID` plus an opaque runtime ref. This is owed whether or not Herdr ships.

## Phases

### Phase 0 — spike

Non-production Go client against a real Herdr on a second machine. Ordered so the items that can kill the plan come first.

1. **Frames over SSH.** Drive `ssh host herdr terminal session observe <target>` with no TTY. Confirm frames arrive, decode, and apply correctly. Measure steady-state bandwidth, per-frame latency, and idle cost over a real link (LAN and, separately, over a VPN or WAN hop).
2. **Observe semantics under a live Herdr client.** With a user attached to the same pane in Herdr's own TUI, confirm Sidecar's observer takes no resize lock, does not become foreground, and does not change what the Herdr user sees. Determine what `--cols`/`--rows` actually do for an observer — whether the observer gets an independently sized render or a bounded view of the terminal's own grid.
3. **Inventory and status fidelity.** `ssh host herdr api snapshot` plus `agent list`. Confirm the returned agent states match what the host's Herdr UI shows, and that Sidecar can map them onto `internal/agentstatus` without inventing certainty for `unknown`.
4. **Event transport decision.** Measure option A (interval snapshot + per-target `agent wait` channels) against option B (`socat` socket pipe) for latency, SSH channel count, and remote CPU. Record the numbers; this decides the Phase A design.
5. **Reconnect and reconcile.** Kill the link mid-stream. Confirm classification of `terminal.closed` reasons, bounded retry, and that a fresh `session.snapshot` reconverges the model.
6. **Identity durability.** Restart the remote `herdr server` and run `server.live_handoff`. Determine whether `terminal_id` and `wN:pM` survive each, and whether Sidecar's mapping reconciles.
7. **Host discovery.** `herdr` not on the non-interactive `PATH`; no server running; wrong protocol integer; host unreachable. Each must produce a distinct, actionable state.

**Exit gate:** a recorded matrix says which of Phase A's behaviors work over SSH today, which need upstream changes, and what the latency and bandwidth budget actually is. If observed frame latency makes a remote pane feel worse than `ssh host tmux attach`, stop and say so.

### Phase A — read-only remote hosts

Behind `features.HerdrRemoteHosts`, default off.

- Host registry with per-host config (`ssh` target, optional `binary`, optional `session`).
- Hosts appear in the Sessions browser as a grouping above the existing project rows.
- Per host: its Herdr workspaces/tabs/panes, cwd, agent kind, and agent status in the existing status column.
- Selecting a remote pane opens a live read-only pane view driven by `terminal session observe`.
- Host health is a first-class row state: reachable, `herdr` missing, server not running, protocol mismatch, unreachable. Each states the fix.
- No creation, no input, no worktree actions, no git, no history.

**Exit gate:** on a real second machine, Sidecar answers "what is running over there and is anything blocked?" without opening a terminal, and a full day of use produces no case where the displayed status is stale or wrong while the pane is on screen.

### Phase B — interactive remote panes

- Upgrade the focused remote pane from `observe` to `control`; downgrade on blur so an idle Sidecar never holds the lock.
- Text, key, and paste input; debounced and coalesced resize.
- `--takeover` only on explicit user action, never automatically.
- Releasing control on "open in Herdr" so the native client can size the pane itself.
- Decide whether Sidecar may spawn a detached remote `herdr server`, or whether that stays the user's job.

**Exit gate:** a real agent session on a remote host can be driven from Sidecar — including answering a blocked prompt — with input ordering and latency good enough to prefer over `ssh` + `herdr`.

### Phase C — remote creation

Only if B earns it. This is where the argv gap returns.

- Create a remote shell in a chosen cwd, via `layout.apply` with a `LayoutNode::Pane` carrying `command`/`cwd`/`env`.
- Launch a known agent via `agent.start` into an existing pane.
- Prove argv, quoting, env, and non-shell programs match what `tmux new-session -d` achieves locally.
- Ownership-safe close: Sidecar closes only what it created.

**Exit gate:** create → work → observe → close, entirely from Sidecar, with no orphaned or wrongly-closed remote objects across a Sidecar restart and a remote server restart.

### Deferred: Herdr locally

Not planned. The conditions that would reopen it, from the research already done: the `geometry_lease.go` + `pane_fit.go` cost (~1,100 lines existing solely because tmux has one pane size per window shared across clients) becoming a bug source rather than a stable tax, plus `pane.read` gaining offset/range so history is not a regression. Both would have to be true. If Phases A–C ship, the runtime seam makes that a smaller question than it is today, which is a reason not to prejudge it now.

## Failure, degradation, security

- **Degrade the host, not the app.** A dead SSH connection marks one host offline. It never blocks a frame, never touches local workspaces, and never falls back to creating something local.
- **Never stop a remote Herdr server.** It may own work unrelated to Sidecar, on a machine the user is not looking at. Protocol mismatch is a diagnostic, never a remediation.
- **SSH is the trust boundary.** Sidecar never binds a socket, never proxies Herdr's API to anything, and never enables option B by default. The user's existing SSH config, keys, and agent forwarding are the whole security model.
- **Remote credentials stay remote.** An agent in a remote pane runs with that host's credentials. Sidecar displays its output; it does not relay secrets. Worth stating in user docs, because a status column that shows remote agents makes it easy to forget which machine is doing the work.
- **Bound everything.** Frame buffers bounded by bytes, not lines (the same rule the local path adopted). Per-host channel count capped. Poll intervals backed off on failure.
- **No silent partial truth.** A host whose data is stale says so in its row. This is the remote form of the `livepanes` rule.

## Upstream requests

In descending order of leverage for this plan:

1. **`herdr api call <method> [json]` and a streaming `herdr api events`.** Removes the need for `socat` on every remote host and makes the entire JSON API reachable through the one boundary that already works over SSH. Small addition, obvious general utility.
2. **A stable machine-readable `code` on `terminal.closed`,** so reconnect logic is not string-matching English.
3. **Offset/range on `pane.read`,** which is the precondition for any future local use and for remote history.
4. **`command`/`env`/`cwd` on `pane.split` and `tab.create`,** so launching a program does not require `layout.apply`. Phase C only.

Requests 1 and 2 are worth opening before Phase A ships. 3 and 4 can wait for evidence that this direction is real.

## Open questions

- What `--cols`/`--rows` mean for an observer, and whether two observers at different sizes each get a correct render (Phase 0, item 2).
- Whether `terminal_id` survives `server.live_handoff` (Phase 0, item 6). If it does not, every remote mapping needs a re-resolution path on every handoff, which changes the reconcile design.
- Whether the per-target `agent wait` channel model scales to a host with a dozen agents, or whether that alone forces upstream request 1.
- Whether remote hosts should ever appear in the project workspace plugin. Currently no; revisit only if a remote checkout gains a project identity in Sidecar's model.
- Whether Sidecar should read remote agent *transcripts* (via `agent.read`, or by reading the remote store over SSH) or stop at status. Phase A stops at status deliberately; transcripts are a much larger surface.

## Acceptance evidence

| Journey | Evidence |
| --- | --- |
| Startup | `SIDECAR_STARTUP_TRACE=stderr` shows no host work before the first ready frame, with a host configured and unreachable |
| Host health | Each of unreachable / no `herdr` / no server / protocol mismatch renders a distinct row state naming the fix |
| Inventory | Remote shells, worktrees, and agents match `herdr api snapshot` on the host |
| Status truth | Remote agent states match the host's own Herdr UI, including `blocked` and `done` |
| Live pane | Frames apply correctly for a shell, a full-screen TUI on the alternate screen, wide characters, colors, and hyperlinks |
| Coexistence | A Herdr user on the same pane sees no change while Sidecar observes; Sidecar takes no resize lock |
| Reconnect | Link drop and restore reconverge via a fresh snapshot with no stale rows and no duplicate panes |
| Isolation | A dead host never blocks a frame, never affects local workspaces, and never silently creates local state |
| Cleanup | Sidecar closes only objects it created; a Sidecar restart and a remote server restart both leave unrelated remote work untouched |
| Rollback | Disabling the feature flag leaves local behavior and state byte-identical |

Latency, bandwidth, SSH channel count, and remote CPU are measured and recorded, not assumed. This should not be adopted on architecture appeal — it should be adopted because seeing a blocked agent on another machine is worth what it costs.

## Changelog

- **2026-08-28** — Created. Supersedes the deprecated tmux-replacement plan by re-scoping Herdr from local runtime to remote host agent, which removes both of that plan's blocking gaps from the critical path.
