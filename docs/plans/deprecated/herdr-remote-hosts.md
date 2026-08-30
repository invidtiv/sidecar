# Herdr as Sidecar's remote host runtime

Status: **active, planning**, 2026-08-28

Supersedes: [Replacing Sidecar's tmux integration with Herdr](../deprecated/replacing-tmux-with-herdr.md) (deprecated) and [Herdr session persistence](../deprecated/herdr-session-persistence.md) (deprecated)
Research input: [What Sidecar can learn from Herdr without replacing tmux](../../research/active/lessons-from-herdr.md)
Related: [Hosting Herdr plugins in Sidecar](herdr-plugin-support.md) — independent axis; this plan makes Sidecar a client of remote Herdr servers, that one makes Sidecar a local host for Herdr's plugin ecosystem
Competing alternative: [Sidecar as its own remote host runtime](sidecar-remote-hosts.md) — same deliverable with Sidecar itself as the on-host agent (proxied tmux control mode plus a headless `sidecar host serve`). Both plans stay active through their Phase 0 spikes; that plan's Relationship section records the bake-off criteria, and its spike should run first. The host registry, SSH transport, Sessions-browser host grouping, and the `HostID`/`TmuxName` inventory changes are shared work whichever wins.
Herdr source inspected: [`herdrdev/herdr`](https://github.com/herdrdev/herdr) at `c2637dc1` (local checkout `~/code/herdr`), protocol 21 (`src/protocol/wire.rs:16`)

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

1. **argv fidelity.** `workspace.create`, `tab.create`, and `pane.split` accept `cwd`/`env` (plus `label` on the first two — `pane.split` has neither `label` nor `command`; `src/api/schema/panes.rs:27-43`) but no command. Launching a program requires `layout.apply`, whose `LayoutPane` carries `command: Vec<String>`/`cwd`/`env`/`label` (`src/api/schema/panes.rs:191-203`).
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

Herdr's socket API is a Unix domain socket at the session data directory, mode `0600` (`SOCKET_PERMISSION_MODE`, `src/server/socket_paths.rs:12`), never network-exposed. That is correct and Sidecar must not change it. The transport is SSH, invoking the `herdr` CLI on the remote host.

Herdr itself already does this for `herdr --remote user@host`, and its ssh recipe (`src/remote/attach.rs`) is worth copying nearly verbatim:

- **Multiplexing by CLI flags only:** `-S <dir>/ctl -o ControlMaster=auto -o ControlPersist=yes -T <target>`, with the control socket named `ctl` in a private `0700` per-process temp dir validated against the ~103-byte Unix socket path limit (`attach.rs:606-621`, `src/platform/unix_common.rs:32-60`; the exact argv is locked by a test at `attach.rs:2375-2389`). One authenticated connection, N cheap channels.
- **Keepalive via a generated `-F` config,** which `Include`s the user's `~/.ssh/config` and `/etc/ssh/ssh_config` *first* (OpenSSH first-value-wins keeps user overrides), then appends `ServerAliveInterval 15` / `ServerAliveCountMax 4` (`write_managed_ssh_config`, `attach.rs:1775-1814`). Link death is therefore detected in ~60 s, and this is the *only* liveness signal — see the frame-protocol notes below.
- **Teardown:** `ssh -O exit -o BatchMode=yes <target>` when done (`attach.rs:581-604`).
- **Two invocation styles, used deliberately:** `ssh -T <target> '/bin/sh -s'` with the script written to stdin for deterministic, quoting-free execution (`attach.rs:463-483`), and `ssh -T <target> '<cmd>'` through the user's login shell precisely when the point is to observe the shell-initialized `PATH` (`attach.rs:485-487`).
- All of it gated behind a config flag (`remote.manage_ssh_config`, default true) so a user with exotic ssh needs can opt out to plain `ssh -T <target>`. Sidecar should mirror that per host.

Sidecar's path is **simpler than Herdr's own remote attach**, because Sidecar speaks the documented CLI/API boundary rather than the private bincode client protocol. The version coupling Herdr enforces is remote-side: `ensure_remote_server_running` runs *on the host* inside `herdr remote-client-bridge` and requires the remote binary's `PROTOCOL_VERSION` to match the running remote server (`src/remote/host_unix.rs:51-69`); the local side additionally refuses a remote binary whose version string and protocol don't exactly match its own (`attach.rs:867-885`). Going through the CLI, only the remote binary ↔ remote server pair matters to Sidecar — there is no local-binary coupling at all.

### Live pane content: `terminal session observe` and `terminal session control`

Both are plain stdio processes emitting newline-delimited JSON with base64 ANSI frames. Target syntax is flexible: raw `terminal_id`, public pane ID, or agent name/label (`resolve_terminal_target`, `src/app/terminal_targets.rs:33-73`).

- `herdr terminal session observe <target> [--cols N] [--rows N]` — read-only (`src/client/mod.rs:960`; defaults 120×40, `src/cli.rs:617-618`). Server-side, `observe_terminal_client` sets the connection mode and returns (`src/server/headless.rs:1886-1910`). It does **not** enter `direct_attach_resize_locks`, does not become foreground (`is_full_app_client` excludes it, `src/server/clients.rs:169-171`), and does not claim exclusivity. Input, input events, clipboard, and scroll from an observing client are all dropped explicitly (`headless.rs:3153-3230`, `1931-1937`). Multiple simultaneous observers are allowed (test at `headless.rs:6628-6656`).
- `herdr terminal session control <target> [--takeover] [--cols N] [--rows N]` — read/write (`src/client/mod.rs:968`). Reads **newline-delimited JSON commands from stdin** in a spawned thread, so it needs no TTY and composes with `ssh` without `-t`. The command vocabulary (`terminal_control_command_from_json`, `src/client/mod.rs:1147-1215`): `{"type":"terminal.input","text":…}` or `{"type":"terminal.input","bytes":"<base64>"}` (special keys are raw escape bytes — there is no named-key abstraction; bracketed paste is constructed by the caller and detected server-side, `headless.rs:409-423`), `{"type":"terminal.resize","cols":…,"rows":…}`, `{"type":"terminal.scroll",…}`, `{"type":"terminal.release"}`. Exactly one controller per terminal (`terminal_attach_owners`); `--takeover` displaces the incumbent, and the ownership pool is **shared with `herdr terminal attach`**, so takeover can eject a human's TUI attach on the host — the displaced side gets `terminal.closed` with reason `"terminal attach taken over"` and exits 0 (`headless.rs:2853-2874`).

Three server-side facts, verified at `c2637dc1`, that shape the design:

1. **Observer sizing is per-client projection, not reflow.** Each render target gets its own render pass at its own `--cols/--rows`: a fresh buffer at the client's size, into which the *single shared* terminal grid (at the PTY's size) is drawn clipped/padded (`render_terminal_virtual`, `src/server/render_stream.rs:368-399`; per-client sizes in `render_targets`, `src/server/clients.rs:288-316`). There is no per-observer VT. An observer's size never touches the PTY — the observer resize branch updates only that client and repaints (`headless.rs:3277-3295`), and a test asserts two observers leave `runtime.current_size()` unchanged (`headless.rs:6620-6657`). Sidecar should request its actual pane size and expect clipping, not reflow, when the remote PTY is larger.
2. **Control resizes the real PTY.** `attach_terminal_client` takes the resize lock *and applies the controller's size to the PTY* on attach (`headless.rs:2903-2905`), and stdin `terminal.resize` calls `runtime.resize` (`headless.rs:3250-3275`). Taking control of a pane at Sidecar's pane size resizes that terminal for everyone on the host — the remote form of the one-pane-size problem `geometry_lease.go` exists to solve locally. Phase B must decide etiquette: open control at the pane's current size, or accept that focusing a remote pane resizes it.
3. **Observe/control is a one-way door per connection.** A connection cannot upgrade from observe to control or back (`client_is_pending_terminal_mode`, `headless.rs:2909-2913`; a second mode request kills the connection). "Upgrade on focus" therefore means: open a control connection, then close the observe one — and order it that way to avoid a frame gap.

One more shared-state wrinkle: scrollback position is server-side and global to the pane. A controller's `terminal.scroll` moves the runtime's scroll offset for **every** client, any `terminal.input` snaps back to bottom, and the cursor is hidden while scrolled (`headless.rs:345-423`, `render_stream.rs:389`). Remote history browsing must therefore go through `pane.read` (a tail window, ≤1000 lines — see below), never by scrolling the live pane.

That split maps cleanly onto the phases: Phase A uses `observe` and can never disturb a Herdr user looking at the same pane; Phase B upgrades the selected pane to `control`.

Frame protocol (`TerminalFrame`, `src/protocol/wire.rs:635-646`): `{"type":"terminal.frame","seq":N,"encoding":"ansi","width":W,"height":H,"full":bool,"bytes":"<base64>"}`, where `width`/`height` are the *client's* requested size. Bandwidth numbers below are from the live probe recorded in the deprecated plan (Herdr 0.8.0, protocol 19); the semantic rows are now verified against source at `c2637dc1`:

| Property | Value |
| --- | --- |
| Escape vocabulary | `CUP`, `SGR`, `DECSET ?25`, `DECSET ?2026`, `OSC 8`, one `CSI 2J` per full frame — nothing else, even under alternate-screen apps |
| Full 80×24 frame | ~36 KB |
| Full 100×30 frame | ~56 KB |
| Typical delta | 70–600 B |
| Idle | zero frames; identical frames are skipped entirely (`render_stream.rs:82-85`) |
| Resize | forces a full frame; so do first-frame-after-connect and any shared-runtime repaint (`render_ansi.rs:86-91`). No periodic full frame exists |
| `seq` | contiguous and strictly increasing per connection, numbered only on successful send (`commit_sent_frame`, `render_stream.rs:121-147`) — coalescing is invisible in it, so it detects nothing; `full: true` is the resync signal |
| Backpressure | render channel capacity is 1; a slow link gets fewer, more-condensed frames re-rendered from fresh state, never a growing queue (`client_transport.rs:52-53`, `headless.rs:4693-4696`) |
| Graceful close | one `{"type":"terminal.closed","reason":…}` line, then exit 0. `reason` is a free-text English string, no code field. Attach *failures* arrive the same way and still exit 0 — success and failure are distinguishable only by string-matching the reason |
| Ungraceful close | **silent EOF, exit 0, no `terminal.closed` at all** (`UnexpectedEof` is treated as `Ok`, `src/client/mod.rs:1092`). There is no heartbeat anywhere in the protocol; liveness comes solely from ssh keepalives |

The enumerable `reason` strings at this revision: `"detached"`, `"terminal {id} exited"`, `"terminal attach taken over"`, the `"terminal attach failed: …"`/`"terminal session … failed: …"` family, `"terminal attach ended: terminal {id} not found"`, `"server is shutting down"`, and `"live update in progress; reconnect after handoff completes"`. Reconnect logic has to classify these (and the silent-EOF case) today; that is upstream request 2.

The dialect is absolutely-positioned cell blits, appliable with `charmbracelet/x/ansi` over buffers Sidecar already depends on. This is the same shape as the byte-fed screen model already shipped in `internal/tty/screenmodel`, and the applier should live near it rather than being written twice. Non-frame server messages (toasts, graphics, clipboard) are silently dropped by the session output loop, so the stream is frames and `terminal.closed` only.

The full-frame-per-resize cost is the reason drag-to-resize must be debounced and coalesced in the adapter, not passed through. Over SSH that matters more than it did locally.

### Discovery and agent state: the JSON API

`src/api/schema.rs` exposes the methods Sidecar needs, including `session.snapshot`, `workspace.list`, `tab.list`, `pane.list`/`pane.get`/`pane.read`, `agent.list`/`agent.get`/`agent.read`/`agent.explain`/`agent.wait`, `worktree.list`, `pane.report_metadata`, `events.subscribe`, `events.wait`, and `pane.wait_for_output`.

`pane.read` specifics (`PaneReadParams`, `src/api/schema/panes.rs:274-287`): `source` is one of `visible | recent | recent_unwrapped | detection`, `lines` is a tail count capped at 1000 with a default of 80 for the `recent` sources (`src/app/api_helpers.rs:117-158`), `strip_ansi` defaults to true. No offset or range — confirming the windowing gap.

`agent.wait { target, until: [status…], timeout_ms }` (`src/api/schema/agents.rs:26-32`; target is singular) is a direct fit for "tell me when this agent goes idle or blocked" and replaces polling for a specific target. It also has a first-class CLI: `herdr agent wait <target> [--until STATUS]… [--timeout MS]` (`src/cli/agent.rs:506-560`), which is what makes transport option A below workable without touching the socket.

Two facts about the socket protocol matter for scaling: it is strictly **one request per connection** — the server reads a single request line, dispatches, writes one response, and hangs up (`src/api/server.rs:173`), with blocking methods (`agent.wait`, `events.wait`, `pane.wait_for_output`) holding their connection open until match/timeout/disconnect — and the server spawns an unbounded thread per connection with no waiter limit (`server.rs:98-118`). So one watched target = one held connection = one ssh channel under ControlMaster. The practical ceiling is not Herdr but **sshd's `MaxSessions`, default 10 channels per multiplexed connection** — a host with a dozen watched agents blows past it. OpenSSH falls back to spawning extra master connections (or fails, depending on `ControlMaster` mode); Phase 0 must measure which, and this is the concrete force behind upstream request 1.

Herdr's state vocabulary maps onto what `internal/agentstatus` already models, but the layering is worth knowing: detection produces only `idle | working | blocked | unknown` (`src/detect/mod.rs:11-20` — there is no detected `done`); the API's `done` is derived as **idle and not yet seen** (`pane_agent_status`, `src/app/api_helpers.rs:96-107`), the same "finished, unseen" distinction Sidecar ported in td-48ecf2, with Sidecar's own done-TTL layered on top. Open detail for Phase 0: what flips `seen` — Sidecar's observation must not silently clear `done` for the host's user, and conversely Sidecar may want a way to mark a remote agent as seen when the user views it in Sidecar.

`events.subscribe` supports subscription kinds including `pane.agent_status_changed` and `pane.output_matched` (`src/api/schema/events.rs:18-85`) — exactly the stream upstream request 1 would unlock over the CLI.

### The transport gap that shapes Phase 0

**`herdr api` exposes only `snapshot` and `schema`** (`src/cli/api.rs:11-22`; verified — no generic call or raw-request mode exists anywhere in `src/cli/`). There is no CLI wrapper for `events.subscribe`. So a remote caller restricted to the CLI cannot hold an event stream.

Three options, to be decided by measurement in Phase 0:

- **A. CLI polling plus targeted blocking waits.** `ssh host herdr api snapshot` on an interval for the host inventory, plus one `ssh host herdr agent wait <target> --until blocked --until idle --timeout …` channel per pane the user is actually looking at (`--until` is repeatable; both verified in `src/cli/agent.rs`). Needs nothing on the remote host beyond `herdr`. Costs one SSH channel per watched target — cheap under ControlMaster until sshd's `MaxSessions` (default 10) bites; see above.
- **B. Pipe the socket.** `ssh host socat - UNIX-CONNECT:<socket>` gives the full JSON API. Two caveats beyond the `socat`/`nc -U` deployment tax: the protocol is one-request-per-connection, so B is *not* a general-purpose multiplexed channel — it is one pipe per request, with `events.subscribe` (which does stream on its connection, `src/api/server.rs:221-242`) as the single long-lived exception worth piping.
- **C. Upstream.** Ask for `herdr api call <method> [json]` and a streaming `herdr api events`. This is a small, obviously-useful addition to Herdr, and it removes option B entirely.

**Recommendation: build on A, open C immediately, treat B as a diagnostic escape hatch never enabled by default.** A is sufficient for Phase A and Phase B and keeps the remote-host requirement to "herdr is installed."

### Operational wrinkles

- **PATH.** `ssh host herdr …` runs a non-login, non-interactive shell. `herdr` may not be on `PATH`. Resolve once per host, cache the absolute path, and allow a per-host `binary` override. The precedent to copy is Herdr's own remote resolution (`attach.rs:763-865`): probe `command -v herdr` through the user's *login* shell (retrying via `/bin/sh` for non-POSIX shells like xonsh), then fall back to a hardcoded candidate list — `~/.local/bin/herdr`, Homebrew and linuxbrew prefixes, mise installs (shims excluded), nix profiles. (`HERDR_REMOTE_BINARY` is *not* this — it is a local file path used as the install source pushed to the remote, `attach.rs:892-924`.)
- **Server autostart.** There is no `herdr server start`, and `herdr server` runs in the foreground (`src/cli/server.rs:8-25`) — but the earlier claim that daemonizing needs a TTY was wrong. `spawn_server_daemon` (`src/server/autodetect.rs:189-236`) daemonizes with a `pre_exec` `setsid()` and all three stdio fds on `/dev/null`, needing no TTY at all, and `herdr remote-client-bridge` invokes it under plain `ssh -T` when no server is listening (`src/remote/host_unix.rs:51-69`) — this is how `herdr --remote` lazily starts remote servers today. What is missing is only a *CLI entry point* Sidecar can call. Phase A still treats "server not running on <host>" as a first-class, actionable state rather than auto-starting. For Phase B there are two honest paths: upstream a `herdr server start` verb (added to the requests below), or replicate the daemonize — and the replication has a trap: Herdr's own remote attach restarts any server that is not its own session leader (`DaemonDetachMissing`, `attach.rs:1057-1076`), so a `nohup … &`-style spawn would be torn down by the next `herdr --remote` user, and macOS has no `setsid(1)` to shell out to. Upstreaming is the better path.
- **Session selection.** Herdr sessions are chosen by `--session NAME` / `HERDR_SESSION` (`src/session.rs:10-11`). The default session's data dir is `~/.config/herdr` itself; named sessions live under `~/.config/herdr/sessions/<name>` (`session.rs:157-167`), each with its own `herdr.sock`. Sidecar should attach to the host's **default** session, so a user running plain `herdr` on that machine sees the same work. A per-host `session` override exists for people who want separation.
- **Protocol.** Negotiate on the `protocol` integer, never the version string. Released 0.8.0 spoke protocol 19 while its version string implied otherwise, and `main` has since moved 19 → 20 → 21 (`PROTOCOL_VERSION = 21` at `c2637dc1`, `src/protocol/wire.rs:16`) — this integer moves fast enough that Sidecar should expect mismatch as a routine state, not an edge case. It is exposed three ways: the `herdr api schema` summary, `herdr status server --json` (what Herdr's own remote attach probes, `attach.rs:1213-1225`), and the API `ping` response, which also carries a `capabilities` list worth recording per host (`src/api/server.rs:347-355`).

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

Rules. The deprecated plan's identity rule said to key on `terminal_id` and treat `wN:pM` as index-shaped; source inspection at `c2637dc1` shows that is **exactly backwards**, and the rules below replace it:

- **Key durable state on the public IDs** (`wN`, `wN:tM`, `wN:pM`). They are base-32-encoded monotonic *allocation counters*, not indexes: closing a pane never renumbers its siblings (`src/workspace.rs:106-151`; test at `src/persist/snapshot.rs:979-1000`), the numbers and their counters are persisted in `session.json` and restored verbatim, and counters are bumped on restore so old IDs are never reused (`snapshot.rs:50-69`, `restore.rs:318-395`). They survive both a cold server restart and `server.live_handoff`.
- **Treat `terminal_id` (`term_<hex>`) as a per-server-process handle.** It is minted from wall-clock micros plus a process-local counter (`src/terminal/id.rs:14-22`) and minted *again* for every pane on every restore and every handoff import (`restore.rs:537,630`) — it survives neither. Use it only as the live handle `terminal session observe/control` wants, re-resolve it from `session.snapshot` keyed on public IDs after any reconnect, and treat a changed `terminal_id` under a stable public ID as the definitive "the server restarted" signal.
- **Know what a remote server restart looks like, because reconcile must handle both kinds.** `server.live_handoff` passes the PTY master fds over `SCM_RIGHTS` (≤64 panes, `src/server/handoff.rs:26`), so child processes and running agents truly survive; a cold restart+restore respawns fresh shells in each pane's saved cwd, does *not* re-run `launch_argv`, and — by default (`session.resume_agents_on_restore = true`) — auto-resumes allowlisted agents (`claude --resume <id>`, `codex resume <id>`, …) once a client attaches (`src/agent_resume.rs`, `restore.rs:536-566`). Either way Sidecar sees the same public IDs with fresh `terminal_id`s; after a cold restart the processes are new even though the layout looks identical.
- **Ownership metadata is advertisement, not truth.** `pane.report_metadata` tokens cap at 16 keys per request and 32 per resource, values ≤80 chars, optionally TTL'd via `Instant` so nothing can survive a process boundary, and reset on both restore and handoff (`src/app/api_helpers.rs:202-208`, `src/terminal/state.rs:163`). Sidecar's own inspectable state is authoritative; tokens are re-published after each remote server start so `herdr` CLI users on that host can see what Sidecar is watching.
- **Never delete what Sidecar does not own.** Cleanup touches only objects recorded in Sidecar's mapping. A remote host is far more likely than a local one to hold unrelated work.
- **Remove `TmuxName` from the cross-project model.** `workspaceinventory.Workspace` gets `HostID` plus an opaque runtime ref. This is owed whether or not Herdr ships.

## Phases

### Phase 0 — spike

Non-production Go client against a real Herdr on a second machine. The source inspection at `c2637dc1` already answered the questions this spike was originally scoped to discover (observer sizing, resize-lock behavior, identity durability); the spike's job is now to *confirm those answers hold at runtime* and to measure the things source cannot answer. Ordered so the items that can kill the plan come first.

1. **Frames over SSH.** Drive `ssh host herdr terminal session observe <target>` with no TTY. Confirm frames arrive, decode, and apply correctly. Measure steady-state bandwidth, per-frame latency, and idle cost over a real link (LAN and, separately, over a VPN or WAN hop). Confirm the capacity-1 coalescing behavior on a deliberately slow link.
2. **Coexistence under a live Herdr client.** With a user attached to the same pane in Herdr's own TUI, confirm at runtime what source promises: Sidecar's observer takes no resize lock, never becomes foreground, and changes nothing the Herdr user sees. Additionally: does observing (or `agent.get`/`agent.read`) flip the `seen` bit that turns `done` back into `idle`? Source shows `done` = idle-and-unseen, but not every path that sets `seen` — this must be pinned down, because Sidecar silently clearing a host user's `done` markers would be a real coexistence bug.
3. **SSH channel ceiling.** Hold N concurrent `agent wait` channels through one ControlMaster connection and find where it breaks — sshd's `MaxSessions` defaults to 10 per connection. Determine whether OpenSSH transparently opens additional master connections or fails, and at what N. This number decides how far option A stretches.
4. **Inventory and status fidelity.** `ssh host herdr api snapshot` plus `agent list`. Confirm the returned agent states match what the host's Herdr UI shows, and that Sidecar can map them onto `internal/agentstatus` without inventing certainty for `unknown`.
5. **Event transport decision.** Measure option A (interval snapshot + per-target `agent wait` channels) against option B (`socat` socket pipe held open for `events.subscribe`) for latency, SSH channel count, and remote CPU. Record the numbers; this decides the Phase A design.
6. **Reconnect and reconcile.** Kill the link mid-stream, and separately `kill -9` the remote server. The first ends the observe process on ssh keepalive timeout (~60 s with Herdr's own settings); the second produces silent EOF with exit 0 and **no** `terminal.closed` line — confirm the client treats both as "stream died, reason unknown" and that a fresh `session.snapshot` reconverges the model with bounded retry.
7. **Restart reconciliation.** Restart the remote `herdr server` cold and via `server.live_handoff`. Source says public IDs survive both and `terminal_id` survives neither, and that a cold restart auto-resumes allowlisted agents once a client attaches — confirm all three at runtime, and confirm the "stable public ID, changed `terminal_id`" signal is a reliable restart detector.
8. **Host discovery.** `herdr` not on the non-interactive `PATH`; no server running; wrong protocol integer; host unreachable. Each must produce a distinct, actionable state.

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

- Upgrade the focused remote pane from `observe` to `control`; downgrade on blur so an idle Sidecar never holds the lock. Because a connection cannot switch modes, the upgrade is open-control-then-close-observe, and the downgrade the reverse.
- **Resize etiquette.** Opening control applies the controller's size to the real PTY, resizing the pane for everyone on the host. Default to opening control at the pane's current size (read from the snapshot) and only resizing on explicit user intent — the geometry-lease lesson, applied remotely. Resize input is debounced and coalesced regardless.
- Text, key, and paste input over the stdin JSON protocol (`terminal.input` with base64 bytes for special keys; bracketed paste constructed client-side).
- `--takeover` only on explicit user action, never automatically — it shares an ownership pool with `herdr terminal attach`, so an automatic takeover could eject a human's TUI session on the host.
- Never scroll the live pane for history: a controller's scroll moves the shared server-side scroll offset every observer sees. History is `pane.read` only.
- Releasing control on "open in Herdr" so the native client can size the pane itself.
- Decide remote server autostart: preferably via an upstreamed `herdr server start` (request 5); if Sidecar must replicate the `setsid` daemonize instead, the spawned server has to be its own session leader or Herdr's own remote attach will restart it (`DaemonDetachMissing`).

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

1. **`herdr api call <method> [json]` and a streaming `herdr api events`.** Removes the need for `socat` on every remote host, makes the entire JSON API reachable through the one boundary that already works over SSH, and collapses N per-target `agent wait` channels (each one held connection, one ssh channel, against sshd's default `MaxSessions` of 10) into a single `events.subscribe` stream. Small addition, obvious general utility.
2. **A stable machine-readable `code` on `terminal.closed`,** so reconnect logic is not string-matching English. Sharpened by source: attach *failures* also arrive as `terminal.closed` and the CLI exits 0 either way, and an ungraceful server death produces silent EOF with no `terminal.closed` at all — so today success, failure, displacement, and crash are distinguishable only by string inspection plus absence-of-output.
3. **Offset/range on `pane.read`,** which is the precondition for any future local use and for remote history. Today it is a tail window capped at 1000 lines (`src/app/api_helpers.rs:117-158`).
4. **`command`/`env`/`cwd` on `pane.split` and `tab.create`,** so launching a program does not require `layout.apply`. Phase C only.
5. **A `herdr server start` verb** that runs the existing `spawn_server_daemon` path (setsid, stdio nulled) from the CLI. The mechanism already exists and already runs TTY-free under `remote-client-bridge`; exposing it lets Sidecar (and any script) start a host's server over plain SSH without replicating session-leader semantics per platform. Phase B only.

Requests 1 and 2 are worth opening before Phase A ships. 3–5 can wait for evidence that this direction is real.

## Open questions

Two questions the original draft carried are now answered from source and moved into the body: observers get an independently sized per-client render of the shared grid (each at its own `--cols/--rows`, clipped/padded, never touching the PTY), and `terminal_id` survives neither restore nor `server.live_handoff` — every remote mapping keys on public IDs and re-resolves `terminal_id` after any restart. Still open:

- What flips an agent's `seen` bit (Phase 0, item 2). Sidecar observing a pane must not silently clear the host user's `done` states; and Sidecar may want to *deliberately* mark seen when its user views the agent.
- Where the per-target `agent wait` channel model actually breaks under sshd `MaxSessions` (Phase 0, item 3), and whether that alone forces upstream request 1 before Phase A rather than after.
- Whether "open control at current PTY size" is achievable — whether the snapshot exposes the PTY's rows/cols for a pane — or whether Phase B's resize etiquette needs an upstream addition (Phase 0 should check while the spike client exists).
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
| Coexistence | A Herdr user on the same pane sees no change while Sidecar observes; Sidecar takes no resize lock and does not flip agents' `done` (unseen) state on the host |
| Reconnect | Link drop, ssh keepalive timeout, and a `kill -9`'d server (silent EOF, no `terminal.closed`) all reconverge via a fresh snapshot with no stale rows and no duplicate panes |
| Isolation | A dead host never blocks a frame, never affects local workspaces, and never silently creates local state |
| Cleanup | Sidecar closes only objects it created; a Sidecar restart and a remote server restart both leave unrelated remote work untouched |
| Rollback | Disabling the feature flag leaves local behavior and state byte-identical |

Latency, bandwidth, SSH channel count, and remote CPU are measured and recorded, not assumed. This should not be adopted on architecture appeal — it should be adopted because seeing a blocked agent on another machine is worth what it costs.

## Changelog

- **2026-08-28** — Created. Supersedes the deprecated tmux-replacement plan by re-scoping Herdr from local runtime to remote host agent, which removes both of that plan's blocking gaps from the critical path.
- **2026-08-28 (later)** — Deep source verification at `c2637dc1`. Corrections: the identity rule was inverted (public `wN:pM` IDs are persisted allocation counters that survive restore and handoff; `terminal_id` is per-process and survives neither); frame `seq` is contiguous per connection, not repeating; the "daemonize needs a TTY" claim was wrong (`spawn_server_daemon` is TTY-free via `setsid`, invoked by `remote-client-bridge` — only a CLI verb is missing); `HERDR_REMOTE_BINARY` is a local install source, not a remote path override; protocol is 21 at this revision. Additions: Herdr's ssh multiplexing/keepalive recipe; control's stdin JSON vocabulary and its PTY-resize/takeover/shared-scrollback consequences; one-request-per-connection socket protocol and the sshd `MaxSessions` ceiling on option A; `done` = idle-and-unseen derivation and the open `seen`-bit question; silent-EOF crash semantics; enumerated `terminal.closed` reasons; upstream request 5 (`herdr server start`). Answered two open questions (observer sizing, `terminal_id` across handoff) from source; Phase 0 refocused from discovery to runtime confirmation plus the items only measurement can answer.
