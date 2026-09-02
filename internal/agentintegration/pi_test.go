package agentintegration

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/agentlifecycle"
)

// The Pi suite.
//
// It is in two halves. The first drives the mapping over the fixtures in
// testdata/pi, which are translations of Herdr's own herdr-agent-state.test.ts
// rather than captured traces -- see that directory's README for why the
// distinction matters and why Pi's capability entry is docs-only because of it.
// The second is the installer contract, which is the same contract OpenCode's
// suite pins, asserted again here because an adapter that satisfies it by
// accident is an adapter that stops satisfying it on the next edit.

// piFixture builds a service against a temporary tree with pi on PATH and pi's
// agent directory already present, which is the state a machine is in after pi
// has been run once.
func piFixture(t *testing.T, opts ...func(*Env)) (Service, Env, piPaths) {
	t.Helper()
	home := t.TempDir()
	env := Env{
		Home: home,
		LookPath: func(file string) (string, error) {
			if file == PiProvider {
				return filepath.Join(home, "bin", "pi"), nil
			}
			return "", errors.New("not found")
		},
		ProviderVersion: func(string) string { return "0.84.3" },
		UID:             os.Getuid(),
	}
	for _, o := range opts {
		o(&env)
	}
	paths := piPathsFor(env)
	if err := os.MkdirAll(paths.AgentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return Service{Env: env, Adapters: DefaultAdapters()}, env, paths
}

func withoutPi(e *Env) {
	e.LookPath = func(string) (string, error) { return "", errors.New("not found") }
}

func piStatus(t *testing.T, s Service) Status {
	t.Helper()
	st, err := s.Status(PiProvider)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	return st
}

func piApply(t *testing.T, s Service, act Action) Plan {
	t.Helper()
	p, err := s.Apply(PiProvider, act)
	if err != nil {
		t.Fatalf("%s: %v", act, err)
	}
	return p
}

// piEvents parses a fixture into handler events.
//
// The column layout is the one the node harness reads, and both readers are
// deliberately literal about the tri-state `idle` column: Pi's ctx.isIdle can be
// absent, and "absent" is neither true nor false to either of the guards that
// read it.
func piEvents(t *testing.T, name string) []PiEvent {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "pi", name))
	if err != nil {
		t.Fatal(err)
	}
	var events []PiEvent
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		cols := strings.Split(line, "\t")
		if len(cols) != 9 {
			t.Fatalf("malformed fixture row in %s (%d columns, want 9): %q", name, len(cols), line)
		}
		text := func(s string) string {
			if s == "-" {
				return ""
			}
			return s
		}
		tri := func(s string) *bool {
			if s == "-" {
				return nil
			}
			v := s == "true"
			return &v
		}
		events = append(events, PiEvent{
			Type:          cols[1],
			Reason:        text(cols[2]),
			Mode:          text(cols[3]),
			Idle:          tri(cols[4]),
			SessionPath:   text(cols[5]),
			SessionID:     text(cols[6]),
			BlockedActive: cols[7] == "true",
			BlockedLabel:  text(cols[8]),
		})
	}
	if len(events) == 0 {
		t.Fatalf("%s has no rows", name)
	}
	return events
}

// piReplay drives a fixture through the handler and returns what it produced.
func piReplay(t *testing.T, name string) []PiAction {
	t.Helper()
	var h PiHandler
	var actions []PiAction
	for _, ev := range piEvents(t, name) {
		actions = append(actions, h.Handle(ev)...)
	}
	return actions
}

// piLanes renders actions the way the assertions below read them: a lane name
// for a state report, "bind:" plus the reference for a session binding.
func piLanes(actions []PiAction) []string {
	var out []string
	for _, a := range actions {
		switch a.Kind {
		case agentlifecycle.KindState:
			out = append(out, string(a.State)+"/"+string(a.Reason))
		case agentlifecycle.KindSession:
			if a.SessionPath != "" {
				out = append(out, "bind:path="+a.SessionPath)
				continue
			}
			out = append(out, "bind:id="+a.SessionID)
		default:
			out = append(out, "?"+string(a.Kind))
		}
	}
	return out
}

func assertPiLanes(t *testing.T, got []PiAction, want ...string) {
	t.Helper()
	have := piLanes(got)
	if !reflect.DeepEqual(have, want) {
		t.Fatalf("actions = %v, want %v", have, want)
	}
}

// TestPiReportsIdleOnlyAfterTheAgentSettles is upstream's central Pi fixture
// (herdr-agent-state.test.ts:243), and it is the one that pins the trap the
// event names hide: a settlement arriving while the run is still active is
// stale and must produce nothing at all.
func TestPiReportsIdleOnlyAfterTheAgentSettles(t *testing.T) {
	assertPiLanes(t, piReplay(t, "idle-only-after-settle.tsv"),
		"idle/session_start",
		"working/turn_start",
		// The third row is agent_settled with isIdle() false. It emits nothing,
		// which is why there is no entry for it here.
		"idle/turn_complete",
	)
}

