# Plan: Active-session steady-state rendering performance

**Task:** `td-e32598` — Plan active-session steady-state rendering performance

**Status:** Active

**Related implemented plan:** [Visible terminal rendering performance](../implemented/visible-terminal-rendering-performance.md)

## Decision first

Make stable pane bodies survive unrelated application redraws. Restore the row analyzer as an invariant of every live terminal leaf, prepare and cache document content-link frames by explicit visual identity, then cache the project Workspace preview composition while replaying current hit regions. Preserve the 175 ms activity pulse, terminal publication cadence, document live refresh, links, selection, pane chrome, and input latency; the work removes repeated computation rather than making the interface update less often.

Do not reopen the terminal transport or globally lower Bubble Tea's frame rate. The current control actor already reduced 1,143 model-frame builds to 31 published frames during the measured 30-second window. The regression is downstream: those 31 publications coincided with 301 terminal renders because activity animation and other ordinary messages redraw the focused Workspace. A stable terminal and document pane should be cheap to project again.

## User journey and outcome

Marcus can keep an agent working in a visible terminal with a rendered Markdown document open beside it and retain the current smooth activity marker, terminal output, selection, live document refresh, and clickable links without Sidecar sitting around 27–35% CPU.

The controlling journey is the exact live layout profiled on 2026-08-31:

1. Open project Workspace on a managed shell with an active agent row.
2. Keep one terminal leaf visible beside one rendered Markdown document leaf.
3. Let the agent continue working while neither the document nor most terminal rows change.
4. Observe the activity pulse, terminal output, file refresh, content links, selection, scrolling, resizing, focus, hover, and pane controls.
5. Sidecar redraws only the presentation that changed. A sidebar pulse does not re-analyze terminal rows, rebuild the document body, rescan document links, or recompose the pane tree.

The same live-leaf invariant and document preparation state apply in project Workspace, global Sessions, and app content decks. Outer view caching may differ where the surfaces have different chrome, but it must wrap the shared `paneframe` compositor rather than create another pane renderer.

## Measured current baseline

The live process was PID 99044, installed from clean `main` revision `87fd7afe43cd2e9c62eb7797944dcb01da46b3da` as `devel+main.87fd7afe`. The process had been running for about 14 minutes, used 236 MB RSS, held 192 file descriptors, and had 59 goroutines. The profiled layout was one 104×58 terminal leaf beside one 103×58 rendered Markdown document leaf. The default tmux server, its sessions, and the Sidecar layout were observed read-only and were not restarted or mutated.

The 30-second CPU profile was captured with both `SIDECAR_PPROF=17657` and `SIDECAR_TERMINAL_PERF=1`, as launched by Marcus. It contains 8.17 seconds of CPU samples, or 27.11% average CPU. Performance-counter atomics therefore contribute to this diagnostic run; authoritative candidate CPU comparisons must run with the counters disabled and use a separate counter-enabled behavioral pass.

| Hot path | CPU in 30 seconds | Finding |
| --- | ---: | --- |
| `app.Model.View` | 4.84 s | Rendering, not update work, owns most sampled Sidecar CPU. |
| `workspace.Plugin.View` | 3.13 s | The focused Workspace is fully projected for ordinary messages. |
| `paneframe.Compose` | 2.56 s | Stable terminal/document leaves and chrome are recomposed together. |
| `docContent.View` | 1.55 s | The visible document body is rebuilt on every Workspace render. |
| `docview.Model.ScanContentLinks` | 1.27 s | The same visible document rows are scanned and decorated repeatedly. |
| `contentlink.ScanFrame` | 0.66 s | Document link recognition is the largest scanner beneath the pane. |
| `terminalContent.View` | 0.39 s | Terminal rendering remains material but is no longer the leading cost. |
| `termpreview.DrawRows` | 0.22 s | Row analysis is needlessly cold but smaller than document rescanning. |
| `workspace.renderSidebarContent` | 0.21 s | The activity pulse's intended changing region is comparatively cheap. |
| `app.Model.Update` | 0.31 s | Message handling is not the primary cost in this run. |

The terminal counters changed as follows during the same 30 seconds:

