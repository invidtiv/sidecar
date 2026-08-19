// Package textselect is Sidecar's one text-selection engine: the click/drag
// gesture state machine, the coordinate mapping under it, the highlight the
// selection is drawn with, and the copy that ends it.
//
// It knows nothing about what it is selecting. A surface implements [Source] —
// where its content is drawn, what its rows say, how far it is scrolled — and
// hands its pointer and key events to a [Surface], which answers with a [Result]
// naming what the host must do about them. Everything else is shared, so a new
// selectable pane is an adapter rather than another implementation.
//
// The embedded terminal needs richer inputs than [Source] carries — absolute
// buffer coordinates, a clipped pane's column offset, lazily loaded scrollback —
// so it drives the lower level directly: [Geometry], [Pointer] and the mapping
// functions are exported for it. [Surface] is the same engine composed for the
// simpler case of a list of rendered rows.
package textselect

import (
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/ui"
)

// Source is the surface-specific half of a selection, and the whole of what a
// binding has to write.
type Source interface {
	// ContentRect is the screen-space rect of the selectable content, inside
	// whatever chrome or gutter the surface draws, in the coordinate space its
	// mouse events arrive in.
	ContentRect() mouse.Rect
	// Line is visual row i as it was laid out — post-wrap, so the engine never
	// sees wrapping — and LineCount how many such rows there are. Selection
	// coordinates are visual rows: what the user sees is what they copy.
	Line(i int) string
	LineCount() int
	// Scroll is the index of the first visible row.
	Scroll() int
	// TabWidth is the tab stop the rows are expanded against. 0 means the rows
	// hold no tabs.
	TabWidth() int
}

// Result is what a host must act on after handing an event to a Surface.
type Result struct {
	// Handled reports that the surface answered the event, so the host must not
	// also act on it.
	Handled bool

	// Changed reports that the selection moved, so the host re-renders.
	Changed bool

	// Copy is the text a copy asks to put on the clipboard, and CopyAsked that
	// one was asked for at all. A copy chord with nothing selected asks and
	// supplies nothing, which is the case [Keys.Notice] speaks to, so a host
	// passes Copy to [Keys.CopySelectionCmd] whenever CopyAsked is set.
	Copy      []string
	CopyAsked bool

	// ClickThrough reports a gesture that resolved to a click rather than a
	// selection: the host runs whatever that click normally does. A drag never
	// sets it, which is the whole of "click activates, drag selects".
	ClickThrough bool

	// AutoScroll is how far the host should scroll its own offset, in rows,
	// positive downwards — a drag has run past an edge and is asking for more
	// content to select. Only the host can move its own window, so the rows the
	// scroll reveals are selected by the next motion event rather than by this
	// one.
	AutoScroll int
}

// Surface is one selectable region: the selection it holds and the gesture
// editing it. A host keeps one per pane and hands it events; the pane's own
// [Source] is passed in rather than stored, because the rows and the rect are
// the host's to answer freshly on every frame.
type Surface struct {
	// Keys are the chords this surface answers. The zero value answers only the
	// platform copy chord.
	Keys Keys

	// CopyOnSelect copies a finished selection without a copy chord, the way
	// xterm does. It is off by default: copying unasked is the single
	// most-complained-about behaviour in the editors that ship it.
	CopyOnSelect bool

	selection ui.SelectionState
	pointer   Pointer

	// dragging records that the gesture in flight began on this surface. A drag
	// and its release are answered by where they started, never by where the
	// pointer has since travelled.
	dragging bool
}

// Selection is the state the host renders from, for a surface that needs more
// than DecorateRow — a scrollbar marker, a status line, its own extraction.
func (s *Surface) Selection() *ui.SelectionState { return &s.selection }

// HasSelection reports whether anything is selected.
func (s *Surface) HasSelection() bool { return s.selection.HasSelection() }

// Clear drops the selection and ends any gesture editing it.
func (s *Surface) Clear() {
	s.selection.Clear()
	s.pointer.Abandon()
	s.pointer.ResetUnit()
	s.dragging = false
}

// Abandon ends a gesture whose release never arrived — the pointer left the
// window, a modal opened, focus moved. A host detects it the way the terminal
// hosts do, by noticing that its mouse handler has stopped dragging without
// having reported a [mouse.ActionDragEnd], and says so here: a surface left
// holding a live gesture answers the next unrelated drag anywhere on screen as
// an extension of a selection the user finished long ago.
func (s *Surface) Abandon() Result {
	if !s.dragging {
		return Result{}
	}
	s.pointer.Abandon()
	s.dragging = false
	return Result{Handled: true}
}

