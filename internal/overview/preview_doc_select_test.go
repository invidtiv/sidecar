package overview

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/terminallink"
	"github.com/marcus/sidecar/internal/termpreview"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// previewDocSelectFixture opens main.go — raw, so it is numbered the way a
// document with a gutter is — in the preview's document pane, and draws the
// surface so the leaf has both its hit region and its origin.
func previewDocSelectFixture(t *testing.T, content string) *Model {
	t.Helper()
	stubPreviewTd(t)
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	path := filepath.Join(m.catalog["a"].Path, "main.go")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, m, m.openPreviewDoc(terminallink.Span{Kind: terminallink.KindFile, Value: "main.go"}))
	if m.preview.doc == nil || m.preview.doc.view() == nil {
		t.Fatal("main.go did not open in the preview's document pane")
	}
	m.WorkspacesView(previewWide, previewTall)
	return m
}

// previewDocTextCell is the screen position of a column of a document row, past
// the leaf's header and its line-number gutter.
func previewDocTextCell(t *testing.T, m *Model, lines, col, row int) (int, int) {
	t.Helper()
	for _, region := range m.workspacesMouse.HitMap.Regions() {
		if kind, ok := region.Data.(string); !ok || kind != previewDocRegionKind {
			continue
		}
		gutter := docview.NewGutterForWidth(lines, region.Rect.W).Width()
		return region.Rect.X + gutter + col, region.Rect.Y + termpreview.HeaderRows + row
	}
	t.Fatal("the document leaf registered no hit region")
	return 0, 0
}

func previewMouse(m *Model, msg tea.Msg) tea.Cmd { return m.WorkspacesMouse(msg) }

func TestPreviewDocDragSelectsTheTextUnderThePointer(t *testing.T) {
	m := previewDocSelectFixture(t, "alpha beta\nsecond line\n")

	x, y := previewDocTextCell(t, m, 2, 0, 0)
	previewMouse(m, tea.MouseClickMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft}))
	previewMouse(m, tea.MouseMotionMsg(tea.Mouse{X: x + 4, Y: y + 1, Button: tea.MouseLeft}))
	previewMouse(m, tea.MouseReleaseMsg(tea.Mouse{X: x + 4, Y: y + 1, Button: tea.MouseLeft}))

	got := m.preview.doc.view().SelectionText()
	if len(got) != 2 || got[0] != "alpha beta" || got[1] != "secon" {
		t.Fatalf("selected text = %#v, want the visible rows without their gutters", got)
	}
}

func TestPreviewDocClickWithoutDragStaysAClick(t *testing.T) {
	m := previewDocSelectFixture(t, "alpha beta\nsecond line\n")
	run(t, m, m.focusList())

	x, y := previewDocTextCell(t, m, 2, 3, 0)
	previewMouse(m, tea.MouseClickMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft}))
	previewMouse(m, tea.MouseReleaseMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft}))

	if !m.preview.doc.focused {
		t.Error("a click did not focus the document pane")
	}
	if m.preview.doc.view().HasSelection() {
		t.Errorf("a click selected %#v", m.preview.doc.view().SelectionText())
	}
}

func TestPreviewDocEscapeClearsTheSelectionBeforeClosingThePane(t *testing.T) {
	m := previewDocSelectFixture(t, "alpha beta\nsecond line\n")

	x, y := previewDocTextCell(t, m, 2, 0, 0)
	previewMouse(m, tea.MouseClickMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft}))
	previewMouse(m, tea.MouseMotionMsg(tea.Mouse{X: x + 4, Y: y, Button: tea.MouseLeft}))
	previewMouse(m, tea.MouseReleaseMsg(tea.Mouse{X: x + 4, Y: y, Button: tea.MouseLeft}))
	if !m.preview.doc.view().HasSelection() {
		t.Fatal("the drag selected nothing")
	}

	handled, _ := m.previewDocKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !handled {
		t.Fatal("escape was not handled by the document pane")
	}
	if m.preview.doc == nil {
		t.Fatal("escape closed the pane instead of clearing its selection")
	}
	if m.preview.doc.view().HasSelection() {
		t.Error("escape left the selection up")
	}
}

func TestPreviewDocCopyChordCopiesTheSelection(t *testing.T) {
	m := previewDocSelectFixture(t, "alpha beta\nsecond line\n")

	x, y := previewDocTextCell(t, m, 2, 0, 0)
	previewMouse(m, tea.MouseClickMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft}))
	previewMouse(m, tea.MouseMotionMsg(tea.Mouse{X: x + 4, Y: y, Button: tea.MouseLeft}))
	previewMouse(m, tea.MouseReleaseMsg(tea.Mouse{X: x + 4, Y: y, Button: tea.MouseLeft}))

	handled, cmd := m.previewDocKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModAlt})
	if !handled || cmd == nil {
		t.Fatalf("copy chord handled=%v cmd=%v, want the shared copy pipeline", handled, cmd)
	}
	if got := m.preview.doc.view().SelectionText(); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("copy dropped the selection: %#v", got)
	}
}
