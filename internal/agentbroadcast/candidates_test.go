package agentbroadcast

import (
	"testing"
)

func TestWithRequestedPromotesAndExcludes(t *testing.T) {
	plan := Plan{Recipients: []Recipient{
		{Target: controlTarget(managed("a", "alpha", "p")), Outcome: OutcomeWouldSend},
		{Target: controlTarget(managed("b", "beta", "p")), Outcome: OutcomeSkipped, Reason: &Reason{Code: "agent_blocked", Message: "blocked"}},
	}}
	got := plan.WithRequested(func(row Recipient) bool {
		return row.Target.Session == "b"
	})
	if got.Recipients[0].Outcome != OutcomeSkipped || got.Recipients[0].Reason == nil || got.Recipients[0].Reason.Code != "excluded" {
		t.Fatalf("deselected would_send = %+v", got.Recipients[0])
	}
	if got.Recipients[1].Outcome != OutcomeWouldSend || got.Recipients[1].Reason != nil {
		t.Fatalf("selected skipped = %+v", got.Recipients[1])
	}
}
