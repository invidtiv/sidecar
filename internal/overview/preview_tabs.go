package overview

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/termpreview"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
	"github.com/marcus/sidecar/internal/workspacediff"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

const (
	previewTabRegionKind = "global-preview-tab"
	previewTabRows       = 2
)

// previewTabHit is the chip index stored on the tab-row region.
type previewTabHit int

// previewTabsVisible is the same rule as the project plugin: chips only for a
// selected non-main worktree. Shells and the main worktree have no tab row.
func (m *Model) previewTabsVisible() bool {
	workspace, ok := m.SelectedWorkspace()
	if !ok {
		return false
	}
	return workspacediff.TabsVisible(workspace.Kind == workspaceinventory.KindShell, workspace.IsMain)
}

func (m *Model) cyclePreviewTab(delta int) tea.Cmd {
	if !m.previewTabsVisible() {
		return nil
	}
	m.previewTab = workspacediff.CycleTab(m.previewTab, delta)
	return m.ensurePreviewExtras()
}

func (m *Model) setPreviewTab(tab workspacediff.Tab) tea.Cmd {
	if !m.previewTabsVisible() {
		m.previewTab = workspacediff.TabOutput
		return nil
	}
	if m.previewTab == tab {
		return m.ensurePreviewExtras()
	}
	m.previewTab = tab
	return m.ensurePreviewExtras()
}

// ensureOutputTab is what Enter uses: Diff/Task are views of the row, so
// typing always happens on Output.
func (m *Model) ensureOutputTab() tea.Cmd {
	if m.previewTab == workspacediff.TabOutput || !m.previewTabsVisible() {
		return nil
	}
	m.previewTab = workspacediff.TabOutput
	return nil
}

// ensurePreviewExtras loads Diff/Task for the selected worktree. Switching
// rows rebuilds the model; the same row is a no-op if already loaded.
func (m *Model) ensurePreviewExtras() tea.Cmd {
	workspace, ok := m.SelectedWorkspace()
	if !ok || !m.previewTabsVisible() {
		m.resetPreviewExtras()
		m.previewTab = workspacediff.TabOutput
		return nil
	}
	if workspace.ID != m.previewExtrasID {
		m.resetPreviewExtras()
		m.previewExtrasID = workspace.ID
	}
	switch m.previewTab {
	case workspacediff.TabDiff:
		if m.diff.State != workspacediff.LoadStateUnknown && m.diff.State != workspacediff.LoadStateError {
			return m.diff.LoadSelectedCommit(workspace.Path, workspace.ID)
		}
		m.diff.State = workspacediff.LoadStateLoading
		return workspacediff.LoadSnapshotCmd(workspace.Path, "", workspace.ID)
	case workspacediff.TabTask:
		return m.loadPreviewTask(workspace)
	}
	return nil
}

func (m *Model) resetPreviewExtras() {
	m.diff = workspacediff.View{}
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
	if msg.Err != nil {
		m.diff.State = workspacediff.LoadStateError
		m.diff.Error = msg.Err.Error()
		return nil
	}
	return m.diff.ApplyLoadedSnapshot(msg.Snapshot, workspace.Path, workspace.ID)
}

func (m *Model) applyCommitDetail(msg workspacediff.CommitDetailMsg) {
	workspace, ok := m.SelectedWorkspace()
	if !ok || workspace.ID != msg.WorkspaceID {
		return
	}
	m.diff.ApplyCommitDetail(msg)
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

func (m *Model) registerPreviewTabRegions(box termpreview.Box) {
	if !m.previewTabsVisible() || m.PreviewInteractive() || box.W < 1 {
		return
	}
	hintFloor := 0
	if m.previewTab == workspacediff.TabOutput && m.PreviewInteractive() {
		hintFloor = len([]rune(m.interactiveHints()))
	}
	for i, placement := range termpreview.LayoutChips(workspacediff.TabChips(m.previewTab), box.W, hintFloor) {
		if !placement.Drawn {
			continue
		}
		m.workspacesMouse.HitMap.AddRect(previewTabRegionKind, box.X+placement.Col, box.Y, placement.Width, 1, previewTabHit(i))
	}
}

func (m *Model) renderPreviewWithTabs(width, height int) string {
	if width < 1 || height < 1 {
		return ""
	}
	if !m.previewTabsVisible() || m.previewTab == workspacediff.TabOutput {
		return m.renderOutputPreview(width, height)
	}

	var lines []string
	lines = append(lines, termpreview.HeaderRow(workspacediff.TabChips(m.previewTab), "", width, 0, termpreview.TruncateANSI))
	lines = append(lines, "")
	contentHeight := height - previewTabRows
	if contentHeight < 1 {
		contentHeight = 1
	}
	switch m.previewTab {
	case workspacediff.TabDiff:
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

func (m *Model) renderOutputPreview(width, height int) string {
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
		Message: message,
	})
}

func (m *Model) previewHeaderChips(workspace workspaceinventory.Workspace) []string {
	if m.previewTabsVisible() && !m.PreviewInteractive() {
		// Same chips as the project plugin: Output / Diff / Task. While
		// typing, the chips yield so the header can still name the live edge.
		return workspacediff.TabChips(m.previewTab)
	}
	chips := []string{previewChip(workspace.Name, m.PreviewFocused())}
	if workspace.ProjectName != "" {
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
