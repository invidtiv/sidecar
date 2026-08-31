package sessionrestore

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentsession"
	"github.com/marcus/sidecar/internal/shellstate"
)

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }

const (
	priorServer   = "pid=1001"
	currentServer = "pid=2002"
)

// shell builds a candidate record that is eligible in the prior server, which
// is the ordinary cold-restore case every table row varies from.
func shell(name string, mutate ...func(*shellstate.Definition)) Shell {
	def := shellstate.Definition{
		TmuxName:    name,
		DisplayName: name + "-display",
		WorkDir:     "/repo/" + name,
		CreatedAt:   time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC),
		Restore: &shellstate.RestoreState{
			Eligible:        true,
			LastSeenServer:  priorServer,
			LastSeenAliveAt: time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC),
		},
	}
	for _, m := range mutate {
		m(&def)
	}
	return Shell{Project: "sidecar", ManifestPath: "/state/sidecar/shells.json", Def: def}
}

func withAgent(kind, value string, reported bool) func(*shellstate.Definition) {
	return func(d *shellstate.Definition) {
		d.AgentType = kind
		d.Agent = &shellstate.AgentBinding{
			Kind: kind,
			Session: &agentsession.Ref{
				Kind:       agentsession.RefID,
				Value:      value,
				Source:     agentsession.OfficialSourceFor(kind),
				Reported:   reported,
				ReportedAt: time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC),
			},
		}
	}
}

func withPolicy(p agentsession.Policy) func(*shellstate.Definition) {
	return func(d *shellstate.Definition) { d.Restore.Policy = p }
}

func baseInput(shells ...Shell) Input {
	return Input{
		Config:        DefaultConfig(),
		CurrentServer: currentServer,
		Live:          map[string]LiveState{},
		Shells:        shells,
		DirExists:     func(string) bool { return true },
		// Every provider is installed unless a row says otherwise.
		ProviderAvailable: func(string) bool { return true },
		Request:           Request{Agents: true},
	}
}

func only(t *testing.T, p Plan) Step {
	t.Helper()
	if len(p.Steps) != 1 {
		t.Fatalf("want exactly 1 step, got %d: %+v", len(p.Steps), p.Steps)
	}
	return p.Steps[0]
}

// TestPlanShellVerdictTable is the plan's restore-plan table for the shell-level
// decision: what happens to the terminal itself, before any agent is considered.
func TestPlanShellVerdictTable(t *testing.T) {
	tests := []struct {
		name       string
		input      func() Input
		wantAction Action
		wantReason Reason
	}{
		{
			name: "live managed session reattaches and creates nothing",
			input: func() Input {
				in := baseInput(shell("a"))
				in.Live["a"] = LiveManaged
				return in
			},
			wantAction: ActionReattach,
			wantReason: ReasonAlreadyLive,
		},
		{
			name: "a foreign holder of the name is refused, never killed",
			input: func() Input {
				in := baseInput(shell("a"))
				in.Live["a"] = LiveForeign
				return in
			},
			wantAction: ActionRefuse,
			wantReason: ReasonNameCollision,
		},
		{
			name:  "eligible in a replaced server is recreated",
			input: func() Input { return baseInput(shell("a")) },

			wantAction: ActionRecreateShell,
			wantReason: ReasonRecreatable,
		},
		{
			name: "never confirmed live is left for the user, not resurrected",
			input: func() Input {
				return baseInput(shell("a", func(d *shellstate.Definition) { d.Restore = nil }))
			},
			wantAction: ActionManual,
			wantReason: ReasonNotPriorLive,
		},
		{
			name: "recorded but not eligible is left for the user",
			input: func() Input {
				return baseInput(shell("a", func(d *shellstate.Definition) { d.Restore.Eligible = false }))
			},
			wantAction: ActionManual,
			wantReason: ReasonNotPriorLive,
		},
		{
			name: "died inside the running server is not revived",
			input: func() Input {
				return baseInput(shell("a", func(d *shellstate.Definition) { d.Restore.LastSeenServer = currentServer }))
			},
			wantAction: ActionManual,
			wantReason: ReasonDiedInThisServer,
		},
		{
			name: "a deleted worktree is refused with no fallback directory",
			input: func() Input {
				in := baseInput(shell("a"))
				in.DirExists = func(string) bool { return false }
				return in
			},
			wantAction: ActionRefuse,
			wantReason: ReasonMissingWorkDir,
		},
		{
			name: "a record with no working directory is refused",
			input: func() Input {
				return baseInput(shell("a", func(d *shellstate.Definition) { d.WorkDir = "" }))
			},
			wantAction: ActionRefuse,
			wantReason: ReasonNoWorkDir,
		},
		{
			name:  "per-shell never policy skips",
			input: func() Input { return baseInput(shell("a", withPolicy(agentsession.PolicyNever))) },

			wantAction: ActionSkip,
			wantReason: ReasonPolicyNever,
		},
		{
			name: "recreateShells off skips",
			input: func() Input {
				in := baseInput(shell("a"))
				in.Config.RecreateShells = false
				return in
			},
			wantAction: ActionSkip,
			wantReason: ReasonRecreateDisabled,
		},
		{
			name: "an unselected shell is accounted for, not omitted",
			input: func() Input {
				in := baseInput(shell("a"))
				in.Request.OnlyShell = "b"
				return in
			},
			wantAction: ActionSkip,
			wantReason: ReasonNotSelected,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			step := only(t, Build(tc.input()))
			if step.Action != tc.wantAction || step.Reason != tc.wantReason {
				t.Fatalf("got %s/%s, want %s/%s (detail %q)", step.Action, step.Reason, tc.wantAction, tc.wantReason, step.Detail)
			}
			if step.Detail == "" {
				t.Error("every verdict must carry a human sentence alongside its code")
			}
		})
	}
}

