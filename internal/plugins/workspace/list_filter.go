package workspace

import (
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/workspacelist"
)

// The project Workspaces sidebar and the global Workspaces browser share one
// definition of filtering: the same `/` entry, the same multi-field matcher,
// the same counts and no-match row, and the same escape/enter behaviour. That
// shared definition lives in internal/workspacelist; this file is the project
// side's projection into it.
//
// Filtering is deliberately explicit. `/` enters it so the project list keeps
// its printable commands (n, D, p) as they are, and the filter reports a
// text-input context while it has focus so sidecar's own shortcuts cannot take
// characters or pastes out of a query.

// filterActive reports that a query is narrowing the list. Everything about
// the non-filtered journey — navigation, hit regions, scrollbar, commands —
// is reached through the same code paths whether or not this is true; an
// inactive filter simply makes every row visible.
func (p *Plugin) filterActive() bool { return p.listFilter.Active() }

func (p *Plugin) filterFocused() bool { return p.listFilter.Focused() }

// focusListFilter is the `/` entry point.
func (p *Plugin) focusListFilter() {
	p.listFilter.Focus()
}

// projectDisplayName is the textual project identity rows can match on. The
// project sidebar shows one project, but the field still participates so a
// query typed in one list behaves the same in the other.
func (p *Plugin) projectDisplayName() string {
	if p.ctx == nil || p.ctx.ProjectRoot == "" {
		return ""
	}
	return filepath.Base(p.ctx.ProjectRoot)
}

// worktreeFilterFields is the exact promised field set for a worktree row.
func (p *Plugin) worktreeFilterFields(wt *Worktree) []string {
	if wt == nil {
		return nil
	}
	provider := ""
	if wt.Agent != nil {
		provider = string(wt.Agent.Type)
	} else if wt.ChosenAgentType != "" && wt.ChosenAgentType != AgentNone {
		provider = string(wt.ChosenAgentType)
	}
	status := agentStatusPresentation(wt).Label
	if wt.IsMain {
		status += " main"
	}
	return []string{wt.Name, p.projectDisplayName(), wt.Branch, wt.TaskID, wt.TaskTitle, provider, status}
}

// shellFilterFields is the exact promised field set for a shell row.
func (p *Plugin) shellFilterFields(shell *ShellSession) []string {
	if shell == nil {
		return nil
	}
	provider := ""
	if shell.Agent != nil {
		provider = string(shell.Agent.Type)
	}
	if provider == "" || provider == string(AgentShell) {
		if shell.ChosenAgent != "" && shell.ChosenAgent != AgentNone {
			provider = string(shell.ChosenAgent)
		}
	}
	status := "no session"
	switch {
	case shell.IsOrphaned:
		status = "offline"
	case shell.Agent != nil:
		status = "live"
	}
	return []string{shell.Name, p.projectDisplayName(), shell.TmuxName, provider, status, "shell"}
}

// visibleShellIndices lists the shell indices the sidebar draws, in order.
// With no query it is every shell, which is why the unfiltered journey is
// byte-identical through the same loop.
func (p *Plugin) visibleShellIndices() []int {
	indices := make([]int, 0, len(p.shells))
	query := p.listFilter.Query()
	for i, shell := range p.shells {
		if query == "" || workspacelist.MatchFields(query, p.shellFilterFields(shell)...) {
			indices = append(indices, i)
		}
	}
	return indices
}

// visibleWorktreeIndices lists the worktree indices the sidebar draws.
func (p *Plugin) visibleWorktreeIndices() []int {
	indices := make([]int, 0, len(p.worktrees))
	query := p.listFilter.Query()
	for i, wt := range p.worktrees {
		if query == "" || workspacelist.MatchFields(query, p.worktreeFilterFields(wt)...) || len(p.visibleNestedShells(wt)) > 0 {
			indices = append(indices, i)
		}
	}
	return indices
}

func (p *Plugin) nestedShellTotal() int {
	n := 0
	for _, wt := range p.worktrees {
		if p.isCurrentWorkDir(wt.Path) {
			continue
		}
		n += len(p.nestedByWorkDir[filepath.Clean(wt.Path)])
	}
	return n
}

