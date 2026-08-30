package overview

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/shellliveness"
	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/tmuxserver"
	"github.com/marcus/sidecar/internal/workspaceinventory"
	"github.com/marcus/sidecar/internal/workspaceops"
)

// The global Workspaces browser binds to the shared liveness rule here, and
// nowhere else. The project plugin has its own one-file binding; the decision
// both of them apply lives in internal/shellliveness (td-6a4100).
//
// The evidence this surface already has is the single `tmux list-panes -a` each
// refresh cycle takes for every project at once. A shell in this tmux namespace
// with no pane in a successful listing is the suspicion; one `list-sessions`
// probe is the confirmation. No new timer, no per-shell polling, and nothing on
// the startup path.
//
// The sequence itself — which evidence may be acted on, which records are in
// scope, and what has to be true again at the moment of the write — moved to
// shellliveness.PlanReap/ConfirmReap/ReapShell when `sidecar host serve` gained
// a reap of its own (td-e3d108). This file is now the binding: it turns this
// surface's cached inventory into an observation, runs the probes as tea.Cmds,
// and drops rows from this surface's projections. There is exactly one
// implementation of the guards, and it is not here.

// shellLivenessProbe, shellLivenessServer, and forgetShell are indirected for tests.
var (
	shellLivenessProbe  = shellliveness.ProbeSession
	shellLivenessServer = tmuxserver.Socket
	forgetShell         = workspaceops.ForgetManagedShell
)

type (
	// shellProbedMsg carries one session's independent second opinion. The
	// probe it answers — including the name-life and server incarnation the
	// suspicion was formed under — travels with it, so nothing about the fence
	// is re-derived on delivery.
	shellProbedMsg struct {
		Probe   shellliveness.ReapProbe
		Verdict shellliveness.Verdict
	}

	// shellForgottenMsg reports the manifest write for a confirmed dead shell.
	shellForgottenMsg struct {
		ProjectKey string
		TmuxName   string
		// Resurrected says the write was skipped because the session was back
		// by the time it ran.
		Resurrected bool
		Err         error
	}
)

func (m *Model) shellLivenessTracker() *shellliveness.Tracker {
	if m.shellLiveness == nil {
		m.shellLiveness = shellliveness.NewTracker()
	}
	return m.shellLiveness
}

// observeTmuxServer is the only place this surface tells the tracker which
// server it is watching. Pane inventory and a live socket-stat on the reap
// path both land here; the shared decision stays in shellliveness (td-388929).
func (m *Model) observeTmuxServer(inc tmuxserver.Incarnation) {
	m.shellLivenessTracker().ObserveServer(inc)
}

// reapDeadShells is called at the end of a completed refresh cycle. It hands
// this cycle's evidence to the shared plan and turns the probes it asks for
// into commands.
//
// Every guard the decision applies — a failed or empty pane listing, a foreign
// tmux namespace, a shell that was never seen running — lives in
// shellliveness.PlanReap and is described there. What belongs here is only the
// shape of this surface's evidence: the single `tmux list-panes -a` the refresh
// cycle already took, and the cached per-project inventory it was correlated
// against.
func (m *Model) reapDeadShells() tea.Cmd {
	obs := shellliveness.ReapObservation{
		// Socket-stat is how a Sidecar running outside tmux notices a restart
		// on this inventory pass. PlanReap records it before its own guards, so
		// a vanished server resets the tracker even on a cycle that decides
		// nothing.
		Server:        shellLivenessServer(),
		Namespace:     tmuxenv.Namespace(),
		ListingFailed: m.tmuxErr != nil,
		Now:           time.Now(),
	}
	// One entry per pane, blank session names included: the empty-listing guard
	// is about whether tmux listed anything at all, so filtering here would
	// change what it sees.
	obs.Panes = make([]string, 0, len(m.currentPanes))
	for _, pane := range m.currentPanes {
		obs.Panes = append(obs.Panes, pane.Session)
	}
	for _, result := range m.results {
		if result.Err != nil {
			continue
		}
		for _, workspace := range result.Workspaces {
			if workspace.Kind != workspaceinventory.KindShell {
				continue
			}
			obs.Shells = append(obs.Shells, shellliveness.Shell{
				ProjectKey:  result.ProjectKey,
				ProjectRoot: result.ProjectRoot,
				TmuxName:    workspace.TmuxName,
				Namespace:   workspace.Namespace,
				CreatedAt:   workspace.CreatedAt,
			})
		}
	}

	plan := shellliveness.PlanReap(m.shellLivenessTracker(), obs)
	if plan.Skipped != "" {
		m.tracef("shell reap skipped: %s", plan.Skipped)
		return nil
	}
	if len(plan.Probes) == 0 {
		return nil
	}
	cmds := make([]tea.Cmd, 0, len(plan.Probes))
	for _, probe := range plan.Probes {
		cmds = append(cmds, func() tea.Msg {
			return shellProbedMsg{Probe: probe, Verdict: shellLivenessProbe(probe.TmuxName)}
		})
	}
	return tea.Batch(cmds...)
}

