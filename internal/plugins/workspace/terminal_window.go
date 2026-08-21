package workspace

import "github.com/marcus/sidecar/internal/tty"

// This is one implementation of the window state both of this surface's
// terminals keep: how far back through scrollback the drawn window sits, and
// whether a pointer gesture or a document activation is holding it still at an
// absolute start.
//
// It used to be two — the primary terminal's and the panel's, six near-identical
// pairs of functions that had to be changed together and twice drifted apart
// (td-bbbbfe). They are parameterized by the same termPanel bool every other
// terminal path on this surface already takes, so a rule stated here is stated
// for both surfaces by construction.

// terminalWindow is one surface's window state, addressed rather than copied so
// the callers below mutate the plugin's own fields.
type terminalWindow struct {
	freeze *tty.WindowFreeze
	scroll *int
	// pinnedByDoc says the live pin belongs to a document activation rather
	// than a pointer gesture. The two are not released by the same events.
	pinnedByDoc *bool
}

func (p *Plugin) terminalWindow(termPanel bool) terminalWindow {
	if termPanel {
		return terminalWindow{freeze: &p.termPanelFreeze, scroll: &p.termPanelScroll, pinnedByDoc: &p.termPanelFreezeDoc}
	}
	return terminalWindow{freeze: &p.previewFreeze, scroll: &p.previewScroll, pinnedByDoc: &p.previewFreezeDoc}
}

// terminalMaxScroll is the furthest back a surface's window can sit, in rows
// from the live bottom. It is the bound of the window the render path actually
// draws, not the raw line count: a trimmed tail or a letterboxed pane puts the
// two several rows apart, and a count-based bound then lets the window walk past
// the oldest row that can be shown — a dead zone at the top of scrollback, and a
// scrollback-history load that only fires after the reader pushes through it.
//
// A panel that is not on screen has no window to bound; asking anyway would
// answer for a surface with no geometry, from the preview's size.
func (p *Plugin) terminalMaxScroll(termPanel bool) int {
	if termPanel {
		if p.termPanelOutput == nil {
			return 0
		}
		if _, _, ok := p.calculateTermPanelDimensions(); !ok {
			return 0
		}
	}
	return p.terminalWindowBound(termPanel)
}

// moveTerminalWindow moves a surface's window delta rows back through
// scrollback, negative towards the live edge — the same direction the global
// preview and the shared scrollback rule count in, so no call site has to invert
// anything.
//
// Scrolling is an explicit navigation of this surface, so it thaws first: a
// window a gesture or a document pinned to an absolute start is handed back to
// the distance-from-bottom model where it stands, rather than being moved in a
// coordinate the reader can no longer see.
func (p *Plugin) moveTerminalWindow(termPanel bool, delta int) {
	p.thawTerminalWindow(termPanel)
	window := p.terminalWindow(termPanel)
	*window.scroll = tty.ScrollWindow(window.freeze, *window.scroll, delta, p.terminalMaxScroll(termPanel))
}

// moveTerminalWindowRows is moveTerminalWindow for a caller counting
// rendered rows down the screen — a wheel notch — rather than rows back through
// scrollback. The two directions are opposite, and reconciling them is the
// shared rule's rather than each call site's.
func (p *Plugin) moveTerminalWindowRows(termPanel bool, rows int) {
	p.thawTerminalWindow(termPanel)
	window := p.terminalWindow(termPanel)
	*window.scroll = tty.ScrollWindowRows(window.freeze, *window.scroll, rows, p.terminalMaxScroll(termPanel))
}

// pinTerminalWindow holds a surface's window at an absolute start and records
// who is holding it. The two owners are not released by the same events: a
// pointer gesture's pin ends with the gesture, while a document activation's
// outlives it, because the document is meant to keep showing the context it was
// opened from. Whether this pin takes at all is the shared freeze's rule — a
// second freeze inside one gesture keeps the first, and its owner with it.
func (p *Plugin) pinTerminalWindow(termPanel bool, start int, doc bool) {
	window := p.terminalWindow(termPanel)
	if window.freeze.Active() {
		return
	}
	window.freeze.Freeze(start)
	*window.pinnedByDoc = doc
}

// releaseTerminalWindowPin drops the pin whoever placed it, for a jump that
// chooses its own window rather than resuming from the pinned one.
func (p *Plugin) releaseTerminalWindowPin(termPanel bool) {
	window := p.terminalWindow(termPanel)
	window.freeze.Release()
	*window.pinnedByDoc = false
}

// releaseTerminalGesturePin drops a pin a pointer gesture placed, once the
// selection it was reading is gone — the gesture's half of the freeze/thaw
// obligation. A pin left behind by a selection that no longer exists holds the
// window off the live edge with nothing on screen to explain why, which reads as
// a pane that went quiet. A document's pin is not this one's to drop.
func (p *Plugin) releaseTerminalGesturePin(termPanel bool) {
	if *p.terminalWindow(termPanel).pinnedByDoc {
		return
	}
	p.releaseTerminalWindowPin(termPanel)
}

// thawTerminalWindow hands a window pinned to an absolute start back to the
// distance-from-bottom model without moving the rows on screen, so it follows
// new output again from exactly where it was left. Where it resumes from is the
// shared rule's. Every explicit navigation of a surface calls it: a window
// pinned and never released has stopped following output for good, which reads
// as a pane that went quiet.
//
// The panel additionally drops any document projection it is showing, before the
// pin is even consulted: a projection always implies a document-owned pin, so a
// projection left behind an inactive freeze would keep drawing a window the
// surface no longer holds.
func (p *Plugin) thawTerminalWindow(termPanel bool) {
	if termPanel {
		p.releaseTerminalDocProjection(true)
	}
	window := p.terminalWindow(termPanel)
	if !window.freeze.Active() {
		return
	}
	if offset, thawed := window.freeze.ThawFrom(p.terminalWindowBound(termPanel)); thawed {
		*window.scroll = offset
	}
	*window.pinnedByDoc = false
}

// thawTerminalGesturePin is the half of the freeze a pointer gesture owes at its
// end: the rows the gesture left on screen stay there, held as a distance from
// the live bottom, so a pin taken at the live edge resumes following from offset
// 0 while a gesture that walked the window back through scrollback keeps where
// it walked to. Releasing instead would resume from whatever offset the surface
// held before the gesture froze it, which snaps the window back with nothing on
// screen to explain the jump. A document activation's pin is not the gesture's
// to release: it outlives the selection the click made.
func (p *Plugin) thawTerminalGesturePin(termPanel bool) {
	if *p.terminalWindow(termPanel).pinnedByDoc {
		return
	}
	p.thawTerminalWindow(termPanel)
}