// TestPiDoesNotTreatAgentEndAsTurnCompletion is the other half of upstream's
// assertion that its completion handlers are exactly ["agent_settled"].
//
// agent_end means "this attempt stopped": Pi can follow it with an automatic
// retry or a compaction, so reporting idle on it announces a finished turn in
// the middle of one. The handler ignores it and the asset never subscribes, and
// both are asserted because either one alone would let the pair drift.
func TestPiDoesNotTreatAgentEndAsTurnCompletion(t *testing.T) {
	var h PiHandler
	idle := true
	notIdle := false
	h.Handle(PiEvent{Type: "session_start", Reason: "startup", Mode: "tui", Idle: &idle})
	h.Handle(PiEvent{Type: "agent_start", Mode: "tui", Idle: &notIdle})

	if got := h.Handle(PiEvent{Type: "agent_end", Mode: "tui", Idle: &idle}); len(got) != 0 {
		t.Fatalf("agent_end produced %v; a retry or a compaction can follow it", piLanes(got))
	}

	asset := PiAsset()
	for _, forbidden := range []string{`pi.on("agent_end"`, `pi.on("session_shutdown"`} {
		if strings.Contains(asset, forbidden) {
			t.Fatalf("the asset subscribes to %s; see the deliberately-not-subscribed note it contradicts", forbidden)
		}
	}
}

// TestPiIgnoresRPCSessionsEvenWhenUIAPIsAreAvailable pins why the gate is on
// ctx.mode and not on ctx.hasUI. An RPC session reports hasUI true while being
// headless, so a hasUI gate would claim a pane with no agent on screen.
func TestPiIgnoresRPCSessionsEvenWhenUIAPIsAreAvailable(t *testing.T) {
	if got := piReplay(t, "rpc-session-is-ignored.tsv"); len(got) != 0 {
		t.Fatalf("an RPC session reported %v", piLanes(got))
	}
}

// TestPiReloadPreservesWorkingState covers the recovery upstream added for a
// reload replacing the extension mid-run: there is no second agent_start, so the
// run's true state has to be read back from ctx rather than assumed idle.
func TestPiReloadPreservesWorkingState(t *testing.T) {
	assertPiLanes(t, piReplay(t, "reload-preserves-working.tsv"), "working/session_change")
}

// TestPiUnknownIdlenessIsNotReportedAsWorking is the tri-state half of the same
// rule, and it is the direction the fixture cannot show. `=== false` rather than
// `!== true` is deliberate: an extension host with no isIdle tells us nothing,
// and nothing must not become a working claim.
func TestPiUnknownIdlenessIsNotReportedAsWorking(t *testing.T) {
	var h PiHandler
	got := h.Handle(PiEvent{Type: "session_start", Reason: "reload", Mode: "tui", Idle: nil})
	assertPiLanes(t, got, "idle/session_change")

	// The same absence must not close a turn either.
	notIdle := false
	h.Handle(PiEvent{Type: "agent_start", Mode: "tui", Idle: &notIdle})
	if settled := h.Handle(PiEvent{Type: "agent_settled", Mode: "tui", Idle: nil}); len(settled) != 0 {
		t.Fatalf("an unknown idleness closed a turn: %v", piLanes(settled))
	}
}

// TestPiSettlementPreservesBlockedPrecedence is the only test that drives the
// blocked branch, which no released Pi can reach: Pi ships no permission system,
// so nothing publishes the channel the asset listens on. The ladder keeps the
// branch because it costs one comparison and upstream's fixture drives it
// directly, and the capability entry records the transition as structurally
// unreachable rather than merely untraced.
func TestPiSettlementPreservesBlockedPrecedence(t *testing.T) {
	assertPiLanes(t, piReplay(t, "blocked-outranks-settle.tsv"),
		"idle/session_start",
		"working/turn_start",
		"blocked/permission_request",
		// agent_settled lands here and emits nothing: blocked outranks working
		// and idle, so the desired lane has not changed.
		"idle/permission_resolved",
	)
}

// TestPiBindsTheSessionBeforeTheFirstStateReport is the mapping half of
// upstream's ordering fixture. Upstream expresses it as an awaited session
// report; here the binding is simply first in the action list, and the asset's
// serialized queue is what turns "first" into "its process has exited".
func TestPiBindsTheSessionBeforeTheFirstStateReport(t *testing.T) {
	assertPiLanes(t, piReplay(t, "session-replacement-binds-then-reports.tsv"),
		"bind:path=/tmp/pi-new.jsonl",
		"idle/session_change",
	)
}

// TestPiRebindsTheSessionOnEveryTurn keeps upstream's per-turn re-assertion,
// which is what recovers a binding Sidecar lost to a restart mid-session.
func TestPiRebindsTheSessionOnEveryTurn(t *testing.T) {
	assertPiLanes(t, piReplay(t, "agent-start-rebinds-the-session.tsv"),
		"bind:path=/tmp/pi-a.jsonl",
		"idle/session_start",
		"bind:path=/tmp/pi-a.jsonl",
		"working/turn_start",
	)
}

// TestPiBindsAWindowsSessionPath is the upstream bug this port fixes. Herdr's Pi
// asset accepts a session path only when it starts with "/", so every Windows
// path is silently discarded; its OMP variant fixed exactly this and has a test
// for it, and the Pi variant never received the fix.
func TestPiBindsAWindowsSessionPath(t *testing.T) {
	assertPiLanes(t, piReplay(t, "windows-session-path-is-bound.tsv"),
		`bind:path=C:\Users\User\.pi\agent\sessions\s.jsonl`,
		"idle/session_change",
	)
	for _, path := range []string{"C:/Users/User/.pi/sessions/s.jsonl", "/tmp/s.jsonl", "d:\\x"} {
		if !piAbsoluteSessionPath(path) {
			t.Fatalf("%q was rejected as a session path", path)
		}
	}
	for _, path := range []string{"", "relative/s.jsonl", "./s.jsonl", "C:relative"} {
		if piAbsoluteSessionPath(path) {
			t.Fatalf("%q was accepted as a session path", path)
		}
	}
}

