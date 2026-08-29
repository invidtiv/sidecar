# Phase 0 spike evidence — Sidecar as its own remote host runtime

Plan: [docs/plans/active/sidecar-remote-hosts.md](../plans/active/sidecar-remote-hosts.md)
Date: 2026-08-29
Verdict: **proceed to Phase A**, with three findings that change what Phase A must build.

## What was actually run

A second real machine, `marcusbook` (MacBook Pro, macOS 26.6.2, arm64, tmux 3.7b), observed from this machine (`aerie`) over two real links and two shaped ones. The spike binary was built from this working tree and copied to a temp directory on the host; the host's own installed Sidecar was never invoked or replaced.

**Isolation held on both axes, on both machines, for the whole run.** Every remote tmux session lived on a private socket under a private `TMUX_TMPDIR`; every remote Sidecar process ran with `XDG_STATE_HOME` and `-config` inside the run root and `SIDECAR_ISOLATED_STATE=1`. Verified before and after: the host's default tmux server kept its one live session throughout, and `~/.local/state/sidecar` kept its 7 entries. Nothing on either machine's real tree was read or written.

Links measured:

| Column | What it is | Baseline RTT |
| --- | --- | --- |
| LAN | `marcusbook-pro.local` over Wi-Fi | 9–100 ms, high variance (Wi-Fi power save) |
| Tailscale | `100.117.87.108` | 8.0 ms, stable — **direct path over the same LAN**, not a relayed WAN |
| WAN-60ms | LAN through `scripts/spike-latency-proxy` | 60 ms, deterministic |
| WAN-150ms | same, 150 ms | 150 ms, deterministic |

The Tailscale hop negotiated a direct connection to `192.168.68.62`, so it measured as a second LAN column rather than a WAN one. The shaped columns exist because two identical columns answer nothing about the range that decides whether this feels local. The shim adds latency only — no jitter, no loss, no bandwidth cap — so the throughput row is a link measurement, not a WAN simulation.

## Matrix — proxied control mode (Phase 0 items 1, 3, 4)

Measured through the production control stack: the real `ControlManager`, its single ordered actor, seed transactions, and the byte-fed screen model. Only the spawned command changed.

| Measure | LAN | Tailscale | WAN-60ms | WAN-150ms |
| --- | --- | --- | --- | --- |
| Attach + first frame | 94 ms | 93 ms | 572 ms | 876 ms |
| Seed bytes (empty pane) | 623 B | 623 B | 623 B | 623 B |
| Idle cost, 5 s | 1817 B, **0 output notifications** | 1224 B | 1817 B | 1817 B |
| Output burst, 256 KiB generated | 349,595 B wire / 230 ms / 21.7 fps | 349,595 B / 188 ms / 26.6 fps | 349,595 B / 504 ms / 11.9 fps | 349,595 B / 913 ms / 7.7 fps |
| Effective throughput | 1482 KiB/s | 1814 KiB/s | 677 KiB/s | 374 KiB/s |
| Resize → reseed | 82 / 233 ms | 104 / 383 ms | 144 / 481 ms | 258 / 854 ms |
| **In-band** send-keys RTT (mean) | **10.4 ms** | 14.4 ms | **83.7 ms** | **173.3 ms** |
| **Out-of-band** send-keys RTT (mean) | 64.0 ms | 46.8 ms | 200.8 ms | 360.8 ms |
| FIFO under 40 back-to-back batches | preserved | preserved | preserved | preserved |
| Fallbacks during the run | none | none | none | none |

Reproduce: `internal/tty/remote_control_spike_test.go`, opt-in via `SIDECAR_SPIKE_HOST`.

### What the numbers say

**A proxied-control-mode pane is local-grade.** Seed is a few hundred bytes and one round trip. An idle pane costs **zero `%output` notifications** — the plan's assertion holds exactly; the 1.8 KB observed over 5 s is the periodic `client_discarded` probe, which is Sidecar's own traffic and not proportional to pane activity. A 256 KiB burst converges in 230 ms on a LAN and still in under a second at 150 ms RTT, at 8–27 model frames per second.

**In-band input is worth building, and the number says why.** The overhead the protocol itself adds is a near-constant ~23 ms on top of the link RTT: 10.4 ms at ~0 RTT, 83.7 at 60, 173.3 at 150. Out-of-band — one `ssh tmux send-keys` per batch, which is what today's sender would do if pointed at a remote host — costs 2.1× to 6.2× more, because each keystroke pays a fresh remote process execution. In-band input turns keystroke latency into roughly one RTT.

**Resize is cheap enough to defer.** The plan flagged control-transport restart per geometry change as possible Phase A work. Measured, a reseed costs 82–383 ms on a real link and 258–854 ms at 150 ms RTT. That is noticeable but not felt on a debounced, deliberate, lease-gated act. **Reseed-without-restart is not Phase A work.** Revisit only if Phase B's interactive resize feels slow in use.

