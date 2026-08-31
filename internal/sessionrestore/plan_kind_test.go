package sessionrestore

import (
	"errors"
	"fmt"
	"testing"

	"github.com/marcus/sidecar/internal/agentsession"
	"github.com/marcus/sidecar/internal/shellstate"
)

// poisoned is the record the shipped kind-binding bug produced and that no
// migration can repair: the shell was created for one provider, and a hook
// belonging to a second provider reported a conversation into it before the
// bind-time gate existed. AgentType names the first, Agent.Kind the second, and
// the conversation id belongs to whichever one actually ran.
func poisoned(created, reportedBy, value string) func(*shellstate.Definition) {
	return func(d *shellstate.Definition) {
		withAgent(reportedBy, value, true)(d)
		d.AgentType = created
	}
}

// TestAKindDisagreementRefusesResume is the whole remediation. Readers prefer
// Agent.Kind, so before this an affected user's cold restore offered
// `claude --resume <grok conversation id>` — the exact failure the two fields
// exist to prevent, arriving through the one path that runs a provider
// unattended.
func TestAKindDisagreementRefusesResume(t *testing.T) {
	// --yes and an auto policy: everything that could authorize a resume is
	// present, so the only thing that can stop it is the refusal under test.
	in := baseInput(shell("sidecar-sh-p-1", poisoned("grok", "claude", "grok-session-1")))
	in.Config.ResumeAgents = ResumeAuto
	in.Request.Confirmed = true

	plan := Build(in)
	if len(plan.Steps) != 1 {
		t.Fatalf("steps = %d", len(plan.Steps))
	}
	step := plan.Steps[0]

	if step.Agent == nil {
		t.Fatal("the poisoned record produced no agent verdict at all")
	}
	if step.Agent.Resume {
		t.Fatal("a record naming two providers was resumed")
	}
	if step.Agent.Reason != ReasonKindDisagreement {
		t.Fatalf("agent reason = %q, want %q", step.Agent.Reason, ReasonKindDisagreement)
	}

	// The shell itself is not punished for the record's confusion.
	if step.Action != ActionRecreateShell {
		t.Fatalf("action = %q, want the shell still recreated", step.Action)
	}
	if step.ExternalExecution {
		t.Fatal("a refused resume still claimed it would run an agent")
	}
	if step.NeedsConfirmation {
		t.Fatal("a refused resume was offered as a confirmable one")
	}
	if len(plan.Executable()) != 1 || plan.WouldExecuteAgents() {
		t.Fatalf("executable=%d wouldExecuteAgents=%v", len(plan.Executable()), plan.WouldExecuteAgents())
	}

	// The sentence has to name both providers: the record is not rewritten by a
	// restore, so this is the only thing telling the user what to fix.
	for _, want := range []string{"grok", "claude"} {
		if !contains(step.Detail, want) {
			t.Fatalf("detail %q does not name %q", step.Detail, want)
		}
	}
}

// The same record with everything else that could refuse removed must still
// refuse, and for this reason rather than an incidental one. If the
// disagreement check is deleted this row resumes, which is what makes the test
// above a tripwire rather than a description.
func TestAKindDisagreementIsCheckedBeforeEveryOtherResumeQuestion(t *testing.T) {
	in := baseInput(shell("sidecar-sh-p-1", poisoned("grok", "claude", "grok-session-1")))
	in.Config.ResumeAgents = ResumeAuto
	in.Request.Confirmed = true

	// Prove the counterfactual: the identical record, healed so the two fields
	// agree, resumes under exactly this input.
	healthy := baseInput(shell("sidecar-sh-p-1", withAgent("claude", "grok-session-1", true)))
	healthy.Config.ResumeAgents = ResumeAuto
	healthy.Request.Confirmed = true
	control := Build(healthy).Steps[0]
	if control.Agent == nil || !control.Agent.Resume || control.Action != ActionResumeAgent {
		t.Fatalf("the control record did not resume, so the refusal below proves nothing: %+v", control.Agent)
	}

	step := Build(in).Steps[0]
	if step.Agent.Resume || step.Agent.Reason != ReasonKindDisagreement {
		t.Fatalf("agent verdict = %+v, want a kind_disagreement refusal", step.Agent)
	}
}

