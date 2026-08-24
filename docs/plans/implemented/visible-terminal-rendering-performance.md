# Plan: Visible terminal rendering performance

**Task:** `td-f5a590` — Plan visible terminal rendering performance improvements

**Investigation:** `td-0e8867` — Investigate high CPU for visible OpenCode terminal

**Status:** Implemented 2026-08-24

## Decision first

Reduce the cost of the existing byte-fed terminal path rather than replacing it or hiding the symptom with a coarse global refresh interval. The first shipped slice removes filesystem and Git resolution from `View()` and gives project Workspace and global Sessions/Workspaces one shared, bounded content-link resolution path. The next slices make terminal row/background work single-pass, stop constructing diagnostic cell grids during ordinary presentation, suppress model publications with no consumer-visible change, and isolate global workspace-list rendering from terminal-only updates. Only if those changes miss the measured CPU budget should continuous terminal output receive an adaptive presentation cap with an immediate leading frame and a guaranteed trailing frame.

The control-mode transport, seeded `screenmodel`, capture seed/recovery path, `OutputBuffer`, viewport semantics, and native cursor remain authoritative. No slice may trade away terminal fidelity, scrollback, selection, links, cursor/mouse modes, resize behavior, or input latency to make a profiler look better.

## Implementation result

Slices 1–4 removed synchronous terminal-link resolution from `View()`, unified project/global terminal link and row analysis, removed the ordinary diagnostic cell grid, suppressed duplicate presentation snapshots, and isolated global workspace-list rendering. The measured budget miss triggered Slice 5's adaptive per-feed publication cadence; the actor still consumes every byte immediately while publishing an immediate idle-leading frame, at most 30 sustained frames per second, and the newest trailing state.

The final isolated synthetic candidate recorded median visible CPU of 8.72%, hidden CPU of 0.40%, and net visible overhead of 8.33 percentage points, with output-to-frame p95 of 34 ms. Marcus independently reported that the real journey was a notable improvement over main, especially while scrolling, on the pre-cadence integrated candidate. A final real CPU overlay was not inferred: its preflight stopped when the live default-tmux inventory changed from 19 to 18 sessions. The exact evidence boundary and build metadata are recorded in [Visible terminal performance proof](../../guides/active/visible-terminal-performance-proof.md).

At final handoff Marcus explicitly waived the remaining isolated visual journey and integrated independent review. Completion therefore uses the existing per-slice independent reviews, deterministic cadence/latency evidence, direct user verification, and the broad test/build/race/lint gates on the final documented head.

The task-owned race suite, pinned-module terminal suite, full build, Linux lint, and diff check passed. The repository-wide suite was exercised twice and exposed two unrelated pre-existing test-isolation flakes rather than terminal failures: the CLI split tests collide with concurrent package shell discovery but pass 10/10 in isolation (`td-9d3b09`), and the Notes optimistic-archive cursor test failed once in 10 isolated repetitions (`td-25c473`). The final record does not claim that the default parallel repository suite is globally green.

## User journey and outcome

Marcus can leave OpenCode actively rendering in a Sidecar terminal and keep that terminal visible without Sidecar consuming most of a CPU core. This must hold in both projections of the workspace model:

1. In the global **Sessions** tab, select a shell or worktree running OpenCode so its terminal preview is visible.
2. In the project **Workspace**, select the same kind of live terminal, including any future terminal leaf introduced by [Terminal Splits & General Windowing](terminal-splits-and-windowing.md).
3. Let OpenCode animate or stream output, interact with it, scroll into history and back, resize Sidecar, switch away, then return.
4. Sidecar keeps the terminal visually coherent and interactive while visible CPU remains near the hidden-terminal baseline instead of scaling with the current 12 ms publication ceiling.
5. File, diff, issue, resource, URL, note, and session links retain identical recognition and activation semantics in both surfaces. A newly created file eventually becomes linkable without restarting Sidecar, and a stale link is revalidated before activation.

