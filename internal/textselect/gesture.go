package textselect

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/ui"
)

// ClickResolution is what a mouse-down over a terminal surface will mean if the
// gesture ends without motion. The two outcomes are mutually exclusive for one
// gesture — "activate and forward" is not a state — and every abort path (lost
// release, double-click, a click elsewhere) drops whichever one is pending.
type ClickResolution uint8

const (
	ClickNone ClickResolution = iota
	// ClickActivate makes a passive terminal live.
	ClickActivate
	// ClickForward hands the click to the app running in a live terminal that
	// has asked for mouse reports.
	ClickForward
)

// SelectionUnit is the granularity a gesture extends by. Double- and
// triple-clicks keep dragging in whole words and whole lines, as xterm and
// iTerm2 do, until the next plain click.
type SelectionUnit uint8

const (
	SelectUnitChar SelectionUnit = iota
	SelectUnitWord
	SelectUnitLine
)

// DragScrollStep bounds how far one motion event past an edge walks the window
// through scrollback. Unbounded, a pointer flicked to the top of the screen
// would skip hundreds of lines the user never saw.
const DragScrollStep = 3

// A held pointer past an edge keeps scrolling on a timer, the way every real
// terminal does; motion events alone stop arriving the moment the hand stops.
const (
	AutoScrollInterval = 70 * time.Millisecond
	AutoScrollStep     = 5
	// A release lost off-window is only noticed when the pointer comes back, so
	// an unbounded chain would keep dragging the selection through all of
	// scrollback (and onto the clipboard under copy-on-select) in the meantime.
	// Roughly 1.5s of holding still without any fresh motion pauses the chain;
	// real motion re-arms it, so a user genuinely parked at the edge only has to
	// twitch to keep going.
	AutoScrollMaxRun = 20
)

// AutoScrollHoldExpired reports when a run of ticks with no fresh drag motion
// behind it has gone on long enough to be treated as a lost release rather than
// a pointer deliberately parked past the edge.
func AutoScrollHoldExpired(ticks int) bool {
	return ticks > AutoScrollMaxRun
}

// PressEvent is a mouse-down over a terminal surface.
//
// SameSource reports whether the press landed on the surface the current
// selection was made in; a host that draws only one terminal always sets it.
// Want is what a release without motion should do, which only the host can
// decide: it knows whether the terminal is already live and whether something
// with a stronger claim on the click (a link) has already refused it.
type PressEvent struct {
	X, Y       int
	Shift, Alt bool
	Rect       mouse.Rect
	Want       ClickResolution
	SameSource bool
}

// Pointer is the click/drag state machine over a terminal surface: what a
// release without motion will mean, the granularity a drag extends by, and the
// held-pointer edge scroll. It owns no selection — the surface it edits is
// passed in — and no transport, so a headless caller can drive a whole gesture.
type Pointer struct {
	// Resolution is what a release without motion will do.
	Resolution ClickResolution

	pressX, pressY int

	// rect is the surface the live gesture belongs to. It is deliberately not
	// the selection's ViewRect: a drag that starts on chrome clears the
	// selection, and the gesture must still be able to anchor itself once it
	// reaches a row.
	rect mouse.Rect

	unit                SelectionUnit
	unitStart, unitEnd  ui.SelectionPoint
	dragX, dragY        int
	generation          uint64
	autoScrollPending   bool
	autoScrollTickCount int
}

// Begin closes whatever gesture was running: any auto-scroll tick still in
// flight belongs to the old generation and is dropped.
func (pt *Pointer) Begin() {
	pt.generation++
	pt.autoScrollPending = false
	pt.autoScrollTickCount = 0
}

// Abandon ends a gesture whose release will never arrive — the pointer left the
// window, focus changed, a modal swallowed the button-up. Neither activation
// nor a forwarded click survives a release the app never saw.
func (pt *Pointer) Abandon() {
	pt.Resolution = ClickNone
	pt.Begin()
}

// Generation identifies the live gesture, so a timer scheduled by an older one
// can be discarded.
func (pt *Pointer) Generation() uint64 { return pt.generation }

