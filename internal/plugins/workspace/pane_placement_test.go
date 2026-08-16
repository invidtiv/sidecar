package workspace

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/issueview"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/tty"
)

// The placement rule is a pure function over the tree, so its answers are
// assertions and not journeys. Every kind of click asks it, which is what keeps
// the two orders of the same two clicks from building two different layouts.
func TestPlanPaneOpenPlacesClickedContentByTheDefaultHeuristic(t *testing.T) {
	terminal := func() *PaneNode { return &PaneNode{ID: 1, Kind: PaneTerminal} }
	beside := func(leaf *PaneNode) *PaneNode {
		return &PaneNode{ID: 9, Split: &PaneSplit{Axis: SplitCols, Ratio: 50, A: terminal(), B: leaf}}
	}
	stacked := &PaneNode{ID: 5, Split: &PaneSplit{Axis: SplitCols, Ratio: 50,
		A: terminal(),
		B: &PaneNode{ID: 4, Split: &PaneSplit{Axis: SplitRows, Ratio: 50,
			A: &PaneNode{ID: 2, Kind: PaneDoc, ContentID: 2},
			B: &PaneNode{ID: 3, Kind: PaneIssue, ContentID: 3},
		}},
	}}
	tests := []struct {
		name string
		root *PaneNode
		kind PaneKind
		want paneOpen
	}{
		{
			name: "an issue with no content on screen falls back to the split a file click gets",
			root: terminal(),
			kind: PaneIssue,
			want: paneOpen{Split: 1, Axis: SplitCols},
		},
		{
			name: "the first file click splits the terminal into columns",
			root: terminal(),
			kind: PaneDoc,
			want: paneOpen{Split: 1, Axis: SplitCols},
		},
		{
			name: "a document leaf is stacked, document above and issue below",
			root: beside(&PaneNode{ID: 2, Kind: PaneDoc, ContentID: 2}),
			kind: PaneIssue,
			want: paneOpen{Split: 2, Axis: SplitRows},
		},
		{
			name: "an issue leaf is stacked too, so a file click after a td click opens no third column",
			root: beside(&PaneNode{ID: 2, Kind: PaneIssue, ContentID: 2}),
			kind: PaneDoc,
			want: paneOpen{Split: 2, Axis: SplitRows},
		},
		{
			name: "an issue leaf is retargeted rather than split again",
			root: stacked,
			kind: PaneIssue,
			want: paneOpen{Retarget: 3},
		},
		{
			name: "a document leaf is retargeted by the same rule",
			root: stacked,
			kind: PaneDoc,
			want: paneOpen{Retarget: 2},
		},
		{
			name: "a third kind stacks on the first content leaf when boxes are unknown",
			root: stacked,
			kind: PaneDiff,
			want: paneOpen{Split: 2, Axis: SplitRows},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := planPaneOpen(tc.root, tc.kind, nil)
			if !ok || got != tc.want {
				t.Fatalf("planPaneOpen = %#v ok=%v, want %#v", got, ok, tc.want)
			}
		})
	}

	if _, ok := planPaneOpen(nil, PaneIssue, nil); ok {
		t.Fatal("a tree with no leaf named a placement")
	}
	if _, ok := planPaneOpen(terminal(), PaneTerminal, nil); ok {
		t.Fatal("a terminal is not content a click opens")
	}
}

