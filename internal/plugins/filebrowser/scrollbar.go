package filebrowser

import (
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/ui"
)

// Interactive scrollbars for the three scrollable views — tree, search
// results, preview — adopting the shared core in internal/ui. Rendering stays
// a string; this file adds the pointer half: geometry caching during render,
// hit-region registration after content regions, and the press/drag/hover
// gesture contract from docs/plans/active/mouse-draggable-scrollbars.md.

// scrollbarView identifies which scrollable view a bar belongs to. The zero
// value means "no bar", so the plugin's pointer state needs no initialization.
type scrollbarView int

const (
	sbNone scrollbarView = iota
	sbTree
	sbSearch
	sbPreview
	sbViewCount
)

// scrollbarBar is what one render pass learned about one view's bar: the
// params it was drawn with and the geometry to register hit regions from.
// Anything not drawn this pass is left zero, and a zero Geometry reports
// HasThumb=false, so stale bars can never register regions.
type scrollbarBar struct {
	params ui.ScrollbarParams
	geom   ui.Geometry
}

// scrollbarPaneInset is the cells between a panel's outer edge and its content
// column: one border plus one padding cell, matching RenderPanel.
const scrollbarPaneInset = 2

// drawScrollbar renders a view's bar with its current pointer emphasis and
// records what this pass drew, for hit registration. Idle output is identical
// to plain RenderScrollbar.
func (p *Plugin) drawScrollbar(view scrollbarView, total, offset, visible int) string {
	params := ui.ScrollbarParams{
		TotalItems:   total,
		ScrollOffset: offset,
		VisibleItems: visible,
		TrackHeight:  visible,
	}
	state := ui.HandleStateFrom(p.hoverScrollbar == view, p.dragScrollbar == view)
	rendered, geom := ui.RenderScrollbarWithState(params, ui.ScrollbarStyle{Thumb: state, Track: state})
	p.bars[view] = scrollbarBar{params: params, geom: geom}
	return rendered
}

// clearTreeColumnBars forgets the tree-pane column's bars at the top of a tree
// pane render. Only one of them is ever drawn per pass; clearing both means a
// bar that is not drawn cannot linger into registration.
func (p *Plugin) clearTreeColumnBars() {
	p.bars[sbTree] = scrollbarBar{}
	p.bars[sbSearch] = scrollbarBar{}
}

// treeScrollbarX is the screen column the tree/search bar occupies: the panel
// inset plus the padded text column, i.e. treeWidth-3 for any real width.
func (p *Plugin) treeScrollbarX() int {
	return scrollbarPaneInset + treeNodeWidth(p.treeWidth)
}

// previewScrollbarX is the screen column the preview bar occupies, in both the
// two-pane and collapsed-tree layouts.
func (p *Plugin) previewScrollbarX() int {
	panelX := 0
	if p.treeVisible {
		panelX = p.treeWidth + dividerWidth
	}
	return panelX + scrollbarPaneInset + p.previewContentWidth()
}

// scrollbarTopY is the screen row of every bar's first track row. All three
// views begin their scrolled content three rows below the panes' top edge:
// one border plus two header rows.
func (p *Plugin) scrollbarTopY() int {
	return p.inputBarHeight() + 3
}

// registerScrollbarRegions registers thumb/track rects for every bar drawn
// this pass. Call it after ALL content regions: HitMap.Test scans reverse, so
// the bar must be registered last to win the column it overlaps — a tree row's
// hit rect and the preview line rects both reach into the bar's column, and a
// scrollbar press must never select a row or start a text selection beneath
// it. Nothing is registered when a view fits without a thumb.
func (p *Plugin) registerScrollbarRegions() {
	hm := p.mouseHandler.HitMap
	topY := p.scrollbarTopY()
	for view := sbTree; view < sbViewCount; view++ {
		bar := p.bars[view]
		if !bar.geom.HasThumb {
			continue
		}
		x := p.treeScrollbarX()
		if view == sbPreview {
			x = p.previewScrollbarX()
		}
		hm.AddRect(ui.RegionScrollbarTrack, x, topY, 1, bar.geom.TrackRect.Dy(), view)
		hm.AddRect(ui.RegionScrollbarThumb, x, topY+bar.geom.ThumbRect.Min.Y, 1, bar.geom.ThumbRect.Dy(), view)
	}
}

