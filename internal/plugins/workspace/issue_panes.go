package workspace

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/issueview"
	"github.com/marcus/sidecar/internal/mouse"
)

// issuePane is one td issue leaf's tab group. The pane tree points at this,
// not at a single model. The surface is what lets a selection change collapse
// the leaf rather than carry issues from one shell into another.
type issuePane struct {
	leafID  int
	root    string
	surface string
	tabs    issueview.Tabs
}

func (i *issuePane) view() *issueview.Model {
	if i == nil {
		return nil
	}
	return i.tabs.ActiveView()
}

// activeIssuePane returns the first live issue leaf. A second td link click
// opens or focuses a tab on this leaf rather than splitting again, which
// mirrors how a file click retargets the document pane.
func (p *Plugin) activeIssuePane() (*issuePane, *PaneNode) {
	for id, issue := range p.issues {
		if issue == nil {
			continue
		}
		if leaf := FindPane(p.paneRoot, issue.leafID); leaf != nil && leaf.Kind == PaneIssue && leaf.ContentID == id {
			return issue, leaf
		}
	}
	return nil, nil
}

// activateIssueLink opens the clicked td id against the selected terminal
// surface. The surface is the same answer a clicked file is bound to, so an
// issue and a document opened from one terminal are collapsed together when
// the selection moves on.
func (p *Plugin) activateIssueLink(issueID string) (tea.Cmd, bool) {
	root, surface, ok := p.selectedTerminalSurface()
	if !ok {
		return nil, false
	}
	cmd := p.openIssuePaneForSurface(root, surface, issueID)
	issue, _ := p.activeIssuePane()
	if issue == nil || issue.tabs.Find(issueID) < 0 {
		return nil, false
	}
	p.clearTerminalSelection()
	return cmd, true
}

// openIssuePaneForSurface opens issueID in the pane tree at the place
// planPaneOpen names. The split is trialled on a clone first, exactly as a
// document's is: a box that cannot hold the result leaves the terminal at the
// size it already has rather than reflowing an agent for a pane that will not
// be drawn.
func (p *Plugin) openIssuePaneForSurface(root, surface, issueID string) tea.Cmd {
	if p.paneRoot == nil || p.ctx == nil || issueview.NormalizeID(issueID) == "" {
		return nil
	}
	plan, ok := planPaneOpen(p.paneRoot, PaneIssue)
	if !ok {
		return nil
	}
	if plan.Retarget != 0 {
		leaf := FindPane(p.paneRoot, plan.Retarget)
		if leaf == nil || leaf.Split != nil {
			return nil
		}
		load := p.attachIssuePane(leaf.ContentID, root, surface, issueID)
		if p.issues[leaf.ContentID] == nil || p.issues[leaf.ContentID].view() == nil {
			return nil
		}
		p.paneFocus = leaf.ID
		p.activePane = PanePreview
		p.saveSelectionState()
		return load
	}

	content, placed := p.previewContentBox()
	if !placed {
		return nil
	}
	id := p.paneNextID
	trial, trialFocus := SplitLeaf(clonePaneTree(p.paneRoot), plan.Split, plan.Axis,
		&PaneNode{ID: id, Kind: PaneIssue, ContentID: id})
	if trialFocus != id {
		return nil
	}
	if _, _, fits := LayoutPanes(trial, content, paneTreeFloors()); !fits {
		p.toastMessage = paneFitMessage("Issue", plan.Axis)
		p.toastTime = time.Now()
		return nil
	}

	newLeaf := &PaneNode{ID: id, Kind: PaneIssue, ContentID: id}
	treeRoot, focus := SplitLeaf(p.paneRoot, plan.Split, plan.Axis, newLeaf)
	if focus != newLeaf.ID {
		return nil
	}
	p.paneRoot, p.paneFocus = treeRoot, focus
	p.paneNextID = maxInt(p.paneNextID, maxPaneID(p.paneRoot)+1)
	p.activePane = PanePreview
	load := p.attachIssuePane(newLeaf.ContentID, root, surface, issueID)
	p.saveSelectionState()
	return tea.Batch(load, p.resizeDocTerminalCmd())
}

// attachIssuePane points the content behind leafID at issueID and returns its
// fetch when a new tab is created. An already-open ID is focused and returns
// nil; the pane still exists so a retarget can proceed without a load.
func (p *Plugin) attachIssuePane(leafID int, root, surface, issueID string) tea.Cmd {
	issueID = issueview.NormalizeID(issueID)
	if p.ctx == nil || issueID == "" {
		return nil
	}
	if p.issues == nil {
		p.issues = make(map[int]*issuePane)
	}
	pane := p.issues[leafID]
	if pane == nil {
		pane = &issuePane{leafID: leafID}
		p.issues[leafID] = pane
	}
	pane.root, pane.surface = root, surface
	return p.openOrFocusIssue(pane, issueID)
}