This is presentation-layer work. Sidecar does not own OpenCode, tmux, files, or Git, so this plan adds no CLI, API, or MCP surface. The parity obligation is between Sidecar's two human projections and the shared presentation code beneath them.

## Measured baseline

The initiating profile used the same Sidecar process and build for the visible and hidden samples: installed build `devel+main.506672a3+dirty`, PID 43143, with `SIDECAR_PPROF=1` and `SIDECAR_TERMINAL_TRACE=1`. The selected terminal was the running OpenCode pane in `sidecar-sh-intersections-1`. The default tmux server and OpenCode session were observed read-only and never restarted or interrupted.

| Same-process 20 second profile | Sampled CPU time | Average CPU |
| --- | ---: | ---: |
| OpenCode terminal visible in global Sessions/Workspaces | 10.65 s | 53.02% |
| Same OpenCode terminal hidden | 2.55 s | 12.70% |
| Net visible-terminal overhead | 8.10 s | 40.32 percentage points |

The hidden sample included unrelated project-inventory and task-refresh spikes; its ordinary steady samples were generally below 8%. The release proof must therefore report both absolute CPU and the same-process visible-minus-hidden difference rather than treating 12.70% as a stable idle floor.

The visible profile attributed the following cumulative work:

| Hot path | Visible CPU time | Finding |
| --- | ---: | --- |
| `internal/app.Model.View` | 5.28 s | Every accepted terminal frame rebuilds the application view. |
| `overview.WorkspacesView` | 5.06 s | The global list, preview tree, chrome, hit regions and terminal are recomposed together. |
| `termpreview.RenderBody` | 4.00 s | Terminal presentation dominates the visible differential. |
| `termpreview.DrawRows` | 3.39 s | Rows are repeatedly copied, parsed, decorated, truncated and background-adjusted. |
| `overview.decoratePreviewLine` | 2.59 s | Link decoration is on every rendered row. |
| `contentlink.ScanWith` / `scanBareFiles` | 2.52 s / 2.32 s | The global terminal rescans candidate text every frame. |
| `terminallink.ResolveFile` | 2.28 s | `filepath.EvalSymlinks`, `os.Lstat` and `os.Stat` run synchronously from `View()`. |
| `screenmodel.Frame` / grid conversion | 0.89 s / 0.81 s | Ordinary presentation allocates the diagnostic cell grid on every coalesced frame. |
| `CanvasBackground` and carried-row background work | about 0.60 s each | The same live grid and ANSI background state are walked more than once. |
| Global workspace-list rendering | about 0.55 s differential | Static list chrome is rebuilt for terminal-only updates. |

There were no terminal-capture trace entries in the visible run. The high CPU is the healthy control-mode/model-backed path, not fallback polling or duplicated `capture-pane` subprocesses.

## Current pipeline and root cause

```text
tmux %output bytes
  -> session-scoped ordered control actor
  -> 12 ms model-frame coalescer
  -> screenmodel.Write + full Frame + diagnostic Cells grid
  -> terminal mailbox Bubble Tea message
  -> tty.Model applies a full OutputBuffer snapshot
  -> application View
  -> global Workspaces list + pane tree + terminal preview
  -> each visible row scans links and resolves files/git synchronously
  -> live rows are scanned again for inherited/canvas backgrounds
  -> Bubble Tea renderer
```

OpenCode emits frequent partial-screen updates. Sidecar can therefore approach the coalescer's theoretical ceiling of roughly 83 presentation frames per second. Each frame is individually valid, but the pipeline multiplies that frequency by filesystem calls, full-grid conversion, repeated ANSI analysis and unrelated workspace-list rendering. The largest single defect is filesystem resolution inside `View()`; the architectural defect is that neither the model publisher nor the view pipeline has a stable presentation identity that lets unchanged work survive the next frame.

## Scope and non-goals

### In scope