| Counter | Delta | Rate / interpretation |
| --- | ---: | --- |
| Model frames built | 1,143 | 38.1/s; the screen model continues consuming ordered output. |
| Model frames published | 31 | 1.03/s; adaptive publication and no-op suppression are working. |
| Terminal views rendered | 301 | 10.0/s; redraws are amplified after publication. |
| Row cache hits | 0 | The live leaf never reused analyzer state. |
| Row cache misses | 26,187 | About 87 analyzed rows per terminal render. |
| Terminal-link resolution requests | 30 | The existing counter covers the terminal link coordinator, not document scans. |
| Terminal-link resolution cache hits | 172 | Terminal preparation repeatedly consulted ready results; document cache behavior is not yet counted. |
| Process-wide resolver calls | 58 | These may be async document or terminal executions and are not proof of I/O directly inside `View()`. |
| Output-to-frame p95 | 30 ms | Below the existing 50 ms terminal presentation budget. |

The process-lifetime allocation profile is not duration-matched to the CPU sample, so its totals are directional rather than an acceptance baseline. It nevertheless explains the GC signature: about 145.6 GB of estimated allocation over the process lifetime, 118.7 GB cumulative beneath `app.Model.View`, 16.0 GB beneath `docContent.View`, 11.7 GB beneath `docview.ScanContentLinks`, 8.1 GB beneath `termpreview.DrawRows`, and 5.8 GB beneath `RowAnalyzer.analyze`. `runtime.memclrNoHeapPointers` was the largest flat CPU symbol at 16.03%, and GC drain accumulated 7.59%.

The existing isolated row benchmark proves the cache algorithm itself works when its owner survives: after warmup it reported 46.53 hits and 0.47 misses per operation. The live zero-hit result is a lifetime defect, not a weak fingerprint or constantly changing terminal.

## Root causes

### 1. Extracted terminal leaves do not all own an analyzer

`termpanes.Leaf` contains `RowAnalyzer`, but `termpanes.NewLeaf` leaves it nil. A few primary-host constructors patch the field afterward; shell, split, staged, decoded, and rekeyed paths can retain nil. `termpreview.DrawRows` treats nil as correct by constructing an ephemeral analyzer, so behavior stays visually correct while every render becomes a cold miss. This invariant drift entered after the implemented terminal performance work when terminal state moved into shared leaves.

The leaf constructor must establish the invariant once. Hosts should not remember which shared fields need initialization, and the renderer's nil fallback should remain correctness-only and observable rather than silently becoming the normal path.

### 2. Document link preparation is performed from every view

Project Workspace, global Sessions, and app content decks call `docview.Model.ScanContentLinks` on the complete visible body during composition. The scan splits and cuts every row, strips and parses ANSI/OSC, runs regex recognition, builds decoration, rebuilds output, and reconstructs hit rectangles even when the document, viewport, resolution snapshot, matchers, selection, and geometry are unchanged.

The document model already caches its expensive Markdown/source layout. The missing layer is a prepared visible frame that combines that layout with content-link spans and records an explicit visual identity. Hosts should replay immutable relative hits and consume prepared output; they should not discover pending work or mutate hit lists from `View()`.

This profile does **not** show `livepanes`, `livewatch`, or file-preview loading as a CPU hotspot. Document freshness is not too aggressive in this run. The repeated work is presentation and content-link rescanning, so slowing filesystem observation would degrade UX without addressing the measured cause.

### 3. A cheap sidebar animation invalidates an expensive preview

The project Workspace activity marker intentionally advances every 175 ms while a visible agent is working or blocked. That cadence is part of the current UX and costs little to render by itself. Bubble Tea still calls the plugin's whole `View()`, which clears regions, recomposes the sidebar and preview, calls every pane content view, rebuilds pane chrome on a canvas, and recreates link hit maps.

The prior terminal plan measured and implemented the inverse cache for global Sessions: terminal-only updates do not rebuild the global list. This profile supplies the evidence the earlier plan required before adding comparable project-specific caching. The project surface now needs its stable preview composition isolated from sidebar-only invalidation.

### 4. Diagnostics do not yet expose redraw attribution

`terminal_views_rendered` proves amplification but cannot distinguish a terminal publication, activity pulse, document-resolution result, hover, or unrelated plugin message. The global list/preview counters are zero because this profile is the project Workspace. Without project view, document frame, preview-compose, and analyzer-bypass counters, regressions can remain visually correct and invisible until CPU rises.

## Scope and non-goals

### In scope

- Shared live terminal leaf construction and row-analyzer lifetime.
- Shared document viewer preparation for project Workspace, global Sessions, and app content decks.
- Root-aware, update-driven document content-link resolution using the existing `contentlink.ResolutionIndex` contract.
- Project Workspace preview composition caching around the existing `paneframe.Compose` and `paneframe.RegisterRegions` split.
- Privacy-safe counters and exact one-terminal-plus-one-document benchmarks needed to prove cache behavior and redraw attribution.
- Same-process CPU profiles and isolated real-app proof with private tmux and state trees.

### Not in scope

