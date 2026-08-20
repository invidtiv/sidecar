package notes

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// twoPaneNotes is the surface with a note open in the right pane, resting in
// preview rather than editing — the shape `tab` cycles.
func twoPaneNotes() *Plugin {
	p := &Plugin{
		activePane:     PaneList,
		previewMode:    true,
		editorNote:     &Note{ID: "n1", Title: "one"},
		editorTextarea: newEditorTextarea(),
	}
	return p
}

// With the centre open the toggle is a ring: the list is not the wrap point
// going forward, the note pane is, and shift+tab wraps at the other end.
func TestNotesRingWrapsAtTheNotePane(t *testing.T) {
	p := twoPaneNotes()
	if p.AtFocusCycleEnd(false) {
		t.Fatal("the list is not the end of the forward cycle; the note pane is")
	}
	if !p.AtFocusCycleEnd(true) {
		t.Fatal("the list is where a reverse cycle wraps")
	}

	p.activePane = PaneEditor
	if !p.AtFocusCycleEnd(false) {
		t.Fatal("the note pane is where a forward cycle wraps")
	}
	if p.AtFocusCycleEnd(true) {
		t.Fatal("the note pane is not the start of the ring")
	}
}

// Coming back from the centre lands on the window the toggle resumes at, and it
// gets there by running the same code the key runs — the note pane arrives
// resting, not editing.
func TestNotesFocusCycleStart(t *testing.T) {
	p := twoPaneNotes()
	p.activePane = PaneEditor
	p.FocusCycleStart(false)
	if p.activePane != PaneList {
		t.Fatalf("forward handback focused %v, want the list", p.activePane)
	}
	p.FocusCycleStart(true)
	if p.activePane != PaneEditor {
		t.Fatalf("reverse handback focused %v, want the note pane", p.activePane)
	}
	if !p.previewMode {
		t.Fatal("the handback dropped into edit mode; tab lands on the resting view")
	}
}

// With no note open there is no second window: the list is the whole ring and
// so is its own wrap point in both directions.
func TestNotesRingSkipsAnEmptyNotePane(t *testing.T) {
	p := twoPaneNotes()
	p.editorNote = nil
	if !p.AtFocusCycleEnd(false) || !p.AtFocusCycleEnd(true) {
		t.Fatal("with no note open the list is the whole ring")
	}
}

// Sub-modes keep tab — above all the editing pane, where tab saves and leaves
// the edit rather than cycling focus.
func TestNotesSubModesKeepTab(t *testing.T) {
	for _, tc := range []struct {
		name  string
		apply func(*Plugin)
	}{
		{"editing", func(p *Plugin) { p.activePane = PaneEditor; p.previewMode = false }},
		{"search", func(p *Plugin) { p.searchMode = true }},
		{"inline edit", func(p *Plugin) { p.inlineEditMode = true }},
		{"info modal", func(p *Plugin) { p.showInfoModal = true }},
		{"delete modal", func(p *Plugin) { p.showDeleteModal = true }},
		{"task modal", func(p *Plugin) { p.showTaskModal = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := twoPaneNotes()
			p.activePane = PaneEditor
			tc.apply(p)
			if p.AtFocusCycleEnd(false) || p.AtFocusCycleEnd(true) {
				t.Fatalf("%s offered the centre a tab stop", tc.name)
			}
		})
	}
}

// With the centre closed the shell never asks, and the plugin's own tab handler
// is the exact toggle it always was.
func TestNotesTabTogglesPanesUnchanged(t *testing.T) {
	p := twoPaneNotes()
	tab := tea.KeyPressMsg{Code: tea.KeyTab}
	p.handleKey(tab)
	if p.activePane != PaneEditor {
		t.Fatalf("tab from the list focused %v, want the note pane", p.activePane)
	}
	p.handleKey(tab)
	if p.activePane != PaneList {
		t.Fatalf("tab from the note pane focused %v, want the list", p.activePane)
	}
}
