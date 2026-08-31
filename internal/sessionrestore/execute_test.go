package sessionrestore

import (
	"context"
	"errors"
	"testing"

	"github.com/marcus/sidecar/internal/agentsession"
)

// fakeWorld is a tmux server that only has names in it, which is all the
// executor's rechecks ever ask about.
type fakeWorld struct {
	live    map[string]LiveState
	server  string
	created []string
	resumed []string

	createErr  error
	resumeErr  error
	planErr    error
	onCreate   func(*fakeWorld)
	onResume   func(*fakeWorld)
	serverCall int
}

func newWorld() *fakeWorld {
	return &fakeWorld{live: map[string]LiveState{}, server: currentServer}
}

func (w *fakeWorld) deps() Deps {
	return Deps{
		Live: func(session string) LiveState {
			if s, ok := w.live[session]; ok {
				return s
			}
			return LiveAbsent
		},
		CurrentServer: func() string {
			w.serverCall++
			return w.server
		},
		CreateShell: func(_ context.Context, step Step) error {
			if w.createErr != nil {
				return w.createErr
			}
			w.created = append(w.created, step.Session)
			w.live[step.Session] = LiveManaged
			if w.onCreate != nil {
				w.onCreate(w)
			}
			return nil
		},
		ResumePlanFor: func(step Step) (agentsession.ResumePlan, error) {
			if w.planErr != nil {
				return agentsession.ResumePlan{}, w.planErr
			}
			return agentsession.PlanResume("codex", agentsession.Ref{
				Kind: agentsession.RefID, Value: "sess-1",
				Source: agentsession.OfficialSourceFor("codex"), Reported: true,
			})
		},
		ResumeAgent: func(_ context.Context, step Step, _ agentsession.ResumePlan) error {
			if w.resumeErr != nil {
				return w.resumeErr
			}
			w.resumed = append(w.resumed, step.Session)
			if w.onResume != nil {
				w.onResume(w)
			}
			return nil
		},
	}
}

func resumePlanInput(w *fakeWorld) Input {
	in := baseInput(shell("a", withAgent("codex", "sess-1", true)))
	in.Config.ResumeAgents = ResumeAuto
	in.Live = w.live
	return in
}

func runPlan(t *testing.T, w *fakeWorld, in Input) Result {
	t.Helper()
	return Execute(context.Background(), Build(in), w.deps())
}

func onlyOutcome(t *testing.T, r Result) Outcome {
	t.Helper()
	if len(r.Outcomes) != 1 {
		t.Fatalf("want 1 outcome, got %d: %+v", len(r.Outcomes), r.Outcomes)
	}
	return r.Outcomes[0]
}

func TestExecuteRestoresAndResumes(t *testing.T) {
	w := newWorld()
	got := onlyOutcome(t, runPlan(t, w, resumePlanInput(w)))
	if got.Status != StatusResumed {
		t.Fatalf("status %s, want %s (%s)", got.Status, StatusResumed, got.Detail)
	}
	if len(w.created) != 1 || len(w.resumed) != 1 {
		t.Fatalf("created %v resumed %v", w.created, w.resumed)
	}
}

// TestExecuteIsIdempotent is the property the tmux session name exists to buy:
// running the same restore twice must not produce two shells or two agents.
func TestExecuteIsIdempotent(t *testing.T) {
	w := newWorld()
	first := onlyOutcome(t, runPlan(t, w, resumePlanInput(w)))
	if first.Status != StatusResumed {
		t.Fatalf("first run: %s (%s)", first.Status, first.Detail)
	}
	// The second run plans against the world the first one left behind.
	second := onlyOutcome(t, runPlan(t, w, resumePlanInput(w)))
	if second.Status != StatusReattached {
		t.Fatalf("second run: %s, want %s (%s)", second.Status, StatusReattached, second.Detail)
	}
	if len(w.created) != 1 {
		t.Errorf("a second restore created another shell: %v", w.created)
	}
	if len(w.resumed) != 1 {
		t.Errorf("a second restore started another agent: %v", w.resumed)
	}
}

