package overview

import (
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/issueview"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/termpreview"
	"github.com/marcus/sidecar/internal/ui"
)

const (
	previewIssueRegionKind = "global-preview-issue"
	previewIssueTabKind    = "global-preview-issue-tab"
)

func isPreviewIssueRegion(kind string) bool {
	return kind == previewIssueRegionKind || kind == previewIssueTabKind
}

// previewIssueTabHit is the tab stored on the issue header region.
type previewIssueTabHit int

// OpenIssueInTDMsg asks the app to leave global and open this issue in td.
// The jump itself belongs to the app — this surface only names the issue.
type OpenIssueInTDMsg struct {
	IssueID string
}

// previewIssue is the memory-only issue pane beside the selected terminal.
// The shared issue group lives here; this wrapper still owns root, workspace
// surface, focus, epoch, and model-ID allocation. paneCache[workspaceID] is
// the lifetime: switching rows restores this value, a restart does not.
type previewIssue struct {
	tabs    issueview.Tabs
	root    string
	surface string
	focused bool
	epoch   uint64
}

func (i *previewIssue) view() *issueview.Model {
	if i == nil {
		return nil
	}
	return i.tabs.ActiveView()
}

// previewIssueLoadedMsg adds workspace identity to issueview's own request
// identity. Routing first resolves the workspace cache entry, then the tab
// whose model ID issued the fetch.
type previewIssueLoadedMsg struct {
	issueview.LoadedMsg
	WorkspaceID string
}

