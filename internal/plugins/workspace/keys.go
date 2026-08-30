package workspace

import (
	"fmt"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	appmsg "github.com/marcus/sidecar/internal/msg"

	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
	"github.com/marcus/sidecar/internal/workspacecreate"
	"github.com/marcus/sidecar/internal/workspaceops"
	"github.com/marcus/sidecar/internal/worktreedelete"
)

// handleKeyPress processes key input based on current view mode.
// paneSwitcherKeyName opens the pane switcher from a focused content leaf.
// Every content pane absorbs the keys it does not own — that is what keeps a
// stray key from reaching the workspace behind it — so before this the
// switcher was reachable only from the sidebar or the terminal, and putting a
// second pane beside the one you were reading meant leaving it first. `n` is
// the same key the sidebar and the terminal preview already answer with
// "make me a new thing", so the answer does not change with focus.
//
// The Diff pane spent `n` on next-change before this; that pair moved to
// `<` / `>` (the shifted form of its `,` / `.` file steps) so one key can
// mean one thing in every pane. The global browser binds the same key in the
// same contexts — internal/keymap's parity test is what holds the two to it.
const paneSwitcherKeyName = "n"

// paneSwitcherKey answers paneSwitcherKeyName for a focused content leaf. Each
// pane asks it AFTER its own input surfaces have declined — a live editor, a
// finder overlay, a committed in-file search — because those own every key
// while they are up, `n` included.
func (p *Plugin) paneSwitcherKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	if msg.String() != paneSwitcherKeyName {
		return false, nil
	}
	return true, p.openCreateModalFocusKind()
}

func (p *Plugin) handleKeyPress(msg tea.KeyPressMsg) tea.Cmd {
	if p.paneLayoutModal != nil {
		return p.handlePaneLayoutModalKey(msg)
	}
	switch p.viewMode {
	case ViewModeList, ViewModeKanban:
		return p.handleListKeys(msg)
	case ViewModeCreate:
		return p.handleCreateKeys(msg)
	case ViewModeTaskLink:
		return p.handleTaskLinkKeys(msg)
	case ViewModeMerge:
		return p.handleMergeKeys(msg)
	case ViewModeAgentConfig:
		return p.handleAgentConfigKeys(msg)
	case ViewModeAgentChoice:
		return p.handleAgentChoiceKeys(msg)
	case ViewModeConfirmDelete:
		return p.handleConfirmDeleteKeys(msg)
	case ViewModeConfirmDeleteShell:
		return p.handleConfirmDeleteShellKeys(msg)
	case ViewModeConfirmCloseSplit:
		return p.handleConfirmCloseSplitKeys(msg)
	case ViewModeCommitForMerge:
		return p.handleCommitForMergeKeys(msg)
	case ViewModeRenameShell:
		return p.handleRenameShellKeys(msg)
	case ViewModeRenameWorktree:
		return p.handleRenameWorktreeKeys(msg)
	case ViewModeFetchPR:
		return p.handleFetchPRKeys(msg)
	case ViewModeFilePicker:
		return p.handleFilePickerKeys(msg)
	case ViewModeInteractive:
		return p.handleInteractiveKeys(msg)
	}
	return nil
}

// handleFetchPRKeys handles keys in the fetch PR modal.
func (p *Plugin) handleFetchPRKeys(msg tea.KeyPressMsg) tea.Cmd {
	p.ensureFetchPRModal()
	if p.fetchPRModal == nil {
		return nil
	}

	// Intercept custom keys before delegating to modal
	switch msg.String() {
	case "esc":
		p.viewMode = ViewModeList
		p.clearFetchPRState()
		return nil
	case "enter":
		if p.fetchPRLoading || p.fetchPRError != "" {
			return nil
		}
		filtered := p.filteredFetchPRItems()
		if p.fetchPRCursor >= 0 && p.fetchPRCursor < len(filtered) {
			pr := filtered[p.fetchPRCursor]
			p.fetchPRLoading = true // Show loading while creating worktree
			p.clearFetchPRModal()   // Rebuild to show loading state
			return p.fetchAndCreateWorktree(pr)
		}
		return nil
	case "j", "down":
		filtered := p.filteredFetchPRItems()
		if p.fetchPRCursor < len(filtered)-1 {
			p.fetchPRCursor++
			p.adjustFetchPRScroll()
			p.clearFetchPRModal()
		}
		return nil
	case "k", "up":
		if p.fetchPRCursor > 0 {
			p.fetchPRCursor--
			p.adjustFetchPRScroll()
			p.clearFetchPRModal()
		}
		return nil
	case "backspace":
		if len(p.fetchPRFilter) > 0 {
			p.fetchPRFilter = p.fetchPRFilter[:len(p.fetchPRFilter)-1]
			p.fetchPRCursor = 0
			p.fetchPRScrollOffset = 0
			p.clearFetchPRModal() // Rebuild to reflect filter change
		}
		return nil
	default:
		// Treat printable characters as filter input
		if text := ui.PrintableKeyText(msg); text != "" {
			p.fetchPRFilter += text
			p.fetchPRCursor = 0
			p.fetchPRScrollOffset = 0
			p.clearFetchPRModal() // Rebuild to reflect filter change
		}
		return nil
	}
}

// handleAgentChoiceKeys handles keys in agent choice modal.
func (p *Plugin) handleAgentChoiceKeys(msg tea.KeyPressMsg) tea.Cmd {
	p.ensureAgentChoiceModal()
	if p.agentChoiceModal == nil {
		return nil
	}

	action, cmd := p.agentChoiceModal.HandleKey(msg)

	switch action {
	case "cancel", agentChoiceCancelID:
		p.viewMode = ViewModeList
		p.clearAgentChoiceModal()
		return nil
	case agentChoiceActionID, agentChoiceConfirmID, "agent-choice-attach", "agent-choice-restart":
		return p.executeAgentChoice()
	}

	return cmd
}

// handleAgentConfigKeys handles keys in agent config modal.
func (p *Plugin) handleAgentConfigKeys(msg tea.KeyPressMsg) tea.Cmd {
	p.ensureAgentConfigModal()
	if p.agentConfigModal == nil {
		return nil
	}

	prevAgent := p.agentConfigAgentType
	prevSkip := p.agentConfigSkipPerms
	action, cmd := p.agentConfigModal.HandleKey(msg)
	p.syncAgentConfigFromIdx()
	if p.agentConfigAgentType != prevAgent {
		p.loadAgentConfigAutoApprove()
	} else if p.agentConfigSkipPerms != prevSkip {
		p.persistAgentConfigAutoApprove()
	}

	switch action {
	case "cancel", agentConfigCancelID:
		p.viewMode = ViewModeList
		p.clearAgentConfigModal()
		return nil
	case agentConfigSubmitID:
		return p.executeAgentConfig()
	}

	return cmd
}

