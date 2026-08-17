package notes

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
)

// wheelTestPlugin builds a Notes plugin with a rendered-size layout and the
// given number of notes, without touching a real store on disk.
func wheelTestPlugin(t *testing.T, noteCount int) *Plugin {
	t.Helper()
	p := New()
	p.store = &Store{}
	p.editorTextarea = textarea.New()
	p.width, p.height = 120, 30
	p.listWidth = 30
	p.notes = make([]Note, noteCount)
	for i := range p.notes {
		p.notes[i] = Note{ID: string(rune('a' + i)), Content: "note"}
	}
	return p
}

func wheelMsg(x, y int, up bool) tea.MouseWheelMsg {
	button := tea.MouseWheelDown
	if up {
		button = tea.MouseWheelUp
	}
	return tea.MouseWheelMsg{X: x, Y: y, Button: button}
}

const (
	listX   = 5   // inside the list pane
	editorX = 100 // inside the editor pane
)

func TestNotesWheelAtBoundaryList(t *testing.T) {
	tests := []struct {
		name      string
		noteCount int
		cursor    int
		up        bool
		want      bool
	}{
		{name: "top of list, up", noteCount: 10, cursor: 0, up: true, want: true},
		{name: "top of list, down", noteCount: 10, cursor: 0, up: false},
		{name: "middle, up", noteCount: 10, cursor: 5, up: true},
		{name: "middle, down", noteCount: 10, cursor: 5, up: false},
		{name: "bottom, down", noteCount: 10, cursor: 9, up: false, want: true},
		{name: "bottom, up", noteCount: 10, cursor: 9, up: true},
		{name: "empty list, up", noteCount: 0, cursor: 0, up: true, want: true},
		{name: "empty list, down", noteCount: 0, cursor: 0, up: false, want: true},
		{name: "single note, down", noteCount: 1, cursor: 0, up: false, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := wheelTestPlugin(t, tt.noteCount)
			p.cursor = tt.cursor
			if got := p.WheelAtBoundary(wheelMsg(listX, 5, tt.up)); got != tt.want {
				t.Fatalf("WheelAtBoundary = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNotesWheelReverseAfterListBoundary(t *testing.T) {
	p := wheelTestPlugin(t, 10)
	p.cursor = 0
	if !p.WheelAtBoundary(wheelMsg(listX, 5, true)) {
		t.Fatal("expected top boundary")
	}
	if p.WheelAtBoundary(wheelMsg(listX, 5, false)) {
		t.Fatal("reverse event after boundary must be movable")
	}
}

// A wheel that cannot move the cursor must not reload the editor: that is the
// expensive half of an inertia tail.
func TestNotesWheelAtListBoundaryDoesNotLoadNote(t *testing.T) {
	for _, tt := range []struct {
		name   string
		cursor int
		up     bool
	}{
		{name: "top", cursor: 0, up: true},
		{name: "bottom", cursor: 9, up: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := wheelTestPlugin(t, 10)
			p.cursor = tt.cursor
			// Deliberately mismatched: a reload would swap this for the note
			// under the cursor.
			other := p.notes[3]
			p.editorNote = &other
			p.previewLines = []string{"unchanged"}

			p2, _ := p.handleMouseScroll(p.mouseHandler.HandleMouse(wheelMsg(listX, 5, tt.up)))

			if p2.cursor != tt.cursor {
				t.Fatalf("cursor moved to %d", p2.cursor)
			}
			if p2.editorNote == nil || p2.editorNote.ID != other.ID {
				t.Fatalf("editor note was reloaded at the boundary: %+v", p2.editorNote)
			}
			if strings.Join(p2.previewLines, "") != "unchanged" {
				t.Fatalf("preview lines were rebuilt: %v", p2.previewLines)
			}
		})
	}
}

func TestNotesWheelMovesCursorAndLoadsNote(t *testing.T) {
	p := wheelTestPlugin(t, 10)
	p.cursor = 0
	p2, _ := p.handleMouseScroll(p.mouseHandler.HandleMouse(wheelMsg(listX, 5, false)))
	if p2.cursor != 3 {
		t.Fatalf("cursor = %d, want 3", p2.cursor)
	}
	if p2.editorNote == nil || p2.editorNote.ID != p.notes[3].ID {
		t.Fatalf("expected note 3 loaded, got %+v", p2.editorNote)
	}
}

func previewPlugin(t *testing.T, lines []string, wrap bool) *Plugin {
	t.Helper()
	p := wheelTestPlugin(t, 3)
	p.previewMode = true
	p.previewWrapEnabled = wrap
	p.editorNote = &p.notes[0]
	p.previewLines = lines
	return p
}

func TestNotesWheelAtBoundaryPreview(t *testing.T) {
	long := make([]string, 100)
	for i := range long {
		long[i] = "line"
	}
	height, width := previewPlugin(t, long, false).previewViewport()
	maxScroll := previewPlugin(t, long, false).previewMaxScroll(height, width)
	if maxScroll <= 0 {
		t.Fatalf("expected a scrollable preview, max = %d", maxScroll)
	}

	tests := []struct {
		name   string
		lines  []string
		offset int
		up     bool
		want   bool
	}{
		{name: "top, up", lines: long, offset: 0, up: true, want: true},
		{name: "top, down", lines: long, offset: 0, up: false},
		{name: "middle, up", lines: long, offset: maxScroll / 2, up: true},
		{name: "middle, down", lines: long, offset: maxScroll / 2, up: false},
		{name: "bottom, down", lines: long, offset: maxScroll, up: false, want: true},
		{name: "bottom, up", lines: long, offset: maxScroll, up: true},
		{name: "empty preview, up", lines: nil, offset: 0, up: true, want: true},
		{name: "empty preview, down", lines: nil, offset: 0, up: false, want: true},
		{name: "short preview, down", lines: []string{"one", "two"}, offset: 0, up: false, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := previewPlugin(t, tt.lines, false)
			p.previewScrollOff = tt.offset
			if got := p.WheelAtBoundary(wheelMsg(editorX, 5, tt.up)); got != tt.want {
				t.Fatalf("WheelAtBoundary = %v, want %v", got, tt.want)
			}
		})
	}
}

// With wrapping on, a start line fills the viewport with fewer logical lines,
// so the exact maximum offset is larger than len(lines)-height. The renderer
// clamp and the boundary query must both use the wrapped maximum, or the last
// lines are unreachable.
func TestNotesPreviewWrappedMaximumIsSharedWithRenderer(t *testing.T) {
	lines := make([]string, 40)
	for i := range lines {
		lines[i] = strings.Repeat("word ", 40)
	}
	wrapped := previewPlugin(t, lines, true)
	plain := previewPlugin(t, lines, false)
	height, width := wrapped.previewViewport()

	wrappedMax := wrapped.previewMaxScroll(height, width)
	plainMax := plain.previewMaxScroll(height, width)
	if wrappedMax <= plainMax {
		t.Fatalf("wrapped max %d should exceed unwrapped max %d", wrappedMax, plainMax)
	}

	// The renderer clamps to the same maximum the boundary query uses.
	wrapped.previewScrollOff = wrappedMax + 5
	wrapped.previewCursorLine = len(lines) - 1
	wrapped.ensurePreviewCursorVisibleWithHeight(height, width)
	if wrapped.previewScrollOff != wrappedMax {
		t.Fatalf("renderer clamped to %d, want %d", wrapped.previewScrollOff, wrappedMax)
	}

	wrapped.previewScrollOff = wrappedMax
	if !wrapped.WheelAtBoundary(wheelMsg(editorX, 5, false)) {
		t.Fatal("expected bottom boundary at the wrapped maximum")
	}
	if wrapped.WheelAtBoundary(wheelMsg(editorX, 5, true)) {
		t.Fatal("reverse event after boundary must be movable")
	}
}

func TestNotesWheelUnknownSurfaces(t *testing.T) {
	tests := []struct {
		name  string
		setup func(p *Plugin)
		x     int
	}{
		// Textarea edit mode cannot honestly report a viewport boundary.
		{name: "textarea edit mode", setup: func(p *Plugin) { p.previewMode = false }, x: editorX},
		{name: "inline tmux editor", setup: func(p *Plugin) { p.inlineEditMode = true }, x: editorX},
		{name: "inline tmux editor over list", setup: func(p *Plugin) { p.inlineEditMode = true }, x: listX},
		// An unrendered modal has no trustworthy geometry yet.
		{name: "task modal before render", setup: func(p *Plugin) { p.showTaskModal = true }, x: listX},
		{name: "delete modal before render", setup: func(p *Plugin) { p.showDeleteModal = true }, x: listX},
		{name: "info modal before render", setup: func(p *Plugin) { p.showInfoModal = true }, x: listX},
		{name: "no store", setup: func(p *Plugin) { p.store = nil }, x: listX},
		{name: "loading", setup: func(p *Plugin) { p.loading = true }, x: listX},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := previewPlugin(t, []string{"one"}, false)
			p.cursor = 0
			tt.setup(p)
			if p.WheelAtBoundary(wheelMsg(tt.x, 5, true)) {
				t.Fatal("unknown surface must not report a boundary")
			}
		})
	}
}

