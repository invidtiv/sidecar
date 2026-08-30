package workspace

import (
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/paneframe"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/panereposition"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/state"
)

func moveKey(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Text: string(r)} }

func TestProjectPaneMoveRunsThroughTheHostKeyLadder(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# move\n")
	p := docPaneTestPlugin(t, root, false)
	p.openTerminalPath("README.md", 1)
	doc := panelayout.FirstOfKind(p.paneRoot, panelayout.Document)
	primary := panelayout.FirstOfKind(p.paneRoot, panelayout.Terminal)
	if doc == nil || primary == nil || p.contentDeck == nil {
		t.Fatalf("test tree is incomplete: %+v", p.paneRoot)
	}
	p.paneRoot.Split.Ratio = 63
	p.paneFocus = doc.ID
	docBefore := doc
	peer, ok := p.previewPeerBox()
	if !ok {
		t.Fatal("project preview has no peer box")
	}
	_, _, fits := panelayout.LayoutPanes(p.paneRoot, peer, paneTreeFloors())
	if !fits {
		t.Fatal("non-50 test tree does not fit")
	}

	// The default-off flag leaves the real host ladder and context untouched.
	p.handleListKeys(moveKey('M'))
	if p.paneMove.Active(p.paneMoveScope(), p.paneRoot) || p.FocusContext() == panereposition.Context {
		t.Fatal("default-off pane_move entered mode")
	}

	enableWorkspaceFeature(t, features.PaneMove.Name)
	if !hasPaneMoveCommand(p.Commands(), panereposition.CommandMove) {
		t.Fatal("project browse commands do not advertise pane move")
	}
	p.handleListKeys(moveKey('M'))
	if got := p.FocusContext(); got != panereposition.Context {
		t.Fatalf("context after M = %q, want %q", got, panereposition.Context)
	}
	if !hasPaneMoveCommand(p.Commands(), "move-pane-left") {
		t.Fatal("project mode commands do not advertise directional movement")
	}
	if got := (paneHost{p}).Chrome(doc); got != paneframe.ChromeMoving {
		t.Fatalf("moving leaf chrome = %v", got)
	}
	// Unknown keys belong to the mode and cannot leak into the document.
	p.handleListKeys(moveKey('x'))
	if p.FocusContext() != panereposition.Context {
		t.Fatal("unknown mode key escaped to the pane below")
	}

	p.handleListKeys(moveKey('h'))
	if panelayout.Find(p.paneRoot, doc.ID) != docBefore {
		t.Fatalf("host move reconstructed the moved leaf: %p -> %p", docBefore, panelayout.Find(p.paneRoot, doc.ID))
	}
	if p.paneFocus != doc.ID || p.FocusContext() != panereposition.Context {
		t.Fatalf("move lost focus/mode: focus=%d context=%q", p.paneFocus, p.FocusContext())
	}
	_, _, fits = panelayout.LayoutPanes(p.paneRoot, peer, paneTreeFloors())
	if !fits || p.paneRoot.Split == nil || p.paneRoot.Split.Ratio == 50 {
		t.Fatalf("host discarded the carried non-50 ratio: tree=%+v", p.paneRoot)
	}
	if got, want := moveGridIDs(p.contentDeck.Tree()), moveGridIDs(p.paneRoot); !reflect.DeepEqual(got, want) {
		t.Fatalf("passive deck did not adopt moved order: %+v", p.contentDeck.Tree())
	}

	// Primary is movable through the same host path and retains its identity.
	p.handleListKeys(moveKey('M')) // exit
	primaryBefore := panelayout.Find(p.paneRoot, primary.ID)
	p.focusLeaf(primary.ID)
	p.handleListKeys(moveKey('M'))
	p.handleListKeys(moveKey('h'))
	if panelayout.Find(p.paneRoot, primary.ID) != primaryBefore || p.paneFocus != primary.ID {
		t.Fatal("moving Primary lost identity or focus")
	}
	p.handleListKeys(tea.KeyPressMsg{Code: tea.KeyEnter})
	if p.FocusContext() == panereposition.Context {
		t.Fatal("enter did not exit pane-move mode")
	}

	// An external in-place rewrite has a new structural generation even though
	// the root pointer and every leaf ID are unchanged.
	p.focusLeaf(doc.ID)
	p.handleListKeys(moveKey('M'))
	sameRoot := p.paneRoot
	p.paneRoot.Split.A, p.paneRoot.Split.B = p.paneRoot.Split.B, p.paneRoot.Split.A
	if p.paneRoot != sameRoot {
		t.Fatal("fixture replaced the root instead of rewriting it in place")
	}
	if p.FocusContext() == panereposition.Context {
		t.Fatal("same-pointer active-tree rewrite retained pane-move mode")
	}
}

