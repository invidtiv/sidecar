package workspace

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/features"
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
	enableWorkspaceFeature(t, features.PaneMove.Name)
	p.openTerminalPath("README.md", 1)
	doc, docLeaf := p.activeDocPane()
	if doc == nil {
		t.Fatal("document pane was not opened")
	}
	p.width = 40
	peer, ok := p.previewPeerBox()
	if !ok {
		t.Fatal("narrow preview has no peer box")
	}
	if _, _, fits := LayoutPanes(p.paneRoot, peer, paneTreeFloors()); fits {
		t.Fatalf("preview peer box %+v still fits the tree", peer)
	}

	p.paneFocus = docLeaf.ID
	if box, ok := p.terminalLeafBox(); ok {
		t.Fatalf("doc-focused zoom gave the terminal box %+v, want none", box)
	}
	rendered, ok := p.renderDocumentSplit(peer.W, peer.H)
	if !ok || !strings.Contains(rendered, doc.view().Title()) {
		t.Fatalf("doc-focused zoom rendered ok=%v, want the document full-box", ok)
	}

	p.paneFocus = terminalLeafID(p.paneRoot)
	p.paneZoom.Set(p.paneLayoutModalScope(), p.paneRoot, p.paneFocus)
	box, ok := p.terminalLeafBox()
	inner := insetPanelChrome(peer)
	if !ok || box != inner {
		t.Fatalf("terminal-focused zoom box = %+v ok=%v, want the peer inner %+v", box, ok, inner)
	}
	rendered, ok = p.renderDocumentSplit(peer.W, peer.H)
	if !ok || !strings.Contains(rendered, "⊞") {
		t.Fatalf("terminal-focused zoom rendered ok=%v without its layout control", ok)
	}
	var layoutX, layoutY int
	found := false
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if region.ID == regionPaneLayout && region.Data == p.paneFocus {
			layoutX, layoutY, found = region.Rect.X, region.Rect.Y, true
			break
		}
	}
	if !found {
		t.Fatal("zoomed terminal drew a layout control without a hit region")
	}
	p.handleMouse(tea.MouseClickMsg(tea.Mouse{X: layoutX, Y: layoutY, Button: tea.MouseLeft}))
	if p.paneLayoutModal == nil || p.paneLayoutModal.LeafID() != p.paneFocus {
		t.Fatal("zoomed terminal layout control did not open its modal")
	}
}
