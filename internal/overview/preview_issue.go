package overview

import (
	tea "charm.land/bubbletea/v2"
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
	nextID  int
}

func (i *previewIssue) view() *issueview.Model {
	if i == nil {
		return nil
	}
	return i.tabs.ActiveView()
}

func (i *previewIssue) allocID() int {
	i.nextID++
	return i.nextID
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
	leafID, refusal := m.ensurePreviewPane(panelayout.Issue, "Issue")
	if refusal != nil {
		return refusal
	}
	if leafID == 0 {
		return nil
	}

	wasInteractive := m.PreviewInteractive()
	if m.preview.issue == nil {
		m.preview.issue = &previewIssue{epoch: m.nextPreviewContentEpoch()}
	}
	issue := m.preview.issue
	issue.root = workspace.Path
	issue.surface = workspace.ID
	m.focusPreviewPane(panelayout.Issue)
	load := m.openOrFocusPreviewIssue(issue, issueID)
	if view := issue.view(); view != nil {
		view.SetActive(true)
		view.SetFocused(true)
	}

	var cmds []tea.Cmd
	if wasInteractive {
		cmds = append(cmds, m.exitPreviewInteractive())
	}
	cmds = append(cmds, load, m.syncTerminalGeometry())
	return tea.Batch(cmds...)
}

func (m *Model) newPreviewIssueModel(issue *previewIssue) *issueview.Model {
	view := issueview.New(nil)
	view.OpenHandler = func(id string) tea.Cmd {
		if m.preview.issue != issue {
			return nil
		}
		return m.openOrFocusPreviewIssue(issue, id)
	}
	return view
}

// openOrFocusPreviewIssue selects an existing tab or appends a fresh model
// and loads it. A unique model ID is allocated per new tab so a late result
// cannot land on whichever tab is now active.
func (m *Model) openOrFocusPreviewIssue(issue *previewIssue, issueID string) tea.Cmd {
	issueID = issueview.NormalizeID(issueID)
	if issue == nil || issueID == "" {
		return nil
	}
	if idx := issue.tabs.Find(issueID); idx >= 0 {
		issue.tabs.Select(idx)
		return nil
	}
	view := m.newPreviewIssueModel(issue)
	if _, created := issue.tabs.OpenOrFocus(issueID, view); !created {
		return nil
	}
	return wrapPreviewIssueLoad(view.Load(issue.allocID(), issue.root, issueID, issue.epoch), issue.surface)
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
	if m.preview.issue == nil {
		return nil
	}
	m.forgetPreviewIssue(m.preview.workspaceID)
	if leaf := panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Issue); leaf != nil {
		m.preview.paneRoot, m.preview.paneFocus = panelayout.Close(m.preview.paneRoot, leaf.ID)
	}
	if m.preview.doc != nil {
		m.focusPreviewPane(panelayout.Document)
		return m.syncTerminalGeometry()
	}
	if m.preview.diff != nil {
		m.focusPreviewPane(panelayout.Diff)
		return m.syncTerminalGeometry()
	}
	return tea.Batch(m.focusList(), m.syncTerminalGeometry())
}

// forgetPreviewIssue drops the in-memory tab set for workspaceID. Global
// issue tabs are not written to disk; q/esc and last-x must not leave a
// cache entry that a later row switch would restore.
func (m *Model) forgetPreviewIssue(workspaceID string) {
	m.preview.issue = nil
	if cached, ok := m.preview.paneCache[workspaceID]; ok {
		cached.issue = nil
		m.preview.paneCache[workspaceID] = cached
	}
}

func (m *Model) closePreviewIssueTab() tea.Cmd {
	if m.preview.issue == nil {
		return nil
	}
	if len(m.preview.issue.tabs.Items) <= 1 {
		return m.closePreviewIssue()
	}
	m.preview.issue.tabs.CloseActive()
	return nil
}

func (m *Model) cyclePreviewIssueTab(delta int) tea.Cmd {
	if m.preview.issue == nil || len(m.preview.issue.tabs.Items) < 2 {
		return nil
	}
	m.preview.issue.tabs.Cycle(delta)
	return nil
}

func (m *Model) clickPreviewIssueTab(index int) tea.Cmd {
	if m.preview.issue == nil {
		return nil
	}
	m.focusPreviewPane(panelayout.Issue)
	if index == m.preview.issue.tabs.Active {
		return nil
	}
	m.preview.issue.tabs.Select(index)
	return nil
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

func (m *Model) registerPreviewIssueRegions(box termpreview.Box) {
	if m.preview.issue == nil {
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
}

func (m *Model) registerPreviewIssueTabRegions(box termpreview.Box) {
	if m.preview.issue == nil {
		return
	}
	issueBox, ok := m.previewPaneBox(panelayout.Issue, box)
	if !ok {
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
	// A focused issue is its own input context. Do not let an unowned key
	// refresh, navigate, or type into the terminal behind the visible card.
	return true, nil
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
