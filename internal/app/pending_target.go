package app

import (
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/plugin"
)

// pendingActivation is the single hand-off slot across a project switch.
//
// switchProject does a full registry.Reinit, which destroys every plugin's
// state, so anything the user asked for *before* the switch has to be re-stated
// *after* it. There is exactly one slot and one apply site, deliberately: a
// second parallel mechanism is how a hand-off quietly stops happening. A newer
// jump supersedes an older one rather than queueing, because the user's last
// request is the one they are waiting to see.
//
// Two payloads share the slot because they are the same moment, not the same
// shape:
//
//   - target — an ActivateTargetMsg re-emitted against the rebuilt registry.
//     It is re-emitted rather than executed inline so the landing goes through
//     the ordinary activation route, guards and all.
//   - selection — the workspace plugin's post-Reinit selection, which is
//     applied synchronously because the plugin must already be pointing at the
//     right row when its first frame is painted.
//
// The slot is cleared when it is applied, and on any user navigation: a user
// who moved on is not waiting for a jump they no longer asked for.
type pendingActivation struct {
	target    *ActivateTargetMsg
	selection *plugin.PendingWorkspaceSelection
}

// setPendingActivation stores a hand-off, replacing whatever was there. Newest
// wins.
func (m *Model) setPendingActivation(pending pendingActivation) {
	m.pendingActivation = &pending
}

// clearPendingActivation drops an unapplied hand-off. Call it wherever the user
// navigates by hand.
func (m *Model) clearPendingActivation() {
	m.pendingActivation = nil
}

// takePendingActivation reads and clears the slot.
func (m *Model) takePendingActivation() *pendingActivation {
	pending := m.pendingActivation
	m.pendingActivation = nil
	return pending
}

// applyPendingActivation lands whatever the slot holds against the current
// (possibly just rebuilt) registry and returns the commands the landing needs.
// It is the one apply site; callers add the result to their command batch.
func (m *Model) applyPendingActivation() []tea.Cmd {
	pending := m.takePendingActivation()
	if pending == nil {
		return nil
	}
	var cmds []tea.Cmd
	if selection := pending.selection; selection != nil {
		if selector, ok := m.registry.Get(workspacePluginID).(plugin.PendingWorkspaceSelector); ok {
			selector.SetPendingWorkspaceSelection(*selection)
		}
		if provider, ok := m.registry.Get(workspacePluginID).(plugin.PendingWorkspaceActionProvider); ok {
			if cmd := provider.TakePendingWorkspaceAction(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}
	if target := pending.target; target != nil {
		landing := *target
		// The project qualifier has been honoured by now; re-emitting it would
		// send the jump back through project resolution against the project it
		// just landed in.
		landing.Project = ""
		cmds = append(cmds, func() tea.Msg { return landing })
	}
	return cmds
}
