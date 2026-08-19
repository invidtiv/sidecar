package docview

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/textselect"
	"github.com/marcus/sidecar/internal/ui"
)

// selectionOrigin is where the fake host draws the content box: somewhere that
// is neither the screen's corner nor the pane's, so a coordinate that forgot to
// account for either shows up as a miss.
const selectionOriginX, selectionOriginY = 10, 5

// newSelectableModel is a raw document with line numbers, drawn at a known
// origin, with the chords a host binds.
func newSelectableModel(t *testing.T, width, height int, lines ...string) *Model {
	t.Helper()
	m := newTestModel(t)
	m.loading = false
	m.rendered = false
	m.result.Content = strings.Join(lines, "\n")
	m.result.HighlightedLines = lines
	m.SetSize(width, height)
	m.SetOrigin(selectionOriginX, selectionOriginY)
	m.SetSelection(textselect.Keys{Copy: "alt+c", SelectAll: "ctrl+a"}, false)
	return m
}

// contentX is the screen column a text column is drawn at, which is the gutter's
// width past the pane's own left edge.
func (m *Model) contentX(col int) int {
	return selectionOriginX + m.display().gutterWidth + col
}

func selectPress(x, y int) mouse.MouseAction {
	return mouse.MouseAction{Type: mouse.ActionClick, X: x, Y: y}
}

func selectDrag(x, y int) mouse.MouseAction {
	return mouse.MouseAction{Type: mouse.ActionDrag, X: x, Y: y}
}

func selectRelease(x, y int) mouse.MouseAction {
	return mouse.MouseAction{Type: mouse.ActionDragEnd, X: x, Y: y}
}

// highlightColumn is the screen column the selection highlight opens at, or -1
// when the row carries none.
func highlightColumn(row string) int {
	idx := strings.Index(row, ui.GetSelectionBgANSI())
	if idx < 0 {
		return -1
	}
	return ansi.StringWidth(row[:idx])
}

func TestSelectionDragHighlightsTextAndLeavesTheGutterAlone(t *testing.T) {
	m := newSelectableModel(t, 40, 3,
		"\x1b[31malpha\x1b[0m beta", "second line", "third line")

	m.HandleSelectionMouse(selectPress(m.contentX(0), selectionOriginY))
	if result := m.HandleSelectionMouse(selectDrag(m.contentX(5), selectionOriginY+1)); !result.Changed {
		t.Fatal("a drag across two rows changed nothing")
	}
	if result := m.HandleSelectionMouse(selectRelease(m.contentX(5), selectionOriginY+1)); result.ClickThrough {
		t.Error("a drag resolved to a click; a drag selects and never activates")
	}

	rows := strings.Split(m.View(), "\n")
	gutter := m.display().gutterWidth
	if got := highlightColumn(rows[0]); got != gutter {
		t.Errorf("highlight opens at column %d, want %d — the gutter is never selected", got, gutter)
	}
	if strings.Contains(ansi.Strip(rows[0][:strings.Index(rows[0], ui.GetSelectionBgANSI())]), "alpha") {
		t.Error("text was drawn before the highlight opened")
	}
	if got := highlightColumn(rows[2]); got != -1 {
		t.Errorf("row outside the selection carries a highlight at column %d", got)
	}

	text := m.SelectionText()
	if len(text) != 2 || text[0] != "alpha beta" || text[1] != "second" {
		t.Fatalf("selected text = %#v, want the visible text without gutter or styling", text)
	}
	for _, line := range text {
		if ansi.Strip(line) != line {
			t.Errorf("selected line %q still carries the styling it was drawn with", line)
		}
	}
}

func TestSelectionIsNeverWrittenIntoTheCachedLayout(t *testing.T) {
	m := newSelectableModel(t, 40, 2, "alpha beta", "second line")

	m.HandleSelectionMouse(selectPress(m.contentX(0), selectionOriginY))
	m.HandleSelectionMouse(selectDrag(m.contentX(4), selectionOriginY))
	m.HandleSelectionMouse(selectRelease(m.contentX(4), selectionOriginY))
	_ = m.View()

	for i, row := range m.display().rows {
		if strings.Contains(row, ui.GetSelectionBgANSI()) {
			t.Fatalf("cached row %d = %q holds a highlight; it belongs to the frame, not the layout", i, row)
		}
	}
}