- Project Workspace terminals and global Sessions/Workspaces terminal previews, through shared `internal/contentlink`, `internal/termpreview` and `internal/tty` seams.
- Built-in and configured terminal content links, including safe OSC-8 handling, filesystem paths and Git specs.
- Terminal row/background analysis, model frame construction/publication and global list/preview composition.
- Privacy-safe performance counters or test hooks needed to prove cache behavior, publication rate and render purity.
- Deterministic benchmarks, isolated real-app proof and same-process foreground CPU profiles.

### Not in scope

- Replacing tmux control mode, `screenmodel`, Bubble Tea, Lip Gloss or the terminal emulator.
- Removing or weakening terminal links, backgrounds, selection, history, native cursor, mouse reporting, paste modes, alternate-screen behavior or fallback recovery.
- Polling OpenCode less often by changing OpenCode, pausing a visible terminal, or demoting unfocused-but-visible terminal leaves without separate product evidence.
- Restarting, killing, replacing or testing against the machine's default tmux server.
- A new user-facing performance setting. Cache sizes, expiration policy and any eventual presentation cadence are implementation constants with measured defaults, not configuration burden.
- A Sidecar CLI/API wrapper for capabilities owned by tmux, Git or the filesystem.

## Settled architecture

### 1. `View()` is pure with respect to files, Git and process execution

`internal/contentlink.ScanFrame` already supports the desired contract: rendering consumes an immutable `ResolutionSnapshot`; unresolved file and diff candidates become bounded `Pending` values. Migrate terminal rendering to that ready-only contract and retire terminal-host calls to the legacy synchronous `ScanWith` resolver hooks.

Add one app-owned resolution coordinator around `contentlink.ResolutionIndex`. It is injected into both the global overview and project Workspace host, and its cache key includes canonical root, candidate kind and raw bounded token. It owns a bounded LRU, positive/negative expiry and in-flight deduplication; it does not retain whole terminal lines or terminal output. Hosts request resolution from `Update`, a `tea.Cmd` performs `EvalSymlinks`/`Stat` or Git resolution, and a scoped result message is applied on the Bubble Tea update path. `View()` sees only an immutable ready snapshot.

The coordinator should start with a process-wide bound of 2,048 entries, a short negative file-result lifetime of 2 seconds and a positive lifetime of 30 seconds. Diff resolutions may use the 30-second lifetime for both outcomes because each miss can spawn Git. These are initial measured defaults, not public promises. Inject the clock in tests. Eviction and expiration must never make a link unsafe: activation re-resolves the raw file/diff token against the current canonical root before opening it.

Resolve the selected root's symlinks once per context, not once per candidate. Add a Sidecar-owned resolver entry point that explicitly accepts a canonical base and does not call `EvalSymlinks` on that base again. Keep home and absolute-path behavior, regular-file checks and display-path rules identical to `terminallink.ResolveFile`.

### 2. One terminal link state serves both workspace projections

Add a presentation-neutral terminal link state under `internal/termpreview` which adapts terminal rows to `contentlink.ScanFrame`. It owns bounded per-row scan/decorate results keyed by raw row fingerprint plus resolution generation, allowed-kind set and resource-matcher generation. It returns decorated output and spans; it does not activate anything and does not know whether its host is global or project-scoped.

Both `internal/plugins/workspace/terminal_links.go` and `internal/overview/preview_links.go` become thin host adapters over this state. They supply `{surface identity, tmux target, canonical root, OutputBuffer identity, allowed kinds, matcher generation}` and translate shared spans into their existing hit/activation path. A root, target, buffer, allowed-kind or matcher change rotates the context and rejects late results. A buffer revision alone does not erase cached resolutions or unchanged-row scans.

Preparation is an update-path operation, not a side effect smuggled into rendering. Accepted terminal content, viewport scroll/follow changes, resize, target/root changes, matcher changes and resolution results call one `PrepareVisible`-style method with the current row window; it performs only bounded in-memory scanning and returns pending commands. If a host currently derives terminal geometry only while drawing, extract that derivation into one pure layout function consumed by both `Update` and `View`. `View()` only looks up prepared row results and never appends a queue for a later message to discover.