// A record that can never resume must not win deduplication either: letting it
// take the key would cost a healthy shell its own conversation and leave both
// of them unrestored.
func TestAKindDisagreementDoesNotTakeTheConversationFromAHealthyShell(t *testing.T) {
	// Sorted by session name, so the poisoned one is considered first and would
	// have won the dedup key under the old rule.
	in := baseInput(
		shell("sidecar-sh-a", poisoned("grok", "claude", "shared-session")),
		shell("sidecar-sh-b", withAgent("claude", "shared-session", true)),
	)
	in.Config.ResumeAgents = ResumeAuto
	in.Request.Confirmed = true

	plan := Build(in)
	byName := map[string]Step{}
	for _, s := range plan.Steps {
		byName[s.Session] = s
	}

	if got := byName["sidecar-sh-a"]; got.Agent.Resume || got.Agent.Reason != ReasonKindDisagreement {
		t.Fatalf("the poisoned shell = %+v, want a kind_disagreement refusal", got.Agent)
	}
	healthy := byName["sidecar-sh-b"]
	if !healthy.Agent.Resume || healthy.Agent.Reason != ReasonPolicyResume {
		t.Fatalf("the healthy shell = %+v, want it to resume", healthy.Agent)
	}
	if healthy.Agent.ConflictWith != "" {
		t.Fatalf("the healthy shell lost deduplication to a record that can never resume: %q",
			healthy.Agent.ConflictWith)
	}
}

// The executor re-reads the record at the moment of mutation and gets the same
// refusal through the other door. Mapping it to the generic provider-rejected
// reason would hide a corrupt record behind a capability answer.
func TestTheExecutorsKindDisagreementMapsToItsOwnReason(t *testing.T) {
	err := fmt.Errorf("reading the binding: %w", shellstate.ErrKindDisagreement)
	if got := resumeRefusal(err); got != ReasonKindDisagreement {
		t.Fatalf("resumeRefusal(ErrKindDisagreement) = %q, want %q", got, ReasonKindDisagreement)
	}
	// The neighbouring mappings are unchanged.
	if got := resumeRefusal(fmt.Errorf("x: %w", agentsession.ErrUnsupportedKind)); got != ReasonProviderNoResume {
		t.Fatalf("ErrUnsupportedKind mapped to %q", got)
	}
	if got := resumeRefusal(errors.New("something else")); got != ReasonProviderRejectedRef {
		t.Fatalf("an unrecognised error mapped to %q", got)
	}
}

// Records every current writer produces are self-consistent, and none of them
// may be dragged into the refusal.
func TestSelfConsistentRecordsAreUntouchedByTheDisagreementCheck(t *testing.T) {
	cases := []struct {
		name  string
		shell Shell
	}{
		{"a v3 record", shell("sidecar-sh-p-1", withAgent("codex", "codex-session-1", true))},
		{"a v2 record with only agentType", shell("sidecar-sh-p-1", func(d *shellstate.Definition) {
			withAgent("codex", "codex-session-1", true)(d)
			d.Agent.Kind = ""
		})},
		{"a v3 record with no agentType", shell("sidecar-sh-p-1", func(d *shellstate.Definition) {
			withAgent("codex", "codex-session-1", true)(d)
			d.AgentType = ""
		})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := baseInput(tc.shell)
			in.Config.ResumeAgents = ResumeAuto
			step := Build(in).Steps[0]
			if step.Agent == nil || step.Agent.Reason == ReasonKindDisagreement {
				t.Fatalf("a healthy record was refused: %+v", step.Agent)
			}
			if !step.Agent.Resume {
				t.Fatalf("a healthy record did not resume: %+v", step.Agent)
			}
		})
	}
}
