# Visible terminal performance proof

This document records the reproducible isolated proof for terminal presentation performance and its decision boundary. The fixture is deterministic, OpenCode-shaped, and privacy-safe; it never reads a real agent pane. Synthetic results validate the harness, visibility gating, and relative candidate behavior, but they cannot substantiate a foreground improvement claim for Marcus's real OpenCode journey or decide the real-journey CPU budget.

## Protocol

Build only a clean reviewed commit with the same flags and dependency mode used by the managed development install. Record the full commit, embedded version, binary SHA-256, Go version, `GOWORK` path or pinned mode, and exact workspace dependency revisions. Give each attempt a fresh proof root and run `./scripts/tmux-drive.sh paths` before fixture creation and again with the fixture repository; the tmux socket, state, cache, config, and manifest paths must all resolve beneath that root.

Create the fixture with `./scripts/terminal-performance-fixture.sh FIXTURE_ROOT DRIVE_ROOT`. It emits in-place ANSI updates every 8 ms and retains the authored OpenCode workspace, sidebar, positive file candidate, safe documentation URL, negative candidate, and live progress at steady state and after resize. The helper uses only tmux-drive's explicit private socket and state/config tree.

For CPU profiles, leave `SIDECAR_TERMINAL_PERF`, `SIDECAR_TMUX_SCREEN_COMPARE`, terminal tracing, overview tracing, and startup tracing unset. Start one 200x50 Sidecar process with only `SIDECAR_PPROF` enabled. Run exactly three 20-second same-process pairs in V-H, H-V, V-H order. Immediately before every phase, save a text and PNG snapshot: visible must have global Sessions selected plus the representative live OpenCode grid; hidden must have global Activity selected and no terminal grid. Any failed assertion invalidates the entire run. Extract sampled CPU seconds and elapsed duration from each profile, compute `sampled / elapsed * 100`, report visible minus hidden for each pair, and take separate medians.

Run counters in a separate diagnostic process because the endpoint and screen comparison can perturb CPU evidence. Snapshot `GET /debug/terminalperf` before and after settled 20-second visible and hidden phases. The endpoint is opt-in, localhost-only alongside pprof, read-only, and exposes only fixed numeric fields.

Always stop through the attempt's explicit `SIDECAR_DRIVE_RUN_DIR`. Verify the explicit socket reports no server and no Sidecar or fixture process remains beneath the proof root. Never inspect, stop, restart, or replace the default tmux server.

## Clean synthetic CPU evidence: 2026-08-24

The accepted run used:

- Sidecar commit: `37d9e37c4ac353e53544cc3c988ff2c408218189` on `opencode-perf`, with a clean worktree.
- Embedded version: `devel+opencode-perf.37d9e37c`.
- Build: `GOWORK=/Users/marcus/code/sidecar-opencode-perf/go.work go build -ldflags "-s -w -X main.Version=devel+opencode-perf.37d9e37c" -o PROOF_ROOT/bin/sidecar ./cmd/sidecar`.
- Binary SHA-256: `5e27b42374afeac7e65b722810b5ed7b18758ddc2ea23b043cfbd90feb5d5a06`.
- Toolchain: `go version go1.27.0 darwin/arm64`.
- Workspace dependencies: `tasks` at `7a9514d2760d999717e2058d2e282cce91f99ca6` and `td` at `51cf46258e7422693d663a5b49793794b3f3a437`, both clean.
- Configuration: deterministic 8 ms fixture, 200x50 Sidecar, pprof on localhost port 17657, diagnostic counters/screen comparison/traces off.
- Isolated proof root: `/private/tmp/sidecar-terminal-cpu-37d9e37c-valid.Ok3Q0F`; all reported tmux/state/cache/config paths were beneath it.

Every pre-phase snapshot passed. Each visible snapshot contained Sessions, `Working (1)`, the OpenCode fixture row, authored workspace/sidebar, `internal/runtime/frame.go`, the safe docs URL, and live progress. Each hidden snapshot selected Activity and contained no terminal grid.

| Pair | Order | Visible sampled / elapsed | Visible CPU | Hidden sampled / elapsed | Hidden CPU | Net visible overhead |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| 1 | V-H | 3.68 s / 20.10 s | 18.31% | 0.13 s / 20.07 s | 0.65% | 17.66 pp |
| 2 | H-V | 3.68 s / 20.09 s | 18.31% | 0.15 s / 20.07 s | 0.75% | 17.57 pp |
| 3 | V-H | 3.67 s / 20.12 s | 18.24% | 0.14 s / 20.08 s | 0.70% | 17.54 pp |
| Median | — | — | 18.31% | — | 0.70% | 17.57 pp |

The explicit private socket reported no server after the run, and no process remained beneath the proof root. A prior readiness-only attempt under a different proof root was stopped before any profile because its assertion required the underlined file path and `:42` to remain byte-contiguous across ANSI decoration. The screen was valid, but the whole process was discarded and is not included above.

## Counter visibility evidence

A separate 20-second counter pass validated the visibility boundary. Visible deltas were 1,251 model frames built and published, 1,405 terminal views rendered, 96,405 row-cache hits, 30,045 row-cache misses, 1,405 canvas inferences, zero content-link resolution requests/cache hits, zero synchronous resolver calls, 126 global workspace list rebuilds, and 1,405 global workspace preview renders. The following settled hidden phase changed every counter by exactly zero. Returning to Sessions again showed the working fixture row and live progress.

This counter pass preceded the fixture's steady-state review fix, so it is evidence only for the visible/hidden subscription gate, not for representative foreground workload cost. The list rebuilds came from the independently changing working-row pulse; focused list-cache tests remain the deterministic proof that terminal-only mutations do not rebuild the workspace list.

