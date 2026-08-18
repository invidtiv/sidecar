package notes

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/markdown"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/ui"
)

func newEditPlugin(t *testing.T, content string) *Plugin {
	t.Helper()
	p := layoutTestPlugin(t, content)
	p.ctx = &plugin.Context{Epoch: 1, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	p.activePane = PaneEditor
	p.previewMode = false
	p.markdownView = false
	p.editorTextarea = newEditorTextarea()
	p.editorTextarea.SetValue(content)
	p.updateTextareaDimensions()
	_ = p.editorTextarea.Focus()
	p.lastSavedContent = content
	p.setTextareaCursorPosition(0, 0)
	return p
}

func typeKey(p *Plugin, msg tea.KeyPressMsg) {
	_, _ = p.handleEditorKey(msg)
}

func TestShiftRightSelectsExclusiveSourceRange(t *testing.T) {
	p := newEditPlugin(t, "hello")
	typeKey(p, tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModShift})
	typeKey(p, tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModShift})
	typeKey(p, tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModShift})
	if !p.hasEditSelection() {
		t.Fatal("shift+right created no selection")
	}
	got := strings.Join(p.getSelectedText(), "\n")
	if got != "hel" {
		t.Fatalf("selection = %q, want %q", got, "hel")
	}
	if p.editorTextarea.Column() != 3 {
		t.Fatalf("caret col = %d, want 3", p.editorTextarea.Column())
	}
}

func TestTypingReplacesSelection(t *testing.T) {
	p := newEditPlugin(t, "hello")
	p.selection.SelectRange(ui.SelectionPoint{Line: 0, Col: 0}, ui.SelectionPoint{Line: 0, Col: 5}, false)
	typeKey(p, tea.KeyPressMsg{Code: 'x', Text: "x"})
	if p.hasEditSelection() {
		t.Fatal("selection survived replace")
	}
	if got := p.editorTextarea.Value(); got != "x" {
		t.Fatalf("after replace = %q, want x", got)
	}
	if !p.editorDirty {
		t.Fatal("replace did not mark dirty")
	}
}

