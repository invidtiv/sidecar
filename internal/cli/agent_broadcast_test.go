package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/agentbroadcast"
	"github.com/marcus/sidecar/internal/agentcontrol"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/shellstate"
)

func TestAgentBroadcastDryRunJSONAgreesWithPlan(t *testing.T) {
	stateDir, terminal := broadcastWorkingPanes(t)
	t.Setenv(shellstate.SessionEnv, "sidecar-sh-demo-1")

	code, out, errOut := runAgentCLI(t, "agent", "broadcast", "hold pushes", "--dry-run", "--json")
	if code != 0 || errOut != "" {
		t.Fatalf("dry-run = %d stdout=%q stderr=%q", code, out, errOut)
	}
	if len(terminal.submitted) != 0 || terminal.launchCalls != 0 {
		t.Fatalf("dry-run wrote: submitted=%q launches=%d", terminal.submitted, terminal.launchCalls)
	}

	var result agentbroadcast.Result
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("dry-run JSON %q: %v", out, err)
	}
	wantText := `[Sidecar broadcast from "one" in demo] hold pushes`
	if result.Text != wantText {
		t.Fatalf("text = %q, want %q", result.Text, wantText)
	}
	if result.Scope.Kind != agentbroadcast.ScopeProject || result.Scope.Project != "demo" {
		t.Fatalf("scope = %+v", result.Scope)
	}

	plan := planBroadcast(t, stateDir, terminal, agentbroadcast.PlanRequest{
		ScopeKind: agentbroadcast.ScopeProject, Project: "demo",
		SenderSession: "sidecar-sh-demo-1", SenderName: "one", SenderProject: "demo",
	})
	if len(result.Recipients) != len(plan.Recipients) {
		t.Fatalf("recipients %d, plan %d", len(result.Recipients), len(plan.Recipients))
	}
	for i, row := range result.Recipients {
		want := plan.Recipients[i]
		if row.Target.Session != want.Target.Session || row.Outcome != want.Outcome {
			t.Fatalf("row %d = %+v, plan %+v", i, row, want)
		}
		if row.Outcome != agentbroadcast.OutcomeWouldSend && row.Outcome != agentbroadcast.OutcomeSkipped {
			t.Fatalf("dry-run outcome = %s, want would_send or skipped", row.Outcome)
		}
	}
	assertBroadcastOutcome(t, result.Recipients, "sidecar-sh-demo-1", agentbroadcast.OutcomeSkipped, "sender")
	assertBroadcastOutcome(t, result.Recipients, "sidecar-sh-demo-2", agentbroadcast.OutcomeWouldSend, "")
}

func TestAgentBroadcastSubmitsEnvelopedTextAndDoesNotLaunch(t *testing.T) {
	_, terminal := broadcastWorkingPanes(t)
	t.Setenv(shellstate.SessionEnv, "sidecar-sh-demo-1")

	code, out, errOut := runAgentCLI(t, "agent", "broadcast", "hold pushes", "--json")
	if code != 0 || errOut != "" {
		t.Fatalf("broadcast = %d stdout=%q stderr=%q", code, out, errOut)
	}
	var result agentbroadcast.Result
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	want := `[Sidecar broadcast from "one" in demo] hold pushes`
	if result.Text != want {
		t.Fatalf("text = %q, want %q", result.Text, want)
	}
	if len(terminal.submitted) != 1 || terminal.submitted[0] != want {
		t.Fatalf("submitted = %q, want %q once", terminal.submitted, want)
	}
	if terminal.launchCalls != 0 {
		t.Fatalf("Launch called %d times", terminal.launchCalls)
	}
	assertBroadcastOutcome(t, result.Recipients, "sidecar-sh-demo-2", agentbroadcast.OutcomeSubmitted, "")
	assertBroadcastOutcome(t, result.Recipients, "sidecar-sh-demo-1", agentbroadcast.OutcomeSkipped, "sender")
	if result.Summary.Submitted != 1 || result.Summary.Skipped != 1 {
		t.Fatalf("summary = %+v", result.Summary)
	}
}

func TestAgentBroadcastRawSubmitsExactText(t *testing.T) {
	_, terminal := broadcastWorkingPanes(t)
	t.Setenv(shellstate.SessionEnv, "sidecar-sh-demo-1")

	code, out, errOut := runAgentCLI(t, "agent", "broadcast", "hold pushes", "--raw", "--json")
	if code != 0 || errOut != "" {
		t.Fatalf("raw = %d stdout=%q stderr=%q", code, out, errOut)
	}
	var result agentbroadcast.Result
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.Text != "hold pushes" {
		t.Fatalf("text = %q, want the raw prompt", result.Text)
	}
	if len(terminal.submitted) != 1 || terminal.submitted[0] != "hold pushes" {
		t.Fatalf("submitted = %q", terminal.submitted)
	}
}

