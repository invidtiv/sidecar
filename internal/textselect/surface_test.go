package textselect

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/ui"
)

// fakeSource is the whole of what a surface has to write to become selectable:
// where its content is drawn, and what its rows say. Its rows carry styling, as
// every real surface's do.
type fakeSource struct {
	rows   []string
	rect   mouse.Rect
	scroll int
}

func (s *fakeSource) ContentRect() mouse.Rect { return s.rect }
func (s *fakeSource) LineCount() int          { return len(s.rows) }
func (s *fakeSource) Scroll() int             { return s.scroll }
func (s *fakeSource) TabWidth() int           { return DefaultTabWidth }

func (s *fakeSource) Line(i int) string {
	if i < 0 || i >= len(s.rows) {
		return ""
	}
	return s.rows[i]
}

// newSource draws three styled rows at (2,1), the way a host with a border and
// a header does.
func newSource() *fakeSource {
	return &fakeSource{
		rows: []string{
			"\x1b[31malpha beta\x1b[0m gamma",
			"second line",
			"third line",
		},
		rect: mouse.Rect{X: 2, Y: 1, W: 40, H: 3},
	}
}

func press(x, y int) mouse.MouseAction {
	return mouse.MouseAction{Type: mouse.ActionClick, X: x, Y: y}
}

func drag(x, y int) mouse.MouseAction {
	return mouse.MouseAction{Type: mouse.ActionDrag, X: x, Y: y}
}

func release(x, y int) mouse.MouseAction {
	return mouse.MouseAction{Type: mouse.ActionDragEnd, X: x, Y: y}
}

func TestSurfaceDragHighlightsRowsAndCopiesWhatTheyRead(t *testing.T) {
	src := newSource()
	var surface Surface

	surface.HandleMouse(press(2+6, 1), src)
	if result := surface.HandleMouse(drag(2+5, 1+1), src); !result.Changed {
		t.Fatal("a drag across two rows changed nothing")
	}
	result := surface.HandleMouse(release(2+5, 1+1), src)
	if result.ClickThrough {
		t.Error("a drag resolved to a click; a drag selects and never activates")
	}

	decorated := surface.DecorateRow(ui.ExpandTabs(src.Line(0), DefaultTabWidth), 0)
	if decorated == src.Line(0) || !strings.Contains(decorated, ui.GetSelectionBgANSI()) {
		t.Errorf("first selected row = %q, want the selection highlight injected", decorated)
	}
	if unselected := surface.DecorateRow(src.Line(2), 2); unselected != src.Line(2) {
		t.Errorf("row outside the selection = %q, want it untouched", unselected)
	}

	text := surface.SelectedText(src)
	if len(text) != 2 || text[0] != "beta gamma" || text[1] != "second" {
		t.Errorf("selected text = %q, want the visible text without its styling", text)
	}
	if strings.Contains(strings.Join(text, ""), "\x1b") {
		t.Errorf("selected text = %q, want no styling on the clipboard", text)
	}
}

func TestSurfaceDoubleClickSnapsToTheWord(t *testing.T) {
	src := newSource()
	var surface Surface

	action := press(2+7, 1)
	action.Type = mouse.ActionDoubleClick
	if result := surface.HandleMouse(action, src); !result.Changed {
		t.Fatal("a double click selected nothing")
	}
	if text := surface.SelectedText(src); len(text) != 1 || text[0] != "beta" {
		t.Errorf("selected text = %q, want the word under the pointer", text)
	}
}

func TestSurfaceClickWithoutDragActivatesAndSelectsNothing(t *testing.T) {
	src := newSource()
	var surface Surface

	surface.HandleMouse(press(2+3, 1+1), src)
	result := surface.HandleMouse(release(2+3, 1+1), src)
	if !result.ClickThrough {
		t.Error("a click without motion did not reach the host's own click behaviour")
	}
	if surface.HasSelection() {
		t.Error("a click without motion left a selection behind")
	}
	if row := surface.DecorateRow(src.Line(1), 1); row != src.Line(1) {
		t.Errorf("row = %q, want it undecorated once nothing is selected", row)
	}
}