// TestPiFallsBackToTheSessionIdAndDiscardsARelativePath covers both remaining
// reference shapes. A path names the exact transcript a restore would resume, so
// it wins where both are known; a relative path names nothing and is not sent at
// all.
func TestPiFallsBackToTheSessionIdAndDiscardsARelativePath(t *testing.T) {
	assertPiLanes(t, piReplay(t, "session-id-only-binds-by-id.tsv"),
		"bind:id=pi-id-only",
		"idle/session_start",
	)
	assertPiLanes(t, piReplay(t, "relative-session-path-is-discarded.tsv"),
		"bind:id=pi-rel",
		"idle/session_start",
	)
}

// TestPiSuppressesAnExactRepeat pins the rule that keeps the queue's depth equal
// to the number of genuine state changes rather than the event rate, which is
// half the argument for serializing instead of coalescing.
func TestPiSuppressesAnExactRepeat(t *testing.T) {
	var h PiHandler
	idle, notIdle := true, false
	h.Handle(PiEvent{Type: "session_start", Reason: "startup", Mode: "tui", Idle: &idle})
	if got := h.Handle(PiEvent{Type: "agent_start", Mode: "tui", Idle: &notIdle}); len(got) != 1 {
		t.Fatalf("first agent_start = %v", piLanes(got))
	}
	for i := 0; i < 5; i++ {
		if got := h.Handle(PiEvent{Type: "agent_start", Mode: "tui", Idle: &notIdle}); len(got) != 0 {
			t.Fatalf("repeat %d re-reported: %v", i, piLanes(got))
		}
	}
}

// TestPiReportsNothingBeforeATuiSessionStart pins the rootSession latch. Until a
// TUI session_start has been seen, this integration has not established that it
// is on a pane at all, and reporting from an RPC or print invocation would claim
// one it is not on screen in.
func TestPiReportsNothingBeforeATuiSessionStart(t *testing.T) {
	var h PiHandler
	idle := true
	for _, ev := range []PiEvent{
		{Type: "agent_start", Mode: "tui", Idle: &idle},
		{Type: "agent_settled", Mode: "tui", Idle: &idle},
		{Type: "blocked", BlockedActive: true, BlockedLabel: "approval"},
	} {
		if got := h.Handle(ev); len(got) != 0 {
			t.Fatalf("%s before a TUI session_start produced %v", ev.Type, piLanes(got))
		}
	}
}

// TestPiReportArgsCarryTheAssetVersionAndOmitTheBlockedLabel pins two separate
// things about the wire shape.
//
// Authority stays scoped to a source at a version, so every state report carries
// one; without it every stored record claims an unknown version and an outdated
// asset can never be detected. And the blocked label never leaves the process:
// it is unbounded text authored by another extension, and nothing but lanes,
// bounded codes and conversation identifiers goes over this wire.
func TestPiReportArgsCarryTheAssetVersionAndOmitTheBlockedLabel(t *testing.T) {
	args := PiReportArgs(PiAction{
		Kind:   agentlifecycle.KindState,
		State:  agentactivity.StateBlocked,
		Reason: agentlifecycle.ReasonPermissionRequest,
	}, 7, "pi-session")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--source-version "+PiAssetVersion) {
		t.Fatalf("argv does not carry the asset version: %v", args)
	}
	if !strings.Contains(joined, "--seq 7") || !strings.Contains(joined, "--session-id pi-session") {
		t.Fatalf("argv lost the sequence or the session id: %v", args)
	}
	if strings.Contains(joined, "--detail") {
		t.Fatalf("argv carries a detail field; the blocked label must not be transmitted: %v", args)
	}

	// A binding is not a point in an ordered stream, so its verb takes no
	// sequence and consumes none.
	bind := PiAction{Kind: agentlifecycle.KindSession, SessionPath: "/tmp/s.jsonl"}
	if PiCarriesSequence(bind) {
		t.Fatal("a session binding was told it carries a sequence; report-session has no --seq flag")
	}
	bindArgs := strings.Join(PiReportArgs(bind, 9, "pi-session"), " ")
	if strings.Contains(bindArgs, "--seq") {
		t.Fatalf("the binding argv carries --seq, which report-session would reject as usage: %s", bindArgs)
	}
	if !strings.Contains(bindArgs, "--kind "+PiProvider) || !strings.Contains(bindArgs, "--path /tmp/s.jsonl") {
		t.Fatalf("the binding argv is not a report-session call: %s", bindArgs)
	}

	cap, ok := agentlifecycle.CapabilityForSource(PiSource)
	if !ok {
		t.Fatal("no capability registered")
	}
	if cap.AssetVersion != PiAssetVersion {
		t.Fatalf("registry asset version %q != bundled %q; every report would look outdated",
			cap.AssetVersion, PiAssetVersion)
	}
}

