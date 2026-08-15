package overview

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/ui"
	"github.com/marcus/sidecar/internal/workspaceinventory"
	"github.com/marcus/sidecar/internal/workspaceops"
)

const (
	globalDeleteConfirmID = "global-delete-confirm"
	globalDeleteCancelID  = "global-delete-cancel"
)

var deleteManagedShell = workspaceops.DeleteManagedShell

type globalShellDeletedMsg struct {
	Project Project
	Err     error
}

func (m *Model) DeleteOpen() bool { return m.deleteOpen }

func (m *Model) RunDeleteCommand(id string) tea.Cmd {
	if !m.deleteOpen {
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
	return nil
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
}

func (m *Model) handleDeleteKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	m.ensureDeleteModal()
	if m.deleteModal == nil || m.deleteBusy {
		return true, nil
	}
	action, cmd := m.deleteModal.HandleKey(msg)
	return true, tea.Batch(cmd, m.applyDeleteAction(action))
}

func (m *Model) handleDeleteMouse(msg tea.MouseMsg) tea.Cmd {
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
	return workspaceops.WorktreeActionRefusal(&workspaceops.WorktreeActionState{
		Path: workspace.Path, Branch: workspace.Branch, IsMain: workspace.IsMain, TrustPath: true,
	}, workspaceops.WorktreeActionMerge)
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