func TestBackspaceDeletesSelectionAsOneUnit(t *testing.T) {
	p := newEditPlugin(t, "hello world")
	p.selection.SelectRange(ui.SelectionPoint{Line: 0, Col: 6}, ui.SelectionPoint{Line: 0, Col: 11}, false)
	beforeID := p.autoSaveID
	typeKey(p, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if got := p.editorTextarea.Value(); got != "hello " {
		t.Fatalf("after delete = %q, want %q", got, "hello ")
	}
	if p.autoSaveID != beforeID+1 {
		t.Fatalf("delete-selection debounce = %d, want %d", p.autoSaveID, beforeID+1)
	}
	typeKey(p, tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	if got := p.editorTextarea.Value(); got != "hello world" {
		t.Fatalf("undo delete-selection = %q, want original", got)
	}
}

func TestPasteReplacesSelectionAsOneUndoAndDebounce(t *testing.T) {
	p := newEditPlugin(t, "hello")
	p.selection.SelectRange(ui.SelectionPoint{Line: 0, Col: 1}, ui.SelectionPoint{Line: 0, Col: 4}, false)
	beforeID := p.autoSaveID
	_, cmd := p.Update(tea.PasteMsg{Content: "XX"})
	if got := p.editorTextarea.Value(); got != "hXXo" {
		t.Fatalf("paste replace = %q, want hXXo", got)
	}
	if p.autoSaveID != beforeID+1 {
		t.Fatalf("paste debounce = %d, want %d", p.autoSaveID, beforeID+1)
	}
	if cmd == nil {
		t.Fatal("paste replace started no debounce")
	}
	typeKey(p, tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	if got := p.editorTextarea.Value(); got != "hello" {
		t.Fatalf("undo paste = %q, want hello", got)
	}
}

func TestBarePrintableKeysType(t *testing.T) {
	p := newEditPlugin(t, "")
	for _, r := range []rune{'v', 'u', 'y', 'd', 'x', 'p'} {
		typeKey(p, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if got := p.editorTextarea.Value(); got != "vuydxp" {
		t.Fatalf("bare keys = %q, want vuydxp", got)
	}
}

func TestTypingBurstUndoesTogether(t *testing.T) {
	p := newEditPlugin(t, "go")
	p.setTextareaCursorPosition(0, 2)
	typeKey(p, tea.KeyPressMsg{Code: 'x', Text: "x"})
	typeKey(p, tea.KeyPressMsg{Code: 'y', Text: "y"})
	typeKey(p, tea.KeyPressMsg{Code: 'z', Text: "z"})
	if got := p.editorTextarea.Value(); got != "goxyz" {
		t.Fatalf("typed = %q", got)
	}
	typeKey(p, tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	if got := p.editorTextarea.Value(); got != "go" {
		t.Fatalf("undo burst = %q, want go", got)
	}
	typeKey(p, tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl})
	if got := p.editorTextarea.Value(); got != "goxyz" {
		t.Fatalf("ctrl+y redo = %q, want goxyz", got)
	}
	typeKey(p, tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	typeKey(p, tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl | tea.ModShift})
	if got := p.editorTextarea.Value(); got != "goxyz" {
		t.Fatalf("ctrl+shift+z redo = %q, want goxyz", got)
	}
}

func TestSelectAllAndAltSExtend(t *testing.T) {
	p := newEditPlugin(t, "ab\ncd")
	typeKey(p, tea.KeyPressMsg{Code: 'a', Mod: tea.ModAlt})
	if got := strings.Join(p.getSelectedText(), "\n"); got != "ab\ncd" {
		t.Fatalf("select-all = %q", got)
	}
	p.clearEditSelection()
	p.setTextareaCursorPosition(0, 0)
	typeKey(p, tea.KeyPressMsg{Code: 's', Mod: tea.ModAlt})
	if !p.selExtend {
		t.Fatal("alt+s did not set extend mode")
	}
	typeKey(p, tea.KeyPressMsg{Code: tea.KeyRight})
	typeKey(p, tea.KeyPressMsg{Code: tea.KeyRight})
	if got := strings.Join(p.getSelectedText(), "\n"); got != "ab" {
		t.Fatalf("alt+s extend = %q, want ab", got)
	}
	typeKey(p, tea.KeyPressMsg{Code: 's', Mod: tea.ModAlt})
	if p.hasEditSelection() || p.selExtend {
		t.Fatal("second alt+s should clear the anchor")
	}
}

func TestWrappedSelectionOverlayAndResize(t *testing.T) {
	content := strings.Repeat("word ", 40)
	p := newEditPlugin(t, content)
	p.width = 72
	p.listWidth = 18
	p.updateTextareaDimensions()
	wrap := p.editorLayout().wrapColumn
	if wrap < 8 {
		t.Fatalf("wrap column %d too narrow", wrap)
	}
	end := 30
	p.selection.SelectRange(ui.SelectionPoint{Line: 0, Col: 0}, ui.SelectionPoint{Line: 0, Col: end}, false)
	if got := strings.Join(p.getSelectedText(), ""); got != content[:end] {
		t.Fatalf("wrapped source extract = %q, want %q", got, content[:end])
	}
	out := p.overlaySelectionOnEditor(p.editorTextarea.View())
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("wrapped overlay painted no selection: %q", ansi.Strip(out))
	}
	// Resize must keep the same source range, not a visual-column range.
	p.width = 50
	p.updateTextareaDimensions()
	if got := strings.Join(p.getSelectedText(), ""); got != content[:end] {
		t.Fatalf("after resize extract = %q, want same source range", got)
	}
	out = p.overlaySelectionOnEditor(p.editorTextarea.View())
	if !strings.Contains(out, "\x1b[") {
		t.Fatal("overlay lost selection after resize")
	}
}

func TestMouseDragUsesSourceCaretsAcrossWrap(t *testing.T) {
	content := strings.Repeat("word ", 40)
	p := newEditPlugin(t, content)
	p.width = 72
	p.listWidth = 18
	p.updateTextareaDimensions()
	p.selection.PrepareDrag(0, 0, ui.SelectionState{}.ViewRect)
	start, end := mouseExclusiveRange(srcPos{}, srcPos{col: 12}, content)
	p.selection.SelectRange(start.point(), end.point(), false)
	got := strings.Join(p.getSelectedText(), "")
	want := content[:13]
	if got != want {
		t.Fatalf("mouse source drag = %q, want %q", got, want)
	}
}

func TestBackwardMouseDragKeepsBothEndpoints(t *testing.T) {
	start, end := mouseExclusiveRange(srcPos{col: 4}, srcPos{col: 0}, "hello")
	got := extractExclusive(sourceLines("hello"), start, end)
	if got != "hello" {
		t.Fatalf("backward drag = %q, want hello", got)
	}
	start, end = mouseExclusiveRange(srcPos{col: 0}, srcPos{col: 4}, "hello")
	got = extractExclusive(sourceLines("hello"), start, end)
	if got != "hello" {
		t.Fatalf("forward drag = %q, want hello", got)
	}
}

func TestMultiLineOverlayHighlightsWholeFirstLine(t *testing.T) {
	content := "ab\ncd"
	surface := markdown.MapWrappedSource(content, 40)
	vStart, vEnd, ok := visualColsForSourceRange(surface, 0, srcPos{}, srcPos{line: 1, col: 2}, content)
	if !ok {
		t.Fatal("first source line did not overlap a full-note selection")
	}
	if vStart != 0 || vEnd != 1 {
		t.Fatalf("first-line visual cols = [%d, %d], want [0, 1] (both runes of ab)", vStart, vEnd)
	}
	p := newEditPlugin(t, content)
	p.selection.SelectRange(ui.SelectionPoint{Line: 0, Col: 0}, ui.SelectionPoint{Line: 1, Col: 2}, false)
	if got := strings.Join(p.getSelectedText(), "\n"); got != content {
		t.Fatalf("select-all extract = %q", got)
	}
}

func TestDeleteSelectionDoesNotCoalesceWithBackspace(t *testing.T) {
	p := newEditPlugin(t, "hello world")
	p.selection.SelectRange(ui.SelectionPoint{Line: 0, Col: 6}, ui.SelectionPoint{Line: 0, Col: 11}, false)
	typeKey(p, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if got := p.editorTextarea.Value(); got != "hello " {
		t.Fatalf("after range delete = %q", got)
	}
	typeKey(p, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if got := p.editorTextarea.Value(); got != "hello" {
		t.Fatalf("after char delete = %q", got)
	}
	typeKey(p, tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	if got := p.editorTextarea.Value(); got != "hello " {
		t.Fatalf("undo char delete = %q, want hello ", got)
	}
}

func TestSyncEditorFromNoteDropsHistory(t *testing.T) {
	p := newEditPlugin(t, "mine")
	p.setTextareaCursorPosition(0, 4)
	typeKey(p, tea.KeyPressMsg{Code: 'x', Text: "x"})
	if !p.historyForCurrent().canUndo() {
		t.Fatal("expected undo after typing")
	}
	p.editorNote.Content = "theirs"
	p.syncEditorFromNote(p.editorNote)
	if p.historyForCurrent().canUndo() {
		t.Fatal("sync kept pre-reload undo snapshots")
	}
	if got := p.editorTextarea.Value(); got != "theirs" {
		t.Fatalf("after sync = %q", got)
	}
}

func TestExtractExclusiveUnicode(t *testing.T) {
	lines := []string{"ab世界cd"}
	got := extractExclusive(lines, srcPos{col: 2}, srcPos{col: 4})
	if got != "世界" {
		t.Fatalf("unicode extract = %q", got)
	}
	if utf8.RuneCountInString(got) != 2 {
		t.Fatalf("unicode extract runes = %d", utf8.RuneCountInString(got))
	}
}
