package workspace

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/workspacediff"
)

// td-43db92 was a bug on the GLOBAL browser, not here: this surface already
// focused the terminal leaf through regionPreviewPane and already left
// interactive mode on a press away from it, by routes of its own. The fix moved
// that answer into internal/paneframe, where focus comes from the leaf's BOX
// rather than from the region a press landed on.
//
// So most of these are parity guards, not bug pins: they hold this surface to
// the property the global browser now has (internal/overview/pane_click_focus_test.go)
// and would have caught the shared path regressing it. Which ones fail without
// which hunk is called out on each. The two that DO pin new behaviour here are
// TestFocusTargetLeavingTheTerminalEndsInteractiveMode, which fails without the
// setFocusTarget hook, and TestKanbanClicksDoNotMovePaneFocus, which fails
// without the drawn-frame Layout.

// everyKindPaneTree places one leaf of every kind beside the terminal, so a
// kind that stops being click-to-focusable fails here rather than in a bug
// report. A kind added later has to be added here as well.
func everyKindPaneTree(t *testing.T, p *Plugin, root string) {
	t.Helper()
	p.paneRoot = &PaneNode{ID: 10, Split: &PaneSplit{Axis: SplitCols, Ratio: 50,
		A: &PaneNode{ID: 1, Kind: PaneTerminal},
		B: &PaneNode{ID: 11, Split: &PaneSplit{Axis: SplitRows, Ratio: 34,
			A: &PaneNode{ID: 2, Kind: PaneDoc, ContentID: 2},
			B: &PaneNode{ID: 12, Split: &PaneSplit{Axis: SplitRows, Ratio: 50,
				A: &PaneNode{ID: 3, Kind: PaneIssue, ContentID: 3},
				B: &PaneNode{ID: 4, Kind: PaneDiff, ContentID: 4},
			}},
		}},
	}}
	p.paneFocus = 3
	p.paneNextID = 13
	p.docs = make(map[int]*docPane)
	p.issues = make(map[int]*issuePane)
	compositorDocLeaf(t, p, root, 2, "clicked.md", "# clicked\n\nfile body\n")
	compositorIssueLeaf(t, p, 3, "td-1a2b3c")
	p.attachDiffPane(4, root, "diff-surface", workspacediff.WorkingTreeTarget())
	if p.diffs[4] == nil || p.diffs[4].view() == nil {
		t.Fatal("the diff leaf has no content")
	}
}

// paneClickPoints is the middle of every placed leaf's box, keyed by leaf ID.
func paneClickPoints(t *testing.T, p *Plugin) map[int][2]int {
	t.Helper()
	layout, ok := paneHost{p}.Layout()
	if !ok {
		t.Fatal("the pane tree did not place")
	}
	points := make(map[int][2]int, len(layout.Leaves))
	for _, placement := range layout.Leaves {
		box := placement.Box
		points[placement.Node.ID] = [2]int{box.X + box.W/2, box.Y + box.H/2}
	}
	return points
}

// A press inside a leaf focuses that leaf, whatever kind it is — including the
// terminal, which owns no click-to-focus region because its presses are the
// live pane's and are forwarded to tmux. Parity guard: this passes with the
// FocusLeafAt hunk removed, because regionPreviewPane covers the terminal leaf
// on this surface and its arm calls focusLeaf. It is here so the shared path
// cannot regress the answer.
func TestClickInsideAnyLeafFocusesThatLeaf(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	everyKindPaneTree(t, p, root)
	p.sidebarVisible = true
	t.Cleanup(p.stopTerminalModels)

	p.View(p.width, p.height)
	points := paneClickPoints(t, p)
	if len(points) != 4 {
		t.Fatalf("placed %d leaves, want one of every kind", len(points))
	}

	for _, leafID := range []int{1, 2, 3, 4, 1} {
		leaf := FindPane(p.paneRoot, leafID)
		point := points[leafID]
		p.handleMouse(tea.MouseClickMsg{X: point[0], Y: point[1], Button: tea.MouseLeft})
		if p.paneFocus != leafID || p.activePane != PanePreview {
			t.Fatalf("click at %v inside the %v leaf %d left focus on leaf %d (pane %v)",
				point, leaf.Kind, leafID, p.paneFocus, p.activePane)
		}
		p.View(p.width, p.height)
	}
}

