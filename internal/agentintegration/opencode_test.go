package agentintegration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/agentlifecycle"
)

// replayTrace drives a checked-in sanitized trace through the handler and
// returns the lane and terminal actions it produced.
//
// This is the point of keeping a Go mirror of the mapping: the traces are real
// recorded provider output, so a mistake in "which event becomes which lane"
// fails here rather than on a user's machine.
func replayTrace(t *testing.T, name string) []OpenCodeAction {
	t.Helper()
	path := filepath.Join("..", "agentlifecycle", "testdata", "traces", "opencode", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var h OpenCodeHandler
	var actions []OpenCodeAction
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		cols := strings.Split(line, "\t")
		if len(cols) < 7 {
			t.Fatalf("malformed trace row: %q", line)
		}
		ev := OpenCodeEvent{Type: cols[2], HasSession: cols[5] != "-"}
		if cols[3] != "-" {
			ev.Status = cols[3]
		}
		if len(cols) > 7 && cols[7] != "-" {
			ev.ErrorName = cols[7]
		}
		actions = append(actions, h.Handle(ev)...)
	}
	return actions
}

func lanes(actions []OpenCodeAction) []string {
	var out []string
	for _, a := range actions {
		switch a.Kind {
		case agentlifecycle.KindState:
			out = append(out, string(a.State))
		case agentlifecycle.KindEnd:
			out = append(out, "end:"+string(a.Outcome))
		case agentlifecycle.KindRelease:
			out = append(out, "release")
		}
	}
	return out
}

func assertLanes(t *testing.T, got []OpenCodeAction, want []string) {
	t.Helper()
	have := lanes(got)
	if len(have) != len(want) {
		t.Fatalf("lanes = %v, want %v", have, want)
	}
	for i := range want {
		if have[i] != want[i] {
			t.Fatalf("lanes = %v, want %v", have, want)
		}
	}
}

// TestToolTurnWithPermissionWalksTheLanes is the steel thread's lane walk,
// derived from the Phase A trace of a real turn that used a tool and hit a
// permission prompt: working, blocked, working again, then idle.
func TestToolTurnWithPermissionWalksTheLanes(t *testing.T) {
	actions := replayTrace(t, "tool-turn-with-permission.tsv")
	assertLanes(t, actions, []string{
		"idle",    // session.created
		"working", // session.status busy
		"blocked", // permission.asked
		"working", // permission.replied
		"idle",    // session.status idle
		"release", // dispose
	})
}

// TestCancelledTurnEndsAsCancelled is the Phase B evidence turned into
// behavior. The bounded error class name is the only thing separating this from
// a provider failure, so getting it wrong would silently record every user
// interrupt as a crash.
func TestCancelledTurnEndsAsCancelled(t *testing.T) {
	actions := replayTrace(t, "cancelled-turn.tsv")
	assertLanes(t, actions, []string{
		"idle",
		"working",
		"end:cancelled",
		"release",
	})
}

// TestProviderErrorEndsAsFailed is the contrast, and it is what makes the test
// above mean something: the identical event sequence with a different error
// name must produce a different outcome.
func TestProviderErrorEndsAsFailed(t *testing.T) {
	actions := replayTrace(t, "provider-error-named.tsv")
	assertLanes(t, actions, []string{
		"idle",
		"working",
		"end:failed",
		"release",
	})
}

// TestUnnamedErrorTraceStillResolvesToIdle covers the Phase A error trace,
// which predates error-name capture. An adapter that cannot read the name must
// still not strand the pane on working -- it reports a failure it cannot
// characterise, which is honest, rather than latching.
func TestUnnamedErrorTraceStillResolvesToIdle(t *testing.T) {
	actions := replayTrace(t, "session-error-turn.tsv")
	last := lanes(actions)
	if len(last) == 0 {
		t.Fatal("no actions")
	}
	var sawEnd bool
	for _, l := range last {
		if strings.HasPrefix(l, "end:") {
			sawEnd = true
			if l != "end:failed" {
				t.Fatalf("an unnamed error should be failed, got %q", l)
			}
		}
	}
	if !sawEnd {
		t.Fatalf("an errored turn produced no terminal outcome: %v", last)
	}
}

// TestRepeatedBusyDoesNotRepeatTheLane pins the suppression rule. OpenCode
// emits session.status busy several times per turn; without this each repeat
// would be a process spawn and a consumed sequence number saying nothing new.
func TestRepeatedBusyDoesNotRepeatTheLane(t *testing.T) {
	var h OpenCodeHandler
	busy := OpenCodeEvent{Type: "session.status", Status: `{"type":"busy"}`}

	first := h.Handle(busy)
	if len(first) != 1 || first[0].State != agentactivity.StateWorking {
		t.Fatalf("first busy = %v", lanes(first))
	}
	for i := 0; i < 5; i++ {
		if got := h.Handle(busy); len(got) != 0 {
			t.Fatalf("repeat %d re-reported: %v", i, lanes(got))
		}
	}
}

