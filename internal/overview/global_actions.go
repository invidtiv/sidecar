package overview

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/modal"
	appmsg "github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/ui"
	"github.com/marcus/sidecar/internal/workspaceinventory"
	"github.com/marcus/sidecar/internal/workspaceops"
	"github.com/marcus/sidecar/internal/worktreedelete"
)

const (
	globalDeleteConfirmID = "global-delete-confirm"
	globalDeleteCancelID  = "global-delete-cancel"
)

var (
	deleteManagedShell = workspaceops.DeleteManagedShell
	// The worktree delete path is the same one the project surface runs.
	// Indirection is here so tests can execute the flow without touching a
	// real repository.
	execDeleteWorktree     = workspaceops.DeleteWorktree
	execDeleteLocalBranch  = workspaceops.DeleteLocalBranch
	execDeleteRemoteBranch = workspaceops.DeleteRemoteBranch
)

type globalShellDeletedMsg struct {
	Project Project
	Err     error
}

func (m *Model) DeleteOpen() bool { return m.deleteOpen }

// DeletingWorktree reports that the open confirmation is the shared
// "Delete Worktree?" one rather than this surface's shell confirmation.
func (m *Model) DeletingWorktree() bool {
	return m.deleteOpen && m.deleteWorkspace.Kind == workspaceinventory.KindWorktree
}

func (m *Model) RunDeleteCommand(id string) tea.Cmd {
	if !m.deleteOpen {
		return nil
	}
	if m.DeletingWorktree() {
		switch id {
		case "confirm-delete":
			return m.applyWorktreeDeleteOutcome(worktreedelete.OutcomeConfirm)
		case "cancel":
			return m.applyWorktreeDeleteOutcome(worktreedelete.OutcomeCancel)
		}
		return nil
	}
	switch id {
	case "confirm-delete":
		return m.applyDeleteAction(globalDeleteConfirmID)
	case "cancel":
		return m.applyDeleteAction(globalDeleteCancelID)
	default:
		return nil
	}
}

func (m *Model) OpenDeleteSelectedShell() tea.Cmd {
	workspace, ok := m.SelectedWorkspace()
	if !ok || workspace.Kind != workspaceinventory.KindShell || workspace.TmuxName == "" {
		return nil
	}
	m.deleteOpen = true
	m.deleteBusy = false
	m.deleteError = ""
	m.deleteWorkspace = workspace
	m.deleteModal = nil
	m.deleteModalW = 0
	// Exactly one confirmation is ever armed.
	m.worktreeDelete.Clear()
	return nil
}

// worktreeActionState is the full refusal input for a catalog row. Every
// marker git reported is carried through the inventory, so this surface
// refuses exactly the set the project surface refuses — a locked worktree is
// not offered a confirmation it would then fail to honour (td-2af16d).
func worktreeActionState(workspace workspaceinventory.Workspace) *workspaceops.WorktreeActionState {
	return &workspaceops.WorktreeActionState{
		Path: workspace.Path, Branch: workspace.Branch,
		IsMain: workspace.IsMain, IsBare: workspace.IsBare,
		IsDetached: workspace.IsDetached, IsLocked: workspace.IsLocked,
		IsMissing: workspace.IsMissing, IsPrunable: workspace.IsPrunable,
		// The inventory has just stat'ed the path; a second stat per frame
		// would answer the same question the collector already answered.
		TrustPath: true,
	}
}

// deleteRefusal is the shared refusal for deleting the selected worktree —
// the same presentation-neutral rules the project surface applies.
func deleteRefusal(workspace workspaceinventory.Workspace) string {
	if workspace.Kind != workspaceinventory.KindWorktree {
		return "delete requires a worktree"
	}
	return workspaceops.WorktreeActionRefusal(worktreeActionState(workspace), workspaceops.WorktreeActionDelete)
}

// OpenDeleteSelectedWorktree raises the shared "Delete Worktree?" confirmation
// for the selected worktree. The modal, its branch cleanup options, and its
// input routing are internal/worktreedelete — the project surface's modal,
// not a copy of it.
func (m *Model) OpenDeleteSelectedWorktree() tea.Cmd {
	workspace, ok := m.SelectedWorkspace()
	if !ok || workspace.Kind != workspaceinventory.KindWorktree {
		return nil
	}
	if reason := deleteRefusal(workspace); reason != "" {
		return appmsg.ShowToast(reason, 3*time.Second)
	}
	m.deleteOpen = true
	m.deleteBusy = false
	m.deleteError = ""
	m.deleteWorkspace = workspace
	m.deleteModal = nil
	m.deleteModalW = 0
	m.worktreeDelete.Open(worktreedelete.Target{
		Name:      workspace.Name,
		Branch:    workspace.Branch,
		Path:      workspace.Path,
		IsMissing: workspace.IsMissing,
	}, false)
	return m.probeWorktreeDelete(workspace)
}

