package overview

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/termpreview"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
	"github.com/marcus/sidecar/internal/workspacediff"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

const (
	previewTabRegionKind   = "global-preview-tab"
	previewGitRegionKind   = "global-preview-git"
	previewDiffDividerKind = "global-preview-diff-divider"
	previewTabRows         = 2
)

type previewDiffDividerHit struct{}

// previewTabHit is the tab stored on the tab-row region.
type previewTabHit int

// previewGitHit marks the Git action chip (a jump, not a tab).
type previewGitHit struct{}

// previewTabSet is which chips this row may show. Global shells get Output+Diff;
// topic worktrees keep Output+Diff+Task; the main worktree stays tabless.
func (m *Model) previewTabSet() workspacediff.TabSet {
	workspace, ok := m.SelectedWorkspace()
	if !ok {
		return workspacediff.TabSetNone
	}
	return workspacediff.GlobalTabsFor(workspace.Kind == workspaceinventory.KindShell, workspace.IsMain)
}

func (m *Model) previewTabsVisible() bool {
	return m.previewTabSet().Visible()
}

func (m *Model) previewTabChips() []string {
	return workspacediff.TabChipsFor(m.previewTab, m.previewTabSet())
}

func gitActionChip() string {
	return styles.RenderPillWithStyle("Git", styles.BarChip, nil)
}

func (m *Model) previewTabRowChips() []string {
	chips := m.previewTabChips()
	if m.canOpenInGit() {
		chips = append(chips, gitActionChip())
	}
	return chips
}

func (m *Model) cyclePreviewTab(delta int) tea.Cmd {
	if !m.previewTabsVisible() {
		return nil
	}
	m.previewTab = workspacediff.CycleTabIn(m.previewTab, delta, m.previewTabSet())
	return tea.Batch(m.ensurePreviewExtras(), m.syncPreviewTerminal())
}

func (m *Model) setPreviewTab(tab workspacediff.Tab) tea.Cmd {
	if !m.previewTabSet().Contains(tab) {
		m.previewTab = workspacediff.TabOutput
		return m.syncPreviewTerminal()
	}
	if m.previewTab == tab {
		return tea.Batch(m.ensurePreviewExtras(), m.syncPreviewTerminal())
	}
	m.previewTab = tab
	return tea.Batch(m.ensurePreviewExtras(), m.syncPreviewTerminal())
}

// ensureOutputTab is what Enter uses: Diff/Task are views of the row, so
// typing always happens on Output.
func (m *Model) ensureOutputTab() tea.Cmd {
	if m.previewTab == workspacediff.TabOutput || !m.previewTabsVisible() {
		return nil
	}
	m.previewTab = workspacediff.TabOutput
	return m.syncPreviewTerminal()
}

// ensurePreviewExtras loads Diff/Task for the selected row. Switching
// rows rebuilds the model; the same row is a no-op if already loaded.
// Shells load Diff from ProjectRoot (the main checkout), not a worktree path.
func (m *Model) ensurePreviewExtras() tea.Cmd {
	workspace, ok := m.SelectedWorkspace()
	if !ok || !m.previewTabsVisible() {
		m.resetPreviewExtras()
		m.previewTab = workspacediff.TabOutput
		return nil
	}
	if !m.previewTabSet().Contains(m.previewTab) {
		m.previewTab = workspacediff.TabOutput
	}
	if workspace.ID != m.previewExtrasID {
		m.resetPreviewExtras()
		m.previewExtrasID = workspace.ID
	}
	switch m.previewTab {
	case workspacediff.TabDiff:
		path := previewDiffPath(workspace)
		m.diff.Bind(path, workspace.ID, m.preview.contentEpoch)
		m.diff.Target = workspacediff.WorkingTreeTarget()
		if m.diff.ListWidth() == 0 {
			if w := state.GetDiffTabFileListWidth(); w > 0 {
				m.diff.SetListWidth(w)
			}
		}
		if m.diff.State != workspacediff.LoadStateUnknown && m.diff.State != workspacediff.LoadStateError {
			return m.diff.LoadSelectedCommit(path, workspace.ID)
		}
		m.diff.State = workspacediff.LoadStateLoading
		return workspacediff.LoadSnapshotCmdAt(path, "", workspace.ID, m.preview.contentEpoch, workspacediff.IdentityWorkingTree)
	case workspacediff.TabTask:
		return m.loadPreviewTask(workspace)
	}
	return nil
}