// TestPlanNeverKillsToTakeAName pins the safety rule as behavior rather than as
// a comment: a name collision produces a refusal and no executable step at all,
// so there is nothing for an executor to act on even if it wanted to.
func TestPlanNeverKillsToTakeAName(t *testing.T) {
	in := baseInput(shell("a"))
	in.Live["a"] = LiveForeign
	plan := Build(in)
	if got := plan.Executable(); len(got) != 0 {
		t.Fatalf("a name collision must produce no executable step, got %+v", got)
	}
	if plan.WouldExecuteAgents() {
		t.Error("a refused shell must not report that it would run an agent")
	}
}

// TestPlanAgentVerdictTable is the resume half of the matrix.
func TestPlanAgentVerdictTable(t *testing.T) {
	tests := []struct {
		name         string
		input        func() Input
		wantAction   Action
		wantResume   bool
		wantReason   Reason
		wantExternal bool
	}{
		{
			name: "auto policy resumes an exact reported reference",
			input: func() Input {
				in := baseInput(shell("a", withAgent("codex", "sess-1", true)))
				in.Config.ResumeAgents = ResumeAuto
				return in
			},
			wantAction:   ActionResumeAgent,
			wantResume:   true,
			wantReason:   ReasonPolicyResume,
			wantExternal: true,
		},
		{
			name:         "ask policy without confirmation plans the shell and holds the agent",
			input:        func() Input { return baseInput(shell("a", withAgent("codex", "sess-1", true))) },
			wantAction:   ActionRecreateShell,
			wantResume:   false,
			wantReason:   ReasonNeedsConfirmation,
			wantExternal: true,
		},
		{
			name: "ask policy with confirmation resumes",
			input: func() Input {
				in := baseInput(shell("a", withAgent("codex", "sess-1", true)))
				in.Request.Confirmed = true
				return in
			},
			wantAction:   ActionResumeAgent,
			wantResume:   true,
			wantReason:   ReasonPolicyResume,
			wantExternal: true,
		},
		{
			name: "off policy recreates the shell only",
			input: func() Input {
				in := baseInput(shell("a", withAgent("codex", "sess-1", true)))
				in.Config.ResumeAgents = ResumeOff
				return in
			},
			wantAction: ActionRecreateShell,
			wantReason: ReasonResumeOff,
		},
		{
			name: "per-shell resume policy overrides an off machine default",
			input: func() Input {
				in := baseInput(shell("a", withAgent("codex", "sess-1", true), withPolicy(agentsession.PolicyResume)))
				in.Config.ResumeAgents = ResumeOff
				return in
			},
			wantAction:   ActionResumeAgent,
			wantResume:   true,
			wantReason:   ReasonPolicyResume,
			wantExternal: true,
		},
		{
			name: "per-shell shell policy overrides an auto machine default",
			input: func() Input {
				in := baseInput(shell("a", withAgent("codex", "sess-1", true), withPolicy(agentsession.PolicyShell)))
				in.Config.ResumeAgents = ResumeAuto
				return in
			},
			wantAction: ActionRecreateShell,
			wantReason: ReasonResumeOff,
		},
		{
			name: "an unreported reference is never auto-resumed",
			input: func() Input {
				in := baseInput(shell("a", withAgent("codex", "sess-1", false)))
				in.Config.ResumeAgents = ResumeAuto
				return in
			},
			wantAction: ActionRecreateShell,
			wantReason: ReasonUnreportedRef,
		},
		{
			name: "a provider with no native resume restores as a plain shell",
			input: func() Input {
				in := baseInput(shell("a", withAgent("copilot", "sess-1", true)))
				in.Config.ResumeAgents = ResumeAuto
				return in
			},
			wantAction: ActionRecreateShell,
			wantReason: ReasonProviderNoResume,
		},
		{
			name: "a missing provider binary restores as a plain shell",
			input: func() Input {
				in := baseInput(shell("a", withAgent("codex", "sess-1", true)))
				in.Config.ResumeAgents = ResumeAuto
				in.ProviderAvailable = func(string) bool { return false }
				return in
			},
			wantAction: ActionRecreateShell,
			wantReason: ReasonProviderUnavailable,
		},
		{
			name: "a shell with no binding recreates without an agent",
			input: func() Input {
				in := baseInput(shell("a", func(d *shellstate.Definition) { d.AgentType = "codex" }))
				in.Config.ResumeAgents = ResumeAuto
				return in
			},
			wantAction: ActionRecreateShell,
			wantReason: ReasonNoSessionRef,
		},
		{
			name: "the caller not asking for agents is honoured even under auto",
			input: func() Input {
				in := baseInput(shell("a", withAgent("codex", "sess-1", true)))
				in.Config.ResumeAgents = ResumeAuto
				in.Request = Request{}
				return in
			},
			wantAction: ActionRecreateShell,
			wantReason: ReasonAgentsNotRequested,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan := Build(tc.input())
			step := only(t, plan)
			if step.Action != tc.wantAction {
				t.Fatalf("action %s, want %s", step.Action, tc.wantAction)
			}
			if step.Agent == nil {
				t.Fatal("a shell with agent metadata must carry an agent verdict")
			}
			if step.Agent.Resume != tc.wantResume {
				t.Errorf("resume %v, want %v", step.Agent.Resume, tc.wantResume)
			}
			if step.Agent.Reason != tc.wantReason {
				t.Errorf("agent reason %s, want %s", step.Agent.Reason, tc.wantReason)
			}
			if step.ExternalExecution != tc.wantExternal {
				t.Errorf("externalExecution %v, want %v", step.ExternalExecution, tc.wantExternal)
			}
		})
	}
}

