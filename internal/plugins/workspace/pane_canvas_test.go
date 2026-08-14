package workspace

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/mouse"
)

// nestedDocPaneTree builds terminal | (doc / doc): the shallowest tree the
// two-leaf renderer could not compose and the shallowest one whose divider hit
// targets overlap.
func nestedDocPaneTree(t *testing.T, p *Plugin, root string) {
	t.Helper()
	p.paneRoot = &PaneNode{ID: 10, Split: &PaneSplit{Axis: SplitCols, Ratio: 50,
		A: &PaneNode{ID: 1, Kind: PaneTerminal},
		B: &PaneNode{ID: 11, Split: &PaneSplit{Axis: SplitRows, Ratio: 50,
			A: &PaneNode{ID: 2, Kind: PaneDoc, ContentID: 2},
			B: &PaneNode{ID: 3, Kind: PaneDoc, ContentID: 3},
		}},
	}}
	p.paneFocus = 2
	p.paneNextID = 12
	p.docs = make(map[int]*docPane)
	for id, rel := range map[int]string{2: "one.md", 3: "two.md"} {
		writeDocPaneFixture(t, root, rel, "# "+rel+"\n")
		viewer := docview.New(nil)
		viewer.Load(id, root, rel, 0, p.ctx.Epoch)
		p.docs[id] = newDocPane(id, root, "shell:test-shell", viewer)
	}
}

func TestNestedPaneTreeFillsItsBoxAndNamesEveryLeaf(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	nestedDocPaneTree(t, p, root)

	const width, height = 120, 24
	got, ok := p.renderDocumentSplit(width, height)
	if !ok {
		t.Fatal("nested pane tree was not rendered")
	}
	lines := strings.Split(got, "\n")
	if len(lines) != height {
		t.Fatalf("rendered rows = %d, want %d", len(lines), height)
	}
	for row, line := range lines {
		if cells := ansi.StringWidth(line); cells != width {
			t.Fatalf("row %d width = %d, want %d: %q", row, cells, width, line)
		}
	}
	stripped := ansi.Strip(got)
	if !strings.Contains(stripped, "one.md") || !strings.Contains(stripped, "two.md") {
		t.Fatalf("nested tree did not draw both document leaves: %q", stripped)
	}

	leaves, dividers, fits := LayoutPanes(p.paneRoot, Box{W: width, H: height}, paneTreeFloors())
	if !fits || len(leaves) != 3 || len(dividers) != 2 {
		t.Fatalf("layout = %d leaves %d dividers fits=%v, want 3/2", len(leaves), len(dividers), fits)
	}
	// Each divider row must carry the divider's own rune at the divider's own
	// column: a block drawn short or long is what walks a nested divider sideways.
	for _, split := range dividers {
		row := []rune(ansi.Strip(lines[split.Box.Y]))
		if got := string(row[split.Box.X]); got != "│" && got != "─" {
			t.Fatalf("divider %d at (%d,%d) drew %q", split.SplitID, split.Box.X, split.Box.Y, got)
		}
	}
}

func TestNestedPaneTreeInnermostDividerWinsContestedCell(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	nestedDocPaneTree(t, p, root)

	const width, height = 120, 24
	p.mouseHandler.Clear()
	if _, ok := p.renderDocumentSplit(width, height); !ok {
		t.Fatal("nested pane tree was not rendered")
	}
	absolute, ok := p.previewContentBox()
	if !ok {
		t.Fatal("preview content box is unplaced")
	}
	_, dividers, _ := LayoutPanes(p.paneRoot, Box{W: width, H: height}, paneTreeFloors())
	outer, inner := dividers[0], dividers[1]
	if outer.SplitID != p.paneRoot.ID || inner.SplitID != p.paneRoot.Split.B.ID {
		t.Fatalf("dividers = %d then %d, want root %d then nested %d",
			outer.SplitID, inner.SplitID, p.paneRoot.ID, p.paneRoot.Split.B.ID)
	}
	// The nested divider's first column sits inside the outer divider's
	// three-cell hit target, so both dividers claim this point.
	x, y := absolute.X+inner.Box.X, absolute.Y+inner.Box.Y
	if x > absolute.X+outer.Box.X+dividerHitWidth-2 {
		t.Fatalf("dividers do not contest a cell: outer x=%d inner x=%d", outer.Box.X, inner.Box.X)
	}
	hit := p.mouseHandler.HitMap.Test(x, y)
	if hit == nil || hit.ID != regionPaneTreeDivider || hit.Data != inner.SplitID {
		t.Fatalf("contested cell (%d,%d) resolves to %#v, want nested split %d", x, y, hit, inner.SplitID)
	}

	// The outer divider still owns the rows the nested one does not reach.
	hit = p.mouseHandler.HitMap.Test(absolute.X+outer.Box.X, absolute.Y+outer.Box.Y)
	if hit == nil || hit.ID != regionPaneTreeDivider || hit.Data != outer.SplitID {
		t.Fatalf("outer divider row resolves to %#v, want root split %d", hit, outer.SplitID)
	}
}

func TestNestedPaneTreeGivesEveryDocumentLeafItsOwnRegion(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	nestedDocPaneTree(t, p, root)

	const width, height = 120, 24
	p.mouseHandler.Clear()
	if _, ok := p.renderDocumentSplit(width, height); !ok {
		t.Fatal("nested pane tree was not rendered")
	}
	absolute, _ := p.previewContentBox()
	leaves, _, _ := LayoutPanes(p.paneRoot, Box{W: width, H: height}, paneTreeFloors())
	for _, placement := range leaves {
		if placement.Node.Kind != PaneDoc {
			continue
		}
		want := mouse.Rect{
			X: absolute.X + placement.Box.X, Y: absolute.Y + placement.Box.Y,
			W: placement.Box.W, H: placement.Box.H,
		}
		var found bool
		for _, region := range p.mouseHandler.HitMap.Regions() {
			if region.ID == regionDocPane && region.Data == placement.Node.ID {
				if region.Rect != want {
					t.Fatalf("doc leaf %d region = %+v, want the box it was drawn in %+v",
						placement.Node.ID, region.Rect, want)
				}
				found = true
			}
		}
		if !found {
			t.Fatalf("doc leaf %d was drawn without a hit region", placement.Node.ID)
		}
	}
}