// Arm records what a release without motion will do, and where the button went
// down — a forwarded click is reported at the press position, never the release.
func (pt *Pointer) Arm(resolution ClickResolution, x, y int) {
	pt.Resolution = resolution
	pt.pressX, pt.pressY = x, y
}

// PressPoint is where the button went down.
func (pt *Pointer) PressPoint() (int, int) { return pt.pressX, pt.pressY }

// Rect is the surface the live gesture belongs to.
func (pt *Pointer) Rect() mouse.Rect { return pt.rect }

// AdoptSurface names the surface a gesture belongs to, for the pointer and for
// the selection it edits at once. Both must describe the same rect: the
// pointer's is what a drag whose press landed off the text anchors against, the
// selection's is what the host hit-tests with, and a caller that wrote only one
// of them left the two able to disagree.
func (pt *Pointer) AdoptSurface(sel *ui.SelectionState, r mouse.Rect) {
	pt.rect = r
	if sel != nil {
		sel.ViewRect = r
	}
}

// Unit is the granularity the live gesture extends by.
func (pt *Pointer) Unit() SelectionUnit { return pt.unit }

// ResetUnit drops the anchor unit. A word span left over from an old
// double-click would otherwise redefine where the next shift-click extends from.
func (pt *Pointer) ResetUnit() {
	pt.unit = SelectUnitChar
	pt.unitStart = ui.SelectionPoint{Line: -1, Col: -1}
	pt.unitEnd = ui.SelectionPoint{Line: -1, Col: -1}
}

// DragPoint is the last pointer position of the live drag, which the edge
// auto-scroll tick re-reads.
func (pt *Pointer) DragPoint() (int, int) { return pt.dragX, pt.dragY }

// NoteDragMotion records fresh pointer motion, which restarts the hold budget
// that bounds an auto-scroll chain running on a lost release.
func (pt *Pointer) NoteDragMotion(x, y int) {
	pt.dragX, pt.dragY = x, y
	pt.autoScrollTickCount = 0
}

// Press arms a gesture at the pointer. Selection only activates on actual drag
// motion, so a press over text prepares a drag rather than starting a selection.
func (pt *Pointer) Press(g Geometry, buf Buffer, sel *ui.SelectionState, ev PressEvent) {
	canExtend := ev.Shift && sel.HasSelection() && ev.SameSource
	pt.Arm(ev.Want, ev.X, ev.Y)
	pt.Begin()
	if !canExtend {
		// A plain click is a character gesture. A shift-click keeps whatever
		// granularity it is extending, the way xterm does.
		pt.ResetUnit()
	}
	pt.AdoptSurface(sel, ev.Rect)

	cell, ok := CellAt(g, buf, ev.X, ev.Y)
	if !ok {
		if canExtend {
			// Shift-clicking the header or the padding is a reach for the nearest
			// text, as in xterm — never an instruction to drop the selection.
			if clamped, hit := ClampedCellAt(g, buf, ev.X, ev.Y); hit {
				pt.ExtendTo(g, buf, sel, clamped)
			}
			return
		}
		sel.Clear()
		// Clear drops ViewRect, but the gesture is still live: a drag that starts
		// on chrome or on empty padding must still be able to anchor itself once
		// it reaches a row (see AnchorFrom).
		pt.AdoptSurface(sel, ev.Rect)
		return
	}

	if canExtend {
		pt.ExtendTo(g, buf, sel, cell)
		return
	}
	sel.PrepareDragMode(cell.Line, cell.Col, ev.Rect, ev.Alt)
}

// AnchorFrom starts a selection for a drag whose mouse-down landed off the text
// — on the header, on the padding below the last row, in the border. The gesture
// is unambiguously a selection by the time it is moving, so anchor it at the
// nearest cell to where the button actually went down rather than letting the
// whole drag do nothing.
func (pt *Pointer) AnchorFrom(g Geometry, buf Buffer, sel *ui.SelectionState, originX, originY int, rectangular bool) bool {
	cell, ok := ClampedCellAt(g, buf, originX, originY)
	if !ok {
		return false
	}
	sel.PrepareDragMode(cell.Line, cell.Col, pt.rect, rectangular)
	return true
}