func TestNotesWheelPlaceholderPaneIsBounded(t *testing.T) {
	p := wheelTestPlugin(t, 3)
	p.editorNote = nil
	for _, up := range []bool{true, false} {
		if !p.WheelAtBoundary(wheelMsg(editorX, 5, up)) {
			t.Fatalf("placeholder pane should be bounded (up=%v)", up)
		}
	}
}

func TestNotesWheelEditAtBoundaryDoesNoWork(t *testing.T) {
	p := wheelTestPlugin(t, 1)
	p.previewMode = false
	p.editorNote = &p.notes[0]
	p.editorTextarea.SetValue("a\nb\nc")
	p.editorTextarea.MoveToBegin()
	p.updateTextareaDimensions()

	beforeLine := p.editorTextarea.Line()
	beforeMode := p.previewMode
	beforeFocus := p.editorTextarea.Focused()

	p2, _ := p.handleMouseScroll(p.mouseHandler.HandleMouse(wheelMsg(editorX, 5, true)))
	if p2.editorTextarea.Line() != beforeLine {
		t.Fatalf("wheel up at first line moved cursor to %d", p2.editorTextarea.Line())
	}
	if p2.previewMode != beforeMode {
		t.Fatal("edit-mode wheel flipped previewMode")
	}
	if p2.editorTextarea.Focused() != beforeFocus {
		t.Fatal("edit-mode wheel changed focus")
	}

	p.editorTextarea.MoveToEnd()
	last := p.editorTextarea.Line()
	p3, _ := p.handleMouseScroll(p.mouseHandler.HandleMouse(wheelMsg(editorX, 5, false)))
	if p3.editorTextarea.Line() != last {
		t.Fatalf("wheel down at last line moved cursor to %d", p3.editorTextarea.Line())
	}
}
