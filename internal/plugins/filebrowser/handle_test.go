package filebrowser

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/mouse"
)

func TestFilesHandleUsesSharedRendererAndStillDrags(t *testing.T) {
	p := newDragTestPlugin(t)
	p.treeVisible = true
	p.calculatePaneWidths()

	idle := p.renderNormalPanes()
	assertFilesHandleColumn(t, idle, p.treeWidth)

	p.hoverDivider = true
	hover := p.renderNormalPanes()
	p.hoverDivider = false
	p.mouseHandler.StartDrag(p.treeWidth, 5, regionPaneDivider, p.treeWidth)
	drag := p.renderNormalPanes()

	if idle == hover || hover == drag || idle == drag {
		t.Fatal("files handle idle/hover/drag rendered the same")
	}
	if ansi.Strip(idle) != ansi.Strip(hover) || ansi.Strip(hover) != ansi.Strip(drag) {
		t.Fatal("files handle state changed glyphs, want color only")
	}

	before := p.treeWidth
	if _, cmd := p.handleMouseDrag(mouse.MouseAction{
		Type:        mouse.ActionDrag,
		DragDX:      6,
		DragStartID: regionPaneDivider,
	}); cmd != nil {
		t.Fatalf("divider drag returned cmd %T", cmd)
	}
	if p.treeWidth != before+6 {
		t.Fatalf("treeWidth = %d, want %d after drag", p.treeWidth, before+6)
	}
	if p.mouseHandler.DragRegion() != regionPaneDivider {
		t.Fatalf("DragRegion = %q, want %q", p.mouseHandler.DragRegion(), regionPaneDivider)
	}
}

func TestFilesHandleHoverTracksDividerRegion(t *testing.T) {
	p := newDragTestPlugin(t)
	p.handleMouseHover(mouse.MouseAction{
		Type:   mouse.ActionHover,
		Region: &mouse.Region{ID: regionPaneDivider},
	})
	if !p.hoverDivider {
		t.Fatal("hovering the divider did not set hoverDivider")
	}
	p.handleMouseHover(mouse.MouseAction{Type: mouse.ActionHover})
	if p.hoverDivider {
		t.Fatal("hover lingered after leaving the divider")
	}
}

func assertFilesHandleColumn(t *testing.T, view string, x int) {
	t.Helper()
	lines := strings.Split(view, "\n")
	for i, line := range lines {
		cells := []rune(ansi.Strip(line))
		if x >= len(cells) {
			t.Fatalf("row %d has no handle column %d", i, x)
		}
		want := "┃"
		if i == 0 || i == len(lines)-1 {
			want = " "
		}
		if got := string(cells[x]); got != want {
			t.Fatalf("row %d col %d = %q, want %q", i, x, got, want)
		}
	}
}
