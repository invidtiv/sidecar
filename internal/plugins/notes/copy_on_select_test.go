package notes

import (
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/plugin"
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
