package notes

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/ui"
)

// finishedSelectionPlugin is a notes editor with a completed drag selection over
// the preview, ready for handleMouseDragEnd.
func finishedSelectionPlugin(t *testing.T, copyOnSelect bool) *Plugin {
	t.Helper()
	p := wheelTestPlugin(t, 1)
	cfg := config.Default()
	cfg.Selection.CopyOnSelect = copyOnSelect
	p.ctx = &plugin.Context{Config: cfg}
	p.previewMode = true
	p.notes[0] = Note{ID: "a", Content: "hello world"}
	p.editorNote = &p.notes[0]
	p.previewLines = []string{"hello world"}
	p.selection.PrepareDrag(0, 0, mouse.Rect{X: 0, Y: 0, W: p.width, H: p.height})
	p.selection.HandleDrag(0, 4)
	p.mouseHandler.StartDrag(editorX, 1, regionEditorLine, 0)
	return p
}

func TestNotesDragEndDoesNotCopyByDefault(t *testing.T) {
	p := finishedSelectionPlugin(t, false)

	p, cmd := p.handleMouseDragEnd()

	if cmd != nil {
		t.Error("a finished drag copied without being asked to")
	}
	if !p.selection.HasSelection() {
		t.Error("a finished drag lost the selection it made")
	}
}

func TestNotesDragEndCopiesWhenConfigured(t *testing.T) {
	p := finishedSelectionPlugin(t, true)

	if _, cmd := p.handleMouseDragEnd(); cmd == nil {
		t.Error("selection.copyOnSelect did not copy a finished drag")
	}
}

func TestNotesCopyOnSelectIsOffWithoutAConfig(t *testing.T) {
	p := finishedSelectionPlugin(t, false)
	p.ctx = nil
	if p.copyOnSelect() {
		t.Error("copy-on-select answered yes with no config to answer from")
	}
	p.ctx = &plugin.Context{}
	if p.copyOnSelect() {
		t.Error("copy-on-select answered yes with an empty context")
	}
}

// A cut has to read its selection before the delete removes it. Ordering the
// two the other way leaves nothing to copy, and the text is gone from the note
// as well as absent from the clipboard — so the copy half must still be there
// alongside the delete.
func TestNotesCutCopiesTheSelectionItRemoves(t *testing.T) {
	p := newEditPlugin(t, "hello world")
	p.selection.SelectRange(ui.SelectionPoint{Line: 0, Col: 0}, ui.SelectionPoint{Line: 0, Col: 5}, false)

	_, cmd := p.cutEditorSelection()
	if cmd == nil {
		t.Fatal("a cut with a selection did nothing")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("cut commands = %#v, want a copy alongside the delete", cmd())
	}
	if got := p.editorTextarea.Value(); got != " world" {
		t.Errorf("note after the cut = %q, want the selection removed", got)
	}
}