// previewDiffPath is the checkout workspacediff should read. Shells have no
// worktree of their own; the mini-diff is the project's main checkout.
func previewDiffPath(workspace workspaceinventory.Workspace) string {
	if workspace.Kind == workspaceinventory.KindShell {
		return workspace.ProjectRoot
	}
	return workspace.Path
}

func (m *Model) resetPreviewExtras() {
	width, mode := m.diff.ListWidth(), m.diff.ViewMode
	m.diff = workspacediff.View{ViewMode: mode, Target: workspacediff.WorkingTreeTarget()}
	m.diff.SetListWidth(width)
	m.task = workspacediff.TaskView{}
	m.previewExtrasID = ""
}

func (m *Model) loadPreviewTask(workspace workspaceinventory.Workspace) tea.Cmd {
	if workspace.TaskID == "" {
		m.task = workspacediff.TaskView{}
		return nil
	}
	if m.task.TaskID == workspace.TaskID && (m.task.Task != nil || m.task.Loading) {
		return nil
	}
	m.task = workspacediff.TaskView{TaskID: workspace.TaskID, Loading: true}
	return workspacediff.LoadTaskCmd(workspace.Path, workspace.TaskID, workspace.ID)
}

func (m *Model) applyDiffSnapshot(msg workspacediff.SnapshotMsg) tea.Cmd {
	workspace, ok := m.SelectedWorkspace()
	if !ok || workspace.ID != msg.WorkspaceID {
		return nil
	}
	m.diff.Bind(previewDiffPath(workspace), workspace.ID, m.preview.contentEpoch)
	return m.diff.ApplySnapshotMsg(msg, previewDiffPath(workspace), workspace.ID)
}

func (m *Model) applyCommitDetail(msg workspacediff.CommitDetailMsg) {
	workspace, ok := m.SelectedWorkspace()
	if !ok || workspace.ID != msg.WorkspaceID {
		return
	}
	m.diff.Bind(previewDiffPath(workspace), workspace.ID, m.preview.contentEpoch)
	_ = m.diff.ApplyCommitDetail(msg)
}

func (m *Model) applyTask(msg workspacediff.TaskMsg) {
	workspace, ok := m.SelectedWorkspace()
	if !ok || workspace.ID != msg.WorkspaceID {
		return
	}
	if m.task.TaskID != msg.TaskID {
		return
	}
	m.task.Loading = false
	if msg.Err != nil {
		m.task.Error = msg.Err.Error()
		return
	}
	m.task.Task = msg.Task
}

func (m *Model) previewDiffKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	if !m.previewTabsVisible() || m.previewTab != workspacediff.TabDiff {
		return false, nil
	}
	w, h := m.diffContentSize()
	m.diff.SetSize(w, h)
	cmd, handled := m.diff.HandleKey(msg)
	if !handled {
		return false, nil
	}
	m.persistDiffViewMode()
	return true, cmd
}

func (m *Model) persistDiffViewMode() {
	switch m.diff.ViewMode {
	case workspacediff.ViewSideBySide:
		_ = state.SetWorkspaceDiffMode("side-by-side")
	case workspacediff.ViewFullFile:
		_ = state.SetWorkspaceDiffMode("full-file")
	default:
		_ = state.SetWorkspaceDiffMode("unified")
	}
}

func (m *Model) diffContentSize() (int, int) {
	box, ok := m.previewBox()
	if !ok {
		h := m.height - 2 - previewTabRows
		if h < 1 {
			h = 1
		}
		return m.width, h
	}
	h := box.H - previewTabRows
	if h < 1 {
		h = 1
	}
	return box.W, h
}