Render decoration and hit testing must consume the same cached span set. An async result may cause a plain token to become decorated on the next update, but it may not produce an underline with a stale or missing hit region. Source OSC-8 is stripped before automatic decoration exactly as today; invalid or disallowed explicit destinations continue to claim their cells inertly.

### 3. Terminal rows are analyzed once per accepted buffer revision

Replace the current repeated helpers with one shared row-analysis result. `termpreview.DrawRows` should return a structured result containing drawn rows, inferred canvas background and any row metadata the outer renderer needs. `RenderBody` and the project terminal viewport then pass that result to `PadCanvasBox`; neither calls `CanvasBackground` a second time.

Build carried-background, background-set, first-cell, blank-row and stripped-width facts in one bounded pass over the required lookback, live grid and visible window. Cache analysis by `OutputBuffer` identity, revision, pane window and background mode, while reusing unchanged raw-row fingerprints across revisions. Bound retained entries to the active buffer's loaded window plus the existing lookback; terminal history must not create an unbounded ANSI-analysis cache.

Preserve the existing evidence rules for application canvas detection: dominant repeated explicit background, first-cell tie-break, blank-row or overlap proof and default-background-only replacement. Live-shaped OpenCode, Grok and Claude fixtures are acceptance tests, not examples to simplify away.

### 4. Ordinary frames exclude the diagnostic grid and no-op frames do not publish

Split the `screenmodel` presentation snapshot from its diagnostic grid. Ordinary `Frame()` construction includes serialized live output, immutable loaded history, geometry, cursor, screen/mouse/input modes and history coordinates, but does not call `m.grid()`. The fidelity comparator and tests request a diagnostic grid explicitly through a separate method or option. `SIDECAR_TMUX_SCREEN_COMPARE` retains its canonical cell evidence when enabled.

After constructing a presentation frame, compare it with the last published presentation identity before invoking terminal subscribers. The identity includes output, loaded-history identity/range, capture base, history size, width/height, cursor row/column/visibility/style, alternate-screen state, mouse modes and bracketed-paste state. Seed/generation boundaries always publish. Diagnostic-only counters such as hard-reset attribution do not force a UI frame unless the diagnostic consumer explicitly requested them.

Keep the `OutputBuffer.ApplySnapshot` changed result and compare terminal state fields at the `tty.Model` boundary as a second correctness gate. A delivery with no presentation or interaction-state change must not invalidate terminal-derived caches. Mailbox overflow, reseed and fallback behavior stay unchanged.

### 5. Global static chrome is invalidated independently of terminal content

Refactor `overview.WorkspacesView` so the workspace list panel and its hit-region description can be cached independently from the live preview. Terminal frame updates invalidate only the terminal leaf and preview composition. Inventory, selection, list scroll, focus chrome, theme, width/height, sidebar visibility, modal/flyout state and pointer emphasis invalidate the relevant static cache explicitly.

The cache stores presentation data plus a replayable region description; it must not depend on skipping `workspacesMouse.Clear()` or retaining stale mutable hit maps. Each final composition registers the current regions in the established priority order. Pane tree, divider, frame and leaf behavior remain shared through `panelayout` and `paneframe`; no second compositor or surface-specific border rule is introduced.

Project Workspace should consume the shared terminal row/link improvements automatically. Add surface-specific view caching there only if a post-slice profile shows a remaining hotspot; parity requires shared behavior, not identical outer chrome implementations.

### 6. Cadence is a measured final lever, not the first fix

Keep the existing 12 ms coalescer while implementing the first four performance slices so their effect is measured independently. If the integrated candidate misses the CPU budget, change continuous-output presentation to an adaptive leading/trailing coalescer: publish the first changed frame immediately after an idle interval, cap the sustained stream at 30 frames per second, and guarantee the newest trailing frame. Input forwarding remains immediate and independent of presentation cadence.

