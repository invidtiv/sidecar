package overview

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/issueview"
	"github.com/marcus/sidecar/internal/mouse"
	appmsg "github.com/marcus/sidecar/internal/msg"
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
}

// previewIssueLoadedMsg adds the selected-workspace generation to issueview's
// own model/request/epoch identity. Both must still match before a result can
// paint: the component protects retargets within one workspace, while this
// wrapper protects selection and visibility changes around it.
type previewIssueLoadedMsg struct {
	issueview.LoadedMsg
	Generation  int
	WorkspaceID string
}

func (m *Model) openPreviewIssue(issueID string) tea.Cmd {
	workspace, ok := m.SelectedWorkspace()
	if !ok || issueID == "" || workspace.Path == "" {
		return nil
	}
	box, hasBox := m.previewBox()
	if !hasBox || !m.previewSecondaryFits(box) {
		return appmsg.ShowToast("Issue pane needs a wider window; terminal left unchanged", 3*time.Second)
	}

	wasInteractive := m.PreviewInteractive()
	m.preview.doc = nil
	if m.preview.issue == nil {
		m.preview.issue = &previewIssue{view: issueview.New(nil)}
	}
	issue := m.preview.issue
	issue.root = workspace.Path
	issue.surface = workspace.ID
	issue.focused = true
	issue.view.SetActive(true)
	issue.view.SetFocused(true)
	load := issue.view.Load(previewIssueModelID, issue.root, issueID, uint64(m.preview.generation))
	m.preview.focus = focusPreview

	var cmds []tea.Cmd
	if wasInteractive {
		cmds = append(cmds, m.exitPreviewInteractive())
	}
	cmds = append(cmds,
		wrapPreviewIssueLoad(load, m.preview.generation, workspace.ID),
		m.syncTerminalGeometry(),
	)
	return tea.Batch(cmds...)
}

func wrapPreviewIssueLoad(cmd tea.Cmd, generation int, workspaceID string) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		msg := cmd()
		if loaded, ok := msg.(issueview.LoadedMsg); ok {
			return previewIssueLoadedMsg{
				LoadedMsg: loaded, Generation: generation, WorkspaceID: workspaceID,
			}
		}
		return msg
	}
}

func (m *Model) applyPreviewIssueLoaded(msg previewIssueLoadedMsg) {
	issue := m.preview.issue
	if issue == nil || issue.view == nil ||
		msg.Generation != m.preview.generation ||
		msg.WorkspaceID != m.preview.workspaceID ||
		issue.surface != msg.WorkspaceID {
		return
	}
	issue.view.SetResult(msg.LoadedMsg)
}

func (m *Model) closePreviewIssue() tea.Cmd {
	if m.preview.issue == nil {
		return nil
	}
	m.preview.issue = nil
	return m.syncTerminalGeometry()
}

func previewIssueHeaderChips(issue *previewIssue, width int) []string {
	title := issue.view.Title()
	title = termpreview.TruncateANSI(title, max(width-6, 8))
	style := styles.BarChip
	if issue.focused {
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
	issue.view.SetFocused(issue.focused)
	header := termpreview.HeaderRow(
		previewIssueHeaderChips(issue, box.W),
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
	_, issueBox, split := m.previewSecondaryLayout(box)
	if !split {
		return
	}
	m.workspacesMouse.HitMap.AddRect(
		previewIssueRegionKind,
		issueBox.X, issueBox.Y, issueBox.W, issueBox.H,
		previewIssueRegionKind,
	)
	chips := previewIssueHeaderChips(issue, issueBox.W)
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
		issue.focused = true
		m.preview.focus = focusPreview
		lx := action.X - action.Region.Rect.X
		ly := action.Y - action.Region.Rect.Y - termpreview.HeaderRows
		_, cmd := issue.view.HandleClick(lx, ly)
		return wrapPreviewIssueLoad(cmd, m.preview.generation, issue.surface)
	case mouse.ActionDoubleClick:
		// The preceding click already navigated. Consume Bubble Tea's
		// follow-up double event so a child that has just rendered its parent
		// at this cell cannot immediately navigate back.
		issue.focused = true
		m.preview.focus = focusPreview
		return nil
	case mouse.ActionScrollUp, mouse.ActionScrollDown:
		issue.view.Scroll(action.Delta)
	}
	return nil
}

func (m *Model) previewIssueKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	issue := m.preview.issue
	if issue == nil || m.PreviewInteractive() {
		return false, nil
	}
	switch msg.String() {
	case "q", "esc":
		return true, m.closePreviewIssue()
	}
	issue.focused = true
	issue.view.SetActive(true)
	issue.view.SetFocused(true)
	handled, cmd := issue.view.HandleKey(msg)
	if handled {
		return true, wrapPreviewIssueLoad(cmd, m.preview.generation, issue.surface)
	}
	// A focused issue is its own input context. Do not let an unowned key
	// refresh, navigate, or type into the terminal behind the visible card.
	return true, nil
}
