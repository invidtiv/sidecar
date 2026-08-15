package overview

import (
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

const (
	ctxGlobalWorkspaces         = "global-workspaces"
	ctxGlobalWorkspacesFilter   = "global-workspaces-filter"
	ctxGlobalWorkspacesRename   = "global-workspaces-rename"
	ctxGlobalWorkspacesCreate   = "global-workspaces-create"
	ctxGlobalWorkspacesTerminal = "global-workspaces-terminal"
	ctxGlobalWorkspacesDoc      = "global-workspaces-doc"
	ctxGlobalWorkspacesIssue    = "global-workspaces-issue"
	ctxGlobalWorkspacesDiff     = "global-workspaces-diff"
)

// Commands is the footer and palette metadata for the active
// WorkspaceFocusContext. Bindings live in the keymap; names and priorities
// live here so a focused issue or document cannot advertise the list's keys.
func (m *Model) Commands() []plugin.Command {
	switch m.WorkspaceFocusContext() {
	case ctxGlobalWorkspacesFilter:
		return []plugin.Command{
			{ID: "filter-accept", Name: "Select", Description: "Keep the selected match and return to the list", Context: ctxGlobalWorkspacesFilter, Priority: 1},
			{ID: "filter-clear", Name: "Clear", Description: "Clear the query, then exit the filter", Context: ctxGlobalWorkspacesFilter, Priority: 2},
		}
	case ctxGlobalWorkspacesRename:
		return []plugin.Command{
			{ID: "confirm", Name: "Rename", Description: "Confirm the new display name", Context: ctxGlobalWorkspacesRename, Priority: 1},
			{ID: "cancel", Name: "Cancel", Description: "Close the rename prompt", Context: ctxGlobalWorkspacesRename, Priority: 2},
		}
	case ctxGlobalWorkspacesCreate:
		return []plugin.Command{
			{ID: "confirm", Name: "Create", Description: "Create the shell in the chosen project", Context: ctxGlobalWorkspacesCreate, Priority: 1},
			{ID: "cancel", Name: "Cancel", Description: "Close the create prompt", Context: ctxGlobalWorkspacesCreate, Priority: 2},
		}
	case ctxGlobalWorkspacesTerminal:
		return []plugin.Command{
			{ID: "exit-interactive", Name: "Stop", Description: "Stop typing and return to the list", Context: ctxGlobalWorkspacesTerminal, Priority: 1},
		}
	case ctxGlobalWorkspacesDoc:
		cmds := []plugin.Command{
			{ID: "close", Name: "Close", Description: "Hide the document pane", Context: ctxGlobalWorkspacesDoc, Priority: 1},
			{ID: "close-tab", Name: "Tab×", Description: "Close the active file tab", Context: ctxGlobalWorkspacesDoc, Priority: 2},
			{ID: "prev-tab", Name: "Tab←", Description: "Previous file tab", Context: ctxGlobalWorkspacesDoc, Priority: 3},
			{ID: "next-tab", Name: "Tab→", Description: "Next file tab", Context: ctxGlobalWorkspacesDoc, Priority: 4},
			{ID: "render", Name: "Raw", Description: "Toggle rendered and raw markdown", Context: ctxGlobalWorkspacesDoc, Priority: 5},
			{ID: "yank-path", Name: "Yank", Description: "Copy the relative path", Context: ctxGlobalWorkspacesDoc, Priority: 6},
		}
		return cmds
	case ctxGlobalWorkspacesDiff:
		cmds := []plugin.Command{
			{ID: "close", Name: "Close", Description: "Hide the diff pane", Context: ctxGlobalWorkspacesDiff, Priority: 1},
			// 2..11 belong to the viewer's navigation, as on the project
			// surface; Close keeps the lead.
			{ID: "close-tab", Name: "Tab×", Description: "Close the active diff tab", Context: ctxGlobalWorkspacesDiff, Priority: 12},
			{ID: "prev-tab", Name: "Tab←", Description: "Previous diff tab", Context: ctxGlobalWorkspacesDiff, Priority: 13},
			{ID: "next-tab", Name: "Tab→", Description: "Next diff tab", Context: ctxGlobalWorkspacesDiff, Priority: 14},
			{ID: "yank-id", Name: "YankID", Description: "Copy target identity", Context: ctxGlobalWorkspacesDiff, Priority: 15},
		}
		if m.preview.diff != nil && m.preview.diff.view() != nil {
			cmds = append(cmds, m.preview.diff.view().Commands(ctxGlobalWorkspacesDiff)...)
		}
		return cmds
	case ctxGlobalWorkspacesIssue:
		return []plugin.Command{
			{ID: "open-item", Name: "Open", Description: "Open selected parent or subtask", Context: ctxGlobalWorkspacesIssue, Priority: 1},
			{ID: "close-tab", Name: "Tab×", Description: "Close the active issue tab", Context: ctxGlobalWorkspacesIssue, Priority: 2},
			{ID: "prev-tab", Name: "Tab←", Description: "Previous issue tab", Context: ctxGlobalWorkspacesIssue, Priority: 3},
			{ID: "next-tab", Name: "Tab→", Description: "Next issue tab", Context: ctxGlobalWorkspacesIssue, Priority: 4},
			{ID: "yank-issue", Name: "Yank", Description: "Copy issue as markdown", Context: ctxGlobalWorkspacesIssue, Priority: 5},
			{ID: "yank-issue-key", Name: "YankID", Description: "Copy issue ID", Context: ctxGlobalWorkspacesIssue, Priority: 6},
			{ID: "close", Name: "Close", Description: "Close the issue pane", Context: ctxGlobalWorkspacesIssue, Priority: 7},
		}
	default:
		cmds := []plugin.Command{
			{ID: "new-worktree", Name: "New", Description: "Create a worktree in a configured project", Context: ctxGlobalWorkspaces, Priority: 1},
			{ID: "new-shell", Name: "Shell", Description: "Create a shell in a configured project", Context: ctxGlobalWorkspaces, Priority: 2},
			{ID: "interactive", Name: "Type", Description: "Start typing in the selected live pane", Context: ctxGlobalWorkspaces, Priority: 2},
			{ID: "filter", Name: "Filter", Description: "Filter workspaces", Context: ctxGlobalWorkspaces, Priority: 2},
			{ID: "sort", Name: "Sort", Description: "Open the sort menu", Context: ctxGlobalWorkspaces, Priority: 3},
			{ID: "pin", Name: "Pin", Description: "Pin or unpin the selected workspace", Context: ctxGlobalWorkspaces, Priority: 4},
			{ID: "refresh", Name: "Refresh", Description: "Refresh the catalog", Context: ctxGlobalWorkspaces, Priority: 5},
			{ID: "toggle-sidebar", Name: "Sidebar", Description: "Toggle sidebar visibility", Context: ctxGlobalWorkspaces, Priority: 6},
			{ID: "close-overview", Name: "Close", Description: "Leave the global space", Context: ctxGlobalWorkspaces, Priority: 7},
		}
		if workspace, ok := m.SelectedWorkspace(); ok {
			switch workspace.Kind {
			case workspaceinventory.KindShell:
				cmds = append(cmds, plugin.Command{
					ID: "rename-shell", Name: "Rename", Description: "Rename the selected shell",
					Context: ctxGlobalWorkspaces, Priority: 8,
				})
			case workspaceinventory.KindWorktree:
				cmds = append(cmds, plugin.Command{
					ID: "rename-worktree", Name: "Rename", Description: "Rename the selected worktree",
					Context: ctxGlobalWorkspaces, Priority: 8,
				})
			}
		}
		if m.canOpenInGit() {
			cmds = append(cmds, plugin.Command{
				ID: "open-in-git", Name: "Git", Description: "Open the selected workspace in Git",
				Context: ctxGlobalWorkspaces, Priority: 9,
			})
		}
		return cmds
	}
}
