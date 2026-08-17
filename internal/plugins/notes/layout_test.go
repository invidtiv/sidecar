package notes

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/ui"
)

func layoutTestPlugin(t *testing.T, contents ...string) *Plugin {
	t.Helper()
	p := New()
	p.store = &Store{}
	p.editorTextarea = newEditorTextarea()
	p.width, p.height = 120, 30
	p.listWidth = 30
	p.notePlaces = make(map[string]notePlace)
	p.previewMode = true
	p.notes = make([]Note, len(contents))
	for i, content := range contents {
		p.notes[i] = Note{ID: string(rune('a' + i)), Content: content}
	}
	if len(p.notes) > 0 {
		p.editorNote = &p.notes[0]
		p.editorTextarea.SetValue(p.notes[0].Content)
		p.previewLines = strings.Split(p.notes[0].Content, "\n")
		if len(p.previewLines) == 0 {
			p.previewLines = []string{""}
		}
	}
	p.updateTextareaDimensions()
	return p
}

func numberedContent(n int) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = "line"
	}
	return strings.Join(lines, "\n")
}

func TestEditorLayoutGeometryParity(t *testing.T) {
	p := layoutTestPlugin(t, numberedContent(40))

	p.previewMode = true
	viewLay := p.editorLayout()
	viewH, viewW := p.previewViewport()

	p.previewMode = false
	editLay := p.editorLayout()
	p.updateTextareaDimensions()

	if viewLay != editLay {
		t.Fatalf("layout differs by mode: view=%+v edit=%+v", viewLay, editLay)
	}
	if viewLay.wrapColumn != viewW || viewLay.contentHeight != viewH {
		t.Fatalf("previewViewport (%d,%d) != layout (h=%d wrap=%d)", viewH, viewW, viewLay.contentHeight, viewLay.wrapColumn)
	}
	if p.editorTextarea.Width() != viewLay.wrapColumn {
		t.Fatalf("textarea width %d, want wrap column %d", p.editorTextarea.Width(), viewLay.wrapColumn)
	}
	if p.editorTextarea.Height() != viewLay.contentHeight {
		t.Fatalf("textarea height %d, want content height %d", p.editorTextarea.Height(), viewLay.contentHeight)
	}
	if viewLay.scrollbarCol != viewLay.innerWidth-1 {
		t.Fatalf("scrollbar col %d, want innerWidth-1=%d", viewLay.scrollbarCol, viewLay.innerWidth-1)
	}
	if viewLay.leftMargin != 0 {
		t.Fatalf("left margin %d, want 0 (no gutter)", viewLay.leftMargin)
	}
	if viewLay.statusRow != 0 {
		t.Fatalf("status row %d, want 0", viewLay.statusRow)
	}
}

func TestModeSwitchCarriesSourceLine(t *testing.T) {
	p := layoutTestPlugin(t, numberedContent(40))
	p.previewMode = true
	p.previewCursorLine = 12
	p.previewScrollOff = 8

	_ = p.enterEditAtPreviewPlace()
	if p.previewMode {
		t.Fatal("enter edit left preview mode on")
	}
	if got := p.editorTextarea.Line(); got != 12 {
		t.Fatalf("edit cursor line %d, want 12", got)
	}
	if got := p.editorTextarea.ScrollYOffset(); got < 6 || got > 10 {
		t.Fatalf("enter edit YOffset=%d, want near previewScrollOff=8", got)
	}

	p.editorTextarea.MoveToBegin()
	p.setTextareaCursorPosition(15, 0)
	p.trackTextareaScroll()
	p.captureEditPlace()
	p.previewMode = true

	if p.previewCursorLine != 15 {
		t.Fatalf("preview cursor %d, want 15 (line being edited)", p.previewCursorLine)
	}
	l := p.editorLayout()
	visibleEnd := p.previewScrollOff + l.contentHeight
	if p.previewCursorLine < p.previewScrollOff || p.previewCursorLine >= visibleEnd {
		t.Fatalf("preview scroll %d does not show edit line 15 (height %d)", p.previewScrollOff, l.contentHeight)
	}
}

