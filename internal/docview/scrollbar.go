package docview

import (
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/textselect"
	"github.com/marcus/sidecar/internal/ui"
)

// Interactive scrollbar for the document pane.
//
// The bar is drawn by View exactly as ui.RenderScrollbar always drew it; this
// file adds the pointer half. The gesture lives at the model's own input seam
// — HandleSelectionMouse — because that is the one path every host already
// routes presses, drags and releases through, whether the pane sits in the
// project workspace or the global browser: a press that lands in the bar is
// answered here instead of reaching the selection engine, the host sees
// Handled and starts its drag, and every later motion and the release come
// back through the same entry (see select.go). A host that prefers region
// registration derives thumb/track rects from ScrollbarRect and the shared
// geometry, registers them after its content regions, and routes them here
// the same way.
//
// Nothing about the gesture is persisted; the scroll offset is view state.

// docScrollbarDrag is the press-time snapshot of an in-flight scrollbar
// gesture. Drags keep the params taken when the button went down so a
// mid-gesture re-render — a live refresh, a resize — cannot shift the mapping
// under the pointer.
type docScrollbarDrag struct {
	active    bool
	grabDelta int                // track rows between the pointer and the thumb top
	params    ui.ScrollbarParams // renderer inputs at press time
	trackTopY int                // absolute Y of the track top at press time
}

// ScrollbarParams reports the renderer inputs View draws the bar with: one
// row per laid-out display row, a viewport of contentHeight rows. Hosts feed
// these to ui.RenderScrollbarWithGeometry for region registration and to
// ui.OffsetAtRow/RowForOffset for press and drag mapping — no scrollbar math
// lives outside internal/ui.
func (m *Model) ScrollbarParams() ui.ScrollbarParams {
	body := m.contentHeight()
	return ui.ScrollbarParams{
		TotalItems:   len(m.display().rows),
		ScrollOffset: m.scroll,
		VisibleItems: body,
		TrackHeight:  body,
	}
}

// HasScrollbar reports whether the model currently draws a bar (content
// overflows the viewport). When false, hosts must register no regions: the
// reserved column is an anti-jitter spacer, not a control.
func (m *Model) HasScrollbar() bool {
	if m == nil || !m.needsScrollbar() {
		return false
	}
	_, geom := ui.RenderScrollbarWithGeometry(m.ScrollbarParams())
	return geom.HasThumb
}

// ScrollbarRect is the track column in the coordinate space mouse actions
// arrive in — the origin SetOrigin recorded — spanning the rows the track is
// drawn over. Zero when no interactive bar exists (nothing overflows, or the
// reserved column is the anti-jitter spacer); the zero rect contains no
// point.
func (m *Model) ScrollbarRect() mouse.Rect {
	if m == nil || !m.needsScrollbar() {
		return mouse.Rect{}
	}
	body := m.contentHeight()
	if body <= 0 {
		return mouse.Rect{}
	}
	_, geom := ui.RenderScrollbarWithGeometry(m.ScrollbarParams())
	if !geom.HasThumb {
		return mouse.Rect{}
	}
	return mouse.Rect{
		X: m.originX + m.contentWidth(),
		Y: m.originY,
		W: 1,
		H: body,
	}
}

// OffsetAtTrackRow maps a track row (pointer Y minus the track top) onto the
// scroll offset whose thumb top anchors there — the shared core every other
// interactive scrollbar uses for track clicks and drags.
func (m *Model) OffsetAtTrackRow(row int) int {
	return ui.OffsetAtRow(m.ScrollbarParams(), row)
}

// ScrollbarDragging reports whether a scrollbar gesture is live.
func (m *Model) ScrollbarDragging() bool {
	return m != nil && m.scrollbarDrag.active
}

// scrollbarStyle derives hover/drag emphasis from the shared core's state
// hooks. Idle is the zero style, which renders byte-identically to plain
// RenderScrollbar.
func (m *Model) scrollbarStyle() ui.ScrollbarStyle {
	dragging := m.scrollbarDrag.active
	hovering := m.scrollbarHover && !dragging
	return ui.ScrollbarStyle{
		Thumb: ui.HandleStateFrom(hovering, dragging),
		Track: ui.HandleStateFrom(hovering, false),
	}
}

// handleScrollbarMouse answers the bar's share of a pointer action before it
// can reach the selection engine. handled is true only for actions it
// consumed: a press inside the bar column, or the drag and release of a
// gesture that started there. Hover updates state without claiming the
// action, since nothing downstream wants it.
func (m *Model) handleScrollbarMouse(action mouse.MouseAction) (textselect.Result, bool) {
	if m == nil {
		return textselect.Result{}, false
	}
	switch action.Type {
	case mouse.ActionDrag:
		if !m.scrollbarDrag.active {
			return textselect.Result{}, false
		}
		// The drag maps through the press-time snapshot and clamps past both
		// ends of the track without ending the gesture; the release settles.
		row := action.Y - m.scrollbarDrag.trackTopY - m.scrollbarDrag.grabDelta
		before := m.scroll
		m.setScroll(ui.OffsetAtRow(m.scrollbarDrag.params, row))
		return textselect.Result{Handled: true, Changed: m.scroll != before}, true

	case mouse.ActionDragEnd:
		if !m.scrollbarDrag.active {
			return textselect.Result{}, false
		}
		m.scrollbarDrag = docScrollbarDrag{}
		return textselect.Result{Handled: true}, true

	case mouse.ActionClick:
		rect := m.ScrollbarRect()
		if rect.W <= 0 || !rect.Contains(action.X, action.Y) {
			return textselect.Result{}, false
		}
		params := m.ScrollbarParams()
		_, geom := ui.RenderScrollbarWithGeometry(params)
		if !geom.HasThumb {
			// Everything fits: the column is the anti-jitter spacer, not a
			// control, and stays inert.
			return textselect.Result{}, false
		}
		row := action.Y - rect.Y
		grabDelta := row - ui.RowForOffset(params, m.scroll)
		if row < geom.ThumbRect.Min.Y || row >= geom.ThumbRect.Max.Y {
			// Track press, macOS jump-to-spot: the grabbed point becomes the
			// thumb anchor, so the continuing drag maps the pointer straight
			// onto track rows.
			m.setScroll(ui.OffsetAtRow(params, row))
			grabDelta = 0
		}
		m.scrollbarDrag = docScrollbarDrag{
			active:    true,
			grabDelta: grabDelta,
			params:    params,
			trackTopY: rect.Y,
		}
		return textselect.Result{Handled: true, Changed: true}, true

	case mouse.ActionHover:
		m.scrollbarHover = m.scrollbarContains(action.X, action.Y)
	}

	return textselect.Result{}, false
}

// scrollbarContains reports whether a point in action space lands on an
// interactive bar.
func (m *Model) scrollbarContains(x, y int) bool {
	rect := m.ScrollbarRect()
	return rect.W > 0 && rect.Contains(x, y)
}

// setScroll pins the viewport at offset, clamped into range by the same
// bounds the renderer maps onto thumb travel.
func (m *Model) setScroll(offset int) {
	m.scroll = offset
	m.clampScroll()
}

// abandonScrollbarGesture ends a scrollbar gesture whose release never
// arrived — the pointer left the window, a modal opened. Hosts already call
// AbandonSelection on that boundary for selections.
func (m *Model) abandonScrollbarGesture() {
	if m == nil {
		return
	}
	m.scrollbarDrag = docScrollbarDrag{}
	m.scrollbarHover = false
}
