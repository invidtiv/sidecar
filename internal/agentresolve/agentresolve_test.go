package agentresolve

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/agentlifecycle"
)

var resolveNow = time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)

// TestNoEvidenceIsExactlyDetect is the proof that extracting one resolver out of
// three duplicated call sites changed no behavior.
//
// It runs every checked-in provider screen fixture through both the old path
// (agentactivity.Detect, which the three sites called directly) and the new one
// (Resolve with no lifecycle source) and requires the results to be identical
// field for field. If they ever diverge, every surface's screen detection has
// silently changed and the Phase A compatibility fixtures were pinning the
// wrong thing.
func TestNoEvidenceIsExactlyDetect(t *testing.T) {
	fixtures := loadProviderFixtures(t)
	if len(fixtures) == 0 {
		t.Fatal("no provider fixtures found; this test would prove nothing")
	}

	for _, fx := range fixtures {
		t.Run(fx.provider+"/"+fx.name, func(t *testing.T) {
			ob := agentactivity.Observation{
				Agent:      fx.provider,
				Screen:     fx.screen,
				CapturedAt: resolveNow,
			}
			want := agentactivity.Detect(ob)

			for _, ref := range []PaneRef{
				{},
				{PaneID: "%7"},
				{Session: "sidecar-sh-1"},
				{PaneID: "%7", Session: "sidecar-sh-1"},
			} {
				got := Result(ob, ref, nil, resolveNow)
				if got != want {
					t.Fatalf("ref %+v changed the screen answer:\n got %+v\nwant %+v", ref, got, want)
				}
			}

			// A source that has nothing to say about this pane must be
			// indistinguishable from no source at all.
			got := Result(ob, PaneRef{PaneID: "%7"}, silentSource{}, resolveNow)
			if got != want {
				t.Fatalf("a silent source changed the screen answer:\n got %+v\nwant %+v", got, want)
			}
		})
	}
}

// TestNoEvidenceStillExplainsItself checks that the identity path is not
// silent. A pane with no integration is the overwhelmingly common case, and
// "why is nothing driving this?" has to have an answer there too.
func TestNoEvidenceExplainsItself(t *testing.T) {
	ob := agentactivity.Observation{Agent: "opencode", Screen: "idle", CapturedAt: resolveNow}
	dec := Resolve(ob, PaneRef{PaneID: "%7"}, nil, resolveNow)

	if dec.Explanation.Authority != agentlifecycle.AuthorityScreen {
		t.Fatalf("authority = %q", dec.Explanation.Authority)
	}
	if dec.Explanation.Tier != agentlifecycle.TierScreenFallback {
		t.Fatalf("tier = %q", dec.Explanation.Tier)
	}
	if dec.Explanation.FallbackReason != agentlifecycle.ReasonNoIntegration {
		t.Fatalf("fallback reason = %q", dec.Explanation.FallbackReason)
	}
	if dec.Explanation.Identity.PaneID != "%7" {
		t.Fatalf("explanation lost the pane: %+v", dec.Explanation.Identity)
	}
	if dec.Explanation.ScreenState != dec.Result.State {
		t.Fatal("the explanation disagrees with the result it explains")
	}
}

// TestFullAuthorityWinsThroughTheSharedResolver is the end of the arbitration
// path as a surface actually calls it: the screen says one thing, the provider
// says another, and the provider wins because it earned full authority.
func TestFullAuthorityWinsThroughTheSharedResolver(t *testing.T) {
	// A screen that positively reads as working.
	ob := agentactivity.Observation{Agent: "opencode", Screen: "Working", CapturedAt: resolveNow}
	screen := agentactivity.Detect(ob)

	src := &stubSource{ev: fullAuthorityEvidence(agentactivity.StateBlocked, agentlifecycle.ReasonPermissionRequest)}
	dec := Resolve(ob, PaneRef{PaneID: "%7"}, src, resolveNow)

	if dec.Result.State != agentactivity.StateBlocked {
		t.Fatalf("state = %q, want blocked (screen said %q)", dec.Result.State, screen.State)
	}
	if dec.Explanation.Authority != agentlifecycle.AuthorityLifecycle {
		t.Fatalf("authority = %q", dec.Explanation.Authority)
	}
	if !dec.Result.VisibleBlocker {
		t.Fatal("a reported blocker must be visible so agentstatus raises attention")
	}
	// The screen's opinion stays on the record so the disagreement is
	// diagnosable rather than invisible.
	if dec.Explanation.ScreenState != screen.State {
		t.Fatalf("screen state was not preserved: %q", dec.Explanation.ScreenState)
	}
}

