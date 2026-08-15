package workspace

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/workspacelist"
)

const activityAnimationInterval = workspacelist.PulseInterval

type activityAnimationTickMsg struct {
	generation uint64
}

func (p *Plugin) animatedActivityPresentation(agent *Agent) (icon, text string, style lipgloss.Style, ok bool) {
	icon, text, style, ok = activityPresentation(agent)
	if !ok || agent == nil {
		return icon, text, style, ok
	}

	if pulsed, pulsedStyle, pulsing := workspacelist.PulseMarker(agent.Activity.DisplayState(), p.activityAnimationFrame); pulsing {
		return pulsed, text, pulsedStyle, true
	}
	return icon, text, style, ok
}

func (p *Plugin) activityAnimationNeeded() bool {
	if !p.focused || !p.applicationFocused || (p.viewMode != ViewModeList && p.viewMode != ViewModeKanban) {
		return false
	}
	needsAnimation := func(agent *Agent) bool {
		if agent == nil || !supportsAgentActivity(agent.Type) {
			return false
		}
		state := agent.Activity.DisplayState()
		return state == string(agentactivity.StateWorking) || state == string(agentactivity.StateBlocked)
	}
	worktreeNeedsAnimation := func(wt *Worktree) bool {
		return wt != nil && !wt.IsMissing && !wt.IsOrphaned && needsAnimation(wt.Agent)
	}
	shellNeedsAnimation := func(shell *ShellSession) bool {
		return shell != nil && !shell.IsOrphaned && needsAnimation(shell.Agent)
	}

	effectiveView := p.viewMode
	if effectiveView == ViewModeKanban && kanbanUsesListFallback(p.width) {
		effectiveView = ViewModeList
	}

	switch effectiveView {
	case ViewModeList:
		if !p.sidebarVisible {
			return false
		}
		// Ask the same projection the renderer draws from. This used to assume
		// the list was every shell followed by a scrollable slice of
		// worktrees, and indexed p.worktrees by scrollOffset — but scrollOffset
		// counts rows in the combined list, and a computed sort interleaves
		// the two kinds and lifts nested shells to the top level. Under any
		// sort but Manual that arithmetic pointed at the wrong records, so a
		// working row on screen could fail to keep its own animation running.
		items := p.visibleSidebarItems()
		start := max(p.scrollOffset, 0)
		end := min(start+p.visibleCount, len(items))
		if p.visibleCount <= 0 {
			// Nothing has been rendered yet, so nothing is known to be hidden.
			// Consider the whole list rather than starting the clock late.
			start, end = 0, len(items)
		}
		for _, item := range items[min(start, len(items)):end] {
			switch item.kind {
			case navKindShell:
				if shellNeedsAnimation(p.shells[item.shellIdx]) {
					return true
				}
			case navKindNestedShell:
				if shellNeedsAnimation(item.shell) {
					return true
				}
			default:
				if worktreeNeedsAnimation(p.worktrees[item.worktreeIdx]) {
					return true
				}
			}
		}
	case ViewModeKanban:
		limit := kanbanVisibleCardCount(p.height)
		p.syncKanbanComponent()
		for _, card := range p.kanban.VisibleCards(limit) {
			if shell := p.kanbanShellByID(card.ID); shell != nil {
				if shellNeedsAnimation(shell) {
					return true
				}
				continue
			}
			if worktreeNeedsAnimation(p.kanbanWorktreeByID(card.ID)) {
				return true
			}
		}
	}
	return false
}

func (p *Plugin) startActivityAnimation() tea.Cmd {
	if p.activityAnimationScheduled || !p.activityAnimationNeeded() {
		return nil
	}
	p.activityAnimationScheduled = true
	generation := p.activityAnimationGeneration
	return tea.Tick(activityAnimationInterval, func(time.Time) tea.Msg {
		return activityAnimationTickMsg{generation: generation}
	})
}

func appendActivityAnimationCmd(cmds []tea.Cmd, cmd tea.Cmd) []tea.Cmd {
	if cmd != nil {
		return append(cmds, cmd)
	}
	return cmds
}
