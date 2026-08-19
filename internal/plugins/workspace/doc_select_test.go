package workspace

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/ui"
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
	other.SetSelection(p.terminalConfig().SelectionKeys(), false)
	other.HandleSelectionKey(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	if !other.HasSelection() {
		t.Fatal("select-all in the other document selected nothing")
	}
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

func TestDocPaneSelectionIsExclusiveWithTheTerminal(t *testing.T) {
	p, doc, _ := docSelectFixture(t, "alpha beta\nsecond line\n")

	// A terminal selection is up in the pane beside the document.
	p.selection.SelectRange(
		ui.SelectionPoint{Line: 0, Col: 0}, ui.SelectionPoint{Line: 0, Col: 4}, false)

	x, y := docTextCell(t, p, 2, 0, 0)
	docPress(p, x, y)
	docDrag(p, x+4, y)
	docRelease(p, x+4, y)
	if !doc.view().HasSelection() {
		t.Fatal("the drag selected nothing")
	}
	if p.selection.HasSelection() {
		t.Error("a document selection left the terminal's highlight up beside it")
	}

	// And the other way: a gesture over the terminal takes the one live
	// selection back from the document.
	p.prepareTerminalClickOrDrag(mouse.MouseAction{
		Type: mouse.ActionClick, X: 1, Y: 3,
		Region: &mouse.Region{ID: regionPreviewPane, Rect: mouse.Rect{X: 0, Y: 2, W: 40, H: 10}},
	})
	if doc.view().HasSelection() {
		t.Error("a terminal gesture left the document's highlight up beside it")
	}
}

func TestDocPaneModalDuringADragEndsTheGesture(t *testing.T) {
	p, doc, leaf := docSelectFixture(t, "alpha beta\nsecond line\n")

	x, y := docTextCell(t, p, 2, 0, 0)
	docPress(p, x, y)
	docDrag(p, x+4, y)
	if p.docSelectLeaf != leaf.ID {
		t.Fatalf("the drag is held by leaf %d, want %d", p.docSelectLeaf, leaf.ID)
	}

	// A modal swallows the release, so nothing else ever ends this gesture.
	p.viewMode = ViewModeCreate
	p.handleMouseDragEnd(mouse.MouseAction{Type: mouse.ActionDragEnd, DragStartID: regionPaneLeaf})

	if p.docSelectLeaf != 0 {
		t.Errorf("leaf %d is still named as the gesture's, after the release was swallowed", p.docSelectLeaf)
	}
	if doc.view().AbandonSelection().Handled {
		t.Error("the document is still holding a live gesture")
	}
}

func TestDocPaneDragPastTheBottomPersistsTheScroll(t *testing.T) {
	p, doc, _ := docSelectFixture(t, strings.Repeat("a line of text\n", 200))
	saves := 0
	p.shellStartupHooks = shellStartupHooks{
		getWorkspaceState: func(string) state.WorkspaceState { return state.WorkspaceState{} },
		setWorkspaceState: func(string, state.WorkspaceState) error { saves++; return nil },
	}

	x, y := docTextCell(t, p, 200, 0, 0)
	docPress(p, x, y)
	docDrag(p, x, y+p.height)
	if doc.view().ScrollOffset() == 0 {
		t.Fatal("a drag past the bottom of the pane did not scroll the document")
	}
	if saves == 0 {
		t.Error("the drag moved the pane's offset without persisting it, unlike every other scroll of it")
	}
}
