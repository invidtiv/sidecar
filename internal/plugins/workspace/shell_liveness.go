package workspace

import (
	"log/slog"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/shellliveness"
	"github.com/marcus/sidecar/internal/tmuxserver"
	"github.com/marcus/sidecar/internal/workspaceops"
)

// The project Workspaces surface binds to the shared liveness rule here, and
// nowhere else. The global browser has its own one-file binding; the decision
// both of them apply lives in internal/shellliveness (td-6a4100).

// shellLivenessProbe and shellLivenessServer are indirected for tests. Probe
// is the only tmux subprocess this file starts; Server is one stat.
var (
	shellLivenessProbe  = shellliveness.ProbeSession
	shellLivenessServer = tmuxserver.Socket
	// observeServerIncarnation answers "which tmux server is running, if any".
	// Indirected for the same reason the two above are: a test needs to state
	// which of the three answers it is exercising, and the reap decision is
	// entirely determined by that answer.
	observeServerIncarnation = workspaceops.ServerIncarnation
)

type (
	// shellDeathSuspectedMsg says a capture just failed in a way that names one
	// missing session. It is suspicion only; the probe decides.
	shellDeathSuspectedMsg struct {
		TmuxName   string
		Generation int
	}

	// shellDeathProbedMsg carries the independent second opinion, tagged with
	// the life of the tmux name it was taken about and the server incarnation
	// it was taken under. Incarnation is the name-life (td-6a4100);
	// Server is the tmux server (td-388929). They are not the same identity.
	shellDeathProbedMsg struct {
		TmuxName    string
		Generation  int
		Incarnation uint64
		Server      tmuxserver.Incarnation
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

// observeTmuxServer is the only place this surface tells the tracker which
// server it is watching. Discovery, a live socket-stat on the liveness path,
// and a confirmed probe all land here; the shared decision stays in
// shellliveness (td-388929).
func (p *Plugin) observeTmuxServer(inc tmuxserver.Incarnation) {
	p.shellLivenessTracker().ObserveServer(inc)
}

// noteShellAlive records positive liveness for a shell this plugin knows is
// running, so a later probe is allowed to close it.
func (p *Plugin) noteShellAlive(tmuxName string) {
	if tmuxName == "" {
		return
	}
	// Capture/create/discovery all learn the current server here so a later
	// socket-stat on the suspicion path is a live transition, not the first
	// observation (which would otherwise clear seenAlive).
	p.observeTmuxServer(shellLivenessServer())
	p.shellLivenessTracker().Observe(tmuxName)
	p.markShellRestoreEligible(tmuxName)
}

// observedServerID resolves the tmux server identity that may be written down
// and compared later, as a "pid=N" id.
//
// The tracker's own identity is socket-only on this surface, and a socket has no
// pid, so it is resolved here and cached for the life of that socket identity.
// The one `tmux display-message` this costs happens once per server rather than
// once per shell or once per cycle.
//
// An empty result means no tmux server is running, and callers rely on that
// exact reading: it is the condition under which a shell cannot be shown to have
// exited, and so the condition under which its record must be preserved rather
// than tombstoned.
func (p *Plugin) observedServer() tmuxserver.Incarnation {
	socket := shellLivenessServer()
	if p.restoreServerKnown && socket.Equal(p.restoreServerSocket) {
		return p.restoreServer
	}
	// ServerIncarnation, not ServerPID: the pid alone returns 0 both when no
	// server is running and when the question could not be answered, and this
	// surface's reap decision turns on telling those apart. Reading a failed
	// subprocess as a dead server is what marks a shell the user closed as a
	// restore candidate.
	inc := observeServerIncarnation()
	p.restoreServerSocket, p.restoreServer, p.restoreServerKnown = socket, inc, true
	return inc
}

// observedServerID is the persistable id of the observed server, empty when the
// server is absent or unidentifiable.
func (p *Plugin) observedServerID() string { return p.observedServer().ServerID() }

// markShellRestoreEligible records that this shell is running under the current
// tmux server, which is what lets a later cold restore tell a shell that died
// with its server from one nobody had open.
//
// noteShellAlive is called on every capture, so the in-memory set is what keeps
// this from becoming a manifest read per capture: the writer beneath already
// declines to write an unchanged marker, but it would still have to open the
// file to find that out. Steady state here is no syscalls at all.
func (p *Plugin) markShellRestoreEligible(tmuxName string) {
	if p.shellManifest == nil {
		return
	}
	server := p.observedServerID()
	if server == "" {
		return
	}
	if p.restoreMarked == nil {
		p.restoreMarked = map[string]string{}
	}
	if p.restoreMarked[tmuxName] == server {
		return
	}
	if err := p.shellManifest.MarkRestoreEligible(tmuxName, server, time.Now().UTC()); err != nil {
		// A marker is an optimization for a future restore, never a precondition
		// for anything happening now.
		slog.Debug("workspace: restore eligibility", "shell", tmuxName, "err", err)
		return
	}
	p.restoreMarked[tmuxName] = server
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
	// Socket-stat is free and is how a Sidecar running outside tmux notices a
	// server restart without waiting for the next discovery pass. Observe
	// before ShouldProbe so a transition clears seenAlive and we do not probe.
	p.observeTmuxServer(shellLivenessServer())
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
	// Read the name-life and server incarnation before the probe leaves the
	// update loop. If the session is recreated under the same name, or the
	// whole server is replaced, while tmux is being asked, Confirm refuses.
	incarnation := p.shellLivenessTracker().Incarnation(tmuxName)
	server := p.shellLivenessTracker().Server()
	return func() tea.Msg {
		return shellDeathProbedMsg{
			TmuxName:    tmuxName,
			Generation:  generation,
			Incarnation: incarnation,
			Server:      server,
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
	p.observeTmuxServer(shellLivenessServer())
	if p.shellLivenessTracker().Confirm(msg.TmuxName, msg.Verdict, msg.Incarnation, msg.Server) {
		tmuxName := msg.TmuxName
		// Generation 0 marks a non-poll lifecycle close, which the dead-shell
		// handler accepts without a generation check.
		return func() tea.Msg { return ShellSessionDeadMsg{TmuxName: tmuxName} }
	}
	return p.scheduleShellPollByName(msg.TmuxName, pollIntervalIdle)
}
