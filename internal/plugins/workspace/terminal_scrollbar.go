package workspace

import (
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

// Interactive scrollbars on this plugin's two terminal surfaces.
//
// The bar is drawn by the shared renderer exactly as it always was; what is
// new is that its column answers the pointer. This host owns the bookkeeping —
// the tty layer is state-free layout, and the window each surface scrolls is
// plugin state — so a press arms a gesture here from a press-time snapshot of
// the drawn window, motions map through that snapshot for as long as the
// button is held, and a release (or the first button-less motion that betrays
// a lost one) hands the window back to the distance-from-live-bottom model,
// which follows output again from wherever the pointer left it.

// terminalScrollbarHit names the surface whose bar a hit region belongs to.
// The payload, not the region ID alone, is what routes a press: both surfaces
// share one hit map and can draw bars at the same time.
type terminalScrollbarHit struct {
	TermPanel bool
}

// termBarGesture is one surface's in-flight pointer gesture on its bar. The
// press-time snapshot keeps a mid-gesture re-render — output landing under the
// window, a resize — from shifting the mapping under the pointer; the shared
// core clamps past both ends of the track without ever ending anything.
//
// While the gesture holds, the surface's window is pinned to an absolute start
// (the same freeze a text selection takes), so rows cannot slide when the live
// edge advances underneath them. lastStart mirrors that pin so each motion can
// move it by a delta without reading back a layout.
type termBarGesture struct {
	termPanel bool
	sb        tty.WindowScrollbar // renderer inputs at press time
	trackTopY int                 // absolute row of the track top at press time
	grabDelta int                 // rows between the pointer and the thumb's anchor row
	lastStart int                 // buffer-relative start the freeze currently pins
	active    bool
}

func isTermScrollbarDragID(id string) bool {
	return id == regionTermScrollbarThumb || id == regionTermScrollbarTrack
}

// registerTerminalScrollbarHits puts a terminal surface's bar regions in the
// hit map. It reads the same derivation the render path draws with — the
// surface box, the window input, the fitted layout — so the regions are where
// the bar actually is. Call it after every content region this frame put down:
// the reverse scan then gives the column to the bar over the terminal beneath
// it, while tabs and close buttons keep their earlier-earned priority. A
// surface whose content fits registers nothing: the reserved column is an
// anti-jitter spacer, not a control.
func (p *Plugin) registerTerminalScrollbarHits(termPanel bool) {
	surface := p.terminalSurfaceGeometry(termPanel)
	if !surface.OK {
		return
	}
	input := p.terminalWindowInputFor(termPanel)
	layout := calculateTerminalViewportLayout(input)
	if !layout.ShowScrollbar || layout.DisplayHeight < 1 {
		return
	}
	sb := tty.WindowScrollbarFor(layout, input.TotalItems)
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
	barX := surface.X + surface.Width - 1
	top := surface.Y
	hit := terminalScrollbarHit{TermPanel: termPanel}
	p.mouseHandler.HitMap.Add(regionTermScrollbarTrack, mouse.Rect{X: barX, Y: top, W: 1, H: geom.TrackRect.Dy()}, hit)
	// The thumb is added after the track so the reverse scan hands a press on
	// their overlap to the thumb, exactly as the shared geometry orders them.
	p.mouseHandler.HitMap.Add(regionTermScrollbarThumb, mouse.Rect{X: barX, Y: top + geom.ThumbRect.Min.Y, W: 1, H: geom.ThumbRect.Dy()}, hit)
}

// terminalBarStyle is the emphasis the named surface draws its bar with: hover
// lights whichever part the pointer rests on, a live gesture keeps the thumb lit.
func (p *Plugin) terminalBarStyle(termPanel bool) ui.ScrollbarStyle {
	dragging := p.termBar.active && p.termBar.termPanel == termPanel
	hovering := p.hoverTermBarSet && p.hoverTermBar == (terminalScrollbarHit{TermPanel: termPanel}) && !dragging
	return ui.ScrollbarStyle{
		Thumb: ui.HandleStateFrom(hovering, dragging),
		Track: ui.HandleStateFrom(hovering, false),
	}
}

// pressTerminalScrollbar begins a terminal surface's bar gesture: grabbing the
// thumb where it was pressed, or jumping to the clicked spot anchored there so
// the same gesture keeps dragging (macOS track-click). A rapid second press
// arrives as ActionDoubleClick and rides this seam deliberately — it re-grabs
// like the first press did instead of reaching whatever sits under the column.
//
// The order below is scrollTerminalWindowByWheel's, and it is load-bearing for
// the same reasons: release a document projection first (a projection is not a
// window anyone can place), thaw any pin second (before the selection is asked
// about), clear the selection third. Only then is the fresh window derived and
// pinned for THIS gesture.
func (p *Plugin) pressTerminalScrollbar(action mouse.MouseAction) {
	hit, ok := action.Region.Data.(terminalScrollbarHit)
	if !ok || action.Region == nil {
		return
	}
	termPanel := hit.TermPanel
	p.releaseTerminalDocProjection(termPanel)
	p.thawTerminalWindow(termPanel)
	p.clearTerminalSelectionOnScroll(termPanel)

	input := p.terminalWindowInputFor(termPanel)
	layout := calculateTerminalViewportLayout(input)
	sb := tty.WindowScrollbarFor(layout, input.TotalItems)
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

	// Take the gesture's own pin: any pin left standing belongs to something
	// that is over, and a rapid double-press must be able to re-grab.
	p.releaseTerminalWindowPin(termPanel)

	start := min(max(layout.Start, 0), max(layout.MaxOffset, 0))
	trackTopY := action.Region.Rect.Y
	grabDelta := 0
	if action.Region.ID == regionTermScrollbarThumb {
		trackTopY -= geom.ThumbRect.Min.Y
		grabDelta = action.Y - trackTopY - ui.RowForOffset(params, sb.Offset)
	} else {
		// Track press: jump-to-spot, anchored at the grabbed row.
		start = sb.StartAtTrackRow(action.Y - trackTopY)
	}
	p.pinTerminalWindow(termPanel, start, false)
	p.termBar = termBarGesture{
		termPanel: termPanel,
		sb:        sb,
		trackTopY: trackTopY,
		grabDelta: grabDelta,
		lastStart: start,
		active:    true,
	}
	p.mouseHandler.StartDrag(action.X, action.Y, action.Region.ID, 0)
}

// dragTerminalScrollbar applies a held gesture's mapping for one pointer row.
// Only a live gesture answers; wherever the pointer has since travelled, the
// surface it started on is the one that scrolls. The window moves inside its
// existing freeze — no thaw between motions — so output arriving mid-drag
// cannot slide the rows under the reader.
func (p *Plugin) dragTerminalScrollbar(y int) {
	g := p.termBar
	if !g.active {
		return
	}
	start := g.sb.StartAtTrackRow(y - g.trackTopY - g.grabDelta)
	bound := max(p.terminalMaxScroll(g.termPanel), 0)
	start = min(max(start, 0), bound)
	window := p.terminalWindow(g.termPanel)
	window.freeze.Scroll(g.lastStart-start, bound)
	g.lastStart = start
	p.termBar = g
}

// finishTerminalScrollbarDrag settles a finished or cancelled gesture: the
// window goes back to following the live edge from wherever the pointer left
// it (offset zero resumes following immediately), and parking at the oldest
// row reaches for older history exactly as a wheel notch there would.
func (p *Plugin) finishTerminalScrollbarDrag() {
	g := p.termBar
	if !g.active {
		return
	}
	p.termBar = termBarGesture{}
	p.thawTerminalGesturePin(g.termPanel)
	p.terminalHistoryIntentForScroll(g.termPanel, 1)
}
