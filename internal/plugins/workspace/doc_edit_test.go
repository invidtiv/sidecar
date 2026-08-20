package workspace

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// markEditing puts a pane in the state a started session leaves it in, without
// tmux: the lifecycle beyond this point is inlineedit's and is tested there.
func markEditing(doc *docPane, path string) {
	session := doc.editor()
	session.Active = true
	session.Name = "sidecar-pane-edit-test"
	session.EditorCmd = "vim"
	session.Path = path
}

// A pane hosting an editor is its own focus context, so internal/app forwards
// every key to it — ctrl+c and the tab numbers included.
func TestDocEditIsItsOwnFocusContext(t *testing.T) {
	p, _ := docSearchPlugin(t, true)
	doc := p.focusedDocPane()
	if got := p.FocusContext(); got != "workspace-doc" {
		t.Fatalf("before editing: context %q, want workspace-doc", got)
	}
	markEditing(doc, "README.md")
	if got := p.FocusContext(); got != "workspace-doc-edit" {
		t.Fatalf("editing: context %q, want workspace-doc-edit", got)
	}
}

// The editor's viewport is the leaf's own body box, and its origin the row
// below the pane header. Dimension drift is the historical failure mode, so it
// is asserted against the numbers the frame handed the leaf rather than
// recomputed.
func TestDocEditHostAnswersFromTheLeafBox(t *testing.T) {
	doc := newDocPane(1, t.TempDir(), "surface", nil)
	doc.boxW, doc.boxH, doc.boxX, doc.boxY = 60, 20, 10, 5
	host := docEditHost{doc}

	width, height := host.EditorViewport()
	if width != 60 || height != 20-terminalHeaderRows {
		t.Fatalf("viewport = %dx%d, want %dx%d", width, height, 60, 20-terminalHeaderRows)
	}
	x, y, ok := host.EditorOrigin()
	if !ok || x != 10 || y != 5+terminalHeaderRows {
		t.Fatalf("origin = (%d,%d,%v), want (10,%d,true)", x, y, ok, 5+terminalHeaderRows)
	}

	// A leaf too short to have a body has no origin to hand out, rather than a
	// negative one the PTY would be placed against.
	doc.boxH = terminalHeaderRows
	if _, _, ok := host.EditorOrigin(); ok {
		t.Fatal("a leaf with no body still reported an origin")
	}
}

// The confirmation holds the action that raised it and runs it once the user
// says what to do with the buffer.
func TestDocEditConfirmationRunsTheHeldAction(t *testing.T) {
	p, _ := docSearchPlugin(t, true)
	doc := p.focusedDocPane()
	markEditing(doc, "README.md")
	doc.editor().ShowExitConfirm = true
	ran := false
	doc.pendingEdit = func() tea.Cmd { ran = true; return nil }

	// Exit without saving: the session goes, then the held action runs.
	doc.editor().ConfirmSelection = 1
	p.handleDocKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if doc.editing() {
		t.Fatal("the session survived an exit-without-saving")
	}
	if !ran {
		t.Fatal("the held action never ran")
	}
}

// Cancel returns to the editor and drops what was held: nothing the user did
// not ask for happens behind the dialog.
func TestDocEditConfirmationCancelKeepsTheSession(t *testing.T) {
	p, _ := docSearchPlugin(t, true)
	doc := p.focusedDocPane()
	markEditing(doc, "README.md")
	doc.editor().ShowExitConfirm = true
	doc.pendingEdit = func() tea.Cmd { t.Fatal("a cancelled action ran"); return nil }

	p.handleDocKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !doc.editing() {
		t.Fatal("cancel ended the session")
	}
	if doc.editor().ShowExitConfirm {
		t.Fatal("cancel left the confirmation up")
	}
	if doc.pendingEdit != nil {
		t.Fatal("cancel kept the held action")
	}
}

// While the confirmation is up the pane's own keys are the dialog's, and
// nothing behind it acts.
func TestDocEditConfirmationOwnsTheKeyboard(t *testing.T) {
	p, _ := docSearchPlugin(t, true)
	doc := p.focusedDocPane()
	markEditing(doc, "README.md")
	doc.editor().ShowExitConfirm = true

	handled, _ := p.handleDocKey(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if !handled {
		t.Fatal("a key reached the document behind the confirmation")
	}
	if doc.editor().ConfirmSelection != 1 {
		t.Fatalf("selection = %d, want 1", doc.editor().ConfirmSelection)
	}
}

// A leaf dropped by a route that cannot ask first (a click on the X, a shell
// switch, a reset) still owns a tmux session, and must release it rather than
// leave an editor holding the file with nothing on screen.
func TestClosingADocLeafReleasesItsEditor(t *testing.T) {
	p, _ := docSearchPlugin(t, true)
	doc := p.focusedDocPane()
	markEditing(doc, "README.md")
	session := doc.editor()

	if !p.closeContentLeaf(doc.leafID) {
		t.Fatal("the doc leaf did not close")
	}
	if session.Active {
		t.Fatal("the leaf was dropped with its editor still live")
	}
}