// TestPlanNeverCarriesTheSessionValue is the redaction rule M3 established,
// applied to the surface that most obviously ends up in a log: a printed plan.
func TestPlanNeverCarriesTheSessionValue(t *testing.T) {
	const secret = "01a05614-0ca7-dead-beef-000000000000"
	in := baseInput(shell("a", withAgent("codex", secret, true)))
	in.Config.ResumeAgents = ResumeAuto
	step := only(t, Build(in))
	if step.Agent == nil || step.Agent.RefKind != "id" || !step.Agent.Reported {
		t.Fatalf("the plan must still report capability and provenance: %+v", step.Agent)
	}
	blob := renderStep(step)
	if contains(blob, secret) {
		t.Fatalf("the plan leaked the conversation identifier: %s", blob)
	}
}

func renderStep(s Step) string {
	return string(mustJSON(s))
}

// TestPlanDedupLetsOneShellResumeAConversation covers the global dedup rule:
// the loser restores as a plain shell and is told which target won, and neither
// record is edited to enforce it.
func TestPlanDedupLetsOneShellResumeAConversation(t *testing.T) {
	older := time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 8, 30, 11, 0, 0, 0, time.UTC)

	loser := shell("a", withAgent("codex", "same-session", true))
	loser.Def.Agent.Session.ReportedAt = older
	winner := shell("b", withAgent("codex", "same-session", true))
	winner.Def.Agent.Session.ReportedAt = newer

	in := baseInput(loser, winner)
	in.Config.ResumeAgents = ResumeAuto
	plan := Build(in)
	if len(plan.Steps) != 2 {
		t.Fatalf("want 2 steps, got %d", len(plan.Steps))
	}
	// Steps are ordered by project then session name, so "a" is first.
	gotLoser, gotWinner := plan.Steps[0], plan.Steps[1]
	if gotLoser.Session != "a" || gotWinner.Session != "b" {
		t.Fatalf("plan is not ordered by session name: %s then %s", gotLoser.Session, gotWinner.Session)
	}
	if gotWinner.Action != ActionResumeAgent || !gotWinner.Agent.Resume {
		t.Errorf("the most recently reported claim should resume: %+v", gotWinner)
	}
	if gotLoser.Action != ActionRecreateShell {
		t.Errorf("the duplicate must still get its shell back, got %s", gotLoser.Action)
	}
	if gotLoser.Agent.Reason != ReasonDuplicateRef {
		t.Errorf("loser reason %s, want %s", gotLoser.Agent.Reason, ReasonDuplicateRef)
	}
	if gotLoser.Agent.ConflictWith != "b" {
		t.Errorf("the loser must name the winning target, got %q", gotLoser.Agent.ConflictWith)
	}
	if gotLoser.ExternalExecution {
		t.Error("a deduplicated shell must not report that it would run an agent")
	}
}

