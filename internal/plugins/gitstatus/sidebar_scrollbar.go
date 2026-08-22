package gitstatus

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/ui"
)

// Which sidebar scrollbar a pointer interaction targets. The values ride in
// the hit region Data slot, so one pair of shared region IDs serves both bars.
const (
	scrollBarNone = iota
	scrollBarFiles
	scrollBarCommits
)

// sidebarBarSnapshot is the geometry a bar reported on its last render. Press
// handlers read it to translate screen rows into track rows; drags keep the
// snapshot taken at press time so mid-gesture re-renders cannot shift the
// mapping under the pointer.
type sidebarBarSnapshot struct {
	params ui.ScrollbarParams
	trackY int  // absolute Y of the track top, plugin coordinates
	valid  bool // false until the bar has rendered at least once
}

// sidebarScrollState carries the pointer state of the sidebar scrollbars.
type sidebarScrollState struct {
	hoverBar  int
	dragBar   int
	grabDelta int // track rows between pointer and thumb anchor
	dragRow   int // absolute Y of the pointer at press
	drag      sidebarBarSnapshot
	files     sidebarBarSnapshot
	commits   sidebarBarSnapshot
}

// sidebarContentX is where panel content starts: left border + padding.
const sidebarContentX = 2

// sidebarScrollbarStyle derives hover/drag emphasis for one bar from the
// shared core's state hooks.
func (p *Plugin) sidebarScrollbarStyle(bar int) ui.ScrollbarStyle {
	dragging := p.mouseHandler != nil &&
		p.mouseHandler.IsDragging() &&
		p.mouseHandler.DragRegion() == ui.RegionScrollbarThumb &&
		p.sidebarScroll.dragBar == bar
	hovering := !dragging && p.sidebarScroll.hoverBar == bar
	return ui.ScrollbarStyle{
		Thumb: ui.HandleStateFrom(hovering, dragging),
		Track: ui.HandleStateFrom(hovering, false),
	}
}

// renderFilesScrollbar renders the file-list bar and records its snapshot.
func (p *Plugin) renderFilesScrollbar(params ui.ScrollbarParams) (string, ui.Geometry) {
	p.sidebarScroll.files = sidebarBarSnapshot{params: params, trackY: sidebarFilesTopY, valid: true}
	return ui.RenderScrollbarWithState(params, p.sidebarScrollbarStyle(scrollBarFiles))
}

// sidebarFilesTopY is the plugin-space Y of the first file-section row:
// pane border plus the two header lines.
const sidebarFilesTopY = 3

// registerFilesScrollbarRegions registers the file bar's hit regions. Call
// after the file-entry regions exist so the reverse scan prefers the bar.
func (p *Plugin) registerFilesScrollbarRegions(params ui.ScrollbarParams, geom ui.Geometry, content string) {
	if !geom.HasThumb {
		p.sidebarScroll.files = sidebarBarSnapshot{}
		return
	}
	p.registerSidebarBarRegions(scrollBarFiles, p.sidebarScroll.files, geom, sidebarContentX+maxLineBlockWidth(content))
}

// registerCommitsScrollbarRegions registers the recent-commits bar after the
// commit-row regions.
func (p *Plugin) registerCommitsScrollbarRegions(params ui.ScrollbarParams, geom ui.Geometry, trackY int, content string) {
	if !geom.HasThumb {
		p.sidebarScroll.commits = sidebarBarSnapshot{}
		return
	}
	p.sidebarScroll.commits = sidebarBarSnapshot{params: params, trackY: trackY, valid: true}
	p.registerSidebarBarRegions(scrollBarCommits, p.sidebarScroll.commits, geom, sidebarContentX+maxLineBlockWidth(content))
}