// DragTo moves the live selection to the cell nearest the pointer, snapped to
// the gesture's unit.
func (pt *Pointer) DragTo(g Geometry, buf Buffer, sel *ui.SelectionState, x, y int) bool {
	cell, ok := ClampedCellAt(g, buf, x, y)
	if !ok {
		return false
	}
	if !pt.ExtendToUnit(g, buf, sel, cell) {
		sel.HandleDrag(cell.Line, cell.Col)
	}
	return true
}

// ExtendTo grows an existing selection to a cell, snapped to the gesture's unit
// when one is in flight.
func (pt *Pointer) ExtendTo(g Geometry, buf Buffer, sel *ui.SelectionState, cell Cell) {
	if pt.ExtendToUnit(g, buf, sel, cell) {
		return
	}
	sel.ExtendTo(ui.SelectionPoint{Line: cell.Line, Col: cell.Col})
}

// ExtendToUnit extends a word or line gesture to the unit under the pointer. The
// anchor unit stays whole in either direction: dragging backwards pins the far
// edge of the anchor, which is what makes word-drag feel like a terminal rather
// than a character drag that happens to start on a word.
func (pt *Pointer) ExtendToUnit(g Geometry, buf Buffer, sel *ui.SelectionState, cell Cell) bool {
	if pt.unit == SelectUnitChar || !pt.unitStart.Valid() || !pt.unitEnd.Valid() {
		return false
	}
	line, ok := LineTextAt(buf, cell.Line)
	if !ok {
		return false
	}
	start, end, ok := UnitSpanAt(pt.unit, line, cell.Line, cell.Col, g.tabWidth())
	if !ok {
		return false
	}
	return pt.ExtendMappedUnit(sel, start, end)
}

// ExtendMappedUnit extends a word or line gesture whose coordinates were
// mapped by the host. This is the same granularity rule as ExtendToUnit, but it
// lets a rendered surface keep its selection in source coordinates rather than
// pretending wrapped visual rows are logical source lines.
func (pt *Pointer) ExtendMappedUnit(sel *ui.SelectionState, start, end ui.SelectionPoint) bool {
	if sel == nil || pt.unit == SelectUnitChar || !pt.unitStart.Valid() || !pt.unitEnd.Valid() ||
		!start.Valid() || !end.Valid() {
		return false
	}
	if start.Before(pt.unitStart) {
		sel.SelectRange(pt.unitEnd, start, false)
	} else {
		sel.SelectRange(pt.unitStart, end, false)
	}
	sel.Active = true
	return true
}

// SelectUnitAt installs the word or line under the pointer and records it as the
// gesture's anchor unit, so a button still held extends by that unit and never
// eats into the unit the gesture started on. The mouse-down that opened the
// gesture asked for the terminal, or for the app's click; a double or triple
// click withdraws that, or the release would fire it under the selection the
// user just made.
func (pt *Pointer) SelectUnitAt(g Geometry, buf Buffer, sel *ui.SelectionState, x, y int, unit SelectionUnit) bool {
	cell, ok := CellAt(g, buf, x, y)
	if !ok {
		return false
	}
	line, ok := LineTextAt(buf, cell.Line)
	if !ok {
		return false
	}
	start, end, ok := UnitSpanAt(unit, line, cell.Line, cell.Col, g.tabWidth())
	if !ok {
		return false
	}
	pt.dragX, pt.dragY = x, y
	return pt.SelectMappedUnit(sel, start, end, unit)
}

// SelectMappedUnit installs a host-mapped word or line span and records it as
// the gesture anchor. Hosts use this when their drawn rows map back to a
// different coordinate space, such as soft-wrapped source text.
func (pt *Pointer) SelectMappedUnit(sel *ui.SelectionState, start, end ui.SelectionPoint, unit SelectionUnit) bool {
	if sel == nil || unit == SelectUnitChar || !start.Valid() || !end.Valid() {
		return false
	}
	pt.Begin()
	pt.unit = unit
	pt.unitStart, pt.unitEnd = start, end
	pt.Resolution = ClickNone
	sel.SelectRange(start, end, false)
	return true
}