func TestSurfaceDragPastTheBottomEdgeAsksForScroll(t *testing.T) {
	src := newSource()
	var surface Surface

	surface.HandleMouse(press(2+1, 1), src)
	result := surface.HandleMouse(drag(2+1, 1+9), src)
	if result.AutoScroll <= 0 {
		t.Errorf("auto scroll = %d, want the host asked to scroll down", result.AutoScroll)
	}
}

func TestSurfaceSelectAllAndCopyChords(t *testing.T) {
	src := newSource()
	surface := Surface{Keys: Keys{Copy: "alt+c", SelectAll: "ctrl+a"}}

	selectAll := surface.HandleKey(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl}, src)
	if !selectAll.Handled || !selectAll.Changed {
		t.Fatalf("select-all result = %+v, want it answered", selectAll)
	}
	copied := surface.HandleKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModAlt}, src)
	if !copied.CopyAsked {
		t.Fatalf("copy result = %+v, want a copy asked for", copied)
	}
	if got := SelectionText(copied.Copy); got != "alpha beta gamma\nsecond line\nthird line" {
		t.Errorf("copied text = %q, want every row as it reads on screen", got)
	}

	surface.Clear()
	if surface.HasSelection() {
		t.Error("Clear left a selection behind")
	}
	if empty := surface.HandleKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModAlt}, src); !empty.CopyAsked || empty.Copy != nil {
		t.Errorf("copy with nothing selected = %+v, want the request with no text: the notice says so", empty)
	}
}

func TestSurfaceCopyOnSelectCopiesWhatTheDragFinished(t *testing.T) {
	src := newSource()
	surface := Surface{CopyOnSelect: true}

	surface.HandleMouse(press(2+6, 1), src)
	surface.HandleMouse(drag(2+5, 1+1), src)
	result := surface.HandleMouse(release(2+5, 1+1), src)

	if !result.CopyAsked {
		t.Fatalf("drag end = %+v, want copy-on-select to have asked for a copy", result)
	}
	if got := SelectionText(result.Copy); got != "beta gamma\nsecond" {
		t.Errorf("copied text = %q, want the selection the drag finished", got)
	}
}

func TestSurfaceWithoutCopyOnSelectLeavesTheClipboardAlone(t *testing.T) {
	src := newSource()
	var surface Surface

	surface.HandleMouse(press(2+6, 1), src)
	surface.HandleMouse(drag(2+5, 1+1), src)

	if result := surface.HandleMouse(release(2+5, 1+1), src); result.CopyAsked || result.Copy != nil {
		t.Errorf("drag end = %+v, want a selection that copies nothing unasked", result)
	}
}

func TestSurfaceCopyOnSelectIgnoresAClickThatSelectedNothing(t *testing.T) {
	src := newSource()
	surface := Surface{CopyOnSelect: true}

	surface.HandleMouse(press(2+3, 1+1), src)

	if result := surface.HandleMouse(release(2+3, 1+1), src); result.CopyAsked {
		t.Errorf("click = %+v, want no copy: a click selects nothing", result)
	}
}

// A release can be lost — the pointer leaves the window, a modal opens. The
// surface must be told, or it answers the next drag anywhere on screen as an
// extension of the selection it is still holding open.
func TestSurfaceAbandonEndsAGestureNoReleaseEnded(t *testing.T) {
	src := newSource()
	var surface Surface

	surface.HandleMouse(press(2+6, 1), src)
	surface.HandleMouse(drag(2+5, 1+1), src)
	if result := surface.Abandon(); !result.Handled {
		t.Fatalf("abandon = %+v, want the gesture ended", result)
	}

	before := surface.SelectedText(src)
	if result := surface.HandleMouse(drag(2+9, 1+2), src); result.Handled {
		t.Errorf("drag after an abandoned gesture = %+v, want it left alone", result)
	}
	if after := surface.SelectedText(src); len(after) != len(before) {
		t.Errorf("selection = %q, want the abandoned one untouched at %q", after, before)
	}
	if result := surface.Abandon(); result.Handled {
		t.Errorf("abandon with no gesture in flight = %+v, want nothing claimed", result)
	}
}