// TestExecuteConvergesAfterEachCrashPoint walks the crash matrix the plan names:
// after the plan, after shell creation, after layout attachment, after the
// resume send, and during the readiness wait. Each is modelled as the state the
// world is left in when the process dies at that point, and each must converge
// on one shell and at most one agent when the restore is run again.
func TestExecuteConvergesAfterEachCrashPoint(t *testing.T) {
	tests := []struct {
		name string
		// leave sets up the world as an interrupted run would have left it.
		leave      func(w *fakeWorld)
		wantStatus Status
		wantCreate int
		wantResume int
	}{
		{
			name:       "crash after the plan, before anything was created",
			leave:      func(w *fakeWorld) {},
			wantStatus: StatusResumed,
			wantCreate: 1,
			wantResume: 1,
		},
		{
			name: "crash after shell creation, before the resume",
			// The session exists; nothing has been resumed into it.
			leave:      func(w *fakeWorld) { w.live["a"] = LiveManaged },
			wantStatus: StatusReattached,
			wantCreate: 0,
			wantResume: 0,
		},
		{
			name: "crash after layout attachment",
			// Layout attachment is by session name and creates nothing, so the
			// world it leaves is the same one shell creation leaves.
			leave:      func(w *fakeWorld) { w.live["a"] = LiveManaged },
			wantStatus: StatusReattached,
			wantCreate: 0,
			wantResume: 0,
		},
		{
			name: "crash after the resume was sent",
			// The provider is running in the session. The name is live, so the
			// retry reattaches instead of starting a second provider.
			leave:      func(w *fakeWorld) { w.live["a"] = LiveManaged },
			wantStatus: StatusReattached,
			wantCreate: 0,
			wantResume: 0,
		},
		{
			name: "crash during the readiness wait",
			// Identical world: the session is live whether or not the provider
			// finished starting. Convergence must not depend on knowing which.
			leave:      func(w *fakeWorld) { w.live["a"] = LiveManaged },
			wantStatus: StatusReattached,
			wantCreate: 0,
			wantResume: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld()
			tc.leave(w)
			got := onlyOutcome(t, runPlan(t, w, resumePlanInput(w)))
			if got.Status != tc.wantStatus {
				t.Fatalf("status %s, want %s (%s)", got.Status, tc.wantStatus, got.Detail)
			}
			if len(w.created) != tc.wantCreate {
				t.Errorf("created %v, want %d", w.created, tc.wantCreate)
			}
			if len(w.resumed) != tc.wantResume {
				t.Errorf("resumed %v, want %d", w.resumed, tc.wantResume)
			}
		})
	}
}

// TestExecuteConvergesOnASessionCreatedByAnotherRunner covers the race the
// recheck exists for: the plan said absent, and by the time the step runs
// something else has created the session.
func TestExecuteConvergesOnASessionCreatedByAnotherRunner(t *testing.T) {
	w := newWorld()
	plan := Build(resumePlanInput(w))
	// Between planning and execution, another restore creates the session.
	w.live["a"] = LiveManaged

	got := onlyOutcome(t, Execute(context.Background(), plan, w.deps()))
	if got.Status != StatusResumed {
		t.Fatalf("status %s, want %s (%s)", got.Status, StatusResumed, got.Detail)
	}
	if len(w.created) != 0 {
		t.Fatalf("a session that already existed was created again: %v", w.created)
	}
}

// TestExecuteNeverKillsAConflictingSession is the safety invariant. The plan was
// built when the name was free; by execution something else holds it.
func TestExecuteNeverKillsAConflictingSession(t *testing.T) {
	w := newWorld()
	plan := Build(resumePlanInput(w))
	w.live["a"] = LiveForeign

	got := onlyOutcome(t, Execute(context.Background(), plan, w.deps()))
	if got.Status != StatusRefused || got.Reason != ReasonNameCollision {
		t.Fatalf("got %s/%s, want %s/%s", got.Status, got.Reason, StatusRefused, ReasonNameCollision)
	}
	if len(w.created) != 0 || len(w.resumed) != 0 {
		t.Fatalf("a collision produced work: created %v resumed %v", w.created, w.resumed)
	}
	if w.live["a"] != LiveForeign {
		t.Fatal("the conflicting session was disturbed")
	}
}

// TestExecuteAdoptsTheServerACoolRestoreStarts is a regression test for a bug
// the first live run found and no unit test had.
//
// A cold restore normally begins with no tmux server at all — that is the whole
// situation it exists for — and creating the first shell is what starts one. The
// executor's server-replacement guard originally compared against the server
// observed before any work, so the transition from "" to "pid=N" read as a
// replacement and every real restore aborted after its first shell. Starting the
// server is the restore working, not the ground moving.
func TestExecuteAdoptsTheServerACoolRestoreStarts(t *testing.T) {
	w := newWorld()
	w.server = "" // no tmux server is running, as after a reboot
	// Creating the first shell starts one, exactly as tmux does.
	w.onCreate = func(w *fakeWorld) { w.server = "pid=4242" }

	in := baseInput(shell("a"), shell("b"))
	in.Live = w.live
	result := Execute(context.Background(), Build(in), w.deps())

	if len(result.Outcomes) != 2 {
		t.Fatalf("want 2 outcomes, got %d", len(result.Outcomes))
	}
	for _, o := range result.Outcomes {
		if o.Status != StatusRestored {
			t.Fatalf("%s: status %s, want %s (%s)", o.Step.Session, o.Status, StatusRestored, o.Detail)
		}
	}
	if len(w.created) != 2 {
		t.Fatalf("created %v, want both shells", w.created)
	}
}

