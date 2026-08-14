package workspace

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/tty"
)

// The placement rule is a pure function over the tree, so the three answers it
// can give are three assertions and not three journeys.
func TestPlanIssueOpenPlacesAnIssueByTheDefaultHeuristic(t *testing.T) {
	terminal := func() *PaneNode { return &PaneNode{ID: 1, Kind: PaneTerminal} }
	tests := []struct {
		name string
		root *PaneNode
		want paneOpen
	}{
		{
			name: "no content leaf falls back to the split a file click gets",
			root: terminal(),
			want: paneOpen{Split: 1, Axis: SplitCols},
		},
		{
			name: "a document leaf is stacked, document above and issue below",
			root: &PaneNode{ID: 3, Split: &PaneSplit{Axis: SplitCols, Ratio: 50,
				A: terminal(),
				B: &PaneNode{ID: 2, Kind: PaneDoc, ContentID: 2},
			}},
			want: paneOpen{Split: 2, Axis: SplitRows},
		},
		{
			name: "an issue leaf is retargeted rather than split again",
			root: &PaneNode{ID: 5, Split: &PaneSplit{Axis: SplitCols, Ratio: 50,
				A: terminal(),
				B: &PaneNode{ID: 4, Split: &PaneSplit{Axis: SplitRows, Ratio: 50,
					A: &PaneNode{ID: 2, Kind: PaneDoc, ContentID: 2},
					B: &PaneNode{ID: 3, Kind: PaneIssue, ContentID: 3},
				}},
			}},
			want: paneOpen{Retarget: 3},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := planIssueOpen(tc.root)
			if !ok || got != tc.want {
				t.Fatalf("planIssueOpen = %#v ok=%v, want %#v", got, ok, tc.want)
			}
		})
	}

	if _, ok := planIssueOpen(nil); ok {
		t.Fatal("a tree with no leaf named a placement")
	}
}

// With nothing but the terminal on screen a td click takes the placement a file
// click would have taken. The fallback is a default, not a rule about issues:
// the user gets a pane beside their terminal either way.
func TestClickingATdIssueWithNoDocumentSplitsTheTerminal(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	p.shells[0].Agent.OutputBuf.Update("follow-up is td-1a2b3c\n")

	if cmd := clickTerminalLink(t, p, "td-1a2b3c"); cmd == nil {
		t.Fatal("clicking the td id opened nothing")
	}
	if p.paneRoot.Split == nil || p.paneRoot.Split.Axis != SplitCols ||
		p.paneRoot.Split.A.Kind != PaneTerminal || p.paneRoot.Split.B.Kind != PaneIssue {
		t.Fatalf("issue pane = %#v, want the column split a file click gets", p.paneRoot)
	}
	if issue, _ := p.activeIssuePane(); issue == nil || issue.view.IssueID() != "td-1a2b3c" {
		t.Fatalf("issue leaf = %#v, want td-1a2b3c", issue)
	}
}

// A box that cannot hold the stacked split leaves the layout exactly as it was.
// The terminal is not reflowed for a pane that would not have been drawn, and
// the refusal is said out loud rather than shown as a missing pane.
func TestAnIssuePaneThatWillNotFitLeavesTheLayoutAlone(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "clicked.md", "# clicked\n")
	p := docPaneTestPlugin(t, root, true)
	if cmd := p.openTerminalPath("clicked.md", 1); cmd == nil {
		t.Fatal("the file did not open")
	}
	before := p.paneRoot.Split.B

	// Six content rows: the document and the issue want three each with a
	// divider between them.
	p.height = 8
	surfaceRoot, surface, ok := p.selectedTerminalSurface()
	if !ok {
		t.Fatal("no selected terminal surface")
	}
	if cmd := p.openIssuePaneForSurface(surfaceRoot, surface, "td-1a2b3c"); cmd != nil {
		t.Fatalf("a stacked split that does not fit still opened: %#v", p.paneRoot)
	}
	if p.paneRoot.Split.B != before || before.Split != nil || len(p.issues) != 0 {
		t.Fatalf("the refused split changed the tree: %#v", p.paneRoot)
	}
	if !strings.Contains(p.toastMessage, "taller") {
		t.Fatalf("refusal toast = %q, want the dimension the split needed", p.toastMessage)
	}
}

// A td id is Sidecar's to open on the live surface too. The click leaves
// interactive routing rather than putting a focused pane beside a terminal that
// still owns the keyboard, exactly as a clicked document does.
func TestClickingATdIssueInALiveTerminalTakesTheClickFromTheApplication(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	p := newSelectionTestPlugin()
	p.ctx = &plugin.Context{WorkDir: root, Epoch: 7}
	p.width, p.height = 140, 30
	p.sidebarVisible = false
	p.shells = []*ShellSession{{TmuxName: "one", Agent: &Agent{
		TmuxSession: "session", TmuxPane: "%1", OutputBuf: tty.NewOutputBuffer(20),
	}}}
	p.shells[0].Agent.OutputBuf.Update("follow-up is td-1a2b3c\n")
	p.paneRoot = &PaneNode{ID: 1, Kind: PaneTerminal}
	p.paneFocus, p.paneNextID = 1, 2
	p.docs = make(map[int]*docPane)
	p.interactiveState.MouseReportingEnabled = true
	p.shellStartupHooks = shellStartupHooks{
		getWorkspaceState: func(string) state.WorkspaceState { return state.WorkspaceState{} },
		setWorkspaceState: func(string, state.WorkspaceState) error { return nil },
	}

	if cmd := p.handleMouseClick(actionAt(ansi.StringWidth("follow-up is td")+1, 4)); cmd == nil {
		t.Fatal("clicking a td id in a live terminal did not activate")
	}
	issue, leaf := p.activeIssuePane()
	if issue == nil || issue.view.IssueID() != "td-1a2b3c" {
		t.Fatalf("issue leaf = %#v, want td-1a2b3c", issue)
	}
	if p.viewMode != ViewModeList || p.interactiveState != nil || p.paneFocus != leaf.ID {
		t.Fatalf("issue activation kept the live terminal: mode=%v interactive=%#v focus=%d",
			p.viewMode, p.interactiveState, p.paneFocus)
	}
	if !p.previewFreeze.Active() {
		t.Fatal("issue activation did not freeze the clicked viewport")
	}
}