// TestPlanDedupSpansProjects proves deduplication is global per host rather than
// per manifest: the same conversation recorded in two projects still resumes
// once, which is the only reading that stops one provider session appearing in
// two panes.
func TestPlanDedupSpansProjects(t *testing.T) {
	a := shell("a", withAgent("codex", "same-session", true))
	a.Project = "alpha"
	b := shell("b", withAgent("codex", "same-session", true))
	b.Project = "beta"
	b.Def.Agent.Session.ReportedAt = time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)

	in := baseInput(a, b)
	in.Config.ResumeAgents = ResumeAuto
	plan := Build(in)

	resumes := 0
	for _, s := range plan.Steps {
		if s.Agent != nil && s.Agent.Resume {
			resumes++
		}
	}
	if resumes != 1 {
		t.Fatalf("one conversation must resume into exactly one shell, got %d", resumes)
	}
}

// TestPlanServerChangedIsEvidence pins that ServerChanged is derived from the
// recorded and observed server identity rather than asserted by a caller.
func TestPlanServerChangedIsEvidence(t *testing.T) {
	t.Run("replaced server", func(t *testing.T) {
		plan := Build(baseInput(shell("a")))
		if !plan.ServerChanged {
			t.Fatal("a prior server different from the current one is a cold restore")
		}
		if len(plan.PriorServers) != 1 || plan.PriorServers[0] != priorServer {
			t.Fatalf("prior servers %v", plan.PriorServers)
		}
	})
	t.Run("same server", func(t *testing.T) {
		in := baseInput(shell("a", func(d *shellstate.Definition) { d.Restore.LastSeenServer = currentServer }))
		if Build(in).ServerChanged {
			t.Fatal("the same server is an ordinary restart, not a cold restore")
		}
	})
}

// TestPlanIsDeterministic is what makes "run status, then run restore" a
// meaningful workflow: the document the user read must be the document that
// executes.
func TestPlanIsDeterministic(t *testing.T) {
	build := func() Plan {
		return Build(baseInput(
			shell("z", withAgent("codex", "s3", true)),
			shell("a", withAgent("claude", "s1", true)),
			shell("m"),
		))
	}
	first, second := build(), build()
	if string(mustJSON(first)) != string(mustJSON(second)) {
		t.Fatalf("two builds over the same input disagreed:\n%s\n%s", mustJSON(first), mustJSON(second))
	}
	want := []string{"a", "m", "z"}
	for i, s := range first.Steps {
		if s.Session != want[i] {
			t.Fatalf("step %d is %s, want %s", i, s.Session, want[i])
		}
	}
}

// TestPlanReattachDoesNotDependOnPolicy pins the ordering choice in planShell: a
// session that is already running is left alone whatever its policy says, since
// "do not restore this" is not a reason to disturb a live process.
func TestPlanReattachDoesNotDependOnPolicy(t *testing.T) {
	for _, policy := range agentsession.Policies() {
		in := baseInput(shell("a", withPolicy(policy)))
		in.Live["a"] = LiveManaged
		if step := only(t, Build(in)); step.Action != ActionReattach {
			t.Errorf("policy %s produced %s for a live session, want reattach", policy, step.Action)
		}
	}
}

func TestParseResumeMode(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    ResumeMode
		wantErr bool
	}{
		{in: "", want: ResumeAsk},
		{in: "off", want: ResumeOff},
		{in: " ASK ", want: ResumeAsk},
		{in: "auto", want: ResumeAuto},
		{in: "always", wantErr: true},
	} {
		got, err := ParseResumeMode(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%q should be rejected", tc.in)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%q -> %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
}