// TestPiCapabilityIsRegisteredForTheBundledSource ties the asset to the registry
// the resolver actually consults. An asset with no capability entry would
// install, report, and be ignored.
//
// The tier is asserted at exactly what the port earned, and no higher. Pi has no
// traces, and the lifecycle plan caps an untraced source at advisory in three
// separate places; a source with no traces at all gets the identity claim its
// own code demonstrably makes and nothing more. When traces land, this
// expectation and capabilities.json move together, which is the point of
// asserting it here rather than leaving the registry to say it alone.
func TestPiCapabilityIsRegisteredForTheBundledSource(t *testing.T) {
	cap, ok := agentlifecycle.CapabilityForSource(PiSource)
	if !ok {
		t.Fatalf("no capability registered for %q", PiSource)
	}
	if cap.Provider != PiProvider {
		t.Fatalf("capability provider = %q", cap.Provider)
	}
	if cap.Evidence != agentlifecycle.EvidenceDocsOnly {
		t.Fatalf("pi claims %q evidence; nothing has traced a live Pi session", cap.Evidence)
	}
	tier, reason := cap.TierFor(agentlifecycle.StatusCurrent, true)
	if tier != agentlifecycle.TierSessionIdentity {
		t.Fatalf("pi exercises %q (%s), want session-identity until traces exist", tier, reason)
	}
	// The transitions the asset does not report must not be claimed. tool_use
	// has no subscription, and process_exit is deliberately absent because
	// session_shutdown fires for three reasons that are not an exit.
	for _, absent := range []agentlifecycle.Transition{
		agentlifecycle.TransitionToolUse,
		agentlifecycle.TransitionProcessExit,
		agentlifecycle.TransitionBlockedOnRequest,
		agentlifecycle.TransitionUnblocked,
	} {
		if cap.Covers(absent) {
			t.Fatalf("pi claims %q, which the shipped asset does not report", absent)
		}
	}
	if !cap.Covers(agentlifecycle.TransitionSessionIdentity) {
		t.Fatal("pi does not claim session_identity, which is the whole of what it earned")
	}
	// Full is structurally unreachable, so the entry must never be typed up to
	// it by accident.
	if cap.CoversFullLifecycle() {
		t.Fatal("pi claims full lifecycle coverage; no released Pi can produce a blocked signal")
	}
}

// TestBundledPiAssetBehavesLikeTheHandler is the real asset-to-handler
// equivalence check.
//
// It is the same mechanism that caught two live defects in the OpenCode pair
// after a substring test had passed over both, and it is the reason this asset
// ships as .js: node cannot import a .ts module, so there is no version of this
// test that runs the shipped file otherwise.
//
// It drives the asset's actual mapping under node over the same fixtures and
// requires the identical ordered argv list -- sequence numbers included, because
// ordering and sequence assignment are exactly what break.
func TestBundledPiAssetBehavesLikeTheHandler(t *testing.T) {
	node := requireNode(t, "the shipped Pi asset's mapping against the checked-in fixtures")

	fixtures := []struct {
		name   string
		silent bool // the RPC fixture is expected to produce nothing at all
	}{
		{name: "idle-only-after-settle.tsv"},
		{name: "reload-preserves-working.tsv"},
		{name: "blocked-outranks-settle.tsv"},
		{name: "session-replacement-binds-then-reports.tsv"},
		{name: "agent-start-rebinds-the-session.tsv"},
		{name: "windows-session-path-is-bound.tsv"},
		{name: "session-id-only-binds-by-id.tsv"},
		{name: "relative-session-path-is-discarded.tsv"},
		{name: "rpc-session-is-ignored.tsv", silent: true},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			path, err := filepath.Abs(filepath.Join("testdata", "pi", fixture.name))
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(node, "replay-harness.mjs", path)
			cmd.Dir = filepath.Join("assets", "pi")
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
			fromHandler := piHandlerArgs(t, fixture.name)

			if len(fromAsset) != len(fromHandler) {
				t.Fatalf("the asset emitted %d reports, the handler %d:\nasset:   %v\nhandler: %v",
					len(fromAsset), len(fromHandler), fromAsset, fromHandler)
			}
			for i := range fromHandler {
				if strings.Join(fromAsset[i], " ") != strings.Join(fromHandler[i], " ") {
					t.Fatalf("report %d differs:\nasset:   %v\nhandler: %v", i, fromAsset[i], fromHandler[i])
				}
			}
			// A fixture that produces nothing on both sides proves nothing --
			// unless producing nothing is the assertion, which is exactly what
			// the RPC case is.
			if len(fromHandler) == 0 && !fixture.silent {
				t.Fatal("neither produced any report; this fixture proves nothing")
			}
			if len(fromHandler) != 0 && fixture.silent {
				t.Fatalf("the RPC fixture produced %v; a headless session must claim no pane", fromHandler)
			}
		})
	}
}

// piHandlerArgs replays a fixture through the Go handler and returns the argv
// each action becomes, in order.
//
// The sequence rule is the asset's: only the verbs that carry --seq consume one,
// so the state stream's sequence stays gapless while bindings pass through it.
func piHandlerArgs(t *testing.T, fixture string) [][]string {
	t.Helper()
	var h PiHandler
	var out [][]string
	var seq uint64
	for _, ev := range piEvents(t, fixture) {
		for _, action := range h.Handle(ev) {
			if PiCarriesSequence(action) {
				seq++
			}
			out = append(out, PiReportArgs(action, seq, h.Session()))
		}
	}
	return out
}

// TestThePiAssetExportsOnlyPluginFactories guards a failure mode no Go test can
// see. Pi's loader takes a module's default export and drops the module when it
// is not a function -- silently, with no error anywhere. The extension then
// installs cleanly, loads, and reports nothing at all.
func TestThePiAssetExportsOnlyPluginFactories(t *testing.T) {
	node := requireNode(t, "the Pi asset's export surface, which Pi silently drops the whole module for")
	cmd := exec.Command(node, "exports-harness.mjs")
	cmd.Dir = filepath.Join("assets", "pi")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("inspecting the asset's exports: %v\n%s", err, stderr.String())
	}
	var surface struct {
		Names         []string `json:"names"`
		NonFunctions  []string `json:"nonFunctions"`
		DefaultIsFunc bool     `json:"defaultIsFunction"`
		DefaultName   string   `json:"defaultName"`
	}
	if err := json.Unmarshal(out, &surface); err != nil {
		t.Fatalf("harness output is not JSON: %q", out)
	}
	if !surface.DefaultIsFunc {
		t.Fatal("the default export is not a function; Pi would import the module and never call it")
	}
	if len(surface.NonFunctions) != 0 {
		t.Fatalf("non-function exports %v; OpenCode rejects a whole module for this and the two assets "+
			"hold to one export convention", surface.NonFunctions)
	}
	if len(surface.Names) != 1 || surface.Names[0] != "default" {
		t.Fatalf("exports = %v, want exactly [default]", surface.Names)
	}
	if surface.DefaultName != "SidecarLifecycle" {
		t.Fatalf("the default export is named %q; the pure mapping hangs off it by name", surface.DefaultName)
	}
}