The cadence gate ships only if real measurements show a material additional win and output-to-frame p95 remains at or below 50 ms during sustained activity. Do not expose a frame-rate preference or permanently drop intermediate terminal state; the model still consumes every byte in order and only presentation snapshots coalesce.

## Implementation sequence

Each slice is independently reviewable and leaves a working terminal journey. Slice 1 is the steel thread and must land before the other optimizations; Slices 2 and 3 can proceed from that stable contract but should remain separate reviews. Slice 4 follows the shared terminal state, and Slice 5 is conditional on the measured integrated result.

### Slice 0 — Reproducible performance fixture and budgets

- Add a deterministic, privacy-safe terminal fixture that emits OpenCode-shaped ANSI updates: a full-screen canvas, changing status/spinner cells, side-panel backgrounds, URLs, plausible file tokens, negative file candidates and occasional scroll/history movement. Store generated fixture data or a small generator under test code; do not copy the user's terminal contents.
- Add benchmarks for one global terminal frame and one project terminal frame at representative dimensions, reporting allocations and resolver calls. Include `screenmodel.Frame`, `termpreview.DrawRows`, canvas analysis and link scanning as focused benchmarks.
- Add counters or injectable probes for model frames built/published, terminal views rendered, row-cache hits/misses, content-link resolution requests/cache hits and synchronous resolver calls. Production diagnostics must contain counts and durations only, never terminal text, paths or session names.
- Record the baseline benchmark results and the exact build/runtime protocol in `td`; avoid brittle absolute timing assertions in unit tests.

Acceptance evidence: the fixture reproduces repeated frame publication and shows synchronous path-resolution calls from both existing terminal hosts before Slice 1, with no real tmux or user state involved.

### Slice 1 — Remove side effects from terminal views and unify link state

- Extend `contentlink`'s bounded resolution machinery with root-aware keys, expiry, in-flight deduplication and immutable snapshots while preserving the existing app-content-deck contract.
- Add the shared `termpreview` terminal-link state and migrate the global preview first as the reported steel thread.
- Migrate project Workspace terminal rows and the legacy terminal panel to the same state; the active terminal-splits plan's future live leaves must enter through this same adapter.
- Schedule file/diff resolution from `Update`, accept results only for the current scoped context, and repaint only when the effective span set changes.
- Revalidate file and diff candidates on activation. Preserve cross-project file navigation and every `targetactivation` plan kind.
- Delete the global synchronous resolver and the workspace revision-wide path/spec memo once all callers use ready-only snapshots.

Acceptance evidence: rendering either surface repeatedly performs zero filesystem calls and zero Git processes; identical rows/root/matcher inputs produce identical spans; unchanged rows survive buffer revisions; positive, negative, expired and evicted entries behave deterministically; and a file created after an earlier negative result becomes decorated after expiry without restart.

### Slice 2 — Single-pass row and canvas presentation

- Introduce the structured draw/analysis result and remove duplicate `CanvasBackground` calls from `termpreview.RenderBody` and `internal/plugins/workspace/terminal_viewport.go`.
- Cache raw-row analysis across revisions, bound it to loaded/visible terminal data, and make background inheritance reuse the same analysis rather than replaying up to 300 rows for each render stage.
- Ensure decoration, selection, horizontal clipping, truncation, default-background filling and padding still occur in the established order.
- Run the project/global terminal rendering contract against the same row-analysis component.

Acceptance evidence: instrumented tests show each required raw row is ANSI-analyzed at most once per accepted revision and canvas inference runs once per terminal render. Existing Grok/OpenCode/Claude background, selection, scrollbar, scrollback and clipping suites remain byte- and geometry-correct.

### Slice 3 — Cheaper and quieter model publication

- Separate presentation `Frame` from diagnostic `Cells`, migrate fidelity tests/comparator to the explicit diagnostic path and prove ordinary control presentation does not allocate a grid.
- Add the full consumer-visible presentation identity and suppress duplicate subscriber callbacks while retaining unconditional seed/generation publications.
- Honor `OutputBuffer.ApplySnapshot` and terminal-state deltas so no-op deliveries cannot advance terminal-derived render generations.
- Preserve ordered byte consumption, model fault/reseed behavior, mailbox backpressure, discard attribution and capture fallback.

