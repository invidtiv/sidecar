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
		Verdict     shellliveness.Verdict
	}

	// shellForgottenMsg reports the manifest write for a confirmed dead shell.
	shellForgottenMsg struct {
		ProjectKey string
		TmuxName   string
		Err        error
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
//     so the whole pass is skipped.
//   - A shell in another tmux namespace is invisible to this listing, so its
//     absence means nothing.
//   - A shell this browser never saw running is left alone. That is what a
//     manifest entry looks like after a reboot, and the offline-shell recreate
//     path — not auto-close — owns it.
func (m *Model) reapDeadShells() tea.Cmd {
	if m.tmuxErr != nil {
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
			if !tracker.SeenAlive(workspace.TmuxName) || !tracker.ShouldProbe(workspace.TmuxName, now) {
				continue
			}
			probe := shellProbedMsg{
				ProjectKey:  result.ProjectKey,
				ProjectRoot: result.ProjectRoot,
				TmuxName:    workspace.TmuxName,
				Namespace:   workspace.Namespace,
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
func (m *Model) applyShellProbe(msg shellProbedMsg) tea.Cmd {
	if !m.shellLivenessTracker().Confirm(msg.TmuxName, msg.Verdict) {
		return nil
	}
	m.dropShellRow(msg.ProjectKey, msg.TmuxName)
	m.syncBoard()
	root, tmux, namespace, key := msg.ProjectRoot, msg.TmuxName, msg.Namespace, msg.ProjectKey
	return func() tea.Msg {
		return shellForgottenMsg{ProjectKey: key, TmuxName: tmux, Err: forgetShell(root, tmux, namespace)}
	}
}

// applyShellForgotten restores nothing on failure: the session really is gone,
// and a manifest that could not be written this cycle is retried the next time
// the entry reappears from disk.
func (m *Model) applyShellForgotten(msg shellForgottenMsg) tea.Cmd {
	if msg.Err != nil {
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
