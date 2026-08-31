package filebrowser

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/clip"
	"github.com/marcus/sidecar/internal/ui"
)

func renderedMarkdownSelectionPlugin(t *testing.T, treeVisible bool) *Plugin {
	t.Helper()
	p := newDragTestPlugin(t)
	p.treeVisible = treeVisible
	p.previewFile = "README.md"
	p.previewLines = []string{"# source heading that must not be copied"}
	p.markdownRenderMode = true
	p.markdownRendered = []string{
		"  \x1b[1mRendered heading\x1b[0m     ",
		"rendered body     ",
	}
	p.activePane = PanePreview
	p.previewWrapEnabled = false
	p.previewScroll = 0
	_ = p.View(100, 30)
	return p
}

func TestRenderedMarkdownSelectionUsesDrawnRowsAndCopiesPlainText(t *testing.T) {
	p := renderedMarkdownSelectionPlugin(t, true)
	rect := p.previewTextRect()
	if rect.H != 2 {
		t.Fatalf("rendered text rect height = %d, want the two Glamour rows", rect.H)
	}
	if region := p.mouseHandler.HitMap.Test(rect.X, rect.Y+1); region == nil || region.ID != regionPreviewLine {
		t.Fatalf("second rendered row has region %#v, want %s", region, regionPreviewLine)
	}

	press(t, p, rect.X, rect.Y)
	motion(t, p, rect.X+rect.W-1, rect.Y+1)
	release(t, p, rect.X+rect.W-1, rect.Y+1)
	if !p.selection.HasSelection() {
		t.Fatal("drag over rendered Markdown created no selection")
	}

	frame := p.renderPreviewPane(p.visibleContentHeight())
	if !strings.Contains(frame, ui.GetSelectionBgANSI()) {
		t.Fatal("rendered Markdown selection has no visible highlight")
	}

	clip.ResetRecent()
	t.Cleanup(clip.ResetRecent)
	cmd := p.copySelectedTextToClipboard()
	if cmd == nil {
		t.Fatal("rendered Markdown selection returned no copy command")
	}
	_ = cmd()
	copied, ok := clip.LastCopied()
	if !ok {
		t.Fatal("rendered Markdown selection never reached the clipboard")
	}
	if strings.Contains(copied, "# source") {
		t.Fatalf("copied Markdown source instead of rendered text: %q", copied)
	}
	if strings.Contains(copied, "\x1b[") || ansi.Strip(copied) != copied {
		t.Fatalf("copied text contains terminal styling: %q", copied)
	}
	if got, want := copied, "  Rendered heading\nrendered body"; got != want {
		t.Fatalf("copied rendered selection = %q, want %q", got, want)
	}
}

func TestRenderedMarkdownSelectionRegionsFollowVisibleBody(t *testing.T) {
	for _, treeVisible := range []bool{true, false} {
		name := "tree-collapsed"
		if treeVisible {
			name = "tree-visible"
		}
		t.Run(name, func(t *testing.T) {
			p := renderedMarkdownSelectionPlugin(t, treeVisible)
			rect := p.previewTextRect()
			if region := p.mouseHandler.HitMap.Test(rect.X, rect.Y+rect.H); region != nil && region.ID == regionPreviewLine {
				t.Fatalf("padding below the rendered body is selectable: %#v", region)
			}
		})
	}
}
