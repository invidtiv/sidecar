# Plan: macOS-style mouse interaction for all scrollbars

## Goal

Every scrollbar in sidecar behaves like a macOS scrollbar under the mouse:

- **Drag the thumb** → content scrolls proportionally, tracking the pointer.
- **Click the track** → view jumps toward the clicked spot (thumb animates/snaps so it is anchored at the click point).
- **Hover** → thumb and track highlight.
- Dragging past either end of the track clamps at that end without losing the gesture; releasing anywhere (even outside the window's idea of the bar) ends the drag cleanly.

Out of scope: keyboard scrolling changes, overlay/auto-hide scrollbars, smooth sub-line animation, touch-style momentum, horizontal scrollbars (none exist today), native tmux scrollbar (sidecar draws its own over captured output).

---

## Current behavior (baseline)

Scrollbars are **draw-only**. Nothing is clickable.

| Concern | Today |
|---|---|
| Shared renderer | `internal/ui/scrollbar.go` — `ScrollbarParams{TotalItems, ScrollOffset, VisibleItems, TrackHeight}` → `RenderScrollbar(params) string`. Proportional thumb math lives inline here. |
| Duplicate | `internal/modal/layout.go:309` re-implements the same math (modal background styling; comment cites import cycle). |
| Hit testing | No surface registers a scrollbar/thumb/track region in any `HitMap`. Clicks on the scrollbar column fall through to whatever is underneath. |
| Drag machinery | Already exists and is battle-tested: `internal/mouse.Handler` (`StartDrag`, `ActionDrag`/`ActionDragEnd`, `DragStartID`, lost-release recovery, double-click tracking). Pattern reference: `.claude/skills/drag-pane/SKILL.md`. |
| Region priority | `HitMap.Test` scans reverse — **last registered wins**. Pane surfaces fix registration order in `internal/paneframe/compose.go` (`RegisterRegions`); content-owned `Body` regions register last there today. |
| Terminal panes | Sidecar draws its own bar over captured scrollback (`internal/tty/viewport.go` `FitViewport`; rendering in `internal/plugins/workspace/terminal_viewport.go:141` and `internal/termpreview/render.go:127`). Last column is permanently reserved (`tty.ContentWidth`). Scroll state = rows-back-from-live-edge + `Follow`. Wheel ownership is governed by `internal/tty/wheel_route.go` (`WheelHandler`, `PaneMouseReporting`); clicks are forwarded into the pane via `tty.SendClick` only after host-side hit-testing decides not to consume them (`internal/tty/tty.go` `handleMouse` forwards nothing by itself). |

### Surfaces with visible scrollbars

Plain lists/text (integer item/row offsets):

- File browser tree, search results, preview — `internal/plugins/filebrowser/view.go`
- Git status sidebar — `internal/plugins/gitstatus/sidebar_view.go`
- Conversations list — `internal/plugins/conversations/view_layout.go`
- Notes list/editor — `internal/plugins/notes/view.go`, `layout.go`, `internal/noteview/model.go`
- Doc viewer / Issue viewer — `internal/docview/model.go`, `internal/issueview/model.go`
- Kanban lanes (one per column) — `internal/kanban/component.go`
- Sessions sidebar list — `internal/workspacelist/sidebar.go`
- Palette — `internal/palette/view.go`
- Config UI theme picker — `internal/configui/themepicker.go`
- Notification centre — `internal/app/notification_centre.go`
- Modal framework + project/worktree/theme switcher modals — `internal/modal/layout.go`, `internal/app/*_modal.go`

Terminal windows (rows-back-from-live-edge + `Follow`):

- Workspace primary terminal + term panel — `internal/plugins/workspace/terminal_viewport.go`
- Global Sessions preview terminals — `internal/overview` via `internal/termpreview`

---

## Product rules

1. **One shared implementation.** Geometry, inverse mapping (track row ↔ scroll offset), and drag math live in `internal/ui` next to the renderer, as **state-free functions/types** a headless caller could adopt unchanged. No surface keeps its own copy of thumb math. The `internal/modal/layout.go` duplicate is retired onto the shared code.
2. **Rendering stays a string.** The component renders exactly as today (same glyphs, same theme keys, same spacer-when-fits behavior); it additionally *reports* its geometry (track rect, thumb rect in local coordinates) so hosts can register hit regions. Anti-jitter spacer column must keep its width even while interactive states change.
3. **Regions win over content.** Each surface registers its scrollbar regions during the same render pass it builds its HitMap, **after** content regions (reverse-scan priority). In pane surfaces this happens inside the leaf's own body-region registration, not by adding a second registration pass to `paneframe`.
4. **Terminal panes never leak scrollbar clicks into the pane app.** A press/wheel inside the scrollbar column is consumed by sidecar regardless of `PaneMouseReporting()`. This check precedes `SendClick`/wheel forwarding.
5. **Dragging off the live edge disengages Follow** (and normal existing re-follow logic resumes when scrolled back to bottom / new activity policy applies). Dragging never snaps the window back mid-gesture.
6. **Track click = jump-to-spot**: the offset jumps such that the grabbed point becomes the thumb anchor (macOS "jump to the spot that's clicked"). Whether to also offer the "jump to next page" alternative is an open question (below); do not build both now.
7. **No regression to wheel behavior.** Wheel routing (`WheelHandler`, flick coalescing, `WheelStaysWithPointer`) is untouched; this plan adds pointer gestures only.
8. **Modal guard holds.** While a modal is open, drags started before it opened cancel (existing td-f63097 pattern).

---

## Design

### New shared piece: `ui.Scrollbar` gains interactivity

Keep `RenderScrollbar` working unchanged for any caller we have not migrated yet; introduce an interactive wrapper alongside it.

```go
// internal/ui/scrollbar.go (extended)
type Geometry struct {
    TrackRect  image.Rectangle // local coords, height == TrackHeight
    ThumbRect  image.Rectangle
    HasThumb   bool            // false when everything fits
}

// Same inputs as RenderScrollbar, plus the geometry it already computes.
func RenderScrollbarWithGeometry(params ScrollbarParams) (string, Geometry)

// Inverse mappings — the state-free core of the feature:
func OffsetAtRow(params ScrollbarParams, row int) int   // track click / thumb grab
func RowForOffset(params ScrollbarParams, offset int) int
```

Region IDs (constants in `internal/ui`, embedded in each surface's own HitMap namespace as today):

- `regionScrollbarThumb`
- `regionScrollbarTrack`

Gesture contract (identical shape to every existing drag):

```
press thumb    → StartDrag(x, y, thumbRegion, startOffset)
press track    → jump-to-spot, then StartDrag(...) at the same anchor
                 (macOS lets you keep dragging after a track click)
ActionDrag     → offset = OffsetAtRow(params, y - grabDelta), clamped
ActionDragEnd  → settle; nothing persisted (scroll offsets are ephemeral state)
hover          → HandleState-style highlight (reuse ui.HandleState pattern from divider.go)
```

Because `OffsetAtRow`/`RowForOffset` take plain numbers, both scroll vocabularies adopt it without translation layers:

- Lists: `TotalItems=len(rows)`, `ScrollOffset=offset` — direct.
- Terminals: map through the window model (`FitViewport` inputs): total = scrollback extent, offset = rows-from-top of window; on release translate back to the host's `Offset/Follow` state. `Follow` clears iff resulting window is not pinned to the live edge.

### Routing notes

- App-level mouse dispatch (`internal/app/update.go`) already Y-offsets messages per surface; no changes needed above the plugin layer except: nothing — scrollbar handling is entirely inside each surface, like every other region.
- Terminal hosts (`workspace` interactive path, `overview` preview): add the scrollbar-column hit test **before** consulting `PaneMouseReporting()` so rule 4 holds.

### Migration shape per surface

Each adoption is mechanical and independent:

1. Replace `RenderScrollbar(...)` call with `RenderScrollbarWithGeometry(...)`.
2. Register `regionScrollbarThumb`/`regionScrollbarTrack` rects in that surface's render-pass HitMap registration (after content).
3. Handle `ActionClick`/`ActionDrag`/`ActionDragEnd`/`ActionHover` for those IDs in the surface's mouse handler, using the shared helpers.
4. Tests: thumb-at-offset math, drag end-to-end, click-track jump, region-priority-over-content.

---

## Work sequence

### Phase 1 — Shared interactive core (no visible change yet)

- Extend `internal/ui/scrollbar.go` with `Geometry`, `RenderScrollbarWithGeometry`, `OffsetAtRow`, `RowForOffset`, region ID constants, and hover/drag style hooks (theme keys exist: `ScrollbarThumb`/`ScrollbarTrack`; add pressed/hover variants if cheap, else derive).
- Unit tests for inverse math: round-trip, clamping, min-size thumb, fits-without-thumb (geometry reports `HasThumb=false`, handlers must ignore).
- Retire `internal/modal/layout.go` duplicate onto the shared renderer.

### Phase 2 — Simple list surfaces

Adopt, one PR-sized slice per surface group, cheapest first to shake out the pattern:

1. Palette + notification centre (small, well-tested views)
2. File browser (tree/search/preview)
3. Git status sidebar, conversations, notes (+ noteview)
4. Kanban lanes (per-column regions — verify N bars coexist in one HitMap)
5. Workspacelist sidebar, configui theme picker
6. Doc viewer, issue viewer
7. Modal framework + project/worktree/theme switcher modals (modal-level mouse routing passes through `m.activeModal()` first — confirm regions reachable there)

### Phase 3 — Terminal windows

- Workspace primary terminal + term panel: scrollbar-column consumption gate ahead of mouse-reporting forwarding; drag maps through the tty window model; `Follow` disengages off-live-edge, restores per existing policy at bottom.
- Overview Sessions preview terminals (shared `termpreview` render path — mostly inherits Phase 1–2 work, plus the same gate).
- Verify wheel burst/coalescing tests still pass untouched.

### Phase 4 — Polish and parity sweep

- Hover highlight on thumb/track across all surfaces.
- Grep audit: no remaining direct `RenderScrollbar` callers that should be interactive (a deliberate allow-list may remain for tiny decorative uses — record it).
- Update `.claude/skills/ui-features/SKILL.md` shortcut/interaction table if it mentions scrolling.

---

## Acceptance evidence

- `go test ./...` green, including new `internal/ui` inverse-math tests and per-surface drag tests modeled on `TestHandler_DragLifecycle`.
- Headless proof runs via `./scripts/tmux-drive.sh` (isolated socket **and** state tree; `paths` checked):
- File browser: drag thumb down → tree offset follows; release outside bar → stays put.
- Terminal pane with an agent producing output: drag up into history → `Follow` off, window holds position while output streams; drag to bottom → re-follow.
- Pane-app-with-mouse-reporting case (e.g. a TUI running inside the embedded terminal): clicking the scrollbar column does not send anything to the pane app.
- Kanban: dragging one lane's thumb does not disturb other lanes.
- Manual checklist against macOS feel: track click anchors at grab point, past-end clamp, hover highlight.

---

## Open questions

- Should track-click support a "jump to next page" alternative (macOS offers both)? Default plan: jump-to-spot only; revisit if it feels wrong in practice.
- Hover/pressed colors: reuse existing theme keys with intensity modulation, or add `ScrollbarThumbHover`/`ScrollbarThumbActive` theme keys? (Adding keys touches `create-theme` docs and curated themes.)
- Should the anti-jitter spacer column (content-fits case) ever appear interactive? Plan says no — no regions registered when `HasThumb` is false.
