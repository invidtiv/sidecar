package kanban

import (
	"github.com/marcus/sidecar/internal/ui"
)

// Interactive per-lane scrollbars adopting the shared core in internal/ui
// (docs/plans/active/mouse-draggable-scrollbars.md). One board paints up to N
// bars — one per lane column — and they all coexist in one HitMap. Namespacing
// rides the existing HitRegion convention: the lane index travels in the
// region's Column field, and the shared ui region IDs name the part, so lane
// 3's thumb drag can never move lane 1.
//
// Render emits each lane's thumb/track regions after every card region, so a
// consumer that registers RenderResult.Regions in order gets the reverse-scan
// priority the bars need over the cards they overlap. Lanes that fit without
// a thumb report HasThumb=false and register nothing: their reserved column
// is the anti-jitter spacer, not a control.
//
// The gesture lives on the component because the component owns the scroll
// offsets and the per-lane render snapshots the mapping needs. Hosts wire it
// the way every other surface wires its bars: on a press inside a scrollbar
// region call PressScrollbar and StartDrag when it reports true, feed
// ActionDrag rows to DragScrollbar, and settle with ReleaseScrollbar. Nothing
// is persisted; scroll offsets are ephemeral view state.

const (
	RegionScrollbarThumb RegionKind = "scrollbar-thumb"
	RegionScrollbarTrack RegionKind = "scrollbar-track"
)

// scrollbarBar is what one render pass learned about one lane's bar: the
// params it was drawn with, the screen row its track starts on, and whether
// an interactive thumb exists. Anything not drawn this pass is left invalid,
// so a stale or spacer-only bar can never begin a gesture.
type scrollbarBar struct {
	params    ui.ScrollbarParams
	trackTopY int
	valid     bool
}

// scrollbarGesture is the press-time snapshot of an in-flight drag. Drags map
// through the params taken when the button went down, so a mid-gesture
// refresh or resize cannot shift the mapping under the pointer. The lane is
// held by ID, matching the scroll map, rather than by index.
type scrollbarGesture struct {
	active    bool
	lane      LaneID
	grabRow   int                // track rows between the pointer and the thumb anchor
	params    ui.ScrollbarParams // renderer inputs at press time
	trackTopY int                // absolute Y of the track top at press time
}

// PressScrollbar begins a lane-bar gesture from a hit-tested region and the
// pointer's screen Y. Pressing the thumb grabs it at the pressed row;
// pressing the track jumps so the thumb top anchors at the grabbed row
// (macOS jump-to-spot) and the same gesture continues from there. Either way
// the caller should StartDrag on true, so releasing anywhere settles cleanly.
// Returns false for anything that is not a live lane bar — including the
// fits-without-thumb spacer column — and changes nothing when it does.
func (c *Component) PressScrollbar(region HitRegion, y int) bool {
	if region.Kind != RegionScrollbarThumb && region.Kind != RegionScrollbarTrack {
		return false
	}
	if region.Column < 0 || region.Column >= len(c.board.Lanes) || region.Column >= len(c.bars) {
		return false
	}
	bar := c.bars[region.Column]
	if !bar.valid {
		return false
	}

	row := y - bar.trackTopY
	grabRow := row - ui.RowForOffset(bar.params, bar.params.ScrollOffset)
	if region.Kind == RegionScrollbarTrack {
		offset := ui.OffsetAtRow(bar.params, row)
		c.setLaneScroll(c.board.Lanes[region.Column].ID, offset)
		bar.params.ScrollOffset = c.scroll[c.board.Lanes[region.Column].ID]
		c.pinSelectionToViewport(c.board.Lanes[region.Column].ID, bar.params.VisibleItems)
		grabRow = 0
	}

	c.barDrag = scrollbarGesture{
		active:    true,
		lane:      c.board.Lanes[region.Column].ID,
		grabRow:   grabRow,
		params:    bar.params,
		trackTopY: bar.trackTopY,
	}
	return true
}

