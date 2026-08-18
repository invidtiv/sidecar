package notes

import (
	"fmt"
	"strings"
	"testing"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/markdown"
	rw "github.com/mattn/go-runewidth"
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

func TestClickRenderedParagraphPreservesWrappedScreenRow(t *testing.T) {
	content := strings.TrimSpace(strings.Repeat("word ", 120))
	p := layoutTestPlugin(t, content)
	p.width = 72
	p.listWidth = 18
	p.updateTextareaDimensions()
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
	p.previewCursorLine = nonEmpty[len(nonEmpty)-2]
	viewRow := p.previewCursorLine - p.previewScrollOff
	if viewRow < 1 {
		t.Fatalf("click is not below the first visible row: cursor=%d scroll=%d",
			p.previewCursorLine, p.previewScrollOff)
	}

	originX := p.listWidth + dividerWidth + 2 + p.editorLayout().leftMargin
	clickX := originX + p.editorLayout().wrapColumn - 1
	clickY := p.editorContentStartY() + viewRow
	srcLine, srcCol := p.clickToSource(clickX, clickY)
	a := p.viewSurface.Click(p.previewScrollOff, viewRow)
	_ = p.enterEditAt(srcLine, srcCol)

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
	// ScrollYOffset and LineInfo.RowOffset are visual-row coordinates. Line()
	// is only a logical source row, so it cannot detect a jump between soft-
	// wrapped rows of the same paragraph.
	screenRow := p.editorTextarea.LineInfo().RowOffset - off
	if screenRow != viewRow {
		t.Fatalf("edit screen row %d (YOffset=%d line=%d rowOffset=%d), want clicked view row %d",
			screenRow, off, p.editorTextarea.Line(), p.editorTextarea.LineInfo().RowOffset, viewRow)
	}
}

func TestClickWrappedTextareaRowUsesVisualAnchor(t *testing.T) {
	content := strings.TrimSpace(strings.Repeat("word ", 400))
	p := layoutTestPlugin(t, content)
	p.width = 72
	p.listWidth = 18
	p.previewMode = false
	p.updateTextareaDimensions()
	raw := markdown.MapWrappedSource(content, p.editorLayout().wrapColumn)
	p.setTextareaCursorAndScroll(0, raw.At(32).SourceCol, 8)
	if p.editorTextarea.ScrollYOffset() == 0 {
		t.Fatal("fixture did not create a scrolled textarea")
	}

	const viewRow = 6
	originX := p.listWidth + dividerWidth + 2 + p.editorLayout().leftMargin
	clickX := originX + 8
	clickY := p.editorContentStartY() + viewRow
	wantAnchor := raw.At(p.editorTextarea.ScrollYOffset() + viewRow)

	srcLine, srcCol := p.clickToSource(clickX, clickY)
	if srcLine != wantAnchor.SourceLine {
		t.Fatalf("wrapped edit click source line %d, want %d", srcLine, wantAnchor.SourceLine)
	}
	if srcCol < wantAnchor.SourceCol {
		t.Fatalf("wrapped edit click source col %d, want at or after row anchor %d", srcCol, wantAnchor.SourceCol)
	}
	_ = p.enterEditAt(srcLine, srcCol)

	screenRow := raw.VisualRowForSource(p.editorTextarea.Line(), p.editorTextarea.Column()) - p.editorTextarea.ScrollYOffset()
	if screenRow != viewRow {
		t.Fatalf("wrapped edit click moved to screen row %d, want %d (line=%d col=%d offset=%d)",
			screenRow, viewRow, p.editorTextarea.Line(), p.editorTextarea.Column(), p.editorTextarea.ScrollYOffset())
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

func TestRawAnchorsMatchTextareaSoftWrapRows(t *testing.T) {
	contents := []string{
		strings.TrimSpace(strings.Repeat("word ", 80)),
		strings.Repeat("abcdefghij", 24),
		strings.Repeat("x", 46), // exact-width line has Bubbles' cursor-edge row
		strings.TrimSpace(strings.Repeat("naïve café 世界 ", 40)),
	}
	for _, content := range contents {
		p := layoutTestPlugin(t, content)
		p.width = 72
		p.listWidth = 18
		p.previewMode = false
		p.updateTextareaDimensions()
		raw := markdown.MapWrappedSource(content, p.editorLayout().wrapColumn)
		for visual, a := range raw.Anchors {
			p.editorTextarea.SetCursorColumn(a.SourceCol)
			if got := p.editorTextarea.LineInfo().RowOffset; got != visual {
				t.Fatalf("raw anchor row %d col %d maps to textarea row %d (wrap=%d, content=%q)",
					visual, a.SourceCol, got, p.editorLayout().wrapColumn, content[:min(40, len(content))])
			}
		}
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

func TestWheelScrollSurvivesPaint(t *testing.T) {
	lines := make([]string, 80)
	for i := range lines {
		lines[i] = "line"
	}
	p := layoutTestPlugin(t, strings.Join(lines, "\n"))
	p.markdownView = false
	p.previewMode = true
	p.activePane = PaneEditor
	p.invalidateViewSurface()
	p.ensureViewSurface()
	p.previewCursorLine = 0
	p.previewScrollOff = 0

	before := p.previewScrollOff
	_, _ = p.handleMouseScroll(p.mouseHandler.HandleMouse(wheelMsg(editorX, 8, false)))
	if p.previewScrollOff <= before {
		t.Fatalf("wheel did not advance scroll: %d", p.previewScrollOff)
	}
	scrolled := p.previewScrollOff
	_ = p.renderViewSurface(p.editorLayout().contentHeight)
	if p.previewScrollOff != scrolled {
		t.Fatalf("paint reset wheel scroll %d -> %d", scrolled, p.previewScrollOff)
	}
	if p.previewCursorLine < p.previewScrollOff {
		t.Fatalf("reading cursor %d left above viewport %d", p.previewCursorLine, p.previewScrollOff)
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

func uniqueWrappedWords() string {
	words := make([]string, 0, 80)
	for i := 0; i < 80; i++ {
		words = append(words, fmt.Sprintf("tok%02d", i))
	}
	return strings.Join(words, " ")
}

func clickWordInView(t *testing.T, p *Plugin, word string) (clickX, clickY, viewRow int) {
	t.Helper()
	p.ensureViewSurface()
	originX := p.listWidth + dividerWidth + 2 + p.editorLayout().leftMargin
	for i, line := range p.viewSurface.Lines {
		plain := []rune(ansi.Strip(line))
		idx := indexToken(plain, word)
		if idx < 0 {
			continue
		}
		col := 0
		for _, r := range plain[:idx] {
			w := rw.RuneWidth(r)
			if w < 1 {
				w = 1
			}
			col += w
		}
		viewRow = i - p.previewScrollOff
		if viewRow < 0 {
			p.previewScrollOff = i
			viewRow = 0
		}
		return originX + col, p.editorContentStartY() + viewRow, viewRow
	}
	t.Fatalf("word %q not found in view surface", word)
	return 0, 0, 0
}

func indexToken(plain []rune, word string) int {
	wr := []rune(word)
	for i := 0; i+len(wr) <= len(plain); i++ {
		if string(plain[i:i+len(wr)]) != word {
			continue
		}
		if i > 0 && (unicode.IsLetter(plain[i-1]) || unicode.IsDigit(plain[i-1]) || plain[i-1] == '_') {
			continue
		}
		end := i + len(wr)
		if end < len(plain) && (unicode.IsLetter(plain[end]) || unicode.IsDigit(plain[end]) || plain[end] == '_') {
			continue
		}
		return i
	}
	return -1
}

func TestClickWrappedRenderedWordLandsOnThatWord(t *testing.T) {
	content := uniqueWrappedWords()
	p := layoutTestPlugin(t, content)
	p.width = 72
	p.listWidth = 18
	p.updateTextareaDimensions()
	p.markdownView = true
	p.previewMode = true
	p.activePane = PaneEditor
	p.invalidateViewSurface()
	p.ensureViewSurface()

	const word = "tok37"
	if strings.Index(content, word) < p.editorLayout().wrapColumn {
		t.Fatalf("%s is still on the first wrap; need a later-row token", word)
	}
	clickX, clickY, _ := clickWordInView(t, p, word)
	srcLine, srcCol := p.clickToSource(clickX, clickY)
	_ = p.enterEditAt(srcLine, srcCol)

	got := p.editorTextarea.Value()
	runes := []rune(got)
	if srcLine != 0 {
		t.Fatalf("source line %d, want 0", srcLine)
	}
	if srcCol < 0 || srcCol > len(runes) {
		t.Fatalf("source col %d out of range", srcCol)
	}
	window := runes[srcCol:]
	if !strings.HasPrefix(string(window), word) {
		gotWord := string(window)
		if len(gotWord) > 20 {
			gotWord = gotWord[:20]
		}
		t.Fatalf("cursor at line=%d col=%d is %q, want %s", srcLine, srcCol, gotWord, word)
	}
}

func TestClickWrappedRenderedWordKeepsIntraOffset(t *testing.T) {
	content := uniqueWrappedWords()
	p := layoutTestPlugin(t, content)
	p.width = 72
	p.listWidth = 18
	p.updateTextareaDimensions()
	p.markdownView = true
	p.previewMode = true
	p.activePane = PaneEditor
	p.invalidateViewSurface()
	p.ensureViewSurface()

	const word = "tok37"
	clickX, clickY, _ := clickWordInView(t, p, word)
	// Click the '3' (third rune) of tok37.
	clickX += rw.StringWidth("tok")
	srcLine, srcCol := p.clickToSource(clickX, clickY)
	_ = p.enterEditAt(srcLine, srcCol)
	got := []rune(p.editorTextarea.Value())
	if srcCol < 3 || srcCol > len(got) || !strings.HasPrefix(string(got[srcCol-3:]), word) {
		t.Fatalf("mid-word click landed at col %d, want inside %s", srcCol, word)
	}
	if string(got[srcCol:])[0] != '3' {
		t.Fatalf("mid-word click col %d is %q, want the '3' in %s", srcCol, string(got[srcCol:min(srcCol+5, len(got))]), word)
	}
}

func TestClickWrappedHeadingWordLandsOnThatWord(t *testing.T) {
	content := "# " + uniqueWrappedWords()
	p := layoutTestPlugin(t, content)
	p.width = 72
	p.listWidth = 18
	p.updateTextareaDimensions()
	p.markdownView = true
	p.previewMode = true
	p.activePane = PaneEditor
	p.invalidateViewSurface()
	p.ensureViewSurface()

	const word = "tok37"
	clickX, clickY, _ := clickWordInView(t, p, word)
	srcLine, srcCol := p.clickToSource(clickX, clickY)
	_ = p.enterEditAt(srcLine, srcCol)
	got := []rune(p.editorTextarea.Value())
	if srcLine != 0 {
		t.Fatalf("heading click source line %d, want 0", srcLine)
	}
	if srcCol < 0 || srcCol > len(got) || !strings.HasPrefix(string(got[srcCol:]), word) {
		t.Fatalf("heading click landed at col %d, want %s", srcCol, word)
	}
}

func TestClickFenceStaysAtBlockStart(t *testing.T) {
	content := "```\n" + uniqueWrappedWords() + "\n```"
	p := layoutTestPlugin(t, content)
	p.width = 72
	p.listWidth = 18
	p.updateTextareaDimensions()
	p.markdownView = true
	p.previewMode = true
	p.activePane = PaneEditor
	p.invalidateViewSurface()
	p.ensureViewSurface()

	// Any click inside a fence is top-of-block, not a wrap-math walk.
	originX := p.listWidth + dividerWidth + 2 + p.editorLayout().leftMargin
	var visual int
	for i, line := range p.viewSurface.Lines {
		if strings.Contains(ansi.Strip(line), "tok10") {
			visual = i
			break
		}
	}
	p.previewScrollOff = 0
	clickY := p.editorContentStartY() + visual
	srcLine, srcCol := p.clickToSource(originX+8, clickY)
	a := p.viewSurface.At(visual)
	if a.Precise {
		t.Fatal("fence row should not be Precise")
	}
	if srcLine != a.SourceLine || srcCol != a.SourceCol {
		t.Fatalf("fence click = (%d,%d), want top-of-block (%d,%d)", srcLine, srcCol, a.SourceLine, a.SourceCol)
	}
}

func TestClickHardWrappedParagraphWordLandsOnThatWord(t *testing.T) {
	// Two source lines that glamour reflows into one paragraph. Wrap-count
	// disagreement used to mark the block !Precise and skip word snap.
	line0 := strings.TrimSpace(strings.Repeat("alpha ", 9))
	line1 := strings.TrimSpace(strings.Repeat("bravo ", 9)) + " TARGET"
	content := line0 + "\n" + line1
	p := layoutTestPlugin(t, content)
	p.width = 72
	p.listWidth = 18
	p.updateTextareaDimensions()
	p.markdownView = true
	p.previewMode = true
	p.activePane = PaneEditor
	p.invalidateViewSurface()
	p.ensureViewSurface()

	clickX, clickY, _ := clickWordInView(t, p, "TARGET")
	srcLine, srcCol := p.clickToSource(clickX, clickY)
	_ = p.enterEditAt(srcLine, srcCol)
	if srcLine != 1 {
		t.Fatalf("TARGET source line %d, want 1", srcLine)
	}
	if !sourceHasPrefix(p.editorTextarea.Value(), srcLine, srcCol, "TARGET") {
		t.Fatalf("hard-wrapped click landed at %d:%d, want TARGET", srcLine, srcCol)
	}
}

func sourceHasPrefix(content string, line, col int, word string) bool {
	lines := strings.Split(content, "\n")
	if line < 0 || line >= len(lines) {
		return false
	}
	runes := []rune(lines[line])
	if col < 0 || col > len(runes) {
		return false
	}
	return strings.HasPrefix(string(runes[col:]), word)
}

func TestClickDoesNotLandInsideSuperstring(t *testing.T) {
	content := strings.TrimSpace(strings.Repeat("word ", 40)) + " cat category " + strings.TrimSpace(strings.Repeat("word ", 40))
	p := layoutTestPlugin(t, content)
	p.width = 72
	p.listWidth = 18
	p.updateTextareaDimensions()
	p.markdownView = true
	p.previewMode = true
	p.activePane = PaneEditor
	p.invalidateViewSurface()
	p.ensureViewSurface()

	clickX, clickY, _ := clickWordInView(t, p, "cat")
	_, srcCol := p.clickToSource(clickX, clickY)
	got := []rune(p.editorTextarea.Value())
	if srcCol < 0 || srcCol > len(got) || !strings.HasPrefix(string(got[srcCol:]), "cat") {
		t.Fatalf("cat click landed at col %d", srcCol)
	}
	// Must be the standalone token, not the prefix of "category".
	after := got[srcCol+len([]rune("cat")):]
	if len(after) > 0 && (after[0] == 'e' || unicode.IsLetter(after[0])) {
		t.Fatalf("cat click landed inside %q", string(got[srcCol:min(srcCol+10, len(got))]))
	}
}

func TestClickWrappedRawWordLandsOnThatWord(t *testing.T) {
	content := uniqueWrappedWords()
	p := layoutTestPlugin(t, content)
	p.width = 72
	p.listWidth = 18
	p.updateTextareaDimensions()
	p.markdownView = false
	p.previewMode = true
	p.activePane = PaneEditor
	p.invalidateViewSurface()
	p.ensureViewSurface()

	const word = "tok37"
	clickX, clickY, _ := clickWordInView(t, p, word)
	srcLine, srcCol := p.clickToSource(clickX, clickY)
	_ = p.enterEditAt(srcLine, srcCol)

	got := []rune(p.editorTextarea.Value())
	if srcCol < 0 || srcCol > len(got) || !strings.HasPrefix(string(got[srcCol:]), word) {
		t.Fatalf("raw click landed at col %d, want %s", srcCol, word)
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
