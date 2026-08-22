package workspace

import (
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/noteview"
	"github.com/marcus/sidecar/internal/paneframe"
	"github.com/marcus/sidecar/internal/ui"
)

// Scrollbar gestures on the project surface's note panes.
//
// noteview draws its own interactive bar but exposes a deliberately state-free
// seam, so this host owns the bookkeeping: it registers the bar's regions from
// what the last render drew, arms a press-time mapping when the button goes
// down, and applies that mapping for every motion until a release — or the
// first button-less motion that betrays a lost one — settles it. Nothing is
// persisted; scroll offsets are view state.

// noteScrollbarHit names the note pane whose bar a hit region belongs to. The
// payload, not the region ID alone, is what routes a press: several surfaces
// share the hit map and more than one of them can draw a bar.
type noteScrollbarHit struct {
	LeafID int
}

// noteBarGesture is one note pane's in-flight pointer gesture on its bar. The
// press-time params snapshot keeps a mid-gesture re-render — a live refresh, a
// resize — from shifting the mapping under the pointer; OffsetAtRow clamps
// past both ends of the track without ever ending anything.
type noteBarGesture struct {
	leafID    int
	params    ui.ScrollbarParams // renderer inputs at press time
	trackTopY int                // absolute row of the track top at press time
	grabDelta int                // rows between the pointer and the thumb's anchor row
	active    bool
}

// isNoteScrollbarDragID reports that a drag started in one of a note pane's
// bar regions, so its motion belongs to that gesture rather than to whatever
// the pointer is over now.
func isNoteScrollbarDragID(id string) bool {
	return id == regionNoteScrollbarThumb || id == regionNoteScrollbarTrack
}

func (p *Plugin) noteViewByLeaf(leafID int) *noteview.Model {
	note := p.notes[leafID]
	if note == nil {
		return nil
	}
	return note.view()
}

// registerNoteScrollbarHits puts a note pane's bar regions in the hit map. It
// runs from the frame's Body pass — after every frame-owned region — so the
// bar wins HitMap.Test's reverse scan over the leaf drawn under it without
// outranking tabs or the close button. A pane whose content fits registers
// nothing: the reserved column is an anti-jitter spacer, not a control.
func (p *Plugin) registerNoteScrollbarHits(leafID int, inner paneframe.Box) {
	view := p.noteViewByLeaf(leafID)
	if view == nil || !view.HasScrollbar() || inner.H <= paneframe.HeaderRows {
		return
	}
	params := view.ScrollbarParams()
	_, geom := ui.RenderScrollbarWithGeometry(params)
	if !geom.HasThumb || geom.TrackRect.Dy() <= 0 {
		return
	}
	barX := inner.X + inner.W - 2 // the card pads one column either side of its bar
	top := inner.Y + paneframe.HeaderRows
	hit := noteScrollbarHit{LeafID: leafID}
	p.mouseHandler.HitMap.Add(regionNoteScrollbarTrack, mouse.Rect{X: barX, Y: top, W: 1, H: geom.TrackRect.Dy()}, hit)
	// The thumb is added after the track so the reverse scan hands a press on
	// their overlap to the thumb, exactly as the shared geometry orders them.
	p.mouseHandler.HitMap.Add(regionNoteScrollbarThumb, mouse.Rect{X: barX, Y: top + geom.ThumbRect.Min.Y, W: 1, H: geom.ThumbRect.Dy()}, hit)
}

// pressNoteScrollbar begins a note pane's bar gesture: grabbing the thumb where
// it was pressed, or jumping to the clicked spot anchored there so the same
// gesture keeps dragging (macOS track-click). The regions are what the last
// render reported, so the pointer maps onto what was actually drawn.
func (p *Plugin) pressNoteScrollbar(leafID int, action mouse.MouseAction) {
	view := p.noteViewByLeaf(leafID)
	if view == nil || action.Region == nil {
		return
	}
	params := view.ScrollbarParams()
	_, geom := ui.RenderScrollbarWithGeometry(params)
	if !geom.HasThumb {
		return
	}
	offset := view.ScrollOffset()
	trackTop := action.Region.Rect.Y
	grabDelta := 0
	if action.Region.ID == regionNoteScrollbarThumb {
		trackTop -= geom.ThumbRect.Min.Y
		grabDelta = action.Y - trackTop - ui.RowForOffset(params, offset)
	} else {
		// Track press: jump-to-spot, anchored at the grabbed row.
		offset = view.OffsetAtTrackRow(action.Y - trackTop)
		view.ScrollToOffset(offset)
	}
	p.noteBar = noteBarGesture{
		leafID:    leafID,
		params:    params,
		trackTopY: trackTop,
		grabDelta: grabDelta,
		active:    true,
	}
	p.mouseHandler.StartDrag(action.X, action.Y, action.Region.ID, leafID)
}

// dragNoteScrollbar applies a held gesture's mapping for one pointer row. Only
// a live gesture answers; wherever the pointer has since travelled, the pane
// it started in is the one that scrolls.
func (p *Plugin) dragNoteScrollbar(y int) {
	if !p.noteBar.active {
		return
	}
	if view := p.noteViewByLeaf(p.noteBar.leafID); view != nil {
		row := y - p.noteBar.trackTopY - p.noteBar.grabDelta
		view.ScrollToOffset(ui.OffsetAtRow(p.noteBar.params, row))
	}
}

// finishNoteScrollbarDrag settles a finished or cancelled gesture. Offsets hold
// where the pointer left them; nothing is persisted.
func (p *Plugin) finishNoteScrollbarDrag() { p.noteBar = noteBarGesture{} }
