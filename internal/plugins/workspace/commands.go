package workspace

import (
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/terminallink"
)

// Commands returns the available commands.
func (p *Plugin) Commands() []plugin.Command {
	if p.viewMode == ViewModeList && p.docSearchActive() {
		return []plugin.Command{
			{ID: "search-open", Name: "Open", Description: "Open the selected file in this pane", Context: "workspace-doc-search", Priority: 1},
			{ID: "search-open-tab", Name: "Tab+", Description: "Open the selected file in a new tab", Context: "workspace-doc-search", Priority: 2},
			{ID: "search-cancel", Name: "Cancel", Description: "Close the search and return to the document", Context: "workspace-doc-search", Priority: 3},
		}
	}
	if p.viewMode == ViewModeList && p.docFocused() {
		cmds := []plugin.Command{
			{ID: "close", Name: "Close", Description: "Hide document pane", Context: "workspace-doc", Priority: 1},
			{ID: "find-file", Name: "Find", Description: "Find a file by name in this pane", Context: "workspace-doc", Priority: 2},
			{ID: "search-project", Name: "Search", Description: "Search the project in this pane", Context: "workspace-doc", Priority: 3},
			{ID: "close-tab", Name: "Tab×", Description: "Close active file", Context: "workspace-doc", Priority: 4},
			{ID: "prev-tab", Name: "Tab←", Description: "Previous file tab", Context: "workspace-doc", Priority: 5},
			{ID: "next-tab", Name: "Tab→", Description: "Next file tab", Context: "workspace-doc", Priority: 6},
			{ID: "toggle-sidebar", Name: "Sidebar", Description: "Toggle sidebar visibility", Context: "workspace-doc", Priority: 7},
		}
		if doc, _ := p.activeDocPane(); doc != nil && doc.view() != nil && terminallink.Markdown(doc.view().Title()) {
			renderName := "Raw"
			if !doc.view().Rendered() {
				renderName = "Render"
			}
			cmds = append(cmds, plugin.Command{ID: "render", Name: renderName, Description: "Toggle rendered and raw markdown", Context: "workspace-doc", Priority: 8})
		}
		cmds = append(cmds,
			plugin.Command{ID: "toggle-wrap", Name: "Wrap", Description: "Toggle line wrapping", Context: "workspace-doc", Priority: 9},
			plugin.Command{ID: "info", Name: "Info", Description: "Show file info", Context: "workspace-doc", Priority: 10},
			plugin.Command{ID: "reveal", Name: "Reveal", Description: "Reveal in file manager", Context: "workspace-doc", Priority: 11},
			plugin.Command{ID: "resize-pane-grow", Name: "Grow", Description: "Grow document pane", Context: "workspace-doc", Priority: 12},
			plugin.Command{ID: "resize-pane-shrink", Name: "Shrink", Description: "Shrink document pane", Context: "workspace-doc", Priority: 13},
			plugin.Command{ID: "next-pane", Name: "Focus", Description: "Focus next pane", Context: "workspace-doc", Priority: 14},
			plugin.Command{ID: "prev-pane", Name: "Back", Description: "Focus previous pane", Context: "workspace-doc", Priority: 15},
		)
		return cmds
	}
	if p.viewMode == ViewModeList && p.issueFocused() {
		return []plugin.Command{
			{ID: "open-item", Name: "Open", Description: "Open selected parent or subtask", Context: "workspace-issue", Priority: 1},
			{ID: "close-tab", Name: "Tab×", Description: "Close active issue", Context: "workspace-issue", Priority: 2},
			{ID: "prev-tab", Name: "Tab←", Description: "Previous issue tab", Context: "workspace-issue", Priority: 3},
			{ID: "next-tab", Name: "Tab→", Description: "Next issue tab", Context: "workspace-issue", Priority: 4},
			{ID: "yank-issue", Name: "Yank", Description: "Copy issue as markdown", Context: "workspace-issue", Priority: 5},
			{ID: "yank-issue-key", Name: "YankID", Description: "Copy issue ID", Context: "workspace-issue", Priority: 6},
			{ID: "close", Name: "Close", Description: "Hide issue pane", Context: "workspace-issue", Priority: 7},
			{ID: "toggle-sidebar", Name: "Sidebar", Description: "Toggle sidebar visibility", Context: "workspace-issue", Priority: 8},
			{ID: "next-pane", Name: "Focus", Description: "Focus next pane", Context: "workspace-issue", Priority: 9},
			{ID: "prev-pane", Name: "Back", Description: "Focus previous pane", Context: "workspace-issue", Priority: 10},
		}
	}
	if p.viewMode == ViewModeList && p.diffFocused() {
		cmds := []plugin.Command{
			{ID: "close", Name: "Close", Description: "Hide diff pane", Context: "workspace-diff", Priority: 1},
			{ID: "close-tab", Name: "Tab×", Description: "Close active diff tab", Context: "workspace-diff", Priority: 2},
			{ID: "prev-tab", Name: "Tab←", Description: "Previous diff tab", Context: "workspace-diff", Priority: 3},
			{ID: "next-tab", Name: "Tab→", Description: "Next diff tab", Context: "workspace-diff", Priority: 4},
			{ID: "yank-id", Name: "YankID", Description: "Copy target identity", Context: "workspace-diff", Priority: 5},
			{ID: "toggle-sidebar", Name: "Sidebar", Description: "Toggle sidebar visibility", Context: "workspace-diff", Priority: 6},
			{ID: "resize-pane-grow", Name: "Grow", Description: "Grow diff pane", Context: "workspace-diff", Priority: 7},
			{ID: "resize-pane-shrink", Name: "Shrink", Description: "Shrink diff pane", Context: "workspace-diff", Priority: 8},
			{ID: "next-pane", Name: "Focus", Description: "Focus next pane", Context: "workspace-diff", Priority: 9},
			{ID: "prev-pane", Name: "Back", Description: "Focus previous pane", Context: "workspace-diff", Priority: 10},
		}
		if view := p.activeDiffView(); view != nil {
			cmds = append(cmds, view.Commands("workspace-diff")...)
		}
		return cmds
	}
	switch p.viewMode {
	case ViewModeInteractive:
		return []plugin.Command{
			{ID: "exit-interactive", Name: "Exit", Description: "Exit interactive mode (" + p.getInteractiveExitKey() + ")", Context: "workspace-interactive", Priority: 1},
			{ID: "copy", Name: "Copy", Description: "Copy selection (" + p.getInteractiveCopyKey() + " or " + superCopyKey + ")", Context: "workspace-interactive", Priority: 2},
			{ID: "paste", Name: "Paste", Description: "Paste clipboard (" + p.getInteractivePasteKey() + ")", Context: "workspace-interactive", Priority: 3},
		}
	case ViewModeCreate:
		if p.createBusyStep != "" {
			return []plugin.Command{{ID: "creation-busy", Name: "Working", Description: p.createBusyStep, Context: "workspace-create-busy", Priority: 1}}
		}
		if p.createSetupResult != nil {
			if p.createDeleteResult != nil && p.createDeleteResult.WorktreeRemoved {
				return []plugin.Command{{ID: createDismissID, Name: "Dismiss", Description: "Close cleanup result", Context: "workspace-create-recovery", Priority: 1}}
			}
			return []plugin.Command{
				{ID: createRetrySetupID, Name: "Retry", Description: "Retry setup", Context: "workspace-create-recovery", Priority: 1},
				{ID: createOpenAnywayID, Name: "Open", Description: "Open without successful setup", Context: "workspace-create-recovery", Priority: 2},
				{ID: createDeleteCreatedID, Name: "Delete", Description: "Delete newly created worktree", Context: "workspace-create-recovery", Priority: 3},
			}
		}
		if p.createPlan != nil {
			return []plugin.Command{
				{ID: createConfirmID, Name: "Create", Description: "Create the confirmed worktree", Context: "workspace-create-confirm", Priority: 1},
				{ID: "cancel", Name: "Back", Description: "Return to creation form", Context: "workspace-create-confirm", Priority: 2},
			}
		}
		return []plugin.Command{
			{ID: "cancel", Name: "Cancel", Description: "Cancel workspace creation", Context: "workspace-create", Priority: 1},
			{ID: "confirm", Name: "Create", Description: "Create the workspace", Context: "workspace-create", Priority: 2},
			{ID: "navigate-picker", Name: "Navigate", Description: "Move through branch or task results", Context: "workspace-create", Priority: 3},
		}
	case ViewModeTaskLink:
		return []plugin.Command{
			{ID: "cancel", Name: "Cancel", Description: "Cancel task linking", Context: "workspace-task-link", Priority: 1},
			{ID: "select-task", Name: "Select", Description: "Link selected task", Context: "workspace-task-link", Priority: 2},
			{ID: "navigate-picker", Name: "Navigate", Description: "Move through task results", Context: "workspace-task-link", Priority: 3},
		}
	case ViewModeMerge:
		if p.mergeState != nil && p.mergeState.Step == MergeStepError {
			cmds := []plugin.Command{
				{ID: "dismiss-merge-error", Name: "Dismiss", Description: "Dismiss error", Context: "workspace-merge-error", Priority: 1},
				{ID: "yank-merge-error", Name: "Yank", Description: "Copy error to clipboard", Context: "workspace-merge-error", Priority: 2},
			}
			if op := p.mergeState.DirectOperation; op != nil {
				switch op.Recovery {
				case DirectMergeRecoveryConflict:
					cmds = append([]plugin.Command{
						{ID: "continue-merge", Name: "Continue", Description: "Continue after resolving conflicts", Context: "workspace-merge-error", Priority: 1},
						{ID: "abort-merge", Name: "Abort", Description: "Abort and restore target", Context: "workspace-merge-error", Priority: 2},
					}, cmds...)
				case DirectMergeRecoveryPushFailure:
					cmds = append([]plugin.Command{{ID: "retry-push", Name: "Retry", Description: "Retry push", Context: "workspace-merge-error", Priority: 1}}, cmds...)
				}
			}
			return cmds
		}
		cmds := []plugin.Command{
			{ID: "cancel", Name: "Cancel", Description: "Cancel merge workflow", Context: "workspace-merge", Priority: 1},
		}
		if p.mergeState != nil {
			switch p.mergeState.Step {
			case MergeStepReviewDiff:
				cmds = append(cmds, plugin.Command{ID: "continue", Name: "Push", Description: "Push branch", Context: "workspace-merge", Priority: 2})
			case MergeStepWaitingMerge:
				cmds = append(cmds, plugin.Command{ID: "continue", Name: "Check", Description: "Check merge status", Context: "workspace-merge", Priority: 2})
				cmds = append(cmds, plugin.Command{ID: mergeStopWatchingID, Name: "Stop", Description: "Stop watching and keep PR URL", Context: "workspace-merge", Priority: 3})
				cmds = append(cmds, plugin.Command{ID: "open-pr", Name: "Open", Description: "Open PR in browser", Context: "workspace-merge", Priority: 4})
				cmds = append(cmds, plugin.Command{ID: "copy-pr", Name: "Copy", Description: "Copy PR URL", Context: "workspace-merge", Priority: 5})
			case MergeStepGeneratePR:
				cmds = append(cmds,
					plugin.Command{ID: mergeFallbackDraftID, Name: "Draft", Description: "Use local commit summary", Context: "workspace-merge", Priority: 2},
					plugin.Command{ID: mergeAgentDraftID, Name: "Agent", Description: "Send capped diff to configured agent provider", Context: "workspace-merge", Priority: 3},
				)
			case MergeStepEditPR:
				cmds = append(cmds, plugin.Command{ID: mergeCreatePRID, Name: "Create", Description: "Create PR with edited title and body (Ctrl+S)", Context: "workspace-merge", Priority: 2})
			case MergeStepDone:
				cmds = append(cmds, plugin.Command{ID: "continue", Name: "Done", Description: "Close modal", Context: "workspace-merge", Priority: 2})
			}
		}
		return cmds
	case ViewModeAgentConfig:
		return []plugin.Command{
			{ID: "cancel", Name: "Cancel", Description: "Cancel agent config", Context: "workspace-agent-config", Priority: 1},
			{ID: "confirm", Name: "Start", Description: "Start agent with config", Context: "workspace-agent-config", Priority: 2},
		}
	case ViewModeAgentChoice:
		return []plugin.Command{
			{ID: "cancel", Name: "Cancel", Description: "Cancel agent choice", Context: "workspace-agent-choice", Priority: 1},
			{ID: "select", Name: "Select", Description: "Choose selected option", Context: "workspace-agent-choice", Priority: 2},
		}
	case ViewModeConfirmDelete:
		return []plugin.Command{
			{ID: "cancel", Name: "Cancel", Description: "Cancel deletion", Context: "workspace-confirm-delete", Priority: 1},
			{ID: "delete", Name: "Delete", Description: "Confirm deletion", Context: "workspace-confirm-delete", Priority: 2},
		}
	case ViewModeConfirmDeleteShell:
		return []plugin.Command{
			{ID: "cancel", Name: "Cancel", Description: "Cancel deletion", Context: "workspace-confirm-delete-shell", Priority: 1},
			{ID: "delete", Name: "Delete", Description: "Terminate shell", Context: "workspace-confirm-delete-shell", Priority: 2},
		}
	case ViewModeCommitForMerge:
		return []plugin.Command{
			{ID: "cancel", Name: "Cancel", Description: "Cancel merge", Context: "workspace-commit-for-merge", Priority: 1},
			{ID: "commit", Name: "Commit", Description: "Commit and continue", Context: "workspace-commit-for-merge", Priority: 2},
		}
	case ViewModeRenameShell:
		return []plugin.Command{
			{ID: "cancel", Name: "Cancel", Description: "Cancel rename", Context: "workspace-rename-shell", Priority: 1},
			{ID: "confirm", Name: "Rename", Description: "Confirm new name", Context: "workspace-rename-shell", Priority: 2},
		}
	case ViewModeRenameWorktree:
		return []plugin.Command{
			{ID: "cancel", Name: "Cancel", Description: "Cancel rename", Context: "workspace-rename-worktree", Priority: 1},
			{ID: "confirm", Name: "Rename", Description: "Confirm new name", Context: "workspace-rename-worktree", Priority: 2},
		}
	case ViewModeFetchPR:
		return []plugin.Command{
			{ID: "cancel", Name: "Cancel", Description: "Cancel PR fetch", Context: "workspace-fetch-pr", Priority: 1},
			{ID: "fetch", Name: "Fetch", Description: "Fetch selected PR", Context: "workspace-fetch-pr", Priority: 2},
		}
	case ViewModeFilePicker:
		return []plugin.Command{
			{ID: "cancel", Name: "Cancel", Description: "Close file picker", Context: "workspace-file-picker", Priority: 1},
			{ID: "select", Name: "Jump", Description: "Jump to selected file", Context: "workspace-file-picker", Priority: 2},
		}
	default:
		// View toggle label changes based on current mode
		viewToggleName := "Kanban"
		if p.viewMode == ViewModeKanban {
			viewToggleName = "List"
		}

		// Return different commands based on active pane
		if p.activePane == PanePreview {
			// Preview pane commands
			cmds := []plugin.Command{
				{ID: "switch-pane", Name: "Focus", Description: "Switch to sidebar", Context: "workspace-preview", Priority: 1},
				{ID: "toggle-sidebar", Name: "Sidebar", Description: "Toggle sidebar visibility", Context: "workspace-preview", Priority: 2},
			}
			// Tab commands only shown when a worktree is selected (not shell)
			// Shell has no tabs - it shows primer/output directly
			cmds = append(cmds,
				plugin.Command{ID: "show-diff", Name: "Diff", Description: "Open working-tree diff pane", Context: "workspace-preview", Priority: 3},
			)
			// Also show agent commands in preview pane
			wt := p.selectedWorktree()
			if wt != nil {
				if wt.Agent == nil {
					cmds = append(cmds,
						plugin.Command{ID: "start-agent", Name: "Start", Description: "Start agent", Context: "workspace-preview", Priority: 10},
					)
				} else {
					cmds = append(cmds,
						plugin.Command{ID: "start-agent", Name: "Agent", Description: "Agent options (attach/restart)", Context: "workspace-preview", Priority: 9},
						plugin.Command{ID: "stop-agent", Name: "Stop", Description: "Stop agent", Context: "workspace-preview", Priority: 11},
					)
					if fullTmuxAttachEnabled() {
						cmds = append(cmds, plugin.Command{ID: "attach", Name: "Attach", Description: "Attach to session", Context: "workspace-preview", Priority: 10})
					}
					if wt.Status == StatusWaiting {
						cmds = append(cmds,
							plugin.Command{ID: "approve", Name: "Approve", Description: "Approve agent prompt", Context: "workspace-preview", Priority: 12},
							plugin.Command{ID: "reject", Name: "Reject", Description: "Reject agent prompt", Context: "workspace-preview", Priority: 13},
						)
					}
				}
			}
			// Show interactive mode hint when feature enabled and session active
			// Workspace: needs agent and Output tab; Shell: always shows output
			if features.IsEnabled(features.TmuxInteractiveInput.Name) {
				hasActiveSession := false
				if p.selectingShell() {
					if shell := p.getSelectedShell(); shell != nil && shell.Agent != nil {
						hasActiveSession = true
					}
				} else if wt != nil && wt.Agent != nil {
					hasActiveSession = true
				}
				if hasActiveSession {
					cmds = append(cmds,
						plugin.Command{ID: "interactive", Name: "Type", Description: "Enter interactive mode (E)", Context: "workspace-preview", Priority: 15},
					)
				}
			}
			// Terminal panel toggle (show on Output tab when an agent or shell is active)
			if terminalPanelEnabled() {
				termName := "Term"
				if p.termPanelVisible {
					termName = "Hide"
				}
				cmds = append(cmds,
					plugin.Command{ID: "toggle-terminal", Name: termName, Description: "Toggle terminal panel", Context: "workspace-preview", Priority: 16},
				)
				if p.termPanelVisible {
					layoutName := "Right"
					if p.termPanelLayout == TermPanelRight {
						layoutName = "Bottom"
					}
					cmds = append(cmds,
						plugin.Command{ID: "switch-terminal-layout", Name: layoutName, Description: "Switch terminal layout", Context: "workspace-preview", Priority: 17},
					)
				}
			}
			return cmds
		}

		// Filter focus is its own context: while a query is being typed the only
		// commands that apply are the ones that end or accept it.
		if p.filterFocused() && p.activePane == PaneSidebar {
			return []plugin.Command{
				{ID: "filter-accept", Name: "Select", Description: "Keep the selected match and return to the list", Context: "workspace-filter", Priority: 1},
				{ID: "filter-clear", Name: "Clear", Description: "Clear the query, then exit the filter", Context: "workspace-filter", Priority: 2},
			}
		}

		// Sidebar list commands - reorganized with unique priorities
		// Priority 1-4: Base commands (always visible)
		// Priority 5-8: Worktree-specific commands
		// Priority 10-14: Agent commands (highest visibility when applicable)
		cmds := []plugin.Command{
			{ID: "new-workspace", Name: "New", Description: "Create new workspace", Context: "workspace-list", Priority: 1},
			{ID: "new-shell", Name: "Shell", Description: "Create new shell session", Context: "workspace-list", Priority: 2},
			{ID: "fetch-pr", Name: "Fetch", Description: "Fetch remote PR as workspace", Context: "workspace-list", Priority: 3},
			{ID: "toggle-view", Name: viewToggleName, Description: "Toggle list/kanban view", Context: "workspace-list", Priority: 4},
			{ID: "toggle-sidebar", Name: "Sidebar", Description: "Toggle sidebar visibility", Context: "workspace-list", Priority: 5},
			{ID: "refresh", Name: "Refresh", Description: "Refresh workspace list", Context: "workspace-list", Priority: 6},
			{ID: "filter-list", Name: "Filter", Description: "Filter workspaces by name, branch, task, agent, or status", Context: "workspace-list", Priority: 7},
			{ID: "show-diff", Name: "Diff", Description: "Open working-tree diff pane", Context: "workspace-list", Priority: 8},
		}

		// F opens a document pane, and kanban draws no pane tree, so the key is
		// only real in list view (see openFinderPane). A footer hint for a key
		// that does nothing is worse than no hint: the fix is to stop
		// advertising it where it cannot work, not to make kanban silently
		// switch views out from under the board.
		if p.viewMode == ViewModeList {
			// Priority 8 was this command's home until the Diff pane took it;
			// 9-16 are the agent and worktree blocks. 17 keeps the ordering
			// deterministic without renumbering them — see the merge note in
			// the commit message if Find should sit beside Diff instead.
			cmds = append(cmds, plugin.Command{ID: "find-file", Name: "Find", Description: "Open a file pane on the file finder", Context: "workspace-list", Priority: 17})
		}

		// Shell-specific commands when shell is selected
		if p.selectingShell() {
			shell := p.getSelectedShell()
			if fullTmuxAttachEnabled() {
				name := "Attach"
				desc := "Create and attach to shell"
				if shell != nil && shell.Agent != nil {
					desc = "Attach to shell"
				}
				cmds = append(cmds, plugin.Command{ID: "attach-shell", Name: name, Description: desc, Context: "workspace-list", Priority: 10})
			}
			if shell != nil && shell.Agent != nil {
				cmds = append(cmds, plugin.Command{ID: "delete-workspace", Name: "Delete", Description: "Delete selected shell", Context: "workspace-list", Priority: 11})
			}
			cmds = append(cmds, plugin.Command{ID: "rename-shell", Name: "Rename", Description: "Rename shell", Context: "workspace-list", Priority: 12})
			return cmds
		}

		wt := p.selectedWorktree()
		if wt != nil {
			// Agent commands first (most context-dependent, highest visibility)
			if wt.Agent == nil {
				cmds = append(cmds,
					plugin.Command{ID: "start-agent", Name: "Start", Description: "Start agent", Context: "workspace-list", Priority: 10},
				)
			} else {
				cmds = append(cmds,
					plugin.Command{ID: "start-agent", Name: "Agent", Description: "Agent options (attach/restart)", Context: "workspace-list", Priority: 9},
					plugin.Command{ID: "stop-agent", Name: "Stop", Description: "Stop agent", Context: "workspace-list", Priority: 11},
				)
				if fullTmuxAttachEnabled() {
					cmds = append(cmds, plugin.Command{ID: "attach", Name: "Attach", Description: "Attach to session", Context: "workspace-list", Priority: 10})
				}
				if wt.Status == StatusWaiting {
					cmds = append(cmds,
						plugin.Command{ID: "approve", Name: "Approve", Description: "Approve agent prompt", Context: "workspace-list", Priority: 12},
						plugin.Command{ID: "reject", Name: "Reject", Description: "Reject agent prompt", Context: "workspace-list", Priority: 13},
						plugin.Command{ID: "approve-all", Name: "Approve All", Description: "Approve all agent prompts", Context: "workspace-list", Priority: 14},
					)
				}
			}
			// Only advertise mutating actions that are safe for this worktree.
			if WorktreeActionRefusal(wt, WorktreeActionDelete) == "" {
				cmds = append(cmds, plugin.Command{ID: "delete-workspace", Name: "Delete", Description: "Delete selected workspace", Context: "workspace-list", Priority: 5})
			}
			if WorktreeActionRefusal(wt, WorktreeActionPush) == "" {
				cmds = append(cmds, plugin.Command{ID: "push", Name: "Push", Description: "Push branch to remote", Context: "workspace-list", Priority: 6})
			}
			if WorktreeActionRefusal(wt, WorktreeActionMerge) == "" {
				cmds = append(cmds, plugin.Command{ID: "merge-workflow", Name: "Merge", Description: "Start merge workflow", Context: "workspace-list", Priority: 7})
			}
			cmds = append(cmds, plugin.Command{ID: "rename-worktree", Name: "Rename", Description: "Rename worktree", Context: "workspace-list", Priority: 12})
			cmds = append(cmds, plugin.Command{ID: "open-in-git", Name: "Git", Description: "Open in Git tab", Context: "workspace-list", Priority: 16})
			// Task linking
			if wt.TaskID != "" {
				cmds = append(cmds,
					plugin.Command{ID: "link-task", Name: "Unlink", Description: "Unlink task", Context: "workspace-list", Priority: 15},
				)
			} else {
				cmds = append(cmds,
					plugin.Command{ID: "link-task", Name: "Task", Description: "Link task", Context: "workspace-list", Priority: 15},
				)
			}
		}
		if terminalPanelEnabled() {
			termName := "Term"
			if p.termPanelVisible {
				termName = "Hide"
			}
			cmds = append(cmds,
				plugin.Command{ID: "toggle-terminal", Name: termName, Description: "Toggle terminal panel", Context: "workspace-list", Priority: 17},
			)
			if p.termPanelVisible {
				layoutName := "Right"
				if p.termPanelLayout == TermPanelRight {
					layoutName = "Bottom"
				}
				cmds = append(cmds,
					plugin.Command{ID: "switch-terminal-layout", Name: layoutName, Description: "Switch terminal layout", Context: "workspace-list", Priority: 18},
				)
			}
		}
		return cmds
	}
}