func TestAgentBroadcastFeatureOffIsExit5(t *testing.T) {
	targetProject(t)
	var out, errOut bytes.Buffer
	handled, code := Run([]string{"agent", "broadcast", "hold pushes", "--project", "demo", "--json"}, &out, &errOut)
	if !handled || code != 5 || out.Len() != 0 || !strings.Contains(errOut.String(), `"code":"feature_disabled"`) {
		t.Fatalf("disabled = handled=%v code=%d stdout=%q stderr=%q", handled, code, out.String(), errOut.String())
	}
}

func TestAgentBroadcastHostIsUsage(t *testing.T) {
	targetProject(t)
	t.Setenv(shellstate.SessionEnv, "sidecar-sh-demo-1")
	code, out, errOut := runAgentCLI(t, "agent", "broadcast", "hold pushes", "--host", "book")
	if code != 2 || out != "" || !strings.Contains(errOut, "does not accept --host") || !strings.Contains(errOut, "remote hosts are not in this slice") {
		t.Fatalf("host = %d stdout=%q stderr=%q", code, out, errOut)
	}
}

func TestAgentBroadcastOutsideManagedShellRequiresScope(t *testing.T) {
	targetProject(t)
	code, out, errOut := runAgentCLI(t, "agent", "broadcast", "hold pushes")
	if code != 2 || out != "" || !strings.Contains(errOut, "--project NAME or --all") {
		t.Fatalf("unscoped = %d stdout=%q stderr=%q", code, out, errOut)
	}
}

func TestAgentBroadcastEnvelopeIsFromTheUserWithoutACallingShell(t *testing.T) {
	_, terminal := broadcastWorkingPanes(t)
	t.Setenv(shellstate.SessionEnv, "")
	want := `[Sidecar broadcast from the user] hold pushes`

	code, out, errOut := runAgentCLI(t, "agent", "broadcast", "hold pushes", "--project", "demo", "--json")
	if code != 0 || errOut != "" {
		t.Fatalf("project = %d stdout=%q stderr=%q", code, out, errOut)
	}
	var result agentbroadcast.Result
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.Text != want {
		t.Fatalf("project text = %q, want %q", result.Text, want)
	}
	if len(terminal.submitted) == 0 {
		t.Fatal("expected a submit")
	}
	for _, got := range terminal.submitted {
		if got != want {
			t.Fatalf("submitted %q, want %q", got, want)
		}
	}

	terminal.submitted = nil
	code, out, errOut = runAgentCLI(t, "agent", "broadcast", "hold pushes", "--all", "--dry-run", "--json")
	if code != 0 || errOut != "" {
		t.Fatalf("all dry-run = %d stdout=%q stderr=%q", code, out, errOut)
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.Text != want {
		t.Fatalf("all dry-run text = %q, want %q", result.Text, want)
	}
	if len(terminal.submitted) != 0 {
		t.Fatalf("dry-run wrote %q", terminal.submitted)
	}
}

func TestAgentBroadcastEmptyPlanIsNoRecipients(t *testing.T) {
	targetProject(t)
	terminal := &cliAgentTerminal{}
	useCLIAgentTerminal(t, terminal)
	t.Setenv(shellstate.SessionEnv, "sidecar-sh-demo-1")

	code, out, errOut := runAgentCLI(t, "agent", "broadcast", "hold pushes", "--json")
	if code != 5 || out != "" || !strings.Contains(errOut, `"code":"no_recipients"`) {
		t.Fatalf("empty plan = %d stdout=%q stderr=%q", code, out, errOut)
	}
	if len(terminal.submitted) != 0 {
		t.Fatalf("empty plan wrote %q", terminal.submitted)
	}
}

func TestAgentBroadcastEmptyPlanDryRunExitsZero(t *testing.T) {
	targetProject(t)
	terminal := &cliAgentTerminal{}
	useCLIAgentTerminal(t, terminal)
	t.Setenv(shellstate.SessionEnv, "sidecar-sh-demo-1")

	code, out, errOut := runAgentCLI(t, "agent", "broadcast", "hold pushes", "--dry-run", "--json")
	if code != 0 || errOut != "" {
		t.Fatalf("dry-run empty = %d stdout=%q stderr=%q", code, out, errOut)
	}
	var result agentbroadcast.Result
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Recipients) != 0 {
		t.Fatalf("recipients = %+v", result.Recipients)
	}
	if result.Summary.ShellsWithoutAgent < 2 {
		t.Fatalf("shellsWithoutAgent = %d, want at least the two fixture shells", result.Summary.ShellsWithoutAgent)
	}
}

func TestAgentBroadcastIncludeSelfSendsToCaller(t *testing.T) {
	_, terminal := broadcastWorkingPanes(t)
	t.Setenv(shellstate.SessionEnv, "sidecar-sh-demo-1")

	code, out, errOut := runAgentCLI(t, "agent", "broadcast", "hold pushes", "--include-self", "--json")
	if code != 0 || errOut != "" {
		t.Fatalf("include-self = %d stdout=%q stderr=%q", code, out, errOut)
	}
	var result agentbroadcast.Result
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	assertBroadcastOutcome(t, result.Recipients, "sidecar-sh-demo-1", agentbroadcast.OutcomeSubmitted, "")
	assertBroadcastOutcome(t, result.Recipients, "sidecar-sh-demo-2", agentbroadcast.OutcomeSubmitted, "")
	if len(terminal.submitted) != 2 {
		t.Fatalf("submitted %d times, want 2", len(terminal.submitted))
	}
}

