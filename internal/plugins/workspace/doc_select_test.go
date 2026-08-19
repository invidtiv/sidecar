package workspace

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/docview"
)

// docSelectFixture opens README.md raw in a document leaf and draws the frame,
// so the leaf has both the hit regions and the origin a pointer gesture needs.
func docSelectFixture(t *testing.T, content string) (*Plugin, *docPane, *PaneNode) {
	t.Helper()
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", content)
	p := docPaneTestPlugin(t, root, false)
	open := p.openTerminalPath("README.md", 1)
	if open == nil {
		t.Fatal("opening README.md returned no command")
	}
	for _, child := range open().(tea.BatchMsg) {
		if msg, ok := child().(docview.LoadedMsg); ok {
			p.applyDocLoaded(msg)
		}
	}
	doc, leaf := p.activeDocPane()
	if doc == nil || leaf == nil {
		t.Fatal("no document pane")
	}
	p.mouseHandler.Clear()
	_ = p.renderListView(p.width, p.height)
	return p, doc, leaf
}

// docTextCell is the screen position of a column of a document row, past the
// leaf's chrome, its header and its line-number gutter.
func docTextCell(t *testing.T, p *Plugin, lines, col, row int) (int, int) {
	t.Helper()
	pane := docPaneRegion(p, regionPaneLeaf)
	if pane == nil {
		t.Fatal("document leaf has no hit region")
	}
	inner := insetPanelChrome(pane.Rect)
	gutter := docview.NewGutterForWidth(lines, inner.W).Width()
	return inner.X + gutter + col, inner.Y + terminalHeaderRows + row
}

func docPress(p *Plugin, x, y int) tea.Cmd {
	return p.handleMouse(tea.MouseClickMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft}))
}

func docDrag(p *Plugin, x, y int) tea.Cmd {
	return p.handleMouse(tea.MouseMotionMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft}))
}

func docRelease(p *Plugin, x, y int) tea.Cmd {
	return p.handleMouse(tea.MouseReleaseMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft}))
}

func TestDocPaneDragSelectsTheTextUnderThePointer(t *testing.T) {
	p, doc, leaf := docSelectFixture(t, "alpha beta\nsecond line\n")

	x, y := docTextCell(t, p, 2, 0, 0)
	docPress(p, x, y)
	docDrag(p, x+4, y+1)
	docRelease(p, x+4, y+1)

	if p.paneFocus != leaf.ID {
		t.Fatalf("pane focus = %d, want the document leaf %d", p.paneFocus, leaf.ID)
	}
	got := doc.view().SelectionText()
	if len(got) != 2 || got[0] != "alpha beta" || got[1] != "secon" {
		t.Fatalf("selected text = %#v, want the visible rows without their gutters", got)
	}
	if p.docSelectLeaf != 0 {
		t.Errorf("the release left leaf %d holding a live gesture", p.docSelectLeaf)
	}
}

func TestDocPaneClickWithoutDragStaysAClick(t *testing.T) {
	p, doc, leaf := docSelectFixture(t, "alpha beta\nsecond line\n")
	p.paneFocus = terminalLeafID(p.paneRoot)

	x, y := docTextCell(t, p, 2, 3, 0)
	docPress(p, x, y)
	docRelease(p, x, y)

	if p.paneFocus != leaf.ID {
		t.Fatalf("a click did not focus the document leaf: focus=%d want=%d", p.paneFocus, leaf.ID)
	}
	if doc.view().HasSelection() {
		t.Errorf("a click selected %#v", doc.view().SelectionText())
	}
}

func TestDocPaneDoubleClickSelectsTheWord(t *testing.T) {
	p, doc, _ := docSelectFixture(t, "alpha beta\nsecond line\n")

	x, y := docTextCell(t, p, 2, 7, 0)
	docPress(p, x, y)
	docRelease(p, x, y)
	docPress(p, x, y)
	docRelease(p, x, y)

	if got := doc.view().SelectionText(); len(got) != 1 || got[0] != "beta" {
		t.Fatalf("double click selected %#v, want the word under the pointer", got)
	}
}

func TestDocPaneEscapeClearsTheSelectionBeforeHidingThePane(t *testing.T) {
	p, doc, _ := docSelectFixture(t, "alpha beta\nsecond line\n")

	x, y := docTextCell(t, p, 2, 0, 0)
	docPress(p, x, y)
	docDrag(p, x+4, y)
	docRelease(p, x+4, y)
	if !doc.view().HasSelection() {
		t.Fatal("the drag selected nothing")
	}

	handled, _ := p.handleDocKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !handled {
		t.Fatal("escape was not handled by the document pane")
	}
	if doc.view().HasSelection() {
		t.Error("escape left the selection up")
	}
	if p.activeDocPaneOrNil() == nil {
		t.Error("escape hid the pane instead of clearing its selection")
	}

	handled, cmd := p.handleDocKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !handled || cmd == nil || p.activeDocPaneOrNil() != nil {
		t.Error("a second escape did not hide the pane")
	}
}

func TestDocPaneCopyChordCopiesTheSelection(t *testing.T) {
	p, doc, _ := docSelectFixture(t, "alpha beta\nsecond line\n")

	x, y := docTextCell(t, p, 2, 0, 0)
	docPress(p, x, y)
	docDrag(p, x+4, y)
	docRelease(p, x+4, y)

	handled, cmd := p.handleDocKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModAlt})
	if !handled || cmd == nil {
		t.Fatalf("copy chord handled=%v cmd=%v, want the shared copy pipeline", handled, cmd)
	}
	if got := doc.view().SelectionText(); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("copy dropped the selection: %#v", got)
	}
}

func TestDocPaneSelectionIsExclusiveAcrossLeaves(t *testing.T) {
	p, doc, _ := docSelectFixture(t, "alpha beta\nsecond line\n")

	other := docview.New(nil)
	other.SetSize(40, 2)
	other.SetOrigin(0, 0)
	other.HandleSelectionKey(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	p.docs[99] = &docPane{leafID: 99}
	p.docs[99].tabs.Append(other)

	x, y := docTextCell(t, p, 2, 0, 0)
	docPress(p, x, y)
	docDrag(p, x+4, y)
	docRelease(p, x+4, y)

	if !doc.view().HasSelection() {
		t.Fatal("the drag selected nothing")
	}
	if other.HasSelection() {
		t.Error("a selection in one document left another document's selection up")
	}
}
