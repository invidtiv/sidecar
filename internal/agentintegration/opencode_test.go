package agentintegration

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	var h OpenCodeHandler
	var actions []OpenCodeAction
	for _, ev := range traceEvents(t, name) {
		actions = append(actions, h.Handle(ev)...)
	}
	return actions
}

// syntheticSession stands in for the session id the traces deliberately do not
// record. The node harness uses the same value, which is what keeps the two
// replays comparable.
const syntheticSession = "s1"

// traceEvents parses a sanitized trace into handler events.
func traceEvents(t *testing.T, name string) []OpenCodeEvent {
	t.Helper()
	path := filepath.Join("..", "agentlifecycle", "testdata", "traces", "opencode", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var events []OpenCodeEvent
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		cols := strings.Split(line, "\t")
		if len(cols) < 7 {
			t.Fatalf("malformed trace row: %q", line)
		}
		ev := OpenCodeEvent{Type: cols[2]}
		if cols[5] != "-" {
			// The traces record only that a session id was present, never its
			// value, so a stable synthetic one stands in. The node harness uses
			// the same placeholder, which is what keeps the two comparable.
			ev.SessionID = syntheticSession
		}
		if cols[3] != "-" {
			ev.Status = cols[3]
		}
		if len(cols) > 7 && cols[7] != "-" {
			ev.ErrorName = cols[7]
		}
		events = append(events, ev)
	}
	return events
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

// TestBundledAssetBehavesLikeTheHandler is the real asset-to-handler
// equivalence check, and it replaced a substring-presence test that could not
// fail for the right reason.
//
// That earlier test passed while the shipped JavaScript and this Go mirror
// disagreed about two things that mattered. The asset had no `ended` latch, so
// the trailing session.status idle that follows every session.error superseded
// the terminal report and a cancelled turn was announced as a clean
// completion; and the two used different session.created rules. Neither
// surfaced until the asset was run against a live provider.
//
// So this drives the asset's actual mapping under node, over the same recorded
// traces, and requires the identical ordered argv list -- sequence numbers
// included, because ordering is exactly what broke.
func TestBundledAssetBehavesLikeTheHandler(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed; cannot verify the shipped asset's behavior")
	}

	traces := []string{
		"tool-turn-with-permission.tsv",
		"cancelled-turn.tsv",
		"provider-error-named.tsv",
		"session-error-turn.tsv",
	}
	for _, trace := range traces {
		t.Run(trace, func(t *testing.T) {
			tracePath, err := filepath.Abs(filepath.Join("..", "agentlifecycle", "testdata", "traces", "opencode", trace))
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(node, "replay-harness.mjs", tracePath)
			cmd.Dir = filepath.Join("assets", "opencode")
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("running the asset harness: %v\n%s", err, stderr.String())
			}

			var fromAsset [][]string
			if err := json.Unmarshal(out, &fromAsset); err != nil {
				t.Fatalf("harness output is not JSON: %q (%v)", out, err)
			}
			fromHandler := handlerArgs(t, trace)

			if len(fromAsset) != len(fromHandler) {
				t.Fatalf("the asset emitted %d reports, the handler %d:\nasset:   %v\nhandler: %v",
					len(fromAsset), len(fromHandler), fromAsset, fromHandler)
			}
			for i := range fromHandler {
				if strings.Join(fromAsset[i], " ") != strings.Join(fromHandler[i], " ") {
					t.Fatalf("report %d differs:\nasset:   %v\nhandler: %v", i, fromAsset[i], fromHandler[i])
				}
			}
			if len(fromHandler) == 0 {
				t.Fatal("neither produced any report; this trace proves nothing")
			}
		})
	}
}

// handlerArgs replays a trace through the Go handler and returns the argv each
// action becomes, in order.
func handlerArgs(t *testing.T, trace string) [][]string {
	t.Helper()
	var h OpenCodeHandler
	var out [][]string
	var seq uint64
	for _, ev := range traceEvents(t, trace) {
		for _, action := range h.Handle(ev) {
			seq++
			out = append(out, ReportArgs(action, seq, h.Session()))
		}
	}
	return out
}