// clickTerminalLink clicks the first on-screen cell whose link is want, through
// the regions the rendered view registered and the hit test the mouse handler
// uses. Finding the cell rather than computing it is what makes this the user's
// click: a link the frame did not draw where the hit test looks for it fails
// here rather than passing against arithmetic the renderer does not share.
func clickTerminalLink(t *testing.T, p *Plugin, want string) tea.Cmd {
	t.Helper()
	p.renderListView(p.width, p.height)
	for y := 0; y < p.height; y++ {
		for x := 0; x < p.width; x++ {
			region := p.mouseHandler.HitMap.Test(x, y)
			if region == nil || region.ID != regionPreviewPane {
				continue
			}
			action := mouse.MouseAction{Type: mouse.ActionClick, X: x, Y: y, Region: region}
			if link, _, _, ok := p.terminalLinkAt(action); !ok || link.Value != want {
				continue
			}
			return p.handleMouseClick(action)
		}
	}
	t.Fatalf("no drawn cell links to %q", want)
	return nil
}

// TestClickingAFileThenATdIssueBuildsTheSteelThread walks the journey this work
// exists for, click by click: a terminal filling the preview, a clicked file
// beside it, then a clicked td id below the file — terminal in the left column
// at full height, document above issue in the right one. A second td click
// retargets the issue leaf instead of growing the tree.
func TestClickingAFileThenATdIssueBuildsTheSteelThread(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	writeDocPaneFixture(t, root, "clicked.md", "# clicked\n\nfile body\n")
	p := docPaneTestPlugin(t, root, true)
	p.shells[0].Agent.OutputBuf.Update(strings.Join([]string{
		"wrote clicked.md:1",
		"follow-up is td-1a2b3c",
		"superseded by td-9f8e7d",
	}, "\n") + "\n")

	if cmd := clickTerminalLink(t, p, "clicked.md"); cmd == nil {
		t.Fatal("clicking the file opened nothing")
	}
	if p.paneRoot.Split == nil || p.paneRoot.Split.Axis != SplitCols ||
		p.paneRoot.Split.A.Kind != PaneTerminal || p.paneRoot.Split.B.Kind != PaneDoc {
		t.Fatalf("file click did not split the terminal into columns: %#v", p.paneRoot)
	}

	issueCmd := clickTerminalLink(t, p, "td-1a2b3c")
	if issueCmd == nil {
		t.Fatal("clicking the td id opened nothing")
	}
	stack := p.paneRoot.Split.B
	if p.paneRoot.Split.Axis != SplitCols || p.paneRoot.Split.A.Kind != PaneTerminal {
		t.Fatalf("the issue click moved the terminal out of its own column: %#v", p.paneRoot)
	}
	if stack.Split == nil || stack.Split.Axis != SplitRows ||
		stack.Split.A.Kind != PaneDoc || stack.Split.B.Kind != PaneIssue {
		t.Fatalf("the issue was not stacked below the document: %#v", stack)
	}
	issue, leaf := p.activeIssuePane()
	if issue == nil || leaf == nil || issue.view.IssueID() != "td-1a2b3c" || !issue.view.Loading() {
		t.Fatalf("issue leaf = %#v, want td-1a2b3c fetching", issue)
	}
	if p.paneFocus != leaf.ID || p.activePane != PanePreview {
		t.Fatalf("focus = pane %d/%v, want the new issue leaf", p.paneFocus, p.activePane)
	}
	// One load and one terminal resize: the terminal's box moved, so the tmux
	// pane behind it is told once, the way opening a document tells it.
	batch, ok := issueCmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("issue open command = %T, want the fetch plus one terminal resize", issueCmd())
	}
	for _, child := range batch {
		if msg, ok := child().(interface{ GetEpoch() uint64 }); ok && msg.GetEpoch() != p.ctx.Epoch {
			t.Fatalf("issue fetch carried epoch %d, want %d", msg.GetEpoch(), p.ctx.Epoch)
		}
	}

	if cmd := clickTerminalLink(t, p, "td-9f8e7d"); cmd == nil {
		t.Fatal("the second td click opened nothing")
	}
	if p.paneRoot.Split.B != stack || stack.Split.B.Kind != PaneIssue || stack.Split.B.Split != nil {
		t.Fatalf("the second td click grew the tree instead of retargeting: %#v", p.paneRoot)
	}
	if len(p.issues) != 1 {
		t.Fatalf("issue panes = %d, want the first leaf retargeted", len(p.issues))
	}
	retargeted, retargetedLeaf := p.activeIssuePane()
	if retargeted.view.IssueID() != "td-9f8e7d" || retargetedLeaf.ID != leaf.ID {
		t.Fatalf("second td click = leaf %d showing %q, want leaf %d retargeted",
			retargetedLeaf.ID, retargeted.view.IssueID(), leaf.ID)
	}
	if doc, _ := p.activeDocPane(); doc == nil || doc.view.Title() != "clicked.md" {
		t.Fatalf("the retarget disturbed the document leaf: %#v", doc)
	}
}