// executeAgentConfig executes the agent config modal action (start or restart).
func (p *Plugin) executeAgentConfig() tea.Cmd {
	wt := p.agentConfigWorktree
	agentType := p.agentConfigAgentType
	skipPerms := p.agentConfigSkipPerms
	isRestart := p.agentConfigIsRestart

	p.viewMode = ViewModeList
	p.clearAgentConfigModal()

	if wt == nil {
		return nil
	}

	_ = state.SetLastCreateAgent(string(agentType))
	_ = state.SetAgentAutoApprove(string(agentType), skipPerms)

	if isRestart {
		return tea.Sequence(
			p.StopAgent(wt),
			func() tea.Msg {
				return restartAgentWithOptionsMsg{
					worktree:  wt,
					agentType: agentType,
					skipPerms: skipPerms,
				}
			},
		)
	}
	return p.StartAgentWithOptions(wt, agentType, skipPerms)
}

// executeAgentChoice executes the selected agent choice action.
func (p *Plugin) executeAgentChoice() tea.Cmd {
	wt := p.agentChoiceWorktree
	idx := p.agentChoiceIdx
	p.viewMode = ViewModeList
	p.clearAgentChoiceModal()
	if wt == nil {
		return nil
	}
	items := p.agentChoiceItems()
	if idx >= 0 && idx < len(items) && items[idx].ID == "agent-choice-attach" {
		return p.AttachToSession(wt)
	}
	// Restart agent: open config modal to choose options
	p.openAgentConfigModal(wt, true)
	return nil
}

// handleConfirmDeleteKeys handles keys in delete confirmation modal.
func (p *Plugin) handleConfirmDeleteKeys(msg tea.KeyPressMsg) tea.Cmd {
	outcome, cmd := p.deleteConfirm.HandleKey(p.width, msg)
	switch outcome {
	case worktreedelete.OutcomeCancel:
		return p.cancelDelete()
	case worktreedelete.OutcomeConfirm:
		return p.executeDelete()
	}
	return cmd
}

// executeDelete performs the actual worktree deletion and cleans up state.
func (p *Plugin) executeDelete() tea.Cmd {
	wt := p.deleteConfirmWorktree
	if wt == nil {
		p.viewMode = ViewModeList
		return nil
	}

	name := wt.Name
	path := wt.Path
	branch := wt.Branch
	isMissing := wt.IsMissing
	deleteLocal := p.deleteConfirm.DeleteLocal
	deleteRemote := p.deleteConfirm.DeleteRemoteBranch()
	workDir := p.ctx.WorkDir
	// The owning project, not the current worktree: it is the project's
	// shells.json that records the shells rooted in the worktree being deleted.
	projectRoot := p.ctx.ProjectRoot
	ctx, scope := p.newLifecycleScope(wt)

	// The kill itself belongs to the shared delete path (workspaceops.
	// DeleteWorktree kills the session before it removes the directory), so
	// this surface only drops the state that is its own: the managed-session
	// record and the cached pane.
	sessionName := worktreeTmuxSession(wt)
	delete(p.managedSessions, sessionName)
	globalPaneCache.remove(sessionName)

	// Clear modal state
	p.viewMode = ViewModeList
	p.clearConfirmDeleteModal()

	// Clear preview pane content
	p.resetDiffView()

	return func() tea.Msg {
		var warnings []string

		// The branch tip is pinned before anything is removed, so the branch
		// deleted below is the one this confirmation referred to.
		branchOID := workspaceops.BranchOID(ctx, workDir, branch)

		// Delete the worktree first. Force is stated here and nowhere else:
		// the person reading "Uncommitted changes will be lost" chose Delete.
		// See workspaceops.WorktreeRemoval.Force.
		err := doDeleteWorktreeContext(ctx, workspaceops.WorktreeRemoval{
			RepoPath: workDir, ProjectRoot: projectRoot,
			Path: path, Branch: branch, Missing: isMissing, Force: true,
		})
		if err != nil {
			return DeleteDoneMsg{OperationScope: scope, Name: name, Err: err}
		}

		// Delete local branch if requested
		if deleteLocal {
			if branchErr := deleteBranchContext(ctx, workspaceops.BranchDeletion{
				RepoPath: workDir, Branch: branch, ExpectedOID: branchOID, Force: true,
			}); branchErr != nil {
				warnings = append(warnings, fmt.Sprintf("Local branch: %v", branchErr))
			}
		}

		// Delete remote branch if requested
		if deleteRemote {
			if remoteErr := deleteRemoteBranchCmdContext(ctx, workspaceops.BranchDeletion{
				RepoPath: workDir, Branch: branch,
			}); remoteErr != nil {
				warnings = append(warnings, fmt.Sprintf("Remote branch: %v", remoteErr))
			}
		}

		return DeleteDoneMsg{OperationScope: scope, Name: name, Err: nil, Warnings: warnings}
	}
}

// cancelDelete closes the delete confirmation modal without deleting.
func (p *Plugin) cancelDelete() tea.Cmd {
	p.viewMode = ViewModeList
	p.clearConfirmDeleteModal()
	return nil
}

func (p *Plugin) clearConfirmDeleteModal() {
	p.deleteConfirmWorktree = nil
	p.deleteConfirm.Clear()
}