// DragScrollbar maps the pointer row back onto the dragged lane's scroll
// offset through the shared inverse mapping, preserving where within the
// thumb the gesture grabbed. OffsetAtRow clamps past both ends of the track
// without ending the gesture. Reports whether the offset moved.
func (c *Component) DragScrollbar(y int) bool {
	if !c.barDrag.active {
		return false
	}
	column := c.laneColumn(c.barDrag.lane)
	if column < 0 {
		// The grabbed lane vanished underneath the gesture (board refresh);
		// settle rather than drag a lane that no longer exists.
		c.ReleaseScrollbar()
		return false
	}
	id := c.board.Lanes[column].ID
	before := c.scroll[id]
	c.setLaneScroll(id, ui.OffsetAtRow(c.barDrag.params, y-c.barDrag.trackTopY-c.barDrag.grabRow))
	c.pinSelectionToViewport(id, c.barDrag.params.VisibleItems)
	return c.scroll[id] != before
}

// ReleaseScrollbar settles a finished or cancelled gesture. Offsets hold
// where the pointer left them; nothing is persisted.
func (c *Component) ReleaseScrollbar() { c.barDrag = scrollbarGesture{} }

// DraggingScrollbar reports whether a lane-bar gesture is live.
func (c *Component) DraggingScrollbar() bool { return c.barDrag.active }

// scrollbarStyle derives one lane's hover/drag emphasis from the shared
// core's state hooks. Idle is the zero style, which renders byte-identically
// to plain RenderScrollbar.
func (c *Component) scrollbarStyle(column int) ui.ScrollbarStyle {
	dragging := c.barDrag.active && c.barDrag.lane == c.board.Lanes[column].ID
	hovering := !dragging && c.barHover == c.board.Lanes[column].ID
	return ui.ScrollbarStyle{
		Thumb: ui.HandleStateFrom(hovering, dragging),
		Track: ui.HandleStateFrom(hovering, false),
	}
}

// registerScrollbarRegion appends one lane's track and thumb hit regions.
// Callers emit these after all card regions so HitMap.Test's reverse scan
// lets the bar win the column it overlaps; track precedes thumb so the thumb
// wins where the two rects meet.
func registerScrollbarRegion(regions []HitRegion, column int, barX, trackTopY, contentRows int, geom ui.Geometry) []HitRegion {
	regions = append(regions,
		HitRegion{Kind: RegionScrollbarTrack, Column: column, Row: -1, X: barX, Y: trackTopY, W: 1, H: contentRows},
		HitRegion{Kind: RegionScrollbarThumb, Column: column, Row: -1, X: barX, Y: trackTopY + geom.ThumbRect.Min.Y, W: 1, H: geom.ThumbRect.Dy()},
	)
	return regions
}

// setLaneScroll assigns a clamped scroll offset to one lane, using the same
// bound clampScroll holds everywhere else.
func (c *Component) setLaneScroll(id LaneID, offset int) {
	column := c.laneColumn(id)
	if column < 0 {
		return
	}
	last := max(0, len(c.board.Lanes[column].Cards)-1)
	c.scroll[id] = min(max(offset, 0), last)
}

// pinSelectionToViewport keeps the selected card of a scrolled lane inside the
// lane's viewport, so the next render's ensureSelectedVisible has nothing to
// undo. It is the same selection-follows-viewport rule wheel scrolling in
// MoveInColumn already follows, applied to a scrollbar-driven viewport, and it
// only ever moves the dragged lane's own selection — never another lane's.
func (c *Component) pinSelectionToViewport(id LaneID, visibleItems int) {
	column := c.laneColumn(id)
	if column < 0 || c.selection.Column != column {
		return
	}
	last := max(0, len(c.board.Lanes[column].Cards)-1)
	row := min(max(c.selection.Row, c.scroll[id]), c.scroll[id]+max(0, visibleItems-1))
	c.selection.Row = min(max(row, 0), last)
}

// laneColumn resolves a lane ID to its current index, or -1 when the board no
// longer has it.
func (c *Component) laneColumn(id LaneID) int {
	for column := range c.board.Lanes {
		if c.board.Lanes[column].ID == id {
			return column
		}
	}
	return -1
}