// globalWorktreeDeleteProbeMsg carries the two git answers the confirmation
// needs but must not block a keypress on: whether the branch is the
// repository's primary one, and whether origin still carries it.
type globalWorktreeDeleteProbeMsg struct {
	Path         string
	IsMainBranch bool
	HasRemote    bool
}

func (m *Model) probeWorktreeDelete(workspace workspaceinventory.Workspace) tea.Cmd {
	root := m.projectRootFor(workspace)
	if root == "" {
		return nil
	}
	path, branch := workspace.Path, workspace.Branch
	return func() tea.Msg {
		ctx := context.Background()
		isMain := workspaceops.IsDefaultBranch(ctx, root, branch)
		hasRemote := false
		if !isMain {
			hasRemote = workspaceops.RemoteBranchExists(ctx, root, branch)
		}
		return globalWorktreeDeleteProbeMsg{Path: path, IsMainBranch: isMain, HasRemote: hasRemote}
	}
}

func (m *Model) applyWorktreeDeleteProbe(msg globalWorktreeDeleteProbeMsg) tea.Cmd {
	if !m.DeletingWorktree() || m.worktreeDelete.Target().Path != msg.Path {
		return nil
	}
	m.worktreeDelete.IsMainBranch = msg.IsMainBranch
	m.worktreeDelete.HasRemote = msg.HasRemote
	if msg.IsMainBranch {
		// The options are no longer on screen, so the intent behind them must
		// not survive either.
		m.worktreeDelete.DeleteLocal = false
		m.worktreeDelete.DeleteRemote = false
	}
	m.worktreeDelete.Invalidate()
	return nil
}

// projectRootFor is the repository the worktree belongs to — the working
// directory every git call in the delete path runs in.
func (m *Model) projectRootFor(workspace workspaceinventory.Workspace) string {
	if idx := m.projectIndex(workspace.ProjectKey); idx >= 0 {
		return m.projects[idx].Path
	}
	return workspace.ProjectRoot
}

type globalWorktreeDeleteDoneMsg struct {
	Project  Project
	Warnings []string
	Err      error
}

func (m *Model) applyWorktreeDeleteOutcome(outcome worktreedelete.Outcome) tea.Cmd {
	switch outcome {
	case worktreedelete.OutcomeCancel:
		m.closeDelete()
	case worktreedelete.OutcomeConfirm:
		return m.executeWorktreeDelete()
	}
	return nil
}

// executeWorktreeDelete runs the shared workspaceops delete path. The
// confirmation closes first, as it does on the project surface: the list is the
// rest state, and the result arrives as a toast.
func (m *Model) executeWorktreeDelete() tea.Cmd {
	workspace := m.deleteWorkspace
	idx := m.projectIndex(workspace.ProjectKey)
	if idx < 0 {
		m.closeDelete()
		return appmsg.ShowToast("Owning project is no longer configured", 3*time.Second)
	}
	project := m.projects[idx]
	target := m.worktreeDelete.Target()
	deleteLocal := m.worktreeDelete.DeleteLocal
	deleteRemote := m.worktreeDelete.DeleteRemoteBranch()
	m.closeDelete()

	return func() tea.Msg {
		ctx := context.Background()
		if err := execDeleteWorktree(ctx, project.Path, target.Path, target.IsMissing); err != nil {
			return globalWorktreeDeleteDoneMsg{Project: project, Err: err}
		}
		var warnings []string
		if deleteLocal {
			if err := execDeleteLocalBranch(ctx, project.Path, target.Branch); err != nil {
				warnings = append(warnings, fmt.Sprintf("Local branch: %v", err))
			}
		}
		if deleteRemote {
			if err := execDeleteRemoteBranch(ctx, project.Path, target.Branch); err != nil {
				warnings = append(warnings, fmt.Sprintf("Remote branch: %v", err))
			}
		}
		return globalWorktreeDeleteDoneMsg{Project: project, Warnings: warnings}
	}
}

func (m *Model) applyWorktreeDeleteDone(msg globalWorktreeDeleteDoneMsg) tea.Cmd {
	if msg.Err != nil {
		return appmsg.ShowToast("Delete failed: "+msg.Err.Error(), 4*time.Second)
	}
	cmds := []tea.Cmd{m.refreshProjectAfterMutation(msg.Project)}
	if len(msg.Warnings) > 0 {
		cmds = append(cmds, appmsg.ShowToast(strings.Join(msg.Warnings, "; "), 4*time.Second))
	}
	return tea.Batch(cmds...)
}