## Headless serve (Phase 0 item 2)

`sidecar host serve --stdio` ran on the host and streamed versioned JSONL over the ssh pipe: hello, snapshot, status transitions, structured errors.

**Exit check: passed.** Three providers, side by side at the same moment, with the same isolated state tree.

Serve's stream (`docs/evidence/phase0-serve-snapshot.jsonl`):

```
Claude pane    live=True  prov=claude    lane=blocked  attention=True
Codex pane     live=True  prov=codex     lane=working  attention=False
Opencode pane  live=True  prov=opencode  lane=blocked  attention=True
Plain shell    live=True  prov=-         lane=-        attention=False
project        live=True  prov=-         lane=-        (worktree)
```

The host's own Sidecar TUI, at the same moment (`docs/evidence/phase0-remote-tui.txt`):

```
◆ NEEDS ATTENTION (2)     ◇ spike Claude pane / claude
                          ◇ spike Opencode pane / opencode
● WORKING (1)             ● spike Codex pane / codex
● LIVE (2)                ◎ spike project / main
                          ◎ spike Plain shell
```

Identical: the same lanes, the same providers, the same attention flags, the same rows. Serve resolves status by running `agentactivity` and `agentstatus.Resolve` on the host over the host's own captures, so remote status is not an approximation of local status — it is the same computation.

The agent panes replay this repo's own captured provider screens (`internal/agentactivity/testdata/`), reproducing `pane_title`, `pane_current_command` and screen text, so the genuine detectors ran. Evidence IDs in the stream are the real ones: `claude.screen.blocked`, `codex.title.working`, `opencode.screen.blocked`.

Previews ship for free: the status pass already captures ~80 lines per agent pane, so a capture decorator retains that text with no collector change, no second capture, and no extra load on the host (176 B / 100 B / 61 B on the rows above).

**Done decay** was not observed end to end over the wire — a 10-minute TTL is not a spike-shaped wait, and the codex `completed.txt` fixture's own recorded expectation is `idle`, which is what serve produced. What was verified: a real lane transition (`working → idle`) streamed as a status event, and the done→idle decay covered as a unit test over the delta encoder. The TTL itself is `agentstatus.DefaultDoneTTL` running unmodified inside serve, so decay is inherited rather than reimplemented.

## Failure axes (Phase 0 item 5)

| Axis | Result |
| --- | --- |
| ssh drop mid-stream | Fallback engaged in **0 ms** (`tmux control: reader EOF`); reattach after toggling visibility produced frames again. A dead link never leaves a pane looking live. |
| Remote tmux server death | Rows transitioned `blocked → paused`, `working → paused`. **`shells.json` was byte-identical (620 B) before and after.** Nothing wiped. |
| Server restart | Emitted as an explicit `event server` carrying the new incarnation. |
| No sidecar on host | `no-sidecar`, naming the fix ("install sidecar on the host, or pass `--binary`"). |
| Unreachable host | `unreachable`, naming the fix. |
| Protocol mismatch | Directional message: "too old on `<host>`" vs "too old here", chosen by which side is behind. Unit-tested; not reachable against a real host while only one protocol version exists. |
| No tmux on host | Distinct `no-tmux` state — reproduced accidentally and usefully, see finding 1. |
| Two viewers on one host | Both connected concurrently, both received identical snapshots (5 items, 3600 B each), no interference, `shells.json` unchanged. |
| Viewer + human TUI on the host | A full Sidecar TUI ran on the host against the same state tree while serve observed. Both agreed; neither disturbed the other; the TUI's reap guards held. |

## Findings that change Phase A

### 1. A non-login ssh shell cannot find the host's binaries — and the fix is `-l -c`, never `-l -s`

`ssh host sidecar` runs a non-login, non-interactive shell whose PATH has no `/opt/homebrew/bin`. On a stock macOS host that means serve starts but then reports `tmux: executable file not found in $PATH` — a machine with tmux plainly installed presents as a host with no tmux. The same trap catches anything tmux itself launches: a Sidecar TUI started with `tmux new-session '<cmd>'` also gets the reduced PATH.

The fix is a login shell, but *which form* matters and the difference is not obvious:

- `$SHELL -l -c CMD` — runs the profile, stdout clean.
- `$SHELL -l -s` (command on stdin) — runs the profile **and the interactive preexec hooks**, which on this host write `OSC 697` sequences to **stdout**, the same pipe the JSONL protocol travels on.

Measured directly on the host: form A produced `MARKER\n`; form B produced `\033]697;OSCLock=\a\033]697;PreExec\aMARKER`. `internal/hosts.RemoteShell` uses form A, and `TestRemoteShellUsesLoginDashC` pins it.

