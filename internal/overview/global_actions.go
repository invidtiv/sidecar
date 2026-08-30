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
	// DeleteWorktree is the whole teardown: it also forgets and closes the
	// shells rooted in the worktree (td-f017b9), which is why the removal
	// carries ProjectRoot as well as RepoPath.
	execDeleteWorktree     = workspaceops.DeleteWorktree
	execDeleteLocalBranch  = workspaceops.DeleteLocalBranch
	execDeleteRemoteBranch = workspaceops.DeleteRemoteBranch
)

type globalShellDeletedMsg struct {
	Project     Project
	WorkspaceID string
	Err         error
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

// remoteVerbs answers "can a host do this?" for one action.
//
// The entries are the verbs that reach a host as a `sidecar <verb> --json`
// invocation it runs itself, so the machine that owns the state is the machine
// that changes it. Everything absent from this map is refused, and each absence
// is a decision:
//
//   - delete and merge resolve the workspace's path against a git repository
//     and a shells.json. Their implementations run here, on this filesystem,
//     which is the failure this guard exists to prevent.
//   - open (navigation, and the Git plugin jump) switches THIS Sidecar's
//     project to a checkout. There is no checkout here to switch to.
//
// Each of those becomes a supported verb by gaining a host-side CLI verb and an
// entry here — not by relaxing the guard.
//
// Only verbs this map is actually consulted for belong in it. Creation does not
// pass through here at all: it resolves a createTarget from the form and asks
// that whether it is remote, because a create has no selected row to judge.
// Listing "create" and "send" here described a gate nothing opened, and a test
// asserting on them proved only that the map contained what the map contained.
var remoteVerbs = map[string]bool{
	"rename": true,
}

// remoteActionRefusal answers whether a host can do what is being asked of one
// of its workspaces, and says why not when it cannot.
//
// State-free and returning a reason rather than a command, so a headless caller
// can adopt the rule unchanged.
//
// The failure it prevents is not a confusing error. It is an action resolving
// a remote path against THIS machine's filesystem and succeeding, because the
// path happens to exist here too.
func remoteActionRefusal(workspace workspaceinventory.Workspace, verb string) string {
	if !workspace.Remote() || remoteVerbs[verb] {
		return ""
	}
	return fmt.Sprintf("%s is on %s — Sidecar can watch and change a remote workspace, but cannot %s one",
		workspace.Name, workspace.HostID, verb)
}

func (m *Model) OpenDeleteSelectedShell() tea.Cmd {
	workspace, ok := m.SelectedWorkspace()
	if !ok || workspace.Kind != workspaceinventory.KindShell || workspace.TmuxName == "" {
		return nil
	}
	if reason := remoteActionRefusal(workspace, "delete"); reason != "" {
		return appmsg.Blocked(reason)
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
	if reason := remoteActionRefusal(workspace, "delete"); reason != "" {
		return reason
	}
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
		return appmsg.Blocked(reason)
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

// globalWorktreeDeleteProbeMsg carries the git answers the confirmation needs
// but must not block a keypress on: whether the branch is the repository's
// primary one, whether origin still carries it, and whether the worktree holds
// uncommitted work.
type globalWorktreeDeleteProbeMsg struct {
	Path         string
	IsMainBranch bool
	HasRemote    bool
	Dirty        worktreedelete.Dirtiness
}

// probeWorktreeDelete answers the confirmation's open questions in one command.
//
// Dirtiness is asked here, when the modal opens, and not carried on
// workspaceinventory.Workspace beside the porcelain markers. Those markers are
// free — one `git worktree list --porcelain` already reports every worktree's
// bare/detached/locked/prunable state — while dirtiness is `git status` per
// worktree, so putting it in the inventory would add a git spawn per worktree
// per refresh cycle on a surface that refreshes on a timer. AGENTS.md is
// explicit that spawns are expensive on machines running endpoint security
// agents. The user deletes one worktree at a time, so one status call per
// opened confirmation buys the same truth for a bounded, user-initiated cost —
// and it is fresher than a cached inventory field would be.
func (m *Model) probeWorktreeDelete(workspace workspaceinventory.Workspace) tea.Cmd {
	root := m.projectRootFor(workspace)
	path, branch := workspace.Path, workspace.Branch
	// A worktree whose directory is gone has nothing to lose and nothing to
	// ask git about; the confirmation says so on its own line already.
	missing := workspace.IsMissing
	return func() tea.Msg {
		ctx := context.Background()
		msg := globalWorktreeDeleteProbeMsg{Path: path}
		if root != "" {
			msg.IsMainBranch = workspaceops.IsDefaultBranch(ctx, root, branch)
			if !msg.IsMainBranch {
				msg.HasRemote = workspaceops.RemoteBranchExists(ctx, root, branch)
			}
		}
		msg.Dirty = worktreedelete.ProbeDirtiness(ctx, path, missing)
		return msg
	}
}

func (m *Model) applyWorktreeDeleteProbe(msg globalWorktreeDeleteProbeMsg) tea.Cmd {
	if !m.DeletingWorktree() || m.worktreeDelete.Target().Path != msg.Path {
		return nil
	}
	m.worktreeDelete.IsMainBranch = msg.IsMainBranch
	m.worktreeDelete.HasRemote = msg.HasRemote
	m.worktreeDelete.Dirty = msg.Dirty
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
	Project     Project
	WorkspaceID string
	Warnings    []string
	Err         error
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
		// The branch tip is pinned before anything is removed, so the branch
		// deleted below is the one this confirmation referred to.
		branchOID := workspaceops.BranchOID(ctx, project.Path, target.Branch)
		// Force: the person reading "Uncommitted changes will be lost" chose
		// Delete. See workspaceops.WorktreeRemoval.Force.
		if err := execDeleteWorktree(ctx, workspaceops.WorktreeRemoval{
			RepoPath: project.Path, ProjectRoot: project.Path,
			Path: target.Path, Branch: target.Branch,
			Missing: target.IsMissing, Force: true,
		}); err != nil {
			return globalWorktreeDeleteDoneMsg{Project: project, WorkspaceID: workspace.ID, Err: err}
		}
		var warnings []string
		if deleteLocal {
			if err := execDeleteLocalBranch(ctx, workspaceops.BranchDeletion{
				RepoPath: project.Path, Branch: target.Branch, ExpectedOID: branchOID, Force: true,
			}); err != nil {
				warnings = append(warnings, fmt.Sprintf("Local branch: %v", err))
			}
		}
		if deleteRemote {
			if err := execDeleteRemoteBranch(ctx, workspaceops.BranchDeletion{
				RepoPath: project.Path, Branch: target.Branch,
			}); err != nil {
				warnings = append(warnings, fmt.Sprintf("Remote branch: %v", err))
			}
		}
		return globalWorktreeDeleteDoneMsg{Project: project, WorkspaceID: workspace.ID, Warnings: warnings}
	}
}

func (m *Model) applyWorktreeDeleteDone(msg globalWorktreeDeleteDoneMsg) tea.Cmd {
	if msg.Err != nil {
		return appmsg.ShowToast("Delete failed: "+msg.Err.Error(), 4*time.Second)
	}
	m.forgetSessionsRow(msg.WorkspaceID)
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
			return globalShellDeletedMsg{Project: project, WorkspaceID: workspace.ID, Err: deleteManagedShell(project.Path, workspace.TmuxName, workspace.Namespace)}
		}
	}
	return nil
}

func mergeRefusal(workspace workspaceinventory.Workspace) string {
	// The remote clause first, through the shared gate, so the footer stops
	// offering Merge on a row the navigation guard would then refuse — every
	// other unavailable action on a remote row is hidden up front, not offered
	// and taken back.
	if reason := remoteActionRefusal(workspace, "merge"); reason != "" {
		return reason
	}
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
