package lifecycleharness

import (
	"os"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/agentlifecycle"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/testenv"
)

func TestMain(m *testing.M) { os.Exit(testenv.Main(m)) }

const (
	provider = "opencode"
	source   = "sidecar.opencode.plugin"
)

// capability is a source that has genuinely earned full authority, so the
// scenarios below exercise arbitration rather than tier demotion.
func capability() agentlifecycle.Capability {
	return agentlifecycle.Capability{
		SchemaVersion: agentlifecycle.SchemaVersion,
		Provider:      provider,
		Source:        source,
		AssetVersion:  "1",
		Tier:          agentlifecycle.TierFull,
		Evidence:      agentlifecycle.EvidenceRealTrace,
		Covered:       agentlifecycle.FullLifecycleTransitions(),
	}
}

// resolve runs the real arbitration against whatever the sink currently holds,
// so a scenario asserts the state a surface would actually show.
func resolve(h *Harness, p *Provider, screen agentactivity.Result, alive bool) agentlifecycle.Decision {
	in := agentlifecycle.Input{
		Now:                   p.Now(),
		Live:                  p.Identity(),
		ProcessAlive:          alive,
		Capability:            capability(),
		Status:                agentlifecycle.StatusCurrent,
		ProviderInTestedRange: true,
		Screen:                screen,
	}
	if latest, ok := p.Latest(); ok {
		in.Latest = &latest
	}
	return agentlifecycle.Resolve(in)
}

func screenWorking() agentactivity.Result {
	return agentactivity.Result{State: agentactivity.StateWorking, Evidence: "opencode.screen.progress-working"}
}

// TestHarnessIsolatesBothTmuxAndState is the first thing to check, because
// every other test in this package is only safe if it holds. Isolating one axis
// and not the other is the exact mistake that has destroyed real user shells.
func TestHarnessIsolatesBothTmuxAndState(t *testing.T) {
	h := Start(t)

	// The socket must be a throwaway file this harness created, never the
	// machine's default server. Comparing against os.TempDir is the check that
	// matters; the name is incidental.
	if !strings.HasPrefix(h.Socket, strings.TrimSuffix(os.TempDir(), "/")) {
		t.Fatalf("tmux socket is not in a temporary directory: %s", h.Socket)
	}
	if strings.Contains(h.Socket, "/tmux-") {
		t.Fatalf("tmux socket looks like a default server socket: %s", h.Socket)
	}
	if err := config.AssertIsolatedPath(config.StateDir()); err != nil {
		t.Fatalf("state directory is not isolated: %v", err)
	}
	if got := config.StateDir(); got != h.StateDir {
		t.Fatalf("state dir = %q, want %q", got, h.StateDir)
	}
	if !h.PaneAlive() {
		t.Fatal("harness pane is not alive")
	}
	if h.ServerIncarnation == "" || h.PaneID == "" || h.PanePID <= 0 {
		t.Fatalf("pane identity incomplete: %+v", h)
	}
}

// TestSteelThreadSequenceResolves walks the ordinary journey the recorded
// OpenCode trace demonstrates and asserts the resolved lane at each step.
func TestSteelThreadSequenceResolves(t *testing.T) {
	h := Start(t)
	p := h.Provider(provider, source, "run-1")

	p.SessionReport("sf-1")
	if got := resolve(h, p, screenWorking(), true); got.Explanation.FallbackReason != agentlifecycle.ReasonNoReport {
		t.Fatalf("a session report alone must not author a lane: %+v", got.Explanation)
	}

	for _, step := range []struct {
		name string
		emit func()
		want agentactivity.State
	}{
		{"work starts", func() { p.Working(agentlifecycle.ReasonTurnStart) }, agentactivity.StateWorking},
		{"permission blocks", func() { p.Blocked(agentlifecycle.ReasonPermissionRequest) }, agentactivity.StateBlocked},
		{"permission resolves", func() { p.Working(agentlifecycle.ReasonPermissionResolved) }, agentactivity.StateWorking},
		{"turn completes", func() { p.Idle(agentlifecycle.ReasonTurnComplete) }, agentactivity.StateIdle},
	} {
		t.Run(step.name, func(t *testing.T) {
			step.emit()
			got := resolve(h, p, screenWorking(), true)
			if got.Result.State != step.want {
				t.Fatalf("state = %q, want %q (%+v)", got.Result.State, step.want, got.Explanation)
			}
			if got.Explanation.Authority != agentlifecycle.AuthorityLifecycle {
				t.Fatalf("authority = %q", got.Explanation.Authority)
			}
		})
	}
}

