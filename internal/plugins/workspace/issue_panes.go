package workspace

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/issueview"
	"github.com/marcus/sidecar/internal/styles"
)

// issuePane is one td issue leaf's state: the component, and the terminal
// surface the link that opened it was clicked in. The surface is what lets a
// selection change collapse the leaf rather than carry an issue from one shell
// into another, exactly as a document leaf carries its own.
type issuePane struct {
	leafID  int
	root    string
	surface string
	view    *issueview.Model
}

// activeIssuePane returns the first live issue leaf. A second td link click
// retargets this leaf rather than splitting again, which mirrors how a file
// click retargets the document pane.
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
	if cmd == nil {
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
	if p.paneRoot == nil || p.ctx == nil || issueID == "" {
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
		if load == nil {
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
// fetch. It both creates a leaf's content and retargets it, so opening and
// retargeting cannot drift into two ways of holding the same state.
func (p *Plugin) attachIssuePane(leafID int, root, surface, issueID string) tea.Cmd {
	if p.ctx == nil || issueID == "" {
		return nil
	}
	if p.issues == nil {
		p.issues = make(map[int]*issuePane)
	}
	pane := p.issues[leafID]
	if pane == nil {
		pane = &issuePane{leafID: leafID, view: issueview.New(p.markdownRenderer)}
		p.issues[leafID] = pane
	}
	pane.root, pane.surface = root, surface
	return pane.view.Load(leafID, root, issueID, p.ctx.Epoch)
}

// applyIssueLoaded delivers a fetch to the pane that asked for it. The epoch
// and surface checks are the document pane's: a result that outlived its
// project or its terminal selection has nowhere to land.
func (p *Plugin) applyIssueLoaded(msg issueview.LoadedMsg) {
	issue := p.issues[msg.ModelID]
	if issue == nil || p.ctx == nil || msg.Epoch != p.ctx.Epoch {
		return
	}
	// The pane asked for this load. SetResult already rejects a stale
	// generation or issue; dropping on a transient surface mismatch left
	// the leaf stuck on "Loading issue…". A real selection change closes
	// the leaf via resetDocPanesForSelection.
	issue.view.SetResult(msg)
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
	// A focused issue pane is the active card: the pane already owns the
	// keyboard, so arrows walk parent/siblings/subtasks instead of waiting
	// for a second enter the way the preview modal must.
	issue.view.SetActive(true)
	issue.view.SetFocused(true)
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
	case "y":
		return true, p.yankFocusedIssue(false)
	case "Y", "shift+y":
		return true, p.yankFocusedIssue(true)
	default:
		beforeID, beforeScroll := issue.view.IssueID(), issue.view.ScrollOffset()
		_, cmd := issue.view.HandleKey(msg)
		if issue.view.IssueID() != beforeID || issue.view.ScrollOffset() != beforeScroll {
			p.saveSelectionState()
		}
		return true, cmd
	}
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

// issueHeaderChips renders the issue leaf's header chips: the issue's own
// identity, and the affordance that closes it. title is the content's answer
// rather than a second read of the component, so the row the frame draws and
// the regions it registers name one issue.
func (p *Plugin) issueHeaderChips(title string, width int, focused bool) []string {
	// The close chip and the row's padding are what the title gives way to;
	// the shared header layout drops a chip whole when even that does not fit.
	titleStyle := styles.BarChip
	if focused {
		titleStyle = styles.BarChipActive
	}
	return []string{
		styles.RenderPillWithStyle(p.truncateCache.Truncate(title, maxInt(width-6, 8), "…"), titleStyle, nil),
		styles.RenderPillWithStyle("×", styles.BarChip, nil),
	}
}

// issuePaneHeaderRow is the issue leaf's header row. It names the one key the
// leaf answers besides scrolling, the way the document's row names its own: a
// focused pane that says nothing about how to leave it reads as stuck.
func (p *Plugin) issuePaneHeaderRow(title string, width int, focused bool) string {
	return p.terminalHeader(p.issueHeaderChips(title, width, focused), dimText("q close"), width, 0)
}

func (p *Plugin) registerIssuePaneRegions(title string, leafID int, box Box) {
	p.mouseHandler.HitMap.AddRect(regionIssuePane, box.X, box.Y, box.W, box.H, leafID)
	chips := p.issueHeaderChips(title, box.W, p.paneFocus == leafID)
	for index, chip := range layoutHeaderChips(chips, box.W, 0) {
		if chip.Drawn && index == len(chips)-1 {
			p.mouseHandler.HitMap.AddRect(regionIssueClose, box.X+chip.Col, box.Y, chip.Width, 1, leafID)
		}
	}
}

func issueViewLocal(actionX, actionY int, box Box) (int, int) {
	return actionX - box.X, actionY - box.Y - terminalHeaderRows
}

func (p *Plugin) yankFocusedIssue(idOnly bool) tea.Cmd {
	issue, _ := p.focusedIssuePane()
	if issue == nil || issue.view == nil {
		return nil
	}
	data := issue.view.Data()
	if data == nil {
		return nil
	}
	if idOnly {
		return issueview.CopyID(data)
	}
	return issueview.CopyMarkdown(data)
}