- Lowering the 175 ms activity pulse cadence, making the marker static, reducing terminal publication below the shipped adaptive policy, or adding a performance preference.
- Slowing document filesystem observation, weakening live refresh, or making a visible document stale.
- Replacing Bubble Tea, Lip Gloss, tmux control mode, `screenmodel`, `paneframe`, `docview`, or `contentlink`.
- Removing links, backgrounds, Markdown rendering, selection, hover, pane borders, scrollbars, cursor/mouse modes, or resize behavior.
- Building a general incremental UI framework before the three measured slices prove insufficient.
- Restarting, killing, replacing, or running automated proof against the default tmux server.

## Settled architecture

### 1. Every live terminal leaf owns durable presentation helpers

`termpanes.NewLeaf` initializes `RowAnalyzer`; `Decode` inherits it through `NewLeaf`, and rekeying retains the leaf. Remove host-specific analyzer initialization so construction has one authority. Add a small leaf-invariant test covering primary, shell, split, staged, decoded, and rekeyed paths in both Workspace projections.

Keep the renderer's nil fallback so malformed legacy/test state remains visually correct, but record a new fixed `row_analyzer_bypass` counter whenever it is used. A normal live journey must report zero bypasses. Do not make `DrawRows` panic or require hosts to coordinate cache invalidation: buffer identity, exact row bytes, visible window, and background mode remain the analyzer's existing keys.

### 2. `docview` exposes a prepared visible frame

Add an immutable `docview.PreparedFrame` containing the rendered body, relative content-link hits, and the pending resolution candidates that were newly discovered while preparing it. Its identity includes the document model's visual revision, visible body geometry, rendered/raw mode, selection/search/scrollbar presentation state, allowed-kind key, resolution snapshot generation, resource-matcher generation, and renderer-owned status. The retained collision guard is bounded to the visible frame; it must not retain whole historical documents.

Give `docview.Model` an explicit visual revision that advances only when its visible answer can change: loaded/refreshed content, size or wrap, scroll, render mode, selection, in-file search, scrollbar hover/drag, theme, and other row decoration state. Repeated reads after an unrelated message return the same prepared frame without rebuilding `Model.View()` or calling `contentlink.ScanFrame`.

Preparation belongs to the update/layout path. Pane geometry and document origins must be available before composition; `View()` consumes prepared immutable data and never queues resolution commands, appends hit lists, or asserts size. The host always replays the prepared frame's relative hits at the current pane origin so cached cells and pointer targets cannot drift.

Use the existing root-aware `contentlink.ResolutionIndex.BeginClassified`, `Apply`, and `SnapshotForRoot` contract for document candidates. Pending identity includes root plus candidate, results are generation/token scoped, and project/global/app-deck hosts share the same preparation semantics. Do not invent a second resolver cache or generalize terminal `LinkState` into a framework unless code reuse is concrete after the document contract exists.

### 3. Project Workspace caches the composed preview, not mutable regions

After pane content views are pure, cache the output of the existing `paneframe.Compose` for the project preview. Reuse it only when the pane layout/zoom/size, terminal and passive-leaf visual revisions, focus, theme/background, header actions, hover/drag chrome, selection, search/modal overlays, and other visible inputs are unchanged.

Always derive the current `panelayout.Layout` and call the existing `paneframe.RegisterRegions` path for the composed frame. Replay document link/search regions from prepared immutable descriptions. Never retain or skip clearing the mutable mouse hit map. The cache wraps the shared compositor; it does not fork leaf chrome, floors, divider order, or pane rendering from global Sessions.

A sidebar-only activity pulse invalidates the sidebar and final horizontal join but not the preview composition. A terminal frame invalidates only its leaf and the preview; a document refresh invalidates only its document leaf and the preview. Resize, theme, focus, hover, modal, split, tab, selection, search, and pointer state invalidate exactly the presentation they change.

### 4. Measure broader redraw work only after the three direct fixes

Add fixed counters for application views, project Workspace views, project sidebar renders, project preview composes/cache hits, document frames built/cache hits, document link scans, document resolution requests/cache hits, and row-analyzer bypasses. Counters carry no paths, terminal text, document text, session names, or message type strings.

Re-profile after the direct slices. The current profile contains smaller work in `tdmonitor.Update`, Tasks update/timezone construction, header/footer layout, and general string/canvas composition, but none outranks the measured pane work. Only if the CPU budget is still missed should a follow-up isolate unrelated plugin messages or add a top-level plugin-frame cache. Do not preemptively route messages or add a global frame cap from this baseline.

## Implementation sequence