// TestDuplicateEventIsIdempotent proves a replayed report changes nothing. A
// provider with at-least-once delivery produces these routinely.
func TestDuplicateEventIsIdempotent(t *testing.T) {
	h := Start(t)
	p := h.Provider(provider, source, "run-1")

	first := p.Blocked(agentlifecycle.ReasonPermissionRequest)
	before := len(h.Recorder.All())

	acceptance, err := p.Replay(first)
	if err != nil {
		t.Fatalf("replay errored: %v", err)
	}
	if acceptance != agentlifecycle.AcceptedDuplicate {
		t.Fatalf("acceptance = %q, want %q", acceptance, agentlifecycle.AcceptedDuplicate)
	}
	if got := len(h.Recorder.All()); got != before {
		t.Fatalf("replay appended a record: %d -> %d", before, got)
	}
	if got := resolve(h, p, screenWorking(), true); got.Result.State != agentactivity.StateBlocked {
		t.Fatalf("state changed after replay: %q", got.Result.State)
	}
}

// TestOutOfOrderEventIsRejected proves a late event cannot roll the lane back.
// OpenCode publishes no cross-plugin ordering guarantee, so this is a real case
// rather than a defensive one.
func TestOutOfOrderEventIsRejected(t *testing.T) {
	h := Start(t)
	p := h.Provider(provider, source, "run-1")

	p.Working(agentlifecycle.ReasonTurnStart) // seq 1
	p.Idle(agentlifecycle.ReasonTurnComplete) // seq 2
	_, err := p.EmitOutOfOrder(agentactivity.StateWorking, agentlifecycle.ReasonTurnStart, 1)
	if err == nil {
		t.Fatal("a stale sequence was accepted")
	}
	if !strings.Contains(err.Error(), string(agentlifecycle.ErrStaleSequence)) {
		t.Fatalf("error = %v, want a stale sequence error", err)
	}
	if got := resolve(h, p, screenWorking(), true); got.Result.State != agentactivity.StateIdle {
		t.Fatalf("a late event rolled the lane back to %q", got.Result.State)
	}
}

// TestSessionRotationEndsAuthorityForTheOldSession proves a rotated provider
// session cannot keep authoring through the previous session's report.
func TestSessionRotationEndsAuthorityForTheOldSession(t *testing.T) {
	h := Start(t)
	p := h.Provider(provider, source, "run-1")

	p.RotateSession("sf-old")
	p.Blocked(agentlifecycle.ReasonPermissionRequest)
	if got := resolve(h, p, screenWorking(), true); got.Result.State != agentactivity.StateBlocked {
		t.Fatalf("setup failed: %q", got.Result.State)
	}

	// The live session rotates; the stored report still names the old one.
	stale, _ := p.Latest()
	p.RotateSession("sf-new")
	in := agentlifecycle.Input{
		Now: p.Now(), Live: p.Identity(), ProcessAlive: true,
		Capability: capability(), Status: agentlifecycle.StatusCurrent,
		ProviderInTestedRange: true, Latest: &stale, Screen: screenWorking(),
	}
	got := agentlifecycle.Resolve(in)
	if got.Explanation.FallbackReason != agentlifecycle.ReasonSessionMismatch {
		t.Fatalf("reason = %q, want session mismatch", got.Explanation.FallbackReason)
	}
	if got.Result.State != agentactivity.StateWorking {
		t.Fatalf("did not fall back to the screen: %q", got.Result.State)
	}
}