// TestThePiAssetSerializesReportsAndBindsFirst pins the two runtime properties
// the pure mapping cannot show.
//
// Each report is a subprocess taking an exclusive lock on an append-only store
// that enforces a strictly increasing sequence per run, so spawning them
// concurrently assigns sequences in order and delivers them out of order -- and
// the store correctly rejects the loser. That defect silently dropped OpenCode's
// terminal report in two live runs out of three, and it is pinned here before Pi
// ever runs live.
//
// It also pins the ordering upstream expresses as an awaited session report: the
// `agent report-session` process has exited before `agent report` is spawned.
// The stub inverts the exit order, so serialized and concurrent produce
// different recorded orders and the assertion cannot pass by luck.
func TestThePiAssetSerializesReportsAndBindsFirst(t *testing.T) {
	node := requireNode(t, "that the shipped Pi asset serializes its reports and binds the session first")
	dir := t.TempDir()
	stub := filepath.Join(dir, "sidecar-stub")
	orderLog := filepath.Join(dir, "order.log")

	cmd := exec.Command(node, "ordering-harness.mjs", stub, orderLog)
	cmd.Dir = filepath.Join("assets", "pi")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
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

	want := []string{"session", "1", "2", "3"}
	if !reflect.DeepEqual(result.Order, want) {
		t.Fatalf("reports were delivered in order %v, want %v.\n"+
			"3,2,1,session is the signature of concurrent spawns: the stub inverts the exit order, so that "+
			"is what unserialized delivery produces and it is exactly what the store rejects. A first "+
			"element that is not \"session\" means a state report raced the binding it depends on.",
			result.Order, want)
	}
}

