package tty

// Where a terminal surface's window sits over its buffer is one model, and this
// is where its rules are written.
//
// Every host places the window as rows back from the live bottom, so zero is the
// live edge and following is derived rather than tracked, except while a pointer
// gesture holds it still — then it is pinned to an absolute start and the freeze
// above owns it. Both states are answered here so that no host has to translate
// between them at the call site: the global preview, the project's primary
// surface and its terminal panel all ask these functions the same questions.

// WindowPlacement is where a window sits, in the terms the viewport takes it.
// A window a gesture is holding is placed from an absolute top row and follows
// nothing; every other window is a distance back from the live bottom, and it
// follows output exactly when that distance is zero.
type WindowPlacement struct {
	Offset     int
	FromBottom bool
	Follow     bool
}

// PlaceWindow reports where a surface's window sits given its scroll offset and
// whatever freeze is holding it. It is the one derivation the render path, the
// native cursor and hit testing share, on every host.
func PlaceWindow(freeze *WindowFreeze, offset int) WindowPlacement {
	if freeze.Active() {
		return WindowPlacement{Offset: freeze.Start()}
	}
	offset = max(offset, 0)
	return WindowPlacement{Offset: offset, FromBottom: true, Follow: offset == 0}
}

// WindowAnchor is where the window sits in whichever coordinate it is currently
// placed by. Callers use it to tell whether a scroll moved anything, which is
// the only question that can be asked across both coordinates.
func WindowAnchor(freeze *WindowFreeze, offset int) int {
	if freeze.Active() {
		return freeze.Start()
	}
	return offset
}

// ScrollWindow moves a window delta rows back through scrollback — negative
// towards the live edge — and reports the offset it lands on. A window a gesture
// is holding is moved in the coordinate it is pinned in and keeps its offset,
// which is what lets an edge-autoscroll during a drag walk the rows without
// handing the window back to the live bottom mid-gesture.
func ScrollWindow(freeze *WindowFreeze, offset, delta, maxOffset int) int {
	if delta == 0 {
		return offset
	}
	if freeze.Active() {
		freeze.Scroll(delta, maxOffset)
		return offset
	}
	return min(max(offset+delta, 0), max(maxOffset, 0))
}

// ScrollWindowRows is ScrollWindow for a caller counting rendered rows down the
// screen — a wheel notch, an edge autoscroll, a rendered-row drag — rather than
// rows back through scrollback. The two directions are opposite, and this is the
// single place they are reconciled: every host used to write that negation into
// its own call site.
func ScrollWindowRows(freeze *WindowFreeze, offset, rows, maxOffset int) int {
	return ScrollWindow(freeze, offset, -rows, maxOffset)
}

// LeaveLiveWindow is where a window is left when a pane stops being live, and
// the answer is: exactly where the reader put it.
//
// The two hosts used to disagree — the project surface snapped to the live edge
// while the global preview kept the offset — which was drift from their two
// window models rather than a decision (td-2e3738). Keeping the reader's window
// is the deliberate choice: the rows on screen when the mode ends are the ones
// the reader was reading, the buffer behind them is not replaced by leaving, and
// a bottom-relative offset stays meaningful as new output arrives, so nothing is
// gained by yanking the view to the bottom of a pane the user just scrolled back
// through. A pin is thawed rather than kept, because the gesture holding it is
// over: the window resumes following output from where it stands.
func LeaveLiveWindow(freeze *WindowFreeze, offset, maxOffset int) int {
	if thawed, ok := freeze.ThawFrom(maxOffset); ok {
		return thawed
	}
	return max(offset, 0)
}