func (p *Plugin) visibleNestedCount() int {
	n := 0
	for _, i := range p.visibleWorktreeIndices() {
		n += len(p.visibleNestedShells(p.worktrees[i]))
	}
	return n
}

// filterCounts is the "N of M" the filter row reports.
func (p *Plugin) filterCounts() (matched, total int) {
	return len(p.visibleShellIndices()) + len(p.visibleWorktreeIndices()) + p.visibleNestedCount(),
		len(p.shells) + len(p.worktrees) + p.nestedShellTotal()
}

// selectionVisible reports that the current selection survives the query.
func (p *Plugin) selectionVisible() bool {
	if p.shellSelected {
		return containsIndex(p.visibleShellIndices(), p.selectedShellIdx)
	}
	if p.selectedNestedTmux != "" {
		for _, i := range p.visibleWorktreeIndices() {
			for _, shell := range p.visibleNestedShells(p.worktrees[i]) {
				if shell.TmuxName == p.selectedNestedTmux {
					return true
				}
			}
		}
		return false
	}
	return containsIndex(p.visibleWorktreeIndices(), p.selectedIdx)
}

func containsIndex(indices []int, want int) bool {
	for _, index := range indices {
		if index == want {
			return true
		}
	}
	return false
}

// clampSelectionToFilter keeps the cursor on a row the user can see. Selection
// is preserved whenever the selected identity still matches; only a selection
// the query removed moves, and then to the first visible row.
//
// Scroll is re-clamped either way: the offset is a position into the filtered
// projection, so a query typed while the list is scrolled has to bring the
// offset back inside the rows that survived it, even when the selection did.
func (p *Plugin) clampSelectionToFilter() tea.Cmd {
	if p.selectionVisible() {
		p.ensureVisible()
		return nil
	}
	items := p.visibleSidebarItems()
	if len(items) == 0 {
		return nil
	}
	p.selectSidebarItem(items[0])
	p.exitInteractiveMode()
	p.saveSelectionState()
	p.ensureVisible()
	return p.loadSelectedContent()
}

// handleFilterKey routes one key while the filter owns the keyboard. Arrow and
// ctrl+n/ctrl+p navigation stays live, so a user can type, arrow onto a match,
// and press enter without leaving the query.
func (p *Plugin) handleFilterKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	if !p.filterFocused() {
		return false, nil
	}
	switch p.listFilter.HandleKey(msg.String(), msg.Text) {
	case workspacelist.KeyIgnored:
		switch msg.String() {
		case "up", "ctrl+p":
			p.moveCursor(-1)
			return true, p.loadSelectedContent()
		case "down", "ctrl+n":
			p.moveCursor(1)
			return true, p.loadSelectedContent()
		}
		// Everything else is swallowed: the filter is a text input, and a stray
		// key must not fall through to a destructive project command.
		return true, nil
	case workspacelist.KeyAccept:
		// Enter leaves the focused item selected and returns to list navigation
		// with the query still narrowing the list.
		return true, nil
	default:
		return true, p.clampSelectionToFilter()
	}
}

// handleFilterPaste appends pasted text to a focused query.
func (p *Plugin) handleFilterPaste(text string) (bool, tea.Cmd) {
	if !p.filterFocused() || text == "" {
		return false, nil
	}
	p.listFilter.Insert(strings.ReplaceAll(text, "\n", " "))
	return true, p.clampSelectionToFilter()
}

// resetListFilter drops query and focus. Filter state is in-memory and per
// consumer, so a plugin reinit starts clean rather than restoring a query the
// user cannot see the origin of.
func (p *Plugin) resetListFilter() { p.listFilter.Reset() }

// selectFirstVisible / selectLastVisible are the filtered forms of g and G.
func (p *Plugin) selectFirstVisible() {
	items := p.visibleSidebarItems()
	if len(items) == 0 {
		return
	}
	p.selectSidebarItem(items[0])
	p.scrollOffset = 0
	p.exitInteractiveMode()
	p.saveSelectionState()
}

func (p *Plugin) selectLastVisible() {
	items := p.visibleSidebarItems()
	if len(items) == 0 {
		return
	}
	p.selectSidebarItem(items[len(items)-1])
	p.exitInteractiveMode()
	p.saveSelectionState()
	p.ensureVisible()
}
