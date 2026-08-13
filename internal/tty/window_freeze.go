package tty

// Holding a window still for the duration of a pointer gesture is one rule, and
// this is where it is written.
//
// A window placed from the live bottom moves whenever the buffer does, and every
// capture renumbers a watched buffer, so output landing mid-drag would slide the
// rows out from under the pointer while the selection anchor still named the
// ones it was made on. For as long as a gesture is reading rows the window is
// pinned to an absolute start instead.
//
// Freezing and thawing are the same rule's two halves, and a surface that
// implements one owes the other: a window frozen by a drag and never thawed has
// stopped following output for good, which reads as a pane that went quiet.

// ThawOffset places a frozen window back against the live bottom without moving
// the rows on screen: start is where the window was pinned, and the result is
// the same rows expressed as a distance back from the live edge, so the window
// follows new output again from exactly where the gesture left it.
func ThawOffset(layout Viewport) int {
	return ThawOffsetFrom(layout.Start, layout.MaxOffset)
}

// ThawOffsetFrom is ThawOffset for a surface that holds the frozen start itself
// rather than in the layout it rendered.
func ThawOffsetFrom(start, maxOffset int) int {
	return min(max(maxOffset-min(max(start, 0), maxOffset), 0), max(maxOffset, 0))
}

// WindowFreeze is the freeze state for a surface whose window is placed from the
// live bottom. It holds the absolute start the gesture pinned, and nothing else:
// the buffer, the layout and the scroll position stay the host's.
type WindowFreeze struct {
	active bool
	start  int
}

// Active reports that a gesture is holding the window still.
func (f *WindowFreeze) Active() bool { return f != nil && f.active }

// Start is the absolute top row the window is pinned to. It is meaningful only
// while Active.
func (f *WindowFreeze) Start() int { return f.start }

// Freeze pins the window to start. A second freeze inside one gesture keeps the
// first: the gesture is reading the rows it was armed on.
func (f *WindowFreeze) Freeze(start int) {
	if f.active {
		return
	}
	f.start, f.active = start, true
}

// Scroll moves a frozen window by delta rows towards the live edge, positive
// downwards, clamped to the buffer. A frozen window is placed from the top, so
// older output is a smaller start rather than a larger distance from the bottom.
func (f *WindowFreeze) Scroll(delta, maxOffset int) {
	f.start = min(max(f.start-delta, 0), maxOffset)
}

// Thaw ends the freeze and reports the offset the window resumes following from,
// leaving the rows on screen where they are. ok is false when nothing was
// frozen, so a caller does not overwrite a scroll position it never pinned.
func (f *WindowFreeze) Thaw(layout Viewport) (offset int, ok bool) {
	if !f.active {
		return 0, false
	}
	f.active = false
	return ThawOffset(layout), true
}

// ThawFrom is Thaw for a surface that measures its own bound rather than
// reading it back from a layout it rendered.
func (f *WindowFreeze) ThawFrom(maxOffset int) (offset int, ok bool) {
	if !f.active {
		return 0, false
	}
	f.active = false
	return ThawOffsetFrom(f.start, maxOffset), true
}

// Rebase shifts a frozen start by rows prepended to the buffer, so a window
// pinned to an absolute row keeps the same output after a history load
// renumbers the buffer underneath it.
func (f *WindowFreeze) Rebase(added int) {
	if !f.active {
		return
	}
	f.start = max(f.start+added, 0)
}

// Release drops the freeze without placing the window, for a jump that chooses
// its own offset — a jump is not a gesture reading the rows it lands on.
func (f *WindowFreeze) Release() { f.active = false }
