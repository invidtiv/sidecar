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
		if query == "" || workspacelist.MatchFields(query, p.worktreeFilterFields(wt)...) {
			indices = append(indices, i)
		}
	}
	return indices
}

// filterCounts is the "N of M" the filter row reports.
func (p *Plugin) filterCounts() (matched, total int) {
	return len(p.visibleShellIndices()) + len(p.visibleWorktreeIndices()), len(p.shells) + len(p.worktrees)
}

// selectionVisible reports that the current selection survives the query.
func (p *Plugin) selectionVisible() bool {
	if p.shellSelected {
		return containsIndex(p.visibleShellIndices(), p.selectedShellIdx)
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
func (p *Plugin) clampSelectionToFilter() tea.Cmd {
	if p.selectionVisible() {
		return nil
	}
	shells, worktrees := p.visibleShellIndices(), p.visibleWorktreeIndices()
	switch {
	case len(shells) > 0:
		p.shellSelected, p.selectedShellIdx = true, shells[0]
	case len(worktrees) > 0:
		p.shellSelected, p.selectedIdx = false, worktrees[0]
	default:
		return nil
	}
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
	shells, worktrees := p.visibleShellIndices(), p.visibleWorktreeIndices()
	switch {
	case len(shells) > 0:
		p.shellSelected, p.selectedShellIdx = true, shells[0]
	case len(worktrees) > 0:
		p.shellSelected, p.selectedIdx = false, worktrees[0]
	default:
		return
	}
	p.scrollOffset = 0
	p.exitInteractiveMode()
	p.saveSelectionState()
}

func (p *Plugin) selectLastVisible() {
	shells, worktrees := p.visibleShellIndices(), p.visibleWorktreeIndices()
	switch {
	case len(worktrees) > 0:
		p.shellSelected, p.selectedIdx = false, worktrees[len(worktrees)-1]
	case len(shells) > 0:
		p.shellSelected, p.selectedShellIdx = true, shells[len(shells)-1]
	default:
		return
	}
	p.exitInteractiveMode()
	p.saveSelectionState()
	p.ensureVisible()
}

// moveCursorFiltered walks the visible rows only: shells first, then
// worktrees, clamping at both ends exactly as the unfiltered walk does.
func (p *Plugin) moveCursorFiltered(delta int) {
	shells, worktrees := p.visibleShellIndices(), p.visibleWorktreeIndices()
	if len(shells)+len(worktrees) == 0 {
		return
	}
	position := 0
	switch {
	case p.shellSelected:
		position = indexOfValue(shells, p.selectedShellIdx)
	default:
		if found := indexOfValue(worktrees, p.selectedIdx); found >= 0 {
			position = len(shells) + found
		} else {
			position = -1
		}
	}
	if position < 0 {
		position = 0
	} else {
		position = min(max(position+delta, 0), len(shells)+len(worktrees)-1)
	}
	if position < len(shells) {
		p.shellSelected, p.selectedShellIdx = true, shells[position]
		return
	}
	p.shellSelected, p.selectedIdx = false, worktrees[position-len(shells)]
}

func indexOfValue(values []int, want int) int {
	for i, value := range values {
		if value == want {
			return i
		}
	}
	return -1
}
