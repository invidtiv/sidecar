package overview

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/features"
	appmsg "github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/termpreview"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/workspacediff"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

const (
	previewGitRegionKind    = "global-preview-git"
	previewActionRegionKind = "global-preview-action"
	previewDiffDividerKind  = "global-preview-diff-divider"
)

type previewDiffDividerHit struct{}

// previewGitHit marks the Git action chip (a jump, not a tab).
type previewGitHit struct{}

// previewActionHit marks a Diff or Task action chip (a jump, not a tab).
type previewActionHit int

const (
	previewActionDiff previewActionHit = iota
	previewActionTask
)

func gitActionChip() string {
	return styles.RenderPillWithStyle("Git", styles.BarChip, nil)
}

func diffActionChip() string {
	return styles.RenderPillWithStyle("Diff", styles.BarChip, nil)
}

func taskActionChip() string {
	return styles.RenderPillWithStyle("Task", styles.BarChip, nil)
}

func (m *Model) previewActionChips() []string {
	chips := []string{diffActionChip()}
	if workspace, ok := m.SelectedWorkspace(); ok && workspace.TaskID != "" {
		chips = append(chips, taskActionChip())
	}
	return chips
}

func (m *Model) clickPreviewAction(hit previewActionHit) tea.Cmd {
	if m.PreviewInteractive() {
		_ = m.exitPreviewInteractive()
	}
	if !features.IsEnabled(features.WorkspaceDocPanes.Name) {
		return appmsg.ShowToast(features.WorkspaceDocPanesDisabledDiff, 3*time.Second)
	}
	workspace, ok := m.SelectedWorkspace()
	if !ok {
		return nil
	}
	if hit == previewActionTask {
		if workspace.TaskID == "" {
			return nil
		}
		return m.openPreviewIssue(workspace.TaskID)
	}
	return m.openPreviewDiff(workspacediff.WorkingTreeTarget())
}

// previewDiffPath is the checkout workspacediff should read. Shells have no
// worktree of their own; the mini-diff is the project's main checkout.
func previewDiffPath(workspace workspaceinventory.Workspace) string {
	if workspace.Kind == workspaceinventory.KindShell {
		return workspace.ProjectRoot
	}
	return workspace.Path
}

func (m *Model) applyDiffSnapshot(msg workspacediff.SnapshotMsg) tea.Cmd {
	return m.applyPreviewDiffSnapshot(msg)
}

func (m *Model) applyCommitDetail(msg workspacediff.CommitDetailMsg) {
	_ = m.applyPreviewDiffCommit(msg)
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

func (m *Model) registerPreviewActionRegions(box termpreview.Box) {
	if box.W < 1 {
		return
	}
	workspace, ok := m.SelectedWorkspace()
	if !ok {
		return
	}
	chips, actionStart, gitIndex := m.previewHitChips(workspace)
	if len(chips) == 0 {
		return
	}
	hintFloor := 0
	if m.PreviewInteractive() {
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
		if i < actionStart {
			continue
		}
		hit := previewActionDiff
		if i > actionStart {
			hit = previewActionTask
		}
		m.workspacesMouse.HitMap.AddRect(previewActionRegionKind, box.X+placement.Col, box.Y, placement.Width, 1, hit)
	}
}

// previewHitChips is the header chips that have hit targets. Git and Diff/Task
// action chips are registered even while typing, so they can jump without
// sending a letter to the pane. The identity chip is drawn but is not a hit.
func (m *Model) previewHitChips(workspace workspaceinventory.Workspace) (chips []string, actionStart, gitIndex int) {
	chips = m.previewHeaderChips(workspace)
	actionStart = 1
	if workspace.IsMain && workspace.Kind != workspaceinventory.KindShell {
		actionStart = 0
	}
	gitIndex = -1
	if m.canOpenInGit() {
		gitIndex = len(chips) - 1
	}
	return chips, actionStart, gitIndex
}

func (m *Model) renderPreviewWithTabs(width, height int) string {
	return m.renderOutputPreview(width, height)
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

// renderOutputPreview draws the terminal leaf's body. Placing and framing the
// leaves is the shared frame's job — see renderPreviewPeer — so what is left
// here is one content: the terminal that has always filled this box.
func (m *Model) renderOutputPreview(width, height int) string {
	return m.renderOutputTerminal(width, height)
}

func (m *Model) previewHeaderChips(workspace workspaceinventory.Workspace) []string {
	chips := []string{previewChip(workspace.Name, m.PreviewFocused())}
	if workspace.IsMain && workspace.Kind != workspaceinventory.KindShell {
		chips = m.previewActionChips()
	} else {
		chips = append(chips, m.previewActionChips()...)
	}
	if m.canOpenInGit() {
		chips = append(chips, gitActionChip())
	} else if workspace.ProjectName != "" && (!workspace.IsMain || workspace.Kind == workspaceinventory.KindShell) {
		chips = append(chips, styles.Muted.Render(workspace.ProjectName))
	}
	return chips
}