func TestSelectionColumnsMatchTabsAsTheyAreDrawn(t *testing.T) {
	m := newSelectableModel(t, 40, 1, "\tword")

	// The gutter shifts every tab stop, so the word begins three columns past
	// the gutter rather than eight.
	m.HandleSelectionMouse(mouse.MouseAction{
		Type: mouse.ActionDoubleClick, X: m.contentX(3), Y: selectionOriginY,
	})
	if got := m.SelectionText(); len(got) != 1 || got[0] != "word" {
		t.Fatalf("double click selected %#v, want the word under the pointer", got)
	}

	row := strings.Split(m.View(), "\n")[0]
	if got, want := highlightColumn(row), m.display().gutterWidth+3; got != want {
		t.Errorf("highlight opens at column %d, want %d — where the word is drawn", got, want)
	}
}

func TestSelectionRunsOverWrappedRowsAsDrawn(t *testing.T) {
	// 13 columns leaves 8 for the text past a five-column gutter, so the line
	// below is laid out as two visual rows the engine never has to know about.
	m := newSelectableModel(t, 13, 2, "alpha beta")
	m.SetWrap(true)
	if got := len(m.display().rows); got != 2 {
		t.Fatalf("laid out %d rows, want the line wrapped in two", got)
	}

	// A drag from the first cell to past the end of the second row selects both
	// rows whole, which is what the two of them read as on screen.
	m.HandleSelectionMouse(selectPress(m.contentX(0), selectionOriginY))
	m.HandleSelectionMouse(selectDrag(m.contentX(20), selectionOriginY+1))
	m.HandleSelectionMouse(selectRelease(m.contentX(20), selectionOriginY+1))

	got := m.SelectionText()
	rows := m.display().rows
	if len(got) != 2 || got[0] != ansi.Strip(rows[0]) || got[1] != ansi.Strip(rows[1]) {
		t.Fatalf("selected %#v, want the visual rows as drawn (%#v)", got, rows)
	}
}

func TestSelectionStopsAtTheEdgeOfWhatIsDrawn(t *testing.T) {
	// Wrap is off, so a line longer than the pane keeps its whole text in the
	// layout and is cut at the pane's edge on the way to the screen. A drag over
	// the pane beside this one must not reach the cut-off columns.
	long := strings.Repeat("abcdefghij", 10)
	m := newSelectableModel(t, 40, 2, long, long)
	drawn := m.width - m.display().gutterWidth

	m.HandleSelectionMouse(selectPress(m.contentX(0), selectionOriginY))
	m.HandleSelectionMouse(selectDrag(m.contentX(drawn+25), selectionOriginY+1))
	m.HandleSelectionMouse(selectRelease(m.contentX(drawn+25), selectionOriginY+1))

	text := m.SelectionText()
	if len(text) != 2 {
		t.Fatalf("selected %#v, want both rows", text)
	}
	for i, line := range text {
		if got := ansi.StringWidth(line); got != drawn {
			t.Errorf("row %d copied %d columns, want the %d that were drawn: %q", i, got, drawn, line)
		}
	}
	for i, row := range strings.Split(m.View(), "\n") {
		if highlightColumn(row) != m.display().gutterWidth {
			t.Errorf("row %d was highlighted from column %d, want the whole drawn row", i, highlightColumn(row))
		}
	}
}

