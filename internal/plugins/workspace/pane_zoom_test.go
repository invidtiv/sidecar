package workspace

import (
	"strings"
	"testing"
)

// zoomTestTree is terminal | doc, the smallest tree with two candidates for the
// zoomed leaf.
func zoomTestTree() (root, terminal, doc *PaneNode) {
	terminal = &PaneNode{ID: 1, Kind: PaneTerminal}
	doc = &PaneNode{ID: 2, Kind: PaneDoc, ContentID: 2}
	root = &PaneNode{ID: 10, Split: &PaneSplit{Axis: SplitCols, Ratio: 50, A: terminal, B: doc}}
	return root, terminal, doc
}

func TestLayoutPaneTreeTilesWhatFitsAndZoomsWhatDoesNot(t *testing.T) {
	root, terminal, doc := zoomTestTree()

	fitting := Box{X: 3, Y: 4, W: 120, H: 24}
	layout, ok := LayoutPaneTree(root, fitting, testPaneFloors, terminal.ID)
	if !ok || layout.Zoomed || len(layout.Leaves) != 2 || len(layout.Dividers) != 1 {
		t.Fatalf("fitting layout = %+v ok=%v, want the tiled tree", layout, ok)
	}
	leaves, dividers, _ := LayoutPanes(root, fitting, testPaneFloors)
	for i, placement := range layout.Leaves {
		if placement != leaves[i] {
			t.Fatalf("tiled leaf %d = %+v, want %+v", i, placement, leaves[i])
		}
	}
	if layout.Dividers[0] != dividers[0] {
		t.Fatalf("tiled divider = %+v, want %+v", layout.Dividers[0], dividers[0])
	}

	narrow := Box{X: 3, Y: 4, W: 34, H: 24}
	for _, focused := range []*PaneNode{terminal, doc} {
		layout, ok := LayoutPaneTree(root, narrow, testPaneFloors, focused.ID)
		if !ok || !layout.Zoomed {
			t.Fatalf("narrow layout for leaf %d = %+v ok=%v, want a zoom", focused.ID, layout, ok)
		}
		if len(layout.Leaves) != 1 || layout.Leaves[0].Node != focused || layout.Leaves[0].Box != narrow {
			t.Fatalf("zoomed layout = %+v, want leaf %d alone in %+v", layout.Leaves, focused.ID, narrow)
		}
		if len(layout.Dividers) != 0 {
			t.Fatalf("zoomed layout drew %d dividers, want none", len(layout.Dividers))
		}
	}
}

func TestLayoutPaneTreeRefusesWhenFocusIsNotALeaf(t *testing.T) {
	root, _, _ := zoomTestTree()
	narrow := Box{W: 34, H: 24}
	for _, focus := range []int{root.ID, 0, 99} {
		if layout, ok := LayoutPaneTree(root, narrow, testPaneFloors, focus); ok {
			t.Fatalf("focus %d produced %+v, want no layout", focus, layout)
		}
	}
	if layout, ok := LayoutPaneTree(nil, Box{W: 120, H: 24}, testPaneFloors, 1); ok {
		t.Fatalf("empty tree produced %+v, want no layout", layout)
	}
}

// TestZoomedLeafOwnsBothTheBoxAndThePixels pins the two consumers of the zoom
// rule to each other: whichever leaf the layout zooms is the one that has a
// terminal box, and the other one renders nothing.
func TestZoomedLeafOwnsBothTheBoxAndThePixels(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# zoom\n")
	p := docPaneTestPlugin(t, root, true)
	p.openTerminalPath("README.md", 1)
	doc, docLeaf := p.activeDocPane()
	if doc == nil {
		t.Fatal("document pane was not opened")
	}
	p.width = 40
	content, ok := p.previewContentBox()
	if !ok {
		t.Fatal("narrow preview has no content box")
	}
	if _, _, fits := LayoutPanes(p.paneRoot, content, paneTreeFloors()); fits {
		t.Fatalf("preview content box %+v still fits the tree", content)
	}

	p.paneFocus = docLeaf.ID
	if box, ok := p.terminalLeafBox(); ok {
		t.Fatalf("doc-focused zoom gave the terminal box %+v, want none", box)
	}
	rendered, ok := p.renderDocumentSplit(content.W, content.H)
	if !ok || !strings.Contains(rendered, doc.view.Title()) {
		t.Fatalf("doc-focused zoom rendered ok=%v, want the document full-box", ok)
	}

	p.paneFocus = terminalLeafID(p.paneRoot)
	box, ok := p.terminalLeafBox()
	if !ok || box != content {
		t.Fatalf("terminal-focused zoom box = %+v ok=%v, want the preview content box %+v", box, ok, content)
	}
	if _, ok := p.renderDocumentSplit(content.W, content.H); ok {
		t.Fatal("terminal-focused zoom rendered the document split instead of the legacy terminal")
	}
}
