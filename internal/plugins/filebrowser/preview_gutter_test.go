package filebrowser

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/ui"
)

// newGutterTestPlugin builds a plugin previewing lineCount numbered lines, with
// no disk or tree behind it.
func newGutterTestPlugin(lineCount int) *Plugin {
	lines := make([]string, lineCount)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	return &Plugin{
		width:        120,
		height:       40,
		previewWidth: 80,
		previewFile:  "big.txt",
		previewSize:  1,
		previewLines: lines,
		mouseHandler: mouse.NewHandler(),
	}
}

// previewRows returns the rendered preview pane split into rows with the ANSI
// stripped, which is what the reader actually sees.
func previewRows(p *Plugin, visibleHeight int) []string {
	rendered := ansi.Strip(p.renderPreviewPane(visibleHeight))
	return strings.Split(rendered, "\n")
}

// TestPreviewGutterWidensPastFourDigits is the reason the shared gutter exists:
// a file over 9999 lines used to be numbered in a fixed five-cell column, so
// the fifth digit ran into the text. The column now grows with the file, and
// every row still starts its text at the same column.
func TestPreviewGutterWidensPastFourDigits(t *testing.T) {
	p := newGutterTestPlugin(12000)
	p.previewScroll = 9995

	rows := previewRows(p, 10)

	var seen int
	for _, row := range rows {
		if !strings.Contains(row, "line ") {
			continue
		}
		seen++
		number, text, ok := strings.Cut(row, "line ")
		if !ok {
			t.Fatalf("row %q has no text", row)
		}
		// The number is right-aligned in a five-digit column plus one space.
		if len(number) != 6 {
			t.Errorf("row %q: gutter is %d cells, want 6", row, len(number))
		}
		want := strings.TrimSpace(number)
		if got := strings.TrimSpace(text); got != want {
			t.Errorf("row %q: numbered %s but reads line %s", row, want, got)
		}
	}
	if seen == 0 {
		t.Fatal("no numbered rows rendered")
	}
}

// TestPreviewGutterStaysFiveCellsForSmallFiles pins the historical width, since
// widening it for ordinary files would move every preview line one cell right.
func TestPreviewGutterStaysFiveCellsForSmallFiles(t *testing.T) {
	p := newGutterTestPlugin(120)

	for _, row := range previewRows(p, 10) {
		if !strings.Contains(row, "line ") {
			continue
		}
		number, _, _ := strings.Cut(row, "line ")
		if len(number) != 5 {
			t.Fatalf("row %q: gutter is %d cells, want 5", row, len(number))
		}
	}
}

// TestPreviewClickGeometryFollowsGutter guards the other half: the hit-test
// geometry reads the same gutter, so a click on a wide-gutter file resolves to
// the column it looks like it hit rather than one five cells to its left.
func TestPreviewClickGeometryFollowsGutter(t *testing.T) {
	wide := newGutterTestPlugin(12000)
	narrow := newGutterTestPlugin(120)

	if got, want := wide.previewGutter().Width(), 6; got != want {
		t.Errorf("wide gutter width = %d, want %d", got, want)
	}
	if got, want := narrow.previewGutter().Width(), 5; got != want {
		t.Errorf("narrow gutter width = %d, want %d", got, want)
	}

	// The same screen X lands one column earlier in the wide file, because its
	// text starts one cell further right.
	x := 1 + 6 + 3 // border + wide gutter + three characters in
	if got, want := wide.previewColAtScreenX(x, 0), 3; got != want {
		t.Errorf("wide col at x=%d is %d, want %d", x, got, want)
	}
	if got, want := narrow.previewColAtScreenX(x, 0), 4; got != want {
		t.Errorf("narrow col at x=%d is %d, want %d", x, got, want)
	}
}

// The gutter's cells carry their own styling now, and a selected line paints
// over them with ui.InjectSelectionBackground. Wrapping already-styled text is
// where a highlight goes wrong: the fragment's own reset ends the selection
// background partway through the cell, or the background leaks past the gutter
// into the line's text. Both are invisible to a test that only strips the ANSI,
// so this one reads the sequences.
func TestPreviewGutterSurvivesTheSelectionBackground(t *testing.T) {
	for _, lineCount := range []int{120, 12000} {
		gutter := docview.NewGutter(lineCount)
		styled := gutter.Number(42)
		if !strings.Contains(styled, "\x1b[") {
			t.Fatalf("%d-line gutter cell %q carries no styling; this test is pointless without it", lineCount, styled)
		}

		selected := ui.InjectSelectionBackground(styled)
		if got, want := ansi.Strip(selected), ansi.Strip(styled); got != want {
			t.Errorf("selection changed the gutter text: %q, want %q", got, want)
		}
		if got, want := ansi.StringWidth(selected), gutter.Width(); got != want {
			t.Errorf("selected gutter cell is %d cells, want %d", got, want)
		}

		bg := ui.GetSelectionBgANSI()
		if !strings.Contains(selected, bg) {
			t.Errorf("selected gutter cell carries no selection background: %q", selected)
		}
		// Every reset inside the cell re-arms the background, so the gutter's
		// own styling cannot end the highlight partway through the column, and
		// the last one closes it so it cannot bleed into the line text appended
		// after it.
		resets := ui.AnsiResetRe.FindAllStringIndex(selected, -1)
		if len(resets) == 0 {
			t.Fatalf("selected gutter cell never resets: %q", selected)
		}
		for _, at := range resets[:len(resets)-1] {
			if rest := selected[at[1]:]; !strings.HasPrefix(rest, bg) {
				t.Errorf("the selection background stops at an inner reset: %q", selected)
			}
		}
		if last := resets[len(resets)-1]; last[1] != len(selected) {
			t.Errorf("selected gutter cell does not reset at its end: %q", selected)
		}
	}
}

// The same at the rendering level: a selected preview line keeps its gutter
// column exactly as wide as an unselected one.
func TestSelectedPreviewLineKeepsTheGutterColumn(t *testing.T) {
	p := newGutterTestPlugin(12000)
	p.previewScroll = 0
	p.selection.Active = true
	p.selection.Start = ui.SelectionPoint{Line: 2, Col: 0}
	p.selection.End = ui.SelectionPoint{Line: 2, Col: 5}

	rendered := p.renderPreviewPane(10)
	if !strings.Contains(rendered, ui.GetSelectionBgANSI()) {
		t.Fatal("no selection background anywhere in a pane with a selected line")
	}
	for _, row := range strings.Split(ansi.Strip(rendered), "\n") {
		if !strings.Contains(row, "line ") {
			continue
		}
		number, _, _ := strings.Cut(row, "line ")
		if len(number) != 6 {
			t.Fatalf("row %q: gutter is %d cells, want 6", row, len(number))
		}
	}
}