## Real current OpenCode journey: 2026-08-24

Marcus explicitly authorized a read-only profile of the current real OpenCode journey after the synthetic proof. The accepted run targeted `sidecar-sh-intersections-1`, pane `%0`, at 211x59. This is the same tmux session and pane identity used by the original baseline, but its live content had advanced since that capture, so the result is a current-journey gate rather than an exact replay of the original bytes. A newer selectable session, `sidecar-sh-intersections-2`, was rejected as a substitute because it was idle and was not the baseline pane.

The installed candidate metadata was:

- Sidecar commit: `ddd8b09c054768f0865f85c9b5f63dc8d7cb6e28`, clean `opencode-perf` checkout.
- Embedded version: `devel+opencode-perf.ddd8b09c`.
- Installed artifact: `/Users/marcus/.local/state/sidecar/dev-installs/opencode-perf-4d07caebc5bd-ddd8b09c-20260824T215508Z-919/sidecar`, built at `2026-08-24T21:55:09Z` with managed-install flags `-s -w -X main.Version=devel+opencode-perf.ddd8b09c`.
- Binary SHA-256: `06369851d5136548b5907cf5ac4ba39af5b418a392070b9c5eef41b8e101e81c`.
- Dependency mode: pinned (`GOWORK=off`), `github.com/marcus/td v0.63.0`, `github.com/marcus/tasks v1.13.0`.
- Toolchain: `go version go1.27.0 darwin/arm64`.
- Configuration: one 211x59 Sidecar process, pprof on localhost port 17661, counters/screen comparison/traces off.

The baseline session's legacy real manifest lacked `agentType`, so a private point-in-time overlay was required to expose it in global Sessions without modifying personal state. The proof copy-on-write cloned the current Sidecar XDG state and copied `config.json` plus `state.json` beneath `/private/tmp/sidecar-real-terminal-overlay-ddd8b09c.Zqq3ul`, then added `agentType: opencode` only to the copied `sidecar-sh-intersections-1` entry. The candidate ran with `SIDECAR_ISOLATED_STATE=1`, private `XDG_STATE_HOME`, private `XDG_CACHE_HOME`, and `-config` pointing at the copied config. Diagnostics confirmed every Sidecar state/config path was private. `TMUX_TMPDIR` was explicitly unset so the candidate could read the real default tmux server; no command was sent to either real OpenCode pane.

The private host used a keeper session for pane `%0` before creating the candidate at `%1`. Without that step, the inventory's host-pane exclusion would filter the real target's numerically equal `%0` even though the panes live on different servers. Selection scanned only column 10 in the bounded left-list body and accepted a row only when the candidate's direct control child was exactly `tmux -C ... -t sidecar-sh-intersections-1` and no session-2 child existed.

Every final pre-phase assertion passed. Visible phases selected Sessions, attached only session 1, and observed a changing direct pane hash before profiling. Hidden phases selected Activity and had no session-1 control child or terminal grid. The hidden UI and catalog came from the private point-in-time Sidecar state copy, while tmux activity and the profiled terminal bytes came from the live real pane.

| Pair | Order | Visible sampled / elapsed | Visible CPU | Hidden sampled / elapsed | Hidden CPU | Net visible overhead |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| 1 | V-H | 5.99 s / 20.10 s | 29.81% | 1.90 s / 20.01 s | 9.50% | 20.31 pp |
| 2 | H-V | 6.15 s / 20.09 s | 30.61% | 1.52 s / 20.09 s | 7.56% | 23.05 pp |
| 3 | V-H | 5.56 s / 20.18 s | 27.55% | 1.87 s / 20.09 s | 9.31% | 18.24 pp |
| Median | — | — | 29.81% | — | 9.31% | 20.31 pp |

Marcus reported that the candidate felt dramatically better in use, especially while scrolling. That is useful qualitative journey evidence and is consistent with the earlier rendering work, but it is not a substitute for the CPU gates and does not turn this measured miss into a passing result.

An earlier same-candidate attempt produced three individually valid phases before the selector failed on the second visible transition: pair-1 visible was 5.93 s / 20.18 s (29.38%), pair-1 hidden was 1.84 s / 20.08 s (9.16%), and pair-2 hidden was 1.95 s / 20.08 s (9.71%). Those profiles are retained and explicitly excluded from the table and medians because the attempt did not complete all three pairs in one process.

The private host and candidate were stopped and port 17661 closed. The real `shells.json` SHA-256 and mtime/size/mode tuple remained exactly unchanged. The default tmux server remained PID 94101 with 19 sessions; both `sidecar-sh-intersections-1` (pane `%0`, PID 94108) and `sidecar-sh-intersections-2` (pane `%20`, PID 26370) remained alive under OpenCode. The copied 17 GB state, config, cache, and all captured text/PNG were deleted immediately after extracting numeric and assertion-safe evidence. Raw pprof and assertion logs remain under the proof root for independent review; they contain no captured terminal text.

## Cadence decision

The real current journey misses both acceptance budgets: median visible CPU is 29.81%, above 25%, and median net visible overhead is 20.31 percentage points, above 12.10. The hidden floor also rose materially from the original baseline, but the net budget remains failed even after subtracting that drift. The existing 12 ms coalescer therefore cannot be accepted as the final policy, and this task remains open.

The next candidate must implement the plan's adaptive actor-publication policy: immediate leading output after idle, sustained publication at no more than 30 fps, and a guaranteed newest trailing frame while model bytes and input remain immediate. It may ship only after a rerun meets both real CPU budgets and proves output-to-frame p95 at or below 50 ms, prompt idle-leading output, the 30 fps sustained cap, and no stranded trailing state.