// TestThePiAssetFailsOpen checks the properties that keep a reporting failure
// from ever becoming the agent's problem.
func TestThePiAssetFailsOpen(t *testing.T) {
	asset := PiAsset()
	if asset == "" {
		t.Fatal("the bundled asset is empty")
	}
	if !strings.HasPrefix(asset, Marker((PiAdapter{}).asset())) {
		t.Fatal("the asset does not open with the marker the installer identifies it by")
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
	if !strings.Contains(asset, "REPORT_TIMEOUT_MS") {
		t.Fatal("the asset has no per-report timeout; one hung subprocess would stall the queue forever")
	}
	if !strings.Contains(asset, "queue = queue.then(") {
		t.Fatal("the asset does not chain reports onto a queue; concurrent spawns lose reports to the store's sequence check")
	}
	if !strings.Contains(asset, `child.on("exit"`) {
		t.Fatal("the asset does not wait for a report process to exit, so the chain does not order deliveries")
	}
	if strings.Contains(asset, "detached: true") {
		t.Fatal("reports must not be detached; the queue depends on observing exit")
	}
	// No shell composition anywhere: every value reaches the CLI as its own
	// argv element.
	for _, forbidden := range []string{"exec(", "shell: true", "/bin/sh", "child_process.exec"} {
		if strings.Contains(asset, forbidden) {
			t.Fatalf("the asset uses %q; provider data must never be shell-composed", forbidden)
		}
	}
	// Herdr's transport must not have come along with the mapping. Claiming to
	// be Herdr is what the parity plan's first decision refuses.
	for _, forbidden := range []string{"HERDR_ENV", "HERDR_SOCKET_PATH", "HERDR_PANE_ID", "node:net"} {
		if strings.Contains(asset, forbidden) {
			t.Fatalf("the asset still references %q; the transport half is Sidecar's", forbidden)
		}
	}
	// The blocked channel is Sidecar's own namespace and not Herdr's. Consuming
	// Herdr's would mean that on a machine with both installed, one project's
	// approval protocol drives the other project's lane -- the identity
	// collision the parity plan's first decision exists to prevent. The channel
	// name appears in prose here, so what is asserted is the subscription.
	if !strings.Contains(asset, `const BLOCKED_CHANNEL = "sidecar:blocked"`) {
		t.Fatal("the blocked channel is not Sidecar's own namespace")
	}
	if strings.Contains(asset, `on("herdr:blocked"`) {
		t.Fatal("the asset subscribes to Herdr's blocked channel")
	}
}

// TestPiAgentDirIsResolvedTheWayPiResolvesIt pins the directory facts a plan
// draft got wrong. There is no PI_CONFIG_DIR -- that is Herdr's own override for
// OMP -- and the tilde expansion is Pi's, not a convenience: a Sidecar that did
// not expand it would install into a literal "~" directory while Pi read
// somewhere else.
func TestPiAgentDirIsResolvedTheWayPiResolvesIt(t *testing.T) {
	home := "/home/u"
	for _, tc := range []struct {
		override string
		want     string
	}{
		{override: "", want: "/home/u/.pi/agent"},
		{override: "~/elsewhere/agent", want: "/home/u/elsewhere/agent"},
		{override: "~", want: "/home/u"},
		{override: "/opt/pi/agent", want: "/opt/pi/agent"},
		{override: "  ", want: "/home/u/.pi/agent"},
	} {
		got := piAgentDir(Env{Home: home, PiAgentDir: tc.override})
		if got != tc.want {
			t.Fatalf("PI_CODING_AGENT_DIR=%q resolved to %q, want %q", tc.override, got, tc.want)
		}
	}
	// The asset lands in <agent dir>/extensions, which is where Pi's loader
	// scans, and nowhere else.
	paths := piPathsFor(Env{Home: home})
	if paths.Owned != "/home/u/.pi/agent/extensions/sidecar-lifecycle.js" {
		t.Fatalf("the asset path is %q", paths.Owned)
	}
}

// TestPiInstallIntoACleanTreeIsExplicitAndIdempotent is the installer's basic
// contract: the operations are named before they happen, and running the same
// action twice is a no-op rather than a second write.
func TestPiInstallIntoACleanTreeIsExplicitAndIdempotent(t *testing.T) {
	svc, _, paths := piFixture(t)

	if st := piStatus(t, svc); st.Status != agentlifecycle.StatusNotInstalled {
		t.Fatalf("a clean tree reports %s", st.Status)
	}

	plan := piApply(t, svc, ActionInstall)
	if len(plan.Ops) != 2 || plan.Ops[0].Kind != OpMkdir || plan.Ops[1].Kind != OpWrite {
		t.Fatalf("install ops = %+v, want mkdir then write", plan.Ops)
	}
	if plan.StatusAfter != agentlifecycle.StatusCurrent {
		t.Fatalf("status after install = %s", plan.StatusAfter)
	}

	installed, err := os.ReadFile(paths.Owned)
	if err != nil {
		t.Fatal(err)
	}
	if string(installed) != PiAsset() {
		t.Fatal("the installed bytes are not the bundled asset")
	}
	if !strings.HasPrefix(string(installed), "// sidecar-integration: id="+PiSource) {
		t.Fatal("the installed file does not carry the marker ownership is decided by")
	}

	again, err := svc.Apply(PiProvider, ActionInstall)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if !again.Unchanged || len(again.Ops) != 0 {
		t.Fatalf("a second install was not a no-op: %+v", again)
	}
}

// TestAPiDryRunAndTheRealRunDescribeTheSameOperations is what makes --dry-run
// honest: the preview is the mutation with the execution step skipped, produced
// by the same function.
func TestAPiDryRunAndTheRealRunDescribeTheSameOperations(t *testing.T) {
	svc, _, _ := piFixture(t)
	preview, err := svc.Plan(PiProvider, ActionInstall)
	if err != nil {
		t.Fatal(err)
	}
	if !preview.DryRun {
		t.Fatal("a planned action did not mark itself a dry run")
	}
	real := piApply(t, svc, ActionInstall)
	if len(preview.Ops) != len(real.Ops) {
		t.Fatalf("dry run planned %d ops, the real run %d", len(preview.Ops), len(real.Ops))
	}
	for i := range preview.Ops {
		a, b := preview.Ops[i], real.Ops[i]
		if a.Kind != b.Kind || a.Path != b.Path || a.Checksum != b.Checksum || a.Mode != b.Mode {
			t.Fatalf("op %d differs:\ndry: %+v\nreal: %+v", i, a, b)
		}
	}
}

// TestPiRefusesWhenPiHasNeverBeenSetUp keeps Herdr's ensure_extension_dir
// semantics without copying its error shape.
//
// Pi's agent directory is created by Pi, so its absence means Pi has never run
// here, and creating a whole ~/.pi/agent tree for it would be Sidecar inventing
// a provider's private state. The refusal is Sidecar's own code so a surface can
// branch on it, and uninstall is still allowed, because cleanup must not depend
// on the provider being healthy.
func TestPiRefusesWhenPiHasNeverBeenSetUp(t *testing.T) {
	home := t.TempDir()
	env := Env{
		Home: home,
		LookPath: func(file string) (string, error) {
			if file == PiProvider {
				return filepath.Join(home, "bin", "pi"), nil
			}
			return "", errors.New("not found")
		},
		ProviderVersion: func(string) string { return "0.84.3" },
		UID:             os.Getuid(),
	}
	svc := Service{Env: env, Adapters: DefaultAdapters()}

	_, err := svc.Plan(PiProvider, ActionInstall)
	r := refusalFrom(t, err)
	if r.Code != RefuseProviderMissing {
		t.Fatalf("install refused with %s, want provider_missing", r.Code)
	}
	if !strings.Contains(r.Message, "agent directory") {
		t.Fatalf("the refusal does not say what is missing: %s", r.Message)
	}
	if !strings.Contains(r.Message, "PI_CODING_AGENT_DIR") {
		t.Fatalf("the refusal does not name the override that would move it: %s", r.Message)
	}

	// Nothing was created by the refusal.
	if _, err := os.Stat(piPathsFor(env).AgentDir); !os.IsNotExist(err) {
		t.Fatal("a refused install created pi's agent directory anyway")
	}
}

// TestAMissingPiProviderRefusesInstallButStillAllowsCleanup covers the other
// missing-provider direction. Removing pi must not strand a file Sidecar wrote.
func TestAMissingPiProviderRefusesInstallButStillAllowsCleanup(t *testing.T) {
	svc, env, paths := piFixture(t)
	piApply(t, svc, ActionInstall)

	gone := Service{Env: env, Adapters: DefaultAdapters()}
	gone.Env.LookPath = func(string) (string, error) { return "", errors.New("not found") }

	st, err := gone.Status(PiProvider)
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != agentlifecycle.StatusProviderMissing {
		t.Fatalf("status with no pi = %s", st.Status)
	}
	if st.EffectiveTier != agentlifecycle.TierScreenFallback {
		t.Fatalf("a missing provider still claims tier %s", st.EffectiveTier)
	}
	if _, err := gone.Plan(PiProvider, ActionInstall); refusalFrom(t, err).Code != RefuseProviderMissing {
		t.Fatal("install with no pi was not refused as provider_missing")
	}
	if _, err := gone.Apply(PiProvider, ActionUninstall); err != nil {
		t.Fatalf("uninstall with no pi: %v", err)
	}
	if _, err := os.Stat(paths.Owned); !os.IsNotExist(err) {
		t.Fatal("uninstall left the asset behind")
	}
}

// TestPiNeverAdoptsOverwritesOrDeletesAFileItDoesNotOwn is the ownership rule,
// and it is the one Herdr's own uninstall does not have: it deletes its file
// without checking a marker. Sidecar removes only what it can prove it wrote.
func TestPiNeverAdoptsOverwritesOrDeletesAFileItDoesNotOwn(t *testing.T) {
	svc, env, paths := piFixture(t)
	if err := os.MkdirAll(paths.OwnedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := "// somebody else's extension\nexport default function () {}\n"
	if err := os.WriteFile(paths.Owned, []byte(foreign), 0o644); err != nil {
		t.Fatal(err)
	}

	st := piStatus(t, svc)
	if st.Status != agentlifecycle.StatusNeedsRepair {
		t.Fatalf("a foreign file at the asset path reports %s", st.Status)
	}
	for _, act := range []Action{ActionInstall, ActionUpdate, ActionRepair, ActionUninstall} {
		_, err := svc.Plan(PiProvider, act)
		code := refusalFrom(t, err).Code
		if code != RefuseForeignFile && code != RefuseNeedsRepair {
			t.Fatalf("%s against a foreign file refused with %s", act, code)
		}
	}
	after, err := os.ReadFile(paths.Owned)
	if err != nil || string(after) != foreign {
		t.Fatal("a file Sidecar does not own was modified")
	}

	// A marker belonging to another integration is not ownership either.
	if err := os.WriteFile(paths.Owned, []byte("// sidecar-integration: id="+OpenCodeSource+" schema=1 version=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if inspectFile(env, paths.Owned, (PiAdapter{}).asset()).Owned {
		t.Fatal("an asset belonging to another integration was claimed as Pi's")
	}
}

// TestASymlinkAtThePiAssetPathIsRefusedRatherThanFollowed pins the Lstat rule. A
// symlink here would make an ordinary Stat report a healthy regular file while
// the write landed wherever the link pointed.
func TestASymlinkAtThePiAssetPathIsRefusedRatherThanFollowed(t *testing.T) {
	svc, _, paths := piFixture(t)
	if err := os.MkdirAll(paths.OwnedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "elsewhere.js")
	if err := os.WriteFile(target, []byte("untouched\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, paths.Owned); err != nil {
		t.Fatal(err)
	}

	for _, act := range Actions() {
		if _, err := svc.Plan(PiProvider, act); err == nil {
			t.Fatalf("%s followed a symlink at the asset path", act)
		}
	}
	if got, _ := os.ReadFile(target); string(got) != "untouched\n" {
		t.Fatal("a write landed outside the directory Sidecar owns")
	}
}

// TestAWorldWritablePiExtensionDirectoryIsRefused: anyone in that group could
// replace the file Pi loads and executes, so installing into it would be handing
// that away.
func TestAWorldWritablePiExtensionDirectoryIsRefused(t *testing.T) {
	svc, _, paths := piFixture(t)
	if err := os.MkdirAll(paths.OwnedDir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(paths.OwnedDir, 0o777); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Plan(PiProvider, ActionInstall)
	if code := refusalFrom(t, err).Code; code != RefuseUnsafeMode && code != RefuseNeedsRepair {
		t.Fatalf("a world-writable extension directory refused with %s", code)
	}
}

// TestPiStatusComesFromTheInstalledBytesNotFromAClaimedVersion: a hand-edited
// asset whose marker still claims the current version is needs-repair, not
// current.
func TestPiStatusComesFromTheInstalledBytesNotFromAClaimedVersion(t *testing.T) {
	svc, _, paths := piFixture(t)
	piApply(t, svc, ActionInstall)

	tampered := PiAsset() + "\n// somebody edited this\n"
	if err := os.WriteFile(paths.Owned, []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}
	st := piStatus(t, svc)
	if st.Status != agentlifecycle.StatusNeedsRepair {
		t.Fatalf("a tampered asset reports %s", st.Status)
	}
	if st.EffectiveTier != agentlifecycle.TierScreenFallback {
		t.Fatalf("a damaged asset still claims tier %s", st.EffectiveTier)
	}
	if _, err := svc.Apply(PiProvider, ActionRepair); err != nil {
		t.Fatalf("repair: %v", err)
	}
	if st := piStatus(t, svc); st.Status != agentlifecycle.StatusCurrent {
		t.Fatalf("status after repair = %s", st.Status)
	}
}

// TestAnOutdatedPiAssetIsUpdatedAndTheReplacedCopyIsRecoverable pins the backup.
func TestAnOutdatedPiAssetIsUpdatedAndTheReplacedCopyIsRecoverable(t *testing.T) {
	svc, _, paths := piFixture(t)
	if err := os.MkdirAll(paths.OwnedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := "// sidecar-integration: id=" + PiSource + " schema=1 version=0\n// an older asset\n"
	if err := os.WriteFile(paths.Owned, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}

	st := piStatus(t, svc)
	if st.Status != agentlifecycle.StatusOutdated {
		t.Fatalf("an older version reports %s", st.Status)
	}
	if _, err := svc.Plan(PiProvider, ActionInstall); refusalFrom(t, err).Code != RefuseAlreadyInstalled {
		t.Fatal("install over an outdated asset was not refused in favour of update")
	}

	plan := piApply(t, svc, ActionUpdate)
	if len(plan.Ops) != 2 || plan.Ops[0].Kind != OpBackup || plan.Ops[1].Kind != OpWrite {
		t.Fatalf("update ops = %+v, want backup then write", plan.Ops)
	}
	backup, err := os.ReadFile(paths.Backup)
	if err != nil {
		t.Fatalf("the replaced asset is not recoverable: %v", err)
	}
	if string(backup) != old {
		t.Fatal("the backup is not the asset that was replaced")
	}
}

// TestPiUninstallLeavesUnrelatedExtensionsExactlyAsItFoundThem is the case a
// machine with Herdr installed is actually in: Herdr's own Pi extension lives in
// this same directory, and it is not Sidecar's.
func TestPiUninstallLeavesUnrelatedExtensionsExactlyAsItFoundThem(t *testing.T) {
	svc, _, paths := piFixture(t)
	piApply(t, svc, ActionInstall)

	neighbours := map[string]string{
		"herdr-agent-state.ts": "// HERDR_INTEGRATION_ID=pi\n// HERDR_INTEGRATION_VERSION=8\n",
		"working-indicator.ts": "export default function () {}\n",
	}
	for name, content := range neighbours {
		if err := os.WriteFile(filepath.Join(paths.OwnedDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	piApply(t, svc, ActionUninstall)

	if _, err := os.Stat(paths.Owned); !os.IsNotExist(err) {
		t.Fatal("uninstall left Sidecar's own asset behind")
	}
	for name, content := range neighbours {
		got, err := os.ReadFile(filepath.Join(paths.OwnedDir, name))
		if err != nil {
			t.Fatalf("uninstall removed %s, which is not Sidecar's: %v", name, err)
		}
		if string(got) != content {
			t.Fatalf("uninstall modified %s", name)
		}
	}
	if _, err := os.Stat(paths.OwnedDir); err != nil {
		t.Fatal("uninstall removed a directory that still holds other extensions")
	}
}

// TestPiUninstallRemovesTheExtensionDirectoryOnlyWhenSidecarEmptiedIt is the
// other half of the same rule.
func TestPiUninstallRemovesTheExtensionDirectoryOnlyWhenSidecarEmptiedIt(t *testing.T) {
	svc, _, paths := piFixture(t)
	piApply(t, svc, ActionInstall)
	piApply(t, svc, ActionUninstall)

	if _, err := os.Stat(paths.OwnedDir); !os.IsNotExist(err) {
		t.Fatal("a directory holding nothing but Sidecar's asset was left behind")
	}
	// The agent directory is Pi's, not Sidecar's, and survives.
	if _, err := os.Stat(paths.AgentDir); err != nil {
		t.Fatalf("uninstall removed pi's own agent directory: %v", err)
	}
}

// TestPiOfferedActionsAreExactlyTheOnesThatWouldNotRefuse: a pill that refuses
// when pressed is worse than one that is not there.
func TestPiOfferedActionsAreExactlyTheOnesThatWouldNotRefuse(t *testing.T) {
	for _, stage := range []string{"clean", "installed", "no-provider"} {
		t.Run(stage, func(t *testing.T) {
			var svc Service
			switch stage {
			case "clean":
				svc, _, _ = piFixture(t)
			case "installed":
				svc, _, _ = piFixture(t)
				piApply(t, svc, ActionInstall)
			case "no-provider":
				svc, _, _ = piFixture(t, withoutPi)
			}
			st := piStatus(t, svc)
			offered := map[Action]bool{}
			for _, a := range st.Offered {
				offered[a] = true
			}
			for _, act := range Actions() {
				_, err := svc.Plan(PiProvider, act)
				if offered[act] != (err == nil) {
					t.Fatalf("%s: offered=%v but planning %s",
						act, offered[act], describeErr(err))
				}
			}
		})
	}
}

// TestThePiAdapterReportsEveryPathItTouches keeps the status honest about where
// it would write, which is what a surface names before asking for confirmation.
func TestThePiAdapterReportsEveryPathItTouches(t *testing.T) {
	svc, env, paths := piFixture(t)
	st := piStatus(t, svc)
	if len(st.TargetPaths) != 1 || st.TargetPaths[0] != paths.Owned {
		t.Fatalf("target paths = %v, want just %s", st.TargetPaths, paths.Owned)
	}
	if got := PiPaths(env); len(got) != 1 || got[0] != paths.Owned {
		t.Fatalf("PiPaths = %v", got)
	}
	var sawOwned bool
	for _, f := range st.Files {
		if f.Path == paths.Owned {
			sawOwned = true
		}
	}
	if !sawOwned {
		t.Fatalf("Inspect did not report the asset path at all: %+v", st.Files)
	}
	if st.ProviderVersion != "0.84.3" {
		t.Fatalf("the provider version was not read: %q", st.ProviderVersion)
	}
}