func (p *Plugin) newIssueModel(pane *issuePane) *issueview.Model {
	view := issueview.New(p.markdownRenderer)
	view.OpenHandler = func(id string) tea.Cmd {
		if pane == nil || p.issues[pane.leafID] != pane {
			return nil
		}
		return p.openOrFocusIssue(pane, id)
	}
	return view
}

func (p *Plugin) nextIssueModelID() int {
	p.issueModelNextID++
	return p.issueModelNextID
}

// openOrFocusIssue selects an existing tab for issueID or appends a fresh
// model and loads it. The returned command is the fetch for a new tab, or
// nil when the ID was already open.
func (p *Plugin) openOrFocusIssue(pane *issuePane, issueID string) tea.Cmd {
	issueID = issueview.NormalizeID(issueID)
	if pane == nil || p.ctx == nil || issueID == "" {
		return nil
	}
	if idx := pane.tabs.Find(issueID); idx >= 0 {
		pane.tabs.Select(idx)
		return nil
	}
	view := p.newIssueModel(pane)
	if _, created := pane.tabs.OpenOrFocus(issueID, view); !created {
		return nil
	}
	return view.Load(p.nextIssueModelID(), pane.root, issueID, p.ctx.Epoch)
}

// applyIssueLoaded delivers a fetch to the tab that asked for it. The epoch
// check is the document pane's: a result that outlived its project has
// nowhere to land. Routing is pane then tab-by-model-ID, so a closed tab or
// a different live tab cannot consume the result.
func (p *Plugin) applyIssueLoaded(msg issueview.LoadedMsg) {
	if p.ctx == nil || msg.Epoch != p.ctx.Epoch {
		return
	}
	for _, issue := range p.issues {
		if issue == nil {
			continue
		}
		for _, item := range issue.tabs.Items {
			if item.Value == nil || item.Value.ModelID() != msg.ModelID {
				continue
			}
			item.Value.SetResult(msg)
			return
		}
	}
}

// issueFocused is the issue leaf's own version of docFocused: not "a content
// leaf holds focus" but "the focused leaf is an issue". A leaf drawn as focused
// owns the keyboard, and without an answer here the keys under a highlighted
// issue pane are still the agent terminal's — `q` would open the quit
// confirmation, `enter` would start typing at the agent.
func (p *Plugin) issueFocused() bool {
	issue, _ := p.focusedIssuePane()
	return issue != nil
}

// focusedIssuePane is the issue leaf holding preview focus, if that is what
// holds it. It reads paneFocus rather than the first issue in the tree, so a
// key can only ever reach the leaf the frame drew as focused.
func (p *Plugin) focusedIssuePane() (*issuePane, *PaneNode) {
	if !p.previewLeafFocused() {
		return nil, nil
	}
	leaf := FindPane(p.paneRoot, p.paneFocus)
	if leaf == nil || leaf.Kind != PaneIssue {
		return nil, nil
	}
	issue := p.issues[leaf.ContentID]
	if issue == nil {
		return nil, nil
	}
	return issue, leaf
}

// handleIssueKey is the focused issue leaf's input context, the counterpart of
// handleDocKey. It closes and scrolls, and absorbs everything else: a key this
// pane does not own must not fall through to the terminal behind it, which is
// the pane the user is not looking at.
func (p *Plugin) handleIssueKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	issue, leaf := p.focusedIssuePane()
	if issue == nil {
		return false, nil
	}
	view := issue.view()
	if view != nil {
		// A focused issue pane is the active card: the pane already owns the
		// keyboard, so arrows walk parent/siblings/subtasks instead of waiting
		// for a second enter the way the preview modal must.
		view.SetActive(true)
		view.SetFocused(true)
	}
	switch msg.String() {
	case "tab", "shift+tab":
		// The pane cycle lives on the list keymap so issue, doc, and
		// terminal stay one ring. Claiming Tab here made the issue leaf
		// a dead end.
		return false, nil
	case "\\":
		return true, p.toggleSidebarCmd()
	case "q", "esc":
		return true, p.closeIssuePane(leaf.ID)
	case "x":
		return true, p.closeActiveIssueTab()
	case "{":
		return true, p.cycleActiveIssueTab(-1)
	case "}":
		return true, p.cycleActiveIssueTab(1)
	case "y":
		return true, p.yankFocusedIssue(false)
	case "Y", "shift+y":
		return true, p.yankFocusedIssue(true)
	default:
		if view == nil {
			return true, nil
		}
		beforeActive := issue.tabs.Active
		beforeID, beforeScroll := view.IssueID(), view.ScrollOffset()
		_, cmd := view.HandleKey(msg)
		after := issue.view()
		if issue.tabs.Active != beforeActive ||
			(after != nil && (after.IssueID() != beforeID || after.ScrollOffset() != beforeScroll)) {
			p.saveSelectionState()
		}
		return true, cmd
	}
}