func (p *Plugin) registerSidebarBarRegions(bar int, snap sidebarBarSnapshot, geom ui.Geometry, trackX int) {
	p.mouseHandler.HitMap.Add(ui.RegionScrollbarTrack, mouse.Rect{
		X: trackX, Y: snap.trackY, W: 1, H: snap.params.TrackHeight,
	}, bar)
	p.mouseHandler.HitMap.Add(ui.RegionScrollbarThumb, mouse.Rect{
		X: trackX, Y: snap.trackY + geom.ThumbRect.Min.Y, W: 1, H: geom.ThumbRect.Dy(),
	}, bar)
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

// handleScrollbarPress starts a thumb drag, or jumps to the clicked spot and
// continues as a drag anchored at the grab row (macOS track-click semantics).
func (p *Plugin) handleScrollbarPress(action mouse.MouseAction) (*Plugin, tea.Cmd) {
	bar, ok := action.Region.Data.(int)
	if !ok || !p.sidebarBarValid(bar) {
		return p, nil
	}
	snap := p.sidebarBarSnapshotFor(bar)
	row := action.Y - snap.trackY

	offset := p.sidebarOffsetFor(bar)
	grabDelta := row - ui.RowForOffset(snap.params, offset)
	if action.Region.ID == ui.RegionScrollbarTrack {
		// Jump-to-spot: the grabbed point becomes the thumb anchor, so a
		// continuing drag maps the pointer straight onto track rows.
		offset = ui.OffsetAtRow(snap.params, row)
		grabDelta = 0
		p.setSidebarOffset(bar, offset)
	}

	p.sidebarScroll.dragBar = bar
	p.sidebarScroll.grabDelta = grabDelta
	p.sidebarScroll.dragRow = action.Y
	p.sidebarScroll.drag = snap
	p.mouseHandler.StartDrag(action.X, action.Y, ui.RegionScrollbarThumb, offset)
	return p, nil
}

// handleScrollbarDrag applies the pressed bar's offset mapping for the pointer
// position, clamped by the shared core at both ends of the track.
func (p *Plugin) handleScrollbarDrag(action mouse.MouseAction) (*Plugin, tea.Cmd) {
	if p.mouseHandler.DragRegion() != ui.RegionScrollbarThumb || p.sidebarScroll.dragBar == scrollBarNone {
		return p, nil
	}
	bar := p.sidebarScroll.dragBar
	snap := p.sidebarScroll.drag
	row := action.Y - snap.trackY - p.sidebarScroll.grabDelta
	p.setSidebarOffset(bar, ui.OffsetAtRow(snap.params, row))
	return p, nil
}

// scrollbarDragEnded settles a finished or cancelled scrollbar gesture. The
// offsets are ephemeral view state; nothing is persisted.
func (p *Plugin) scrollbarDragEnded() {
	p.sidebarScroll.dragBar = scrollBarNone
	p.sidebarScroll.grabDelta = 0
}

// updateScrollbarHover records which bar (if any) is under the pointer.
func (p *Plugin) updateScrollbarHover(region *mouse.Region) {
	p.sidebarScroll.hoverBar = scrollBarNone
	if region == nil {
		return
	}
	if region.ID != ui.RegionScrollbarThumb && region.ID != ui.RegionScrollbarTrack {
		return
	}
	if bar, ok := region.Data.(int); ok {
		p.sidebarScroll.hoverBar = bar
	}
}

func (p *Plugin) sidebarBarValid(bar int) bool {
	switch bar {
	case scrollBarFiles:
		return p.sidebarScroll.files.valid
	case scrollBarCommits:
		return p.sidebarScroll.commits.valid
	}
	return false
}

func (p *Plugin) sidebarBarSnapshotFor(bar int) sidebarBarSnapshot {
	switch bar {
	case scrollBarFiles:
		return p.sidebarScroll.files
	case scrollBarCommits:
		return p.sidebarScroll.commits
	}
	return sidebarBarSnapshot{}
}

func (p *Plugin) sidebarOffsetFor(bar int) int {
	switch bar {
	case scrollBarFiles:
		return p.scrollOff
	case scrollBarCommits:
		return p.commitScrollOff
	}
	return 0
}

func (p *Plugin) setSidebarOffset(bar, offset int) {
	switch bar {
	case scrollBarFiles:
		p.scrollOff = offset
	case scrollBarCommits:
		p.commitScrollOff = offset
	}
}
