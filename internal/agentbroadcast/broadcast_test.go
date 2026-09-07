package agentbroadcast

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/agentcontrol"
	"github.com/marcus/sidecar/internal/managedtarget"
)

func TestEnvelopeStrings(t *testing.T) {
	got := Envelope(`"tacoma-fable" in clara-home`, "Code freeze on main until td-1a2b3c lands; hold pushes.")
	want := `[Sidecar broadcast from "tacoma-fable" in clara-home] Code freeze on main until td-1a2b3c lands; hold pushes.`
	if got != want {
		t.Fatalf("agent envelope = %q, want %q", got, want)
	}
	got = Envelope("the user", "hold pushes")
	want = `[Sidecar broadcast from the user] hold pushes`
	if got != want {
		t.Fatalf("user envelope = %q, want %q", got, want)
	}
}

func TestPlanExcludesSenderUnlessIncludeSelf(t *testing.T) {
	term := newStage()
	term.add("s1", "alpha", "p", "claude:idle")
	term.add("s2", "beta", "p", "claude:working")
	cands := []managedtarget.Target{managed("s1", "alpha", "p"), managed("s2", "beta", "p")}
	svc := testService(term, cands)

	plan, err := svc.Plan(context.Background(), PlanRequest{
		ScopeKind: ScopeProject, Project: "p", SenderSession: "s1",
		SenderName: "alpha", SenderProject: "p",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertOutcome(t, plan, "s1", OutcomeSkipped, reasonSender)
	assertOutcome(t, plan, "s2", OutcomeWouldSend, "")
	if term.submitCount() != 0 || term.launchCount() != 0 {
		t.Fatalf("Plan wrote: submits=%d launches=%d", term.submitCount(), term.launchCount())
	}

	included, err := svc.Plan(context.Background(), PlanRequest{
		ScopeKind: ScopeProject, Project: "p", SenderSession: "s1", IncludeSelf: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertOutcome(t, included, "s1", OutcomeWouldSend, "")
	assertOutcome(t, included, "s2", OutcomeWouldSend, "")
}

func TestPlanStatusFilterDropsWorking(t *testing.T) {
	term := newStage()
	term.add("idle", "idle-one", "p", "claude:idle")
	term.add("work", "work-one", "p", "claude:working")
	cands := []managedtarget.Target{managed("idle", "idle-one", "p"), managed("work", "work-one", "p")}
	svc := testService(term, cands)

	def, err := svc.Plan(context.Background(), PlanRequest{ScopeKind: ScopeProject, Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	assertOutcome(t, def, "idle", OutcomeWouldSend, "")
	assertOutcome(t, def, "work", OutcomeWouldSend, "")

	idleOnly, err := svc.Plan(context.Background(), PlanRequest{
		ScopeKind: ScopeProject, Project: "p", Status: []agentcontrol.Status{agentcontrol.StatusIdle},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertOutcome(t, idleOnly, "idle", OutcomeWouldSend, "")
	assertOutcome(t, idleOnly, "work", OutcomeSkipped, reasonStatus)
}

func TestPlanSkipsBlockedUnknownStale(t *testing.T) {
	term := newStage()
	term.add("blocked", "blocked-one", "p", "claude:blocked")
	term.add("unknown", "unknown-one", "p", "claude:unknown")
	term.add("stale", "stale-one", "p", "claude:idle:stale")
	term.add("copy", "copy-one", "p", "claude:idle").copyMode = true
	term.add("dead", "dead-one", "p", "claude:idle").dead = true
	cands := []managedtarget.Target{
		managed("blocked", "blocked-one", "p"),
		managed("unknown", "unknown-one", "p"),
		managed("stale", "stale-one", "p"),
		managed("copy", "copy-one", "p"),
		managed("dead", "dead-one", "p"),
	}
	plan, err := testService(term, cands).Plan(context.Background(), PlanRequest{ScopeKind: ScopeProject, Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	assertOutcome(t, plan, "blocked", OutcomeSkipped, string(agentcontrol.ErrBlocked))
	assertOutcome(t, plan, "unknown", OutcomeSkipped, string(agentcontrol.ErrNotReady))
	assertOutcome(t, plan, "stale", OutcomeSkipped, string(agentcontrol.ErrNotReady))
	assertOutcome(t, plan, "copy", OutcomeSkipped, string(agentcontrol.ErrPaneBusy))
	assertOutcome(t, plan, "dead", OutcomeSkipped, string(agentcontrol.ErrPaneBusy))
}

// A candidate nothing answered for is a registry row, not a shell: the plan
// omits it and the shells-without-agent count does not speak for it either.
// Registered worktrees nobody has opened are the common case, and counting
// them told the user about dozens of shells that were not there (td-cd1706).
func TestPlanOmitsObserveFailureEntirely(t *testing.T) {
	term := newStage()
	cands := []managedtarget.Target{managed("gone", "missing", "p")}
	plan, err := testService(term, cands).Plan(context.Background(), PlanRequest{ScopeKind: ScopeProject, Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Recipients) != 0 {
		t.Fatalf("recipients = %+v, want none", plan.Recipients)
	}
	if plan.ShellsWithoutAgent != 0 {
		t.Fatalf("ShellsWithoutAgent = %d, want 0", plan.ShellsWithoutAgent)
	}
}

func TestPlanOmitsNoProviderPane(t *testing.T) {
	term := newStage()
	term.add("agent", "live", "p", "claude:idle")
	term.add("shell", "plain", "p", ":unknown")
	cands := []managedtarget.Target{managed("agent", "live", "p"), managed("shell", "plain", "p")}
	svc := testService(term, cands)
	plan, err := svc.Plan(context.Background(), PlanRequest{ScopeKind: ScopeProject, Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.ShellsWithoutAgent != 1 {
		t.Fatalf("ShellsWithoutAgent = %d, want 1", plan.ShellsWithoutAgent)
	}
	if _, ok := recipient(plan.Recipients, "shell"); ok {
		t.Fatalf("no-provider pane listed as a row: %+v", plan.Recipients)
	}
	assertOutcome(t, plan, "agent", OutcomeWouldSend, "")
	if term.inspectCount() != 2 {
		t.Fatalf("Plan inspected %d times, want 1 per candidate", term.inspectCount())
	}

	res, err := svc.Send(context.Background(), plan, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if len(term.submitted("shell")) != 0 {
		t.Fatalf("no-provider pane received %q", term.submitted("shell"))
	}
	if res.Summary.Submitted != 1 {
		t.Fatalf("summary = %+v", res.Summary)
	}
}

func TestPlanToPrefersExactSessionAndRefusesAmbiguity(t *testing.T) {
	term := newStage()
	term.add("s1", "reviewer", "a", "claude:idle")
	term.add("reviewer", "other", "b", "claude:idle")
	term.add("s2", "reviewer", "b", "claude:idle")
	cands := []managedtarget.Target{
		managed("s1", "reviewer", "a"),
		managed("reviewer", "other", "b"),
		managed("s2", "reviewer", "b"),
	}
	svc := testService(term, cands)

	plan, err := svc.Plan(context.Background(), PlanRequest{To: []string{"reviewer"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Recipients) != 1 || plan.Recipients[0].Target.Session != "reviewer" {
		t.Fatalf("exact session = %+v", plan.Recipients)
	}

	displayOnly := []managedtarget.Target{cands[0], cands[2]}
	_, err = testService(term, displayOnly).Plan(context.Background(), PlanRequest{To: []string{"reviewer"}})
	var me *managedtarget.Error
	if !errors.As(err, &me) || me.Kind != managedtarget.Ambiguous {
		t.Fatalf("ambiguous --to err = %T %v", err, err)
	}
}

func TestPlanToPlusScopeAddsRatherThanReplaces(t *testing.T) {
	term := newStage()
	term.add("s1", "alpha", "a", "claude:idle")
	term.add("reviewer", "other", "b", "claude:working")
	cands := []managedtarget.Target{
		managed("s1", "alpha", "a"),
		managed("reviewer", "other", "b"),
	}
	plan, err := testService(term, cands).Plan(context.Background(), PlanRequest{
		ScopeKind: ScopeProject, Project: "a", To: []string{"reviewer"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Recipients) != 2 {
		t.Fatalf("recipients = %+v, want project a plus --to reviewer", plan.Recipients)
	}
	assertOutcome(t, plan, "s1", OutcomeWouldSend, "")
	assertOutcome(t, plan, "reviewer", OutcomeWouldSend, "")
}

func TestPlanOrdersByProjectThenDisplayName(t *testing.T) {
	term := newStage()
	term.add("z", "alpha", "zeta", "claude:idle")
	term.add("a2", "zeta", "alpha", "claude:idle")
	term.add("a1", "alpha", "alpha", "claude:idle")
	cands := []managedtarget.Target{
		managed("z", "alpha", "zeta"),
		managed("a2", "zeta", "alpha"),
		managed("a1", "alpha", "alpha"),
	}
	plan, err := testService(term, cands).Plan(context.Background(), PlanRequest{ScopeKind: ScopeAll})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(plan.Recipients))
	for i, r := range plan.Recipients {
		got[i] = r.Target.Project + "/" + r.Target.Name
	}
	want := []string{"alpha/alpha", "alpha/zeta", "zeta/alpha"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestSendRefusalDoesNotStopOthers(t *testing.T) {
	term := newStage()
	term.add("a", "one", "p", "claude:working")
	term.add("b", "two", "p", "claude:working")
	term.add("c", "three", "p", "claude:working")
	cands := []managedtarget.Target{
		managed("a", "one", "p"), managed("b", "two", "p"), managed("c", "three", "p"),
	}
	svc := testService(term, cands)
	plan, err := svc.Plan(context.Background(), PlanRequest{ScopeKind: ScopeProject, Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	term.pane("b").stage = "claude:blocked"

	res, err := svc.Send(context.Background(), plan, "stop")
	if err != nil {
		t.Fatal(err)
	}
	assertResult(t, res, "a", OutcomeSubmitted, "")
	assertResult(t, res, "b", OutcomeSkipped, string(agentcontrol.ErrBlocked))
	assertResult(t, res, "c", OutcomeSubmitted, "")
	if res.Summary.Submitted != 2 || res.Summary.Skipped != 1 {
		t.Fatalf("summary = %+v", res.Summary)
	}
	if term.launchCount() != 0 {
		t.Fatalf("Launch called %d times", term.launchCount())
	}
	if len(term.submitted("a")) != 1 || len(term.submitted("c")) != 1 || len(term.submitted("b")) != 0 {
		t.Fatalf("submits a=%q b=%q c=%q", term.submitted("a"), term.submitted("b"), term.submitted("c"))
	}
}

func TestSendReportsUnknownAndDoesNotRetry(t *testing.T) {
	term := newStage()
	term.add("ok", "good", "p", "claude:working")
	term.add("drop", "bad", "p", "claude:working")
	term.pane("drop").submitErr = errors.New("connection dropped during write")
	cands := []managedtarget.Target{managed("ok", "good", "p"), managed("drop", "bad", "p")}
	svc := testService(term, cands)
	plan, err := svc.Plan(context.Background(), PlanRequest{ScopeKind: ScopeProject, Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.Send(context.Background(), plan, "hello")
	if err != nil {
		t.Fatal(err)
	}
	assertResult(t, res, "ok", OutcomeSubmitted, "")
	assertResult(t, res, "drop", OutcomeUnknown, string(agentcontrol.ErrTransport))
	if term.submitCount() != 2 {
		t.Fatalf("submit attempts = %d, want 2 (unknown must not retry)", term.submitCount())
	}
	if len(term.submitted("drop")) != 0 {
		t.Fatalf("unknown target recorded a write: %q", term.submitted("drop"))
	}
}

func TestSendNeverLaunches(t *testing.T) {
	term := newStage()
	term.add("s", "one", "p", "claude:working")
	svc := testService(term, []managedtarget.Target{managed("s", "one", "p")})
	plan, err := svc.Plan(context.Background(), PlanRequest{ScopeKind: ScopeAll})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Send(context.Background(), plan, "hi"); err != nil {
		t.Fatal(err)
	}
	if term.launchCount() != 0 {
		t.Fatalf("Launch called %d times", term.launchCount())
	}
	if term.submitCount() != 1 {
		t.Fatalf("submits = %d, want 1", term.submitCount())
	}
}

func TestSendAppliesEnvelopeUnlessRaw(t *testing.T) {
	term := newStage()
	term.add("s", "tacoma-fable", "clara-home", "claude:working")
	cands := []managedtarget.Target{managed("s", "tacoma-fable", "clara-home")}
	svc := testService(term, cands)

	plan, err := svc.Plan(context.Background(), PlanRequest{
		ScopeKind: ScopeProject, Project: "clara-home",
		SenderName: "tacoma-fable", SenderProject: "clara-home",
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.Send(context.Background(), plan, "hold pushes")
	if err != nil {
		t.Fatal(err)
	}
	want := `[Sidecar broadcast from "tacoma-fable" in clara-home] hold pushes`
	if res.Text != want || len(term.submitted("s")) != 1 || term.submitted("s")[0] != want {
		t.Fatalf("delivered %q / %q, want %q", res.Text, term.submitted("s"), want)
	}

	plan.Raw = true
	raw, err := svc.Send(context.Background(), plan, "hold pushes")
	if err != nil {
		t.Fatal(err)
	}
	if raw.Text != "hold pushes" || term.submitted("s")[1] != "hold pushes" {
		t.Fatalf("raw delivered %q / %q", raw.Text, term.submitted("s"))
	}

	plan.Raw = false
	plan.FromUser = true
	user, err := svc.Send(context.Background(), plan, "hold pushes")
	if err != nil {
		t.Fatal(err)
	}
	wantUser := `[Sidecar broadcast from the user] hold pushes`
	if user.Text != wantUser {
		t.Fatalf("user envelope = %q", user.Text)
	}
}

func recipient(rows []Recipient, session string) (Recipient, bool) {
	for _, r := range rows {
		if r.Target.Session == session {
			return r, true
		}
	}
	return Recipient{}, false
}

func assertOutcome(t *testing.T, plan Plan, session string, outcome Outcome, reason string) {
	t.Helper()
	row, ok := recipient(plan.Recipients, session)
	if !ok {
		t.Fatalf("session %s missing from plan %+v", session, plan.Recipients)
	}
	if row.Outcome != outcome {
		t.Fatalf("%s outcome = %s, want %s", session, row.Outcome, outcome)
	}
	gotReason := ""
	if row.Reason != nil {
		gotReason = row.Reason.Code
	}
	if gotReason != reason {
		t.Fatalf("%s reason = %q, want %q (row=%+v)", session, gotReason, reason, row)
	}
}

func assertResult(t *testing.T, res Result, session string, outcome Outcome, reason string) {
	t.Helper()
	assertOutcome(t, Plan{Recipients: res.Recipients}, session, outcome, reason)
}
