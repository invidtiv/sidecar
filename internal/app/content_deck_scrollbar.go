package app

import (
	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/issueview"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/noteview"
	"github.com/marcus/sidecar/internal/paneframe"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/textselect"
	"github.com/marcus/sidecar/internal/ui"
)

// Scrollbar gestures on deck-hosted document panes.
//
// The deck draws document, issue, and note panes whose views own interactive
// bars at their own seams; this file is the host half that turns the bar from
// draw-only into draggable here. Each kind keeps its own seam — docview's
// HandleSelectionMouse answers presses before its selection engine, issueview
// arms through HandleClick and continues through ScrollbarDrag, and noteview
// exposes a state-free mapping the host drives — so what lives here is only
// routing: where the bar was drawn, which events belong to it, and when a
// gesture ends. Nothing about a gesture is persisted; scroll offsets are view
// state.

const (
	// appDeckDocGestureRegion names a drag that began on a deck-hosted
	// document. HandleSelectionMouse is one seam for the bar and a text
	// selection alike, so this ID covers both: a press it answered starts the
	// shared handler's drag, and every motion and the release come back to the
	// same leaf's entry.
	appDeckDocGestureRegion = "app-content-doc-gesture"
	// appDeckIssueScrollbarRegion names a drag that began on an issue card's
	// scrollbar. The card arms the gesture in HandleClick (which also does any
	// track-click jump itself); this ID is what turns the host's StartDrag into
	// motions routed to ScrollbarDrag and a release anywhere that settles it.
	appDeckIssueScrollbarRegion = "app-content-issue-scrollbar"
)

// appDeckScrollbarHit is the payload of a hosted pane's registered bar region:
// which leaf the thumb or track belongs to.
type appDeckScrollbarHit struct {
	LeafID int
}

// isAppDeckNoteBarRegion reports that a region ID or drag source names a note
// card's bar. Note bars reuse the shared renderer's region IDs, the same
// vocabulary every other host registers them under.
func isAppDeckNoteBarRegion(id string) bool {
	return id == ui.RegionScrollbarThumb || id == ui.RegionScrollbarTrack
}

// appDeckNoteBar carries one note leaf's in-flight pointer gesture on its bar.
//
// noteview exposes a deliberately state-free seam, so the host owns the
// bookkeeping: the press-time params snapshot keeps a mid-gesture re-render —
// a live refresh, a resize — from shifting the mapping under the pointer, and
// OffsetAtRow clamps past both ends of the track without ever ending anything.
type appDeckNoteBar struct {
	leafID    int
	params    ui.ScrollbarParams // renderer inputs at press time
	trackTopY int                // absolute row of the track top at press time
	grabDelta int                // rows between the pointer and the thumb's anchor row
	active    bool
}

// registerAppContentScrollbars puts a hosted pane's bar regions in the hit map.
// It runs from the frame's Body pass — after every frame-owned content region —
// so the bar wins HitMap.Test's reverse scan over the leaf body drawn under it
// without outranking tabs or the close button. A pane whose content fits
// registers nothing: the reserved column is an anti-jitter spacer, not a
// control, and gestures over it stay inert.
func (h *appContentDeck) registerAppContentScrollbars(n *panelayout.Node, inner paneframe.Box) {
	var params ui.ScrollbarParams
	barX := 0
	switch v := h.deck.Viewer(n.ID).(type) {
	case *docview.Model:
		params = v.ScrollbarParams()
		barX = inner.X + inner.W - 1 // the body's last column
	case *issueview.Model:
		rect := v.ScrollbarRect() // view-local; zero when nothing overflows
		if rect.W <= 0 {
			return
		}
		params = v.ScrollbarParams()
		barX = inner.X + rect.X
	case *noteview.Model:
		if !v.HasScrollbar() {
			return
		}
		params = v.ScrollbarParams()
		barX = inner.X + inner.W - 2 // the card pads one column either side of its bar
	default:
		return
	}
	_, geom := ui.RenderScrollbarWithGeometry(params)
	if !geom.HasThumb || geom.TrackRect.Dy() <= 0 || inner.H <= paneframe.HeaderRows {
		return
	}
	top := inner.Y + paneframe.HeaderRows
	hit := appDeckScrollbarHit{LeafID: n.ID}
	h.mouse.HitMap.Add(ui.RegionScrollbarTrack, mouse.Rect{X: barX, Y: top, W: 1, H: geom.TrackRect.Dy()}, hit)
	// The thumb is added after the track so the reverse scan hands a press on
	// their overlap to the thumb, exactly as the shared geometry orders them.
	h.mouse.HitMap.Add(ui.RegionScrollbarThumb, mouse.Rect{X: barX, Y: top + geom.ThumbRect.Min.Y, W: 1, H: geom.ThumbRect.Dy()}, hit)
}

