package modal

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/scroll"
	"github.com/marcus/sidecar/internal/styles"
)

// Region IDs for the interactive scrollbars a modal can carry. They mirror
// ui.RegionScrollbarThumb/Track; internal/ui imports this package (the overlay
// helpers), so sharing them by import would cycle. Each surface owns its own
// HitMap, so the two spellings never meet in one map.
const (
	RegionScrollbarThumb = "scrollbar-thumb"
	RegionScrollbarTrack = "scrollbar-track"
)

// SectionScrollbar declares the interactive scrollbar a section drew into its
// own content, at LocalX columns from the section's left edge. Declaring it is
// what makes the library place the hit regions (last, so they beat the content
// they overlap) and report their ownership back through SectionBarAt.
//
// Rendering stays the section's job — the string it drew remains the single
// source of what its bar looks like. Gesture answering is the declaring
// surface's too, via SectionBarAt from its own mouse handler; the framework
// only absorbs the presses so they can never fall through to a list row
// underneath. This split exists because the app Model is passed by value per
// update: routed callbacks would mutate a stale copy, while the owning
// handler runs on the copy that survives.
type SectionScrollbar struct {
	TotalItems   int
	ScrollOffset int
	VisibleItems int
	TrackHeight  int
	LocalX       int // column within the section content where the bar sits
}

// placedBar is one interactive bar this modal rendered and registered: either
// the framework's own viewport bar (section == -1) or a section-declared one.
// The slice is rebuilt every Render; hit-region Data indexes into it, so an
// event is always answered against the geometry that was on screen.
type placedBar struct {
	has            bool
	section        int // index into sections; -1 for the modal viewport bar
	declared       SectionScrollbar
	total          int // content rows behind a viewport bar
	visible        int // viewport rows the bar was sized against
	trackH         int // drawn track height in rows
	trackX, trackY int // absolute; trackY is the unclipped top row
	thumbTop       int // thumb top within the track
	thumbH         int
}

// barGesture snapshots everything a live viewport-bar drag needs, taken at
// press time. Like every other surface's scrollbar gesture, re-renders cannot
// shift the mapping under the pointer mid-drag.
type barGesture struct {
	total, visible, trackH int
	trackY                 int
	grabDelta              int // track rows between pointer and thumb anchor
	onThumb                bool
}

// Hover/drag emphasis derives from the theme palette via intensity modulation,
// mirroring internal/ui's scrollbar derivation; the constants are duplicated
// because importing ui would cycle.
const (
	barHoverLighten = 0.20
	barDragLighten  = 0.35
)

// renderViewportBar renders the framework scrollbar for the body viewport with
// its current pointer emphasis. Idle output stays byte-identical to the
// draw-only renderer this replaces.
func (m *Modal) renderViewportBar(handler *mouse.Handler, totalItems, scrollOffset, trackHeight int) (string, placedBar) {
	if trackHeight < 1 || totalItems <= trackHeight {
		return "", placedBar{}
	}

	loc := scroll.ThumbLocFor(totalItems, scrollOffset, trackHeight, trackHeight)
	if !loc.Has {
		return "", placedBar{}
	}

	dragging := m.press != nil && handler != nil && handler.IsDragging()
	hovering := !dragging && m.barHover

	theme := styles.GetCurrentTheme()
	trackChar := barPartStyle(styles.ScrollbarTrackColor, theme.Colors.ScrollbarTrack, hovering, dragging).Render("│")
	thumbChar := barPartStyle(styles.ScrollbarThumbColor, theme.Colors.ScrollbarThumb, hovering, dragging).Render("┃")

	lines := make([]string, trackHeight)
	for i := range trackHeight {
		if i >= loc.Pos && i < loc.Pos+loc.Size {
			lines[i] = thumbChar
		} else {
			lines[i] = trackChar
		}
	}

	return strings.Join(lines, "\n"), placedBar{
		has:      true,
		section:  -1,
		total:    totalItems,
		visible:  trackHeight,
		trackH:   trackHeight,
		thumbTop: loc.Pos,
		thumbH:   loc.Size,
	}
}

// barPartStyle derives hover/drag emphasis from the theme palette with the
// same intensity modulation ui applies to its scrollbars.
func barPartStyle(idle color.Color, baseHex string, hovering, dragging bool) lipgloss.Style {
	fg := idle
	switch {
	case dragging && baseHex != "":
		fg = lipgloss.Color(styles.Lighten(baseHex, barDragLighten))
	case hovering && baseHex != "":
		fg = lipgloss.Color(styles.Lighten(baseHex, barHoverLighten))
	}
	return lipgloss.NewStyle().Foreground(fg).Background(styles.BgSecondary)
}