// applyShellProbe acts only on a confirmed Gone verdict. The row leaves the
// board immediately so the next cycle does not probe it again, and the manifest
// entry is tombstoned through the shared write half rather than by touching
// shells.json here.
//
// Both halves of the resurrection defence live in shellliveness — the
// incarnation fence in ConfirmReap, the fresh re-probe in ReapShell — and are
// described there. What this surface adds is dropping the row between them.
func (m *Model) applyShellProbe(msg shellProbedMsg) tea.Cmd {
	if !shellliveness.ConfirmReap(m.shellLivenessTracker(), shellLivenessServer(), msg.Probe, msg.Verdict) {
		return nil
	}
	m.dropShellRow(msg.Probe.ProjectKey, msg.Probe.TmuxName)
	m.syncBoard()
	probe := msg.Probe
	return func() tea.Msg {
		resurrected, err := shellliveness.ReapShell(shellLivenessProbe, forgetShell, probe)
		// A resurrected shell leaves the manifest alone; the next refresh
		// re-reads shells.json and restores the row.
		return shellForgottenMsg{
			ProjectKey:  probe.ProjectKey,
			TmuxName:    probe.TmuxName,
			Resurrected: resurrected,
			Err:         err,
		}
	}
}

// applyShellForgotten records the outcome of the write.
//
// A failed or refused write leaves the entry on disk while the row is already
// gone from this board, and nothing retries it: Confirm dropped the tracker
// state, so the entry re-read on the next full refresh has no observed liveness
// and can never be probed again. That is the correct direction to fail in — a
// stale row costs a line on screen, and the alternative is a delete that keeps
// trying — but it is worth saying plainly rather than implying a retry that
// does not exist.
func (m *Model) applyShellForgotten(msg shellForgottenMsg) tea.Cmd {
	switch {
	case msg.Resurrected:
		m.tracef("shell forget project=%s shell=%s skipped=resurrected", msg.ProjectKey, msg.TmuxName)
	case msg.Err != nil:
		m.tracef("shell forget project=%s shell=%s err=%v", msg.ProjectKey, msg.TmuxName, msg.Err)
	}
	return nil
}

// dropShellRow removes one shell from the cached inventory and from every
// projection built over it.
func (m *Model) dropShellRow(projectKey, tmuxName string) {
	for _, store := range []map[string]workspaceinventory.ProjectResult{m.results, m.statusInputs, m.inventoryResults} {
		result, ok := store[projectKey]
		if !ok {
			continue
		}
		kept := make([]workspaceinventory.Workspace, 0, len(result.Workspaces))
		for _, workspace := range result.Workspaces {
			if workspace.Kind == workspaceinventory.KindShell && workspace.TmuxName == tmuxName {
				continue
			}
			kept = append(kept, workspace)
		}
		result.Workspaces = kept
		store[projectKey] = result
	}
}