// routeAppContentGesture answers the pointer events that belong to a hosted
// pane's scrollbar — and, because docview exposes one seam for both, a
// document's text selection rides the same path. claimed consumes the event;
// unclaimed ones keep every existing answer.
//
// Routing is by gesture source once a drag is live: motions and releases
// belong to the bar they started on, wherever the pointer has since travelled,
// which is what lets a drag run past its pane's edges and settle anywhere.
func (h *appContentDeck) routeAppContentGesture(action mouse.MouseAction, wasDragging bool, dragSourceBefore string) (tea.Cmd, bool) {
	if action.Type == mouse.ActionHover && wasDragging && !h.mouse.IsDragging() {
		// A release lost off-window or behind focus change: the shared handler
		// cancels the stale drag on this first button-less motion, and the
		// gesture ends with it at the same boundary.
		return nil, h.settleAppContentGesture(dragSourceBefore)
	}
	switch action.Type {
	case mouse.ActionClick, mouse.ActionDoubleClick, mouse.ActionTripleClick:
		return h.pressAppContentGesture(action)
	case mouse.ActionDrag, mouse.ActionDragEnd:
		return h.continueAppContentGesture(action)
	}
	return nil, false
}

// pressAppContentGesture answers a button going down on one of the bar regions
// the last render registered. Each kind keeps its own seam: a document's press
// goes to HandleSelectionMouse (which answers the bar before its selection
// engine), an issue card's to HandleClick, a note's to the state-free mapping.
func (h *appContentDeck) pressAppContentGesture(action mouse.MouseAction) (tea.Cmd, bool) {
	if action.Region == nil {
		return nil, false
	}
	hit, ok := action.Region.Data.(appDeckScrollbarHit)
	if !ok {
		return nil, false
	}
	switch v := h.deck.Viewer(hit.LeafID).(type) {
	case *docview.Model:
		result := v.HandleSelectionMouse(action)
		if !result.Handled {
			// The view disagrees with the region (a re-render moved the thumb
			// between frames): nothing was armed, so claim nothing.
			return nil, false
		}
		h.mouse.StartDrag(action.X, action.Y, appDeckDocGestureRegion, hit.LeafID)
		h.docGestureLeaf = hit.LeafID
		return appDeckSelectionCopyCmd(v, result), true
	case *issueview.Model:
		if action.Type != mouse.ActionClick {
			// Bubble Tea emits the first click and then double/triple events at
			// the same cell. The first click is the sole navigation; replaying
			// it can open the child and then its newly rendered parent at once.
			return nil, true
		}
		lx, ly := h.appContentCardLocal(hit.LeafID, action.X, action.Y)
		kind, cmd := v.HandleClick(lx, ly)
		if kind == issueview.HitScrollbar {
			// The card armed a scrollbar gesture (and did any track-click jump
			// itself). Start the shared handler's drag so motions come back to
			// ScrollbarDrag and the release anywhere settles it.
			h.issueScrollLeaf = hit.LeafID
			h.issueScrollTrackY = action.Y - ly
			h.mouse.StartDrag(action.X, action.Y, appDeckIssueScrollbarRegion, hit.LeafID)
		}
		return cmd, true
	case *noteview.Model:
		return h.pressNoteScrollbar(hit.LeafID, action), true
	default:
		return nil, false
	}
}

// continueAppContentGesture extends or settles the live gesture named by its
// drag source.
func (h *appContentDeck) continueAppContentGesture(action mouse.MouseAction) (tea.Cmd, bool) {
	switch action.DragStartID {
	case appDeckDocGestureRegion:
		view, _ := h.deck.Viewer(h.docGestureLeaf).(*docview.Model)
		if view == nil {
			return nil, true
		}
		if action.Type == mouse.ActionDragEnd {
			h.docGestureLeaf = 0
		}
		return appDeckSelectionCopyCmd(view, view.HandleSelectionMouse(action)), true
	case appDeckIssueScrollbarRegion:
		view, _ := h.deck.Viewer(h.issueScrollLeaf).(*issueview.Model)
		if action.Type == mouse.ActionDragEnd {
			if view != nil {
				view.ScrollbarDragEnd()
			}
			h.issueScrollLeaf, h.issueScrollTrackY = 0, 0
			return nil, true
		}
		if view != nil {
			// The press-time snapshot of the card's top row maps the pointer
			// onto view-local rows; the shared core clamps past both ends of
			// the track without ending anything.
			view.ScrollbarDrag(action.Y - h.issueScrollTrackY)
		}
		return nil, true
	}
	if isAppDeckNoteBarRegion(action.DragStartID) {
		if action.Type == mouse.ActionDragEnd {
			h.noteBar = appDeckNoteBar{}
			return nil, true
		}
		h.dragNoteScrollbar(action.Y)
		return nil, true
	}
	return nil, false
}