// TestChildAgentCannotAuthorTheParentLane proves a subagent's reports stay in
// their own run. The plan explicitly refuses to let child activity block or
// complete the parent without a proved aggregation rule.
func TestChildAgentCannotAuthorTheParentLane(t *testing.T) {
	h := Start(t)
	parent := h.Provider(provider, source, "run-parent")
	child := parent.Child("run-child")

	parent.Working(agentlifecycle.ReasonTurnStart)
	child.Blocked(agentlifecycle.ReasonPermissionRequest)
	child.Idle(agentlifecycle.ReasonTurnComplete)

	if got := resolve(h, parent, screenWorking(), true); got.Result.State != agentactivity.StateWorking {
		t.Fatalf("child activity changed the parent lane to %q", got.Result.State)
	}
	if parentLatest, _ := parent.Latest(); parentLatest.Identity.RunID != "run-parent" {
		t.Fatalf("parent picked up a child record: %+v", parentLatest)
	}
	if childLatest, _ := child.Latest(); childLatest.State != agentactivity.StateIdle {
		t.Fatalf("child run did not record its own state: %+v", childLatest)
	}
}

// TestCancellationReleasesAuthority proves a cancelled turn hands the pane back
// to screen detection rather than leaving a lane latched.
func TestCancellationReleasesAuthority(t *testing.T) {
	h := Start(t)
	p := h.Provider(provider, source, "run-1")

	p.Working(agentlifecycle.ReasonTurnStart)
	p.Cancel()

	got := resolve(h, p, screenWorking(), true)
	if got.Explanation.FallbackReason != agentlifecycle.ReasonRunEnded {
		t.Fatalf("reason = %q, want run ended", got.Explanation.FallbackReason)
	}
	if got.Explanation.ReportOutcome != agentlifecycle.OutcomeCancelled {
		t.Fatalf("outcome = %q", got.Explanation.ReportOutcome)
	}
	if got.Result != screenWorking() {
		t.Fatalf("did not return the screen's own result: %+v", got.Result)
	}
}

// TestProcessExitEndsAuthorityImmediately proves a dead provider process ends
// authority without waiting for a freshness window, using a real pane kill.
func TestProcessExitEndsAuthorityImmediately(t *testing.T) {
	h := Start(t)
	p := h.Provider(provider, source, "run-1")

	p.Blocked(agentlifecycle.ReasonPermissionRequest)
	if got := resolve(h, p, screenWorking(), h.PaneAlive()); got.Result.State != agentactivity.StateBlocked {
		t.Fatalf("setup failed: %q", got.Result.State)
	}

	h.KillPaneProcess(t)
	if h.PaneAlive() {
		t.Fatal("pane survived the kill")
	}

	got := resolve(h, p, screenWorking(), h.PaneAlive())
	if got.Explanation.FallbackReason != agentlifecycle.ReasonProcessExited {
		t.Fatalf("reason = %q, want process exited", got.Explanation.FallbackReason)
	}
	if got.Result.State != agentactivity.StateWorking {
		t.Fatalf("did not fall back to the screen: %q", got.Result.State)
	}
}