func hasPaneMoveCommand(commands []plugin.Command, id string) bool {
	for _, command := range commands {
		if command.ID == id {
			return true
		}
	}
	return false
}

func moveGridIDs(root *panelayout.Node) [][]int {
	grid := panelayout.GridOf(root)
	if grid == nil {
		return nil
	}
	out := make([][]int, grid.ColumnCount())
	for col := 1; col <= grid.ColumnCount(); col++ {
		out[col-1] = make([]int, grid.RowCount(col))
		for row := 1; row <= grid.RowCount(col); row++ {
			out[col-1][row-1] = grid.Cell(col, row).ID
		}
	}
	return out
}

func TestProjectPaneMoveBoundaryUsesToast(t *testing.T) {
	p := docPaneTestPlugin(t, t.TempDir(), false)
	enableWorkspaceFeature(t, features.PaneMove.Name)
	p.handleListKeys(moveKey('M'))
	p.handleListKeys(moveKey('k'))
	if p.toastMessage != "already at the top" {
		t.Fatalf("boundary toast = %q", p.toastMessage)
	}
}

func TestProjectMovedShellSurvivesPassiveDeckReprojection(t *testing.T) {
	p := shellLeafTestPlugin(t, SplitRows)
	writeDocPaneFixture(t, p.ctx.WorkDir, "README.md", "# shell graft\n")
	p.openTerminalPath("README.md", 1)
	shell := p.shellLeaf()
	primary := panelayout.FirstOfKind(p.paneRoot, panelayout.Terminal)
	if shell == nil || primary == nil || p.contentDeck == nil {
		t.Fatalf("fixture lacks composed panes: %+v", p.paneRoot)
	}
	state := p.terminalPanes.Leaf(shell.ID)
	if state == nil || !state.Requested {
		t.Fatal("fixture lacks requested shell terminal state")
	}
	liveBefore := panelayout.LiveLeafCount(p.paneRoot)

	enableWorkspaceFeature(t, features.PaneMove.Name)
	p.focusLeaf(shell.ID)
	p.handleListKeys(moveKey('M'))
	p.handleListKeys(moveKey('l')) // move Shell from under Primary to the doc column
	shapeBefore, ok := shellGraftShape(p.paneRoot, shell.ID)
	if !ok || shapeBefore.anchorID == primary.ID {
		t.Fatalf("Shell did not move away from Primary: %+v tree=%+v", shapeBefore, p.paneRoot)
	}

	root, surface, ok := p.selectedTerminalSurface()
	if !ok {
		t.Fatal("fixture has no selected terminal surface")
	}
	p.syncWorkspaceDeckProjection(root, surface)
	shapeAfter, ok := shellGraftShape(p.paneRoot, shell.ID)
	if !ok || shapeAfter != shapeBefore {
		t.Fatalf("deck projection changed Shell graft: before=%+v after=%+v", shapeBefore, shapeAfter)
	}
	if p.shellLeaf() != shell || p.shellLeaf().ID != shell.ID || p.terminalPanes.Leaf(shell.ID) != state {
		t.Fatal("deck projection replaced Shell leaf identity or terminal state")
	}
	if p.paneFocus != shell.ID || !p.shellLeafFocused() {
		t.Fatalf("deck projection lost Shell focus: focus=%d", p.paneFocus)
	}
	if got := panelayout.LiveLeafCount(p.paneRoot); got != liveBefore {
		t.Fatalf("live leaf count = %d, want %d", got, liveBefore)
	}
}

