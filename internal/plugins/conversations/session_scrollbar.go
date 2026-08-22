package conversations

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/ui"
)

// listBarSnapshot is the geometry the session-list bar reported on its last
// render, in plugin coordinates. Press handlers translate screen rows into
// track rows with the latest snapshot; drags keep the press-time snapshot so
// re-renders cannot shift the mapping under the pointer.
type listBarSnapshot struct {
	params   ui.ScrollbarParams
	trackX   int  // absolute X of the one-column track
	trackY   int  // absolute Y of the track top
	thumbTop int  // thumb top row within the track
	thumbH   int  // thumb height in rows
	has      bool // false when everything fits and no regions exist
}

// listScrollState carries the pointer state of the session-list scrollbar.
type listScrollState struct {
	hovering  bool
	grabDelta int             // track rows between pointer and thumb anchor
	dragging  listBarSnapshot // snapshot taken at press time
	bar       listBarSnapshot // latest render's snapshot
}

// listContentX is where panel content starts: left border + padding.
const listContentX = 2

// renderSessionScrollbar renders the session-list bar and records where it
// landed relative to the just-built session rows.
func (p *Plugin) renderSessionScrollbar(params ui.ScrollbarParams, trackY int, sessionContent string) string {
	rendered, geom := ui.RenderScrollbarWithState(params, p.sessionScrollbarStyle())
	bar := p.listScroll.bar
	bar.params, bar.has = params, geom.HasThumb
	if geom.HasThumb {
		bar.trackY = trackY
		bar.trackX = listContentX + maxLineBlockWidth(sessionContent)
		bar.thumbTop = geom.ThumbRect.Min.Y
		bar.thumbH = geom.ThumbRect.Dy()
	}
	p.listScroll.bar = bar
	return rendered
}

// sessionScrollbarStyle derives hover/drag emphasis from the shared core's
// state hooks.
func (p *Plugin) sessionScrollbarStyle() ui.ScrollbarStyle {
	dragging := p.mouseHandler != nil &&
		p.mouseHandler.IsDragging() &&
		p.mouseHandler.DragRegion() == ui.RegionScrollbarThumb
	hovering := !dragging && p.listScroll.hovering
	return ui.ScrollbarStyle{
		Thumb: ui.HandleStateFrom(hovering, dragging),
		Track: ui.HandleStateFrom(hovering, false),
	}
}

// registerListScrollbarRegion registers the thumb/track hit regions. Call
// after the session-row regions so the reverse scan prefers the bar and a
// scrollbar press can never select a session underneath.
func (p *Plugin) registerListScrollbarRegion() {
	bar := p.listScroll.bar
	if !bar.has || p.filterMode || len(p.visibleSessions()) == 0 {
		return
	}
	p.mouseHandler.HitMap.Add(ui.RegionScrollbarTrack, mouse.Rect{
		X: bar.trackX, Y: bar.trackY, W: 1, H: bar.params.TrackHeight,
	}, nil)
	p.mouseHandler.HitMap.Add(ui.RegionScrollbarThumb, mouse.Rect{
		X: bar.trackX, Y: bar.trackY + bar.thumbTop, W: 1, H: bar.thumbH,
	}, nil)
}

// maxLineBlockWidth returns the visual width of the widest line in s, which is
// where JoinHorizontal places the scrollbar column beside it.
func maxLineBlockWidth(s string) int {
	width := 0
	for _, line := range strings.Split(s, "\n") {
		if w := lipgloss.Width(line); w > width {
			width = w
		}
	}
	return width
}

// handleListScrollbarPress starts a thumb drag, or jumps to the clicked spot
// and continues as a drag anchored at the grab row (macOS track-click).
func (p *Plugin) handleListScrollbarPress(action mouse.MouseAction) (*Plugin, tea.Cmd) {
	bar := p.listScroll.bar
	if !bar.has {
		return p, nil
	}
	row := action.Y - bar.trackY
	offset := p.scrollOff
	grabDelta := row - ui.RowForOffset(bar.params, offset)
	if action.Region.ID == ui.RegionScrollbarTrack {
		// Jump-to-spot: the grabbed point becomes the thumb anchor, so a
		// continuing drag maps the pointer straight onto track rows.
		offset = ui.OffsetAtRow(bar.params, row)
		grabDelta = 0
		p.scrollOff = offset
	}
	p.listScroll.grabDelta = grabDelta
	p.listScroll.dragging = bar
	p.mouseHandler.StartDrag(action.X, action.Y, ui.RegionScrollbarThumb, offset)
	return p, nil
}

// handleListScrollbarDrag applies the offset mapping for the pointer position,
// clamped by the shared core at both ends of the track.
func (p *Plugin) handleListScrollbarDrag(action mouse.MouseAction) (*Plugin, tea.Cmd) {
	if p.mouseHandler.DragRegion() != ui.RegionScrollbarThumb {
		return p, nil
	}
	bar := p.listScroll.dragging
	row := action.Y - bar.trackY - p.listScroll.grabDelta
	p.scrollOff = ui.OffsetAtRow(bar.params, row)
	return p, nil
}

// listScrollbarDragEnded settles a finished or cancelled gesture. The scroll
// offset is ephemeral view state; nothing is persisted.
func (p *Plugin) listScrollbarDragEnded() {
	p.listScroll.dragging = listBarSnapshot{}
	p.listScroll.grabDelta = 0
}

// updateListScrollbarHover records whether the pointer is over the bar.
func (p *Plugin) updateListScrollbarHover(region *mouse.Region) {
	p.listScroll.hovering = region != nil &&
		(region.ID == ui.RegionScrollbarThumb || region.ID == ui.RegionScrollbarTrack)
}
