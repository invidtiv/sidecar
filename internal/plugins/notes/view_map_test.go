package notes

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/markdown"
)

func wrappingParagraph() string {
	return strings.TrimSpace(strings.Repeat("word ", 48))
}

func TestMarkdownViewIsDefault(t *testing.T) {
	p := New()
	if !p.markdownView {
		t.Fatal("new plugin should rest in glamour view")
	}
	p = layoutTestPlugin(t, wrappingParagraph())
	p.markdownView = true
	p.invalidateViewSurface()
	p.ensureViewSurface()
	if len(p.viewSurface.Lines) == 0 {
		t.Fatal("glamour view produced no lines")
	}
	out := p.renderViewSurface(8)
	plain := ansi.Strip(out)
	if !strings.Contains(plain, "word") {
		t.Fatalf("glamour view missing paragraph text: %q", plain)
	}
}

func TestToggleMarkdownRemapsPlace(t *testing.T) {
	p := layoutTestPlugin(t, wrappingParagraph())
	p.markdownView = true
	p.previewMode = true
	p.invalidateViewSurface()
	p.ensureViewSurface()
	if len(p.viewSurface.Lines) < 3 {
		t.Fatalf("expected wrapping paragraph, got %d visual rows", len(p.viewSurface.Lines))
	}
	p.previewCursorLine = 2
	p.previewScrollOff = 1
	src := p.viewSurface.At(2)

	p.toggleMarkdownView()
	if p.markdownView {
		t.Fatal("m should have switched to raw")
	}
	got := p.viewSurface.At(p.previewCursorLine)
	if got.SourceLine != src.SourceLine {
		t.Fatalf("raw cursor source %d, want %d", got.SourceLine, src.SourceLine)
	}

	p.toggleMarkdownView()
	if !p.markdownView {
		t.Fatal("m should have switched back to rendered")
	}
	back := p.viewSurface.At(p.previewCursorLine)
	if back.SourceLine != src.SourceLine {
		t.Fatalf("rendered cursor source %d, want %d", back.SourceLine, src.SourceLine)
	}
}

func TestClickRenderedParagraphLandsOnSourceLine(t *testing.T) {
	content := wrappingParagraph()
	p := layoutTestPlugin(t, content)
	p.markdownView = true
	p.previewMode = true
	p.activePane = PaneEditor
	p.invalidateViewSurface()
	p.ensureViewSurface()

	nonEmpty := nonemptyVisual(p.viewSurface)
	if len(nonEmpty) < 3 {
		t.Fatalf("expected wrapping paragraph, got %d rows", len(nonEmpty))
	}
	// Scroll so the clicked row is not at the top of the note.
	p.previewScrollOff = nonEmpty[1]
	p.previewCursorLine = nonEmpty[2]
	viewRow := p.previewCursorLine - p.previewScrollOff
	if viewRow < 1 {
		t.Fatalf("click is not below the first visible row: cursor=%d scroll=%d",
			p.previewCursorLine, p.previewScrollOff)
	}

	a := p.viewSurface.Click(p.previewScrollOff, viewRow)
	_ = p.enterEditAt(a.SourceLine, a.SourceCol)

	if p.previewMode {
		t.Fatal("click left preview mode on")
	}
	if got := p.editorTextarea.Line(); got != a.SourceLine {
		t.Fatalf("edit line %d, want mapped source %d", got, a.SourceLine)
	}
	if a.SourceLine != 0 {
		t.Fatalf("single-line paragraph mapped to source %d, want 0", a.SourceLine)
	}
	if a.SourceCol < p.editorLayout().wrapColumn/2 {
		t.Fatalf("mid-paragraph click col %d looks like the start of the line", a.SourceCol)
	}

	off := p.editorTextarea.ScrollYOffset()
	screenRow := p.editorTextarea.Line() - off
	if screenRow != viewRow && screenRow != viewRow-1 && screenRow != viewRow+1 {
		t.Fatalf("edit screen row %d (YOffset=%d line=%d), want near clicked view row %d",
			screenRow, off, p.editorTextarea.Line(), viewRow)
	}
}

func TestRawAndEditShareWrapPolicy(t *testing.T) {
	line := strings.Repeat("abcd", 30) // 120 cells, one source line
	p := layoutTestPlugin(t, line)
	p.markdownView = false
	p.previewMode = true
	p.invalidateViewSurface()
	p.ensureViewSurface()

	l := p.editorLayout()
	visual := len(p.viewSurface.Lines)
	if visual < 2 {
		t.Fatalf("raw view did not wrap a %d-cell line at wrap=%d", ansi.StringWidth(line), l.wrapColumn)
	}

	// Edit mode textarea wraps at the same column. A truncate policy would
	// have produced one visual row in raw view and many in the textarea.
	p.previewMode = false
	p.updateTextareaDimensions()
	if p.editorTextarea.Width() != l.wrapColumn {
		t.Fatalf("textarea wrap %d, want layout wrap %d", p.editorTextarea.Width(), l.wrapColumn)
	}
	if visual == 1 {
		t.Fatal("raw view truncated instead of wrapping")
	}
}

func TestViewPasteMapsThroughRender(t *testing.T) {
	p := layoutTestPlugin(t, wrappingParagraph())
	p.markdownView = true
	p.previewMode = true
	p.activePane = PaneEditor
	p.invalidateViewSurface()
	p.ensureViewSurface()
	p.previewCursorLine = 0

	_, _ = p.Update(tea.PasteMsg{Content: "IN"})
	if p.previewMode {
		t.Fatal("view paste left preview mode")
	}
	got := p.editorTextarea.Value()
	if !strings.HasPrefix(got, "IN") {
		t.Fatalf("paste did not insert at mapped reading position: %q", got[:min(40, len(got))])
	}
}

func TestTabFromListFocusesPreviewNotEdit(t *testing.T) {
	p := layoutTestPlugin(t, wrappingParagraph())
	p.markdownView = true
	p.previewMode = true
	p.activePane = PaneList
	p.invalidateViewSurface()

	_, _ = p.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if p.activePane != PaneEditor {
		t.Fatal("tab from list did not focus the editor pane")
	}
	if !p.previewMode {
		t.Fatal("tab from list entered edit; it should focus the rendered view")
	}

	_, _ = p.handleKey(tea.KeyPressMsg{Code: 'i', Text: "i"})
	if p.previewMode {
		t.Fatal("i from preview should enter edit")
	}
}

func TestLeaveEditReturnsToRenderedView(t *testing.T) {
	p := layoutTestPlugin(t, wrappingParagraph())
	p.markdownView = true
	p.previewMode = true
	p.invalidateViewSurface()
	p.ensureViewSurface()
	p.previewCursorLine = 2
	_ = p.enterEditAtPreviewPlace()
	if p.previewMode {
		t.Fatal("enter edit stayed in preview")
	}

	p.leaveEditToView()
	if !p.previewMode {
		t.Fatal("leave edit did not restore view mode")
	}
	if !p.markdownView {
		t.Fatal("leave edit dropped out of glamour view")
	}
}

func nonemptyVisual(m markdown.MappedRender) []int {
	var rows []int
	for i, line := range m.Lines {
		if strings.TrimSpace(ansi.Strip(line)) != "" {
			rows = append(rows, i)
		}
	}
	return rows
}
