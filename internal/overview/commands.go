package overview

import (
	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/panereposition"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/resourceview"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

const (
	ctxGlobalWorkspaces         = "global-workspaces"
	ctxGlobalWorkspacesFilter   = "global-workspaces-filter"
	ctxGlobalWorkspacesRename   = "global-workspaces-rename"
	ctxGlobalWorkspacesCreate   = "global-workspaces-create"
	ctxGlobalWorkspacesDelete   = "global-workspaces-delete"
	ctxGlobalWorkspacesTerminal = "global-workspaces-terminal"
	ctxGlobalWorkspacesDoc      = "global-workspaces-doc"
	// The two claims a focused document pane can make on the keyboard: the
	// finder / project-search modal scoped to the pane, and docview's in-file
	// search bar. Both mirror the project workspace's workspace-doc-search and
	// workspace-doc-find.
	ctxGlobalWorkspacesDocSearch = "global-workspaces-doc-search"
	ctxGlobalWorkspacesDocFind   = "global-workspaces-doc-find"
	ctxGlobalWorkspacesIssue     = "global-workspaces-issue"
	ctxGlobalWorkspacesNote      = "global-workspaces-note"
	ctxGlobalWorkspacesDiff      = "global-workspaces-diff"
	ctxGlobalWorkspacesResource  = "global-workspaces-resource"
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
	case ctxGlobalWorkspacesDelete:
		subject := "shell"
		if m.DeletingWorktree() {
			subject = "worktree"
		}
		return []plugin.Command{
			{ID: "confirm-delete", Name: "Delete", Description: "Delete the selected " + subject, Context: ctxGlobalWorkspacesDelete, Priority: 1},
			{ID: "cancel", Name: "Cancel", Description: "Close the delete confirmation", Context: ctxGlobalWorkspacesDelete, Priority: 2},
		}
	case ctxGlobalWorkspacesTerminal:
		return []plugin.Command{
			{ID: "exit-interactive", Name: "Stop", Description: "Stop typing and return to the list", Context: ctxGlobalWorkspacesTerminal, Priority: 1},
			{ID: "search-terminal", Name: "Search", Description: "Search the complete terminal history", Context: ctxGlobalWorkspacesTerminal, Priority: 2},
		}
	case ctxGlobalWorkspacesDocSearch:
		return []plugin.Command{
			{ID: "search-open", Name: "Open", Description: "Open the selected file in this pane", Context: ctxGlobalWorkspacesDocSearch, Priority: 1},
			{ID: "search-open-tab", Name: "Tab+", Description: "Open the selected file in a new tab", Context: ctxGlobalWorkspacesDocSearch, Priority: 2},
			{ID: "search-cancel", Name: "Cancel", Description: "Close the search and return to the document", Context: ctxGlobalWorkspacesDocSearch, Priority: 3},
		}
	case ctxGlobalWorkspacesDocFind:
		return docview.SearchCommands(ctxGlobalWorkspacesDocFind)
	case ctxGlobalWorkspacesDoc:
		cmds := []plugin.Command{
			{ID: "close", Name: "Close", Description: "Hide the document pane", Context: ctxGlobalWorkspacesDoc, Priority: 1},
			{ID: "search-content", Name: "InFile", Description: "Search this file's contents", Context: ctxGlobalWorkspacesDoc, Priority: 2},
			{ID: "edit", Name: "Edit", Description: "Edit this file inline", Context: ctxGlobalWorkspacesDoc, Priority: 3},
			{ID: "reload", Name: "Reload", Description: "Reload this file from disk", Context: ctxGlobalWorkspacesDoc, Priority: 4},
			{ID: "find-file", Name: "Find", Description: "Find a file by name in this pane", Context: ctxGlobalWorkspacesDoc, Priority: 5},
			{ID: "search-project", Name: "Search", Description: "Search the project in this pane", Context: ctxGlobalWorkspacesDoc, Priority: 6},
			{ID: "close-tab", Name: "Tab×", Description: "Close the active file tab", Context: ctxGlobalWorkspacesDoc, Priority: 7},
			{ID: "prev-tab", Name: "Tab←", Description: "Previous file tab", Context: ctxGlobalWorkspacesDoc, Priority: 8},
			{ID: "next-tab", Name: "Tab→", Description: "Next file tab", Context: ctxGlobalWorkspacesDoc, Priority: 9},
			{ID: "render", Name: "Raw", Description: "Toggle rendered and raw markdown", Context: ctxGlobalWorkspacesDoc, Priority: 10},
			{ID: "yank-contents", Name: "Yank", Description: "Copy file contents", Context: ctxGlobalWorkspacesDoc, Priority: 11},
			{ID: "yank-path", Name: "Path", Description: "Copy the relative path", Context: ctxGlobalWorkspacesDoc, Priority: 12},
		}
		return m.withPaneMoveCommand(cmds, ctxGlobalWorkspacesDoc)
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
		return m.withPaneMoveCommand(cmds, ctxGlobalWorkspacesDiff)
	case ctxGlobalWorkspacesResource:
		// The vocabulary is resourceview's, not this surface's: both hosts
		// register exactly this list, so a Resource pane cannot advertise one
		// set of keys in the project workspace and another here. Close is the
		// one addition, because hiding a content pane is each surface's own
		// rule rather than the pane's.
		cmds := []plugin.Command{
			{ID: "close", Name: "Close", Description: "Hide the resource pane", Context: ctxGlobalWorkspacesResource, Priority: 1},
		}
		for i, c := range resourceview.Commands() {
			cmds = append(cmds, plugin.Command{
				ID: c.ID, Name: c.Name, Description: resourceCommandDescription(c.ID),
				Context: ctxGlobalWorkspacesResource, Priority: i + 2,
			})
		}
		return m.withPaneMoveCommand(cmds, ctxGlobalWorkspacesResource)
	case ctxGlobalWorkspacesIssue:
		return m.withPaneMoveCommand([]plugin.Command{
			{ID: "open-item", Name: "Open", Description: "Open selected parent or subtask", Context: ctxGlobalWorkspacesIssue, Priority: 1},
			{ID: "open-in-td", Name: "TD", Description: "Open the selected issue in td", Context: ctxGlobalWorkspacesIssue, Priority: 2},
			{ID: "close-tab", Name: "Tab×", Description: "Close the active issue tab", Context: ctxGlobalWorkspacesIssue, Priority: 3},
			{ID: "prev-tab", Name: "Tab←", Description: "Previous issue tab", Context: ctxGlobalWorkspacesIssue, Priority: 4},
			{ID: "next-tab", Name: "Tab→", Description: "Next issue tab", Context: ctxGlobalWorkspacesIssue, Priority: 5},
			{ID: "yank-issue", Name: "Yank", Description: "Copy issue as markdown", Context: ctxGlobalWorkspacesIssue, Priority: 6},
			{ID: "yank-issue-key", Name: "YankID", Description: "Copy issue ID", Context: ctxGlobalWorkspacesIssue, Priority: 7},
			{ID: "close", Name: "Close", Description: "Close the issue pane", Context: ctxGlobalWorkspacesIssue, Priority: 8},
		}, ctxGlobalWorkspacesIssue)
	case ctxGlobalWorkspacesNote:
		return m.withPaneMoveCommand([]plugin.Command{
			{ID: "close-tab", Name: "Tab×", Description: "Close the active note tab", Context: ctxGlobalWorkspacesNote, Priority: 1},
			{ID: "prev-tab", Name: "Tab←", Description: "Previous note tab", Context: ctxGlobalWorkspacesNote, Priority: 2},
			{ID: "next-tab", Name: "Tab→", Description: "Next note tab", Context: ctxGlobalWorkspacesNote, Priority: 3},
			{ID: "yank-note", Name: "Yank", Description: "Copy note as markdown", Context: ctxGlobalWorkspacesNote, Priority: 4},
			{ID: "yank-note-key", Name: "YankID", Description: "Copy note ID", Context: ctxGlobalWorkspacesNote, Priority: 5},
			{ID: "close", Name: "Close", Description: "Close the note pane", Context: ctxGlobalWorkspacesNote, Priority: 6},
		}, ctxGlobalWorkspacesNote)
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
				cmds = append(cmds, plugin.Command{ID: "delete-shell", Name: "Delete", Description: "Delete the selected shell", Context: ctxGlobalWorkspaces, Priority: 9})
			case workspaceinventory.KindWorktree:
				cmds = append(cmds, plugin.Command{
					ID: "rename-worktree", Name: "Rename", Description: "Rename the selected worktree",
					Context: ctxGlobalWorkspaces, Priority: 8,
				})
				if mergeRefusal(workspace) == "" {
					cmds = append(cmds, plugin.Command{ID: "merge-workflow", Name: "Merge", Description: "Open the owning project's merge strategy workflow", Context: ctxGlobalWorkspaces, Priority: 9})
				}
				if deleteRefusal(workspace) == "" {
					cmds = append(cmds, plugin.Command{ID: "delete-worktree", Name: "Delete", Description: "Delete the selected worktree", Context: ctxGlobalWorkspaces, Priority: 10})
				}
			}
		}
		if m.canOpenInGit() {
			cmds = append(cmds, plugin.Command{
				ID: "open-in-git", Name: "Git", Description: "Open the selected workspace in Git",
				Context: ctxGlobalWorkspaces, Priority: 9,
			})
		}
		return m.withPaneMoveCommand(cmds, ctxGlobalWorkspaces)
	}
}

func (m *Model) withPaneMoveCommand(cmds []plugin.Command, context string) []plugin.Command {
	if m.paneLayoutShortcutLeaf() == 0 {
		return cmds
	}
	return append(cmds, plugin.Command{
		ID: panereposition.CommandMove, Name: "Move", Description: "Reposition this pane",
		Context: context, Priority: 90,
	})
}

// resourceCommandDescription is the sentence for a shared Resource command.
// The IDs, keys and footer names come from resourceview so the two surfaces
// cannot drift; only the longer help text is written here.
func resourceCommandDescription(id string) string {
	switch id {
	case resourceview.CommandRefresh:
		return "Re-resolve the active resource"
	case resourceview.CommandOpenSource:
		return "Open the resource in a browser"
	case resourceview.CommandPrevTab:
		return "Previous resource tab"
	case resourceview.CommandNextTab:
		return "Next resource tab"
	case resourceview.CommandCloseTab:
		return "Close the active resource tab"
	default:
		return ""
	}
}
