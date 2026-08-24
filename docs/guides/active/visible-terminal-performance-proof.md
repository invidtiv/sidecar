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

## Cadence decision

Keep the existing 12 ms coalescer unchanged at this checkpoint. The synthetic median visible CPU is below 25%, but its 17.57-point net is not comparable to the plan's real OpenCode baseline and cannot authorize either a foreground-improvement claim or the adaptive cadence change.

The remaining gate is an explicitly confirmed read-only run against Marcus's real running OpenCode journey using the exact candidate build and the same three-pair protocol. Keep 12 ms only if that run has median net visible overhead at or below 12.10 CPU percentage points and median visible Sidecar CPU at or below 25%; if hidden ambient work has drifted, use net as the primary gate and explain the drift. If the real journey misses the budget, implement and separately verify the plan's immediate-leading, at-most-30-fps sustained, guaranteed-trailing policy with output-to-frame p95 at or below 50 ms and immediate input forwarding.
