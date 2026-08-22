package filebrowser

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/markdown"
)

func TestIsMarkdownFile(t *testing.T) {
	tests := []struct {
		name        string
		previewFile string
		want        bool
	}{
		{"md extension", "README.md", true},
		{"markdown extension", "docs/guide.markdown", true},
		{"uppercase MD", "test.MD", true},
		{"mixed case", "Test.Md", true},
		{"go file", "main.go", false},
		{"txt file", "notes.txt", false},
		{"empty path", "", false},
		{"no extension", "README", false},
		{"md in path but not extension", "docs/md/file.txt", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{
				previewFile: tt.previewFile,
			}
			if got := p.isMarkdownFile(); got != tt.want {
				t.Errorf("isMarkdownFile() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestToggleMarkdownRender(t *testing.T) {
	t.Run("toggles mode for markdown file", func(t *testing.T) {
		renderer, _ := markdown.NewRenderer()
		p := &Plugin{
			previewFile:      "README.md",
			previewLines:     []string{"# Hello", "", "World"},
			markdownRenderer: renderer,
			previewWidth:     80,
		}

		// Initially off
		if p.markdownRenderMode {
			t.Error("markdownRenderMode should start false")
		}

		// Toggle on
		p.toggleMarkdownRender()
		if !p.markdownRenderMode {
			t.Error("markdownRenderMode should be true after toggle")
		}

		// Should have rendered content
		if len(p.markdownRendered) == 0 {
			t.Error("markdownRendered should have content after toggle on")
		}

		// Toggle off
		p.toggleMarkdownRender()
		if p.markdownRenderMode {
			t.Error("markdownRenderMode should be false after second toggle")
		}
	})

	t.Run("no-op for non-markdown file", func(t *testing.T) {
		p := &Plugin{
			previewFile: "main.go",
		}

		p.toggleMarkdownRender()
		if p.markdownRenderMode {
			t.Error("markdownRenderMode should remain false for non-markdown file")
		}
	})

	t.Run("no-op for empty preview file", func(t *testing.T) {
		p := &Plugin{
			previewFile: "",
		}

		p.toggleMarkdownRender()
		if p.markdownRenderMode {
			t.Error("markdownRenderMode should remain false for empty file")
		}
	})
}

func TestRenderMarkdownContent(t *testing.T) {
	t.Run("renders content with renderer", func(t *testing.T) {
		renderer, _ := markdown.NewRenderer()
		p := &Plugin{
			previewFile:      "test.md",
			previewLines:     []string{"# Header", "", "Some text here"},
			markdownRenderer: renderer,
			previewWidth:     80,
		}

		p.renderMarkdownContent()

		if len(p.markdownRendered) == 0 {
			t.Error("markdownRendered should have content")
		}
	})

	t.Run("safe with nil renderer", func(t *testing.T) {
		p := &Plugin{
			previewFile:      "test.md",
			previewLines:     []string{"# Header"},
			markdownRenderer: nil,
			previewWidth:     80,
		}

		// Should not panic
		p.renderMarkdownContent()

		if len(p.markdownRendered) != 0 {
			t.Error("markdownRendered should be empty with nil renderer")
		}
	})

	t.Run("safe with empty preview lines", func(t *testing.T) {
		renderer, _ := markdown.NewRenderer()
		p := &Plugin{
			previewFile:      "test.md",
			previewLines:     []string{},
			markdownRenderer: renderer,
			previewWidth:     80,
		}

		// Should not panic
		p.renderMarkdownContent()
	})

	t.Run("respects width for rendering", func(t *testing.T) {
		renderer, _ := markdown.NewRenderer()
		content := []string{"This is a very long line that should wrap when the width is narrow enough to cause wrapping behavior"}

		p40 := &Plugin{
			previewFile:      "test.md",
			previewLines:     content,
			markdownRenderer: renderer,
			previewWidth:     46, // contentWidth 41, glamour word-wrap 43
		}
		p40.renderMarkdownContent()

		p100 := &Plugin{
			previewFile:      "test.md",
			previewLines:     content,
			markdownRenderer: renderer,
			previewWidth:     106, // contentWidth 101, glamour word-wrap 103
		}
		p100.renderMarkdownContent()

		// Narrower width should produce more lines (or equal)
		if len(p40.markdownRendered) < len(p100.markdownRendered) {
			t.Errorf("narrow width produced fewer lines: %d vs %d",
				len(p40.markdownRendered), len(p100.markdownRendered))
		}
	})
}

// TestRenderMarkdownFillsPreviewContentWidth pins the rendered mode's wrap
// contract: every rendered row must fit the frame's content column, and a
// long paragraph must actually fill it — Glamour's 2-column document margin
// becomes the mode's visual indent while the text reaches the right edge.
// The old previewWidth−6 word-wrap left the render floating several columns
// short of the frame (td-65095b).
func TestRenderMarkdownFillsPreviewContentWidth(t *testing.T) {
	renderer, err := markdown.NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	p := &Plugin{
		previewFile:      "test.md",
		previewLines:     []string{"# Head", "", strings.Repeat("word ", 120)},
		markdownRenderer: renderer,
		previewWidth:     106,
	}
	p.renderMarkdownContent()
	if len(p.markdownRendered) == 0 {
		t.Fatal("markdownRendered is empty")
	}

	contentWidth := p.previewContentWidth() // 101 at previewWidth 106
	longest := 0
	for _, line := range p.markdownRendered {
		w := ansi.StringWidth(line)
		if w > contentWidth {
			t.Fatalf("rendered line of %d cells overflows the %d-cell content column: %q",
				w, contentWidth, ansi.Strip(line))
		}
		if w > longest {
			longest = w
		}
	}
	if longest != contentWidth {
		t.Fatalf("longest rendered line is %d cells, want %d: the render stops short of the frame",
			longest, contentWidth)
	}
}