func TestEnterEditDeepScrollKeepsCursorOnScreen(t *testing.T) {
	p := layoutTestPlugin(t, numberedContent(80))
	p.previewMode = true
	p.previewCursorLine = 40
	p.previewScrollOff = 35

	_ = p.enterEditAtPreviewPlace()
	if got := p.editorTextarea.Line(); got != 40 {
		t.Fatalf("edit cursor line %d, want 40", got)
	}
	l := p.editorLayout()
	off := p.editorTextarea.ScrollYOffset()
	if off == 0 {
		t.Fatal("enter edit left YOffset=0; preview row was 5, not the top of the note")
	}
	if got := p.editorTextarea.Line(); got < off || got >= off+l.contentHeight {
		t.Fatalf("cursor line 40 not visible after enter (YOffset=%d height=%d)", off, l.contentHeight)
	}
}

func TestPerNotePlaceMemory(t *testing.T) {
	p := layoutTestPlugin(t, numberedContent(40), numberedContent(10))
	p.cursor = 0
	p.previewMode = true
	p.loadNoteIntoEditor()
	p.previewCursorLine = 12
	p.previewScrollOff = 8
	p.ensurePreviewCursorVisible()

	p.cursor = 1
	p.loadNoteIntoEditor()
	if p.editorNote == nil || p.editorNote.ID != p.notes[1].ID {
		t.Fatalf("expected note B, got %+v", p.editorNote)
	}
	if p.previewScrollOff == 8 && p.previewCursorLine == 12 {
		t.Fatal("note B inherited note A's place")
	}

	p.cursor = 0
	p.loadNoteIntoEditor()
	if p.editorNote == nil || p.editorNote.ID != p.notes[0].ID {
		t.Fatalf("expected note A, got %+v", p.editorNote)
	}
	if p.previewCursorLine != 12 {
		t.Fatalf("note A cursor %d, want 12", p.previewCursorLine)
	}
	if p.previewScrollOff != 8 {
		t.Fatalf("note A scroll %d, want 8", p.previewScrollOff)
	}
	last := len(p.previewLines) - 1
	if p.previewCursorLine == last {
		t.Fatal("note A restored at end-of-note")
	}
}

func TestNewNoteOpensAtEnd(t *testing.T) {
	p := layoutTestPlugin(t, numberedContent(12))
	p.cursor = 0
	p.loadNoteIntoEditorAtEnd()
	if p.previewMode {
		t.Fatal("new note should open in edit mode")
	}
	want := p.editorTextarea.LineCount() - 1
	if got := p.editorTextarea.Line(); got != want {
		t.Fatalf("new-note cursor line %d, want last line %d", got, want)
	}
}

func TestSelectionDoesNotSwapRenderer(t *testing.T) {
	p := layoutTestPlugin(t, "alpha\nbeta\ngamma")
	p.previewMode = false
	p.activePane = PaneEditor
	p.updateTextareaDimensions()
	beforeW, beforeH := p.editorTextarea.Width(), p.editorTextarea.Height()

	p.selection.SelectRange(
		ui.SelectionPoint{Line: 0, Col: 0},
		ui.SelectionPoint{Line: 0, Col: 3},
		false,
	)
	if !p.selection.HasSelection() {
		t.Fatal("expected an active selection")
	}

	// Desync previewLines so a renderer swap would be visible in the output.
	p.previewLines = []string{"XXX", "YYY", "ZZZ"}
	out := p.renderEditorPane(p.height-2, 80)
	if p.previewMode {
		t.Fatal("selection flipped previewMode on")
	}
	if strings.Contains(out, "XXX") {
		t.Fatalf("selection swapped to preview renderer: %q", out)
	}
	// "alpha" is split by the selection overlay; an unselected line is intact.
	if !strings.Contains(out, "beta") {
		t.Fatalf("edit+selection did not draw textarea: %q", out)
	}
	if p.editorTextarea.Width() != beforeW || p.editorTextarea.Height() != beforeH {
		t.Fatalf("textarea geometry changed: %dx%d -> %dx%d",
			beforeW, beforeH, p.editorTextarea.Width(), p.editorTextarea.Height())
	}

	// Re-render in preview for contrast: that path is previewMode, not selection.
	p.selection.Clear()
	p.previewMode = true
	_ = p.renderEditorPane(p.height-2, 80)
	if !p.previewMode {
		t.Fatal("preview render cleared previewMode")
	}
}