func TestSelectionInRenderedMarkdownHasNoGutter(t *testing.T) {
	m := newTestModel(t)
	m.loading = false
	m.result.Content = "plain body text\n"
	m.SetSize(40, 3)
	m.SetOrigin(selectionOriginX, selectionOriginY)
	m.SetSelection(textselect.Keys{Copy: "alt+c", SelectAll: "ctrl+a"}, false)
	if got := m.display().gutterWidth; got != 0 {
		t.Fatalf("rendered markdown reserved a %d-column gutter", got)
	}

	all := m.HandleSelectionKey(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	if !all.Changed {
		t.Fatal("select-all selected nothing")
	}
	if got := strings.Join(m.SelectionText(), "\n"); !strings.Contains(got, "plain body text") {
		t.Fatalf("selected %q, want the rendered body", got)
	}
}

func TestSelectionSurvivesScrolling(t *testing.T) {
	m := newSelectableModel(t, 40, 2, "one", "two", "three", "four", "five")
	m.Scroll(2)

	// The third row, which is the first one on screen at this offset.
	m.HandleSelectionMouse(mouse.MouseAction{
		Type: mouse.ActionTripleClick, X: m.contentX(1), Y: selectionOriginY,
	})
	m.HandleSelectionMouse(selectRelease(m.contentX(1), selectionOriginY))
	if got := m.SelectionText(); len(got) != 1 || got[0] != "three" {
		t.Fatalf("triple click selected %#v, want the whole line", got)
	}

	m.Scroll(-2)
	if got := m.SelectionText(); len(got) != 1 || got[0] != "three" {
		t.Errorf("after scrolling away the selection reads %#v, want the rows it was made in", got)
	}
	rows := strings.Split(m.View(), "\n")
	if highlightColumn(rows[0]) != -1 || highlightColumn(rows[1]) != -1 {
		t.Error("the highlight followed the viewport instead of staying on its own rows")
	}
	m.Scroll(2)
	if got := highlightColumn(strings.Split(m.View(), "\n")[0]); got != m.display().gutterWidth {
		t.Errorf("scrolling back left the highlight at column %d, want it where it was made", got)
	}
}

func TestSelectionClearsWhenTheContentIsRelaidOut(t *testing.T) {
	for _, tc := range []struct {
		name  string
		apply func(m *Model)
	}{
		{"wrap toggle", func(m *Model) { m.ToggleWrap() }},
		{"width change", func(m *Model) { m.SetSize(30, 2) }},
		{"render mode", func(m *Model) { m.SetRendered(true) }},
		{"content replaced", func(m *Model) {
			m.result.HighlightedLines = []string{"replaced"}
			m.invalidateRender()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newSelectableModel(t, 40, 2, "alpha beta", "second line")
			m.HandleSelectionMouse(selectPress(m.contentX(0), selectionOriginY))
			m.HandleSelectionMouse(selectDrag(m.contentX(4), selectionOriginY))
			m.HandleSelectionMouse(selectRelease(m.contentX(4), selectionOriginY))
			if !m.HasSelection() {
				t.Fatal("the drag selected nothing")
			}
			tc.apply(m)
			if m.HasSelection() {
				t.Errorf("the selection outlived the rows it was made in (%#v)", m.SelectionText())
			}
		})
	}
}

func TestPressWithoutMotionStaysAClick(t *testing.T) {
	m := newSelectableModel(t, 40, 2, "alpha beta", "second line")

	m.HandleSelectionMouse(selectPress(m.contentX(3), selectionOriginY))
	result := m.HandleSelectionMouse(selectRelease(m.contentX(3), selectionOriginY))
	if !result.ClickThrough {
		t.Error("a press that never moved did not resolve to a click")
	}
	if m.HasSelection() {
		t.Errorf("a click left a selection behind: %#v", m.SelectionText())
	}
	if strings.Contains(m.View(), ui.GetSelectionBgANSI()) {
		t.Error("a click highlighted something")
	}
}

func TestPressOnTheGutterIsNotASelection(t *testing.T) {
	m := newSelectableModel(t, 40, 2, "alpha beta", "second line")

	result := m.HandleSelectionMouse(selectPress(selectionOriginX, selectionOriginY))
	if result.Handled {
		t.Error("a press on the line-number column was answered as a selection gesture")
	}
	if m.HasSelection() {
		t.Error("a press on the line-number column selected something")
	}
}

func TestSelectionChordsCopyAndSelectAll(t *testing.T) {
	m := newSelectableModel(t, 40, 2, "alpha beta", "second line")

	empty := m.HandleSelectionKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModAlt})
	if !empty.Handled || !empty.CopyAsked || empty.Copy != nil {
		t.Fatalf("copy with nothing selected = %#v, want an asked-for copy with nothing to give", empty)
	}

	all := m.HandleSelectionKey(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	if !all.Handled || !all.Changed {
		t.Fatalf("select-all = %#v, want the whole document selected", all)
	}
	copied := m.HandleSelectionKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModAlt})
	if len(copied.Copy) != 2 || copied.Copy[0] != "alpha beta" || copied.Copy[1] != "second line" {
		t.Fatalf("copied %#v, want every row without its gutter", copied.Copy)
	}
}

