package workspace

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/docview"
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

	leaves, dividers, fits := LayoutPanes(p.paneRoot, p.previewLayoutBox(width, height), paneTreeFloors())
	if !fits || len(leaves) != 3 || len(dividers) != 2 {
		t.Fatalf("layout = %d leaves %d dividers fits=%v, want 3/2", len(leaves), len(dividers), fits)
	}
	// Each divider must carry the 1-cell gap at an interior handle cell; the
	// shared renderer deliberately leaves both endpoints blank.
	for _, split := range dividers {
		x, y := split.Box.X, split.Box.Y
		want := "┃"
		if split.Axis == SplitRows {
			want = "━"
			x++
		} else {
			y++
		}
		row := []rune(ansi.Strip(lines[y]))
		if got := string(row[x]); got != want {
			t.Fatalf("divider %d at (%d,%d) drew %q, want handle %q", split.SplitID, x, y, got, want)
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
	_, dividers, _ := LayoutPanes(p.paneRoot, p.previewLayoutBox(width, height), paneTreeFloors())
	outer, inner := dividers[0], dividers[1]
	if outer.SplitID != p.paneRoot.ID || inner.SplitID != p.paneRoot.Split.B.ID {
		t.Fatalf("dividers = %d then %d, want root %d then nested %d",
			outer.SplitID, inner.SplitID, p.paneRoot.ID, p.paneRoot.Split.B.ID)
	}
	// The nested divider's first column sits inside the outer divider's
	// three-cell hit target, so both dividers claim this point.
	x, y := inner.Box.X, inner.Box.Y
	if x > outer.Box.X+dividerHitWidth-2 {
		t.Fatalf("dividers do not contest a cell: outer x=%d inner x=%d", outer.Box.X, inner.Box.X)
	}
	hit := p.mouseHandler.HitMap.Test(x, y)
	if hit == nil || hit.ID != regionPaneTreeDivider || hit.Data != inner.SplitID {
		t.Fatalf("contested cell (%d,%d) resolves to %#v, want nested split %d", x, y, hit, inner.SplitID)
	}

	// The outer divider still owns the rows the nested one does not reach.
	hit = p.mouseHandler.HitMap.Test(outer.Box.X, outer.Box.Y)
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
	leaves, _, _ := LayoutPanes(p.paneRoot, p.previewLayoutBox(width, height), paneTreeFloors())
	for _, placement := range leaves {
		if placement.Node.Kind != PaneDoc {
			continue
		}
		want := placement.Box
		var found bool
		for _, region := range p.mouseHandler.HitMap.Regions() {
			if region.ID == regionPaneLeaf && region.Data == placement.Node.ID {
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