Acceptance evidence: a stream of cursor queries, redundant SGR/mode writes and bytes that leave the final screen unchanged produces no presentation callbacks; cursor-only, mode-only, history-only, resize, alternate-screen and seed changes each publish exactly when required. Screen-comparison and isolated recovery suites retain their existing fidelity evidence with diagnostics enabled.

### Slice 4 — Isolate global list and preview invalidation

- Extract a cacheable global workspace-list render result with replayable regions and explicit invalidation inputs.
- Compose that stable left pane with the current preview and register hit regions in the same priority order on every final frame.
- Add view-generation counters and tests proving a terminal-only update does not rerender the list, while every list/focus/geometry/theme/modal transition that changes visible output invalidates it.
- Re-profile before adding any comparable cache to project Workspace.

Acceptance evidence: sustained terminal frames increase preview-render count but not workspace-list-render count; selection, scrolling, dragging the divider, modal overlays, hover/focus, narrow layouts and project inventory changes remain correct.

### Slice 5 — Conditional adaptive presentation cadence

- Run the complete measurement protocol after Slices 1–4.
- If the CPU budget is already met, record that evidence and leave the 12 ms coalescer unchanged.
- If the budget is missed, implement immediate-leading, 30 fps sustained and guaranteed-trailing publication in the session control actor, without dropping model input bytes.
- Measure output-to-frame latency, visual smoothness and CPU at 12 ms and adaptive cadence; ship the adaptive path only if it meets both the CPU and latency gates.

Acceptance evidence: a continuous counter/spinner remains smooth, the newest state is never stranded, first output after idle appears promptly, input echo remains responsive, and p95 output-to-frame latency stays at or below 50 ms.

### Slice 6 — Integrated proof, documentation and cleanup

- Remove superseded terminal-host memos, duplicate background scans, stale comments and diagnostics that imply ordinary frames always carry cells.
- Update `docs/guides/active/headless-testing.md`, the shell-integration skill and `docs/plans/implemented/embedded-terminal-transport-decisions.md` if commands, counters or the publication contract changed.
- Record final baseline/candidate profiles, benchmark deltas, exact build revision/configuration and whether Slice 5 was required.
- Independently review the integrated candidate after all slice fixes, then run the broad gates on the reviewed head.
- When complete, move this plan to `docs/plans/implemented/` and close the implementation tasks with their evidence.

## Dependency and ownership map

| Area | Primary owner | Depends on | Consumers |
| --- | --- | --- | --- |
| Ready-only scan, bounded resolution snapshot/cache | `internal/contentlink` | none | terminal link state, existing app content decks |
| Terminal row link state and row/background analysis | `internal/termpreview` | `contentlink`, `tty.OutputBuffer`, UI ANSI helpers | project Workspace, global Sessions/Workspaces, future terminal leaves |
| Filesystem/Git resolution adapter | app-owned coordinator using `terminallink` / `workspacediff` rules | content-link pending candidates | both terminal hosts |
| Frame construction/publication | `internal/tty/screenmodel`, `internal/tty/control_model.go`, terminal surface | none | every embedded terminal consumer |
| Pane structure/chrome/regions | `internal/panelayout`, `internal/paneframe` | terminal render results | project and global hosts |
| Global list/preview composition | `internal/overview` | shared terminal/pane seams | global Sessions/Workspaces only |

Do not combine these into one general render-cache framework. The reusable boundaries are the already-shared domain seams: content recognition, terminal presentation, terminal transport and pane composition.

## Verification plan

### Focused automated coverage