// FocusContext returns the current focus context for keybinding dispatch.
func (p *Plugin) FocusContext() string {
	switch p.viewMode {
	case ViewModeInteractive:
		return "workspace-interactive"
	case ViewModeCreate:
		if p.createBusyStep != "" {
			return "workspace-create-busy"
		}
		if p.createSetupResult != nil {
			return "workspace-create-recovery"
		}
		if p.createPlan != nil {
			return "workspace-create-confirm"
		}
		return "workspace-create"
	case ViewModeTaskLink:
		return "workspace-task-link"
	case ViewModeMerge:
		if p.mergeState != nil && p.mergeState.Step == MergeStepError {
			return "workspace-merge-error"
		}
		return "workspace-merge"
	case ViewModeAgentConfig:
		return "workspace-agent-config"
	case ViewModeAgentChoice:
		return "workspace-agent-choice"
	case ViewModeConfirmDelete:
		return "workspace-confirm-delete"
	case ViewModeConfirmDeleteShell:
		return "workspace-confirm-delete-shell"
	case ViewModeCommitForMerge:
		return "workspace-commit-for-merge"
	case ViewModeRenameShell:
		return "workspace-rename-shell"
	case ViewModeRenameWorktree:
		return "workspace-rename-worktree"
	case ViewModeTypeSelector:
		return "workspace-type-selector"
	case ViewModeFetchPR:
		return "workspace-fetch-pr"
	case ViewModeFilePicker:
		return "workspace-file-picker"
	default:
		// A pane-scoped search is its own text-input context: while a query has
		// focus the document's keys — and the host's root-context q — must not
		// take printable characters from it.
		if p.docSearchActive() {
			return "workspace-doc-search"
		}
		if p.docFocused() {
			return "workspace-doc"
		}
		// A focused issue leaf is its own context for the same reason: falling
		// through to workspace-preview would hand the terminal's keys — and the
		// host's root-context `q` quits Sidecar — to a pane drawn as focused.
		if p.issueFocused() {
			return "workspace-issue"
		}
		if p.diffFocused() {
			return "workspace-diff"
		}
		if p.filterFocused() && p.activePane == PaneSidebar {
			// A dedicated text-input context: while the query has focus, app
			// shortcuts must not take printable characters or pastes from it.
			return "workspace-filter"
		}
		if p.activePane == PanePreview {
			return "workspace-preview"
		}
		return "workspace-list"
	}
}

// ConsumesTextInput reports whether the workspace plugin is currently in a
// mode that expects typed text input.
func (p *Plugin) ConsumesTextInput() bool {
	if p.docSearchActive() {
		return true
	}
	if p.filterFocused() && p.activePane == PaneSidebar && !p.docFocused() {
		return true
	}
	switch p.viewMode {
	case ViewModeInteractive,
		ViewModeCreate,
		ViewModeTaskLink,
		ViewModeCommitForMerge,
		ViewModeRenameShell,
		ViewModeRenameWorktree,
		ViewModeTypeSelector,
		ViewModeFetchPR:
		return true
	case ViewModeMerge:
		return p.mergeState != nil && p.mergeState.Step == MergeStepEditPR
	default:
		return false
	}
}

// BlocksGlobalKeys reports whether a plugin-owned modal has keyboard focus.
func (p *Plugin) BlocksGlobalKeys() bool {
	if p.docInfo != nil {
		return true
	}
	// A pane-scoped search surface is a modal with the keyboard, box or no box.
	if p.docSearchActive() {
		return true
	}
	return p.viewMode != ViewModeList && p.viewMode != ViewModeKanban && p.viewMode != ViewModeInteractive
}