func TestCopyOnSelectCopiesWhenTheDragEnds(t *testing.T) {
	m := newSelectableModel(t, 40, 2, "alpha beta", "second line")
	m.SetSelection(textselect.Keys{Copy: "alt+c", SelectAll: "ctrl+a"}, true)

	m.HandleSelectionMouse(selectPress(m.contentX(0), selectionOriginY))
	m.HandleSelectionMouse(selectDrag(m.contentX(4), selectionOriginY))
	result := m.HandleSelectionMouse(selectRelease(m.contentX(4), selectionOriginY))
	if !result.CopyAsked || len(result.Copy) != 1 || result.Copy[0] != "alpha" {
		t.Fatalf("drag end under copy-on-select = %#v, want the selection copied", result)
	}
}

func TestDragPastTheBottomScrollsTheDocument(t *testing.T) {
	m := newSelectableModel(t, 40, 2, "one", "two", "three", "four", "five")

	m.HandleSelectionMouse(selectPress(m.contentX(0), selectionOriginY))
	result := m.HandleSelectionMouse(selectDrag(m.contentX(0), selectionOriginY+4))
	if result.AutoScroll <= 0 {
		t.Fatalf("a drag past the last row asked for %d rows of scroll", result.AutoScroll)
	}
	if m.ScrollOffset() != result.AutoScroll {
		t.Errorf("scroll offset = %d, want the %d rows the drag asked for", m.ScrollOffset(), result.AutoScroll)
	}

	// Nothing left to reveal: the drag keeps asking, the document does not move,
	// and a host that persists the offset is told there is nothing to save.
	m.Scroll(len(m.display().rows))
	again := m.HandleSelectionMouse(selectDrag(m.contentX(0), selectionOriginY+4))
	if again.AutoScroll != 0 {
		t.Errorf("a drag at the last row reported %d rows of scroll, want the 0 it applied", again.AutoScroll)
	}
}

func TestEscapeClearsTheSelectionAndNothingElse(t *testing.T) {
	m := newSelectableModel(t, 40, 2, "alpha beta", "second line")
	esc := tea.KeyPressMsg{Code: tea.KeyEscape}

	if result := m.HandleSelectionKey(esc); result.Handled {
		t.Error("escape was answered with nothing selected; the pane's own escape must still reach it")
	}

	m.HandleSelectionMouse(selectPress(m.contentX(0), selectionOriginY))
	m.HandleSelectionMouse(selectDrag(m.contentX(4), selectionOriginY))
	m.HandleSelectionMouse(selectRelease(m.contentX(4), selectionOriginY))
	if !m.HasSelection() {
		t.Fatal("the drag selected nothing")
	}
	if result := m.HandleSelectionKey(esc); !result.Handled {
		t.Fatal("escape did not answer a live selection")
	}
	if m.HasSelection() {
		t.Error("escape left the selection behind")
	}
}

func TestClearSelectionsExceptKeepsOneAlive(t *testing.T) {
	kept := newSelectableModel(t, 40, 2, "alpha beta", "second line")
	dropped := newSelectableModel(t, 40, 2, "alpha beta", "second line")
	for _, m := range []*Model{kept, dropped} {
		m.HandleSelectionKey(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
		if !m.HasSelection() {
			t.Fatal("select-all selected nothing")
		}
	}

	tabs := Tabs{Items: []Item{{View: kept}, {View: dropped}, {View: nil}}}
	tabs.ClearSelectionsExcept(kept)

	if !kept.HasSelection() {
		t.Error("the document the gesture belongs to lost its selection")
	}
	if dropped.HasSelection() {
		t.Error("a second document kept a selection; only one is ever alive")
	}
}
