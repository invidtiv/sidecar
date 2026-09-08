# Files tab idle CPU — stop paying for unchanged frames

**Status:** M0–M2 landed on main 2026-09-08 (M3 measured unnecessary, M4 open) **Created:** 2026-09-08 **Tracking:** `td-8cdc05` (M0+M1 `td-5f1e01`, M2 `td-4fc78e`)

One sentence: **an idle Files tab must cost no more than any other idle tab, without changing how quickly the tree and preview follow the filesystem.**

## The problem, measured

On a live instance in this repo the user sees roughly 11% CPU idle on Workspaces and 25–35% idle on Files. The headless reproduction (`scripts/tmux-drive.sh` with `SIDECAR_PPROF=6171 SIDECAR_TERMINAL_PERF=1`, the user's own `fileBrowser["/Users/marcus/code/sidecar"]` entry seeded into the isolated `state.json`: six expanded dirs, three preview tabs, `docs/reference/design-language.md` rendered as the active tab, two chatty shells producing terminal output) gives the per-frame numbers that explain it:

| Surface | Renders/s (headless) | CPU over 20s | Cost per rendered frame |
| --- | --- | --- | --- |
| Workspaces | 3.2 | 520ms | ~2.8ms |
| Files | 2.75 | 690ms | ~6.7ms |

Every Bubble Tea message re-renders the whole application (`Program.render` calls `Model.View` after each `Update`), so a frame's cost is multiplied by the message rate. The live instance has 17 live shells plus agent watchers, so its message rate is far higher than the headless one, and the 2.4× per-frame difference becomes the observed doubling. The Files render rate is not the problem: idle, headless Files renders at 1.6/s, the same as td.

Where the 6.7ms goes, from `go tool pprof` on the seeded run (`renderContentDeck` is 360ms of the 370ms `Model.View` total):

1. **`appContentDeck.scanPrimary` — 150ms (40%).** Every frame re-runs `contentlink.ScanFrame` over every visible preview row: `ansi.Cut` per row, then ten regexps per row (`scanBareFiles`, `scanGitSpecs`, `scanPathLines`, URL, issue, session, resource matchers), then the frame is re-joined. Nothing is cached across frames even though the frame string is byte-identical between two idle renders. `internal/app/content_deck.go:662`.
2. **`filebrowser.(*Plugin).View` — 150ms (40%).** The tree pane and the preview pane are rebuilt from scratch on every render: per-node lipgloss styling, gutter, wrap, selection injection, scrollbar. The existing `viewCache` in `plugin.go:1374` is a "reuse once" latch set only by one mouse path (`mouse.go:555`), so it never helps an idle frame.
3. **`paneframe.Compose` / `ui.FitBlock` / `Canvas.Blit` / `leavesStyleOpen` — ~60ms (16%).** The deck composes a single primary leaf through the general compositor every frame, scanning styles to fit the block.

The `Workspaces` document pane already solved the first two for its own surface: `docview.(*Model).PrepareFrame` (`internal/docview/prepared_frame.go`) builds the visible document and its link metadata once per visual identity and replays hits at the current origin, recording `DocumentFrameCacheHit` in `internal/terminalperf`. The Files preview does not go through docview, so it gets none of that.

Not the cause, checked and ruled out:

- The filesystem watcher. `TreeWatcher` is fsnotify/kqueue with a 150ms quiet period and a 1s latency cap; it only fires when something under a watched directory changes, and a refresh runs `BuildTree` off the update goroutine. Idle, it costs nothing. **It stays exactly as it is.**
- Message rate specific to Files. Measured equal to other tabs when idle.
- Syntax highlighting or markdown rendering per frame. Both are precomputed (`previewHighlighted`, `markdownRendered`) and only the visible rows are styled per frame.

## Constraints

- **Freshness is not negotiable.** The tree must keep following the filesystem through the watcher, the preview must keep re-reading a changed file, and a change must land on screen within the same latency it does today. Every cache in this plan is invalidated by the same events that mutate the state it caches, and never by a timer alone.
- **No second compositor, border rule, or scanner.** `paneframe` and `contentlink` stay the single owners of their concerns (see AGENTS.md, "Project and global workspace parity"). Caching wraps them; it does not fork them.
- **Do not touch the render loop's cadence.** Throttling `Model.View` or the FPS is a cross-cutting change with its own risks; it is a follow-up (M4), gated on a measurement, not part of the fix.
- **Measure before and after with the same harness.** Claims of improvement come from `scripts/` runs and `terminalperf` counters, not from reading code.

## Work sequence

### M0 — Reproducible measurement

Add a script `scripts/files-idle-cpu.sh` that drives the existing `tmux-drive.sh` isolation: builds the binary, starts with `SIDECAR_PPROF` and `SIDECAR_TERMINAL_PERF=1`, seeds the isolated `state.json` with a fixture `fileBrowser` entry for the launch repo (six expanded dirs, three tabs, a ~40KB markdown active tab, mirroring the user's state), creates two chatty shells via `tmux-drive.sh cli create shell --run ...`, then for each of Workspaces and Files: switches tab, waits, snapshots `terminalperf` counters, takes a 20s pprof CPU profile, snapshots counters again, and prints renders/s, total CPU, and CPU per rendered frame, plus the top cumulative `sidecar/internal` frames. It must run `tmux-drive.sh paths` first and stop on exit (trap), and refuse to run against the default tmux server.

Add `terminalperf` counters for the new caches so hits are observable: `FilesFrameBuilt`, `FilesFrameCacheHit`, `FilesLinkScans`, `FilesLinkScanCacheHits`, `DeckComposeCacheHits`.

Acceptance: the script prints the baseline table above (numbers within noise) on an unmodified tree.

### M1 — Cache the deck's primary-surface link scan

In `appContentDeck.scanPrimary`, cache the scan result per surface and reuse it when nothing that feeds the scan changed. The key is the surface's rect, its `Kinds`, `RendererOwned`, `WorkDir`, the resolution snapshot generation for that root (`h.resolution.SnapshotForRoot(...).Generation()`), `h.matcherGeneration`, and the exact bytes of the rows inside the rect (compare the extracted segments, or the whole frame string when that is cheaper; a string equality check on ~20KB is far cheaper than ten regexps per row). On a hit, reuse the decorated rows and replay the recorded spans into `h.links` at the current origin, exactly the way `docview.AppendHitsAt` replays hits. Pending resolutions are re-offered through the same `queueContentLinkResolve` path so an in-flight resolution still lands; a resolution landing bumps the snapshot generation, which is what invalidates the cache and re-decorates the row. Prefer reusing `docview`'s `PreparedFrame` machinery over writing a parallel structure if it can be extracted cleanly; if not, keep the new cache inside `content_deck.go` and make it obviously the same shape.

Acceptance: with the M0 harness, `FilesLinkScanCacheHits` grows at the render rate while idle and `FilesLinkScans` stays flat; a preview change (write to the previewed file) or a resolution landing produces exactly one new scan; existing content-link tests in `internal/app` and `internal/contentlink` pass; clicking a link in the Files preview still activates it at the right cell after the tree pane is resized.

### M2 — Memoize the file browser's own frame

Replace the "reuse once" latch in `filebrowser.(*Plugin).View` with a real memo: the rendered frame is kept and returned while it is valid for the same width and height, and invalidated by any state change that affects what is drawn. Invalidation is explicit and conservative: mark the frame dirty at the top of `update()` for every message the plugin handles (anything that does not fall through to the ignore path), in every mouse handler, in `SetFocused`, on `ThemeChangedMsg`, on `WindowSizeMsg`, when a tree build, preview load, preview refresh, search, blame, info, quick-open, file-op, drag, inline-edit, selection, scroll, tab, or wrap state changes, and whenever the app calls a setter that changes selection binding (`SetSelection`). A message the plugin does not recognize must not invalidate — that is the whole point — so the switch's default branch is the only place that leaves the memo alone, and it must be audited to confirm it mutates nothing.

Hit regions: `renderView` clears and re-registers `p.mouseHandler` regions during a render. A cached frame keeps the regions registered by the render that produced it, which is correct because the frame and its regions are one snapshot; verify `registerPreviewSelectionRegions` and the tree/preview scrollbar regions survive a cache hit, and add a test that a click after two idle renders still lands.

The inline editor (`p.edit.Active`) and any state that embeds a live terminal must bypass the memo entirely: terminal frames come from tmux and change without a plugin-visible message. Image previews rendered through the terminal also bypass.

Acceptance: `FilesFrameCacheHit` grows at the render rate while idle and `FilesFrameBuilt` stays flat; every existing filebrowser test passes; the autorefresh tests (`autorefresh_test.go`, `live_preview_test.go`) still see a new frame after a watcher event; a new test drives a watcher event through `Update` and asserts the next `View` differs; cursor movement, scrolling, hover, selection drag, and search each produce a fresh frame on the very next render.

### M3 — Skip composition for an unchanged single-leaf deck

Only if M1 and M2 leave `paneframe.Compose` as the largest remaining per-frame cost on Files (measure first). When the deck's layout is unchanged since the last render (same `h.layout`, same canvas, same focus, same zoom), every leaf's rendered content is the same string as last time, and no overlay is active, return the previously composed frame instead of re-blitting. The leaf content strings are already produced by M1/M2 caches, so the comparison is cheap. Keep `RegisterRegions` running every frame; only the pixel work is skipped.

Acceptance: `DeckComposeCacheHits` grows at the render rate while idle; the drag-handle, focus-border, and hit-region tests in `internal/paneframe` and `internal/app` pass unchanged.

### M4 — Follow-up, separately gated: render rate on a busy instance

Not part of this fix. Measure `application_views_rendered` per second on the live instance with `SIDECAR_TERMINAL_PERF=1` while idle on each tab and record which message types arrive when nothing on screen changes (off-screen terminal deliveries, agent watchers, td monitor). If the idle rate is well above the 1/s app tick, open a separate plan for suppressing renders from messages that touched no visible surface. That is a whole-app change and must not be smuggled into this one.

## Result (2026-09-08)

M0, M1, and M2 are on main (`scripts/files-idle-cpu.sh`, the deck scan cache in `internal/app/content_deck.go`, the frame memo in `internal/plugins/filebrowser/view_memo.go`), reviewed by a fresh-context agent with one should-fix applied (the memo is invalidated before the remote-bound early return in `Update`). Same harness, same seeded state, two chatty shells, 20s per surface:

| Surface | Renders/s | CPU over 20s | Render path per frame | Cache counters |
| --- | --- | --- | --- | --- |
| Workspaces (chatty panes visible) | 11.0 | 1.24s | ~2.0ms | — |
| Files | 3.0 | 280ms | 0.83ms (deck 0.33, link scan 0.17, Compose 0.33) | 0 frames built / 60 hits; 0 scans / 60 hits |

Files went from ~6.7ms to under 1ms per rendered frame and now costs less than Workspaces per frame. Freshness was proved live on both branches: a file created in an expanded directory and a line appended to the previewed file each appeared within the watcher's existing window at the cost of exactly one rebuild and one scan.

M3 is not needed: `paneframe.Compose` is 0.33ms per frame after M1 and M2, below the threshold that would justify a compose cache. Keep the `DeckComposeCacheHit` counter for the day that changes.

Two review nits are recorded here rather than fixed, because neither affects correctness: the `reuseViewOnce` field is now write-only and can be deleted with its wheel-burst comment, and the image-preview bypass could be narrowed to when the preview pane is on screen.

## Acceptance for the whole plan

- With the M0 harness, CPU per rendered frame on Files is at or below Workspaces, and total idle CPU on Files is at or below Workspaces and td.
- All existing tests pass: `go test ./internal/plugins/filebrowser/... ./internal/app/... ./internal/contentlink/... ./internal/paneframe/... ./internal/docview/...`.
- A watcher-driven proof: with the harness running and Files active, `touch` a file in an expanded directory and append a line to the previewed file; both changes appear on the next snapshot within the watcher's existing 150ms–1s window.
- The live instance, after `make install-local` and a restart, shows the Files tab at or below the Workspaces tab in `top`.

## Decisions

- Cache, do not throttle. The user experience (tree and preview freshness, hover, selection) stays identical; only work that produces a byte-identical result is skipped.
- Invalidate on events, never on time. A timer-based revalidation would hide a missed invalidation instead of surfacing it; a missed invalidation is a bug to fix with a test.
- The deck scan cache (M1) is independent of the plugin memo (M2) and lands even if M2 is later reverted; each is measured on its own.

## Open questions

- Whether `docview.PreparedFrame` can be reused for the primary surface without dragging docview's viewer model into the deck. Decide in M1 after reading `prepared_frame.go`; either answer is acceptable, duplication of the scanning logic is not.
