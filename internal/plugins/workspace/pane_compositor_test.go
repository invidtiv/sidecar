package workspace

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/mouse"
)

// compositorDocLeaf gives one leaf a document whose load has already been
// applied, so a golden cell is the document's own text rather than the loading
// state every doc leaf would otherwise share.
func compositorDocLeaf(t *testing.T, p *Plugin, root string, leafID int, rel, body string) {
	t.Helper()
	writeDocPaneFixture(t, root, rel, body)
	viewer := docview.New(nil)
	loaded, ok := viewer.Load(leafID, root, rel, 0, p.ctx.Epoch)().(docview.LoadedMsg)
	if !ok {
		t.Fatalf("document %s did not load", rel)
	}
	if !viewer.SetResult(loaded) {
		t.Fatalf("document %s rejected its own load result", rel)
	}
	p.docs[leafID] = newDocPane(leafID, root, "shell:test-shell", viewer)
}

// threeLeafPaneTree is a terminal above two documents side by side: three
// leaves and two dividers on both axes, which the two-leaf renderer could not
// compose at all.
func threeLeafPaneTree(t *testing.T, p *Plugin, root string) {
	t.Helper()
	p.paneRoot = &PaneNode{ID: 10, Split: &PaneSplit{Axis: SplitRows, Ratio: 40,
		A: &PaneNode{ID: 1, Kind: PaneTerminal},
		B: &PaneNode{ID: 11, Split: &PaneSplit{Axis: SplitCols, Ratio: 50,
			A: &PaneNode{ID: 2, Kind: PaneDoc, ContentID: 2},
			B: &PaneNode{ID: 3, Kind: PaneDoc, ContentID: 3},
		}},
	}}
	p.paneFocus = 2
	p.paneNextID = 12
	p.docs = make(map[int]*docPane)
	compositorDocLeaf(t, p, root, 2, "left.md", "# left\n\nleft body\n")
	compositorDocLeaf(t, p, root, 3, "right.md", "# right\n\nright body\n")
}

// fourLeafPaneTree splits both halves of a column split, so every divider but
// the root's is nested and each one ends inside a neighbour's hit target.
func fourLeafPaneTree(t *testing.T, p *Plugin, root string) {
	t.Helper()
	p.paneRoot = &PaneNode{ID: 10, Split: &PaneSplit{Axis: SplitCols, Ratio: 50,
		A: &PaneNode{ID: 11, Split: &PaneSplit{Axis: SplitRows, Ratio: 50,
			A: &PaneNode{ID: 1, Kind: PaneTerminal},
			B: &PaneNode{ID: 2, Kind: PaneDoc, ContentID: 2},
		}},
		B: &PaneNode{ID: 12, Split: &PaneSplit{Axis: SplitRows, Ratio: 50,
			A: &PaneNode{ID: 3, Kind: PaneDoc, ContentID: 3},
			B: &PaneNode{ID: 4, Kind: PaneDoc, ContentID: 4},
		}},
	}}
	p.paneFocus = 2
	p.paneNextID = 13
	p.docs = make(map[int]*docPane)
	compositorDocLeaf(t, p, root, 2, "under-terminal.md", "# under\n\nunder body\n")
	compositorDocLeaf(t, p, root, 3, "upper-right.md", "# upper\n\nupper body\n")
	compositorDocLeaf(t, p, root, 4, "lower-right.md", "# lower\n\nlower body\n")
}

// composePaneTree renders the tree and returns its rows, having established
// that every row is exactly width cells: a block that renders short or long is
// what walks a divider sideways once splits nest.
func composePaneTree(t *testing.T, p *Plugin, width, height int) []string {
	t.Helper()
	p.mouseHandler.Clear()
	rendered, ok := p.renderDocumentSplit(width, height)
	if !ok {
		t.Fatal("pane tree was not rendered")
	}
	rows := strings.Split(rendered, "\n")
	if len(rows) != height {
		t.Fatalf("rendered rows = %d, want %d", len(rows), height)
	}
	for row, line := range rows {
		if cells := ansi.StringWidth(line); cells != width {
			t.Fatalf("row %d width = %d, want %d: %q", row, cells, width, line)
		}
	}
	return rows
}

// assertPaneTreeGolden compares every cell of the composed grid, glyph by
// glyph. Styling is stripped so the golden survives a theme change; the cell a
// glyph landed in is what the compositor decides.
func assertPaneTreeGolden(t *testing.T, rows []string, name string) {
	t.Helper()
	got := trimGoldenRows(ansi.Strip(strings.Join(rows, "\n")) + "\n")
	path := filepath.Join("testdata", name)
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if got != trimGoldenRows(string(want)) {
		t.Fatalf("composed cells differ from %s\ngot:\n%s\nwant:\n%s", path, got, want)
	}
}