func TestProjectRestoredShellMoveUsesDeckOwnedPassiveIDs(t *testing.T) {
	p := docPaneTestPlugin(t, t.TempDir(), true)
	writeDocPaneFixture(t, p.ctx.WorkDir, "README.md", "# restored shell graft\n")
	root, surface, ok := p.selectedTerminalSurface()
	if !ok {
		t.Fatal("fixture has no selected terminal surface")
	}
	stagedShellState := p.requireShellTermPane()
	stagedShellState.Session = termPanelSessionPrefix + "restored-move"
	layout := &state.PaneLayoutJSON{
		Root: root, Surface: surface, Open: true,
		Split: &state.PaneSplitJSON{Axis: "cols", Ratio: 62,
			A: &state.PaneLayoutJSON{Split: &state.PaneSplitJSON{Axis: "rows", Ratio: 41,
				A: &state.PaneLayoutJSON{Kind: contentKindTerminal},
				B: &state.PaneLayoutJSON{Kind: contentKindShell, Session: stagedShellState.Session},
			}},
			B: &state.PaneLayoutJSON{Kind: contentKindDoc, Tabs: []state.PaneDocTabJSON{{Path: "README.md"}}},
		},
	}
	p.restorePaneLayout(layout)

	shell := p.shellLeaf()
	doc := panelayout.FirstOfKind(p.paneRoot, panelayout.Document)
	primary := panelayout.FirstOfKind(p.paneRoot, panelayout.Terminal)
	if shell == nil || doc == nil || primary == nil || p.contentDeck == nil {
		t.Fatalf("restored tree is incomplete: %+v", p.paneRoot)
	}
	if p.contentDeck.Leaf(panelayout.Primary) != primary.ID || p.contentDeck.Leaf(panelayout.Document) != doc.ID {
		t.Fatalf("passive IDs diverged: host primary/doc=%d/%d deck=%d/%d", primary.ID, doc.ID,
			p.contentDeck.Leaf(panelayout.Primary), p.contentDeck.Leaf(panelayout.Document))
	}
	deckIDs := make(map[int]bool)
	var collectIDs func(*panelayout.Node)
	collectIDs = func(node *panelayout.Node) {
		if node == nil {
			return
		}
		deckIDs[node.ID] = true
		if node.Split != nil {
			collectIDs(node.Split.A)
			collectIDs(node.Split.B)
		}
	}
	collectIDs(p.contentDeck.Tree())
	if deckIDs[shell.ID] {
		t.Fatalf("restored Shell ID %d aliases Deck ownership", shell.ID)
	}
	var assertHostSplitsDistinct func(*panelayout.Node)
	assertHostSplitsDistinct = func(node *panelayout.Node) {
		if node == nil || node.Split == nil {
			return
		}
		if deckIDs[node.ID] {
			t.Fatalf("host split ID %d aliases Deck ownership", node.ID)
		}
		assertHostSplitsDistinct(node.Split.A)
		assertHostSplitsDistinct(node.Split.B)
	}
	assertHostSplitsDistinct(p.paneRoot)
	if got := p.terminalPanes.Leaf(shell.ID); got != stagedShellState {
		t.Fatal("restore replaced the staged Shell terminal state")
	}

	enableWorkspaceFeature(t, features.PaneMove.Name)
	p.focusLeaf(shell.ID)
	p.handleListKeys(moveKey('M'))
	p.handleListKeys(moveKey('l'))
	shapeBefore, moved := shellGraftShape(p.paneRoot, shell.ID)
	if !moved || shapeBefore.anchorID == primary.ID {
		t.Fatalf("restored Shell move did not leave Primary: %+v", shapeBefore)
	}
	liveBefore := panelayout.LiveLeafCount(p.paneRoot)
	p.syncWorkspaceDeckProjection(root, surface)
	shapeAfter, restored := shellGraftShape(p.paneRoot, shell.ID)
	if !restored || shapeAfter != shapeBefore {
		t.Fatalf("restored-tree projection changed Shell graft: before=%+v after=%+v", shapeBefore, shapeAfter)
	}
	if p.shellLeaf() != shell || p.terminalPanes.Leaf(shell.ID) != stagedShellState || p.paneFocus != shell.ID || !p.shellLeafFocused() {
		t.Fatal("restored-tree projection lost Shell leaf/state/focus identity")
	}
	if got := panelayout.LiveLeafCount(p.paneRoot); got != liveBefore {
		t.Fatalf("restored-tree live count = %d, want %d", got, liveBefore)
	}
}

type testShellGraftShape struct {
	anchorID   int
	axis       panelayout.Axis
	ratio      int
	shellFirst bool
}

func shellGraftShape(root *panelayout.Node, shellID int) (testShellGraftShape, bool) {
	parent := panelayout.Find(root, parentSplitID(root, shellID))
	if parent == nil || parent.Split == nil {
		return testShellGraftShape{}, false
	}
	shape := testShellGraftShape{axis: parent.Split.Axis, ratio: parent.Split.Ratio}
	if parent.Split.A.ID == shellID {
		shape.shellFirst, shape.anchorID = true, parent.Split.B.ID
		return shape, true
	}
	if parent.Split.B.ID == shellID {
		shape.anchorID = parent.Split.A.ID
		return shape, true
	}
	return testShellGraftShape{}, false
}