func TestAgentBroadcastSkipsBlockedAndSubmitsTheOther(t *testing.T) {
	working := agentFixture(t, "working.txt")
	blocked := agentFixture(t, "blocked.txt")
	targetProject(t)
	terminal := &cliAgentTerminal{screenBySession: map[string]string{
		"sidecar-sh-demo-1": blocked,
		"sidecar-sh-demo-2": working,
	}}
	useCLIAgentTerminal(t, terminal)

	code, out, errOut := runAgentCLI(t, "agent", "broadcast", "hold pushes", "--project", "demo", "--json")
	if code != 0 || errOut != "" {
		t.Fatalf("mixed = %d stdout=%q stderr=%q", code, out, errOut)
	}
	var result agentbroadcast.Result
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	assertBroadcastOutcome(t, result.Recipients, "sidecar-sh-demo-1", agentbroadcast.OutcomeSkipped, string(agentcontrol.ErrBlocked))
	assertBroadcastOutcome(t, result.Recipients, "sidecar-sh-demo-2", agentbroadcast.OutcomeSubmitted, "")
	if len(terminal.submitted) != 1 {
		t.Fatalf("submitted = %q, want one write to the working pane", terminal.submitted)
	}
	if terminal.launchCalls != 0 {
		t.Fatalf("Launch called %d times", terminal.launchCalls)
	}
}

func TestAgentBroadcastEmptyTextIsUsage(t *testing.T) {
	targetProject(t)
	t.Setenv(shellstate.SessionEnv, "sidecar-sh-demo-1")
	for _, args := range [][]string{
		{"agent", "broadcast"},
		{"agent", "broadcast", ""},
		{"agent", "broadcast", "   "},
	} {
		code, out, errOut := runAgentCLI(t, args...)
		if code != 2 || out != "" {
			t.Fatalf("%v = %d stdout=%q stderr=%q", args, code, out, errOut)
		}
	}
}

func TestAgentBroadcastReadsStdinDash(t *testing.T) {
	stateDir, terminal := broadcastWorkingPanes(t)
	t.Setenv(shellstate.SessionEnv, "sidecar-sh-demo-1")

	var out, errOut bytes.Buffer
	env := Env{
		Stdout:           &out,
		Stderr:           &errOut,
		Stdin:            strings.NewReader("hold pushes\n"),
		StateDir:         stateDir,
		Ctx:              context.Background(),
		FeatureOverrides: map[string]bool{features.AgentControl.Name: true},
	}
	code := runAgentBroadcast(env, []string{"-", "--raw", "--json"})
	if code != 0 || errOut.Len() != 0 {
		t.Fatalf("stdin = %d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
	if len(terminal.submitted) != 1 || terminal.submitted[0] != "hold pushes" {
		t.Fatalf("submitted = %q", terminal.submitted)
	}
}

func broadcastWorkingPanes(t *testing.T) (stateDir string, terminal *cliAgentTerminal) {
	t.Helper()
	working := agentFixture(t, "working.txt")
	stateDir, _ = targetProject(t)
	terminal = &cliAgentTerminal{screenBySession: map[string]string{
		"sidecar-sh-demo-1": working,
		"sidecar-sh-demo-2": working,
	}}
	useCLIAgentTerminal(t, terminal)
	return stateDir, terminal
}

func TestAgentBroadcastHelpIsDiscoverableWhileDisabled(t *testing.T) {
	setupIsolatedCLI(t)
	var out, errOut bytes.Buffer
	handled, code := Run([]string{"agent", "broadcast", "--help"}, &out, &errOut)
	if !handled || code != 0 || errOut.Len() != 0 || !strings.Contains(out.String(), "sidecar agent broadcast") {
		t.Fatalf("help = handled=%v code=%d stderr=%q", handled, code, errOut.String())
	}
}

func planBroadcast(t *testing.T, stateDir string, terminal *cliAgentTerminal, req agentbroadcast.PlanRequest) agentbroadcast.Plan {
	t.Helper()
	svc := agentbroadcast.Service{
		Control:    agentcontrol.Service{Terminal: terminal},
		Candidates: broadcastCandidates(Env{StateDir: stateDir}),
	}
	plan, err := svc.Plan(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func assertBroadcastOutcome(t *testing.T, rows []agentbroadcast.Recipient, session string, outcome agentbroadcast.Outcome, reason string) {
	t.Helper()
	for _, row := range rows {
		if row.Target.Session != session {
			continue
		}
		if row.Outcome != outcome {
			t.Fatalf("%s outcome = %s, want %s", session, row.Outcome, outcome)
		}
		got := ""
		if row.Reason != nil {
			got = row.Reason.Code
		}
		if got != reason {
			t.Fatalf("%s reason = %q, want %q (row=%+v)", session, got, reason, row)
		}
		return
	}
	t.Fatalf("session %s missing from %+v", session, rows)
}