func (m *Model) registerPreviewDiffRegions(box termpreview.Box) {
	if !m.previewTabsVisible() || m.previewTab != workspacediff.TabDiff {
		return
	}
	leaf := mouse.Rect{X: box.X, Y: box.Y + previewTabRows, W: box.W, H: box.H - previewTabRows}
	if leaf.H < 1 {
		return
	}
	m.diff.SetSize(leaf.W, leaf.H)
	for _, hit := range m.diff.FileHits(leaf) {
		m.workspacesMouse.HitMap.AddRect(hit.ID, hit.Rect.X, hit.Rect.Y, hit.Rect.W, hit.Rect.H, hit.Data)
	}
	if d := m.diff.DividerHit(leaf); d.W > 0 && d.H > 0 {
		m.workspacesMouse.HitMap.AddRect(previewDiffDividerKind, d.X, d.Y, d.W, d.H, previewDiffDividerHit{})
	}
}

func (m *Model) registerPreviewTabRegions(box termpreview.Box) {
	if box.W < 1 {
		return
	}
	workspace, ok := m.SelectedWorkspace()
	if !ok {
		return
	}
	chips, tabCount, gitIndex := m.previewHitChips(workspace)
	if len(chips) == 0 {
		return
	}
	hintFloor := 0
	if m.previewTab == workspacediff.TabOutput && m.PreviewInteractive() {
		hintFloor = len([]rune(m.interactiveHints()))
	}
	for i, placement := range termpreview.LayoutChips(chips, box.W, hintFloor) {
		if !placement.Drawn {
			continue
		}
		if i == gitIndex {
			m.workspacesMouse.HitMap.AddRect(previewGitRegionKind, box.X+placement.Col, box.Y, placement.Width, 1, previewGitHit{})
			continue
		}
		if m.PreviewInteractive() || i >= tabCount {
			continue
		}
		tabs := m.previewTabSet().Tabs()
		if i < len(tabs) {
			m.workspacesMouse.HitMap.AddRect(previewTabRegionKind, box.X+placement.Col, box.Y, placement.Width, 1, previewTabHit(tabs[i]))
		}
	}
}

// previewHitChips is the header chips that have hit targets. The Git chip is
// registered even while typing, so it can jump without sending O to the pane.
func (m *Model) previewHitChips(workspace workspaceinventory.Workspace) (chips []string, tabCount, gitIndex int) {
	gitIndex = -1
	if m.previewTabsVisible() && !m.PreviewInteractive() {
		chips = m.previewTabRowChips()
		tabCount = len(m.previewTabChips())
		if m.canOpenInGit() {
			gitIndex = tabCount
		}
		return chips, tabCount, gitIndex
	}
	if m.PreviewInteractive() && m.canOpenInGit() {
		chips = m.previewHeaderChips(workspace)
		gitIndex = len(chips) - 1
		return chips, 0, gitIndex
	}
	return nil, 0, -1
}

func (m *Model) renderPreviewWithTabs(width, height int) string {
	if width < 1 || height < 1 {
		return ""
	}
	if !m.previewTabsVisible() || m.previewTab == workspacediff.TabOutput {
		return m.renderOutputPreview(width, height)
	}

	var lines []string
	lines = append(lines, termpreview.HeaderRow(m.previewTabRowChips(), "", width, 0, termpreview.TruncateANSI))
	lines = append(lines, "")
	contentHeight := height - previewTabRows
	if contentHeight < 1 {
		contentHeight = 1
	}
	switch m.previewTab {
	case workspacediff.TabDiff:
		m.diff.SetSize(width, contentHeight)
		lines = append(lines, m.diff.Render(width, contentHeight, workspacediff.RenderOpts{
			Truncate: func(s string, w int, _ string) string { return termpreview.TruncateANSI(s, w) },
		}))
	case workspacediff.TabTask:
		view, count := workspacediff.RenderTask(m.task, workspacediff.TaskRenderOpts{
			Width: width, Height: contentHeight, Offset: m.task.Offset,
		})
		m.task.LineCount = count
		lines = append(lines, view)
	}
	return m.truncatePreviewLines(strings.Join(lines, "\n"), width)
}

