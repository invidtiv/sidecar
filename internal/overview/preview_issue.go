package overview

import (
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/issueview"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/termpreview"
)

const (
	previewIssueRegionKind = "global-preview-issue"
	previewIssueCloseKind  = "global-preview-issue-close"
	previewIssueModelID    = -1
)

func isPreviewIssueRegion(kind string) bool {
	return kind == previewIssueRegionKind || kind == previewIssueCloseKind
}

// previewIssue is the memory-only issue card beside the selected terminal.
// Its root and surface keep the read scoped to the workspace whose link was
// clicked; selection changes drop the entire value in resetPreviewContent.
type previewIssue struct {
	view    *issueview.Model
	root    string
	surface string
	focused bool
	epoch   uint64
}

// previewIssueLoadedMsg adds workspace identity to issueview's own request
// identity. The component protects retargets while the wrapper routes a result
// to the active or cached workspace that owns it.
type previewIssueLoadedMsg struct {
	issueview.LoadedMsg
	WorkspaceID string
}

func (m *Model) openPreviewIssue(issueID string) tea.Cmd {
	workspace, ok := m.SelectedWorkspace()
	if !ok || issueID == "" || workspace.Path == "" {
		return nil
	}
	leafID, refusal := m.ensurePreviewPane(panelayout.Issue, "Issue")
	if refusal != nil {
		return refusal
	}
	if leafID == 0 {
		return nil
	}

	wasInteractive := m.PreviewInteractive()
	if m.preview.issue == nil {
		m.preview.issue = &previewIssue{view: issueview.New(nil), epoch: m.nextPreviewContentEpoch()}
	}
	issue := m.preview.issue
	issue.root = workspace.Path
	issue.surface = workspace.ID
	m.focusPreviewPane(panelayout.Issue)
	issue.view.SetActive(true)
	issue.view.SetFocused(true)
	load := issue.view.Load(previewIssueModelID, issue.root, issueID, issue.epoch)

	var cmds []tea.Cmd
	if wasInteractive {
		cmds = append(cmds, m.exitPreviewInteractive())
	}
	cmds = append(cmds,
		wrapPreviewIssueLoad(load, workspace.ID),
		m.syncTerminalGeometry(),
	)
	return tea.Batch(cmds...)
}

func wrapPreviewIssueLoad(cmd tea.Cmd, workspaceID string) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		msg := cmd()
		if loaded, ok := msg.(issueview.LoadedMsg); ok {
			return previewIssueLoadedMsg{
				LoadedMsg: loaded, WorkspaceID: workspaceID,
			}
		}
		return msg
	}
}

func (m *Model) applyPreviewIssueLoaded(msg previewIssueLoadedMsg) {
	issue := m.preview.issue
	if msg.WorkspaceID != m.preview.workspaceID {
		issue = m.preview.paneCache[msg.WorkspaceID].issue
	}
	if issue == nil || issue.view == nil || issue.surface != msg.WorkspaceID {
		return
	}
	issue.view.SetResult(msg.LoadedMsg)
}

func (m *Model) closePreviewIssue() tea.Cmd {
	if m.preview.issue == nil {
		return nil
	}
	m.preview.issue = nil
	if leaf := panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Issue); leaf != nil {
		m.preview.paneRoot, m.preview.paneFocus = panelayout.Close(m.preview.paneRoot, leaf.ID)
	}
	if m.preview.doc != nil {
		m.focusPreviewPane(panelayout.Document)
		return m.syncTerminalGeometry()
	}
	return tea.Batch(m.focusList(), m.syncTerminalGeometry())
}

func previewIssueHeaderChips(issue *previewIssue, width int, focused bool) []string {
	title := issue.view.Title()
	title = termpreview.TruncateANSI(title, max(width-6, 8))
	style := styles.BarChip
	if focused {
		style = styles.BarChipActive
	}
	return []string{
		styles.RenderPillWithStyle(title, style, nil),
		styles.RenderPillWithStyle("×", styles.BarChip, nil),
	}
}

func (m *Model) renderPreviewIssue(issue *previewIssue, box termpreview.Box) string {
	contentHeight := max(box.H-termpreview.HeaderRows, 0)
	issue.view.SetSize(box.W, contentHeight)
	focused := m.PreviewFocused() && issue.focused
	issue.view.SetFocused(focused)
	header := termpreview.HeaderRow(
		previewIssueHeaderChips(issue, box.W, focused),
		styles.Muted.Render("q close"), box.W, 0, termpreview.TruncateANSI,
	)
	if contentHeight <= 0 {
		return header
	}
	return header + "\n" + issue.view.View()
}

func (m *Model) registerPreviewIssueRegions(box termpreview.Box) {
	issue := m.preview.issue
	if issue == nil {
		return
	}
	issueBox, split := m.previewPaneBox(panelayout.Issue, box)
	if !split {
		return
	}
	m.workspacesMouse.HitMap.AddRect(
		previewIssueRegionKind,
		issueBox.X, issueBox.Y, issueBox.W, issueBox.H,
		previewIssueRegionKind,
	)
	chips := previewIssueHeaderChips(issue, issueBox.W, m.PreviewFocused() && issue.focused)
	for index, chip := range termpreview.LayoutChips(chips, issueBox.W, 0) {
		if chip.Drawn && index == len(chips)-1 {
			m.workspacesMouse.HitMap.AddRect(
				previewIssueCloseKind,
				issueBox.X+chip.Col, issueBox.Y, chip.Width, 1,
				previewIssueCloseKind,
			)
		}
	}
}

func (m *Model) handlePreviewIssueMouse(action mouse.MouseAction) tea.Cmd {
	kind, _ := regionKind(action.Region)
	if kind == previewIssueCloseKind {
		if action.Type == mouse.ActionClick || action.Type == mouse.ActionDoubleClick {
			return m.closePreviewIssue()
		}
		return nil
	}
	issue := m.preview.issue
	if kind != previewIssueRegionKind || issue == nil {
		return nil
	}
	switch action.Type {
	case mouse.ActionClick:
		m.focusPreviewPane(panelayout.Issue)
		lx := action.X - action.Region.Rect.X
		ly := action.Y - action.Region.Rect.Y - termpreview.HeaderRows
		_, cmd := issue.view.HandleClick(lx, ly)
		return wrapPreviewIssueLoad(cmd, issue.surface)
	case mouse.ActionDoubleClick:
		// The preceding click already navigated. Consume Bubble Tea's
		// follow-up double event so a child that has just rendered its parent
		// at this cell cannot immediately navigate back.
		m.focusPreviewPane(panelayout.Issue)
		return nil
	case mouse.ActionScrollUp, mouse.ActionScrollDown:
		issue.view.Scroll(action.Delta)
	}
	return nil
}

func (m *Model) previewIssueKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	issue := m.preview.issue
	if issue == nil || !issue.focused || m.PreviewInteractive() {
		return false, nil
	}
	switch msg.String() {
	case "q", "esc":
		return true, m.closePreviewIssue()
	case "y":
		return true, m.yankPreviewIssue(false)
	case "Y", "shift+y":
		return true, m.yankPreviewIssue(true)
	}
	issue.focused = true
	issue.view.SetActive(true)
	issue.view.SetFocused(true)
	handled, cmd := issue.view.HandleKey(msg)
	if handled {
		return true, wrapPreviewIssueLoad(cmd, issue.surface)
	}
	// A focused issue is its own input context. Do not let an unowned key
	// refresh, navigate, or type into the terminal behind the visible card.
	return true, nil
}

func (m *Model) yankPreviewIssue(idOnly bool) tea.Cmd {
	issue := m.preview.issue
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
