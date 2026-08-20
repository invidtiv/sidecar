package overview

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/termpreview"
)

// markPreviewEditing puts the pane in the state a started session leaves it in,
// without tmux: the lifecycle beyond that is inlineedit's.
func markPreviewEditing(m *Model, path string) {
	session := m.preview.doc.editor()
	session.Active = true
	session.Name = "sidecar-pane-edit-test"
	session.EditorCmd = "vim"
	session.Path = path
}

// This browser bypasses the app's context ladder, so "the preview owns the
// keyboard" has to be true of an editing document pane the way it is of a live
// terminal — otherwise the list's own keys take characters out of vim.
func TestPreviewDocEditOwnsTheKeyboard(t *testing.T) {
	m := focusedDocPreview(t)
	if m.PreviewOwnsKeyboard() {
		t.Fatal("an idle document pane claimed the keyboard")
	}
	markPreviewEditing(m, "README.md")
	if !m.PreviewOwnsKeyboard() {
		t.Fatal("an editing document pane did not claim the keyboard")
	}

	// `/` is a slash in an editor, not the browser's filter.
	if !pressWorkspaces(t, m, tea.KeyPressMsg{Code: '/', Text: "/"}) {
		t.Fatal("/ was not consumed by the live editor")
	}
	if m.WorkspacesFilterFocused() {
		t.Fatal("/ reached the workspace filter behind a live editor")
	}
}

// The editor's box is the pane's own, one row below its header. Dimension drift
// is the historical failure mode, so it is asserted against the box the render
// recorded rather than recomputed.
func TestPreviewDocEditHostAnswersFromThePaneBox(t *testing.T) {
	doc := &previewDoc{box: termpreview.Box{X: 4, Y: 3, W: 50, H: 18}}
	host := previewDocEditHost{doc}

	width, height := host.EditorViewport()
	if width != 50 || height != 18-termpreview.HeaderRows {
		t.Fatalf("viewport = %dx%d, want %dx%d", width, height, 50, 18-termpreview.HeaderRows)
	}
	x, y, ok := host.EditorOrigin()
	if !ok || x != 4 || y != 3+termpreview.HeaderRows {
		t.Fatalf("origin = (%d,%d,%v), want (4,%d,true)", x, y, ok, 3+termpreview.HeaderRows)
	}

	doc.box.H = termpreview.HeaderRows
	if _, _, ok := host.EditorOrigin(); ok {
		t.Fatal("a pane with no body still reported an origin")
	}
}

// Closing the pane with a live session asks first, and answering runs the close
// that was held.
func TestPreviewDocEditConfirmationRunsTheHeldClose(t *testing.T) {
	m := focusedDocPreview(t)
	markPreviewEditing(m, "README.md")
	m.preview.doc.editor().ShowExitConfirm = true
	m.preview.doc.editor().ConfirmSelection = 1
	closed := false
	m.preview.doc.pendingEdit = func() tea.Cmd { closed = true; return nil }

	pressWorkspaces(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.previewDocEditing() {
		t.Fatal("the session survived an exit-without-saving")
	}
	if !closed {
		t.Fatal("the held close never ran")
	}
}