func (p *Plugin) cycleActiveIssueTab(delta int) tea.Cmd {
	issue, _ := p.focusedIssuePane()
	if issue == nil || len(issue.tabs.Items) < 2 {
		return nil
	}
	issue.tabs.Cycle(delta)
	p.saveSelectionState()
	return nil
}

func (p *Plugin) closeActiveIssueTab() tea.Cmd {
	issue, leaf := p.focusedIssuePane()
	if issue == nil {
		return nil
	}
	if len(issue.tabs.Items) <= 1 {
		return p.closeIssuePane(leaf.ID)
	}
	issue.tabs.CloseActive()
	p.saveSelectionState()
	return nil
}

func (p *Plugin) selectIssueTab(issue *issuePane, leafID, idx int) tea.Cmd {
	if issue == nil {
		return nil
	}
	p.activePane = PanePreview
	p.paneFocus = leafID
	p.termPanelFocused = false
	p.pointer.Abandon()
	if p.viewMode == ViewModeInteractive {
		p.exitInteractiveMode()
	}
	if idx == issue.tabs.Active {
		return nil
	}
	issue.tabs.Select(idx)
	p.saveSelectionState()
	return nil
}

// clickIssueTabAt selects an issue tab from a pointer position. File tabs
// test the tab row first so a one-cell miss on a widened divider does not
// become a terminal click; issue tabs need the same steal.
func (p *Plugin) clickIssueTabAt(x, y int) (tea.Cmd, bool) {
	if !p.docVisible() {
		return nil, false
	}
	var tabs []mouse.Region
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if region.ID != regionIssueTab {
			continue
		}
		if y != region.Rect.Y {
			continue
		}
		tabs = append(tabs, region)
	}
	if len(tabs) == 0 {
		return nil, false
	}
	inIssueHeader := false
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if region.ID != regionIssuePane {
			continue
		}
		if x >= region.Rect.X && x < region.Rect.X+region.Rect.W && y == region.Rect.Y {
			inIssueHeader = true
			break
		}
	}
	if !inIssueHeader {
		return nil, false
	}
	best := tabs[0]
	bestDist := tabRowDistance(x, best.Rect)
	for _, region := range tabs[1:] {
		if d := tabRowDistance(x, region.Rect); d < bestDist {
			best, bestDist = region, d
		}
	}
	return p.clickIssueTab(best.Data), true
}

func (p *Plugin) clickIssueTab(data any) tea.Cmd {
	hit, ok := data.(issueTabHit)
	if !ok {
		return nil
	}
	leaf := FindPane(p.paneRoot, hit.LeafID)
	if leaf == nil || leaf.Kind != PaneIssue {
		return nil
	}
	issue := p.issues[leaf.ContentID]
	if issue == nil {
		return nil
	}
	return p.selectIssueTab(issue, hit.LeafID, hit.Index)
}

// closeIssuePane removes the issue leaf and gives its box back to its sibling.
func (p *Plugin) closeIssuePane(leafID int) tea.Cmd {
	if !p.closeContentLeaf(leafID) {
		return nil
	}
	p.activePane = PanePreview
	p.saveSelectionState()
	return p.resizeDocTerminalCmd()
}

// issuePaneHeaderRow is the issue leaf's header: the tab strip only.
func (p *Plugin) issuePaneHeaderRow(issue *issuePane, width int, focused bool) string {
	return layoutIssueTabStrip(issue, width, focused).Row
}

func (p *Plugin) registerIssuePaneRegions(issue *issuePane, leafID int, box Box) {
	p.mouseHandler.HitMap.AddRect(regionIssuePane, box.X, box.Y, box.W, box.H, leafID)
}

func (p *Plugin) registerIssueTabRegions(issue *issuePane, leafID int, box Box) {
	for _, tab := range layoutIssueTabStrip(issue, box.W, p.paneFocus == leafID).Tabs {
		p.mouseHandler.HitMap.AddRect(regionIssueTab, box.X+tab.Col, box.Y, tab.Width, 1, issueTabHit{LeafID: leafID, Index: tab.Index})
	}
}

func issueViewLocal(actionX, actionY int, box Box) (int, int) {
	return actionX - box.X, actionY - box.Y - terminalHeaderRows
}

func (p *Plugin) yankFocusedIssue(idOnly bool) tea.Cmd {
	issue, _ := p.focusedIssuePane()
	view := issue.view()
	if view == nil {
		return nil
	}
	data := view.Data()
	if data == nil {
		return nil
	}
	if idOnly {
		return issueview.CopyID(data)
	}
	return issueview.CopyMarkdown(data)
}