Phase A consequences: the login-shell probe is mandatory, not an optimisation; the host registry needs its `RemoteBinary` escape hatch; and the "stream is not the protocol" row state must exist, because some host will have a profile that prints to stdout no matter what form is used. That state is implemented and names the fix.

### 2. tmux server incarnation detects a *restart*, not a *death*

`tmux kill-server` does not unlink its socket — verified by inode, identical before and after. So `tmuxserver.Socket()` keeps reporting the same identity across a death, and the incarnation only changes when a new server recreates the socket with a new inode. In the recorded run the server event fired on the restart, not on the death.

This is not a bug, but it constrains Phase A's health vocabulary: **"the remote tmux server died" cannot be detected from the incarnation.** It is detected by the pane listing going empty — which is precisely the condition the reaper must refuse to act on (td-8d18de). Phase A should surface a distinct "no tmux server" row state driven by the empty listing, and must not treat a stable incarnation as evidence the server is alive.

### 3. CLI subcommands bypass `CheckStateIsolation` entirely

`main()` calls `config.CheckStateIsolation()` before touching the filesystem, but `cli.Run` dispatches *before* main reaches that check. Every subcommand has therefore been exempt from `SIDECAR_ISOLATED_STATE` since dispatch was introduced. That matters most for serve, which is the subcommand that gets spawned on someone else's machine and reads their state tree.

`runHostServe` now calls it explicitly, so the plan's isolation guarantee holds for this command. **The wider gap is not fixed** — `notify`, `create`, `shell`, `layout` and `open` still skip it. That is a separate change with its own blast radius and belongs in its own task, not in a spike.

### Two smaller ones

**Duplicate project directories shadow each other.** `projectdir.Lookup` returns the first directory whose `meta.json` matches, and a project registered under both `/tmp/x` and `/private/tmp/x` produces two directories — one of which has no manifest. The symptom is a Sessions browser with no shells in it and no error anywhere. This cost real time in the spike. Not a remote-hosts problem, but worth a task.

**The remote pane fixture needed a purpose-built binary.** Provider detectors gate on `pane_current_command`, so a replay pane must *identify* as the provider, not just look like it. Copying `/bin/sh` to the target name fails on Apple Silicon (the copy loses its signature and will not exec); ad-hoc re-signing makes it exec but the pane still reports `bash`; symlinking resolves to the real name. `scripts/spike-holdpane` is the working answer and is retained.

## What was kept

Product code (real Phase A seams, not spike scaffolding):

- `internal/hostproto` — the versioned protocol, both directions, with a decoder that names shell contamination.
- `internal/hostserve` — the headless collector loop. Read-only by construction: the package imports no writer.
- `internal/hosts` — the SSH/ControlMaster transport, shared with the Herdr plan.
- `internal/tty/control_remote.go` — the two seams: the spawner-driven factory and the in-band input encoder. The local path is untouched and pinned by test.
- `internal/buildinfo` — the library-visible version accessor the plan listed as a work item.
- `sidecar host serve --stdio` and `sidecar host probe` — the host half and the smallest viewer.

Harnesses:

- `scripts/remote-spike.sh` — isolation-enforcing deploy, fixture, serve, probe, teardown. Run `paths` first; it prints every root and the host's untouched real state.
- `scripts/spike-holdpane` — makes a pane identify as a provider.
- `scripts/spike-latency-proxy` — the deterministic WAN column.
- `internal/tty/remote_control_spike_test.go` — the measurement rig, opt-in.

## Recommendation

**Proceed to Phase A.** The existential question is answered: a proxied-control-mode pane is local-grade on a real link, and remains usable at 150 ms RTT. The seam analysis in the plan was accurate — swapping the spawned command carried the entire downstream stack unchanged, and the headless awareness stack needed orchestration, not reimplementation.

Two plan amendments follow from the evidence:

1. **Drop "resize-without-transport-restart" from the Phase A work items.** Measured at 82–854 ms for a debounced, lease-gated act. Revisit on Phase B experience, not on principle.
2. **Add the login-shell probe and the "stream is not the protocol" row state as required Phase A items**, and re-word the tmux health vocabulary around finding 2. These are not polish; without them a correctly configured host presents as broken.

For the bake-off against the Herdr plan: the numbers above are the input. The claim this spike set out to test — that reusing tmux's own control protocol solves the hard, latency-sensitive part for free — held. The remaining Herdr advantage is Herdr-native users' workflows, not capability.

**Not proven, and worth saying plainly:** done-lane TTL decay over the wire, a Linux host (both machines are arm64 macOS, so `process_identity`'s Linux stub was never exercised and every hello reported `processIdentity: true`), a genuinely relayed WAN path, and a full day of use. The Linux gap is the most significant: remote hosts will often be Linux, and Phase A's `/proc`-based process identity is untested by this spike.
