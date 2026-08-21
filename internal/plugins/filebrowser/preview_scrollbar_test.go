package filebrowser

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/mouse"
)

func newPreviewScrollbarTestPlugin(lineCount int) *Plugin {
	lines := make([]string, lineCount)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d of content", i+1)
	}
	return &Plugin{
		width:        100,
		height:       30,
		previewWidth: 60,
		previewFile:  "test.txt",
		previewSize:  100,
		previewLines: lines,
		mouseHandler: mouse.NewHandler(),
	}
}

func TestPreviewScrollbarSpacerWhenContentFits(t *testing.T) {
	p := newPreviewScrollbarTestPlugin(5)
	rendered := ansi.Strip(p.renderPreviewPane(15))
	rows := strings.Split(rendered, "\n")

	// Find rows containing content
	contentRows := 0
	for _, row := range rows {
		if strings.Contains(row, "line ") {
			contentRows++
			if !strings.HasSuffix(row, " ") {
				t.Errorf("content row does not end in spacer column: %q", row)
			}
		}
	}
	if contentRows != 5 {
		t.Fatalf("found %d content rows, want 5", contentRows)
	}
}

func TestPreviewScrollbarRendersThumbAndTrackOnOverflow(t *testing.T) {
	p := newPreviewScrollbarTestPlugin(50)
	visibleHeight := p.previewSourceRowCapacity()

	// At top (scroll = 0): thumb at top
	p.previewScroll = 0
	rendered := ansi.Strip(p.renderPreviewPane(visibleHeight))
	rows := strings.Split(rendered, "\n")

	var previewContentRows []string
	for _, row := range rows {
		if strings.Contains(row, "line ") {
			previewContentRows = append(previewContentRows, row)
		}
	}
	if len(previewContentRows) == 0 {
		t.Fatal("no content rows rendered")
	}

	if !strings.HasSuffix(previewContentRows[0], "┃") {
		t.Fatalf("first row at top should end with thumb ┃: %q", previewContentRows[0])
	}
	if !strings.HasSuffix(previewContentRows[len(previewContentRows)-1], "│") {
		t.Fatalf("last row at top should end with track │: %q", previewContentRows[len(previewContentRows)-1])
	}

	// At bottom: thumb at bottom
	p.previewScroll = len(p.previewLines) - visibleHeight
	rendered = ansi.Strip(p.renderPreviewPane(visibleHeight))
	rows = strings.Split(rendered, "\n")

	previewContentRows = nil
	for _, row := range rows {
		if strings.Contains(row, "line ") {
			previewContentRows = append(previewContentRows, row)
		}
	}
	if len(previewContentRows) == 0 {
		t.Fatal("no content rows rendered")
	}

	if !strings.HasSuffix(previewContentRows[len(previewContentRows)-1], "┃") {
		t.Fatalf("last row at bottom should end with thumb ┃: %q", previewContentRows[len(previewContentRows)-1])
	}
	if !strings.HasSuffix(previewContentRows[0], "│") {
		t.Fatalf("first row at bottom should end with track │: %q", previewContentRows[0])
	}
}

func TestPreviewScrollbarWithMarkdownRenderMode(t *testing.T) {
	p := newPreviewScrollbarTestPlugin(50)
	p.previewFile = "doc.md"
	p.markdownRenderMode = true
	mdLines := make([]string, 50)
	for i := range mdLines {
		mdLines[i] = fmt.Sprintf("markdown rendered line %d", i+1)
	}
	p.markdownRendered = mdLines
	visibleHeight := p.previewSourceRowCapacity()

	p.previewScroll = 0
	rendered := ansi.Strip(p.renderPreviewPane(visibleHeight))
	rows := strings.Split(rendered, "\n")

	var contentRows []string
	for _, row := range rows {
		if strings.Contains(row, "markdown rendered line") {
			contentRows = append(contentRows, row)
		}
	}
	if len(contentRows) == 0 {
		t.Fatal("no rendered markdown rows found")
	}

	if !strings.HasSuffix(contentRows[0], "┃") {
		t.Fatalf("first markdown row should end with thumb ┃: %q", contentRows[0])
	}
	if !strings.HasSuffix(contentRows[len(contentRows)-1], "│") {
		t.Fatalf("last markdown row should end with track │: %q", contentRows[len(contentRows)-1])
	}
}
