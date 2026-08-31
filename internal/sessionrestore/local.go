package sessionrestore

import (
	"context"
	"time"

	"github.com/marcus/sidecar/internal/agentcontrol"
	"github.com/marcus/sidecar/internal/agentsession"
	"github.com/marcus/sidecar/internal/workspaceops"
)

// The local binding of the executor to this machine.
//
// It lives here, and not in the CLI, because the CLI and the automatic startup
// restore must be the same restore. Two bindings would be two answers to "what
// does restoring actually do", and the difference would show up exactly once, in
// the situation nobody can reproduce.

// LocalDepsOptions tunes the timeouts of the local binding.
type LocalDepsOptions struct {
	// Namespace is the tmux socket path the restore is scoped to.
	Namespace string
	// ShellReady bounds the wait for a recreated shell to settle before a
	// resume is sent into it. Zero uses a sensible default.
	ShellReady time.Duration
	// ResumeReady bounds the wait for the provider to identify itself and
	// become ready. Zero uses a sensible default. There is no implicit
	// unbounded wait anywhere in this path.
	ResumeReady time.Duration
}

// LocalDeps binds the executor to real tmux, real shell creation, and real
// provider resume.
func LocalDeps(opts LocalDepsOptions) Deps {
	if opts.ShellReady <= 0 {
		opts.ShellReady = 30 * time.Second
	}
	if opts.ResumeReady <= 0 {
		opts.ResumeReady = 60 * time.Second
	}
	collector := Collector{Namespace: opts.Namespace}
	svc := agentcontrol.Service{Terminal: agentcontrol.NewLocalTerminal()}

	return Deps{
		Live: func(session string) LiveState {
			if !workspaceops.SessionExists(session) {
				return LiveAbsent
			}
			// A live name is only "ours" if the session itself says so. Deciding
			// this from the name would make the collision refusal decorative:
			// anything could take a Sidecar-shaped name and be converged onto.
			if collector.ManagedSessionOrDefault(context.Background(), session) {
				return LiveManaged
			}
			return LiveForeign
		},
		CurrentServer: collector.ServerIDOrDefault,
		CreateShell: func(_ context.Context, step Step) error {
			// CreateShell, not CreateManagedShell: the manifest record already
			// exists and is the thing being restored. Adding it again would fail,
			// and adding it with a fresh CreatedAt would discard the identity
			// every other fence in the system is keyed on.
			_, err := workspaceops.CreateShell(workspaceops.ShellSpec{
				WorkDir:     step.WorkDir,
				SessionName: step.Session,
				DisplayName: step.Name,
			})
			return err
		},
		ResumePlanFor: func(step Step) (agentsession.ResumePlan, error) {
			return ResumePlanFor(step, opts.Namespace)
		},
		ResumeAgent: func(ctx context.Context, step Step, plan agentsession.ResumePlan) error {
			target := agentcontrol.Target{
				Host:      "local",
				Project:   step.Project,
				Session:   step.Session,
				Name:      step.Name,
				Namespace: opts.Namespace,
			}
			ready, err := svc.WaitShellReady(ctx, target, opts.ShellReady)
			if err != nil {
				return err
			}
			_, err = svc.StartResume(ctx, agentcontrol.ResumeRequest{
				Target:  ready.Target,
				Plan:    plan,
				Timeout: opts.ResumeReady,
			})
			return err
		},
	}
}