// The reverse of the steel thread's order: a td click first, then a file click.
// The second click stacks in the right column rather than splitting the
// terminal again, because both clicks read the same placement rule — three
// columns at a minimum width of 72 is a layout neither click asked for.
func TestClickingATdIssueThenAFileStacksTheRightColumn(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	writeDocPaneFixture(t, root, "clicked.md", "# clicked\n\nfile body\n")
	p := docPaneTestPlugin(t, root, true)
	p.shells[0].Agent.OutputBuf.Update("follow-up is td-1a2b3c\nwrote clicked.md:1\n")

	if cmd := clickTerminalLink(t, p, "td-1a2b3c"); cmd == nil {
		t.Fatal("clicking the td id opened nothing")
	}
	fileCmd := clickTerminalLink(t, p, "clicked.md")
	if fileCmd == nil {
		t.Fatal("clicking the file opened nothing")
	}
	deliverLoads(t, p, fileCmd)

	stack := p.paneRoot.Split.B
	if p.paneRoot.Split.Axis != SplitCols || p.paneRoot.Split.A.Kind != PaneTerminal {
		t.Fatalf("the file click moved the terminal out of its own column: %#v", p.paneRoot)
	}
	if stack.Split == nil || stack.Split.Axis != SplitRows ||
		stack.Split.A.Kind != PaneIssue || stack.Split.B.Kind != PaneDoc {
		t.Fatalf("the file was not stacked below the issue: %#v", stack)
	}
	boxes, content := paneLeafBoxes(t, p)
	if len(boxes) != 3 {
		t.Fatalf("the two clicks left %d leaves, want terminal, issue and document", len(boxes))
	}
	if boxes[PaneTerminal].H != content.H || boxes[PaneTerminal].Y != content.Y {
		t.Fatalf("terminal box %#v, want the left column at the full height of %#v", boxes[PaneTerminal], content)
	}
	if boxes[PaneDoc].X != boxes[PaneIssue].X || boxes[PaneDoc].W != boxes[PaneIssue].W {
		t.Fatalf("document box %#v is not in the issue's column %#v", boxes[PaneDoc], boxes[PaneIssue])
	}

	// The lower document's tab header begins one row below this divider. The
	// filename-click fallback must not consume the divider press.
	p.renderListView(p.width, p.height)
	layout, ok := LayoutPaneTree(p.paneRoot, content, paneTreeFloors(), p.paneFocus)
	if !ok {
		t.Fatal("stacked pane layout disappeared before divider drag")
	}
	var rowDivider Divider
	for _, divider := range layout.Dividers {
		if divider.SplitID == stack.ID {
			rowDivider = divider
			break
		}
	}
	x, y := rowDivider.Box.X+rowDivider.Box.W/2, rowDivider.Box.Y
	hit := p.mouseHandler.HitMap.Test(x, y)
	if hit == nil || hit.ID != regionPaneTreeDivider || hit.Data != stack.ID {
		t.Fatalf("stack divider at (%d,%d) resolves to %#v", x, y, hit)
	}
	before := stack.Split.Ratio
	p.handleMouseClick(mouse.MouseAction{Type: mouse.ActionClick, X: x, Y: y, Region: hit})
	p.handleMouseDrag(mouse.MouseAction{Type: mouse.ActionDrag, DragStartID: regionPaneTreeDivider, DragDY: 3})
	if stack.Split.Ratio == before {
		t.Fatalf("stack divider stayed at %d after drag", before)
	}
	if doc, _ := p.activeDocPane(); doc == nil || doc.view().Title() != "clicked.md" {
		t.Fatalf("divider press selected or replaced the file tab: %#v", doc)
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
	if issue, _ := p.activeIssuePane(); issue == nil || issue.view().IssueID() != "td-1a2b3c" {
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
	if issue == nil || issue.view().IssueID() != "td-1a2b3c" {
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

// deliverLoads runs cmd and hands any content load result back to the plugin,
// the way the runtime would. A pane whose fetch is never delivered draws its
// loading state, which is not the screen the journey is about.
func deliverLoads(t *testing.T, p *Plugin, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, child := range msg {
			deliverLoads(t, p, child)
		}
	case docview.LoadedMsg, issueview.LoadedMsg:
		p.update(msg)
	}
}

// paneLeafBoxes reads where the layout actually put each leaf, through the same
// authority the terminal sizers read. A tree of the right shape is not the claim
// the journey makes: the claim is a terminal column at full height beside a
// stacked document and issue, and only the boxes say that.
func paneLeafBoxes(t *testing.T, p *Plugin) (map[PaneKind]Box, Box) {
	t.Helper()
	peer, ok := p.previewPeerBox()
	if !ok {
		t.Fatal("preview peer box is unplaced")
	}
	layout, ok := LayoutPaneTree(p.paneRoot, peer, paneTreeFloors(), p.paneFocus)
	if !ok || layout.Zoomed {
		t.Fatalf("layout ok=%v zoomed=%v, want every leaf in a box of its own", ok, layout.Zoomed)
	}
	boxes := make(map[PaneKind]Box, len(layout.Leaves))
	for _, placement := range layout.Leaves {
		if _, seen := boxes[placement.Node.Kind]; seen {
			t.Fatalf("two leaves of kind %d; this journey has one of each", placement.Node.Kind)
		}
		boxes[placement.Node.Kind] = placement.Box
	}
	return boxes, peer
}

// TestClickingAFileThenATdIssueBuildsTheSteelThread walks the journey this work
// exists for, click by click: a terminal filling the preview, a clicked file
// beside it, then a clicked td id below the file — terminal in the left column
// at full height, document above issue in the right one. Every stage is measured
// as boxes and then as composed cells, because a tree of the right shape and a
// screen of the right shape are two claims. A second td click appends a tab
// on the issue leaf instead of growing the tree.
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

	// Where the journey starts: one terminal leaf holding the whole preview peer.
	boxes, content := paneLeafBoxes(t, p)
	if len(boxes) != 1 || boxes[PaneTerminal] != content {
		t.Fatalf("before the first click the terminal holds %#v, want the whole peer box %#v",
			boxes[PaneTerminal], content)
	}

	fileCmd := clickTerminalLink(t, p, "clicked.md")
	if fileCmd == nil {
		t.Fatal("clicking the file opened nothing")
	}
	deliverLoads(t, p, fileCmd)
	if p.paneRoot.Split == nil || p.paneRoot.Split.Axis != SplitCols ||
		p.paneRoot.Split.A.Kind != PaneTerminal || p.paneRoot.Split.B.Kind != PaneDoc {
		t.Fatalf("file click did not split the terminal into columns: %#v", p.paneRoot)
	}
	boxes, content = paneLeafBoxes(t, p)
	terminalBox, docBox := boxes[PaneTerminal], boxes[PaneDoc]
	if len(boxes) != 2 {
		t.Fatalf("file click left %d leaves, want the terminal and the document", len(boxes))
	}
	if terminalBox.X != content.X || terminalBox.Y != content.Y || terminalBox.H != content.H {
		t.Fatalf("terminal box %#v, want the left column at the full height of %#v", terminalBox, content)
	}
	if docBox.X != terminalBox.X+terminalBox.W+1 || docBox.Y != content.Y || docBox.H != content.H ||
		docBox.X+docBox.W != content.X+content.W {
		t.Fatalf("document box %#v, want the right column of %#v across the divider", docBox, content)
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
	if issue == nil || leaf == nil || issue.view().IssueID() != "td-1a2b3c" || !issue.view().Loading() {
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
	loaded := false
	for _, child := range batch {
		msg := child()
		if epoched, ok := msg.(interface{ GetEpoch() uint64 }); ok && epoched.GetEpoch() != p.ctx.Epoch {
			t.Fatalf("issue fetch carried epoch %d, want %d", epoched.GetEpoch(), p.ctx.Epoch)
		}
		// The fetch is delivered through the plugin's own update, so what the
		// cells below show is what the runtime would have put there.
		if result, ok := msg.(issueview.LoadedMsg); ok {
			p.update(result)
			loaded = true
		}
	}
	if !loaded {
		t.Fatal("the issue click scheduled no fetch to deliver")
	}

	boxes, content = paneLeafBoxes(t, p)
	if len(boxes) != 3 {
		t.Fatalf("the issue click left %d leaves, want terminal, document and issue", len(boxes))
	}
	stackedDoc, issueBox := boxes[PaneDoc], boxes[PaneIssue]
	if boxes[PaneTerminal] != terminalBox {
		t.Fatalf("the issue click moved the terminal from %#v to %#v; the left column is its own full-height column",
			terminalBox, boxes[PaneTerminal])
	}
	if stackedDoc.X != docBox.X || stackedDoc.W != docBox.W || stackedDoc.Y != docBox.Y {
		t.Fatalf("the issue click moved the document out of the right column: %#v -> %#v", docBox, stackedDoc)
	}
	if issueBox.X != stackedDoc.X || issueBox.W != stackedDoc.W {
		t.Fatalf("issue box %#v, want the document's column %#v", issueBox, stackedDoc)
	}
	if issueBox.Y != stackedDoc.Y+stackedDoc.H+1 {
		t.Fatalf("issue box starts at row %d, want the row below the document's divider at %d",
			issueBox.Y, stackedDoc.Y+stackedDoc.H)
	}
	if issueBox.Y+issueBox.H != content.Y+content.H {
		t.Fatalf("issue box ends at row %d, want the bottom of the content box at %d",
			issueBox.Y+issueBox.H, content.Y+content.H)
	}
	if terminalBox.H <= stackedDoc.H {
		t.Fatalf("terminal height %d is not above the stacked document's %d; the left column was split too",
			terminalBox.H, stackedDoc.H)
	}

	// The boxes are the layout's answer; these are the cells. Each pane's own
	// identity has to be inside its own rectangle, or the composition disagrees
	// with the geometry the terminal is being sized from.
	rows := composePaneTree(t, p, content.W, content.H)
	within := func(box Box) string {
		lines := make([]string, 0, box.H)
		for row := 0; row < box.H; row++ {
			cells := []rune(ansi.Strip(rows[box.Y-content.Y+row]))
			lines = append(lines, string(cells[box.X-content.X:box.X-content.X+box.W]))
		}
		return strings.Join(lines, "\n")
	}
	documentCells, issueCells, terminalCells := within(stackedDoc), within(issueBox), within(terminalBox)
	if !strings.Contains(documentCells, "clicked.md") || !strings.Contains(documentCells, "file body") {
		t.Fatalf("document box does not hold the clicked file:\n%s", documentCells)
	}
	if !strings.Contains(issueCells, "td-1a2b3c") || !strings.Contains(issueCells, "Body of td-1a2b3c") {
		t.Fatalf("issue box does not hold the clicked issue:\n%s", issueCells)
	}
	if strings.Contains(terminalCells, "Body of td-1a2b3c") || strings.Contains(terminalCells, "file body") {
		t.Fatalf("the right column bled into the terminal's own column:\n%s", terminalCells)
	}

	if cmd := clickTerminalLink(t, p, "td-9f8e7d"); cmd == nil {
		t.Fatal("the second td click opened nothing")
	}
	if p.paneRoot.Split.B != stack || stack.Split.B.Kind != PaneIssue || stack.Split.B.Split != nil {
		t.Fatalf("the second td click grew the tree instead of appending a tab: %#v", p.paneRoot)
	}
	if len(p.issues) != 1 {
		t.Fatalf("issue panes = %d, want the first leaf kept", len(p.issues))
	}
	retargeted, retargetedLeaf := p.activeIssuePane()
	if retargeted.view().IssueID() != "td-9f8e7d" || retargetedLeaf.ID != leaf.ID {
		t.Fatalf("second td click = leaf %d showing %q, want leaf %d with the new tab",
			retargetedLeaf.ID, retargeted.view().IssueID(), leaf.ID)
	}
	if len(retargeted.tabs.Items) != 2 || retargeted.tabs.Find("td-1a2b3c") < 0 {
		t.Fatalf("second td click tabs = %d, want both issues in one group", len(retargeted.tabs.Items))
	}
	if doc, _ := p.activeDocPane(); doc == nil || doc.view().Title() != "clicked.md" {
		t.Fatalf("the append disturbed the document leaf: %#v", doc)
	}
}

// File, then a td issue, then Diff: the third kind stacks on the largest
// content leaf. The terminal stays a full-height left column — the live pane
// is not split again.
func TestClickingAFileThenATdIssueThenDiffKeepsTheTerminalFullHeight(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	writeDocPaneFixture(t, root, "clicked.md", "# clicked\n\nfile body\n")
	p := docPaneTestPlugin(t, root, true)
	p.shells[0].Agent.OutputBuf.Update("wrote clicked.md:1\nfollow-up is td-1a2b3c\n")

	fileCmd := clickTerminalLink(t, p, "clicked.md")
	if fileCmd == nil {
		t.Fatal("clicking the file opened nothing")
	}
	deliverLoads(t, p, fileCmd)
	if cmd := clickTerminalLink(t, p, "td-1a2b3c"); cmd == nil {
		t.Fatal("clicking the td id opened nothing")
	}
	if cmd := p.showDiffCmd(); cmd == nil {
		t.Fatal("opening Diff after File and Issue opened nothing")
	}

	if p.paneRoot.Split == nil || p.paneRoot.Split.Axis != SplitCols ||
		p.paneRoot.Split.A.Kind != PaneTerminal {
		t.Fatalf("Diff open moved the terminal out of its own column: %#v", p.paneRoot)
	}
	right := p.paneRoot.Split.B
	if right.Split == nil || right.Split.Axis != SplitRows || right.Split.B.Kind != PaneIssue {
		t.Fatalf("right column = %#v, want Issue still below a stacked pair", right)
	}
	pair := right.Split.A
	if pair.Split == nil || pair.Split.Axis != SplitRows ||
		pair.Split.A.Kind != PaneDoc || pair.Split.B.Kind != PaneDiff {
		t.Fatalf("Diff did not stack on the document: %#v", pair)
	}

	boxes, content := paneLeafBoxes(t, p)
	if len(boxes) != 4 {
		t.Fatalf("File+Issue+Diff left %d leaves, want four", len(boxes))
	}
	if boxes[PaneTerminal].H != content.H || boxes[PaneTerminal].Y != content.Y {
		t.Fatalf("terminal box %#v, want the left column at the full height of %#v",
			boxes[PaneTerminal], content)
	}
	if boxes[PaneDiff].X != boxes[PaneDoc].X || boxes[PaneDiff].W != boxes[PaneDoc].W {
		t.Fatalf("diff box %#v is not in the document's column %#v", boxes[PaneDiff], boxes[PaneDoc])
	}
}

// Areas, not kind order, name the leaf a third content kind splits. Equal
// areas (and nil boxes) keep today's DFS-A answer; a dragged-larger document
// still wins; a dragged-larger issue is the case today's walk would get wrong.
func TestPlanOpenSplitsTheLargestContentLeaf(t *testing.T) {
	p := docPaneTestPlugin(t, t.TempDir(), true)
	doc := &PaneNode{ID: 2, Kind: PaneDoc, ContentID: 2}
	issue := &PaneNode{ID: 3, Kind: PaneIssue, ContentID: 3}
	stack := &PaneNode{ID: 4, Split: &PaneSplit{Axis: SplitRows, Ratio: 50, A: doc, B: issue}}
	p.paneRoot = &PaneNode{ID: 5, Split: &PaneSplit{Axis: SplitCols, Ratio: 50,
		A: &PaneNode{ID: 1, Kind: PaneTerminal},
		B: stack,
	}}
	p.paneFocus, p.paneNextID = 2, 6

	equal := map[int]Box{2: {W: 40, H: 10}, 3: {W: 40, H: 10}}
	got, ok := planPaneOpen(p.paneRoot, PaneDiff, equal)
	if !ok || got != (paneOpen{Split: 2, Axis: SplitRows}) {
		t.Fatalf("equal areas = %#v ok=%v, want DFS-A document", got, ok)
	}
	if got, ok := planPaneOpen(p.paneRoot, PaneDiff, nil); !ok || got != (paneOpen{Split: 2, Axis: SplitRows}) {
		t.Fatalf("nil boxes = %#v ok=%v, want DFS-A document", got, ok)
	}

	stack.Split.Ratio = 70
	got, ok = planPaneOpen(p.paneRoot, PaneDiff, p.lastPaneBoxes())
	if !ok || got != (paneOpen{Split: 2, Axis: SplitRows}) {
		t.Fatalf("dragged-larger document = %#v ok=%v last=%#v", got, ok, p.lastPaneBoxes())
	}

	stack.Split.Ratio = 25
	got, ok = planPaneOpen(p.paneRoot, PaneDiff, p.lastPaneBoxes())
	if !ok || got != (paneOpen{Split: 3, Axis: SplitRows}) {
		t.Fatalf("dragged-larger issue = %#v ok=%v last=%#v", got, ok, p.lastPaneBoxes())
	}

	p.width = 20
	if boxes := p.lastPaneBoxes(); boxes != nil {
		t.Fatalf("zoomed layout offered areas %#v", boxes)
	}
}

func TestSplitFlagIsAxisOverrideOnly(t *testing.T) {
	p := docPaneTestPlugin(t, t.TempDir(), true)
	doc := &PaneNode{ID: 2, Kind: PaneDoc, ContentID: 2}
	issue := &PaneNode{ID: 3, Kind: PaneIssue, ContentID: 3}
	stack := &PaneNode{ID: 4, Split: &PaneSplit{Axis: SplitRows, Ratio: 50, A: doc, B: issue}}
	p.paneRoot = &PaneNode{ID: 5, Split: &PaneSplit{Axis: SplitCols, Ratio: 50,
		A: &PaneNode{ID: 1, Kind: PaneTerminal},
		B: stack,
	}}
	p.paneFocus, p.paneNextID = 2, 6

	auto, ok := p.planOpen(PaneDiff)
	if !ok || auto.Retarget != 0 {
		t.Fatalf("auto = %#v ok=%v", auto, ok)
	}
	p.openSplit = "right"
	right, ok := p.planOpen(PaneDiff)
	if !ok || right.Split != auto.Split || right.Axis != SplitCols {
		t.Fatalf("--split right = %#v, want same leaf %d axis cols", right, auto.Split)
	}
	p.openSplit = "below"
	below, ok := p.planOpen(PaneDiff)
	if !ok || below.Split != auto.Split || below.Axis != SplitRows {
		t.Fatalf("--split below = %#v, want same leaf %d axis rows", below, auto.Split)
	}

	p.paneRoot = &PaneNode{ID: 6, Split: &PaneSplit{Axis: SplitCols, Ratio: 50,
		A: &PaneNode{ID: 1, Kind: PaneTerminal},
		B: &PaneNode{ID: 7, Kind: PaneDiff, ContentID: 7},
	}}
	p.openSplit = "below"
	retarget, ok := p.planOpen(PaneDiff)
	if !ok || retarget.Retarget == 0 || retarget.Split != 0 {
		t.Fatalf("--split on retarget = %#v, want retarget only", retarget)
	}
}
