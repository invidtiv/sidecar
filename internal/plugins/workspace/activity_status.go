package workspace

import (
	"log/slog"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
)

// applyAgentActivity is the only semantic status mutation path for providers
// supported by agentactivity. WorktreeStatus remains a product/kanban model;
// its supported-provider values are projections of semantic activity.
func applyAgentActivity(agent *Agent, result agentactivity.Result, capturedAt, now time.Time) bool {
	if agent == nil || !supportsAgentActivity(agent.Type) {
		return false
	}
	prior := agent.Activity.State
	if capturedAt.IsZero() {
		capturedAt = now
	}
	changed := agent.Activity.Apply(result, now)
	agent.ActivityCapturedAt = capturedAt
	if !changed {
		return false
	}
	age := now.Sub(capturedAt)
	if age < 0 {
		age = 0
	}
	slog.Debug("agent activity transition",
		"agent", agent.Type,
		"prior", prior,
		"new", agent.Activity.State,
		"evidence", agent.Activity.Evidence,
		"capture_age", age,
	)
	return true
}

func worktreeStatusForActivity(agent *Agent, fallback WorktreeStatus) WorktreeStatus {
	if agent == nil || !supportsAgentActivity(agent.Type) {
		return fallback
	}
	switch agent.Activity.State {
	case agentactivity.StateWorking:
		return StatusActive
	case agentactivity.StateBlocked, agentactivity.StateIdle:
		return StatusWaiting
	default:
		return fallback
	}
}