// TestExecuteStopsWhenTheServerDisappearsMidRun is the other half: once a server
// has been adopted, it going away is a real abort, because the sessions already
// created went with it.
func TestExecuteStopsWhenTheServerDisappearsMidRun(t *testing.T) {
	w := newWorld()
	w.onCreate = func(w *fakeWorld) { w.server = "" }

	in := baseInput(shell("a"), shell("b"), shell("c"))
	in.Live = w.live
	result := Execute(context.Background(), Build(in), w.deps())

	// "a" is created and the server vanishes. "b" is the step that notices, so
	// it fails; "c" is never attempted at all.
	if result.Outcomes[0].Status != StatusRestored {
		t.Errorf("first shell: %s", result.Outcomes[0].Status)
	}
	if result.Outcomes[1].Status != StatusFailed {
		t.Errorf("the step that notices the loss should fail, got %s", result.Outcomes[1].Status)
	}
	if result.Outcomes[2].Status != StatusSkipped {
		t.Errorf("later steps must not be attempted, got %s", result.Outcomes[2].Status)
	}
	if len(w.created) != 1 {
		t.Errorf("created %v, want only the first", w.created)
	}
}

// TestExecuteStopsWhenTheServerIsReplacedMidRun proves a restore does not keep
// building on a foundation that has gone away.
func TestExecuteStopsWhenTheServerIsReplacedMidRun(t *testing.T) {
	w := newWorld()
	in := baseInput(
		shell("a", withAgent("codex", "s1", true)),
		shell("b", withAgent("codex", "s2", true)),
	)
	in.Config.ResumeAgents = ResumeAuto
	in.Live = w.live
	// The server is replaced as soon as the first shell has been created.
	w.onCreate = func(w *fakeWorld) { w.server = "pid=9999" }

	result := Execute(context.Background(), Build(in), w.deps())
	if len(result.Outcomes) != 2 {
		t.Fatalf("want 2 outcomes, got %d", len(result.Outcomes))
	}
	if result.Outcomes[1].Status != StatusSkipped {
		t.Errorf("the second shell should not have been attempted, got %s", result.Outcomes[1].Status)
	}
	if len(w.created) != 1 {
		t.Errorf("created %v, want only the first", w.created)
	}
}

// TestExecuteRebuildsTheBindingAtResumeTime pins the binding recheck: a
// reference the provider rotated or cleared after planning must not be resumed.
func TestExecuteRebuildsTheBindingAtResumeTime(t *testing.T) {
	w := newWorld()
	w.planErr = errors.New("session reference was cleared")

	got := onlyOutcome(t, runPlan(t, w, resumePlanInput(w)))
	if got.Status != StatusRestored {
		t.Fatalf("status %s, want %s: the shell should still come back", got.Status, StatusRestored)
	}
	if len(w.resumed) != 0 {
		t.Fatal("a stale binding was resumed")
	}
}

// TestExecuteFailureLeavesTheStepRetryable pins the invariant that a restore
// failure never destroys anything: it reports, and the next run can try again.
func TestExecuteFailureLeavesTheStepRetryable(t *testing.T) {
	w := newWorld()
	w.createErr = errors.New("tmux refused")

	result := runPlan(t, w, resumePlanInput(w))
	got := onlyOutcome(t, result)
	if got.Status != StatusFailed {
		t.Fatalf("status %s, want %s", got.Status, StatusFailed)
	}
	if len(result.Failed()) != 1 {
		t.Fatal("the failure is not reported as retryable")
	}

	// The next run succeeds, which is what "retryable" has to mean.
	w.createErr = nil
	if next := onlyOutcome(t, runPlan(t, w, resumePlanInput(w))); next.Status != StatusResumed {
		t.Fatalf("retry status %s, want %s (%s)", next.Status, StatusResumed, next.Detail)
	}
}

// TestExecuteResumeFailureStillReportsTheRestoredShell keeps the two halves
// honest: losing the conversation must not be reported as losing the terminal.
func TestExecuteResumeFailureStillReportsTheRestoredShell(t *testing.T) {
	w := newWorld()
	w.resumeErr = errors.New("provider never became ready")

	got := onlyOutcome(t, runPlan(t, w, resumePlanInput(w)))
	if got.Status != StatusRestored {
		t.Fatalf("status %s, want %s", got.Status, StatusRestored)
	}
	if len(w.created) != 1 {
		t.Fatal("the shell was not created")
	}
}

// TestExecuteRunsNoAgentUnderAskWithoutConfirmation is the ask-policy
// non-execution proof at the executor level: the plan may say a resume is
// pending, but nothing runs.
func TestExecuteRunsNoAgentUnderAskWithoutConfirmation(t *testing.T) {
	w := newWorld()
	in := baseInput(shell("a", withAgent("codex", "sess-1", true)))
	in.Live = w.live // Config.ResumeAgents defaults to ask, Request is unconfirmed

	plan := Build(in)
	if !plan.WouldExecuteAgents() {
		t.Fatal("the plan should still disclose that a resume is possible")
	}
	if len(plan.PendingConfirmation()) != 1 {
		t.Fatal("the resume should be reported as awaiting confirmation")
	}

	got := onlyOutcome(t, Execute(context.Background(), plan, w.deps()))
	if got.Status != StatusRestored {
		t.Fatalf("status %s, want %s", got.Status, StatusRestored)
	}
	if len(w.resumed) != 0 {
		t.Fatal("an unconfirmed ask-policy restore ran an agent")
	}
}