func TestPreviewTruncationIsRuneSafe(t *testing.T) {
	// 世 is 3 UTF-8 bytes; a byte slice at wrapWidth-in-bytes would split it.
	line := "ab世界cd"
	got := truncatePreviewLine(line, 3)
	if !utf8.ValidString(got) {
		t.Fatalf("truncated %q is not valid UTF-8: %q", line, got)
	}
	if strings.ContainsRune(got, '\uFFFD') {
		t.Fatalf("truncated %q contains replacement rune: %q", line, got)
	}
	if ansiWidth := len([]rune(got)); ansiWidth == 0 {
		t.Fatal("truncated line is empty")
	}

	p := layoutTestPlugin(t, line)
	p.previewMode = true
	p.previewWrapEnabled = false
	p.previewLines = []string{line}
	out := p.renderPreviewContent(4, 3)
	if !utf8.ValidString(out) {
		t.Fatalf("preview render is not valid UTF-8: %q", out)
	}
	if strings.Contains(out, "~") {
		t.Fatalf("preview still draws ~ filler: %q", out)
	}
}

func TestPreviewHasNoGutter(t *testing.T) {
	p := layoutTestPlugin(t, "hello")
	p.previewMode = true
	p.previewWrapEnabled = false
	p.previewLines = []string{"hello"}
	out := p.renderPreviewContent(3, 20)
	if strings.Contains(out, "~") {
		t.Fatalf("preview still draws ~ filler: %q", out)
	}
	// A line-number gutter would prefix the first content line with digits.
	first := strings.Split(out, "\n")[0]
	if strings.HasPrefix(strings.TrimLeft(first, " "), "1 ") {
		t.Fatalf("preview still draws a line-number gutter: %q", first)
	}
}

func TestEditorStatusHeaderKeepsSaveStateAtNarrowWidths(t *testing.T) {
	p := layoutTestPlugin(t, "body")
	p.editorNote.CreatedAt = time.Date(2026, time.August, 17, 0, 0, 0, 0, time.UTC)
	p.editorNote.UpdatedAt = p.editorNote.CreatedAt
	p.previewMode = true

	for _, width := range []int{12, 24, 40} {
		got := p.renderEditorStatusHeader(width)
		plain := ansi.Strip(got)
		if !strings.Contains(plain, "Saved") {
			t.Fatalf("width %d lost save state: %q", width, plain)
		}
		if gotWidth := ansi.StringWidth(got); gotWidth != width {
			t.Fatalf("width %d rendered %d cells: %q", width, gotWidth, plain)
		}
	}

	p.editorDirty = true
	got := ansi.Strip(p.renderEditorStatusHeader(12))
	if !strings.Contains(got, "Unsaved") {
		t.Fatalf("narrow dirty header lost unsaved state: %q", got)
	}
}

func TestNoteRowUnicodeTitleIsCellSafe(t *testing.T) {
	p := layoutTestPlugin(t, "body")
	note := Note{Title: strings.Repeat("世界", 40)}

	for _, selected := range []bool{false, true} {
		got := p.renderNoteRow(note, selected, 24)
		if !utf8.ValidString(got) {
			t.Fatalf("selected=%v produced invalid UTF-8: %q", selected, got)
		}
		if width := ansi.StringWidth(got); width > 24 {
			t.Fatalf("selected=%v rendered %d cells, want <= 24: %q", selected, width, got)
		}
	}
}