func (m *Model) openPreviewIssue(issueID string) tea.Cmd {
	workspace, ok := m.SelectedWorkspace()
	issueID = issueview.NormalizeID(issueID)
	if !ok || issueID == "" || workspace.Path == "" {
		return nil
	}
	return m.openPreviewContent(contentlink.Ref{Kind: contentlink.KindIssue, Value: issueID}, "Issue")
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

func (m *Model) previewIssueForWorkspace(workspaceID string) *previewIssue {
	if m.preview.issue != nil && m.preview.workspaceID == workspaceID {
		return m.preview.issue
	}
	if cached, ok := m.preview.paneCache[workspaceID]; ok {
		return cached.issue
	}
	return nil
}

func (m *Model) applyPreviewIssueLoaded(msg previewIssueLoadedMsg) {
	issue := m.previewIssueForWorkspace(msg.WorkspaceID)
	if issue == nil || issue.surface != msg.WorkspaceID {
		return
	}
	for _, item := range issue.tabs.Items {
		if item.Value == nil || item.Value.ModelID() != msg.ModelID {
			continue
		}
		item.Value.SetResult(msg.LoadedMsg)
		return
	}
}

func (m *Model) closePreviewIssue() tea.Cmd {
	if m.preview.deck == nil {
		return nil
	}
	m.preview.deck.FocusLeaf(m.preview.deck.Leaf(panelayout.Issue))
	for m.preview.deck.Leaf(panelayout.Issue) != 0 {
		m.preview.deck.CloseActive()
	}
	if ctx, ok := m.previewDeckContext(); ok {
		m.syncPreviewDeckProjection(ctx)
	}
	if m.preview.doc != nil {
		m.focusPreviewPane(panelayout.Document)
		return m.syncTerminalGeometry()
	}
	if m.preview.diff != nil {
		m.focusPreviewPane(panelayout.Diff)
		return m.syncTerminalGeometry()
	}
	if m.preview.resource != nil {
		m.focusPreviewPane(panelayout.Resource)
		return m.syncTerminalGeometry()
	}
	return tea.Batch(m.focusList(), m.syncTerminalGeometry())
}

func (m *Model) closePreviewIssueTab() tea.Cmd {
	if m.preview.deck == nil {
		return nil
	}
	m.preview.deck.FocusLeaf(m.preview.deck.Leaf(panelayout.Issue))
	m.preview.deck.CloseActive()
	return m.finishPreviewDeckClose()
}

func (m *Model) cyclePreviewIssueTab(delta int) tea.Cmd {
	if m.preview.deck == nil || !m.preview.deck.FocusLeaf(m.preview.deck.Leaf(panelayout.Issue)) {
		return nil
	}
	cmd := m.preview.deck.CycleTab(delta)
	if ctx, ok := m.previewDeckContext(); ok {
		m.syncPreviewDeckProjection(ctx)
	}
	return cmd
}

func (m *Model) clickPreviewIssueTab(index int) tea.Cmd {
	if m.preview.deck == nil {
		return nil
	}
	cmd := m.preview.deck.SelectTab(m.preview.deck.Leaf(panelayout.Issue), index)
	if ctx, ok := m.previewDeckContext(); ok {
		m.syncPreviewDeckProjection(ctx)
	}
	return cmd
}

func (m *Model) renderPreviewIssue(issue *previewIssue, box termpreview.Box) string {
	view := issue.view()
	contentHeight := max(box.H-termpreview.HeaderRows, 0)
	focused := m.PreviewFocused() && issue.focused
	if view != nil {
		view.SetSize(box.W, contentHeight)
		view.SetFocused(focused)
	}
	header := m.composePreviewHeader(issueview.LayoutTabStrip(issue.tabs, ui.ReserveHeaderClose(box.W).TabsWidth, focused).Row, box.W, panelayout.Issue)
	if contentHeight <= 0 {
		return header
	}
	body := ""
	if view != nil {
		body = view.View()
	}
	return header + "\n" + body
}

// registerPreviewIssueRegion covers the Issue leaf's INNER box.
func (m *Model) registerPreviewIssueRegion(issueBox termpreview.Box) {
	if m.preview.issue == nil {
		return
	}
	m.workspacesMouse.HitMap.AddRect(
		previewIssueRegionKind,
		issueBox.X, issueBox.Y, issueBox.W, issueBox.H,
		previewIssueRegionKind,
	)
}

func (m *Model) registerPreviewIssueTabRegions(issueBox termpreview.Box) {
	if m.preview.issue == nil {
		return
	}
	focused := m.PreviewFocused() && m.preview.issue.focused
	for _, tab := range issueview.LayoutTabStrip(m.preview.issue.tabs, ui.ReserveHeaderClose(issueBox.W).TabsWidth, focused).Tabs {
		m.workspacesMouse.HitMap.AddRect(
			previewIssueTabKind,
			issueBox.X+tab.Col, issueBox.Y, tab.Width, 1,
			previewIssueTabHit(tab.Index),
		)
	}
}

func (m *Model) handlePreviewIssueMouse(action mouse.MouseAction) tea.Cmd {
	if tab, ok := action.Region.Data.(previewIssueTabHit); ok {
		if action.Type == mouse.ActionClick || action.Type == mouse.ActionDoubleClick {
			return m.clickPreviewIssueTab(int(tab))
		}
		if view := m.preview.issue.view(); view != nil {
			switch action.Type {
			case mouse.ActionScrollUp, mouse.ActionScrollDown:
				view.Scroll(action.Delta)
			}
		}
		return nil
	}
	issue := m.preview.issue
	kind, _ := regionKind(action.Region)
	if kind != previewIssueRegionKind || issue == nil {
		return nil
	}
	view := issue.view()
	switch action.Type {
	case mouse.ActionClick:
		m.focusPreviewPane(panelayout.Issue)
		if view == nil {
			return nil
		}
		lx := action.X - action.Region.Rect.X
		ly := action.Y - action.Region.Rect.Y - termpreview.HeaderRows
		_, cmd := view.HandleClick(lx, ly)
		return cmd
	case mouse.ActionDoubleClick:
		// The preceding click already navigated. Consume Bubble Tea's
		// follow-up double event so a child that has just rendered its parent
		// at this cell cannot immediately navigate back.
		m.focusPreviewPane(panelayout.Issue)
		return nil
	case mouse.ActionScrollUp, mouse.ActionScrollDown:
		if view != nil {
			view.Scroll(action.Delta)
		}
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
	case "x":
		return true, m.closePreviewIssueTab()
	case "{":
		return true, m.cyclePreviewIssueTab(-1)
	case "}":
		return true, m.cyclePreviewIssueTab(1)
	case "y":
		return true, m.yankPreviewIssue(false)
	case "Y", "shift+y":
		return true, m.yankPreviewIssue(true)
	}
	view := issue.view()
	if view == nil {
		return true, nil
	}
	issue.focused = true
	view.SetActive(true)
	view.SetFocused(true)
	handled, cmd := view.HandleKey(msg)
	if handled {
		return true, cmd
	}
	// Unclaimed keys fall through to WorkspacesKey, which lets host globals
	// through and swallows the rest so they cannot drive the list.
	return false, nil
}

func (m *Model) yankPreviewIssue(idOnly bool) tea.Cmd {
	view := m.preview.issue.view()
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