### Slice 0 — Exact fixture and attribution counters

- Add a deterministic Workspace benchmark with one OpenCode-shaped terminal, one rendered Markdown document containing representative file/issue/diff/resource/URL tokens, and a working activity marker.
- Benchmark a cold frame, an unchanged pulse-only frame, a terminal-only frame, a document-resolution frame, and a document-refresh frame. Report allocations plus operation counters; do not assert machine-specific nanoseconds.
- Extend `/debug/terminalperf` with the fixed project/document/bypass counters and add snapshot/HTTP tests.
- Record counter-enabled behavior separately from counter-disabled CPU profiles.

Acceptance evidence: the unchanged pulse fixture reproduces repeated terminal row misses when the leaf lacks an analyzer, repeated document link scans, and repeated preview composition before the fixes.

### Slice 1 — Restore terminal leaf invariants

- Initialize `RowAnalyzer` in `termpanes.NewLeaf` and remove scattered host patches.
- Cover primary, shell, split, staged, decoded, replacement, and rekeyed leaves on project and global hosts.
- Record nil fallback as a bypass and prove normal real/fixture leaves never use it.

Acceptance evidence: after one cold draw, an unchanged live terminal draw produces row-cache hits, zero misses for unchanged rows, and zero analyzer bypasses. Terminal bytes, backgrounds, selection, scrollback, and cursor geometry remain identical.

### Slice 2 — Prepare document frames once per visual identity

- Add the `docview` visual revision and prepared-frame cache.
- Move document content-link discovery and root-aware resolution scheduling out of project/global/app-deck view composition.
- Derive decoration and pointer hits from the same immutable span set and replay hits at the current origin.
- Preserve negative expiry, newly created file discovery, renderer-owned Markdown links, OSC safety, cross-project roots, selection-over-link styling, search, raw/rendered mode, and activation behavior.

Acceptance evidence: 100 pulse-only redraws build one document frame and perform one visible-row link scan; a real document or resolution change builds exactly one new frame; an unchanged negative result schedules no duplicate resolver command before expiry.

### Slice 3 — Isolate project sidebar and preview invalidation

- Add the project preview composition cache around `paneframe.Compose`.
- Keep layout calculation and `paneframe.RegisterRegions` current on every final composition, replaying prepared link/search regions after the ordinary leaf/divider/header order.
- Make explicit invalidation tests cover terminal output, document refresh, size, split/move/zoom, tab, focus, theme/background, hover/drag, selection, search, modal overlays, action chips, and close/layout controls.
- Prove a pulse-only redraw rerenders the sidebar but performs zero preview composes and zero terminal/document content views.

Acceptance evidence: marker frames remain byte-distinct at the current 175 ms cadence, while the terminal/document preview bytes and all pointer regions remain correct and are reused.

### Slice 4 — Re-profile and address only remaining measured work

- Run the exact fixture and the live journey with the same build, dimensions, content, activity, profile duration, and visibility.
- If the CPU budget is missed, attribute the remainder with the new counters before changing plugin broadcast handling, Tasks/td monitor refresh work, app header/footer composition, or broader view memoization.
- Keep any follow-up a separate reviewed slice with its own behavior shield; do not bundle speculative cleanup into the direct cache fixes.

### Slice 5 — Integrated proof and documentation

- Run focused, race, pinned-module, broad build/test/lint, and diff checks.
- Run `./scripts/tmux-drive.sh paths` before the isolated real-app proof and confirm both tmux and Sidecar state/config paths are private.
- Exercise the terminal/document layout under output, idle, scroll, selection, link activation, refresh, resize, focus, hover, split/move/zoom, and return-to-follow.
- Update the shell-integration skill and active performance proof guide if diagnostic fields or commands change.
- Record exact revisions and counter-disabled CPU profiles; move this plan to `implemented` only after the measured journey passes.

## Verification commands

```bash
go test ./internal/termpanes/... ./internal/termpreview/... ./internal/docview/... ./internal/contentlink/...
go test ./internal/plugins/workspace/... ./internal/overview/... ./internal/app/... ./internal/paneframe/...
go test -race ./internal/termpanes/... ./internal/termpreview/... ./internal/docview/... ./internal/contentlink/... ./internal/plugins/workspace/... ./internal/overview/...
GOWORK=off go test ./internal/termpanes/... ./internal/termpreview/... ./internal/tty/... ./internal/tty/screenmodel/...
go test ./...
go build ./...
make lint-linux
git diff --check
```

Use current package paths if Go rejects a redundant `...`; the boundary coverage is authoritative, not the literal command spelling.

## Performance and UX acceptance budgets