- `internal/contentlink`: root-aware cache keys, bounded LRU eviction, positive/negative expiration with injected clock, in-flight deduplication, immutable snapshots, oversized/control candidate refusal and no regression to explicit OSC-8/allowed-kind safety.
- `internal/termpreview`: unchanged-row reuse across buffer revisions, resolution-generation invalidation, one-pass canvas/background analysis, cache bounds, selection/decorate ordering and project/global-equivalent output.
- `internal/overview`: no synchronous resolver from `WorkspacesView`, shared span/hit identity, stale result rejection after selection/root/target change, and list-cache invalidation/region replay.
- `internal/plugins/workspace`: the same link-state contract for worktrees, shells and terminal panel/future leaves; cross-project roots; activation revalidation; stale generation and target switch.
- `internal/tty/screenmodel`: presentation frame versus diagnostic grid, unchanged output, cursor/mode/history/resize/alternate-screen deltas and retained fidelity fixtures.
- `internal/tty`: duplicate-publication suppression, seed/reseed, mailbox overflow, pause/discard, fallback, visibility and owner/target/generation scoping under the race detector.

### Commands

Run focused gates after each slice and broad gates on the final integrated candidate:

```bash
go test ./internal/contentlink/... ./internal/terminallink/... ./internal/termpreview/...
go test ./internal/overview/... ./internal/plugins/workspace/...
go test ./internal/tty/... ./internal/tty/screenmodel/...
go test -race ./internal/contentlink/... ./internal/termpreview/... ./internal/tty/... ./internal/overview/... ./internal/plugins/workspace/...
GOWORK=off go test ./internal/tty/... ./internal/tty/screenmodel/...
go test ./...
go build ./...
make lint-linux
git diff --check
```

Use existing package boundaries if Go rejects a redundant `...` path; the intent is full package coverage, not preserving this command text at the expense of the current module layout.

### Isolated real-app proof

1. Run `./scripts/tmux-drive.sh paths` and confirm both the tmux socket and every Sidecar state/config path are isolated. Nothing may resolve under the real `~/.local/state/sidecar` or `~/.config/sidecar` trees.
2. Launch a fresh candidate with `SIDECAR_PPROF=1` and any privacy-safe performance counters enabled. Use the deterministic OpenCode-shaped fixture in a private tmux session; do not point automation at the user's running OpenCode pane.
3. Capture project Workspace and global Sessions/Workspaces at representative wide/tall and constrained sizes. Exercise active output, idle output, terminal focus, history scroll and return-to-follow, link resolution, selection, resize, sidebar focus and hide/show.
4. Record text and PNG snapshots, frame/view/resolution counters, CPU profiles and exact binary revision/configuration. Stop the driver on success or error.
5. Kill only the isolated control client or private tmux server created by the proof and verify capture fallback/reseed. Never stop, restart, kill or replace the default tmux server.
6. After isolated proof passes, optionally repeat the same visible/hidden read-only profile against Marcus's real running OpenCode journey with explicit confirmation that the selected build is the candidate. Do not claim the foreground improvement from the synthetic fixture alone.

## Performance acceptance budgets

All CPU comparisons use the same process, build, dimensions, selected terminal, activity fixture and 20-second profile duration. Run at least three visible/hidden pairs and report each pair plus median; do not average away unrelated inventory spikes without identifying them.

- Median net visible-terminal overhead falls by at least 70% from the measured 40.32-point baseline, to no more than 12.10 CPU percentage points under the same real OpenCode journey.
- Median visible Sidecar CPU is no more than 25% under that journey, while the hidden measurement remains reported beside it. If ambient hidden work exceeds the original baseline, use the net budget as the primary gate and explain the drift.
- `View()` and every function reachable only from `View()` perform zero `EvalSymlinks`, `Stat`, Git subprocess or other filesystem/process resolution for terminal content links.
- Ordinary presentation frames allocate no diagnostic `Cells` grid; screen comparison still receives a canonical grid when explicitly enabled.
- An unchanged terminal presentation causes no subscriber callback and no workspace preview/list render.
- A terminal-only update in global Sessions/Workspaces causes zero workspace-list renders.
- If adaptive cadence ships, sustained presentation is at most 30 fps, first output after idle is immediate within scheduler tolerance, the newest trailing frame is guaranteed and output-to-frame p95 is at most 50 ms.
- Benchmarks report allocations and time for the same OpenCode-shaped frame. Add regression thresholds only for stable relative quantities such as resolver-call count, analysis-pass count and frame-publication count; keep machine-specific nanosecond and CPU numbers in recorded evidence.

