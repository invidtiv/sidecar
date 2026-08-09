package workspace

import (
	"log/slog"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
)

// applyObservedAgentType updates only the live pane identity. Launch preference
// remains in ChosenAgentType/ChosenAgent, so exiting an agent does not rewrite
// how a future Sidecar-created session starts. A provider transition resets its
// semantic tracker so state from the previous program cannot leak across.
func applyObservedAgentType(agent *Agent, observed AgentType, now time.Time) bool {
	if agent == nil || observed == "" || observed == agent.Type {
		return false
	}
	prior := agent.Type
	agent.Type = observed
	if now.IsZero() {
		now = time.Now()
	}
	agent.Activity.ResetForProcessChange(now)
	agent.ActivityCapturedAt = time.Time{}
	agent.WaitingFor = ""
	slog.Debug("live pane identity changed", "prior", prior, "new", observed)
	return true
}

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
	if agent == nil {
		return fallback
	}
	if agent.Type == AgentShell {
		return StatusPaused
	}
	if !supportsAgentActivity(agent.Type) {
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
