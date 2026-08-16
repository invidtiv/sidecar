package workspace

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/ui"
)

func TestSidebarHandleSitsInTheExistingGap(t *testing.T) {
	p := surfacePlugin(false)
	p.sidebarVisible = true
	split := p.previewSplitFor(p.width)
	idle := p.renderListView(p.width, p.height)
	assertColumnIsHandle(t, idle, split.SidebarWidth)

	p.hoverDividerRegion = regionPaneDivider
	hover := p.renderListView(p.width, p.height)
	p.hoverDividerRegion = ""
	p.mouseHandler.StartDrag(split.SidebarWidth, 2, regionPaneDivider, p.sidebarWidth)
	drag := p.renderListView(p.width, p.height)

	if idle == hover || hover == drag || idle == drag {
		t.Fatal("sidebar handle idle/hover/drag rendered the same")
	}
	if ansi.Strip(idle) != ansi.Strip(hover) || ansi.Strip(hover) != ansi.Strip(drag) {
		t.Fatal("sidebar handle state changed glyphs, want color only")
	}
}

func TestTreeHandlesOnBothAxesRespondToHoverAndDrag(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	threeLeafPaneTree(t, p, root)

	const width, height = 100, 24
	idleRows := composePaneTree(t, p, width, height)
	leaves, dividers, fits := LayoutPanes(p.paneRoot, p.previewLayoutBox(width, height), paneTreeFloors())
	if !fits || len(leaves) != 3 || len(dividers) != 2 {
		t.Fatalf("layout = %d leaves %d dividers fits=%v, want 3/2", len(leaves), len(dividers), fits)
	}
	assertDividersDrawn(t, idleRows, dividers)

	var rowSplit, colSplit Divider
	for _, split := range dividers {
		if split.Axis == SplitRows {
			rowSplit = split
		} else {
			colSplit = split
		}
	}
	if rowSplit.Box.H != 1 || colSplit.Box.W != 1 {
		t.Fatalf("gap budget grew: row=%+v col=%+v", rowSplit.Box, colSplit.Box)
	}

	idle := strings.Join(idleRows, "\n")
	p.hoverDividerRegion = regionPaneTreeDivider
	p.hoverDividerID = colSplit.SplitID
	hover := strings.Join(composePaneTree(t, p, width, height), "\n")

	p.hoverDividerRegion = ""
	p.hoverDividerID = 0
	p.paneDragSplitID = rowSplit.SplitID
	p.mouseHandler.StartDrag(rowSplit.Box.X, rowSplit.Box.Y, regionPaneTreeDivider, 40)
	drag := strings.Join(composePaneTree(t, p, width, height), "\n")

	if idle == hover {
		t.Fatal("hovering the column handle changed nothing")
	}
	if idle == drag {
		t.Fatal("dragging the row handle changed nothing")
	}
	if ansi.Strip(idle) != ansi.Strip(hover) {
		t.Fatal("column hover changed glyphs")
	}
	if ansi.Strip(idle) != ansi.Strip(drag) {
		t.Fatal("row drag changed glyphs")
	}

	// Only the hovered split's cells should restyle.
	hoverRows := strings.Split(hover, "\n")
	if idleRows[colSplit.Box.Y] == hoverRows[colSplit.Box.Y] {
		t.Fatal("hovered column handle cell is unchanged")
	}
	if idleRows[rowSplit.Box.Y] != hoverRows[rowSplit.Box.Y] {
		t.Fatal("unhovered row handle restyled with the column hover")
	}
}

// A row divider widens onto the border row of the leaf on each side, never onto
// a header row — that is what keeps a stacked leaf's tabs and close button
// reachable while the handle stays easy to grab. The property is measured
// against a real placement, because it is a fact about where layout puts a
// divider relative to a leaf's chrome, not about the widening rule alone.
func TestPaneDividerHitBoxDoesNotCoverAStackedHeader(t *testing.T) {
	root := &PaneNode{ID: 3, Split: &PaneSplit{
		Axis: SplitRows, Ratio: 50,
		A: &PaneNode{ID: 1, Kind: PaneTerminal},
		B: &PaneNode{ID: 2, Kind: PaneDoc},
	}}
	leaves, dividers, fits := LayoutPanes(root, Box{W: 120, H: 40}, paneTreeFloors())
	if !fits || len(dividers) != 1 {
		t.Fatalf("stacked layout did not fit: leaves=%d dividers=%d", len(leaves), len(dividers))
	}
	hit := paneDividerHitBox(dividers[0])
	if hit.W != dividers[0].Box.W {
		t.Fatalf("row hit box %+v lost the divider's own width", hit)
	}
	if hit.H < dividerHitWidth {
		t.Fatalf("row hit height = %d, want at least %d", hit.H, dividerHitWidth)
	}
	for _, placement := range leaves {
		header := leafGeometry(placement.Box).Inner.Y
		if header >= hit.Y && header < hit.Y+hit.H {
			t.Fatalf("row hit box %+v covers leaf %+v's header row %d", hit, placement.Box, header)
		}
	}
}

func TestSidebarHandleUsesSharedRenderer(t *testing.T) {
	got := ui.RenderHandle(8, true, ui.HandleHover)
	if !strings.Contains(ansi.Strip(got), "┃") {
		t.Fatalf("shared handle missing bar: %q", got)
	}
}

func assertColumnIsHandle(t *testing.T, view string, x int) {
	t.Helper()
	lines := strings.Split(view, "\n")
	for i, line := range lines {
		cells := []rune(ansi.Strip(line))
		if x < 0 || x >= len(cells) {
			t.Fatalf("row %d has no column %d", i, x)
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

func TestTreeHandleHoverTracksRegion(t *testing.T) {
	p := surfacePlugin(false)
	p.handleMouseHover(mouse.MouseAction{
		Type:   mouse.ActionHover,
		Region: &mouse.Region{ID: regionPaneTreeDivider, Data: 11},
	})
	if p.hoverDividerRegion != regionPaneTreeDivider || p.hoverDividerID != 11 {
		t.Fatalf("hover = %q/%d, want tree divider 11", p.hoverDividerRegion, p.hoverDividerID)
	}
	p.handleMouseHover(mouse.MouseAction{Type: mouse.ActionHover})
	if p.hoverDividerRegion != "" || p.hoverDividerID != 0 {
		t.Fatalf("hover lingered after leaving: %q/%d", p.hoverDividerRegion, p.hoverDividerID)
	}
}
