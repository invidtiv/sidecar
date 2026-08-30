package overview

import (
	tea "charm.land/bubbletea/v2"
	appmsg "github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// OpenInGitMsg asks the app to leave global, switch to this checkout, and
// focus the Git plugin. Sequence (not Batch) is the app's job — plugin reinit
// deadlocks if SwitchWorktree and FocusPlugin run concurrently.
type OpenInGitMsg struct {
	Path string
}

// OpenSelectedInGit jumps to the project's Git plugin at the checkout the
// mini-diff showed: worktree Path, or ProjectRoot for a shell.
func (m *Model) OpenSelectedInGit() tea.Cmd {
	if workspace, ok := m.SelectedWorkspace(); ok {
		if reason := remoteActionRefusal(workspace, "open"); reason != "" {
			return appmsg.Blocked(reason)
		}
	}
	path, ok := m.openInGitPath()
	if !ok {
		return nil
	}
	return func() tea.Msg { return OpenInGitMsg{Path: path} }
}

// openInGitPath is the local checkout the Git plugin would open.
//
// A remote row has none. Its Path names a directory on another machine, and
// the app answers OpenInGitMsg with a local SwitchWorktree — which on a machine
// laid out like the other one does not fail, it silently opens THIS machine's
// repository under the remote row's name. That is the same class the navigation
// guard refuses, and this is the second door into it.
func (m *Model) openInGitPath() (string, bool) {
	workspace, ok := m.SelectedWorkspace()
	if !ok {
		return "", false
	}
	if remoteActionRefusal(workspace, "open") != "" {
		return "", false
	}
	path := workspace.Path
	if workspace.Kind == workspaceinventory.KindShell {
		path = workspace.ProjectRoot
	}
	if path == "" {
		return "", false
	}
	return path, true
}

func (m *Model) canOpenInGit() bool {
	_, ok := m.openInGitPath()
	return ok
}

// CanOpenInGit reports that the list cursor has a checkout the Git plugin can open.
func (m *Model) CanOpenInGit() bool { return m.canOpenInGit() }