func (m *Model) renderOutputTerminal(width, height int) string {
	workspace, ok := m.SelectedWorkspace()
	if !ok {
		return termpreview.RenderBuffer(termpreview.RenderBufferInput{
			Width: width, Height: height, Message: "No workspace selected",
		})
	}

	chips := m.previewHeaderChips(workspace)
	hints := m.interactiveHints()
	if !m.PreviewInteractive() {
		hints = previewHints(workspace, m.PreviewFocused())
	}
	message := m.preview.reason
	if message != "" {
		message += "\n\n" + previewMetadata(workspace)
	}

	input := m.previewViewportInput(width, height-termpreview.HeaderRows)
	_, total := tty.BufferBase(input.Buffer)
	layout := tty.FitViewport(input)
	hints = m.appendWindowStatus(styles.Muted.Render(hints), input, layout, width, chips)
	return termpreview.RenderBuffer(termpreview.RenderBufferInput{
		Width: width, Height: height, Chips: chips, Hints: hints,
		Layout: layout, Buffer: input.Buffer, AbsoluteBase: input.AbsoluteBase,
		TotalItems: total, PaneHeight: input.PaneHeight, Interactive: input.Interactive,
		Follow: input.Follow, Selection: &m.preview.selection, TabWidth: tty.DefaultTabWidth,
		Message: message, Decorate: m.decoratePreviewLine,
	})
}

func (m *Model) renderOutputPreview(width, height int) string {
	box := termpreview.Box{W: width, H: height}
	layout, ok := m.layoutPreviewPanes(box)
	if !ok || len(layout.Leaves) == 0 {
		return m.renderOutputTerminal(width, height)
	}
	canvas := ui.NewCanvas(width, height)
	for _, leaf := range layout.Leaves {
		var content string
		switch leaf.Node.Kind {
		case panelayout.Terminal:
			content = m.renderOutputTerminal(leaf.Box.W, leaf.Box.H)
		case panelayout.Document:
			if m.preview.doc != nil {
				content = m.renderPreviewDoc(m.preview.doc, leaf.Box)
			}
		case panelayout.Issue:
			if m.preview.issue != nil {
				content = m.renderPreviewIssue(m.preview.issue, leaf.Box)
			}
		}
		canvas.Blit(leaf.Box, content)
	}
	focused := m.PreviewFocused() && ((m.preview.doc != nil && m.preview.doc.focused) || (m.preview.issue != nil && m.preview.issue.focused))
	for _, divider := range layout.Dividers {
		canvas.Blit(divider.Box, renderPreviewPaneDivider(divider, focused))
	}
	return canvas.String()
}

func (m *Model) previewHeaderChips(workspace workspaceinventory.Workspace) []string {
	if m.previewTabsVisible() && !m.PreviewInteractive() {
		// Output / Diff / Task, plus Git as a jump (not a tab).
		return m.previewTabRowChips()
	}
	chips := []string{previewChip(workspace.Name, m.PreviewFocused())}
	// While typing, O is a letter; the Git chip is how you jump without exiting.
	// Drop the project-name chip when Git is present so the live-edge window
	// status still fits on the header.
	if m.canOpenInGit() {
		chips = append(chips, gitActionChip())
	} else if workspace.ProjectName != "" {
		chips = append(chips, styles.Muted.Render(workspace.ProjectName))
	}
	return chips
}

func (m *Model) scrollVisiblePreviewTab(delta int) {
	switch {
	case !m.previewTabsVisible() || m.previewTab == workspacediff.TabOutput:
		m.scrollWatchedPreview(delta)
	case m.previewTab == workspacediff.TabDiff:
		m.diff.ScrollContent(delta)
	case m.previewTab == workspacediff.TabTask:
		m.task.Scroll(delta, max(1, m.height-2-previewTabRows))
	}
}

func (m *Model) truncatePreviewLines(content string, maxWidth int) string {
	if maxWidth <= 0 {
		return content
	}
	var b strings.Builder
	start := 0
	for i := 0; i <= len(content); i++ {
		if i == len(content) || content[i] == '\n' {
			line := content[start:i]
			line = ui.ExpandTabs(line, tty.DefaultTabWidth)
			if lipgloss.Width(line) > maxWidth {
				line = termpreview.TruncateANSI(line, maxWidth)
			}
			if start > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(line)
			start = i + 1
		}
	}
	return b.String()
}
