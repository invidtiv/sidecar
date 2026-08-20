package notes

import (
	"strings"
	"testing"
)

// The mapped view surface is markdown rendered through the shared renderer, so
// a theme change must rebuild it - and must re-anchor the cursor and scroll
// through the source mapping rather than leave them on the old visual rows.
func TestViewSurfaceRebuildsOnMarkdownStyleChange(t *testing.T) {
	p := layoutTestPlugin(t, strings.Join([]string{
		"# Heading", "", wrappingParagraph(), "", "tail line",
	}, "\n"))
	p.markdownView = true
	p.previewMode = true
	p.invalidateViewSurface()
	p.ensureViewSurface()

	if p.viewSurfaceStyle != p.md.StyleKey() {
		t.Fatalf("viewSurfaceStyle = %q, want %q", p.viewSurfaceStyle, p.md.StyleKey())
	}
	if len(p.viewSurface.Lines) < 3 {
		t.Fatalf("expected a multi-row surface, got %d rows", len(p.viewSurface.Lines))
	}

	p.previewCursorLine = 2
	p.previewScrollOff = 1
	want := p.viewSurface.At(p.previewCursorLine)

	// A theme change leaves the source and width alone; only the style moves.
	p.viewSurfaceStyle = "stale-theme"
	p.ensureViewSurface()

	if p.viewSurfaceStyle != p.md.StyleKey() {
		t.Fatalf("viewSurfaceStyle = %q after rebuild, want %q", p.viewSurfaceStyle, p.md.StyleKey())
	}
	got := p.viewSurface.At(p.previewCursorLine)
	if got.SourceLine != want.SourceLine {
		t.Errorf("cursor source line = %d, want %d", got.SourceLine, want.SourceLine)
	}
	if p.previewScrollOff < 0 || p.previewScrollOff > p.previewCursorLine {
		t.Errorf("scroll %d does not keep cursor %d visible", p.previewScrollOff, p.previewCursorLine)
	}
}

// The raw (non-markdown) surface has no theme state, so it must not churn.
func TestRawViewSurfaceIgnoresMarkdownStyle(t *testing.T) {
	p := layoutTestPlugin(t, wrappingParagraph())
	p.markdownView = false
	p.invalidateViewSurface()
	p.ensureViewSurface()
	before := len(p.viewSurface.Lines)
	p.ensureViewSurface()
	if len(p.viewSurface.Lines) != before {
		t.Fatal("raw surface changed without a source or width change")
	}
}