// trimGoldenRows drops trailing spaces that pad a row out to its box. The
// compositor already requires every row to be exactly width cells; keeping
// those pads in the file makes git diff --check fail on an otherwise empty
// header remainder.
func trimGoldenRows(s string) string {
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.Join(lines, "\n") + "\n"
}

// assertDividersDrawn requires each divider's own rune in every cell of the box
// LayoutPanes gave it, on both axes.
func assertDividersDrawn(t *testing.T, rows []string, dividers []Divider) {
	t.Helper()
	for _, split := range dividers {
		want := "│"
		if split.Axis == SplitRows {
			want = "─"
		}
		for y := split.Box.Y; y < split.Box.Y+split.Box.H; y++ {
			cells := []rune(ansi.Strip(rows[y]))
			for x := split.Box.X; x < split.Box.X+split.Box.W; x++ {
				if got := string(cells[x]); got != want {
					t.Fatalf("split %d drew %q at (%d,%d), want %q", split.SplitID, got, x, y, want)
				}
			}
		}
	}
}

// assertPaneTreeRegions requires every document leaf and every divider to be
// clickable at exactly the box it was drawn in, offset by the preview content
// box: pixels and clicks come from one set of placements or they can disagree.
func assertPaneTreeRegions(t *testing.T, p *Plugin, leaves []Placement, dividers []Divider) {
	t.Helper()
	origin, ok := p.previewContentBox()
	if !ok {
		t.Fatal("preview content box is unplaced")
	}
	regions := p.mouseHandler.HitMap.Regions()
	find := func(id string, data int) (mouse.Rect, bool) {
		for _, region := range regions {
			if region.ID == id && region.Data == data {
				return region.Rect, true
			}
		}
		return mouse.Rect{}, false
	}
	for _, placement := range leaves {
		region := ""
		switch placement.Node.Kind {
		case PaneDoc:
			region = regionDocPane
		case PaneIssue:
			region = regionIssuePane
		default:
			continue
		}
		want := mouse.Rect{
			X: origin.X + placement.Box.X, Y: origin.Y + placement.Box.Y,
			W: placement.Box.W, H: placement.Box.H,
		}
		got, found := find(region, placement.Node.ID)
		if !found {
			t.Fatalf("content leaf %d was drawn without a hit region", placement.Node.ID)
		}
		if got != want {
			t.Fatalf("content leaf %d region = %+v, want the box it was drawn in %+v",
				placement.Node.ID, got, want)
		}
	}
	for _, split := range dividers {
		want := mouse.Rect{
			X: origin.X + split.Box.X, Y: origin.Y + split.Box.Y,
			W: split.Box.W, H: split.Box.H,
		}
		if split.Axis == SplitCols {
			want.X--
			want.W = dividerHitWidth
		} else {
			want.Y--
			want.H = dividerHitWidth - 1
		}
		got, found := find(regionPaneTreeDivider, split.SplitID)
		if !found {
			t.Fatalf("split %d was drawn without a hit region", split.SplitID)
		}
		if got != want {
			t.Fatalf("split %d region = %+v, want its drawn box widened to grab %+v",
				split.SplitID, got, want)
		}
	}
}

func TestThreeLeafPaneTreeComposesEveryCell(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	threeLeafPaneTree(t, p, root)

	const width, height = 100, 24
	rows := composePaneTree(t, p, width, height)
	assertPaneTreeGolden(t, rows, "pane-tree-three-leaf.txt")

	leaves, dividers, fits := LayoutPanes(p.paneRoot, Box{W: width, H: height}, paneTreeFloors())
	if !fits || len(leaves) != 3 || len(dividers) != 2 {
		t.Fatalf("layout = %d leaves %d dividers fits=%v, want 3/2", len(leaves), len(dividers), fits)
	}
	assertDividersDrawn(t, rows, dividers)
	assertPaneTreeRegions(t, p, leaves, dividers)
}

func TestNestedFourLeafPaneTreeComposesEveryCell(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	fourLeafPaneTree(t, p, root)

	const width, height = 100, 24
	rows := composePaneTree(t, p, width, height)
	assertPaneTreeGolden(t, rows, "pane-tree-four-leaf.txt")

	leaves, dividers, fits := LayoutPanes(p.paneRoot, Box{W: width, H: height}, paneTreeFloors())
	if !fits || len(leaves) != 4 || len(dividers) != 3 {
		t.Fatalf("layout = %d leaves %d dividers fits=%v, want 4/3", len(leaves), len(dividers), fits)
	}
	assertDividersDrawn(t, rows, dividers)
	assertPaneTreeRegions(t, p, leaves, dividers)
}