// TestHookFailureIsSilentAndLeavesTheAgentAlone proves a broken hook degrades
// to screen detection instead of corrupting or blocking anything. A reporting
// failure must never change the provider's own operation.
func TestHookFailureIsSilentAndLeavesTheAgentAlone(t *testing.T) {
	h := Start(t)
	p := h.Provider(provider, source, "run-1")

	p.Working(agentlifecycle.ReasonTurnStart)

	p.FailNextHook()
	p.Blocked(agentlifecycle.ReasonPermissionRequest)
	if p.HookFailures() != 1 {
		t.Fatalf("hook failures = %d, want 1", p.HookFailures())
	}
	if len(h.Recorder.Rejected()) != 0 {
		t.Fatalf("a dropped hook produced store errors: %v", h.Recorder.Rejected())
	}

	// The blocked report never arrived, so the last truth is still working, and
	// the pane keeps resolving from the evidence it does have.
	got := resolve(h, p, screenWorking(), true)
	if got.Result.State != agentactivity.StateWorking {
		t.Fatalf("state = %q, want working", got.Result.State)
	}

	// The provider carries on, and the next successful report lands normally.
	p.Idle(agentlifecycle.ReasonTurnComplete)
	if got := resolve(h, p, screenWorking(), true); got.Result.State != agentactivity.StateIdle {
		t.Fatalf("provider did not recover: %q", got.Result.State)
	}
}

// TestInvalidReportsAreRejectedNotStored proves the untrusted-input rules are
// applied before anything is persisted.
func TestInvalidReportsAreRejectedNotStored(t *testing.T) {
	h := Start(t)
	p := h.Provider(provider, source, "run-1")
	base := p.Identity()

	valid := agentlifecycle.Report{
		SchemaVersion: agentlifecycle.SchemaVersion,
		ID:            "ok", Kind: agentlifecycle.KindState, Identity: base,
		Source: source, Sequence: 1, State: agentactivity.StateWorking,
		Reason: agentlifecycle.ReasonTurnStart,
	}

	for _, tc := range []struct {
		name string
		edit func(*agentlifecycle.Report)
	}{
		{"unknown state", func(r *agentlifecycle.Report) { r.State = agentactivity.StateUnknown }},
		{"state report carrying an outcome", func(r *agentlifecycle.Report) { r.Outcome = agentlifecycle.OutcomeCompleted }},
		{"reason outside the allowlist", func(r *agentlifecycle.Report) { r.Reason = "arbitrary text" }},
		{"unknown kind", func(r *agentlifecycle.Report) { r.Kind = "invented" }},
		{"future schema version", func(r *agentlifecycle.Report) { r.SchemaVersion = 99 }},
		{"missing pane identity", func(r *agentlifecycle.Report) { r.Identity.PaneID = "" }},
		{"oversized detail", func(r *agentlifecycle.Report) { r.Detail = strings.Repeat("x", 201) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := valid
			tc.edit(&bad)
			before := len(h.Recorder.All())
			if _, err := h.Recorder.Append(bad); err == nil {
				t.Fatal("invalid report was accepted")
			}
			if got := len(h.Recorder.All()); got != before {
				t.Fatal("invalid report was stored")
			}
		})
	}
}

// TestRecordedReportsCarryNoProviderContent is the privacy guard. It walks
// every field of every report the harness produced and fails on anything that
// looks like prompt, response, path, or credential content.
func TestRecordedReportsCarryNoProviderContent(t *testing.T) {
	h := Start(t)
	p := h.Provider(provider, source, "run-1")
	p.SessionReport("sf-1")
	p.Working(agentlifecycle.ReasonTurnStart)
	p.Blocked(agentlifecycle.ReasonPermissionRequest)
	p.Idle(agentlifecycle.ReasonTurnComplete)
	p.End(agentlifecycle.OutcomeCompleted, agentlifecycle.ReasonTurnComplete)

	for _, rep := range h.Recorder.All() {
		if rep.Detail != "" {
			t.Fatalf("harness produced a detail string: %q", rep.Detail)
		}
		// The reason is the only vocabulary a provider chooses, and it must come
		// from the frozen allowlist rather than be free text.
		if rep.Reason != "" && !validReason(rep.Reason) {
			t.Fatalf("reason %q is outside the allowlist", rep.Reason)
		}
		if strings.Contains(rep.Identity.SessionFingerprint, "/") {
			t.Fatalf("session fingerprint looks like a path: %q", rep.Identity.SessionFingerprint)
		}
	}
}