// TestBusyClearsAStrandedBlockedLane is the deliberate handling of the matrix's
// recorded gap: the blocked lane is transition-shaped, not state-shaped, so a
// dropped permission.replied does not self-correct the way a dropped busy does.
//
// The compensation is that any later positive status assertion clears blocked.
// That bounds a missed unblock to "until the next status event" instead of
// leaving the pane stranded for the rest of the run.
func TestBusyClearsAStrandedBlockedLane(t *testing.T) {
	var h OpenCodeHandler
	h.Handle(OpenCodeEvent{Type: "session.status", Status: `{"type":"busy"}`})
	h.Handle(OpenCodeEvent{Type: "permission.asked"})

	// permission.replied never arrives -- the recorded failure mode.
	got := h.Handle(OpenCodeEvent{Type: "session.status", Status: `{"type":"busy"}`})
	if len(got) != 1 || got[0].State != agentactivity.StateWorking {
		t.Fatalf("a busy assertion did not clear the stranded blocked lane: %v", lanes(got))
	}
}

// TestUnmatchedPermissionRepliedIsIgnored covers the other direction: a
// resolution for a block that was never reported must not invent a working
// lane, because the pane may legitimately be idle.
func TestUnmatchedPermissionRepliedIsIgnored(t *testing.T) {
	var h OpenCodeHandler
	h.Handle(OpenCodeEvent{Type: "session.status", Status: `{"type":"idle"}`})
	if got := h.Handle(OpenCodeEvent{Type: "permission.replied"}); len(got) != 0 {
		t.Fatalf("an unmatched permission.replied produced %v", lanes(got))
	}
}

// TestATerminalOutcomeLatches stops a trailing status event from resurrecting a
// run that already reported its end. OpenCode emits session.status idle after
// session.error, and without the latch that would be reported as an ordinary
// idle lane arriving after the run was declared over.
func TestATerminalOutcomeLatches(t *testing.T) {
	var h OpenCodeHandler
	h.Handle(OpenCodeEvent{Type: "session.status", Status: `{"type":"busy"}`})
	h.Handle(OpenCodeEvent{Type: "session.error", ErrorName: OpenCodeAbortedError})

	for _, ev := range []OpenCodeEvent{
		{Type: "session.status", Status: `{"type":"idle"}`},
		{Type: "session.status", Status: `{"type":"busy"}`},
		{Type: "permission.asked"},
	} {
		if got := h.Handle(ev); len(got) != 0 {
			t.Fatalf("%s after a terminal outcome produced %v", ev.Type, lanes(got))
		}
	}
}

// TestBundledAssetMatchesTheHandler keeps the shipped JavaScript and the Go
// mirror from drifting into two different mappings.
//
// It cannot prove they behave identically -- one is JS running inside OpenCode,
// the other is Go -- so it checks the things that would actually diverge in
// practice: the identifiers, the discriminator, and the event names each one
// claims to handle. A mapping added to one and not the other shows up here.
func TestBundledAssetMatchesTheHandler(t *testing.T) {
	asset := OpenCodeAsset()
	if asset == "" {
		t.Fatal("the bundled asset is empty")
	}

	for _, want := range []string{
		OpenCodeSource,
		OpenCodeProvider,
		OpenCodeAbortedError,
		"session.status",
		"permission.asked",
		"permission.replied",
		"session.error",
		"session.created",
	} {
		if !strings.Contains(asset, want) {
			t.Fatalf("the bundled asset never mentions %q; the handler and the asset disagree", want)
		}
	}

	// The asset must invoke the Sidecar binary Sidecar itself published, and
	// must never build a shell command out of provider data.
	if !strings.Contains(asset, "SIDECAR_BIN") {
		t.Fatal("the asset does not use the published Sidecar binary path")
	}
	if !strings.Contains(asset, "SIDECAR_MANAGED_SHELL") {
		t.Fatal("the asset does not check the managed-shell cue, so it would run outside Sidecar")
	}
	for _, forbidden := range []string{"exec(", "shell: true", "/bin/sh", "child_process.exec"} {
		if strings.Contains(asset, forbidden) {
			t.Fatalf("the asset uses %q; provider data must never be shell-composed", forbidden)
		}
	}
}

// TestOpenCodeOwnsExactlyOnePluginDirectory pins the recorded double-load gap.
// Both plugin/ and plugins/ are loaded by OpenCode, and an asset present in
// both fires every event twice, which would double every sequence number and
// make ordering meaningless.
func TestOpenCodeOwnsExactlyOnePluginDirectory(t *testing.T) {
	if OpenCodeOwnedDir == OpenCodeConflictDir {
		t.Fatal("the owned and conflicting plugin directories must differ")
	}
	if OpenCodeOwnedDir == "" || OpenCodeConflictDir == "" {
		t.Fatal("both directories must be named so repair can detect the other")
	}
}

// TestCapabilityIsRegisteredForTheBundledSource ties the asset to the registry
// the resolver actually consults. An asset with no capability entry would
// install, report, and be ignored.
func TestCapabilityIsRegisteredForTheBundledSource(t *testing.T) {
	cap, ok := agentlifecycle.CapabilityForSource(OpenCodeSource)
	if !ok {
		t.Fatalf("no capability registered for %q", OpenCodeSource)
	}
	if cap.Provider != OpenCodeProvider {
		t.Fatalf("capability provider = %q", cap.Provider)
	}
	tier, reason := cap.TierFor(agentlifecycle.StatusCurrent, true)
	if tier != agentlifecycle.TierFull {
		t.Fatalf("opencode exercises %q (%s), want full", tier, reason)
	}
}