// TestReleaseAndExitReturnToScreenImmediately covers the two ways authority
// ends. Neither may leave the pane holding its last reported lane: a stale
// guess is worse than an honest fallback, and "immediately" means on the very
// next resolution, with no grace period.
func TestReleaseAndExitReturnToScreenImmediately(t *testing.T) {
	ob := agentactivity.Observation{Agent: "opencode", Screen: "Working", CapturedAt: resolveNow}
	screen := agentactivity.Detect(ob)

	t.Run("explicit release", func(t *testing.T) {
		ev := fullAuthorityEvidence(agentactivity.StateBlocked, agentlifecycle.ReasonPermissionRequest)
		ev.Latest.Kind = agentlifecycle.KindRelease
		ev.Latest.State = ""
		ev.Latest.Reason = agentlifecycle.ReasonIntegrationRemoved

		dec := Resolve(ob, PaneRef{PaneID: "%7"}, &stubSource{ev: ev}, resolveNow)
		if dec.Result != screen {
			t.Fatalf("release did not return to the screen result: %+v", dec.Result)
		}
		if dec.Explanation.FallbackReason != agentlifecycle.ReasonAuthorityRelease {
			t.Fatalf("fallback reason = %q", dec.Explanation.FallbackReason)
		}
	})

	t.Run("process exit", func(t *testing.T) {
		ev := fullAuthorityEvidence(agentactivity.StateBlocked, agentlifecycle.ReasonPermissionRequest)
		ev.ProcessAlive = false

		dec := Resolve(ob, PaneRef{PaneID: "%7"}, &stubSource{ev: ev}, resolveNow)
		if dec.Result != screen {
			t.Fatalf("a dead process kept authority: %+v", dec.Result)
		}
		if dec.Explanation.FallbackReason != agentlifecycle.ReasonProcessExited {
			t.Fatalf("fallback reason = %q", dec.Explanation.FallbackReason)
		}
	})

	t.Run("a report from a previous run", func(t *testing.T) {
		ev := fullAuthorityEvidence(agentactivity.StateBlocked, agentlifecycle.ReasonPermissionRequest)
		ev.Latest.Identity.RunID = "run-previous"

		dec := Resolve(ob, PaneRef{PaneID: "%7"}, &stubSource{ev: ev}, resolveNow)
		if dec.Result != screen {
			t.Fatalf("an old run kept authority: %+v", dec.Result)
		}
		if dec.Explanation.FallbackReason != agentlifecycle.ReasonRunMismatch {
			t.Fatalf("fallback reason = %q", dec.Explanation.FallbackReason)
		}
	})
}

// TestTheThreeCallSitesGoThroughOneResolver is a source-level guard, and it is
// here because the failure it prevents is invisible in behavior.
//
// The M6 plan is explicit that changing only workspaceinventory would leave the
// project Workspace surface and its notifications screen-only. Nothing about a
// fourth direct Detect call would fail a behavioral test today, because with no
// lifecycle source installed the two paths agree exactly — it would only start
// being wrong once an integration was installed, on one surface, silently. So
// the invariant is asserted where it can actually be checked.
//
// agentactivity.Detect stays legitimate inside agentactivity itself and inside
// this package, which is the one place allowed to call it.
func TestTheThreeCallSitesGoThroughOneResolver(t *testing.T) {
	sites := []string{
		filepath.Join("..", "plugins", "workspace", "agent.go"),
		filepath.Join("..", "plugins", "workspace", "shell.go"),
		filepath.Join("..", "workspaceinventory", "inventory.go"),
	}
	for _, site := range sites {
		t.Run(filepath.Base(filepath.Dir(site))+"/"+filepath.Base(site), func(t *testing.T) {
			data, err := os.ReadFile(site)
			if err != nil {
				t.Fatal(err)
			}
			src := string(data)
			if strings.Contains(src, "agentactivity.Detect(") {
				t.Fatal("this call site still calls agentactivity.Detect directly; " +
					"lifecycle evidence would never reach it. Route it through agentresolve.")
			}
			if !strings.Contains(src, "agentresolve.") {
				t.Fatal("this call site no longer goes through agentresolve")
			}
		})
	}
}

// --- helpers ---

type silentSource struct{}

func (silentSource) Evidence(PaneRef) (Evidence, bool) { return Evidence{}, false }

type stubSource struct{ ev Evidence }

func (s *stubSource) Evidence(PaneRef) (Evidence, bool) { return s.ev, true }

func liveIdentity() agentlifecycle.Identity {
	return agentlifecycle.Identity{
		Host:              "host-a",
		ServerIncarnation: "pid=4242",
		PaneID:            "%7",
		Provider:          "opencode",
		RunID:             "run-1",
		ProcessGeneration: "pid=99,start=x",
	}
}

func fullAuthorityEvidence(state agentactivity.State, reason agentlifecycle.ReasonCode) Evidence {
	report := &agentlifecycle.Report{
		SchemaVersion: agentlifecycle.SchemaVersion,
		ID:            "rpt-1",
		Kind:          agentlifecycle.KindState,
		Identity:      liveIdentity(),
		Source:        "sidecar.opencode.plugin",
		SourceVersion: "1",
		Sequence:      3,
		State:         state,
		ObservedAt:    resolveNow.Add(-2 * time.Second),
		Reason:        reason,
	}
	return Evidence{
		Live:         liveIdentity(),
		ProcessAlive: true,
		Capability: agentlifecycle.Capability{
			SchemaVersion: agentlifecycle.SchemaVersion,
			Provider:      "opencode",
			Source:        "sidecar.opencode.plugin",
			AssetVersion:  "1",
			Tier:          agentlifecycle.TierFull,
			Evidence:      agentlifecycle.EvidenceRealTrace,
			Covered:       agentlifecycle.FullLifecycleTransitions(),
		},
		Status:                agentlifecycle.StatusCurrent,
		ProviderInTestedRange: true,
		Latest:                report,
	}
}

type providerFixture struct {
	provider string
	name     string
	screen   string
}

// loadProviderFixtures reads the checked-in screen captures agentactivity's own
// detector tests use, so the identity proof runs against real provider output
// rather than invented strings.
func loadProviderFixtures(t *testing.T) []providerFixture {
	t.Helper()
	root := filepath.Join("..", "agentactivity", "testdata")
	providers, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var out []providerFixture
	for _, p := range providers {
		if !p.IsDir() || p.Name() == "proof" {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, p.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || filepath.Ext(f.Name()) != ".txt" {
				continue
			}
			data, err := os.ReadFile(filepath.Join(root, p.Name(), f.Name()))
			if err != nil {
				continue
			}
			out = append(out, providerFixture{provider: p.Name(), name: f.Name(), screen: string(data)})
		}
	}
	return out
}
