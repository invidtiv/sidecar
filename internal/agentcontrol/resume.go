package agentcontrol

import (
	"context"
	"time"

	"github.com/marcus/sidecar/internal/agentsession"
)

// ResumeRequest asks for an exact conversation to be resumed in a managed shell.
//
// There is no session-value field and no command string, only the validated Ref
// and the argv agentsession already built from it. That is the whole point of
// the type: a conversation identifier reaches a terminal exactly once, as a
// single argv element that was constructed by the provider catalog and never
// concatenated into anything a shell will parse.
type ResumeRequest struct {
	// Target is the managed shell to resume into.
	Target Target
	// Plan is the structured resume from agentsession.PlanResume. Callers do not
	// assemble Argv themselves; taking the plan whole is what keeps the kind, the
	// argv, and the reference they describe from drifting apart.
	Plan agentsession.ResumePlan
	// Timeout bounds the wait for the provider to identify itself and settle.
	// There is no implicit timeout; zero is refused.
	Timeout time.Duration
}

// StartResume launches a provider's native resume in a managed shell and returns
// only once that provider is positively identified and ready.
//
// It is deliberately a thin composition over Start rather than a second launch
// path. Every rule Start enforces is a rule a resume needs at least as much: the
// pane must be occupied by its own interactive shell and nothing else, the
// occupant is pinned so a replacement cannot satisfy the wait, a different
// provider is a kind mismatch rather than a success, an early exit is a start
// failure, and blocked is reported as not-ready with the target left
// inspectable. A resume that invented its own readiness contract would be the
// one launch path in Sidecar that could report success into an empty pane.
//
// What this adds is the refusal that belongs to resuming specifically: a plan
// whose reference was never vouched for by an official integration is rejected
// before any bytes are sent. agentsession.PlanResume already refuses to build
// such a plan, so this is a second check on the same rule at the boundary where
// the bytes would actually be written — worth having, because this function is
// callable with a hand-built plan and the cost of being wrong here is a provider
// resuming somebody else's conversation.
func (s Service) StartResume(ctx context.Context, req ResumeRequest) (Agent, error) {
	if req.Plan.Ref.Empty() {
		return Agent{}, &Error{
			Code:    ErrNotReady,
			Message: "there is no session reference to resume",
			Target:  &req.Target,
		}
	}
	if !req.Plan.Ref.Reported {
		return Agent{}, &Error{
			Code:    ErrNotReady,
			Message: "that session reference was not reported by an official Sidecar integration, so it is not auto-resumable",
			Target:  &req.Target,
		}
	}
	if len(req.Plan.Argv) == 0 {
		return Agent{}, &Error{
			Code:    ErrNotReady,
			Message: "the resume plan carries no command",
			Target:  &req.Target,
		}
	}
	return s.Start(ctx, StartRequest{
		Target:  req.Target,
		Kind:    req.Plan.Kind,
		Argv:    req.Plan.Argv,
		Timeout: req.Timeout,
	})
}
