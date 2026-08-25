package overview

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/shellliveness"
	"github.com/marcus/sidecar/internal/tmuxenv"
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

// shellLivenessProbe and forgetShell are indirected for tests.
var (
	shellLivenessProbe = shellliveness.ProbeSession
	forgetShell        = workspaceops.ForgetManagedShell
)

type (
	// shellProbedMsg carries one session's independent second opinion.
	shellProbedMsg struct {
		ProjectKey  string
		ProjectRoot string
		TmuxName    string
		Namespace   string
		// CreatedAt and Incarnation both identify the life this verdict is
		// about. tmux names are reused, so a verdict outlives the thing it
		// judged: CreatedAt fences a replacement entry written to the manifest,
		// Incarnation fences a session this browser saw come back.
		CreatedAt   time.Time
		Incarnation uint64
		Verdict     shellliveness.Verdict
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

// reapDeadShells is called at the end of a completed refresh cycle. It records
// which shells this cycle saw alive and probes the ones that have gone missing
// since they were last seen.
//
// Three things keep it conservative:
//
//   - A tmux inventory that failed (m.tmuxErr) is no evidence about anything,
//     so the whole pass is skipped. So is an empty one: the collector reports a
//     server that is not running as zero panes and no error, which is the exact
//     shape of a tmux restart and would otherwise suspect every shell at once.
//     The probe would answer Unknown for all of them, but a guard that only
//     works because the last line of defence holds is not a guard. This skip
//     stays as a cheap belt even after server incarnation is a first-class
//     identity; incarnation gating (td-388929) is the real fence.
//   - A shell in another tmux namespace is invisible to this listing, so its
//     absence means nothing.
//   - A shell this browser never saw running is left alone. That is what a
//     manifest entry looks like after a reboot, and the offline-shell recreate
//     path — not auto-close — owns it.
func (m *Model) reapDeadShells() tea.Cmd {
	if m.tmuxErr != nil || len(m.currentPanes) == 0 {
		return nil
	}
	namespace := tmuxenv.Namespace()
	if namespace == "" {
		return nil
	}
	live := make(map[string]bool, len(m.currentPanes))
	for _, pane := range m.currentPanes {
		live[pane.Session] = true
	}
	tracker := m.shellLivenessTracker()
	now := time.Now()
	var cmds []tea.Cmd
	for _, result := range m.results {
		if result.Err != nil {
			continue
		}
		for _, workspace := range result.Workspaces {
			if workspace.Kind != workspaceinventory.KindShell || workspace.TmuxName == "" {
				continue
			}
			if workspace.Namespace != namespace {
				continue
			}
			if live[workspace.TmuxName] {
				tracker.Observe(workspace.TmuxName)
				continue
			}
			if !tracker.ShouldProbe(workspace.TmuxName, now) {
				continue
			}
			probe := shellProbedMsg{
				ProjectKey:  result.ProjectKey,
				ProjectRoot: result.ProjectRoot,
				TmuxName:    workspace.TmuxName,
				Namespace:   workspace.Namespace,
				CreatedAt:   workspace.CreatedAt,
				Incarnation: tracker.Incarnation(workspace.TmuxName),
			}
			cmds = append(cmds, func() tea.Msg {
				probe.Verdict = shellLivenessProbe(probe.TmuxName)
				return probe
			})
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// applyShellProbe acts only on a confirmed Gone verdict. The row leaves the
// board immediately so the next cycle does not probe it again, and the manifest
// entry is dropped through the shared shell operation rather than by writing
// shells.json here.
//
// A verdict is a statement about the moment it was taken. Between that moment
// and this write the user can have brought the same tmux name back — pressing
// Enter on an offline row recreates the session under its old name — and
// deleting the identity of a shell that is now alive is the one outcome this
// feature must never produce. Two independent things prevent it: the probe is
// repeated inside the write command, so the evidence is fresh at the moment of
// the deletion, and the write itself is conditional on the incarnation, checked
// under the same lock a creating write takes.
func (m *Model) applyShellProbe(msg shellProbedMsg) tea.Cmd {
	if !m.shellLivenessTracker().Confirm(msg.TmuxName, msg.Verdict, msg.Incarnation) {
		return nil
	}
	m.dropShellRow(msg.ProjectKey, msg.TmuxName)
	m.syncBoard()
	probe := msg
	return func() tea.Msg {
		if shellLivenessProbe(probe.TmuxName) != shellliveness.Gone {
			// It came back while we were deciding. Leave the manifest alone;
			// the next refresh re-reads shells.json and restores the row.
			return shellForgottenMsg{ProjectKey: probe.ProjectKey, TmuxName: probe.TmuxName, Resurrected: true}
		}
		return shellForgottenMsg{
			ProjectKey: probe.ProjectKey,
			TmuxName:   probe.TmuxName,
			Err:        forgetShell(probe.ProjectRoot, probe.TmuxName, probe.Namespace, probe.CreatedAt),
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
