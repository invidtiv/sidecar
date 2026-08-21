package notes

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/clip"
	"github.com/marcus/sidecar/internal/msg"
)

func seedRing(t *testing.T, texts ...string) {
	t.Helper()
	clip.ResetRecent()
	t.Cleanup(clip.ResetRecent)
	for _, text := range texts {
		// Copy records at call time; the returned command is never run, so the
		// test touches no real clipboard.
		clip.Copy(text, nil)
	}
}

func TestAltVPastesTheLastSessionCopyIntoTheEditor(t *testing.T) {
	p := newPastePlugin(t)
	p.activePane = PaneEditor
	p.previewMode = false
	p.editorNote = &p.notes[0]
	p.editorTextarea.SetValue("hello")
	p.editorTextarea.Focus()
	p.setTextareaCursorPosition(0, 5)
	seedRing(t, "yanked elsewhere")

	_, cmd := p.Update(tea.KeyPressMsg{Code: 'v', Mod: tea.ModAlt})
	if cmd == nil {
		t.Fatal("alt+v returned no command")
	}
	pasted, ok := cmd().(tea.PasteMsg)
	if !ok {
		t.Fatalf("alt+v produced %T, want tea.PasteMsg", cmd())
	}
	if pasted.Content != "yanked elsewhere" {
		t.Fatalf("paste content = %q, want the session copy", pasted.Content)
	}

	if _, _ = p.Update(pasted); !strings.Contains(p.editorTextarea.Value(), "yanked elsewhere") {
		t.Fatalf("textarea = %q, want the session copy inserted", p.editorTextarea.Value())
	}
}

func TestSuperVAlsoRepastesTheSessionCopy(t *testing.T) {
	p := newPastePlugin(t)
	p.activePane = PaneEditor
	p.previewMode = false
	p.editorNote = &p.notes[0]
	p.editorTextarea.SetValue("hello")
	p.editorTextarea.Focus()
	p.setTextareaCursorPosition(0, 5)
	seedRing(t, "super copy")

	_, cmd := p.Update(tea.KeyPressMsg{Code: 'v', Mod: tea.ModSuper})
	pasted, ok := cmd().(tea.PasteMsg)
	if !ok {
		t.Fatalf("super+v produced %T, want tea.PasteMsg", cmd())
	}
	if pasted.Content != "super copy" {
		t.Fatalf("paste content = %q, want the session copy", pasted.Content)
	}
}

func TestAltVWithAnEmptyRingFlashesInsteadOfPasting(t *testing.T) {
	p := newPastePlugin(t)
	p.activePane = PaneEditor
	p.previewMode = false
	p.editorNote = &p.notes[0]
	p.editorTextarea.SetValue("hello")
	p.editorTextarea.Focus()
	seedRing(t)

	_, cmd := p.Update(tea.KeyPressMsg{Code: 'v', Mod: tea.ModAlt})
	flash, ok := cmd().(msg.FlashMsg)
	if !ok {
		t.Fatalf("empty-ring alt+v produced %T, want flash", cmd())
	}
	if !strings.Contains(flash.Text, "Nothing copied") {
		t.Fatalf("flash = %q, want nothing-copied wording", flash.Text)
	}
	if got := p.editorTextarea.Value(); got != "hello" {
		t.Fatalf("textarea = %q, want it untouched", got)
	}
}

func TestListAltVCreatesANoteFromTheSessionCopy(t *testing.T) {
	p := newPastePlugin(t)
	p.activePane = PaneList
	p.viewFilter = FilterActive
	seedRing(t, "Yanked title\nbody from yank")

	_, cmd := p.Update(tea.KeyPressMsg{Code: 'v', Mod: tea.ModAlt})
	pasted, ok := cmd().(tea.PasteMsg)
	if !ok {
		t.Fatalf("list alt+v produced %T, want tea.PasteMsg", cmd())
	}
	_, create := p.Update(pasted)
	result, ok := create().(NoteSavedMsg)
	if !ok {
		t.Fatalf("list paste produced %T, want NoteSavedMsg", create())
	}
	if result.Err != nil {
		t.Fatalf("create failed: %v", result.Err)
	}
	if result.Note == nil || result.Note.Content != "Yanked title\nbody from yank" {
		t.Fatalf("created note = %+v, want the session copy as content", result.Note)
	}
}

func TestEmptyListAltVStillCreatesFromTheSessionCopy(t *testing.T) {
	p := newPastePlugin(t)
	p.notes = nil
	p.filteredNotes = nil
	p.cursor = 0
	p.activePane = PaneList
	p.viewFilter = FilterActive
	seedRing(t, "note from nothing")

	if len(p.getDisplayNotes()) != 0 {
		t.Fatal("the list was not empty")
	}

	_, cmd := p.Update(tea.KeyPressMsg{Code: 'v', Mod: tea.ModAlt})
	pasted, ok := cmd().(tea.PasteMsg)
	if !ok {
		t.Fatalf("empty-list alt+v produced %T, want tea.PasteMsg", cmd())
	}
	_, create := p.Update(pasted)
	result, ok := create().(NoteSavedMsg)
	if !ok {
		t.Fatalf("empty-list paste produced %T, want NoteSavedMsg", create())
	}
	if result.Err != nil {
		t.Fatalf("create failed: %v", result.Err)
	}
	if result.Note == nil || result.Note.Content != "note from nothing" {
		t.Fatalf("created note = %+v, want the session copy as content", result.Note)
	}
}

func TestAltZUndoesAndAltShiftZRedoesEdits(t *testing.T) {
	p := newEditPlugin(t, "hello")
	typeKey(p, tea.KeyPressMsg{Code: 'x', Text: "x"})
	if got := p.editorTextarea.Value(); got != "xhello" {
		t.Fatalf("after typing = %q, want xhello", got)
	}

	typeKey(p, tea.KeyPressMsg{Code: 'z', Mod: tea.ModAlt})
	if got := p.editorTextarea.Value(); got != "hello" {
		t.Fatalf("after alt+z = %q, want hello", got)
	}

	typeKey(p, tea.KeyPressMsg{Code: 'z', Mod: tea.ModAlt | tea.ModShift})
	if got := p.editorTextarea.Value(); got != "xhello" {
		t.Fatalf("after alt+shift+z = %q, want xhello", got)
	}
}

func TestSuperZJoinsTheUndoAliases(t *testing.T) {
	p := newEditPlugin(t, "hello")
	typeKey(p, tea.KeyPressMsg{Code: 'x', Text: "x"})

	typeKey(p, tea.KeyPressMsg{Code: 'z', Mod: tea.ModSuper})
	if got := p.editorTextarea.Value(); got != "hello" {
		t.Fatalf("after super+z = %q, want hello", got)
	}
}
