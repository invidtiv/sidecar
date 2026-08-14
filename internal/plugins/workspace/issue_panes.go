package workspace

import (
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
	issue.view.SetResult(msg)
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

// issuePaneHeaderRow is the issue leaf's header row. It carries no key hints:
// this leaf answers clicks and the wheel, and a hint naming a key nothing
// handles reads as a pane that has stopped responding.
func (p *Plugin) issuePaneHeaderRow(title string, width int, focused bool) string {
	return p.terminalHeader(p.issueHeaderChips(title, width, focused), "", width, 0)
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
