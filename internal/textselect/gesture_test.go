package textselect

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/ui"
)

func pressAt(pt *Pointer, g Geometry, buf Buffer, sel *ui.SelectionState, x, y int, want ClickResolution) {
	pt.Press(g, buf, sel, PressEvent{X: x, Y: y, Rect: g.Content, Want: want, SameSource: true})
}

func TestReleaseWithoutMotionResolvesTheClick(t *testing.T) {
	for _, want := range []ClickResolution{ClickActivate, ClickForward} {
		buf := testBuffer("hello world")
		g := testGeometry(1)
		sel := &ui.SelectionState{}
		pt := &Pointer{}

		pressAt(pt, g, buf, sel, 4, 1, want)
		got, selected := pt.Release(sel)
		if selected {
			t.Errorf("want %v: a motionless click left a selection", want)
		}
		if got != want {
			t.Errorf("resolution = %v, want %v", got, want)
		}
		if pt.Resolution != ClickNone {
			t.Errorf("resolution survived the release as %v", pt.Resolution)
		}
	}
}

func TestDragMakesTheGestureASelectionNotAClick(t *testing.T) {
	buf := testBuffer("hello world", "second line")
	g := testGeometry(2)
	sel := &ui.SelectionState{}
	pt := &Pointer{}

	pressAt(pt, g, buf, sel, 2, 1, ClickActivate)
	if !pt.DragTo(g, buf, sel, 2+6, 1+1) {
		t.Fatal("a drag over text did not track")
	}
	resolution, selected := pt.Release(sel)
	if !selected {
		t.Fatal("a drag across two rows left no selection")
	}
	if resolution != ClickNone {
		t.Errorf("resolution = %v, want none: a drag is not a click", resolution)
	}
}

func TestOneCellCharGestureIsAJitteredClick(t *testing.T) {
	buf := testBuffer("hello world")
	g := testGeometry(1)
	sel := &ui.SelectionState{}
	pt := &Pointer{}

	pressAt(pt, g, buf, sel, 4, 1, ClickActivate)
	pt.DragTo(g, buf, sel, 4, 1)
	resolution, selected := pt.Release(sel)
	if selected {
		t.Error("a click that jittered inside one cell was kept as a selection")
	}
	if resolution != ClickActivate {
		t.Errorf("resolution = %v, want activate", resolution)
	}
}

func TestOneCharacterWordGestureSurvivesTheRelease(t *testing.T) {
	buf := testBuffer("a bc")
	g := testGeometry(1)
	sel := &ui.SelectionState{}
	pt := &Pointer{}

	if !pt.SelectUnitAt(g, buf, sel, 2, 1, SelectUnitWord) {
		t.Fatal("double click selected nothing")
	}
	if _, selected := pt.Release(sel); !selected {
		t.Error("a word gesture on a one-character word was discarded as a jittered click")
	}
}

func TestAbandonCancelsTheClickAndInvalidatesTicks(t *testing.T) {
	buf := testBuffer("hello world")
	g := testGeometry(1)
	sel := &ui.SelectionState{}
	pt := &Pointer{}

	pressAt(pt, g, buf, sel, 4, 1, ClickForward)
	generation := pt.Generation()
	if cmd := pt.ScheduleAutoScroll(func(uint64) tea.Msg { return nil }); cmd == nil {
		t.Fatal("no auto-scroll tick was scheduled")
	}

	pt.Abandon()
	if pt.Resolution != ClickNone {
		t.Error("a forwarded click survived a release the app never saw")
	}
	if pt.BeginAutoScrollTick(generation) {
		t.Error("a tick from the abandoned gesture was still accepted")
	}
}

func TestAutoScrollRunIsBounded(t *testing.T) {
	pt := &Pointer{}
	ticks := 0
	for pt.ConsumeAutoScrollTick() {
		ticks++
		if ticks > AutoScrollMaxRun*4 {
			t.Fatal("an auto-scroll chain on a lost release never stopped")
		}
	}
	if ticks != AutoScrollMaxRun {
		t.Errorf("ran %d ticks, want %d", ticks, AutoScrollMaxRun)
	}
	pt.NoteDragMotion(1, 1)
	if !pt.ConsumeAutoScrollTick() {
		t.Error("real motion did not re-arm the hold budget")
	}
}

