package overview

import (
	tea "charm.land/bubbletea/v2"
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
	path, ok := m.openInGitPath()
	if !ok {
		return nil
	}
	return func() tea.Msg { return OpenInGitMsg{Path: path} }
}

func (m *Model) openInGitPath() (string, bool) {
	workspace, ok := m.SelectedWorkspace()
	if !ok {
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