// settleAppContentGesture ends whichever hosted-pane gesture a lost release
// left live, at the boundary where the shared handler abandoned its own drag.
// Reports whether dragSource named one of this deck's gestures.
func (h *appContentDeck) settleAppContentGesture(dragSource string) bool {
	switch dragSource {
	case appDeckDocGestureRegion:
		if view, ok := h.deck.Viewer(h.docGestureLeaf).(*docview.Model); ok {
			view.AbandonSelection()
		}
		h.docGestureLeaf = 0
	case appDeckIssueScrollbarRegion:
		if view, ok := h.deck.Viewer(h.issueScrollLeaf).(*issueview.Model); ok {
			view.ScrollbarDragEnd()
		}
		h.issueScrollLeaf, h.issueScrollTrackY = 0, 0
	case ui.RegionScrollbarThumb, ui.RegionScrollbarTrack:
		h.noteBar = appDeckNoteBar{}
	default:
		return false
	}
	return true
}

// pressNoteScrollbar begins a note card's bar gesture: grabbing the thumb where
// it was pressed, or jumping to the clicked spot anchored there so the same
// gesture keeps dragging (macOS track-click). The regions are what the last
// render reported, so the pointer maps onto what was actually drawn.
func (h *appContentDeck) pressNoteScrollbar(leafID int, action mouse.MouseAction) tea.Cmd {
	view, _ := h.deck.Viewer(leafID).(*noteview.Model)
	if view == nil || action.Region == nil {
		return nil
	}
	params := view.ScrollbarParams()
	_, geom := ui.RenderScrollbarWithGeometry(params)
	if !geom.HasThumb {
		return nil
	}
	offset := view.ScrollOffset()
	trackTop := action.Region.Rect.Y
	grabDelta := 0
	if action.Region.ID == ui.RegionScrollbarThumb {
		trackTop -= geom.ThumbRect.Min.Y
		grabDelta = action.Y - trackTop - ui.RowForOffset(params, offset)
	} else {
		// Track press: jump-to-spot, anchored at the grabbed row.
		offset = view.OffsetAtTrackRow(action.Y - trackTop)
		view.ScrollToOffset(offset)
	}
	h.noteBar = appDeckNoteBar{
		leafID:    leafID,
		params:    params,
		trackTopY: trackTop,
		grabDelta: grabDelta,
		active:    true,
	}
	h.mouse.StartDrag(action.X, action.Y, action.Region.ID, offset)
	return nil
}

// dragNoteScrollbar applies a note card's held gesture for one pointer row.
func (h *appContentDeck) dragNoteScrollbar(y int) {
	if !h.noteBar.active {
		return
	}
	if view, ok := h.deck.Viewer(h.noteBar.leafID).(*noteview.Model); ok {
		row := y - h.noteBar.trackTopY - h.noteBar.grabDelta
		view.ScrollToOffset(ui.OffsetAtRow(h.noteBar.params, row))
	}
}

// appContentCardLocal maps a surface point onto a hosted card's view-local
// cells: the chrome-aware inner box minus the tab-header row the card's body
// sits below.
func (h *appContentDeck) appContentCardLocal(leafID, x, y int) (int, int) {
	for _, placement := range h.layout.Leaves {
		if placement.Node == nil || placement.Node.ID != leafID {
			continue
		}
		inner := paneframe.GeometryForChrome(placement.Box, appDeckHost{h}.Chrome(placement.Node)).Inner
		return x - inner.X, y - inner.Y - paneframe.HeaderRows
	}
	return x, y
}

// appDeckSelectionCopyCmd delivers what a document gesture's engine result
// asked for — a copy, phrased by the shared pipeline and wrapped in this
// surface's own toast types. A result that asked for nothing produces no
// command, so every result goes straight here.
func appDeckSelectionCopyCmd(view *docview.Model, result textselect.Result) tea.Cmd {
	return view.SelectionCopyCmd(result, func(notice textselect.CopyNotice) tea.Msg {
		if notice.IsError {
			return msg.ToastMsg{Message: notice.Message, Duration: notice.Duration, IsError: true}
		}
		return msg.FlashMsg{Text: notice.Message}
	})
}