// TestTheAssetSerializesReports pins the fix for the ordering defect the live
// exit gate exposed.
//
// Each report is a subprocess taking an exclusive lock on an append-only store
// that enforces a strictly increasing sequence per run. Spawning them
// concurrently assigns sequences in order but delivers them out of order, and
// the store correctly rejects the loser -- which silently dropped the terminal
// `end` report in two live runs out of three. The store's contract is frozen
// and the late-prior-run rejection depends on it, so the asset serializes
// rather than the store loosening.
func TestTheAssetSerializesReports(t *testing.T) {
	asset := OpenCodeAsset()

	// The queue itself: every report is chained onto the previous one.
	if !strings.Contains(asset, "queue = queue.then(") {
		t.Fatal("the asset does not chain reports onto a queue; concurrent spawns lose reports to the store's sequence check")
	}
	// Resolving on exit is what makes the chain mean "the previous report has
	// landed" rather than "the previous report has been started".
	if !strings.Contains(asset, `child.on("exit"`) {
		t.Fatal("the asset does not wait for a report process to exit, so the chain does not order deliveries")
	}
	// A hung report must not stall every later event for the rest of the run.
	if !strings.Contains(asset, "REPORT_TIMEOUT_MS") {
		t.Fatal("the asset has no per-report timeout; one hung subprocess would stall the queue forever")
	}
	if strings.Contains(asset, "detached: true") {
		t.Fatal("reports must not be detached; the queue depends on observing exit")
	}
}

// TestTheAssetFailsOpen checks the properties that keep a reporting failure
// from ever becoming the agent's problem.
func TestTheAssetFailsOpen(t *testing.T) {
	asset := OpenCodeAsset()
	if asset == "" {
		t.Fatal("the bundled asset is empty")
	}

	if !strings.Contains(asset, "SIDECAR_MANAGED_SHELL") {
		t.Fatal("the asset does not check the managed-shell cue, so it would spawn outside Sidecar")
	}
	if !strings.Contains(asset, "SIDECAR_BIN") {
		t.Fatal("the asset does not use the published Sidecar binary path")
	}
	if !strings.Contains(asset, `stdio: "ignore"`) {
		t.Fatal("the asset does not silence report output; it would appear in the agent's own terminal")
	}
	// No shell composition anywhere: every value reaches the CLI as its own
	// argv element.
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

// TestTheAssetExportsOnlyPluginFactories guards the failure mode the live exit
// gate found, and which no Go test could have.
//
// OpenCode's plugin loader requires EVERY export of a plugin module to be a
// plugin factory. One non-function export and the module is imported and then
// never called -- silently, with no error anywhere. Measured against 1.18.25
// with four probe plugins: a lone function export loads; a function plus a
// string export does not; a function plus an object export does not; a function
// carrying its helpers as properties loads.
//
// That is why the asset's pure mapping hangs off SidecarLifecycle rather than
// being exported beside it. The first attempt at making the mapping testable
// exported it, every test here passed, and the plugin reported nothing at all
// against a real provider.
func TestTheAssetExportsOnlyPluginFactories(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed; cannot inspect the asset's export surface")
	}
	cmd := exec.Command(node, "exports-harness.mjs")
	cmd.Dir = filepath.Join("assets", "opencode")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("inspecting the asset's exports: %v\n%s", err, stderr.String())
	}
	var surface struct {
		Names        []string `json:"names"`
		NonFunctions []string `json:"nonFunctions"`
	}
	if err := json.Unmarshal(out, &surface); err != nil {
		t.Fatalf("harness output is not JSON: %q", out)
	}
	if len(surface.NonFunctions) != 0 {
		t.Fatalf("non-function exports %v; OpenCode skips the whole module without an error, "+
			"so the plugin would install cleanly and never report", surface.NonFunctions)
	}
	if len(surface.Names) != 1 || surface.Names[0] != "SidecarLifecycle" {
		t.Fatalf("exports = %v, want exactly [SidecarLifecycle]", surface.Names)
	}
}

// TestReportArgsCarryTheAssetVersion pins that authority stays scoped to a
// source *at a version*. Without it every stored record claims an unknown
// version and an outdated asset can never be detected.
func TestReportArgsCarryTheAssetVersion(t *testing.T) {
	args := ReportArgs(OpenCodeAction{
		Kind:   agentlifecycle.KindState,
		State:  agentactivity.StateWorking,
		Reason: agentlifecycle.ReasonTurnStart,
	}, 1, "s1")

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--source-version "+OpenCodeAssetVersion) {
		t.Fatalf("argv does not carry the asset version: %v", args)
	}

	cap, ok := agentlifecycle.CapabilityForSource(OpenCodeSource)
	if !ok {
		t.Fatal("no capability registered")
	}
	if cap.AssetVersion != OpenCodeAssetVersion {
		t.Fatalf("registry asset version %q != bundled %q; every report would look outdated",
			cap.AssetVersion, OpenCodeAssetVersion)
	}
}

