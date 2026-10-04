package workspaceops

import (
	"context"
	"time"

	"github.com/marcus/sidecar/internal/agentcontrol"
)

// AgentStartStage identifies the failed phase without prescribing a surface's
// error wording. A successful start returns the empty stage.
type AgentStartStage string

const (
	AgentWaitReady     AgentStartStage = "wait_ready"
	AgentStartProvider AgentStartStage = "start_provider"
)

// AgentLauncher runs the common readiness-then-start sequence. Callers supply
// the resolved target and argv, and retain their presentation and naming rules.
// The zero value uses one local agentcontrol service for both phases.
type AgentLauncher struct {
	Wait       func(context.Context, agentcontrol.Target, time.Duration) (agentcontrol.Snapshot, error)
	StartAgent func(context.Context, agentcontrol.StartRequest) (agentcontrol.Agent, error)
}

// Start waits only when requested: reconnecting worktree sessions already have
// an occupant that Start must inspect rather than waiting for an idle shell.
// useReadyTarget preserves callers that pin the physical identity observed
// during readiness. Other callers keep their existing requested identity.
// Failures retain the original error and never retry or submit after a failed
// readiness check.
func (l AgentLauncher) Start(ctx context.Context, request agentcontrol.StartRequest, waitReady, useReadyTarget bool) (agentcontrol.Agent, AgentStartStage, error) {
	wait, start := l.Wait, l.StartAgent
	if wait == nil || start == nil {
		svc := agentcontrol.Service{Terminal: agentcontrol.NewLocalTerminal()}
		if wait == nil {
			wait = svc.WaitShellReady
		}
		if start == nil {
			start = svc.Start
		}
	}
	if waitReady {
		ready, err := wait(ctx, request.Target, request.Timeout)
		if err != nil {
			return agentcontrol.Agent{}, AgentWaitReady, err
		}
		if useReadyTarget {
			request.Target = ready.Target
		}
	}
	agent, err := start(ctx, request)
	if err != nil {
		return agent, AgentStartProvider, err
	}
	return agent, "", nil
}