// Release ends the gesture and reports what it meant: the armed resolution when
// nothing was selected, and whether a selection survives.
//
// A character gesture that never left its anchor cell is a click that jittered,
// not a selection. Without this, a twitch during a click leaves a one-cell
// selection, silently copies it under copy-on-select, and swallows the
// activation the user was asking for. A word gesture on a one-character word
// legitimately ends here, so it is exempt.
func (pt *Pointer) Release(sel *ui.SelectionState) (ClickResolution, bool) {
	sel.FinishDrag()
	// End the gesture before anything else: a scroll tick still in flight must
	// not keep dragging the window after the button is up.
	unit := pt.unit
	pt.Begin()
	if unit == SelectUnitChar && sel.HasSelection() && sel.Start == sel.End {
		sel.Clear()
	}
	resolution := pt.Resolution
	pt.Resolution = ClickNone
	if sel.HasSelection() {
		return ClickNone, true
	}
	return resolution, false
}

// EdgeScrollDelta reports how far one step should move the window for a pointer
// held at y, and zero once the pointer is back inside the content.
func EdgeScrollDelta(g Geometry, y, limit int) int {
	if !g.Valid() {
		return 0
	}
	return EdgeScrollRows(OutputRowAt(g, y), g.Rows(), limit)
}

// ScheduleAutoScroll queues the next step of the held-pointer scroll, at most
// one tick in flight at a time. The tick message is the host's, built with the
// generation that pins it to this gesture.
func (pt *Pointer) ScheduleAutoScroll(tick func(generation uint64) tea.Msg) tea.Cmd {
	if pt.autoScrollPending {
		return nil
	}
	pt.autoScrollPending = true
	generation := pt.generation
	return tea.Tick(AutoScrollInterval, func(time.Time) tea.Msg {
		return tick(generation)
	})
}

// BeginAutoScrollTick clears the in-flight tick and reports whether it still
// belongs to the live gesture.
func (pt *Pointer) BeginAutoScrollTick(generation uint64) bool {
	if generation != pt.generation {
		return false
	}
	pt.autoScrollPending = false
	return true
}

// ConsumeAutoScrollTick counts one tick without fresh motion behind it,
// reporting false once the run is long enough to be a lost release.
func (pt *Pointer) ConsumeAutoScrollTick() bool {
	pt.autoScrollTickCount++
	return !AutoScrollHoldExpired(pt.autoScrollTickCount)
}

// AutoScrollTarget is the surface a held-pointer edge scroll walks. The host
// supplies the window as callbacks because the scroll moves it: everything after
// the step has to be read again.
type AutoScrollTarget struct {
	Geometry  func() Geometry
	Buffer    func() Buffer
	Selection *ui.SelectionState

	// Scroll moves the window by delta rendered rows, positive downwards, and
	// reports whether it actually moved. A window already against the end of the
	// buffer ends the chain instead of ticking forever.
	Scroll func(delta int) bool
}

// AdvanceAutoScroll runs one step of the held-pointer edge scroll and reports
// whether another is owed. It stops when the tick belongs to an ended gesture,
// when there is no selection to extend, when the run is long enough to be a lost
// release, when the pointer is back inside the content, or when the window has
// no more rows in that direction.
func (pt *Pointer) AdvanceAutoScroll(generation uint64, target AutoScrollTarget) bool {
	if !pt.BeginAutoScrollTick(generation) {
		return false
	}
	if target.Selection == nil || !target.Selection.Anchor.Valid() {
		return false
	}
	if !pt.ConsumeAutoScrollTick() {
		return false
	}
	dragX, dragY := pt.DragPoint()
	delta := EdgeScrollDelta(target.Geometry(), dragY, AutoScrollStep)
	if delta == 0 || !target.Scroll(delta) {
		return false
	}
	// The window moved under the pointer, so ask again before extending.
	pt.DragTo(target.Geometry(), target.Buffer(), target.Selection, dragX, dragY)
	return true
}