// handleConfirmDeleteShellKeys handles keys in the shell delete confirmation modal.
func (p *Plugin) handleConfirmDeleteShellKeys(msg tea.KeyPressMsg) tea.Cmd {
	p.ensureConfirmDeleteShellModal()
	if p.deleteShellModal == nil {
		return nil
	}

	switch msg.String() {
	case "D":
		return p.executeShellDelete()
	case "esc", "q":
		return p.cancelShellDelete()
	case "j", "down", "l", "right":
		p.deleteShellModal.HandleKey(tea.KeyPressMsg{Code: tea.KeyTab})
		return nil
	case "k", "up", "h", "left":
		p.deleteShellModal.HandleKey(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
		return nil
	}

	action, cmd := p.deleteShellModal.HandleKey(msg)
	switch action {
	case "cancel", deleteShellConfirmCancelID:
		return p.cancelShellDelete()
	case deleteShellConfirmDeleteID:
		return p.executeShellDelete()
	}
	return cmd
}

// executeShellDelete performs the shell deletion.
func (p *Plugin) executeShellDelete() tea.Cmd {
	shell := p.deleteConfirmShell
	if shell == nil {
		p.viewMode = ViewModeList
		return nil
	}

	sessionName := shell.TmuxName

	// Clear modal state
	p.viewMode = ViewModeList
	p.clearConfirmDeleteShellModal()

	return p.killShellSessionByName(sessionName)
}

// cancelShellDelete closes the shell delete confirmation modal without deleting.
func (p *Plugin) cancelShellDelete() tea.Cmd {
	p.viewMode = ViewModeList
	p.clearConfirmDeleteShellModal()
	return nil
}

func (p *Plugin) clearConfirmDeleteShellModal() {
	p.deleteConfirmShell = nil
	p.deleteShellModal = nil
	p.deleteShellModalWidth = 0
}

// handleListKeys handles keys in list view (and kanban view).
func (p *Plugin) handleListKeys(msg tea.KeyPressMsg) tea.Cmd {
	// The View surface is a modal over the list: while it is open it owns the
	// keyboard, so a stray key cannot fall through to a project command.
	if p.viewFlyoutActive() {
		_, cmd := p.handleViewFlyoutKey(msg)
		return cmd
	}
	// A live pane editor owns every key in the split, Tab included: the ring
	// must not move focus out from under a session the user is typing into.
	if doc := p.focusedDocEdit(); doc != nil {
		_, cmd := p.handleDocEditKey(doc, msg)
		return cmd
	}
	// Clear any deletion warnings on key interaction
	p.deleteWarnings = nil
	// M opens the same transactional reposition modal as the pane-header button.
	// The resolver targets the focused preview leaf or the sidebar row's Primary
	// leaf and declines every text-input/overlay state.
	if handled, cmd := p.handlePaneMoveKey(msg); handled {
		return cmd
	}
	// Tab walks every window on screen — sidebar, tree leaves, terminal panel —
	// through the one ring, so no visible window is a dead end and none is
	// unreachable. A terminal in interactive mode never gets here: that mode
	// dispatches before the list keys and keeps Tab for the shell.
	if msg.String() == "tab" || msg.String() == "shift+tab" {
		// A live pane search owns Tab, exactly as the same surface does in the
		// Files plugin: it moves focus inside the surface — query ↔ results —
		// rather than cycling panes. Letting the ring have it first left the box
		// drawn, cursor and all, over a pane that no longer took keys: a modal
		// that looks focused, is not, and cannot be dismissed.
		if p.docSearchActive() {
			if handled, cmd := p.handleDocKey(msg); handled {
				return cmd
			}
		}
		// Leaving a live terminal search input the way the global overview
		// leaves a focused filter: stop taking keystrokes, keep the query and
		// its matches, then move focus. Without this the search box would keep
		// drawing a cursor for an input that no longer receives keys.
		p.terminalSearch.InputActive = false
		p.cyclePaneFocus(msg.String() == "shift+tab")
		return nil
	}
	// A Workspaces list that is empty because something is not configured yet
	// offers one action, and Enter is it. Nothing else about the list changes:
	// n still creates, and the branch is only reached while the contextual
	// prompt is the thing on screen.
	if msg.String() == "enter" && p.activePane == PaneSidebar && p.setupPromptActive() {
		return p.openSetupCmd()
	}
	if msg.String() == "enter" && p.activePane == PaneSidebar && p.firstRunEmptyActive() {
		return p.openCreateModal()
	}
	if handled, cmd := p.handleDocKey(msg); handled {
		return cmd
	}
	// A focused issue leaf owns the keyboard the same way a focused document
	// does. The two focuses are mutually exclusive, so asking both in turn is
	// one question: which content leaf, if any, the keys belong to.
	if handled, cmd := p.handleIssueKey(msg); handled {
		return cmd
	}
	if handled, cmd := p.handleNoteKey(msg); handled {
		return cmd
	}
	if handled, cmd := p.handleDiffKey(msg); handled {
		return cmd
	}
	if handled, cmd := p.handleResourceKey(msg); handled {
		return cmd
	}
	// A focused list filter owns the keyboard while the sidebar has focus. It is
	// asked after the doc-pane keys deliberately: a focused document keeps its
	// own q/m/+/- context, and the two focuses are mutually exclusive, so
	// neither steals the other's keys.
	if p.filterFocused() && p.activePane == PaneSidebar && !p.docFocused() {
		if handled, cmd := p.handleFilterKey(msg); handled {
			return cmd
		}
	}
	// A key that moves a window puts the surface back on its live buffer: the
	// document projection it may be showing has no window to move. Which keys
	// those are is the shared rule's, so the set here cannot drift from the set
	// that scrolls.
	if p.activePane == PanePreview && !p.shellLeafFocused() && tty.IsScrollbackKey(tty.ScrollbackWatched, msg) {
		p.releaseTerminalDocProjection(false)
	}
	if p.activePane == PanePreview {
		if handled, cmd := p.handleTerminalSearchKey(msg, false); handled {
			return cmd
		}
		config := p.terminalConfig()
		switch {
		case config.IsSelectAllChord(msg):
			p.selectAllTerminalOutput(p.shellLeafVisible() && p.shellLeafFocused())
			return nil
		case config.IsCopyChord(msg):
			return p.copyInteractiveSelectionCmd()
		}
	}

	// The shifted form of a navigation key is the window's and nothing else's
	// here, so it is claimed before the switch below, which dispatches on a key's
	// name and never names those forms. The bare forms still carry this surface's
	// own meanings — the sidebar cursor, the terminal panel's focus — and reach
	// the same rule from inside the switch once those have had their say.
	if tty.IsScrollbackKey(tty.ScrollbackLive, msg) {
		if handled, cmd := p.handleWatchedScrollbackKey(msg); handled {
			return cmd
		}
	}

	switch msg.String() {
	case "/":
		// Explicit filter entry. Only from the sidebar, and never in kanban or
		// from the preview, where `/` belongs to terminal search.
		if p.viewMode != ViewModeKanban && p.activePane == PaneSidebar && !p.docFocused() {
			p.focusListFilter()
			return nil
		}
	case "j", "down":
		if p.viewMode == ViewModeKanban {
			// Kanban mode: move cursor down within column (no selection change)
			p.moveKanbanRow(1)
			return nil
		}
		if p.activePane == PaneSidebar {
			p.moveCursor(1)
			return p.loadSelectedContent()
		}
		// Terminal panel split: switch focus between agent and terminal sub-panes
		// Only applies on Output tab (or shell view) where the terminal panel is rendered
		if p.activePane == PanePreview && p.shellLeafVisible() && !p.shellSplitIsColumns() {
			if !p.shellLeafFocused() {
				p.setShellLeafFocused(true)
				return nil
			}
			// Already at terminal panel (bottom) — scroll it.
		}
		// Scroll down: a terminal window moves towards its live bottom, a
		// document's offset towards the end of its content.
		if handled, cmd := p.handleWatchedScrollbackKey(msg); handled {
			return cmd
		}
		if maxOffset := p.getMaxScrollOffset(); p.previewOffset < maxOffset {
			p.previewOffset++
		}
	case "k", "up":
		if p.viewMode == ViewModeKanban {
			// Kanban mode: move cursor up within column (no selection change)
			p.moveKanbanRow(-1)
			return nil
		}
		if p.activePane == PaneSidebar {
			p.moveCursor(-1)
			return p.loadSelectedContent()
		}
		// Terminal panel split: switch focus between agent and terminal sub-panes
		// Only applies on Output tab (or shell view) where the terminal panel is rendered
		if p.activePane == PanePreview && p.shellLeafVisible() && !p.shellSplitIsColumns() {
			if p.shellLeafFocused() {
				p.setShellLeafFocused(false)
				return nil
			}
			// Already at agent (top) — fall through to scroll agent output
		}
		// Scroll up: a terminal window moves back through scrollback, a
		// document's offset towards the top of its content.
		if handled, cmd := p.handleWatchedScrollbackKey(msg); handled {
			return cmd
		}
		if p.previewOffset > 0 {
			p.previewOffset--
		}
	case "g":
		if p.viewMode == ViewModeKanban {
			// Kanban mode: jump cursor to top of current column
			p.kanbanRow = 0
			return nil
		}
		if p.activePane == PaneSidebar {
			if p.filterActive() {
				p.selectFirstVisible()
				p.scrollOffset = 0
				return p.loadSelectedContent()
			}
			// Jump to top = select first shell if any, otherwise first worktree
			if len(p.shells) > 0 {
				p.selectTopShellAt(0)
				// Exit interactive mode when switching selection (td-fc758e88)
				p.exitInteractiveMode()
				p.saveSelectionState()
			} else if len(p.worktrees) > 0 {
				p.selectWorktreeAt(0)
				// Exit interactive mode when switching selection (td-fc758e88)
				p.exitInteractiveMode()
				p.saveSelectionState()
			}
			p.scrollOffset = 0
			return p.loadSelectedContent()
		}
		// Go to top: the oldest rows the surface holds, and the older ones behind
		// them that the same jump on a live pane reaches for.
		if handled, cmd := p.handleWatchedScrollbackKey(msg); handled {
			return cmd
		}
		p.previewOffset = 0
	case "G":
		if p.viewMode == ViewModeKanban {
			// Kanban mode: jump cursor to bottom of current column
			columns := p.getKanbanColumns()
			count := p.kanbanColumnItemCount(p.kanbanCol, columns)
			if count > 0 {
				p.kanbanRow = count - 1
			}
			return nil
		}
		if p.activePane == PaneSidebar {
			if p.filterActive() {
				p.selectLastVisible()
				return p.loadSelectedContent()
			}
			// Jump to the last visible sidebar row (worktree or nested child).
			if p.sharedSidebarRowCount() > 0 {
				p.selectLastVisible()
				return p.loadSelectedContent()
			}
			return nil
		}
		// Go to bottom: the newest content, which for a terminal is the live
		// edge it follows from.
		if handled, cmd := p.handleWatchedScrollbackKey(msg); handled {
			return cmd
		}
		p.previewOffset = p.getMaxScrollOffset()
	case "home", "end":
		// The jumps a pager's keys make are the same jumps g and G make, and the
		// shared rule maps both forms — so a reader who reaches for home on a
		// watched pane lands where they land on a live one.
		if handled, cmd := p.handleWatchedScrollbackKey(msg); handled {
			return cmd
		}
	case "n":
		return p.openCreateModal()
	case "o":
		// The pane switcher, reachable from the preview without moving focus
		// to the sidebar: same grown create modal, kind list focused. The
		// sidebar's n still opens it name-focused.
		if p.activePane == PanePreview {
			return p.openCreateModalFocusKind()
		}
	case "ctrl+n":
		// Instant create: default agent, auto-approve off. Does not open the form.
		return p.createDefaultShell(false)
	case "d":
		return p.showDiffCmd()
	case "D":
		// Any selected shell answers D, including one nested under a sibling
		// worktree: the row is a shell wherever it is drawn, and reaching it
		// should not require switching into that worktree first.
		if shell := p.getSelectedShell(); shell != nil {
			p.viewMode = ViewModeConfirmDeleteShell
			p.deleteConfirmShell = shell
			p.deleteShellModal = nil
			p.deleteShellModalWidth = 0
			return nil
		}
		// Otherwise delete worktree
		wt := p.selectedWorktree()
		if wt == nil {
			return nil
		}
		if reason := WorktreeActionRefusal(wt, WorktreeActionDelete); reason != "" {
			return appmsg.Blocked(reason)
		}
		p.viewMode = ViewModeConfirmDelete
		p.deleteConfirmWorktree = wt
		p.deleteConfirm.Open(worktreeDeleteTarget(wt), isMainBranch(p.ctx.WorkDir, wt.Branch))
		// Dirtiness is asked for every target, protected branch or not: the
		// warning about losing uncommitted work is what the confirmation is
		// for, and it must not depend on which branch is checked out.
		cmds := []tea.Cmd{p.checkWorktreeDirty(wt)}
		if !p.deleteConfirm.IsMainBranch {
			// Main branch is protected: it gets no branch options, so nothing
			// asks about the remote.
			cmds = append(cmds, p.checkRemoteBranch(wt))
		}
		return tea.Batch(cmds...)
	case "p":
		if wt := p.selectedWorktree(); wt != nil {
			if reason := WorktreeActionRefusal(wt, WorktreeActionPush); reason != "" {
				return appmsg.Blocked(reason)
			}
		}
		return p.pushSelected()
	case "l", "right":
		if p.viewMode == ViewModeKanban {
			// Kanban mode: move cursor to next column (no selection change)
			p.moveKanbanColumn(1)
			return nil
		}
		if p.activePane == PaneSidebar {
			p.activePane = PanePreview
		} else if p.activePane == PanePreview && p.shellLeafVisible() && p.shellSplitIsColumns() && !p.shellLeafFocused() {
			// Right layout: move focus from agent to terminal panel
			p.thawTerminalWindow(true)
			p.setShellLeafFocused(true)
		}
	case "enter":
		// Kanban mode: sync cursor to selection, then fall through to activate
		if p.viewMode == ViewModeKanban {
			oldShellSelected := p.shellSelected
			oldShellIdx := p.selectedShellIdx
			oldWorktreeIdx := p.selectedIdx
			p.syncKanbanToList()
			p.applyKanbanSelectionChange(oldShellSelected, oldShellIdx, oldWorktreeIdx)
		}
		// Terminal panel focused: enter interactive mode for the terminal panel
		if p.shellLeafFocused() && p.shellLeafVisible() {
			if cmd := p.enterTermPanelInteractiveMode(); cmd != nil {
				return cmd
			}
			return nil
		}
		// Enter interactive mode (tmux input passthrough) - feature gated
		if p.activePane == PanePreview || p.activePane == PaneSidebar {
			// Handle orphaned worktrees: start new agent instead of silently returning nil
			if !p.selectingShell() {
				wt := p.selectedWorktree()
				if wt != nil && wt.IsOrphaned && wt.Agent == nil {
					wt.IsOrphaned = false
					agentType := p.resolveWorktreeAgentType(wt)
					return p.StartAgent(wt, agentType)
				}
				if p.activePane == PanePreview && p.startAgentEmptyActive() {
					return p.openStartAgentCreate(wt)
				}
			}
			if cmd := p.enterInteractiveMode(); cmd != nil {
				return cmd
			}
			// Interactive mode couldn't start — at least load content for the selection
			return p.loadSelectedContent()
		}
	case "t":
		if !fullTmuxAttachEnabled() {
			return nil
		}
		// Attach to tmux session
		// Shell entry: attach to selected shell session by TmuxName
		if shell := p.getSelectedShell(); shell != nil {
			return p.ensureShellAndAttach(shell)
		}
		wt := p.selectedWorktree()
		if wt == nil {
			return nil
		}
		// Attach to tmux session if agent running
		if wt.Agent != nil {
			p.attachedSession = wt.Name
			return p.AttachToSession(wt)
		}
		// Orphaned worktree: recover by starting new agent
		if wt.IsOrphaned {
			// Clear flag immediately for UI feedback; also cleared in AgentStartedMsg
			// handler when agent actually starts (StartAgent is async)
			wt.IsOrphaned = false
			agentType := p.resolveWorktreeAgentType(wt)
			return p.StartAgent(wt, agentType)
		}
		// No agent, not orphaned: focus preview
		if p.activePane == PaneSidebar {
			p.activePane = PanePreview
		}
	case "h", "left":
		if p.viewMode == ViewModeKanban {
			// Kanban mode: move cursor to previous column (no selection change)
			p.moveKanbanColumn(-1)
			return nil
		}
		if p.activePane == PanePreview && p.shellLeafVisible() && p.shellSplitIsColumns() && p.shellLeafFocused() {
			// Right layout: move focus from terminal panel back to agent
			p.setShellLeafFocused(false)
			p.releaseTerminalDocProjection(false)
			return nil
		}
		if p.activePane == PanePreview {
			p.setShellLeafFocused(false) // Reset when leaving preview
			p.activePane = PaneSidebar
		}
	case "esc":
		if !p.sidebarVisible {
			p.toggleSidebar()
			return p.resizeSelectedPaneCmd()
		}
		if p.activePane == PanePreview {
			p.setShellLeafFocused(false)
			p.activePane = PaneSidebar
		}
	case "+":
		// Grow sidebar width
		if p.sidebarVisible {
			p.sidebarWidth += 3
			if p.sidebarWidth > 60 {
				p.sidebarWidth = 60
			}
			_ = state.SetWorkspaceSidebarWidth(p.sidebarWidth)
			return p.resizeSelectedPaneCmd()
		}

	case "-":
		// Shrink sidebar width
		if p.sidebarVisible {
			p.sidebarWidth -= 3
			if p.sidebarWidth < 20 {
				p.sidebarWidth = 20
			}
			_ = state.SetWorkspaceSidebarWidth(p.sidebarWidth)
			return p.resizeSelectedPaneCmd()
		}

	case "\\":
		return p.toggleSidebarCmd()
	case "r":
		return func() tea.Msg { return RefreshMsg{} }
	case tty.EnterInteractiveKeyAlt:
		// E is the explicit type key. i is Sidecar's find-TD-task shortcut
		// (td-ba46ea); enter remains the primary way in.
		if p.shellLeafFocused() && p.shellLeafVisible() {
			return p.enterTermPanelInteractiveMode()
		}
		return p.enterInteractiveMode()
	case "v":
		// v opens View, matching the global browser. Kanban moves to V: the
		// two surfaces should not spend their most obvious "view" key on
		// different things, and switching a project to a board is the rarer
		// action of the two.
		if p.activePane == PaneSidebar && p.viewMode == ViewModeList {
			p.openViewFlyout()
			return nil
		}
	case "V":
		if p.activePane == PaneSidebar || p.viewMode == ViewModeKanban {
			switch p.viewMode {
			case ViewModeList:
				p.viewMode = ViewModeKanban
				p.syncListToKanban()
				return p.pollAllAgentStatusesNow()
			case ViewModeKanban:
				p.viewMode = ViewModeList
				return p.pollSelectedAgentNowIfVisible()
			}
		}
	case "ctrl+d", "pgdown":
		// Page down in preview pane (unified: increase offset toward bottom)
		if p.activePane == PanePreview {
			// A terminal surface pages by its own drawn rows, which is the shared
			// rule's business; a document has only this plugin's height to go on.
			if handled, cmd := p.handleWatchedScrollbackKey(msg); handled {
				return cmd
			}
			pageSize := max(p.height/2, 5)
			maxOffset := p.getMaxScrollOffset()
			p.previewOffset += pageSize
			if p.previewOffset > maxOffset {
				p.previewOffset = maxOffset
			}
		}
	case "ctrl+u", "pgup":
		// Page up in preview pane (unified: decrease offset toward top)
		if p.activePane == PanePreview {
			if handled, cmd := p.handleWatchedScrollbackKey(msg); handled {
				return cmd
			}
			pageSize := max(p.height/2, 5)
			p.previewOffset -= pageSize
			if p.previewOffset < 0 {
				p.previewOffset = 0
			}
		}
	// Agent control keys
	case "s":
		// Start agent on selected worktree
		wt := p.selectedWorktree()
		if wt == nil {
			return nil
		}
		if wt.Agent == nil {
			return p.openStartAgentCreate(wt)
		}
		// Agent exists - show choice modal (attach and/or restart)
		p.agentChoiceWorktree = wt
		p.agentChoiceIdx = 0
		p.viewMode = ViewModeAgentChoice
		return nil
	case "S":
		// Stop agent on selected worktree
		wt := p.selectedWorktree()
		if wt != nil && wt.Agent != nil {
			return p.StopAgent(wt)
		}
	case "R":
		// Rename selected shell, or the selected worktree's display name.
		if shell := p.getSelectedShell(); shell != nil {
			p.viewMode = ViewModeRenameShell
			p.renameShellSession = shell
			p.renameShellLeafID = 0
			p.renameShellInput = textinput.New()
			p.renameShellInput.SetValue(shell.Name)
			p.renameShellInput.CharLimit = 50
			p.renameShellInput.SetWidth(30)
			p.renameShellInput.Prompt = ""
			p.renameShellError = ""
			return nil
		}
		if wt := p.selectedWorktree(); wt != nil {
			p.openRenameWorktree(wt)
		}
	case "T":
		// Link/unlink td task
		wt := p.selectedWorktree()
		if wt != nil {
			if wt.TaskID != "" {
				// Already linked - unlink
				return p.unlinkTask(wt)
			}
			// No task linked - show task link modal
			p.viewMode = ViewModeTaskLink
			p.linkingWorktree = wt
			p.taskSearchInput = textinput.New()
			p.taskSearchInput.Placeholder = "Search tasks..."
			p.taskSearchInput.Prompt = ""
			p.taskSearchInput.Focus()
			p.taskSearchInput.CharLimit = 100
			p.taskSearchIdx = 0
			p.taskSearchScroll = 0
			p.taskSearchLoading = true
			p.taskLinkModal = nil
			p.taskLinkModalWidth = 0
			return p.loadOpenTasks()
		}
	case "F":
		// Open a file pane on the fuzzy file finder. F is the file key here for
		// the same reason it is in the Files plugin's neighbourhood: fetching a
		// PR moved to P, which is free and says what it does.
		return p.openFinderPane()
	case "P":
		// Fetch remote PR as workspace
		p.viewMode = ViewModeFetchPR
		p.fetchPRLoading = true
		p.fetchPRFilter = ""
		p.fetchPRCursor = 0
		p.fetchPRError = ""
		return p.fetchPRList()
	case "m":
		// Start merge workflow
		wt := p.selectedWorktree()
		if wt != nil {
			if reason := WorktreeActionRefusal(wt, WorktreeActionMerge); reason != "" {
				return appmsg.Blocked(reason)
			}
			return p.startMergeWorkflow(wt)
		}
	case "O":
		// Open selected worktree in git tab - switch to worktree and focus git plugin
		wt := p.selectedWorktree()
		if wt != nil {
			return p.openInGitTab(wt)
		}
	case "ctrl+t":
		if !terminalPanelEnabled() {
			return nil
		}
		// Toggle a shell split at the remembered axis and ratio.
		return p.toggleTermPanel()
	default:
		// Unhandled key in preview pane - flash to indicate attach is needed
		if fullTmuxAttachEnabled() && p.activePane == PanePreview {
			canAttach := p.selectingShell() || (p.selectedWorktree() != nil && p.selectedWorktree().Agent != nil)
			if canAttach {
				p.flashPreviewTime = time.Now()
			}
		}
	}
	return nil
}

// toggleSidebarCmd is the single non-interactive transition used by list,
// preview and document focus. Keeping its resize/toast side effects together
// prevents one pane from hiding a sidebar it cannot later restore.
func (p *Plugin) toggleSidebarCmd() tea.Cmd {
	p.toggleSidebar()
	resizeCmds := []tea.Cmd{p.resizeSelectedPaneCmd()}
	if p.shellLeafVisible() {
		resizeCmds = append(resizeCmds, p.resizeTermPanelPaneCmd())
	}
	if !p.sidebarVisible {
		// A toggle the user just performed and can see: flash it.
		resizeCmds = append(resizeCmds, appmsg.ShowFlash("Sidebar hidden (\\ to restore)"))
	}
	return tea.Batch(resizeCmds...)
}

// handleCreateKeys handles keys in create modal. Focus is owned by the modal.
func (p *Plugin) handleCreateKeys(msg tea.KeyPressMsg) tea.Cmd {
	if p.createBusyStep != "" {
		return nil
	}
	if p.createPlan != nil {
		p.ensureCreateOperationModal()
		if p.createOperationModal == nil {
			return nil
		}
		action, cmd := p.createOperationModal.HandleKey(msg)
		if action == "cancel" || action == createCancelID {
			if p.createSetupResult != nil {
				return nil
			}
			p.createPlan = nil
			p.createOperationModal = nil
			p.createOperationWidth = 0
			return nil
		}
		if action != "" {
			return p.handleCreateOperationAction(action)
		}
		return cmd
	}
	p.ensureCreateModal()
	if p.createForm == nil {
		return nil
	}

	// The form owns the two-step flow: Esc on the picker step returns to the
	// kind list, and Enter on a target-needing kind advances to it. What
	// escapes is an action for this switch.
	action, cmd := p.createForm.HandleKey(msg)
	p.syncCreateFormAfterInput()

	switch action {
	case createSubmitID:
		return p.submitCreateForm()
	case "cancel", createCancelID:
		p.viewMode = ViewModeList
		p.clearCreateModal()
		return nil
	}
	if workspacecreate.IsPlacementAction(action) {
		return p.createFormPlacementAction(action)
	}

	if action == "" {
		p.setCreateError("")
	}
	return cmd
}

func (p *Plugin) validateAndCreateWorktree() tea.Cmd {
	if p.createBusyStep != "" || p.createPlan != nil {
		return nil
	}
	if p.createForm == nil {
		return nil
	}
	if err := p.createForm.Validate(); err != "" {
		p.setCreateError(err)
		return nil
	}
	p.setCreateError("")
	p.createBusyStep = "Validating branch, source, and destination"
	p.createOperationModal = nil
	return p.resolveCreatePlan()
}

func (p *Plugin) handleCreateOperationAction(action string) tea.Cmd {
	switch action {
	case createConfirmID:
		if p.createSetupResult != nil {
			return nil
		}
		p.createBusyStep = "Creating Git worktree"
		p.createOperationModal = nil
		return p.beginCreateWorktree()
	case createRetrySetupID:
		if p.createSetupResult == nil || p.createSetupResult.Worktree == nil {
			return nil
		}
		p.createBusyStep = "Retrying setup"
		p.createDeleteResult = nil
		p.createOperationModal = nil
		return p.runCreateSetupCmd(p.createPlan, p.createSetupResult.Worktree)
	case createOpenAnywayID:
		return func() tea.Msg { return CreateOpenAnywayMsg{OperationScope: p.currentCreateScope()} }
	case createDeleteCreatedID:
		p.createBusyStep = "Revalidating and deleting newly created worktree"
		p.createDeleteResult = nil
		p.createOperationModal = nil
		return p.deleteNewlyCreatedCmd()
	case createDismissID:
		p.viewMode = ViewModeList
		p.clearCreateModal()
		return nil
	}
	return nil
}

// handleTaskLinkKeys handles keys in task link modal.
func (p *Plugin) handleTaskLinkKeys(msg tea.KeyPressMsg) tea.Cmd {
	p.ensureTaskLinkModal()
	if p.taskLinkModal == nil {
		return nil
	}
	key := msg.String()
	focusID := p.taskLinkModal.FocusedID()
	inputFocused := focusID == "" || focusID == taskLinkFieldID
	switch key {
	case "esc":
		p.closeTaskLinkModal()
		return nil
	case "tab", "shift+tab":
		_, cmd := p.taskLinkModal.HandleKey(msg)
		return cmd
	case "enter":
		action, cmd := p.taskLinkModal.HandleKey(msg)
		if action == "cancel" || action == createCancelID {
			p.closeTaskLinkModal()
			return cmd
		}
		idx := p.taskSearchIdx
		if parsed, ok := parseIndexedID(taskLinkItemPrefix, action); ok {
			idx = parsed
		}
		if idx >= 0 && idx < len(p.taskSearchFiltered) && p.linkingWorktree != nil {
			selectedTask := p.taskSearchFiltered[idx]
			wt := p.linkingWorktree
			p.closeTaskLinkModal()
			return p.linkTask(wt, selectedTask.ID)
		}
		return cmd
	}
	if delta, navigate := taskPickerNavigationDelta(key, inputFocused); navigate {
		p.moveTaskPickerSelection(delta, true, !inputFocused, taskLinkItemPrefix)
		return nil
	}
	if !inputFocused {
		_, cmd := p.taskLinkModal.HandleKey(msg)
		return cmd
	}

	// Delegate to textinput for all other keys (typing, backspace, paste, etc.)
	var cmd tea.Cmd
	oldQuery := p.taskSearchInput.Value()
	p.taskSearchInput, cmd = p.taskSearchInput.Update(msg)
	// Update filtered results on input change
	p.taskSearchFiltered = filterTasks(p.taskSearchInput.Value(), p.taskSearchAll)
	if p.taskSearchInput.Value() != oldQuery {
		p.taskSearchIdx = 0
		p.taskSearchScroll = 0
	}
	return cmd
}

func (p *Plugin) closeTaskLinkModal() {
	p.viewMode = ViewModeList
	p.linkingWorktree = nil
	p.taskSearchInput = textinput.Model{}
	p.taskSearchAll = nil
	p.taskSearchFiltered = nil
	p.taskSearchIdx = 0
	p.taskSearchScroll = 0
	p.taskLinkModal = nil
	p.taskLinkModalWidth = 0
}

// handleMergeKeys handles keys in merge workflow modal.
func (p *Plugin) handleMergeKeys(msg tea.KeyPressMsg) tea.Cmd {
	if p.mergeState == nil {
		p.viewMode = ViewModeList
		return nil
	}

	// Ensure modal is built for key handling
	p.ensureMergeModal()
	if p.mergeState.Step == MergeStepGeneratePR && !p.mergeState.PRGenerationActive {
		switch msg.String() {
		case "d":
			return p.startPRDraft(false)
		case "a":
			return p.startPRDraft(true)
		}
	}
	if p.mergeState.Step == MergeStepEditPR && msg.String() == "ctrl+s" {
		return p.advanceMergeStep()
	}
	if p.mergeState.Step == MergeStepWaitingMerge && msg.String() == "s" {
		p.mergeState.PRWatchStopped = true
		p.clearMergeModal()
		return nil
	}

	// Handle error step — yank, dismiss
	if p.mergeState.Step == MergeStepError {
		switch msg.String() {
		case "y":
			return p.yankMergeErrorToClipboard()
		case "c":
			if p.mergeState.DirectOperation != nil && p.mergeState.DirectOperation.Recovery == DirectMergeRecoveryConflict {
				return p.recoverDirectMerge("continue")
			}
			return nil
		case "a":
			if p.mergeState.DirectOperation != nil && p.mergeState.DirectOperation.Recovery == DirectMergeRecoveryConflict {
				return p.recoverDirectMerge("abort")
			}
			return nil
		case "r":
			if p.mergeState.DirectOperation != nil && p.mergeState.DirectOperation.Recovery == DirectMergeRecoveryPushFailure {
				return p.recoverDirectMerge("retry-push")
			}
			return nil
		case "esc", "q":
			p.cancelMergeWorkflow()
			p.clearMergeModal()
			return nil
		}
		if p.mergeModal != nil {
			action, cmd := p.mergeModal.HandleKey(msg)
			switch action {
			case "continue", "abort", "retry-push":
				return p.recoverDirectMerge(action)
			case "dismiss", "cancel":
				p.cancelMergeWorkflow()
				p.clearMergeModal()
				return nil
			}
			return cmd
		}
		return nil
	}

	// For PostMergeConfirmation step, delegate to modal library for Tab/Enter/Space
	if (p.mergeState.Step == MergeStepGeneratePR || p.mergeState.Step == MergeStepEditPR || p.mergeState.Step == MergeStepWaitingMerge) && p.mergeModal != nil {
		action, cmd := p.mergeModal.HandleKey(msg)
		switch action {
		case mergeFallbackDraftID:
			return p.startPRDraft(false)
		case mergeAgentDraftID:
			return p.startPRDraft(true)
		case mergeCreatePRID:
			return p.advanceMergeStep()
		case "check-pr":
			return p.checkPRMerged(p.mergeState.Worktree)
		case mergeStopWatchingID:
			p.mergeState.PRWatchStopped = true
			p.clearMergeModal()
			return nil
		case "cancel":
			p.cancelMergeWorkflow()
			p.clearMergeModal()
			return nil
		case "":
			if cmd != nil {
				return cmd
			}
		default:
			return cmd
		}
	}

	// For PostMergeConfirmation step, delegate to modal library for Tab/Enter/Space
	if p.mergeState.Step == MergeStepPostMergeConfirmation && p.mergeModal != nil {
		action, cmd := p.mergeModal.HandleKey(msg)
		switch action {
		case "cancel":
			p.cancelMergeWorkflow()
			p.clearMergeModal()
			return nil
		case mergeCleanUpButtonID:
			return p.advanceMergeStep()
		case mergeSkipButtonID:
			p.mergeState.DeleteLocalWorktree = false
			p.mergeState.DeleteLocalBranch = false
			p.mergeState.DeleteRemoteBranch = false
			p.mergeState.PullAfterMerge = false
			return p.advanceMergeStep()
		case "":
			// Modal handled internally (Tab cycling, checkbox toggle, etc.)
			if cmd != nil {
				return cmd
			}
			// Fall through to custom key handling
		default:
			// Unhandled action from modal
			return cmd
		}
	}

	switch msg.String() {
	case "esc", "q":
		p.cancelMergeWorkflow()
		p.clearMergeModal()
		return nil

	case "enter":
		// Continue to next step based on current step
		switch p.mergeState.Step {
		case MergeStepReviewDiff:
			// User reviewed diff, proceed to target branch selection
			return p.advanceMergeStep()
		case MergeStepTargetBranch:
			// User selected target branch, proceed to merge method
			return p.advanceMergeStep()
		case MergeStepMergeMethod:
			// User selected merge method, proceed
			return p.advanceMergeStep()
		case MergeStepWaitingMerge:
			// Manual check for merge status
			return p.checkPRMerged(p.mergeState.Worktree)
		case MergeStepPostMergeConfirmation:
			// Already handled by modal library above
			return nil
		case MergeStepDone:
			// Close modal
			p.cancelMergeWorkflow()
			p.clearMergeModal()
		}

	case "up", "k":
		switch p.mergeState.Step {
		case MergeStepTargetBranch:
			if p.mergeState.TargetBranchOption > 0 {
				p.mergeState.TargetBranchOption--
				p.clearMergeModal()
			}
		case MergeStepMergeMethod:
			// Select PR workflow (option 0)
			p.mergeState.MergeMethodOption = 0
			p.clearMergeModal() // Rebuild with new selection
		case MergeStepWaitingMerge:
			// Select "Delete worktree after merge"
			p.mergeState.DeleteAfterMerge = true
		}

	case "down", "j":
		switch p.mergeState.Step {
		case MergeStepTargetBranch:
			if p.mergeState.TargetBranchOption < len(p.mergeState.TargetBranches)-1 {
				p.mergeState.TargetBranchOption++
				p.clearMergeModal()
			}
		case MergeStepMergeMethod:
			// Select direct merge (option 1)
			p.mergeState.MergeMethodOption = 1
			p.clearMergeModal() // Rebuild with new selection
		case MergeStepWaitingMerge:
			// Select "Keep worktree"
			p.mergeState.DeleteAfterMerge = false
		}

	case "s":
		// Skip current step (for pushing, creating PR)
		switch p.mergeState.Step {
		case MergeStepReviewDiff:
			// Skip push step if already pushed
			p.mergeState.StepStatus[MergeStepReviewDiff] = "done"
			p.mergeState.Step = MergeStepPush
			return p.advanceMergeStep()
		}

	case "o":
		// Open PR in browser (only during WaitingMerge step with a PR URL)
		if p.mergeState.Step == MergeStepWaitingMerge && p.mergeState.PRURL != "" {
			return openInBrowser(p.mergeState.PRURL)
		}

	case "y":
		// Copy PR URL to clipboard (only during WaitingMerge step with a PR URL)
		if p.mergeState.Step == MergeStepWaitingMerge && p.mergeState.PRURL != "" {
			return p.yankPRURLToClipboard()
		}

	case "d":
		// Toggle error details in Done step
		if p.mergeState.Step == MergeStepDone &&
			p.mergeState.CleanupResults != nil &&
			p.mergeState.CleanupResults.PullError != nil {
			p.mergeState.CleanupResults.ShowErrorDetails = !p.mergeState.CleanupResults.ShowErrorDetails
			p.clearMergeModal() // Rebuild with toggled details
		}
		return nil

	case "r":
		// Rebase action (only when branch diverged in Done step)
		if p.mergeState.Step == MergeStepDone &&
			p.mergeState.CleanupResults != nil &&
			p.mergeState.CleanupResults.BranchDiverged {
			return p.executeRebaseResolution()
		}
		return nil

	case "m":
		// Merge action (only when branch diverged in Done step)
		// Note: 'm' in list view starts merge workflow, but here we're in MergeStepDone
		if p.mergeState.Step == MergeStepDone &&
			p.mergeState.CleanupResults != nil &&
			p.mergeState.CleanupResults.BranchDiverged {
			return p.executeMergeResolution()
		}
		return nil
	}
	return nil
}

// handleCommitForMergeKeys handles keys in the commit-before-merge modal.
func (p *Plugin) handleCommitForMergeKeys(msg tea.KeyPressMsg) tea.Cmd {
	p.ensureCommitForMergeModal()
	if p.commitForMergeModal == nil {
		return nil
	}

	// Clear error when input is focused and user types
	if p.commitForMergeModal.FocusedID() == commitForMergeInputID {
		p.mergeCommitState.Error = ""
	}

	action, cmd := p.commitForMergeModal.HandleKey(msg)
	switch action {
	case "cancel", commitForMergeCancelID:
		p.mergeCommitState = nil
		p.mergeCommitMessageInput = textinput.Model{}
		p.clearCommitForMergeModal()
		p.viewMode = ViewModeList
		return nil
	case commitForMergeActionID, commitForMergeCommitID:
		message := p.mergeCommitMessageInput.Value()
		if message == "" {
			p.mergeCommitState.Error = "Commit message cannot be empty"
			return nil
		}
		p.mergeCommitState.Error = ""
		return p.stageAllAndCommit(p.mergeCommitState.Worktree, message)
	}
	return cmd
}

// handleRenameShellKeys handles keys in the rename shell modal.
func (p *Plugin) handleRenameShellKeys(msg tea.KeyPressMsg) tea.Cmd {
	p.ensureRenameShellModal()
	if p.renameShellModal == nil {
		return nil
	}

	// Clear error on typing when input is focused
	if p.renameShellModal.FocusedID() == renameShellInputID {
		p.renameShellError = ""
	}

	action, cmd := p.renameShellModal.HandleKey(msg)

	switch action {
	case "cancel", renameShellCancelID:
		p.viewMode = ViewModeList
		p.clearRenameShellModal()
		return nil
	case renameShellActionID, renameShellRenameID:
		return p.executeRenameShell()
	}

	return cmd
}

// executeRenameShell performs the rename operation.
func (p *Plugin) executeRenameShell() tea.Cmd {
	// A modal opened from a pane title names the leaf, not a manifest shell.
	if p.renameShellLeafTarget() != nil {
		return p.executeRenameShellLeaf()
	}
	if p.renameShellSession == nil {
		return nil
	}
	newName, err := shellstate.NormalizeName(p.renameShellInput.Value())
	if err != nil {
		p.renameShellError = err.Error()
		return nil
	}

	shell := p.renameShellSession
	tmuxName := shell.TmuxName

	return func() tea.Msg {
		if p.shellManifest == nil {
			return RenameShellDoneMsg{TmuxName: tmuxName, NewName: newName, Err: fmt.Errorf("shell manifest is unavailable")}
		}
		result, err := p.shellManifest.RenameShell(tmuxName, shellToDefinition(shell).Namespace, newName)
		return RenameShellDoneMsg{
			TmuxName: tmuxName,
			NewName:  result.Name,
			Err:      err,
		}
	}
}

// clearRenameShellModal clears rename modal state.
func (p *Plugin) clearRenameShellModal() {
	p.renameShellSession = nil
	p.renameShellLeafID = 0
	p.renameShellInput = textinput.Model{}
	p.renameShellModal = nil
	p.renameShellModalWidth = 0
	p.renameShellError = ""
}

func (p *Plugin) openRenameWorktree(wt *Worktree) {
	p.viewMode = ViewModeRenameWorktree
	p.renameWorktree = wt
	p.renameWorktreeInput = textinput.New()
	p.renameWorktreeInput.SetValue(wt.Name)
	p.renameWorktreeInput.CharLimit = shellstate.MaxNameBytes
	p.renameWorktreeInput.SetWidth(30)
	p.renameWorktreeInput.Prompt = ""
	p.renameWorktreeError = ""
	p.renameWorktreeModal = nil
	p.renameWorktreeModalWidth = 0
}

// handleRenameWorktreeKeys handles keys in the rename worktree modal.
func (p *Plugin) handleRenameWorktreeKeys(msg tea.KeyPressMsg) tea.Cmd {
	p.ensureRenameWorktreeModal()
	if p.renameWorktreeModal == nil {
		return nil
	}

	if p.renameWorktreeModal.FocusedID() == renameWorktreeInputID {
		p.renameWorktreeError = ""
	}

	action, cmd := p.renameWorktreeModal.HandleKey(msg)

	switch action {
	case "cancel", renameWorktreeCancelID:
		p.viewMode = ViewModeList
		p.clearRenameWorktreeModal()
		return nil
	case renameWorktreeActionID, renameWorktreeRenameID:
		return p.executeRenameWorktree()
	}

	return cmd
}

// executeRenameWorktree persists a display name. It does not rename the git
// branch, move the directory, or rewrite shells.json.
func (p *Plugin) executeRenameWorktree() tea.Cmd {
	newName, err := shellstate.NormalizeName(p.renameWorktreeInput.Value())
	if err != nil {
		p.renameWorktreeError = err.Error()
		return nil
	}

	wt := p.renameWorktree
	if wt == nil {
		p.renameWorktreeError = "no worktree selected"
		return nil
	}
	projectRoot := ""
	if p.ctx != nil {
		projectRoot = p.ctx.ProjectRoot
	}
	if projectRoot == "" {
		p.renameWorktreeError = "owning project is unavailable"
		return nil
	}
	path := wt.Path
	return func() tea.Msg {
		err := saveDisplayName(projectRoot, path, newName)
		return RenameWorktreeDoneMsg{Path: path, NewName: newName, Err: err}
	}
}

func (p *Plugin) clearRenameWorktreeModal() {
	p.renameWorktree = nil
	p.renameWorktreeInput = textinput.Model{}
	p.renameWorktreeModal = nil
	p.renameWorktreeModalWidth = 0
	p.renameWorktreeError = ""
}

// handleFilePickerKeys handles keys in the file picker modal.
func (p *Plugin) handleFilePickerKeys(msg tea.KeyPressMsg) tea.Cmd {
	view := p.activeDiffView()
	fileCount := view.FileCount()
	if fileCount == 0 {
		p.viewMode = ViewModeList
		return nil
	}

	switch msg.String() {
	case "esc", "q":
		p.viewMode = ViewModeList
		return nil
	case "j", "down":
		p.filePickerIdx++
		if p.filePickerIdx >= fileCount {
			p.filePickerIdx = fileCount - 1
		}
		return nil
	case "k", "up":
		p.filePickerIdx--
		if p.filePickerIdx < 0 {
			p.filePickerIdx = 0
		}
		return nil
	case "g":
		p.filePickerIdx = 0
		return nil
	case "G":
		p.filePickerIdx = fileCount - 1
		return nil
	case "enter":
		var cmd tea.Cmd
		if p.filePickerIdx >= 0 && p.filePickerIdx < fileCount {
			oldCursor := view.Cursor
			view.Cursor = p.filePickerIdx
			cmd = view.OnCursorChanged(oldCursor)
		}
		p.viewMode = ViewModeList
		return cmd
	}
	return nil
}
