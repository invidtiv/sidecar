package shellstate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentsession"
)

const (
	testNS   = "/tmp/socket"
	testLive = "pid=100,start=A"
	testDead = "pid=99,start=Z"
)

func seedShell(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "shells.json")
	if err := AddAtPath(path, Definition{
		TmuxName: name, DisplayName: name, Namespace: testNS,
		CreatedAt: time.Now().UTC().Truncate(time.Second), WorkDir: "/repo",
	}); err != nil {
		t.Fatal(err)
	}
	return path
}

func reportRef(value, generation string) agentsession.Ref {
	return agentsession.Ref{
		Kind: agentsession.RefID, Value: value,
		Source: "sidecar.codex.hooks", Reported: true, Generation: generation,
	}
}

// TestAHookCanReportRotateAndClearAndOnlyTheLiveGenerationWins is the M3 exit
// gate driven through the real persistence path: a fake hook reports, rotates,
// clears, and attempts a stale late update, and the manifest agrees with the
// fence at every step.
func TestAHookCanReportRotateAndClearAndOnlyTheLiveGenerationWins(t *testing.T) {
	path := seedShell(t, "sidecar-sh-p-1")
	id := Identity{TmuxName: "sidecar-sh-p-1", Namespace: testNS}

	bound := func(t *testing.T) (agentsession.Ref, bool) {
		t.Helper()
		ref, _, ok, err := SessionRefAtPath(path, id)
		if err != nil {
			t.Fatal(err)
		}
		return ref, ok
	}

	// Report.
	out, err := BindSessionAtPath(path, id, SessionUpdate{Ref: reportRef("sess-1", testLive), Kind: "codex", Live: testLive})
	if err != nil || out.Decision != agentsession.DecisionRecorded {
		t.Fatalf("first report = %v, %v; wanted recorded", out.Decision, err)
	}
	if ref, ok := bound(t); !ok || ref.Value != "sess-1" {
		t.Fatalf("after the first report the binding is %+v (bound=%v)", ref, ok)
	}

	// Replay is idempotent and must not rewrite the file.
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out, err = BindSessionAtPath(path, id, SessionUpdate{Ref: reportRef("sess-1", testLive), Kind: "codex", Live: testLive})
	if err != nil || out.Decision != agentsession.DecisionUnchanged {
		t.Fatalf("replaying a report = %v, %v; wanted unchanged", out.Decision, err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("replaying an identical report rewrote the manifest; records are written on transitions, not on every event")
	}

	// Rotate.
	out, err = BindSessionAtPath(path, id, SessionUpdate{Ref: reportRef("sess-2", testLive), Kind: "codex", Live: testLive})
	if err != nil || out.Decision != agentsession.DecisionRotated {
		t.Fatalf("rotation = %v, %v; wanted rotated", out.Decision, err)
	}
	if ref, _ := bound(t); ref.Value != "sess-2" {
		t.Fatalf("after rotation the binding is %q", ref.Value)
	}

	// A stale late update from the previous provider generation must change
	// nothing at all.
	frozen, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out, err = BindSessionAtPath(path, id, SessionUpdate{Ref: reportRef("sess-from-the-dead", testDead), Kind: "codex", Live: testLive})
	if out.Decision != agentsession.DecisionIgnored || !errors.Is(err, agentsession.ErrStaleGeneration) {
		t.Fatalf("a late report = %v, %v; wanted ignored + ErrStaleGeneration", out.Decision, err)
	}
	if ref, _ := bound(t); ref.Value != "sess-2" {
		t.Fatalf("a late report overwrote the live binding with %q", ref.Value)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(frozen) != string(current) {
		t.Fatal("a losing report still wrote to the manifest; an ignored report must not even re-stamp a timestamp")
	}

	// A stale clear cannot unbind the live conversation either.
	out, err = BindSessionAtPath(path, id, SessionUpdate{
		Ref: agentsession.Ref{Generation: testDead}, Clear: true, Kind: "codex", Live: testLive,
	})
	if out.Decision != agentsession.DecisionIgnored || !errors.Is(err, agentsession.ErrStaleGeneration) {
		t.Fatalf("a late clear = %v, %v; wanted ignored", out.Decision, err)
	}
	if ref, ok := bound(t); !ok || ref.Value != "sess-2" {
		t.Fatal("a late clear unbound a live conversation")
	}

	// Clear from the live generation.
	out, err = BindSessionAtPath(path, id, SessionUpdate{
		Ref: agentsession.Ref{Generation: testLive}, Clear: true, Kind: "codex", Live: testLive,
	})
	if err != nil || out.Decision != agentsession.DecisionCleared {
		t.Fatalf("clear = %v, %v; wanted cleared", out.Decision, err)
	}
	if _, ok := bound(t); ok {
		t.Fatal("the binding survived an authorised clear")
	}
}

// TestANewProviderGenerationTakesOverThePane separates "late" from "new": the
// same comparison that rejects a dead process's report must let its replacement
// bind the pane.
func TestANewProviderGenerationTakesOverThePane(t *testing.T) {
	path := seedShell(t, "sidecar-sh-p-1")
	id := Identity{TmuxName: "sidecar-sh-p-1", Namespace: testNS}

	if _, err := BindSessionAtPath(path, id, SessionUpdate{Ref: reportRef("old", testDead), Kind: "codex", Live: testDead}); err != nil {
		t.Fatal(err)
	}
	out, err := BindSessionAtPath(path, id, SessionUpdate{Ref: reportRef("new", testLive), Kind: "codex", Live: testLive})
	if err != nil || out.Decision != agentsession.DecisionRotated {
		t.Fatalf("a relaunched provider = %v, %v; wanted rotated", out.Decision, err)
	}
	ref, _, ok, err := SessionRefAtPath(path, id)
	if err != nil || !ok || ref.Value != "new" {
		t.Fatalf("binding after takeover = %+v (bound=%v, err=%v)", ref, ok, err)
	}
}

func TestBindingRefusesAShellItDoesNotKnow(t *testing.T) {
	path := seedShell(t, "sidecar-sh-p-1")
	_, err := BindSessionAtPath(path, Identity{TmuxName: "sidecar-sh-nope", Namespace: testNS},
		SessionUpdate{Ref: reportRef("x", testLive), Kind: "codex", Live: testLive})
	if err == nil || !IsNotFound(err) {
		t.Fatalf("binding an unknown shell = %v; wanted a not-found refusal", err)
	}
}

// TestVersionThreeIsAdditiveOverVersionTwo is the compatibility direction a v3
// binary owns: it must read a v2 file, and rewriting one must not invent agent
// or restore objects on records that never had an agent.
func TestVersionThreeIsAdditiveOverVersionTwo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shells.json")
	source, err := os.ReadFile("testdata/v2-shells.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, source, 0644); err != nil {
		t.Fatal(err)
	}

	// A v2 record reads with no agent or restore object at all.
	defs, err := ListAtPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 1 || defs[0].Agent != nil || defs[0].Restore != nil {
		t.Fatalf("a v2 record decoded with v3 objects: %+v", defs[0])
	}
	// The v2 agentType field still carries the provider; the v3 binding does
	// not replace it.
	if defs[0].AgentType != "codex" {
		t.Fatalf("the v2 agentType field was lost: %+v", defs[0])
	}

	// Writing upgrades the version in place and adds no per-record keys.
	if err := AddAtPath(path, Definition{TmuxName: "sidecar-sh-two", DisplayName: "two", Namespace: "/private/tmp/tmux/default"}); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != CurrentVersion || CurrentVersion != 3 {
		t.Fatalf("version = %d, wanted %d", m.Version, CurrentVersion)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"agent"`) || strings.Contains(string(raw), `"restore"`) {
		t.Fatalf("a v3 rewrite added agent/restore keys to records that have no agent:\n%s", raw)
	}
}

// TestAVersionTwoBinaryWouldRefuseAVersionThreeFile is the other direction, and
// the reason the version was bumped at all: dropping a session reference would
// present as a restore that silently declines to resume, not as an error.
func TestAVersionTwoBinaryWouldRefuseAVersionThreeFile(t *testing.T) {
	// CheckWritableVersion is the whole rule, and a v2 build is this build with
	// CurrentVersion == 2. Asserting the boundary rather than the literal 3
	// keeps the test meaningful after the next bump.
	if err := CheckWritableVersion(CurrentVersion); err != nil {
		t.Fatalf("this build refused its own version: %v", err)
	}
	if err := CheckWritableVersion(CurrentVersion - 1); err != nil {
		t.Fatalf("this build refused an older version it can upgrade: %v", err)
	}
	err := CheckWritableVersion(CurrentVersion + 1)
	if err == nil || !IsUnknownVersion(err) {
		t.Fatalf("a newer file was writable: %v", err)
	}
	if !strings.Contains(err.Error(), "refusing to rewrite") {
		t.Fatalf("the refusal does not say what it refused: %v", err)
	}
}

// TestAVersionThreeManifestRoundTripsItsAgentAndRestoreFields proves the new
// fields survive a write/read cycle intact, including through a writer that has
// nothing to do with sessions.
func TestAVersionThreeManifestRoundTripsItsAgentAndRestoreFields(t *testing.T) {
	path := seedShell(t, "sidecar-sh-p-1")
	id := Identity{TmuxName: "sidecar-sh-p-1", Namespace: testNS}

	if _, err := BindSessionAtPath(path, id, SessionUpdate{Ref: reportRef("sess-1", testLive), Kind: "codex", Live: testLive}); err != nil {
		t.Fatal(err)
	}

	// An unrelated writer must preserve the binding rather than dropping the
	// fields it was not thinking about.
	if _, err := RenameAtPath(path, RenameRequest{TmuxName: id.TmuxName, Namespace: testNS, Name: "renamed"}); err != nil {
		t.Fatal(err)
	}
	if err := AddAtPath(path, Definition{TmuxName: "sidecar-sh-p-2", DisplayName: "second", Namespace: testNS}); err != nil {
		t.Fatal(err)
	}

	ref, kind, ok, err := SessionRefAtPath(path, id)
	if err != nil || !ok {
		t.Fatalf("the binding did not survive unrelated writes: ok=%v err=%v", ok, err)
	}
	if ref.Value != "sess-1" || ref.Kind != agentsession.RefID || !ref.Reported || ref.Source != "sidecar.codex.hooks" {
		t.Fatalf("the reference lost a field: %+v", ref)
	}
	if kind != "codex" {
		t.Fatalf("kind = %q, wanted codex", kind)
	}
	if ref.ReportedAt.IsZero() {
		t.Fatal("the acceptance time was not stamped")
	}

	// The on-disk shape is the one the plan describes.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Version int `json:"version"`
		Shells  []struct {
			TmuxName string `json:"tmuxName"`
			Agent    *struct {
				Kind    string `json:"kind"`
				Session *struct {
					Source   string `json:"source"`
					Kind     string `json:"kind"`
					Value    string `json:"value"`
					Reported bool   `json:"reported"`
				} `json:"session"`
			} `json:"agent"`
		} `json:"shells"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Version != 3 {
		t.Fatalf("on-disk version = %d", decoded.Version)
	}
	var bound, unbound int
	for _, s := range decoded.Shells {
		if s.Agent != nil && s.Agent.Session != nil {
			bound++
			if s.Agent.Session.Value != "sess-1" || s.Agent.Session.Kind != "id" || !s.Agent.Session.Reported {
				t.Fatalf("on-disk session = %+v", s.Agent.Session)
			}
		} else {
			unbound++
		}
	}
	if bound != 1 || unbound != 1 {
		t.Fatalf("wanted exactly one bound and one unbound record, got %d/%d", bound, unbound)
	}
}

func TestSessionHoldersReportsOnlyBoundShells(t *testing.T) {
	path := seedShell(t, "sidecar-sh-p-1")
	if err := AddAtPath(path, Definition{TmuxName: "sidecar-sh-p-2", DisplayName: "two", Namespace: testNS}); err != nil {
		t.Fatal(err)
	}
	id := Identity{TmuxName: "sidecar-sh-p-1", Namespace: testNS}
	if _, err := BindSessionAtPath(path, id, SessionUpdate{Ref: reportRef("sess-1", testLive), Kind: "codex", Live: testLive}); err != nil {
		t.Fatal(err)
	}
	holders, err := SessionHoldersAtPath(path, "proj")
	if err != nil {
		t.Fatal(err)
	}
	if len(holders) != 1 {
		t.Fatalf("holders = %+v, wanted only the bound shell", holders)
	}
	if holders[0].Project != "proj" || holders[0].Session != "sidecar-sh-p-1" || holders[0].Kind != "codex" {
		t.Fatalf("holder = %+v", holders[0])
	}
}

// TestBindingNeverWritesAReferenceThroughAShellString guards the rule that a
// session value is data, never syntax. The binding path accepts whatever the
// validator accepted and stores it as a JSON value.
func TestBindingNeverWritesAReferenceThroughAShellString(t *testing.T) {
	path := seedShell(t, "sidecar-sh-p-1")
	id := Identity{TmuxName: "sidecar-sh-p-1", Namespace: testNS}
	// A value the validator permits but which is still worth proving lands as
	// one JSON string rather than being spliced into anything.
	ref := reportRef("a.b:c-d_e", testLive)
	if _, err := BindSessionAtPath(path, id, SessionUpdate{Ref: ref, Kind: "codex", Live: testLive}); err != nil {
		t.Fatal(err)
	}
	got, _, ok, err := SessionRefAtPath(path, id)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if got.Value != "a.b:c-d_e" {
		t.Fatalf("value round-tripped as %q", got.Value)
	}
}
