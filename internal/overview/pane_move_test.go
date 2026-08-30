package overview

import (
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	appmsg "github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/paneframe"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/panereposition"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func globalMoveKey(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Text: string(r)} }

func enableGlobalPaneMove(t *testing.T) {
	t.Helper()
	features.Init(config.Default())
	features.SetOverride(features.PaneMove.Name, true)
	t.Cleanup(func() { features.Init(config.Default()) })
}

func TestGlobalPaneMoveRunsThroughTheHostKeyLadder(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, openPreviewDocSpan(m, mustPreviewSpan(t, m, previewNeedleAction(t, m, "README.md"))))
	m.preview.focus = focusPreview
	doc := panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Document)
	primary := panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Terminal)
	if doc == nil || primary == nil || m.preview.deck == nil {
		t.Fatalf("test tree is incomplete: %+v", m.preview.paneRoot)
	}
	m.preview.paneRoot.Split.Ratio = 64
	m.preview.paneFocus = doc.ID
	docBefore := doc
	peer, ok := m.previewPeerBox()
	if !ok {
		t.Fatal("global preview has no peer box")
	}
	_, _, fits := panelayout.LayoutPanes(m.preview.paneRoot, panelayout.Box(peer), previewPaneFloors())
	if !fits {
		t.Fatal("non-50 test tree does not fit")
	}

	// The default-off flag leaves the real preview ladder and context alone.
	if handled, _ := m.previewKey(globalMoveKey('M')); handled {
		t.Fatal("default-off pane_move claimed M")
	}
	if m.WorkspaceFocusContext() == panereposition.Context {
		t.Fatal("default-off pane_move changed the context")
	}

	enableGlobalPaneMove(t)
	if !hasGlobalPaneMoveCommand(m.Commands(), panereposition.CommandMove) {
		t.Fatal("global browse commands do not advertise pane move")
	}
	if handled, _ := m.previewKey(globalMoveKey('M')); !handled {
		t.Fatal("enabled pane_move did not claim M")
	}
	if got := m.WorkspaceFocusContext(); got != panereposition.Context {
		t.Fatalf("context after M = %q, want %q", got, panereposition.Context)
	}
	if !hasGlobalPaneMoveCommand(m.Commands(), "move-pane-left") {
		t.Fatal("global mode commands do not advertise directional movement")
	}
	if got := (paneHost{m}).Chrome(doc); got != paneframe.ChromeMoving {
		t.Fatalf("moving leaf chrome = %v", got)
	}
	if handled, _ := m.previewKey(globalMoveKey('x')); !handled || m.WorkspaceFocusContext() != panereposition.Context {
		t.Fatal("unknown mode key escaped to the pane below")
	}

	if handled, _ := m.previewKey(globalMoveKey('h')); !handled {
		t.Fatal("move key was not handled")
	}
	if panelayout.Find(m.preview.paneRoot, doc.ID) != docBefore {
		t.Fatal("host move reconstructed the moved leaf")
	}
	if m.preview.paneFocus != doc.ID || m.WorkspaceFocusContext() != panereposition.Context {
		t.Fatalf("move lost focus/mode: focus=%d context=%q", m.preview.paneFocus, m.WorkspaceFocusContext())
	}
	_, _, fits = panelayout.LayoutPanes(m.preview.paneRoot, panelayout.Box(peer), previewPaneFloors())
	if !fits || m.preview.paneRoot.Split == nil || m.preview.paneRoot.Split.Ratio == 50 {
		t.Fatalf("host discarded the carried non-50 ratio: tree=%+v", m.preview.paneRoot)
	}
	if got, want := globalMoveGridIDs(m.preview.deck.Tree()), globalMoveGridIDs(m.preview.paneRoot); !reflect.DeepEqual(got, want) {
		t.Fatalf("passive deck did not adopt moved order: %+v", m.preview.deck.Tree())
	}

	// Primary follows the same path and keeps the actual live leaf object.
	m.previewKey(globalMoveKey('M')) // exit
	primaryBefore := panelayout.Find(m.preview.paneRoot, primary.ID)
	m.focusPreviewLeaf(primary.ID)
	m.previewKey(globalMoveKey('M'))
	m.previewKey(globalMoveKey('h'))
	if panelayout.Find(m.preview.paneRoot, primary.ID) != primaryBefore || m.preview.paneFocus != primary.ID {
		t.Fatal("moving Primary lost identity or focus")
	}
	m.previewKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.WorkspaceFocusContext() == panereposition.Context {
		t.Fatal("escape did not exit pane-move mode")
	}

	m.focusPreviewLeaf(doc.ID)
	m.previewKey(globalMoveKey('M'))
	sameRoot := m.preview.paneRoot
	m.preview.paneRoot.Split.A, m.preview.paneRoot.Split.B = m.preview.paneRoot.Split.B, m.preview.paneRoot.Split.A
	if m.preview.paneRoot != sameRoot {
		t.Fatal("fixture replaced the root instead of rewriting it in place")
	}
	if m.WorkspaceFocusContext() == panereposition.Context {
		t.Fatal("same-pointer active-tree rewrite retained pane-move mode")
	}
}

func hasGlobalPaneMoveCommand(commands []plugin.Command, id string) bool {
	for _, command := range commands {
		if command.ID == id {
			return true
		}
	}
	return false
}

func TestGlobalPaneMoveBoundaryUsesFlash(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	m.preview.focus = focusPreview
	enableGlobalPaneMove(t)
	m.previewKey(globalMoveKey('M'))
	handled, cmd := m.previewKey(globalMoveKey('k'))
	if !handled || cmd == nil {
		t.Fatal("global boundary did not produce a flash command")
	}
	flash, ok := cmd().(appmsg.FlashMsg)
	if !ok || flash.Text != "already at the top" {
		t.Fatalf("boundary result = %#v, want top-boundary flash", flash)
	}
}

func globalMoveGridIDs(root *panelayout.Node) [][]int {
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
