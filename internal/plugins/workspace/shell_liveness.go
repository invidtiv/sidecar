package workspace

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/shellliveness"
)

// The project Workspaces surface binds to the shared liveness rule here, and
// nowhere else. The global browser has its own one-file binding; the decision
// both of them apply lives in internal/shellliveness (td-6a4100).

// shellLivenessProbe is indirected for tests. It is the only thing in this file
// that touches tmux, and it never runs on the update loop.
var shellLivenessProbe = shellliveness.ProbeSession

type (
	// shellDeathSuspectedMsg says a capture just failed in a way that names one
	// missing session. It is suspicion only; the probe decides.
	shellDeathSuspectedMsg struct {
		TmuxName   string
		Generation int
	}

	// shellDeathProbedMsg carries the independent second opinion, tagged with
	// the life of the tmux name it was taken about.
	shellDeathProbedMsg struct {
		TmuxName    string
		Generation  int
		Incarnation uint64
		Verdict     shellliveness.Verdict
	}
)

// shellLivenessTracker lazily builds the per-plugin tracker. Construction
// allocates a map and nothing else, so this stays off the startup budget.
func (p *Plugin) shellLivenessTracker() *shellliveness.Tracker {
	if p.shellLiveness == nil {
		p.shellLiveness = shellliveness.NewTracker()
	}
	return p.shellLiveness
}

// noteShellAlive records positive liveness for a shell this plugin knows is
// running, so a later probe is allowed to close it.
func (p *Plugin) noteShellAlive(tmuxName string) {
	if tmuxName == "" {
		return
	}
	p.shellLivenessTracker().Observe(tmuxName)
}

// suspectShellDeath raises suspicion about one session from outside the poll
// chain — an embedded terminal that reported its pane gone, a send that was
// refused. Generation 0 marks it as belonging to no poll owner.
func (p *Plugin) suspectShellDeath(tmuxName string) tea.Cmd {
	if tmuxName == "" || p.findShellByName(tmuxName) == nil {
		return nil
	}
	return func() tea.Msg { return shellDeathSuspectedMsg{TmuxName: tmuxName} }
}

// handleShellDeathSuspected turns a suspicious capture failure into at most one
// tmux probe. A shell this surface never saw running has nothing to close, and
// a shell probed moments ago waits rather than spawning tmux again.
func (p *Plugin) handleShellDeathSuspected(msg shellDeathSuspectedMsg) tea.Cmd {
	if msg.Generation != 0 && !p.pollScheduler.IsCurrent(shellPollKey(msg.TmuxName), msg.Generation) {
		return nil
	}
	if p.findShellByName(msg.TmuxName) == nil {
		return nil
	}
	// The gate is the tracker's own liveness record, the same one the global
	// browser uses. This used to accept "the row has an Agent" as a stand-in
	// and grant liveness from it, which the nested sibling projection
	// fabricates for rows on tmux servers this instance cannot see (td-6a4100).
	if !p.shellLivenessTracker().ShouldProbe(msg.TmuxName, time.Now()) {
		// Keep the poll alive under a fresh owner, at the idle cadence: a shell
		// we cannot capture has nothing new to show, and polling it hard would
		// spend a subprocess every 200ms to learn that again.
		return p.scheduleShellPollByName(msg.TmuxName, pollIntervalIdle)
	}
	tmuxName, generation := msg.TmuxName, msg.Generation
	// Read the incarnation before the probe leaves the update loop. If the
	// session is recreated under the same name while tmux is being asked — the
	// user pressing Enter on the row is all it takes — the verdict comes back
	// tagged with a life that has ended, and Confirm refuses it.
	incarnation := p.shellLivenessTracker().Incarnation(tmuxName)
	return func() tea.Msg {
		return shellDeathProbedMsg{
			TmuxName:    tmuxName,
			Generation:  generation,
			Incarnation: incarnation,
			Verdict:     shellLivenessProbe(tmuxName),
		}
	}
}

// handleShellDeathProbed closes the shell only on a confirmed Gone verdict.
// Anything else resumes polling: an unreachable tmux is not a dead shell.
func (p *Plugin) handleShellDeathProbed(msg shellDeathProbedMsg) tea.Cmd {
	if msg.Generation != 0 && !p.pollScheduler.IsCurrent(shellPollKey(msg.TmuxName), msg.Generation) {
		return nil
	}
	if p.shellLivenessTracker().Confirm(msg.TmuxName, msg.Verdict, msg.Incarnation) {
		tmuxName := msg.TmuxName
		// Generation 0 marks a non-poll lifecycle close, which the dead-shell
		// handler accepts without a generation check.
		return func() tea.Msg { return ShellSessionDeadMsg{TmuxName: tmuxName} }
	}
	return p.scheduleShellPollByName(msg.TmuxName, pollIntervalIdle)
}