All CPU comparisons use the same process build, dimensions, pane tree, document, activity fixture, terminal workload, and 30-second duration. Run at least three candidate pairs and report the median. Use one counter-enabled pass for behavioral rates and counter-disabled passes for CPU.

- Median CPU for the exact active terminal-plus-document journey is at most 15%, or at least 50% below the 27.11% diagnostic baseline if machine/background variance makes the absolute number incomparable.
- After warmup, unchanged terminal draws report at least 95% row-cache hits and zero analyzer bypasses. A pulse-only interval reports zero row misses.
- A pulse-only redraw performs zero document frame builds, zero document link scans, zero document resolver requests, zero terminal content views, and zero preview-tree composes.
- An unchanged document presentation performs no `contentlink.ScanFrame` calls and allocates no new prepared body or hit list. A changed document or resolution generation appears on the next frame without waiting for a TTL unrelated to that change.
- The activity marker keeps its current 175 ms cadence and visual frames. No performance change makes working/blocked state static or delayed.
- Document live refresh keeps its current watcher behavior and freshness; file changes, raw/rendered mode, search, selection, and scrolling remain immediately visible through their existing update path.
- Terminal output-to-frame p95 remains at or below 50 ms, the immediate leading/newest trailing frame policy remains intact, and input echo is not coupled to presentation caching.
- Project Workspace, global Sessions, and app content decks produce equivalent document link recognition and activation for the same root/content/matcher state.
- Benchmarks report allocation and operation-count deltas. Only stable semantic counts become test thresholds; machine-specific timings remain evidence in `td` and the proof guide.

## Risks and shields

| Risk | Required shield |
| --- | --- |
| A cached row carries stale ANSI background state | Keep the existing buffer/window/context keys and exact collision guard; constructor repair changes ownership only. |
| Cached document decoration disagrees with click targets | Body and relative hits come from one immutable prepared frame and one resolution generation. |
| A document cache hides selection, search, hover, or scrollbar changes | Those states advance the explicit doc visual revision, with focused invalidation tests. |
| A negative resolution hides a newly created file | Preserve short negative expiry and repaint on an accepted current result. |
| Two document roots collide on the same token | Use root-aware resolution keys and pending identity; reject stale token/context results. |
| Preview caching leaves stale pointer regions | Cache composed bytes only; clear and replay current regions through `paneframe.RegisterRegions` and prepared region descriptions every frame. |
| Preview caching forks project/global pane behavior | Continue using shared `panelayout`/`paneframe`; surface caches wrap their result and do not implement chrome or floors. |
| Activity appears less alive | Keep cadence and marker frames unchanged; only the stable preview is reused. |
| Document freshness is traded for CPU | Do not alter `livepanes`/`livewatch`; refresh messages advance the document revision immediately. |
| Diagnostics materially inflate the claimed result | Behavioral counter pass and counter-disabled CPU pass are separate and explicitly labeled. |
| Default tmux or live state is damaged during proof | Use only private tmux sockets and private state/config roots; never restart or automate the default server. |

## Review gates

- Slice 1 review checks every live-leaf construction/rekey/decode path and confirms the nil fallback is no longer normal operation.
- Slice 2 review traces content from document layout through preparation, resolution, decoration, hit replay, and activation on all three hosts, including stale roots and OSC/renderer-owned rules.
- Slice 3 review enumerates every preview invalidator and verifies the existing region priority order rather than only comparing rendered text.
- Slice 4 review rejects unmeasured global throttling or message-routing complexity and requires current profiler evidence for any additional optimization.
- Final review inspects the exact candidate revision, counter-disabled CPU evidence, counter-enabled semantic rates, isolated proof paths, and default-tmux inventory before completion.

## Completion criteria

1. The exact active terminal-plus-rendered-document journey meets the CPU budget without reducing activity, terminal, or document update cadence.
2. Every live terminal leaf owns a durable row analyzer, and normal project/global paths report no analyzer bypasses.
3. Unchanged documents reuse a prepared frame; no document host scans visible content links or schedules resolution from `View()`.
4. A project sidebar pulse does not render terminal/document bodies or compose the preview pane tree, while current hit regions and pane chrome remain correct.
5. Project Workspace, global Sessions, and app content decks share document preparation semantics and root-aware resolution behavior.
6. Focused, race, pinned-module, broad test/build/lint, benchmark, and isolated real-app proofs pass on the reviewed head, with any unrelated failures recorded honestly.
7. Final evidence records both the counter-enabled behavior pass and counter-disabled CPU profiles, and this plan moves to `docs/plans/implemented/` only after the measured journey is complete.