func (m *Model) ensureDeleteModal() {
	if !m.deleteOpen {
		return
	}
	width := 50
	if m.width > 0 && width > m.width-4 {
		width = m.width - 4
	}
	if width < 24 {
		width = 24
	}
	if m.deleteModal != nil && m.deleteModalW == width {
		return
	}
	m.deleteModalW = width
	m.deleteModal = modal.New("Delete Shell", modal.WithWidth(width), modal.WithPrimaryAction(globalDeleteConfirmID))
	m.deleteModal.AddSection(modal.Text(fmt.Sprintf("Delete %s?\n\nThis closes its tmux session and removes its Sidecar identity.", m.deleteWorkspace.Name)))
	if m.deleteError != "" {
		m.deleteModal.AddSection(modal.Spacer())
		m.deleteModal.AddSection(modal.Text("Error: " + m.deleteError))
	}
	if m.deleteBusy {
		m.deleteModal.AddSection(modal.Spacer())
		m.deleteModal.AddSection(modal.Text("Deleting shell…"))
	}
	m.deleteModal.AddSection(modal.Spacer())
	m.deleteModal.AddSection(modal.Buttons(modal.Btn(" Delete ", globalDeleteConfirmID, modal.BtnDanger()), modal.Btn(" Cancel ", globalDeleteCancelID)))
}

func (m *Model) overlayDelete(background string, width, height int) string {
	if m.DeletingWorktree() {
		built := m.worktreeDelete.Modal(m.width)
		if built == nil {
			return background
		}
		return ui.OverlayModal(background, built.Render(width, height, m.deleteMouse), width, height)
	}
	m.ensureDeleteModal()
	if m.deleteModal == nil {
		return background
	}
	return ui.OverlayModal(background, m.deleteModal.Render(width, height, m.deleteMouse), width, height)
}

func (m *Model) closeDelete() {
	m.deleteOpen, m.deleteBusy = false, false
	m.deleteError = ""
	m.deleteModal = nil
	m.deleteModalW = 0
	m.worktreeDelete.Clear()
}

func (m *Model) handleDeleteKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	if m.DeletingWorktree() {
		outcome, cmd := m.worktreeDelete.HandleKey(m.width, msg)
		return true, tea.Batch(cmd, m.applyWorktreeDeleteOutcome(outcome))
	}
	m.ensureDeleteModal()
	if m.deleteModal == nil || m.deleteBusy {
		return true, nil
	}
	action, cmd := m.deleteModal.HandleKey(msg)
	return true, tea.Batch(cmd, m.applyDeleteAction(action))
}

func (m *Model) handleDeleteMouse(msg tea.MouseMsg) tea.Cmd {
	if m.DeletingWorktree() {
		return m.applyWorktreeDeleteOutcome(m.worktreeDelete.HandleMouse(m.width, msg, m.deleteMouse))
	}
	m.ensureDeleteModal()
	if m.deleteModal == nil || m.deleteBusy {
		return nil
	}
	return m.applyDeleteAction(m.deleteModal.HandleMouse(msg, m.deleteMouse))
}

func (m *Model) applyDeleteAction(action string) tea.Cmd {
	switch action {
	case "cancel", globalDeleteCancelID:
		m.closeDelete()
	case globalDeleteConfirmID:
		workspace := m.deleteWorkspace
		idx := m.projectIndex(workspace.ProjectKey)
		if idx < 0 {
			m.deleteError = "Owning project is no longer configured"
			m.deleteModal = nil
			return nil
		}
		project := m.projects[idx]
		m.deleteBusy = true
		m.deleteModal = nil
		return func() tea.Msg {
			return globalShellDeletedMsg{Project: project, Err: deleteManagedShell(project.Path, workspace.TmuxName, workspace.Namespace)}
		}
	}
	return nil
}

func mergeRefusal(workspace workspaceinventory.Workspace) string {
	if workspace.Kind != workspaceinventory.KindWorktree {
		return "merge requires a worktree"
	}
	return workspaceops.WorktreeActionRefusal(worktreeActionState(workspace), workspaceops.WorktreeActionMerge)
}

func (m *Model) StartSelectedMerge() tea.Cmd {
	workspace, ok := m.SelectedWorkspace()
	if !ok || mergeRefusal(workspace) != "" {
		return nil
	}
	// Validation and project navigation preserve the selected identity, then
	// the owning project opens its existing PR/direct strategy workflow.
	return m.RequestNavigationAction(workspace, "merge")
}
