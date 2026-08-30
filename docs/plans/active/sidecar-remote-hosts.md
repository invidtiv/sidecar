# Sidecar as its own remote host runtime

Status: **active, Phase B complete — interactive remote hosts behind a flag**, 2026-08-29

Related: [Herdr as Sidecar's remote host runtime](herdr-remote-hosts.md) — the competing alternative for the same deliverable; see [Relationship to the Herdr plan](#relationship-to-the-herdr-plan) for what decides between them. [Hosting Herdr plugins in Sidecar](herdr-plugin-support.md) is orthogonal to both.
Evidence: all claims verified against the Sidecar codebase on `main` (citations inline); the Herdr comparisons reference the source inspection at `c2637dc1` recorded in the Herdr plan. Phase 0 measurements, transcripts, and findings are in [docs/evidence/sidecar-remote-hosts-phase0.md](../../evidence/sidecar-remote-hosts-phase0.md). Phase B's final-candidate tests and isolated two-machine proof are in [docs/evidence/sidecar-remote-hosts-phase-b.md](../../evidence/sidecar-remote-hosts-phase-b.md).

## Decision first

Sidecar becomes its own remote host agent. To see what is running on the Mac mini, the user installs Sidecar there — nothing else — and registers `remote:mac-mini` in the local Sidecar. The remote machine's shells, worktrees, agent states, and live panes appear in the Sessions browser, served by two channels over SSH:

1. **Pane content: proxied tmux control mode.** The local Sidecar attaches to the remote tmux server by spawning `ssh host tmux -C attach-session -f ignore-size -t <session>` instead of a local `tmux -C`. This is not a new protocol — it is tmux's own control protocol, which Sidecar's entire terminal stack already consumes, arriving over a different pipe.
2. **Sidecar-level truth: `sidecar host serve`.** A headless, ephemeral, read-only (until Phase C) process spawned on the remote host over SSH stdio, running the existing inventory/liveness/agent-status stack and streaming snapshots and status transitions as versioned JSONL.

The "new streaming transport" is therefore mostly channel 2, and it is small: the pane-bytes problem — the hard, latency-sensitive, high-bandwidth part — is solved by reusing a protocol both ends already speak.

## Why the codebase is already most of the way there

Three findings make this plan an integration exercise rather than a build:

**The control-mode consumer is transport-agnostic behind one seam.** `newProcessControlChannelCommand(session, cmd *exec.Cmd)` (`internal/tty/control_transport.go:70-116`) wires any `exec.Cmd`'s pipes into the line parser; nothing below it knows the command is local. Production hardcodes the local spawn via `controlChannelFactory` (`control_transport.go:31`, `control_manager.go:146-150`). Swapping the factory's `exec.Cmd` for an ssh invocation carries the entire downstream stack unchanged: the single ordered-actor delivery (`control_manager.go:606-630`), seed transactions and race detection (`control_model.go:261-277, 529-540`), the byte-fed `screenmodel` with 30 fps publication (`control_model.go:194`), `%pause`/`%continue` reseeds, the capture path with its 12 ms coalesce, and the polling fallback that engages on control failure.

**The geometry problem was already solved for exactly this case.** `geometry_lease.go`'s own header describes the scenario: "Two sidecar instances on two machines can be attached to one tmux server. Each asserts its own pane geometry unconditionally, the last resize-window wins … a continuous ping-pong" (td-ee222a, `internal/tty/geometry_lease.go:16-21`). The lease store is a tmux session option — the server is the shared medium, so remote and local claimants coordinate through the same store with no extra machinery; tokens carry durations, not timestamps, precisely so they survive clock skew across machines (`geometry_lease.go:42-44`); the decision function is state-free "so a headless caller can adopt it unchanged" (`geometry_lease.go:160-191`). And `pane_fit.go` is already the non-owner's rendering path — clip with cursor-anchored offsets when the pane is bigger, letterbox when smaller, never stretch, with the "200x50, showing 120x40" indicator (`pane_fit.go:5-14, 169-177`). A read-only remote viewer is a lease non-owner by definition and reuses this wholesale.

**The awareness stack is UI-free by construction.** `workspaceinventory`, `shellliveness`, `shellstate`, `tmuxserver`, `agentactivity`, `agentstatus`, `activitystore`, `workspaceops`, and every `adapter` have zero Bubble Tea references (verified by grep); collectors take injectable runners, captures, and clocks (`workspaceinventory/inventory.go:226-239`); `workspaceops` already exposes shell and worktree creation as plain functions exercised headlessly by `sidecar create` today. A headless server is orchestration over existing libraries — the one piece of UI choreography to re-implement headlessly is the reap sequence in `internal/overview/shell_liveness.go` (~150 lines), and Phase A does not reap at all.

## Relationship to the Herdr plan

Both plans deliver the same product surface: a **Host** in the Sessions browser. They differ in the on-host agent, and each is honest about what that buys:

| Axis | Herdr on the host | Sidecar on the host |
| --- | --- | --- |
| On-host dependency | Herdr installed *and its server running*; no CLI verb to start it detached (upstream request filed) | Sidecar installed. No daemon at all — `host serve` is spawned per connection over SSH stdio and dies with it |
| Pane streaming | Herdr's frame protocol: absolutely-positioned cell blits, full frame on resize, no offset/range history | tmux control mode: `%output` deltas, absolute history addressing via `capture-pane -S/-E`, so lazy history, frozen selection, and search work remotely unchanged (`internal/tty/history.go`, `capture_range.go:49-81`) |
| Agent status | Herdr's detection manifests, computed by its server | Sidecar's own `agentactivity`/`agentstatus` stack — identical semantics to local, same lanes, same done-TTL, same provider detectors |
| Transcripts | Not available (`agent.read` is screen text; stores unreadable remotely) | The adapter stack runs on the host and can serve session lists/messages — a capability the Herdr path can never offer |
| Creation fidelity | `layout.apply` workaround for the argv gap; Phase C risk | `workspaceops.CreateManagedShell`/`LaunchWorktreeSession`, the same code paths as local, already headless |
| Protocol risk | Herdr's protocol integer moves fast (19→20→21); string-matched close reasons; silent-EOF crash semantics | Both ends are this repo; protocol versioned here, errors structured here |
| What it costs | Depends on a third-party product's pace and priorities | Sidecar must build the host protocol, a headless entry point, and maintain its own remote transport forever |
| Terminal runtime on the host | Herdr **is** the terminal runtime — it sees panes it owns | tmux — Sidecar sees any tmux session on the default server, including shells no Sidecar created |

Decision posture: **both plans stay active through their Phase 0 spikes; this plan's spike is cheaper and should run first.** If proxied control mode over a real link feels as good as the seam analysis predicts, the Herdr path's remaining advantage is Herdr-native users' workflows, not capability, and the Herdr plan should then be re-scoped or deprecated with that evidence cited. If the spike finds the SSH round-trips make in-band capture sluggish in a way Herdr's push-frames avoid, that is the number that keeps the Herdr plan alive. The shared pieces — host registry, SSH/ControlMaster transport (adopt the ssh recipe recorded in the Herdr plan: `-S <dir>/ctl -o ControlMaster=auto -o ControlPersist=yes -T`, generated `-F` config with `ServerAliveInterval 15`/`CountMax 4`, `ssh -O exit` teardown), Sessions-browser host grouping, `HostID` on `workspaceinventory.Workspace` — are identical in both plans and are not throwaway whichever wins.

## Scope boundary

**In scope**

- A host registry and SSH transport (shared shape with the Herdr plan).
- `sidecar host serve`: headless, stdio, versioned JSONL; read-only through Phase B.
- Read-only remote observation: inventory, agent status with full local fidelity, live pane view via proxied control mode.
- Interactive remote panes (Phase B): in-band input, cross-host geometry lease rules.
- Remote creation (Phase C): shells and worktrees through the existing `workspaceops` pipeline.
- Remote conversations via the adapter stack (Phase C, gated on demand).

**Explicitly out of scope**

- Any change to local behavior. The transport injection must leave the local path byte-identical.
- A persistent daemon on the remote host. Serve processes are per-connection and ephemeral; if that ever changes, it changes in its own plan.
- Exposing anything to a network. SSH stdio is the only transport; serve never binds a socket.
- Remote git/diff/file browsing beyond what inventory already summarizes. Same boundary as the Herdr plan.
- A Sidecar plugin system. Noted as a possible future direction (and [the Herdr-plugin plan](herdr-plugin-support.md) takes the opposite bet); neither is prerequisite to this work.

## Architecture

```text
Sidecar (viewer)
├── internal/hosts                 NEW — registry, health, SSH transport (ControlMaster; shared with the Herdr plan)
├── internal/tty                   control channel factory becomes host-aware; alternate terminalInputSender
├── internal/hostproto             NEW — the serve protocol: types, version, encode/decode (shared by both ends)
├── internal/workspaceinventory    gains HostID; TmuxName stops leaking upward (owed regardless of this plan)
├── internal/overview              Sessions browser host grouping; remote rows fed by the serve stream
└── internal/paneframe/panelayout  unchanged — a remote pane is an ordinary leaf

Sidecar (host, same binary)
└── internal/hostserve             NEW — `sidecar host serve`: collector loop, liveness tracker,
                                   activity trackers, status resolution, JSONL stream over stdio
```

### Channel 1 — pane content

- **Attach:** the host-aware `controlChannelFactory` builds `ssh <target> tmux -C attach-session -f ignore-size -t <session>` through the ControlMaster transport. Everything downstream is untouched.
- **Input (Phase B):** a second `terminalInputSender` implementation (`internal/tty/terminal_surface.go:45-166` is the interface) that routes `send-keys`/paste through the control channel's own stdin (`Send`/`SendPair`, `control_transport.go:14-29`) instead of spawning subprocesses — one write on an open pipe rather than one ssh exec per keystroke, preserving the FIFO ordering the send queue exists for (td-8fcd2e, `send_queue.go:48-101`).
- **History, metadata, captures:** the in-band command path already exists — the capture path issues `display-message` + `capture-pane` pairs through the control channel today (`control_manager.go:1115-1129`). `CapturePaneRange`'s absolute `-S/-E` windows move in-band the same way, keeping `HistoryReach`, `WindowFreeze`, and search working remotely with their existing contracts.
- **Resize (Phase B):** `assertDimensions` currently restarts the control transport on every geometry change (`tty.go:1376-1436`). Locally that is cheap; over SSH it is a subprocess respawn through the master connection (~fast, but measured, not assumed — Phase 0 item 3). If the number is bad, the work item is teaching the transport to reseed without a process restart, which is a local-path improvement too.
- **Out-of-band one-shots** (`ResizeTmuxPane`, `QueryPaneSize`, lease option reads/writes) run as `ssh <target> tmux …` through the master connection, or move in-band where ordering matters.

### Channel 2 — the serve protocol

`ssh <target> sidecar host serve --stdio`, spawned by the viewer per connection. Newline-delimited JSON, both directions, defined once in `internal/hostproto`:

- **Hello:** `{proto, version, host, os, tmuxPresent, projects: N}` — protocol integer checked first; the version string is display-only. The running version currently lives in `main`-only ldflags (`cmd/sidecar/main.go:57-63`); a library accessor is a small prerequisite work item.
- **Snapshot:** the remote machine's own `config.json` `projects.list` (discovery mechanism unchanged — `internal/config/config.go:54-59`), each project's shells.json contents, worktrees, and resolved `agentstatus.Presentation` per workspace — the same `CollectProjectInventory` → `RefreshProjectStatus` pipeline the overview runs (`workspaceinventory/inventory.go:442-556`), driven by serve's own loop at the overview's adaptive cadence (5 s live / 10 s ready / 30 s idle).
- **Events:** inventory deltas and presentation transitions, pushed as they happen. This is the event stream the Herdr plan could only request upstream; here it is simply built. `notify.LaneTracker` — written so "a headless watcher tomorrow" reimplements only the adapter file (`internal/notify/triggers.go:11-29`) — is that adapter's second consumer.
- **Previews:** the status pass already captures ~80 lines per agent pane (`inventory.go:562-635`); serve ships that text (bounded by the existing `tmuxCaptureMaxBytes` discipline) so Sessions-browser preview cells work without opening a control channel. Full pane view on selection uses channel 1.
- **Requests (Phase C only):** create shell / worktree / start agent, mapping 1:1 onto `workspaceops` functions that the `sidecar create` CLI already exercises headlessly.

Serve is **read-only until Phase C** — it never writes shells.json, never reaps, never takes a geometry lease, and never resizes (the one capture path that resizes, the lease-gated semantic preview at `workspace/agent.go:905-925`, is disabled under serve). When Phase C adds mutations, they go through the existing guarded writers: flock + read-modify-write (`shellstate.go:294-336`), tombstones instead of deletions, incarnation fencing — the exact hardening added after the td-8d18de/shells-wipe incident, which is also why serve gets no bespoke state-writing code of its own.

### Ephemeral serve dissolves the daemon problem

Herdr needs its server because the server *is* the terminal runtime. Sidecar's remote truth lives in the tmux server and the state tree, both of which outlive any Sidecar process — so serve can be spawned per connection and die on disconnect. There is no autostart question, no stale-daemon question, and no version-skew-restart question: the viewer spawns whatever binary is on the host, reads its protocol integer from the hello, and renders "sidecar too old on <host> (proto 3, need 4)" as an actionable row state. Multiple viewers each spawn their own serve; concurrent read-only serves against one state tree are the already-normal multi-instance case (flocked writes, atomic renames, fsnotify cross-instance visibility — `overview/live_shells.go:27-40`).

### Geometry across hosts (Phase B)

The lease already coordinates cross-machine through the tmux option store. Two rules need sharpening for a claimant whose PID is not on the tmux server's machine, both localized in `DecideGeometryLease`'s policy:

- **No defunct-PID reclaim of foreign tokens.** `OwnerDefunct` liveness checking is only valid on the owner's machine (`geometry_lease.go:143-147`); a remote viewer treats any token from another host as live and relies on idle/stale preemption only.
- **Input evidence is local.** The tty-matched `client_activity` harvest (`geometry_lease.go:340-371`) doesn't exist for a remote claimant; its "I typed recently" evidence comes from its own send-queue timestamps instead.

Everything else — duration-not-timestamp tokens, tick-based staleness, back-off on a fresh foreign token — was designed for this and transfers unchanged. A read-only viewer never claims; an interactive viewer claims on focus exactly as the local interactive mode does, and the human sitting at the remote machine wins it back by typing, per the idle-preemption rule that already exists.

### Where it binds in the UI

Identical to the Herdr plan, and stated once: remote panes are ordinary `paneframe` leaves; the remote pane kind gets one `livepanes.Binding` per surface fed by the frame stream rather than `livewatch`; hosts are overview-only until a remote checkout earns a project identity; nothing runs before the first frame (`SIDECAR_STARTUP_TRACE=stderr` must show no new pre-frame phase). `workspaceinventory.Workspace` gains `HostID` and drops the `TmuxName` leak — owed whichever remote plan ships, or neither.

## Work items surfaced by the research

- ~~**Library-visible version/protocol accessor** (currently `main`-only ldflags).~~ **Done in Phase 0** — `internal/buildinfo`.
- **Login-shell binary resolution is mandatory, not an optimisation.** A non-login ssh shell has no `/opt/homebrew/bin` on PATH, so a host with tmux plainly installed reports `tmux: executable file not found`. The remote command must be wrapped in `$SHELL -l -c CMD` — and specifically not `$SHELL -l -s`, which additionally runs the shell's interactive preexec hooks and writes OSC sequences onto the same stdout the protocol uses. Both forms were measured on a real host; `internal/hosts.RemoteShell` implements the safe one.
- **A "stream is not the protocol" row state.** Some host will have a login profile that prints to stdout regardless. The viewer must name that specific failure and its fix rather than surfacing a JSON syntax error.
- **Server death is not an incarnation change.** `tmux kill-server` does not unlink its socket (verified by inode), so `tmuxserver.Socket()` reports the same identity across a death; the incarnation only moves when a new server recreates the socket. "The remote server died" must be driven by the pane listing going empty — which is exactly the condition the reaper must refuse to act on (td-8d18de).
- **Linux process identity.** `process_identity_other.go` is a stub — argv0 disambiguation of shared-runtime panes (node/bun/agent) silently degrades to screen-chrome detection on Linux, and remote hosts will often be Linux. A `/proc`-based implementation (tpgid from `/proc/<pid>/stat`, argv0 from `cmdline`) is a Phase A item that also improves any Linux user's local fidelity today.
- **Host-aware control channel factory + remote `terminalInputSender`** — the two seams in `internal/tty`.
- ~~**Resize-without-transport-restart** if Phase 0 item 3 says the respawn is felt.~~ **Measured and dropped from Phase A.** A reseed over ssh costs 82–383 ms on a real link and 258–854 ms at 150 ms RTT — noticeable but not felt on a debounced, lease-gated, deliberate act. Revisit on Phase B experience, not on principle.
- **Headless reap choreography** — not before Phase C, and only by porting the overview's guards (empty-listing skip, incarnation fence, tombstone writes), never fresh logic.

## Phases

### Phase 0 — spike ✅ complete (2026-08-29)

A second machine with Sidecar installed, a real link (LAN and a WAN/VPN hop), agents running in tmux sessions over there.

1. **Proxied control mode.** Swap the factory `exec.Cmd` for the ssh invocation against a remote session. Verify seed, byte continuity, `%pause` reseeds, and history windows behave identically to local. Measure: keystroke-to-frame latency (once input lands in Phase 0 item 4), output-burst throughput, idle cost (should be zero bytes), and seed cost on attach.
2. **Headless serve.** Drive `Collector` + `shellliveness.Tracker` + activity trackers + `agentstatus.Resolve` in a loop with no Bubble Tea, streaming snapshot + transitions as JSONL to a local consumer. Exit check: status shown locally matches the remote machine's own Sidecar TUI for working/blocked/done/idle across at least three agent providers, including the `done` decay.
3. **Resize cost.** Measure the control-transport restart over SSH per geometry change; decide whether reseed-without-restart is Phase A work or deferrable.
4. **In-band input.** Prototype the control-channel `terminalInputSender`; verify FIFO ordering under fast typing and paste; measure RTT per keystroke batch on the WAN hop.
5. **Failure axes.** ssh drop mid-stream (keepalive detection, fallback engagement, clean reattach); remote tmux server death (incarnation transition must mark rows dead, never wipe anything); sidecar missing/too old on the host (distinct actionable states); two viewers on one host; a viewer plus a human TUI on the host.

**Exit gate:** a recorded matrix of latency/bandwidth/CPU numbers and the answer to the only existential question: does a proxied-control-mode pane feel local-grade? Compare directly against the Herdr plan's Phase 0 numbers if both spikes run; this table is the bake-off input.

**Result: passed. Proceed to Phase A.** Run against `marcusbook` over LAN, Tailscale, and two shaped-latency columns, with both machines' default tmux servers and real state trees provably untouched. Headlines:

- **A proxied pane is local-grade.** Attach + first frame 94 ms on a LAN, 876 ms at 150 ms RTT. Seed 623 bytes. An idle pane costs **zero `%output` notifications**. A 256 KiB burst converges in 230 ms (LAN) / 913 ms (150 ms RTT) at 8–27 fps. No fallbacks in any run.
- **In-band input is worth building and the number says why.** Its overhead is a near-constant ~23 ms above link RTT (10.4 / 83.7 / 173.3 ms at 0 / 60 / 150 ms). Out-of-band — one `ssh tmux send-keys` per batch — costs 2.1×–6.2× more. FIFO held across 40 back-to-back batches on every link.
- **Serve matches the host's own TUI exactly.** Three providers, same lanes, same providers, same attention flags, same rows, at the same moment — because serve runs `agentactivity` and `agentstatus.Resolve` on the host over the host's own captures. Previews ship for free by decorating the capture the status pass already takes.
- **Failure axes held.** ssh drop → fallback in 0 ms and clean reattach. Remote tmux death → rows went `paused`, and `shells.json` was **byte-identical before and after**. Two viewers coexisted; a human's TUI on the host coexisted with a viewer; neither disturbed the other.
- **Not proven:** done-TTL decay over the wire, a Linux host (both machines are arm64 macOS, so the `process_identity` Linux stub was never exercised), a relayed WAN path, and a full day of use.

The three findings that change Phase A are recorded in the work items above and in the evidence document.

### Phase A — read-only remote hosts ✅ complete (2026-08-29)

Behind `features.SidecarRemoteHosts`, default off.

- Host registry with per-host config (ssh target, optional remote binary path — resolved via the login-shell probe recipe recorded in the Herdr plan — optional remote config path).
- Sessions browser host grouping; per-host projects/shells/worktrees with the full local status vocabulary; preview cells from serve captures.
- Live read-only pane view on selection via proxied control mode, rendered through `FitPane` at the pane's own size; never resizes, never claims a lease.
- Health as first-class row states: unreachable, no sidecar, protocol too old, no tmux server, stale data. Each names the fix.
- Linux process identity; version accessor; protocol v1.

**Exit gate:** the Herdr plan's, verbatim — on a real second machine, Sidecar answers "what is running over there and is anything blocked?" without opening a terminal, and a full day of use produces no stale or wrong status while a pane is on screen. Plus: the local path is provably untouched (no behavior or state diff with the flag off, and none with it on until a host is registered).

**Result: the gate is met except for the day of use.** Driven end to end against `marcusbook` from a fully isolated local Sidecar:

- **The question is answered without opening a terminal.** The Sessions browser showed `marcusbook · spike Claude pane` and `Opencode pane` under NEEDS ATTENTION, `Codex pane` under WORKING, and the worktree and plain shell under LIVE — the remote machine's real lanes, resolved by its own detectors.
- **Remote rows are ordinary rows.** They group, filter, sort and pin through the existing projections because `hosts.ProjectResults` converts a snapshot into `workspaceinventory` results carrying a `HostID`. Host grouping is the project label; no new renderer.
- **Health is a row, not a silence.** A deliberately unreachable second host showed `⚠ ghost` with ssh's own reason and the fix. Each state names one.
- **The live pane works.** Selecting the remote Claude row opened its actual screen through proxied control mode — the agent's question, rendered locally.
- **Read-only is enforced at three seams, not one.** Input is dropped, the pane is never resized and no lease is claimed, and the capture fallback is disabled rather than left pointing at local tmux — a local `capture-pane -t %4` for a remote `%4` does not fail, it paints an unrelated local pane. Interactive mode is refused outright rather than entered inertly.
- **Rollback is provable.** With the flag off: no rows, no registry, no ssh process. `SIDECAR_STARTUP_TRACE` shows the first ready frame at 63.7ms with an unreachable host configured, and no host phase before it.
- **Both machines were untouched:** default tmux servers and real state trees unchanged, run roots removed.

**Not met:** the full day of use. That is a soak, not a build step, and it is the remaining Phase A evidence.

The retained code was then independently reviewed. One CRITICAL defect, two HIGH, and several medium findings were fixed; the critical one is worth recording because no unit test would have found it and the real run did not surface it either:

**A `tty.Model` made remote stayed remote forever.** The preview reuses one terminal model across row selections, and `UseRemoteControl` had no counterpart — so after viewing a remote pane once, the next LOCAL row was opened by `ssh <host> tmux -C attach-session -t <local session name>`. Both machines run Sidecar and derive session names the same way, so that attach often *succeeds*: another machine's pane painted into a local workspace's preview, interactive mode offered and silently swallowing every keystroke, and the local pane never resized again. The fix is `UseLocalControl`, and the rule that the surface SETS the mode on every activation rather than changing it when it notices a difference. Pinned by a test that fails when the reset is removed.

Two more that mattered: preview content panes (diff, file finder, doc) resolved a remote workspace's path against the LOCAL filesystem — and on a machine with the same checkout that succeeds, showing this machine's diff under the remote row's name; and `tty.Target.Host` was dropped by the stored target, so "am I already showing this?" could never answer yes for a remote pane and every poll tore down its ssh connection and reseeded.

Three things the real run found that no unit test would have:

1. `hosts` in config.json parsed into nothing. The loader merges a `rawConfig` field by field, so a key present only on `Config` is silently ignored — a correct config producing no hosts and no error.
2. ssh's ControlMaster socket blew the ~104-byte unix path limit under macOS's `$TMPDIR` (`/var/folders/<2>/<28>/T/`), surfacing as an unreachable host with no hint of the cause. The control root is now under `/tmp`.
3. A registered host was invisible until its first connection resolved — and an ssh dial to a machine that is off runs to a full connect timeout. Initial health is now published at registration.

Host configuration is live-reloaded. Saving config refreshes feature resolution, reconciles the host registry, closes a selected terminal when its host is removed or retargeted, and rejects queued updates from the replaced host-client incarnation. This is the Phase B completion of td-998e58; restart-only host configuration is no longer a product limitation.

### Phase B — interactive remote panes ✅ complete (2026-08-29)

- In-band input uses the existing host-aware control pipe and a model-level FIFO, preserving exact ordering for fast keys, literal input, paste, mouse reports, lease changes, and backend replacement.
- Remote panes enter the Sessions browser's ordinary interactive mode with the same chrome, reserved chords, double-escape behavior, and immediate exit semantics as local panes.
- Cross-host leases use viewer-local input evidence, never foreign PID liveness. Interactive owners refresh at a settled size, blur/exit releases safely, and either machine restores its current viewport when its human input preempts the other.
- Complete history, search, match navigation, and frozen selection use the terminal model's host-aware in-band `CapturePaneRange`; no remote fallback can read an ambient local pane with the same ID.
- Remote resize remains a lease-gated explicit act through the existing debounced restart/reseed path. Generation and incarnation fences reject late activation, resize, host-update, and old-backend teardown work.
- Config saves refresh feature resolution and reconcile hosts without restart, including removal, same-ID retarget, selected-terminal teardown, and queued old-client rejection (td-998e58).

**Exit gate:** a blocked agent prompt on the remote host answered from the local Sidecar, with input ordering correct under fast typing, and a human at the remote machine able to take the pane back just by using it.

**Result: passed.** The final `7cff6d1e` candidate was driven between isolated Sidecars on `aerie` and `marcusbook`. Exact ordered input arrived on the remote pane. With deliberately different viewports, viewer input reclaimed and restored 103×45, then remote-human input reclaimed and restored 73×30 without either side leaving interactive mode. Viewer exit preserved the human's lease; human exit removed it. Both default tmux servers and real Sidecar state trees were untouched. Full repository tests, build, focused race tests, and independent review passed; see the Phase B evidence document.

### Phase C — creation, mutation, conversations

- Serve gains a request channel: create shell, create worktree (with the setup-hook confirmation flow surfaced locally), start agent, rename — all mapping onto existing `workspaceops` functions with their existing guards.
- Reap parity for remote rows, by porting the overview's guarded choreography.
- Remote conversations: the adapter stack served over the protocol (session lists first; messages on demand). This is the capability that exists on no other path and should be scoped by real demand, not built speculatively.

**Exit gate:** create → work → observe → answer → close entirely from the local Sidecar, with remote state trees left exactly as a local Sidecar would leave them, verified across a remote tmux restart and a serve reconnect.

## Failure, degradation, security

- **Degrade the host, not the app** — a dead link marks one host offline; never blocks a frame; never touches local state (inherited verbatim from the Herdr plan).
- **Serve is read-only until Phase C, and provably so** — Phase A/B builds carry no state-writing code paths in `hostserve` at all, which is a stronger guarantee than a runtime flag.
- **The reaper never runs remotely until it has all three local guards.** The shells-wipe class of bug (td-8d18de) is the named hazard; empty-pane-listing skip, incarnation fencing, and tombstones are the named mitigations, already in the writers serve would eventually call.
- **SSH is the entire trust boundary.** Serve speaks stdio only; no sockets, no ports, no forwarding. The user's ssh config, keys, and agent are the security model, same as the Herdr plan.
- **Isolation discipline extends remotely.** Serve honors `SIDECAR_ISOLATED_STATE` and the `AssertIsolatedPath` guards, so proof runs against a remote host can never touch a real remote state tree — the same rule `tmux-drive.sh` enforces locally.
- **Bound everything:** serve output framed and capped; preview captures under the existing byte cap; control mailbox overflow already forces clean reseed (`terminal_surface.go:21, 301-312`); reconnect with backoff.

## Open questions

- **Does the serve loop's capture pass disturb a human using the remote machine?** It reuses the overview's semaphore-bounded, non-resizing observation, which coexists with a local TUI today — but "today" is same-machine; Phase 0 item 5 confirms nothing changes when the observer is remote-spawned.
- **One serve per viewer vs one serve shared.** Per-viewer is the simple default and matches the ephemeral design; if a host ever has many viewers, the collector work duplicates. Revisit only on evidence.
- **Namespace/socket scope.** Inventory correlates shell rows only on the default tmux socket namespace (`inventory.go:549`); whether remote hosts should surface non-default-socket sessions follows the local answer, whatever it becomes.
- **Should the hello carry host capabilities** (process-identity fidelity, adapter availability) so the viewer can render honest confidence per host? Likely yes and cheap; decide with the protocol v1 schema.
- **Bake-off criteria vs the Herdr plan** are recorded in [Relationship](#relationship-to-the-herdr-plan); the open question is who runs both spikes and when, not what to measure.

## Acceptance evidence

| Journey | Evidence |
| --- | --- |
| Startup | `SIDECAR_STARTUP_TRACE=stderr` shows no host work before the first ready frame, with an unreachable host configured |
| Host health | unreachable / no sidecar / protocol too old / no tmux / stale each render a distinct row state naming the fix |
| Status truth | Remote lanes match the remote machine's own Sidecar TUI, including `blocked` and `done` decay, across ≥3 providers |
| Live pane | A remote full-screen TUI, wide chars, colors, and alt-screen render correctly through the proxied control channel; idle costs zero bytes |
| History | Lazy history, search, and drag-selection during output work on a remote pane with the same contracts as local |
| Geometry | A read-only viewer never resizes anything; viewer and remote-human input preempt each other without re-entry and restore their own distinct viewports |
| Coexistence | A human's Sidecar TUI on the remote host sees no behavior change while a viewer observes; two viewers coexist |
| Reconnect | Link drop and tmux-server restart both reconverge (incarnation transition → dead rows → recovery), with nothing wiped |
| Isolation | Serve under `SIDECAR_ISOLATED_STATE` refuses to touch a real state tree |
| Rollback | Flag off → local behavior and state byte-identical |

Latency, bandwidth, and remote CPU are measured and recorded in the Phase 0 matrix, side by side with the Herdr plan's numbers if both spikes run. The decision between the two plans is made from that table, on the record.

## Changelog

- **2026-08-29** — Phase B completed and independently reviewed. Added ordered in-band remote input, Sessions interactive/search/history parity, host-aware lease and capture paths, bidirectional input-driven geometry takeover, nonblocking ordered teardown across control/backend replacement, and live host-config reconciliation including td-998e58. The final isolated two-machine gate passed with distinct 103×45 and 73×30 viewports; evidence is recorded in `docs/evidence/sidecar-remote-hosts-phase-b.md`.
- **2026-08-29** — Phase A built and driven end to end against a second real machine: host registry and config, a long-lived serve client with reconnect/backoff and a health vocabulary that names its fix, `HostID` on the inventory, remote rows in the Sessions browser and the Activity board, host grouping, health rows, a read-only live pane over proxied control mode, preview content from serve captures, and Linux `/proc` process identity. Behind `features.SidecarRemoteHosts`, default off. Remaining Phase A evidence: a full day of use.
- **2026-08-29** — Phase 0 run end to end against a second real machine. Verdict: proceed to Phase A. Resize-without-restart dropped from Phase A on measured evidence; login-shell resolution, a not-the-protocol row state, and the incarnation-vs-death distinction added as required Phase A items. Retained: `internal/hostproto`, `internal/hostserve`, `internal/hosts`, `internal/tty/control_remote.go`, `internal/buildinfo`, `sidecar host serve|probe`, and the spike harnesses under `scripts/`.
- **2026-08-28** — Created, from source research into the control-mode transport seam, the geometry lease's explicit two-machine design (td-ee222a), the UI-free awareness stack, shells.json v2 hardening, and the headless-readiness of `workspaceops`. Positioned as the competing alternative to the Herdr remote-hosts plan with a recorded bake-off posture.
