package issueview

import (
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/ui"
)

// Interactive scrollbar for the issue card.
//
// The bar is drawn by View exactly as ui.RenderScrollbar always drew it; this
// file adds the pointer half. The gesture lives at the card's own input seam
// — HandleClick and HandleHover, the view-local entries every host already
// routes — so a press on the bar is answered here instead of reaching the
// nav rows: parent/subtask rows are the card's action buttons, and a press on
// the bar must never open one. Because a click alone cannot turn motion into
// a drag, HandleClick arms the gesture and hosts ask ScrollbarDragging to
// decide whether to start their shared handler's drag; the motions come back
// through ScrollbarDrag and its release through ScrollbarDragEnd. A host that
// prefers region registration derives thumb/track rects from ScrollbarRect
// and the shared geometry, registers them after its content regions, and
// routes them through the same three calls.
//
// Nothing about the gesture is persisted; the scroll offset is view state.

// ScrollbarParams reports the renderer inputs View draws the bar with: one
// row per rendered card row, a viewport of height rows. Hosts feed these to
// ui.RenderScrollbarWithGeometry for region registration and to
// ui.OffsetAtRow/RowForOffset for press and drag mapping — no scrollbar math
// lives outside internal/ui.
func (m *Model) ScrollbarParams() ui.ScrollbarParams {
	return ui.ScrollbarParams{
		TotalItems:   len(m.ensureRows()),
		ScrollOffset: m.scroll,
		VisibleItems: m.height,
		TrackHeight:  m.height,
	}
}

// HasScrollbar reports whether the card currently draws a bar (content
// overflows the viewport). When false, hosts must register no regions: the
// reserved column is an anti-jitter spacer, not a control.
func (m *Model) HasScrollbar() bool {
	if m == nil || !m.needsScrollbar() {
		return false
	}
	_, geom := ui.RenderScrollbarWithGeometry(m.ScrollbarParams())
	return geom.HasThumb
}

// ScrollbarRect is the track column in view-local cells, spanning the rows
// the track is drawn over. Zero when no interactive bar exists (nothing
// overflows, or the reserved column is the anti-jitter spacer); the zero rect
// contains no point.
func (m *Model) ScrollbarRect() mouse.Rect {
	if m == nil || !m.needsScrollbar() || m.height <= 0 {
		return mouse.Rect{}
	}
	_, geom := ui.RenderScrollbarWithGeometry(m.ScrollbarParams())
	if !geom.HasThumb {
		return mouse.Rect{}
	}
	return mouse.Rect{
		X: m.leftPadding() + m.contentWidth(),
		Y: 0,
		W: 1,
		H: m.height,
	}
}

// OffsetAtTrackRow maps a track row (view-local pointer Y) onto the scroll
// offset whose thumb top anchors there — the shared core every other
// interactive scrollbar uses for track clicks and drags.
func (m *Model) OffsetAtTrackRow(row int) int {
	return ui.OffsetAtRow(m.ScrollbarParams(), row)
}

// ScrollbarDragging reports whether a scrollbar gesture is live.
func (m *Model) ScrollbarDragging() bool {
	return m != nil && m.scrollbarDragging
}

// ScrollbarDrag extends the live gesture from a held pointer at view-local y,
// clamping past both ends of the track without ending it. Reports whether a
// gesture was live.
func (m *Model) ScrollbarDrag(y int) bool {
	if m == nil || !m.scrollbarDragging {
		return false
	}
	// The press-time params snapshot keeps a mid-gesture re-render — a live
	// refresh, an ACTIONS row appearing — from shifting the mapping under the
	// pointer.
	offset := ui.OffsetAtRow(m.scrollbarDragParams, y-m.scrollbarGrabDelta)
	m.scroll = offset
	m.clampScroll()
	return true
}

// ScrollbarDragEnd settles a finished or cancelled scrollbar gesture.
func (m *Model) ScrollbarDragEnd() {
	if m == nil {
		return
	}
	m.scrollbarDragging = false
	m.scrollbarGrabDelta = 0
}

// settleStaleScrollbarGesture ends a gesture whose continuation can no longer
// arrive. HandleClick and HandleHover call it on the boundaries that prove a
// live drag is over — a fresh press, or motion the shared handler delivered as
// hover because it held no drag — so an unwired or lost release can never
// leave the thumb rendered pressed indefinitely.
func (m *Model) settleStaleScrollbarGesture() {
	if m != nil && m.scrollbarDragging {
		m.ScrollbarDragEnd()
	}
}

// beginScrollbarGesture answers a press on an interactive bar: grabbing the
// thumb where it was pressed, or jumping to the clicked spot and anchoring
// the thumb there so the same gesture keeps dragging (macOS track-click).
// Reports whether the press was the bar's.
func (m *Model) beginScrollbarGesture(x, y int) bool {
	if m == nil {
		return false
	}
	rect := m.ScrollbarRect()
	if rect.W <= 0 || !rect.Contains(x, y) {
		return false
	}
	params := m.ScrollbarParams()
	_, geom := ui.RenderScrollbarWithGeometry(params)
	if !geom.HasThumb {
		// Everything fits: the column is the anti-jitter spacer, not a
		// control, and stays inert — the click falls through to the card's
		// ordinary handling.
		return false
	}

	m.scrollbarHover = true
	m.scrollbarDragging = true
	m.scrollbarDragParams = params
	if y >= geom.ThumbRect.Min.Y && y < geom.ThumbRect.Max.Y {
		m.scrollbarGrabDelta = y - ui.RowForOffset(params, m.scroll)
		return true
	}
	// Track press: jump-to-spot, anchored at the grabbed row.
	m.scrollbarGrabDelta = 0
	m.scroll = ui.OffsetAtRow(params, y)
	m.clampScroll()
	return true
}

// scrollbarContains reports whether a view-local point lands on an
// interactive bar.
func (m *Model) scrollbarContains(x, y int) bool {
	rect := m.ScrollbarRect()
	return rect.W > 0 && rect.Contains(x, y)
}

// scrollbarStyle derives hover/drag emphasis from the shared core's state
// hooks. Idle is the zero style, which renders byte-identically to plain
// RenderScrollbar.
func (m *Model) scrollbarStyle() ui.ScrollbarStyle {
	hovering := m.scrollbarHover && !m.scrollbarDragging
	return ui.ScrollbarStyle{
		Thumb: ui.HandleStateFrom(hovering, m.scrollbarDragging),
		Track: ui.HandleStateFrom(hovering, false),
	}
}
