package agentbroadcast

import (
	"context"

	"github.com/marcus/sidecar/internal/managedtarget"
)

// CandidatesFromState is the Plan candidate source for both the CLI verb and
// the TUI modal: one scan of stateDir, filtered by the request's scope.
func CandidatesFromState(stateDir string) func(context.Context, PlanRequest) ([]managedtarget.Target, error) {
	return func(ctx context.Context, req PlanRequest) ([]managedtarget.Target, error) {
		projectKey := ""
		if req.ScopeKind == ScopeProject && len(req.To) == 0 {
			projectKey = req.Project
		}
		return managedtarget.List(ctx, stateDir, projectKey)
	}
}

// WithRequested treats keep=true rows as would_send (so Send will attempt them
// and Prompt may refuse again) and keep=false would_send rows as skipped.
// Already-skipped unchecked rows keep their reason.
func (p Plan) WithRequested(keep func(Recipient) bool) Plan {
	out := p
	if len(p.Recipients) == 0 {
		return out
	}
	out.Recipients = append([]Recipient(nil), p.Recipients...)
	for i, row := range out.Recipients {
		if keep(row) {
			out.Recipients[i].Outcome = OutcomeWouldSend
			out.Recipients[i].Reason = nil
			continue
		}
		if row.Outcome == OutcomeWouldSend {
			out.Recipients[i].Outcome = OutcomeSkipped
			out.Recipients[i].Reason = &Reason{Code: "excluded", Message: "deselected"}
		}
	}
	return out
}

// RecipientID is the stable checklist key for a plan row.
func RecipientID(row Recipient) string {
	return row.Target.Project + "\x1f" + row.Target.Session
}
