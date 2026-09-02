package app

import (
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/termtitle"
)

// titleResyncTicks is how many one-second ticks pass between forced re-emissions
// of the terminal title. Programs run through tea.ExecProcess set titles of
// their own, and the only one that reports back to the app is the editor
// (EditorReturnedMsg); an attached tmux session returns into the workspace
// plugin, so the tick path re-asserts the title periodically and the tab label
// heals itself no matter which path clobbered it.
const titleResyncTicks = 10

// worktreeInventoryTicks is how many one-second ticks pass between background
// refreshes of the worktree inventory. Anything that changes the inventory from
// inside sidecar refreshes it directly; this only catches changes made outside.
const worktreeInventoryTicks = 10

// terminalTitle renders the configured title template against the current
// project. Empty means "don't touch the terminal title".
func (m Model) terminalTitle() string {
	if m.titleTemplate == "" {
		return ""
	}

	// Only a linked worktree is worth naming — on the main worktree the branch
	// is just noise, which is the same call renderHeader makes.
	worktree := ""
	project := m.intro.RepoName
	if m.boundDestination.HostID != "" {
		project = BoundDestinationTitleProject(m.boundDestination)
		worktree = BoundDestinationTitleWorktree(m.boundDestination)
	} else if wtInfo := m.currentWorktreeInfo(); wtInfo != nil && !wtInfo.IsMain {
		worktree = wtInfo.Branch
		if worktree == "" {
			worktree = "worktree"
		}
	}

	pluginName := ""
	if p := m.ActivePlugin(); p != nil {
		pluginName = p.Name()
	}

	// filepath.Base answers "." for an empty path and "/" for the root; neither
	// is a name worth putting on a tab.
	dir := filepath.Base(m.ui.WorkDir)
	if dir == "." || dir == string(filepath.Separator) {
		dir = ""
	}

	vars := termtitle.Vars{
		Project:  project,
		Worktree: worktree,
		Plugin:   pluginName,
		Dir:      dir,
	}

	// A template can legitimately render to nothing — {project} is empty
	// outside a git repository, {worktree} is empty on the main worktree. Fall
	// back to the directory name rather than emitting an empty title, which
	// would wipe whatever the user's shell had put there.
	if title := termtitle.Render(m.titleTemplate, vars); title != "" {
		return title
	}
	return termtitle.Render("{dir}", vars)
}

// syncTerminalTitle emits the terminal title when it has changed, and nil
// otherwise. force re-sends an unchanged title, to take the terminal back from
// a program that set its own — see [titleResyncTicks].
func (m *Model) syncTerminalTitle(force bool) tea.Cmd {
	title := m.terminalTitle()
	if title == "" || (title == m.lastTitle && !force) {
		return nil
	}
	m.lastTitle = title
	return tea.Raw(termtitle.Set(title))
}