// registerBars places every interactive bar this build rendered into absolute
// coordinates and adds its hit regions last, after all content, form-field,
// and overlay regions — HitMap.Test scans reverse, so a scrollbar press must
// never fall through to a row underneath. Bars outside the viewport register
// nothing, and neither does a bar whose content fits: HasThumb semantics keep
// spacer columns inert under the pointer.
func (m *Modal) registerBars(handler *mouse.Handler, viewport placedBar, visible []renderedSection, contentX, contentY, contentWidth, viewportTop, viewportH int) {
	m.bars = m.bars[:0]
	if handler == nil {
		return
	}
	hm := handler.HitMap
	viewportBottom := viewportTop + viewportH

	add := func(bar placedBar) bool {
		top := max(bar.trackY, viewportTop)
		bottom := min(bar.trackY+bar.trackH, viewportBottom)
		if bottom <= top {
			return false
		}
		idx := len(m.bars)
		hm.AddRect(RegionScrollbarTrack, bar.trackX, top, 1, bottom-top, idx)
		tTop := max(bar.trackY+bar.thumbTop, top)
		tBottom := min(bar.trackY+bar.thumbTop+bar.thumbH, bottom)
		if tBottom > tTop {
			hm.AddRect(RegionScrollbarThumb, bar.trackX, tTop, 1, tBottom-tTop, idx)
		}
		m.bars = append(m.bars, bar)
		return true
	}

	if viewport.has {
		vp := viewport
		// The bar renders in the last content column: buildLayout normalizes
		// the viewport to contentWidth-1 before joining, so the glyph lands
		// here and the region sits on it by construction.
		vp.trackX = contentX + contentWidth - 1
		vp.trackY = viewportTop
		add(vp)
	}

	sectionY := 0
	for _, r := range visible {
		if sb := r.scrollbar; sb != nil && r.section >= 0 && sb.TrackHeight > 0 {
			loc := scroll.ThumbLocFor(sb.TotalItems, sb.ScrollOffset, sb.VisibleItems, sb.TrackHeight)
			if loc.Has {
				add(placedBar{
					has:      true,
					section:  r.section,
					declared: *sb,
					visible:  sb.VisibleItems,
					trackH:   sb.TrackHeight,
					trackX:   contentX + sb.LocalX,
					trackY:   contentY + sectionY - m.scrollOffset,
					thumbTop: loc.Pos,
					thumbH:   loc.Size,
				})
			}
		}
		sectionY += r.height
	}
}

// barIndexAt resolves which placed bar a hit region belongs to, if any.
func (m *Modal) barIndexAt(region *mouse.Region) (int, bool) {
	if region == nil {
		return 0, false
	}
	idx, ok := region.Data.(int)
	if !ok || idx < 0 || idx >= len(m.bars) {
		return 0, false
	}
	if region.ID != RegionScrollbarThumb && region.ID != RegionScrollbarTrack {
		return 0, false
	}
	return idx, true
}

// SectionBarAt reports the section-declared scrollbar under (x, y), if the
// point lands on one of its regions: the declaration the owner drew it with,
// the absolute position of the track's unclipped top row (so a press anchors
// correctly even when part of the bar is scrolled out of the viewport), and
// whether the point is on the thumb. ok is false for the framework's own
// viewport bar, empty space, and every non-scrollbar region — a host's mouse
// handler uses this to claim exactly its bar's events before anything else
// sees them.
func (m *Modal) SectionBarAt(x, y int, handler *mouse.Handler) (declared SectionScrollbar, trackX, trackY int, onThumb bool, ok bool) {
	if handler == nil {
		return SectionScrollbar{}, 0, 0, false, false
	}
	region := handler.HitMap.Test(x, y)
	idx, ok := m.barIndexAt(region)
	if !ok || m.bars[idx].section < 0 {
		return SectionScrollbar{}, 0, 0, false, false
	}
	bar := m.bars[idx]
	return bar.declared, bar.trackX, bar.trackY,
		region.ID == RegionScrollbarThumb, true
}

// handleViewportBarPress answers a press on the framework's own viewport bar.
// Pressing the thumb grabs it at the pressed row; pressing the track jumps the
// thumb so its top anchors at the grabbed row and the same gesture continues
// from there (macOS jump-to-spot). Either way the press ends in StartDrag, so
// releasing anywhere settles cleanly.
func (m *Modal) handleViewportBarPress(action mouse.MouseAction, handler *mouse.Handler) {
	idx, ok := m.barIndexAt(action.Region)
	if !ok || m.bars[idx].section >= 0 {
		return
	}
	bar := m.bars[idx]
	g := &barGesture{
		total:   bar.total,
		visible: bar.visible,
		trackH:  bar.trackH,
		trackY:  bar.trackY,
		onThumb: action.Region.ID == RegionScrollbarThumb,
	}

	row := action.Y - bar.trackY
	if g.onThumb {
		g.grabDelta = row - scroll.RowForOffset(bar.total, bar.visible, bar.trackH, m.scrollOffset)
	} else {
		// Jump-to-spot: the grabbed point becomes the thumb anchor, so a
		// continuing drag maps the pointer straight onto track rows.
		m.scrollOffset = scroll.OffsetAtRow(bar.total, bar.visible, bar.trackH, row)
	}
	handler.StartDrag(action.X, action.Y, action.Region.ID, m.scrollOffset)
	m.press = g
}

// handleViewportBarMotion applies the live gesture's offset mapping.
// OffsetAtRow clamps past both ends of the track without ending the gesture.
func (m *Modal) handleViewportBarMotion(y int) {
	if m.press == nil {
		return
	}
	m.scrollOffset = scroll.OffsetAtRow(
		m.press.total, m.press.visible, m.press.trackH,
		y-m.press.trackY-m.press.grabDelta,
	)
}

// endViewportBarGesture settles the finished or cancelled gesture wherever the
// pointer is. The scroll offset is view state; nothing persists.
func (m *Modal) endViewportBarGesture() { m.press = nil }

// isViewportBarRegion reports whether a hit region belongs to the framework's
// own viewport bar rather than to a section-declared one. Wheel events over
// the viewport bar scroll the body, exactly as they did back when the column
// had no regions of its own.
func (m *Modal) isViewportBarRegion(region *mouse.Region) bool {
	idx, ok := m.barIndexAt(region)
	return ok && m.bars[idx].section < 0
}