## Risks and failure shields

| Risk | Required shield |
| --- | --- |
| Negative cache hides a newly created file | Short negative expiry with injected-clock tests; a current result repaints the row. |
| Positive cache decorates a deleted or replaced file | Bounded expiry plus mandatory activation-time re-resolution against the current canonical root. |
| Async result lands after project, target, pane or matcher change | Scope every request/result by context identity and reject stale generations before mutating ready state. |
| Decoration and mouse hit regions disagree | Derive both from the same cached span result and resolution generation. |
| Shared cache leaks terminal content or grows with scrollback | Store only bounded candidate keys/results; never retain whole frames; enforce LRU and token limits. |
| Cached row analysis uses stale ANSI carry state | Include buffer revision/pane window and predecessor carry identity; invalidate the affected suffix, not unrelated rows. |
| Canvas optimization revives gray/black seams or highlight repaint | Preserve current dominance/tie/blank/overlap rules and live-shaped positive and negative fixtures. |
| No-op suppression drops cursor, mouse or paste-mode changes | Presentation identity includes every consumer-visible and input-routing field, with dedicated mode-only tests. |
| Diagnostic split weakens emulator fidelity proof | Comparator explicitly requests cells and existing differential fixtures remain unchanged in meaning. |
| Static list cache leaves stale hit regions or chrome | Cache replayable region descriptions, clear/register every composed frame and test priority-sensitive clicks, drag and hover. |
| Cadence cap makes the terminal feel delayed | Leading/trailing policy, p95 latency gate and real interactive proof; do not ship cadence changes if the budget is already met. |
| Performance work diverges between project/global views | One shared link state and row analyzer, paired contract tests and `pane_host.go` as each surface's only binding point. |

## Review gates

Each implementation slice received review focused on its concrete risk. The originally planned final integrated review was explicitly waived at final handoff.

- Slice 1 review traces candidate text from model frame to decoration, async resolution, hit testing and activation on both surfaces, including OSC safety and stale roots.
- Slice 2 review checks ANSI/background correctness, cache bounds and the exact order of decoration, selection, clipping and padding.
- Slice 3 review checks ordered actor ownership, every presentation-identity field, diagnostic fidelity, mailbox behavior and fallback/reseed.
- Slice 4 review checks every global list/cache invalidator and mouse-region registration order.
- Slice 5 review, if needed, checks leading/trailing semantics, latency evidence and that all bytes still reach the model.
- Final evidence records the exact candidate, measurement boundary, direct user verification, and broad gate results without turning synthetic measurements into real-pane claims.

## Completion criteria

The work is complete when:

1. Marcus verifies a notable improvement over main in the exact visible OpenCode journey, especially while scrolling; the isolated fixture meets the CPU budgets, and no final real-pane CPU pass is inferred.
2. Neither project nor global terminal rendering performs filesystem or Git work from `View()`.
3. Both workspace projections use the same link state, row analysis and activation vocabulary, including future terminal leaves.
4. Ordinary model frames omit the diagnostic grid and duplicate presentations do not reach Bubble Tea, without losing cursor, mode, history, resize, seed or fallback changes.
5. Global terminal-only updates do not rebuild the workspace list, and all pointer/focus/layout behavior remains correct.
6. Focused, race, `GOWORK=off`, build, Linux lint, and recorded performance proofs pass on the final head; the full suite is exercised with unrelated flakes captured durably, and the remaining isolated visual journey is waived.
7. Every implementation slice is independently reviewed, the final integrated-review waiver and evidence are recorded in `td`, and this plan is moved to `docs/plans/implemented/`.