func TestWordDragKeepsTheAnchorWordWhole(t *testing.T) {
	buf := testBuffer("alpha beta gamma")
	g := testGeometry(1)
	sel := &ui.SelectionState{}
	pt := &Pointer{}

	// Double click "beta", then drag backwards into "alpha".
	if !pt.SelectUnitAt(g, buf, sel, 2+7, 1, SelectUnitWord) {
		t.Fatal("double click selected nothing")
	}
	pt.DragTo(g, buf, sel, 2+1, 1)

	lines := SelectedLines(buf, sel, DefaultTabWidth)
	if len(lines) != 1 || lines[0] != "alpha beta" {
		t.Errorf("selection = %q, want the anchor word kept whole", lines)
	}
}

func TestLineGestureSelectsWholeLines(t *testing.T) {
	buf := testBuffer("alpha", "beta")
	g := testGeometry(2)
	sel := &ui.SelectionState{}
	pt := &Pointer{}

	if !pt.SelectUnitAt(g, buf, sel, 2+2, 1, SelectUnitLine) {
		t.Fatal("triple click selected nothing")
	}
	pt.DragTo(g, buf, sel, 2+1, 1+1)

	lines := SelectedLines(buf, sel, DefaultTabWidth)
	if len(lines) != 2 || lines[0] != "alpha" || lines[1] != "beta" {
		t.Errorf("selection = %q, want both whole lines", lines)
	}
}

func TestPressOnChromeStillLetsADragAnchor(t *testing.T) {
	buf := testBuffer("hello world")
	g := testGeometry(1)
	sel := &ui.SelectionState{}
	pt := &Pointer{}

	// The press lands on the header, which is not text.
	pt.Press(g, buf, sel, PressEvent{X: 4, Y: 0, Rect: g.Content, Want: ClickActivate, SameSource: true})
	if sel.Anchor.Valid() {
		t.Fatal("a press on chrome anchored a selection")
	}
	if pt.Rect() != g.Content {
		t.Error("the gesture lost the surface it belongs to")
	}
	if !pt.AnchorFrom(g, buf, sel, 4, 0, false) {
		t.Fatal("a drag that started on chrome could not anchor")
	}
	if !pt.DragTo(g, buf, sel, 2+8, 1) {
		t.Fatal("the drag did not track after anchoring")
	}
	if _, selected := pt.Release(sel); !selected {
		t.Error("a drag from chrome onto text selected nothing")
	}
}

func TestShiftPressExtendsAnExistingSelection(t *testing.T) {
	buf := testBuffer("hello world")
	g := testGeometry(1)
	sel := &ui.SelectionState{}
	pt := &Pointer{}

	sel.SelectRange(ui.SelectionPoint{Line: 0, Col: 0}, ui.SelectionPoint{Line: 0, Col: 2}, false)
	pt.Press(g, buf, sel, PressEvent{X: 2 + 8, Y: 1, Shift: true, Rect: g.Content, SameSource: true})

	lines := SelectedLines(buf, sel, DefaultTabWidth)
	if len(lines) != 1 || lines[0] != "hello wor" {
		t.Errorf("selection = %q, want the shift-click to have extended it", lines)
	}
}

func TestShiftPressAcrossSurfacesDoesNotExtend(t *testing.T) {
	buf := testBuffer("hello world")
	g := testGeometry(1)
	sel := &ui.SelectionState{}
	pt := &Pointer{}

	sel.SelectRange(ui.SelectionPoint{Line: 0, Col: 0}, ui.SelectionPoint{Line: 0, Col: 2}, false)
	pt.Press(g, buf, sel, PressEvent{X: 2 + 8, Y: 1, Shift: true, Rect: g.Content})

	if sel.HasSelection() && sel.End.Col == 8 {
		t.Error("a shift-click on another surface extended a selection made in this one")
	}
}

func TestPressRecordsWhereTheButtonWentDown(t *testing.T) {
	buf := testBuffer("hello world")
	g := testGeometry(1)
	sel := &ui.SelectionState{}
	pt := &Pointer{}

	pt.Press(g, buf, sel, PressEvent{X: 9, Y: 1, Rect: mouse.Rect{X: 2, Y: 1, W: 40, H: 1}, Want: ClickForward})
	if x, y := pt.PressPoint(); x != 9 || y != 1 {
		t.Errorf("press point = (%d,%d), want (9,1): a forwarded click reports the press", x, y)
	}
}