// A click out of a live terminal takes the keyboard with it: the ring moving
// while interactive mode kept forwarding keys is what put a shortcut into the
// agent's CLI on the global browser. Parity guard again — notePressAwayFromTerminal
// already answered this here, so it passes without either hunk.
func TestClickOutOfALiveTerminalTakesTheKeyboardWithIt(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	everyKindPaneTree(t, p, root)
	p.sidebarVisible = true
	t.Cleanup(p.stopTerminalModels)

	p.View(p.width, p.height)
	points := paneClickPoints(t, p)

	for _, leafID := range []int{2, 3, 4} {
		p.focusLeaf(terminalLeafID(p.paneRoot))
		p.viewMode = ViewModeInteractive
		p.interactiveState = &InteractiveState{Active: true, TargetPane: "%1", TargetSession: "focus-test"}

		point := points[leafID]
		p.handleMouse(tea.MouseClickMsg{X: point[0], Y: point[1], Button: tea.MouseLeft})

		if p.paneFocus != leafID {
			t.Fatalf("click at %v left focus on leaf %d, want %d", point, p.paneFocus, leafID)
		}
		if p.viewMode == ViewModeInteractive {
			t.Fatalf("click on leaf %d drew the ring there but left the keys in the terminal", leafID)
		}
		p.View(p.width, p.height)
	}
}

// setFocusTarget is the single writer, so the rule holds for the keyboard too:
// Tab or any other caller landing on a window that is not a terminal ends the
// live pane's hold rather than leaving a ring that lies.
func TestFocusTargetLeavingTheTerminalEndsInteractiveMode(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	everyKindPaneTree(t, p, root)
	t.Cleanup(p.stopTerminalModels)

	cases := []struct {
		name  string
		want  bool // interactive mode survives
		focus panelayout.Target
	}{
		{"terminal leaf", true, panelayout.Target{Kind: panelayout.TargetLeaf, Leaf: 1}},
		{"doc leaf", false, panelayout.Target{Kind: panelayout.TargetLeaf, Leaf: 2}},
		{"issue leaf", false, panelayout.Target{Kind: panelayout.TargetLeaf, Leaf: 3}},
		{"diff leaf", false, panelayout.Target{Kind: panelayout.TargetLeaf, Leaf: 4}},
		{"terminal panel", true, panelayout.Target{Kind: panelayout.TargetTermPanel}},
		{"sidebar", false, panelayout.Target{Kind: panelayout.TargetSidebar}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p.focusLeaf(terminalLeafID(p.paneRoot))
			p.viewMode = ViewModeInteractive
			p.interactiveState = &InteractiveState{Active: true, TargetPane: "%1", TargetSession: "focus-test"}

			p.setFocusTarget(tc.focus)

			if got := p.viewMode == ViewModeInteractive; got != tc.want {
				t.Fatalf("focus on the %s left interactive=%v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// A view that replaces the preview draws no pane tree, and pointer geometry has
// to go with it. The kanban board is the case that bit us: its cards sit over
// the boxes leaves occupied in the list view, so a Host that answered geometry
// it could place — rather than geometry it drew — let a click on a card move
// pane focus the user never asked to move, taking the keyboard off the sidebar
// and, in interactive mode, ending the live pane.
func TestKanbanClicksDoNotMovePaneFocus(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	everyKindPaneTree(t, p, root)
	p.sidebarVisible = true
	t.Cleanup(p.stopTerminalModels)

	p.View(p.width, p.height)
	points := paneClickPoints(t, p)
	if _, drawn := (paneHost{p}).Layout(); !drawn {
		t.Fatal("premise: the list view draws the pane tree")
	}

	p.viewMode = ViewModeKanban
	p.setFocusTarget(panelayout.Target{Kind: panelayout.TargetSidebar})
	p.View(p.width, p.height)
	if _, drawn := (paneHost{p}).Layout(); drawn {
		t.Fatal("the kanban board answered pane geometry it did not draw")
	}

	wantFocus := p.paneFocus
	landed := 0
	for _, kind := range []panelayout.Kind{PaneDoc, PaneIssue, PaneDiff, PaneTerminal} {
		leaf := panelayout.FirstOfKind(p.paneRoot, kind)
		point := points[leaf.ID]
		if p.mouseHandler.HitMap.Test(point[0], point[1]) != nil {
			landed++
		}
		p.handleMouse(tea.MouseClickMsg{X: point[0], Y: point[1], Button: tea.MouseLeft})
		if p.activePane != PaneSidebar {
			t.Fatalf("a kanban click at %v (the %v leaf's old box) took the keyboard off the sidebar",
				point, kindName(kind))
		}
		if p.paneFocus != wantFocus {
			t.Fatalf("a kanban click at %v moved pane focus from leaf %d to leaf %d",
				point, wantFocus, p.paneFocus)
		}
	}
	if landed == 0 {
		t.Fatal("premise: at least one click landed on a region the board drew")
	}
}

// kindName names a leaf kind for a message, since PaneKind is an int.
func kindName(kind PaneKind) string {
	switch kind {
	case PaneTerminal:
		return "terminal"
	case PaneDoc:
		return "document"
	case PaneIssue:
		return "issue"
	case PaneDiff:
		return "diff"
	default:
		return "unknown"
	}
}