func TestNestedFourLeafPaneTreeGivesContestedCellsToTheInnerSplit(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	fourLeafPaneTree(t, p, root)

	const width, height = 100, 24
	composePaneTree(t, p, width, height)
	origin, ok := p.previewContentBox()
	if !ok {
		t.Fatal("preview content box is unplaced")
	}
	_, dividers, _ := LayoutPanes(p.paneRoot, Box{W: width, H: height}, paneTreeFloors())
	outer := dividers[0]
	if outer.SplitID != p.paneRoot.ID {
		t.Fatalf("first divider = split %d, want the root %d", outer.SplitID, p.paneRoot.ID)
	}
	// Each nested row divider ends inside the root column divider's three-cell
	// hit target, on the side of it that divider lies on.
	for _, inner := range dividers[1:] {
		x := inner.Box.X
		if inner.Box.X < outer.Box.X {
			x = inner.Box.X + inner.Box.W - 1
		}
		if x < outer.Box.X-1 || x > outer.Box.X+dividerHitWidth-2 {
			t.Fatalf("split %d does not contest a cell with the root: x=%d, root x=%d",
				inner.SplitID, x, outer.Box.X)
		}
		hit := p.mouseHandler.HitMap.Test(origin.X+x, origin.Y+inner.Box.Y)
		if hit == nil || hit.ID != regionPaneTreeDivider || hit.Data != inner.SplitID {
			t.Fatalf("contested cell (%d,%d) resolves to %#v, want nested split %d",
				x, inner.Box.Y, hit, inner.SplitID)
		}
	}
	// The root divider still owns the rows no nested divider reaches.
	hit := p.mouseHandler.HitMap.Test(origin.X+outer.Box.X, origin.Y+outer.Box.Y)
	if hit == nil || hit.ID != regionPaneTreeDivider || hit.Data != outer.SplitID {
		t.Fatalf("root divider row resolves to %#v, want split %d", hit, outer.SplitID)
	}
}

func TestPaneTreeDrawsFocusOnlyIntoTheFocusedLeafsHeader(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	fourLeafPaneTree(t, p, root)

	const width, height = 100, 24
	styled := map[int]string{}
	stripped := map[int]string{}
	for _, leafID := range []int{2, 3} {
		p.paneFocus = leafID
		rows := composePaneTree(t, p, width, height)
		styled[leafID] = strings.Join(rows, "\n")
		stripped[leafID] = ansi.Strip(styled[leafID])
	}
	if stripped[2] != stripped[3] {
		t.Fatal("moving focus between leaves moved a cell; focus is styling, not geometry")
	}
	if styled[2] == styled[3] {
		t.Fatal("moving focus between leaves changed nothing; the frame's focus never reached a header")
	}
	for _, leafID := range []int{2, 3} {
		other := 5 - leafID
		doc := p.docs[leafID]
		tabs := layoutDocTabStrip(doc, paneChipWidthFor(t, p, leafID, width, height), true).Tabs
		if len(tabs) == 0 {
			t.Fatalf("leaf %d has no tab strip", leafID)
		}
		active := tabs[0].Rendered
		if !strings.Contains(styled[leafID], active) {
			t.Fatalf("leaf %d holds focus but its active tab is not drawn", leafID)
		}
		if strings.Contains(styled[other], active) {
			t.Fatalf("leaf %d drew the focused tab while leaf %d held focus", leafID, other)
		}
	}
}

// fiveLeafPaneTree nests three levels: the root's column split, a row split in
// each half, and a column split inside the left half's lower half. Its deepest
// divider's hit target overlaps its parent's, which overlaps the root's — the
// shape the four-leaf tree cannot produce, where one level of nesting makes
// "each split before the splits inside it" and "innermost last" the same order.
func fiveLeafPaneTree(t *testing.T, p *Plugin, root string) {
	t.Helper()
	p.paneRoot = &PaneNode{ID: 10, Split: &PaneSplit{Axis: SplitCols, Ratio: 50,
		A: &PaneNode{ID: 11, Split: &PaneSplit{Axis: SplitRows, Ratio: 50,
			A: &PaneNode{ID: 1, Kind: PaneTerminal},
			B: &PaneNode{ID: 13, Split: &PaneSplit{Axis: SplitCols, Ratio: 50,
				A: &PaneNode{ID: 2, Kind: PaneDoc, ContentID: 2},
				B: &PaneNode{ID: 3, Kind: PaneDoc, ContentID: 3},
			}},
		}},
		B: &PaneNode{ID: 12, Split: &PaneSplit{Axis: SplitRows, Ratio: 50,
			A: &PaneNode{ID: 4, Kind: PaneDoc, ContentID: 4},
			B: &PaneNode{ID: 5, Kind: PaneDoc, ContentID: 5},
		}},
	}}
	p.paneFocus = 2
	p.paneNextID = 14
	p.docs = make(map[int]*docPane)
	compositorDocLeaf(t, p, root, 2, "lower-left.md", "# lower left\n\nbody\n")
	compositorDocLeaf(t, p, root, 3, "lower-middle.md", "# lower middle\n\nbody\n")
	compositorDocLeaf(t, p, root, 4, "upper-right.md", "# upper right\n\nbody\n")
	compositorDocLeaf(t, p, root, 5, "lower-right.md", "# lower right\n\nbody\n")
}