// HandleMouse advances the gesture over src and reports what the host owes.
//
// The host is expected to have started a drag on the press (mouse.Handler's
// StartDrag), because that is what turns the release into a
// [mouse.ActionDragEnd] this can resolve; without it a press arms a gesture no
// release ever ends. A release that is lost rather than late is [Surface.Abandon]'s;
// nothing in a mouse action says one happened.
func (s *Surface) HandleMouse(action mouse.MouseAction, src Source) Result {
	if src == nil {
		return Result{}
	}
	geometry := geometryFor(src)
	buffer := sourceBuffer{src: src}
	rect := geometry.Content

	switch PointerIntentFor(PointerIntentInput{
		Action:       action.Type,
		OverTerminal: rect.Contains(action.X, action.Y),
		FromTerminal: s.dragging,
	}) {
	case PointerPress:
		s.dragging = true
		s.pointer.Press(geometry, buffer, &s.selection, PressEvent{
			X:          action.X,
			Y:          action.Y,
			Shift:      action.Shift,
			Alt:        action.Alt,
			Rect:       rect,
			Want:       ResolveClick(ClickIntent{Modified: action.Shift || action.Alt}),
			SameSource: true,
		})
		return Result{Handled: true, Changed: true}

	case PointerSelectWord:
		return s.selectUnit(geometry, buffer, action, SelectUnitWord)

	case PointerSelectLine:
		return s.selectUnit(geometry, buffer, action, SelectUnitLine)

	case PointerDrag:
		if !s.selection.Anchor.Valid() {
			// The press landed on chrome or on the padding below the last row.
			// The gesture is unambiguously a selection by the time it is moving,
			// so anchor it where the button actually went down.
			pressX, pressY := s.pointer.PressPoint()
			s.pointer.AnchorFrom(geometry, buffer, &s.selection, pressX, pressY, action.Alt)
		}
		s.pointer.NoteDragMotion(action.X, action.Y)
		changed := s.pointer.DragTo(geometry, buffer, &s.selection, action.X, action.Y)
		return Result{
			Handled:    true,
			Changed:    changed,
			AutoScroll: EdgeScrollDelta(geometry, action.Y, DragScrollStep),
		}

	case PointerFinish:
		resolution, selected := s.pointer.Release(&s.selection)
		s.dragging = false
		result := Result{Handled: true, Changed: true, ClickThrough: resolution == ClickActivate}
		if selected && s.CopyOnSelect {
			result.Copy, result.CopyAsked = s.SelectedText(src), true
		}
		return result
	}
	return Result{}
}

// selectUnit installs the word or line under the pointer as the gesture's
// anchor unit, so a button still held extends by that unit.
func (s *Surface) selectUnit(geometry Geometry, buffer Buffer, action mouse.MouseAction, unit SelectionUnit) Result {
	s.dragging = true
	s.pointer.AdoptSurface(&s.selection, geometry.Content)
	if !s.pointer.SelectUnitAt(geometry, buffer, &s.selection, action.X, action.Y, unit) {
		return Result{Handled: true}
	}
	return Result{Handled: true, Changed: true}
}

// HandleKey answers the chords that act on the selection: copy, and select-all.
// A release is not one of them.
func (s *Surface) HandleKey(msg tea.KeyMsg, src Source) Result {
	press, ok := msg.(tea.KeyPressMsg)
	if !ok || src == nil {
		return Result{}
	}
	switch {
	case s.Keys.IsCopyChord(press):
		return Result{Handled: true, Copy: s.SelectedText(src), CopyAsked: true}
	case s.Keys.IsSelectAllChord(press):
		start, end, ok := SelectAllSpan(sourceBuffer{src: src}, src.TabWidth())
		if !ok {
			return Result{Handled: true}
		}
		s.pointer.ResetUnit()
		s.selection.SelectRange(start, end, false)
		s.selection.ViewRect = src.ContentRect()
		return Result{Handled: true, Changed: true}
	}
	return Result{}
}

// DecorateRow paints the selection onto one drawn row, identified by its visual
// row index — the same index [Source.Line] answers to, not its position on
// screen. It is called at slice time, on the row about to be drawn, never into
// anything the surface caches: the highlight belongs to this frame only.
//
// The row must already be tab-expanded, as the surface's own layout leaves it,
// or the columns the selection names will not be the columns it is drawn at.
func (s *Surface) DecorateRow(row string, visualRow int) string {
	if !s.selection.HasSelection() {
		return row
	}
	startCol, endCol := s.selection.GetLineSelectionCols(visualRow)
	if startCol < 0 {
		return row
	}
	return ui.InjectCharacterRangeBackground(row, startCol, endCol)
}

// SelectedText is the selection as the user sees it: the rows it covers,
// stripped of the styling they were drawn with, ready for the clipboard.
func (s *Surface) SelectedText(src Source) []string {
	if src == nil {
		return nil
	}
	return SelectedLines(sourceBuffer{src: src}, &s.selection, src.TabWidth())
}

// geometryFor places a source's content and names the rows drawn in it. The
// window is the source's scroll offset and as many rows as its rect is tall,
// clamped to the rows it actually has.
func geometryFor(src Source) Geometry {
	rect := src.ContentRect()
	count := src.LineCount()
	start := min(max(src.Scroll(), 0), max(count, 0))
	end := min(start+max(rect.H, 0), count)
	return Geometry{
		Content:  rect,
		Start:    start,
		End:      end,
		TabWidth: src.TabWidth(),
	}
}

// sourceBuffer reads a Source as the Buffer the gesture engine takes. A
// source's row indices are already its coordinate space — they name the same
// row however far it is scrolled — so it keeps no absolute range, and a
// selection made in it survives scrolling.
type sourceBuffer struct{ src Source }

func (b sourceBuffer) LineCount() int {
	if b.src == nil {
		return 0
	}
	return b.src.LineCount()
}

func (b sourceBuffer) LinesRange(start, end int) []string {
	if b.src == nil {
		return nil
	}
	start = max(start, 0)
	end = min(end, b.src.LineCount())
	if start >= end {
		return nil
	}
	lines := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		lines = append(lines, b.src.Line(i))
	}
	return lines
}

func (b sourceBuffer) LinesAbsoluteRange(int, int) []string { return nil }

func (b sourceBuffer) AbsoluteRange() (int, int, bool) { return 0, 0, false }