// TestTheAssetSerializesReportsUnderInvertedExitOrder pins the promise chain that makes report
// ordering safe.
//
// This is the defect that cost the Phase B exit gate two attempts, and it was
// invisible to every offline test: each report is a subprocess taking an
// exclusive lock on an append-only store that enforces a strictly increasing
// sequence, so spawning them concurrently assigns sequences in order and
// delivers them out of order. The store then correctly rejects the loser — and
// what was lost, in two live runs out of three, was the terminal `end` report
// carrying the cancelled-versus-failed outcome.
//
// The fix was to chain each spawn onto the previous one's exit. Until now that
// was proven only by running against a real provider, which means a regression
// would have reached a user before it reached a test. This drives the real
// plugin factory against a stub whose processes are rigged to exit in the
// opposite order to the one they were started in, so serialized and concurrent
// produce different recorded orders and the assertion cannot pass by luck.
func TestTheAssetSerializesReportsUnderInvertedExitOrder(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed; cannot drive the shipped asset")
	}
	dir := t.TempDir()
	stub := filepath.Join(dir, "sidecar-stub")
	orderLog := filepath.Join(dir, "order.log")

	cmd := exec.Command(node, "ordering-harness.mjs", stub, orderLog)
	cmd.Dir = filepath.Join("assets", "opencode")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	// The harness process is timed from out here, and that is load-bearing
	// rather than convenient. See the dispose assertion below: the defect being
	// pinned is a pending timer holding Node's event loop open, which delays
	// process *exit* and not the dispose await the harness can time from
	// inside itself. cmd.Output waits for exit, so this is the only clock that
	// can see it.
	started := time.Now()
	out, err := cmd.Output()
	processElapsed := time.Since(started)
	if err != nil {
		t.Fatalf("running the ordering harness: %v\n%s", err, stderr.String())
	}

	var result struct {
		Order     []string `json:"order"`
		ElapsedMS int      `json:"elapsedMs"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("harness output is not JSON: %q (%v)", out, err)
	}
	order := result.Order

	// Four reports: idle/session_start, working/turn_start, end/cancelled, and
	// the release dispose adds. Anything fewer means one was dropped, which is
	// the same failure in a different disguise.
	want := []string{"1", "2", "3", "4"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("reports were delivered in order %v, want %v.\n"+
			"3,2,1,4 is the signature of concurrent spawns: the stub inverts the exit order, so that is what "+
			"unserialized delivery produces and it is exactly what the store rejects.", order, want)
	}

	// The same harness pins a second defect it found. dispose bounds its wait
	// with a timer, and that timer has to be cleared when the queue wins: a
	// pending timer keeps Node's event loop alive, so leaving it held OpenCode's
	// process open for the full budget after every report had already landed —
	// a five second pause added to every quit, in a file whose whole premise is
	// that nothing in it may delay what OpenCode does.
	//
	// The measurement is the harness *process's* lifetime, not the time dispose
	// itself took. Those differ by exactly the bug: dispose returns the moment
	// Promise.race picks the queue, so an uncleared timer leaves its own await
	// looking fast — measured 1098ms with the defect reintroduced — while the
	// process lingers for the full budget behind the live event loop. Measured
	// end to end: 1.26s clean, 5.03s with the clearTimeout removed.
	//
	// What is measured is the TAIL -- how long the process outlived dispose's
	// own return -- rather than absolute wall clock. An absolute bound cannot
	// separate the two under load: a loaded clean run was observed at 4.04s
	// against a 4s bound while the defect sits at ~5.03s, so the two converge
	// exactly when the machine is busy. The tail does not converge, because
	// load inflates the reports on both sides equally while the defect adds a
	// fixed REPORT_TIMEOUT_MS on top of whatever the reports cost: clean leaves
	// a tail near zero, the defect leaves one near the full five second budget.
	tail := processElapsed - time.Duration(result.ElapsedMS)*time.Millisecond
	if tail >= 2*time.Second {
		t.Fatalf("the harness process outlived dispose by %v (process %v, dispose reported %dms); "+
			"the bounding timer is holding the process open after the queue drained",
			tail.Round(time.Millisecond), processElapsed.Round(time.Millisecond), result.ElapsedMS)
	}
}