// liveScrollbarParams rebuilds a view's scrollbar inputs from current state so
// a gesture survives content changing underneath it (a watcher rebuild, a
// resize). ok is false for unknown views and a missing tree.
func (p *Plugin) liveScrollbarParams(view scrollbarView) (ui.ScrollbarParams, bool) {
	switch view {
	case sbTree:
		if p.tree == nil {
			return ui.ScrollbarParams{}, false
		}
		v := p.treeItemRows()
		return ui.ScrollbarParams{
			TotalItems:   p.tree.Len(),
			ScrollOffset: p.treeScrollOff,
			VisibleItems: v,
			TrackHeight:  v,
		}, true
	case sbSearch:
		v := p.treeItemRows()
		return ui.ScrollbarParams{
			TotalItems:   len(p.searchMatches),
			ScrollOffset: p.effectiveSearchScrollOff(v),
			VisibleItems: v,
			TrackHeight:  v,
		}, true
	case sbPreview:
		lines := p.getPreviewLines()
		v := p.previewSourceRowCapacity()
		return ui.ScrollbarParams{
			TotalItems:   len(lines),
			ScrollOffset: p.previewScroll,
			VisibleItems: v,
			TrackHeight:  v,
		}, true
	default:
		return ui.ScrollbarParams{}, false
	}
}

// effectiveSearchScrollOff is the offset the search results render at: the
// manual position a scrollbar gesture set, or — before anyone has touched the
// bar — the cursor-anchored derivation the view always used.
func (p *Plugin) effectiveSearchScrollOff(visible int) int {
	n := len(p.searchMatches)
	maxOff := n - visible
	if maxOff < 0 {
		return 0
	}
	if p.searchScrollOff >= 0 {
		return min(p.searchScrollOff, maxOff)
	}
	off := 0
	if p.searchCursor >= visible {
		off = p.searchCursor - visible + 1
	}
	return min(off, maxOff)
}

// followSearchCursor resumes cursor-anchored scrolling after keyboard
// movement, abandoning whatever position a scrollbar gesture left.
func (p *Plugin) followSearchCursor() {
	p.searchScrollOff = -1
}

// scrollbarOffset reads a view's current scroll offset.
func (p *Plugin) scrollbarOffset(view scrollbarView) int {
	switch view {
	case sbTree:
		return p.treeScrollOff
	case sbSearch:
		return p.effectiveSearchScrollOff(p.treeItemRows())
	case sbPreview:
		return p.previewScroll
	}
	return 0
}

// setScrollbarOffset assigns a clamped scroll offset to a view.
func (p *Plugin) setScrollbarOffset(view scrollbarView, off int) {
	switch view {
	case sbTree:
		p.treeScrollOff = off
		p.clampTreeScroll()
	case sbSearch:
		maxOff := len(p.searchMatches) - p.treeItemRows()
		if maxOff < 0 {
			maxOff = 0
		}
		p.searchScrollOff = min(max(off, 0), maxOff)
	case sbPreview:
		maxOff := len(p.getPreviewLines()) - p.previewSourceRowCapacity()
		if maxOff < 0 {
			maxOff = 0
		}
		p.previewScroll = min(max(off, 0), maxOff)
	}
}

// handleScrollbarPress begins a scrollbar gesture. Pressing the thumb grabs it
// at the pressed row; pressing the track jumps the thumb so its top anchors at
// the grabbed row and the same gesture continues from there. Either way the
// press ends in StartDrag, so releasing anywhere settles cleanly.
func (p *Plugin) handleScrollbarPress(action mouse.MouseAction) (*Plugin, tea.Cmd) {
	view, ok := action.Region.Data.(scrollbarView)
	if !ok || view <= sbNone || view >= sbViewCount {
		return p, nil
	}
	params, ok := p.liveScrollbarParams(view)
	if !ok {
		return p, nil
	}

	localRow := action.Y - p.scrollbarTopY()
	if action.Region.ID == ui.RegionScrollbarThumb {
		p.scrollbarGrabRow = localRow - ui.RowForOffset(params, params.ScrollOffset)
	} else {
		p.scrollbarGrabRow = 0
		p.setScrollbarOffset(view, ui.OffsetAtRow(params, localRow))
	}

	p.mouseHandler.StartDrag(action.X, action.Y, action.Region.ID, p.scrollbarOffset(view))
	p.dragScrollbar = view
	return p, nil
}

// handleScrollbarDrag maps the pointer row back onto the dragged view's
// scroll offset through the shared inverse mapping, preserving where within
// the thumb the gesture grabbed. OffsetAtRow clamps past both ends without
// ending the gesture.
func (p *Plugin) handleScrollbarDrag(action mouse.MouseAction) (*Plugin, tea.Cmd) {
	view := p.dragScrollbar
	if view == sbNone {
		return p, nil
	}
	params, ok := p.liveScrollbarParams(view)
	if !ok {
		return p, nil
	}
	localRow := action.Y - p.scrollbarTopY()
	p.setScrollbarOffset(view, ui.OffsetAtRow(params, localRow-p.scrollbarGrabRow))
	return p, nil
}

// scrollbarRegionIsTreeSide reports whether a scrollbar region belongs to one
// of the two bars in the tree pane's column, so wheel routing keeps treating
// that column as the tree pane.
func scrollbarRegionIsTreeSide(r *mouse.Region) bool {
	if r == nil {
		return false
	}
	view, ok := r.Data.(scrollbarView)
	return ok && view > sbNone && view < sbPreview
}