// splitsInside is the split IDs a split encloses, itself excluded.
func splitsInside(node *PaneNode) []int {
	if node == nil || node.Split == nil {
		return nil
	}
	return append(append([]int{node.Split.A.ID}, splitsInside(node.Split.A)...),
		append([]int{node.Split.B.ID}, splitsInside(node.Split.B)...)...)
}

func findPaneSplit(node *PaneNode, id int) *PaneNode {
	if node == nil || node.Split == nil {
		return nil
	}
	if node.ID == id {
		return node
	}
	if found := findPaneSplit(node.Split.A, id); found != nil {
		return found
	}
	return findPaneSplit(node.Split.B, id)
}

// Where two dividers' three-cell targets overlap, the click belongs to the
// enclosed split — the one the pointer is nearer inside — and it gets there
// because the enclosing split was registered first and HitMap.Test scans in
// reverse. Registering the other way round would hand every contested cell to
// the outermost divider, which is the drag a user would never mean.
func TestNestedDividerTargetsResolveToTheEnclosedSplit(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	fiveLeafPaneTree(t, p, root)

	const width, height = 160, 30
	composePaneTree(t, p, width, height)
	origin, ok := p.previewContentBox()
	if !ok {
		t.Fatal("preview content box is unplaced")
	}
	_, dividers, fits := LayoutPanes(p.paneRoot, Box{W: width, H: height}, paneTreeFloors())
	if !fits || len(dividers) != 4 {
		t.Fatalf("layout = %d dividers fits=%v, want 4", len(dividers), fits)
	}

	contested := 0
	for outer := range dividers {
		enclosed := splitsInside(findPaneSplit(p.paneRoot, dividers[outer].SplitID))
		for inner := outer + 1; inner < len(dividers); inner++ {
			a, b := paneDividerHitBox(dividers[outer]), paneDividerHitBox(dividers[inner])
			overlap := Box{X: max(a.X, b.X), Y: max(a.Y, b.Y)}
			overlap.W = min(a.X+a.W, b.X+b.W) - overlap.X
			overlap.H = min(a.Y+a.H, b.Y+b.H) - overlap.Y
			if overlap.W <= 0 || overlap.H <= 0 {
				continue
			}
			contested++
			if !slices.Contains(enclosed, dividers[inner].SplitID) {
				t.Fatalf("splits %d and %d contest a cell but neither encloses the other",
					dividers[outer].SplitID, dividers[inner].SplitID)
			}
			hit := p.mouseHandler.HitMap.Test(origin.X+overlap.X, origin.Y+overlap.Y)
			if hit == nil || hit.ID != regionPaneTreeDivider || hit.Data != dividers[inner].SplitID {
				t.Fatalf("cell (%d,%d) contested by splits %d and %d resolves to %#v, want the enclosed %d",
					overlap.X, overlap.Y, dividers[outer].SplitID, dividers[inner].SplitID,
					hit, dividers[inner].SplitID)
			}
		}
	}
	// At least one nested overlap makes the ordering testable. Horizontal
	// targets intentionally no longer reach into the lower leaf's header, so
	// this asymmetric geometry has fewer contested cells than the old 3x3 rule.
	if contested < 1 {
		t.Fatalf("only %d divider targets overlap; the tree does not exercise the ordering", contested)
	}
}

// paneChipWidthFor is the width the header chips of one leaf were built at,
// which is the leaf's own box rather than the tree's.
func paneChipWidthFor(t *testing.T, p *Plugin, leafID, width, height int) int {
	t.Helper()
	leaves, _, _ := LayoutPanes(p.paneRoot, Box{W: width, H: height}, paneTreeFloors())
	for _, placement := range leaves {
		if placement.Node.ID == leafID {
			return placement.Box.W
		}
	}
	t.Fatalf("leaf %d is not placed", leafID)
	return 0
}
