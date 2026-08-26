package overview

import (
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

// Interactive scrollbar on the global Sessions preview terminal.
//
// The bar is drawn by the shared renderer exactly as it always was; what is
// new is that its column answers the pointer. The host owns the bookkeeping —
// tty is state-free layout and the window is preview state — so a press arms a
// gesture here from a press-time snapshot of the drawn window, motions map
// through that snapshot while the button is held, and a release hands the
// window back to the distance-from-live-bottom model, following output again
// from wherever the pointer left it.
//
// previewTermBarKind is the data carried by this pane's bar regions. The
// region IDs are the shared renderer's (ui.RegionScrollbar*), which the note
// tab's bar also uses in this same hit map, so the payload — not the ID — is
// what tells them apart, and at most one of the two gestures is ever live.
const previewTermBarKind = "global-preview-term-bar"

func isPreviewTermBarRegion(region *mouse.Region) bool {
	if region == nil {
		return false
	}
	if region.ID != ui.RegionScrollbarThumb && region.ID != ui.RegionScrollbarTrack {
		return false
	}
	kind, _ := regionKind(region)
	return kind == previewTermBarKind
}

// previewTermBar is one in-flight pointer gesture on the preview terminal's
// bar. The press-time snapshot keeps a mid-gesture re-render — output landing
// under the window, a resize — from shifting the mapping under the pointer;
// the shared core clamps past both ends of the track without ever ending
// anything.
//
// While the gesture holds, the window is pinned to an absolute start (the same
// freeze a text selection takes), so rows cannot slide when the live edge
// advances underneath. lastStart mirrors that pin so each motion moves it by a
// delta without reading back a layout.
type previewTermBar struct {
	sb        tty.WindowScrollbar // renderer inputs at press time
	trackTopY int                 // absolute row of the track top at press time
	grabDelta int                 // rows between the pointer and the thumb's anchor row
	lastStart int                 // buffer-relative start the freeze currently pins
	active    bool
}

// registerPreviewTermScrollbarRegions puts the preview terminal's bar regions
// in the hit map from the one derivation the render path draws with. It runs
// after every region this frame put down over the box — leaf, tabs, close,
// action chips — so the bar wins HitMap.Test's reverse scan inside its column.
// A surface whose content fits registers nothing: the reserved column is an
// anti-jitter spacer, not a control.
func (m *Model) registerPreviewTermScrollbarRegions() {
	window := m.previewWindow()
	if !window.ok || !window.layout.ShowScrollbar || window.layout.DisplayHeight < 1 {
		return
	}
	_, total := tty.BufferBase(m.previewBuffer())
	sb := tty.WindowScrollbarFor(window.layout, total)
	if !sb.HasThumb() {
		return
	}
	params := ui.ScrollbarParams{
		TotalItems:   sb.Total,
		ScrollOffset: sb.Offset,
		VisibleItems: sb.Visible,
		TrackHeight:  sb.Track,
	}
	_, geom := ui.RenderScrollbarWithGeometry(params)
	if !geom.HasThumb {
		return
	}
	// The viewport pads its content to PadWidth before joining the bar, which
	// pins the bar to the surface's final column whether or not the pane
	// letterboxes (td-26bdb2).
	barX := window.surface.X + window.surface.Width - 1
	top := window.surface.Y
	m.workspacesMouse.HitMap.AddRect(ui.RegionScrollbarTrack, barX, top, 1, geom.TrackRect.Dy(), previewTermBarKind)
	// The thumb is added after the track so the reverse scan hands a press on
	// their overlap to the thumb, exactly as the shared geometry orders them.
	m.workspacesMouse.HitMap.AddRect(ui.RegionScrollbarThumb, barX, top+geom.ThumbRect.Min.Y, 1, geom.ThumbRect.Dy(), previewTermBarKind)
}

// pressPreviewTermScrollbar begins the terminal's bar gesture: grabbing the
// thumb where it was pressed, or jumping to the clicked spot anchored there so
// the same gesture keeps dragging (macOS track-click). A rapid second press
// arrives as ActionDoubleClick and rides this seam deliberately — it re-grabs
// like the first press did instead of reaching whatever sits under the column.
func (m *Model) pressPreviewTermScrollbar(action mouse.MouseAction) {
	if action.Region == nil {
		return
	}
	window := m.previewWindow()
	if !window.ok || !window.layout.ShowScrollbar {
		return
	}
	_, total := tty.BufferBase(m.previewBuffer())
	sb := tty.WindowScrollbarFor(window.layout, total)
	params := ui.ScrollbarParams{
		TotalItems:   sb.Total,
		ScrollOffset: sb.Offset,
		VisibleItems: sb.Visible,
		TrackHeight:  sb.Track,
	}
	_, geom := ui.RenderScrollbarWithGeometry(params)
	if !geom.HasThumb {
		return
	}

	m.clearPreviewSelectionOnScroll()

	start := min(max(window.layout.Start, 0), max(window.layout.MaxOffset, 0))
	if !m.previewTerminalLeaf().Freeze.Active() {
		// Pin before anything reads or moves the window: a capture landing
		// mid-gesture would otherwise renumber the rows under it.
		m.previewTerminalLeaf().Freeze.Freeze(start)
	}

	trackTopY := action.Region.Rect.Y
	grabDelta := 0
	if action.Region.ID == ui.RegionScrollbarThumb {
		trackTopY -= geom.ThumbRect.Min.Y
		grabDelta = action.Y - trackTopY - ui.RowForOffset(params, sb.Offset)
	} else {
		// Track press: jump-to-spot, anchored at the grabbed row.
		bound := max(m.previewMaxOffset(), 0)
		target := min(max(sb.StartAtTrackRow(action.Y-trackTopY), 0), bound)
		m.previewTerminalLeaf().Freeze.Scroll(start-target, bound)
		start = target
	}
	m.previewTerminalState().termBar = previewTermBar{
		sb:        sb,
		trackTopY: trackTopY,
		grabDelta: grabDelta,
		lastStart: start,
		active:    true,
	}
	m.workspacesMouse.StartDrag(action.X, action.Y, action.Region.ID, 0)
}

// dragPreviewTermScrollbar applies a held gesture's mapping for one pointer
// row. Only a live gesture answers; wherever the pointer has since travelled,
// the surface it started on is the one that scrolls. The window moves inside
// its existing freeze — no thaw between motions — so output arriving mid-drag
// cannot slide the rows under the reader.
func (m *Model) dragPreviewTermScrollbar(y int) {
	g := m.previewTerminalState().termBar
	if !g.active {
		return
	}
	target := g.sb.StartAtTrackRow(y - g.trackTopY - g.grabDelta)
	bound := max(m.previewMaxOffset(), 0)
	target = min(max(target, 0), bound)
	m.previewTerminalLeaf().Freeze.Scroll(g.lastStart-target, bound)
	g.lastStart = target
	m.previewTerminalState().termBar = g
}

// settlePreviewTermScrollbar ends a finished or cancelled gesture: the window
// goes back to following the live edge from wherever the pointer left it
// (offset zero resumes following immediately, and cancels any pending reach
// for older history), and parking at the oldest row reaches for older history
// exactly as a wheel notch there would.
func (m *Model) settlePreviewTermScrollbar() {
	if !m.previewTerminalState().termBar.active {
		return
	}
	m.previewTerminalState().termBar = previewTermBar{}
	m.thawPreviewWindow()
	if m.previewTerminalLeaf().Scroll == 0 {
		// Back at the live edge: whatever older history the reader was reaching
		// for is no longer where they are looking.
		m.previewTerminalLeaf().History.Cancel()
		return
	}
	if m.previewTerminalLeaf().Scroll >= m.previewMaxOffset() {
		m.reachOlderPreviewHistory(tty.HistoryChunkLines)
	}
}

// previewTermBarOwnsDrag reports that a drag named by its source belongs to
// the terminal's live bar gesture. The note tab's bar starts its drags under
// the same shared region IDs, so the live-gesture state — not the ID alone —
// is what tells them apart.
func (m *Model) previewTermBarOwnsDrag(dragSource string) bool {
	return m.previewTerminalState().termBar.active &&
		(dragSource == ui.RegionScrollbarThumb || dragSource == ui.RegionScrollbarTrack)
}

// previewTermBarStyle is the emphasis the bar draws with: hover lights
// whichever part the pointer rests on, a live gesture keeps the thumb lit.
func (m *Model) previewTermBarStyle() ui.ScrollbarStyle {
	dragging := m.previewTerminalState().termBar.active
	hovering := m.hoverTermBar && !dragging
	return ui.ScrollbarStyle{
		Thumb: ui.HandleStateFrom(hovering, dragging),
		Track: ui.HandleStateFrom(hovering, false),
	}
}
