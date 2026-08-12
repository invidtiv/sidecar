package tty

import (
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/ui"
)

func testBuffer(lines ...string) *OutputBuffer {
	buf := NewOutputBuffer(max(len(lines), 1))
	buf.ApplySnapshot(PaneSnapshot{Output: strings.Join(lines, "\n"), PaneRows: len(lines)})
	return buf
}

// testGeometry places a surface whose content starts at (2, 1), the way a host
// with a border and a header does.
func testGeometry(rows int) Geometry {
	return Geometry{
		Content:  mouse.Rect{X: 2, Y: 1, W: 40, H: rows},
		Start:    0,
		End:      rows,
		TabWidth: DefaultTabWidth,
	}
}

func TestCellAtMapsScreenToBuffer(t *testing.T) {
	buf := testBuffer("hello world", "second line", "third")
	g := testGeometry(3)

	cell, ok := CellAt(g, buf, 2+6, 1+1)
	if !ok {
		t.Fatal("a position over text did not map to a cell")
	}
	if cell.Line != 1 || cell.Col != 6 {
		t.Errorf("cell = %+v, want line 1 col 6", cell)
	}
}

func TestCellAtRefusesChrome(t *testing.T) {
	buf := testBuffer("hello world")
	g := testGeometry(1)

	if _, ok := CellAt(g, buf, 4, 0); ok {
		t.Error("a position on the header mapped to a cell; chrome is not text")
	}
	if _, ok := CellAt(g, buf, 1, 1); ok {
		t.Error("a position left of the content mapped to a cell")
	}
	if _, ok := CellAt(g, buf, 4, 1+1); ok {
		t.Error("a position below the last row mapped to a cell")
	}
}

func TestClampedCellAtTracksAPointerOffTheSurface(t *testing.T) {
	buf := testBuffer("hello world", "second line")
	g := testGeometry(2)

	cell, ok := ClampedCellAt(g, buf, 0, 40)
	if !ok {
		t.Fatal("a pointer dragged below the surface stopped tracking")
	}
	if cell.Line != 1 {
		t.Errorf("line = %d, want the last visible row", cell.Line)
	}
	if cell.Col != 0 {
		t.Errorf("col = %d, want the first column", cell.Col)
	}
}

func TestCellAtHonorsAClippedPaneColumnOffset(t *testing.T) {
	buf := testBuffer("0123456789")
	g := testGeometry(1)
	g.ColOffset = 3

	cell, ok := CellAt(g, buf, 2, 1)
	if !ok {
		t.Fatal("the first drawn column did not map to a cell")
	}
	if cell.Col != 3 {
		t.Errorf("col = %d, want the pane column drawn there", cell.Col)
	}
}

func TestEdgeScrollRowsScalesWithOvershootAndClamps(t *testing.T) {
	for _, tc := range []struct {
		name      string
		outputRow int
		want      int
	}{
		{"inside", 4, 0},
		{"just above", -1, -1},
		{"far above", -40, -5},
		{"just below", 10, 1},
		{"far below", 60, 5},
	} {
		if got := EdgeScrollRows(tc.outputRow, 10, 5); got != tc.want {
			t.Errorf("%s: EdgeScrollRows(%d, 10, 5) = %d, want %d",
				tc.name, tc.outputRow, got, tc.want)
		}
	}
}

func TestWordSpanAtSelectsWholeWordsAndSingleCells(t *testing.T) {
	const line = "run cmd --flag=value"
	for _, tc := range []struct {
		name             string
		col              int
		wantStart, wantE int
	}{
		{"word", 5, 4, 6},
		{"whitespace is its own cell", 3, 3, 3},
		{"a flag run stays whole", 12, 8, 13},
	} {
		start, end, ok := WordSpanAt(line, tc.col)
		if !ok {
			t.Fatalf("%s: no span at col %d", tc.name, tc.col)
		}
		if start != tc.wantStart || end != tc.wantE {
			t.Errorf("%s: span = [%d,%d], want [%d,%d]", tc.name, start, end, tc.wantStart, tc.wantE)
		}
	}
}

func TestUnitSpanAtCoversTheWholeLine(t *testing.T) {
	start, end, ok := UnitSpanAt(SelectUnitLine, "abc", 7, 1, DefaultTabWidth)
	if !ok {
		t.Fatal("a line unit produced no span")
	}
	if start != (ui.SelectionPoint{Line: 7, Col: 0}) || end != (ui.SelectionPoint{Line: 7, Col: 2}) {
		t.Errorf("span = %+v..%+v, want the whole line", start, end)
	}
	if _, _, ok := UnitSpanAt(SelectUnitChar, "abc", 7, 1, DefaultTabWidth); ok {
		t.Error("a character gesture produced a span to snap to")
	}
}

func TestSelectedLinesClipsToTheBuffer(t *testing.T) {
	buf := testBuffer("alpha", "beta", "gamma")
	sel := &ui.SelectionState{}
	sel.SelectRange(ui.SelectionPoint{Line: 0, Col: 0}, ui.SelectionPoint{Line: 9, Col: 0}, false)

	lines := SelectedLines(buf, sel, DefaultTabWidth)
	if len(lines) != 3 {
		t.Fatalf("selected %d lines, want the 3 the buffer holds: %q", len(lines), lines)
	}
}

func TestSelectAllSpanCoversEveryLine(t *testing.T) {
	buf := testBuffer("alpha", "longer line")
	start, end, ok := SelectAllSpan(buf, DefaultTabWidth)
	if !ok {
		t.Fatal("select-all produced no span")
	}
	if start.Line != 0 || start.Col != 0 {
		t.Errorf("start = %+v, want the first cell", start)
	}
	if end.Line != 1 || end.Col != len("longer line")-1 {
		t.Errorf("end = %+v, want the last cell of the last line", end)
	}
}
