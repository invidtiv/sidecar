package agentlifecycle

import (
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
)

// resolveNow is the fixed clock every arbitration row is stated against.
var resolveNow = time.Date(2026, 8, 30, 17, 0, 0, 0, time.UTC)

// liveIdentity is the pane and run every row resolves for. Rows that test a
// discontinuity mutate one field of the *report's* copy, never this one.
func liveIdentity() Identity {
	return Identity{
		Host:               "local",
		ServerIncarnation:  "present inode=10 ctime=20 pid=30",
		PaneID:             "%7",
		Provider:           "opencode",
		RunID:              "run-1",
		ProcessGeneration:  "pid=4242 start=1000",
		SessionFingerprint: "sf-abc123",
	}
}

// provedCapability is a source that genuinely earned full authority: real
// traces covering every transition in FullLifecycleTransitions.
func provedCapability() Capability {
	return Capability{
		SchemaVersion: SchemaVersion,
		Provider:      "opencode",
		Source:        "sidecar.opencode.plugin",
		AssetVersion:  "1",
		Tier:          TierFull,
		Evidence:      EvidenceRealTrace,
		Covered:       FullLifecycleTransitions(),
	}
}

func capabilityAt(tier Tier) Capability {
	c := provedCapability()
	c.Tier = tier
	return c
}

// stateReport is a fresh, matching, lane-asserting report.
func stateReport(state agentactivity.State, reason ReasonCode) *Report {
	return &Report{
		SchemaVersion: SchemaVersion,
		ID:            "rpt-1",
		Kind:          KindState,
		Identity:      liveIdentity(),
		Source:        "sidecar.opencode.plugin",
		SourceVersion: "1",
		Sequence:      7,
		State:         state,
		ObservedAt:    resolveNow.Add(-2 * time.Second),
		Reason:        reason,
	}
}

// withReport applies a mutation to a copy of a report, so a row can state
// exactly one difference from the healthy case.
func withReport(r *Report, edit func(*Report)) *Report {
	c := *r
	edit(&c)
	return &c
}

// screen is the ordinary detector result a row pretends the pane produced.
func screen(state agentactivity.State, evidence string) agentactivity.Result {
	return agentactivity.Result{State: state, Evidence: evidence}
}

// healthyInput is full authority, fresh report, matching identity, live
// process. Every row starts here and changes as little as possible.
func healthyInput() Input {
	return Input{
		Now:                   resolveNow,
		Live:                  liveIdentity(),
		ProcessAlive:          true,
		Capability:            provedCapability(),
		Status:                StatusCurrent,
		ProviderInTestedRange: true,
		Latest:                stateReport(agentactivity.StateBlocked, ReasonPermissionRequest),
		Screen:                screen(agentactivity.StateWorking, "opencode.screen.progress-working"),
	}
}

func mutate(edit func(*Input)) Input {
	in := healthyInput()
	edit(&in)
	return in
}

// TestArbitrationTable is the Phase A exit gate.
//
// It states every authority tier crossed with every freshness and identity
// condition the M6 plan's tier table names, and asserts the resolved lane, the
// authority that produced it, the exercised tier, and the reason. The coverage
// assertions at the end are the part that makes it a gate rather than a
// sample: they fail if any tier, freshness, authority, or fallback reason in
// the frozen vocabularies is never exercised by a row above, so adding a
// vocabulary entry without proving its behavior breaks the build.
func TestArbitrationTable(t *testing.T) {
	tests := []struct {
		name string
		in   Input

		wantState     agentactivity.State
		wantAuthority Authority
		wantTier      Tier
		wantFreshness Freshness
		wantReason    FallbackReason
		// wantTierReason is checked only when non-empty.
		wantTierReason FallbackReason
	}{
		// ---- Tier: full lifecycle ----
		{
			name:          "full authority authors blocked over a contradicting screen",
			in:            healthyInput(),
			wantState:     agentactivity.StateBlocked,
			wantAuthority: AuthorityLifecycle,
			wantTier:      TierFull,
			wantFreshness: FreshnessFresh,
		},
		{
			name: "full authority authors idle while the screen still shows working",
			in: mutate(func(in *Input) {
				in.Latest = stateReport(agentactivity.StateIdle, ReasonTurnComplete)
			}),
			wantState:     agentactivity.StateIdle,
			wantAuthority: AuthorityLifecycle,
			wantTier:      TierFull,
			wantFreshness: FreshnessFresh,
		},
		{
			name: "full authority and screen agreeing is still one lifecycle answer",
			in: mutate(func(in *Input) {
				in.Latest = stateReport(agentactivity.StateWorking, ReasonTurnStart)
			}),
			wantState:     agentactivity.StateWorking,
			wantAuthority: AuthorityLifecycle,
			wantTier:      TierFull,
			wantFreshness: FreshnessFresh,
		},

		// ---- Freshness ----
		{
			name: "a working report past its window falls back to the screen",
			in: mutate(func(in *Input) {
				in.Latest = withReport(stateReport(agentactivity.StateWorking, ReasonTurnStart), func(r *Report) {
					r.ObservedAt = resolveNow.Add(-31 * time.Minute)
				})
			}),
			wantState:     agentactivity.StateWorking,
			wantAuthority: AuthorityScreen,
			wantTier:      TierFull,
			wantFreshness: FreshnessStale,
			wantReason:    ReasonReportStale,
		},
		{
			name: "a blocked report inside the longer blocked window still authors",
			in: mutate(func(in *Input) {
				in.Latest = withReport(stateReport(agentactivity.StateBlocked, ReasonPermissionRequest), func(r *Report) {
					r.ObservedAt = resolveNow.Add(-45 * time.Minute)
				})
			}),
			wantState:     agentactivity.StateBlocked,
			wantAuthority: AuthorityLifecycle,
			wantTier:      TierFull,
			wantFreshness: FreshnessFresh,
		},
		{
			name: "an idle report past the idle window falls back",
			in: mutate(func(in *Input) {
				in.Latest = withReport(stateReport(agentactivity.StateIdle, ReasonTurnComplete), func(r *Report) {
					r.ObservedAt = resolveNow.Add(-9 * time.Hour)
				})
			}),
			wantState:     agentactivity.StateWorking,
			wantAuthority: AuthorityScreen,
			wantTier:      TierFull,
			wantFreshness: FreshnessStale,
			wantReason:    ReasonReportStale,
		},

		// ---- Release and end ----
		{
			name: "an explicit release surrenders authority immediately",
			in: mutate(func(in *Input) {
				in.Latest = withReport(stateReport(agentactivity.StateBlocked, ReasonIntegrationRemoved), func(r *Report) {
					r.Kind = KindRelease
					r.State = ""
				})
			}),
			wantState:     agentactivity.StateWorking,
			wantAuthority: AuthorityScreen,
			wantTier:      TierFull,
			wantFreshness: FreshnessReleased,
			wantReason:    ReasonAuthorityRelease,
		},
		{
			name: "an end report clears authority and returns to the screen",
			in: mutate(func(in *Input) {
				in.Latest = withReport(stateReport(agentactivity.StateIdle, ReasonTurnComplete), func(r *Report) {
					r.Kind = KindEnd
					r.State = ""
					r.Outcome = OutcomeCompleted
				})
			}),
			wantState:     agentactivity.StateWorking,
			wantAuthority: AuthorityScreen,
			wantTier:      TierFull,
			wantFreshness: FreshnessReleased,
			wantReason:    ReasonRunEnded,
		},
		{
			name: "a cancelled end is a release, not a lane",
			in: mutate(func(in *Input) {
				in.Latest = withReport(stateReport(agentactivity.StateIdle, ReasonCancelled), func(r *Report) {
					r.Kind = KindEnd
					r.State = ""
					r.Outcome = OutcomeCancelled
				})
			}),
			wantState:     agentactivity.StateWorking,
			wantAuthority: AuthorityScreen,
			wantTier:      TierFull,
			wantFreshness: FreshnessReleased,
			wantReason:    ReasonRunEnded,
		},
		{
			name: "a session-only latest report asserts no lane",
			in: mutate(func(in *Input) {
				in.Latest = withReport(stateReport(agentactivity.StateIdle, ReasonSessionStart), func(r *Report) {
					r.Kind = KindSession
					r.State = ""
				})
			}),
			wantState:     agentactivity.StateWorking,
			wantAuthority: AuthorityScreen,
			wantTier:      TierFull,
			wantFreshness: FreshnessNone,
			wantReason:    ReasonNoReport,
		},
		{
			name:          "no report at all is an ordinary screen answer",
			in:            mutate(func(in *Input) { in.Latest = nil }),
			wantState:     agentactivity.StateWorking,
			wantAuthority: AuthorityScreen,
			wantTier:      TierFull,
			wantFreshness: FreshnessNone,
			wantReason:    ReasonNoReport,
		},

		// ---- Identity discontinuity ----
		{
			name: "a report from another host cannot author",
			in: mutate(func(in *Input) {
				in.Latest = withReport(in.Latest, func(r *Report) { r.Identity.Host = "elsewhere" })
			}),
			wantState:     agentactivity.StateWorking,
			wantAuthority: AuthorityScreen,
			wantTier:      TierFull,
			wantFreshness: FreshnessNone,
			wantReason:    ReasonHostMismatch,
		},
		{
			name: "a recycled pane id from a previous server cannot inherit authority",
			in: mutate(func(in *Input) {
				in.Latest = withReport(in.Latest, func(r *Report) { r.Identity.ServerIncarnation = "gone inode=1 ctime=2 pid=3" })
			}),
			wantState:     agentactivity.StateWorking,
			wantAuthority: AuthorityScreen,
			wantTier:      TierFull,
			wantFreshness: FreshnessNone,
			wantReason:    ReasonServerIncarnationNew,
		},
		{
			name: "a report for another pane cannot author",
			in: mutate(func(in *Input) {
				in.Latest = withReport(in.Latest, func(r *Report) { r.Identity.PaneID = "%9" })
			}),
			wantState:     agentactivity.StateWorking,
			wantAuthority: AuthorityScreen,
			wantTier:      TierFull,
			wantFreshness: FreshnessNone,
			wantReason:    ReasonPaneMismatch,
		},
		{
			name: "a report naming another provider cannot author",
			in: mutate(func(in *Input) {
				in.Latest = withReport(in.Latest, func(r *Report) { r.Identity.Provider = "codex" })
			}),
			wantState:     agentactivity.StateWorking,
			wantAuthority: AuthorityScreen,
			wantTier:      TierFull,
			wantFreshness: FreshnessNone,
			wantReason:    ReasonProviderMismatch,
		},
		{
			name: "a late report from the previous run cannot regain authority",
			in: mutate(func(in *Input) {
				in.Latest = withReport(in.Latest, func(r *Report) { r.Identity.RunID = "run-0" })
			}),
			wantState:     agentactivity.StateWorking,
			wantAuthority: AuthorityScreen,
			wantTier:      TierFull,
			wantFreshness: FreshnessNone,
			wantReason:    ReasonRunMismatch,
		},
		{
			name: "a restarted provider process invalidates the previous generation",
			in: mutate(func(in *Input) {
				in.Latest = withReport(in.Latest, func(r *Report) { r.Identity.ProcessGeneration = "pid=1 start=1" })
			}),
			wantState:     agentactivity.StateWorking,
			wantAuthority: AuthorityScreen,
			wantTier:      TierFull,
			wantFreshness: FreshnessNone,
			wantReason:    ReasonProcessGenChanged,
		},
		{
			name: "a rotated provider session invalidates the report",
			in: mutate(func(in *Input) {
				in.Latest = withReport(in.Latest, func(r *Report) { r.Identity.SessionFingerprint = "sf-rotated" })
			}),
			wantState:     agentactivity.StateWorking,
			wantAuthority: AuthorityScreen,
			wantTier:      TierFull,
			wantFreshness: FreshnessNone,
			wantReason:    ReasonSessionMismatch,
		},
		{
			name: "an absent session fingerprint is not a rotation",
			in: mutate(func(in *Input) {
				in.Latest = withReport(in.Latest, func(r *Report) { r.Identity.SessionFingerprint = "" })
			}),
			wantState:     agentactivity.StateBlocked,
			wantAuthority: AuthorityLifecycle,
			wantTier:      TierFull,
			wantFreshness: FreshnessFresh,
		},
		{
			name: "a report from an unregistered source cannot author",
			in: mutate(func(in *Input) {
				in.Latest = withReport(in.Latest, func(r *Report) { r.Source = "someone.else" })
			}),
			wantState:     agentactivity.StateWorking,
			wantAuthority: AuthorityScreen,
			wantTier:      TierFull,
			wantFreshness: FreshnessNone,
			wantReason:    ReasonSourceMismatch,
		},
		{
			name:          "a dead provider process ends authority without waiting for staleness",
			in:            mutate(func(in *Input) { in.ProcessAlive = false }),
			wantState:     agentactivity.StateWorking,
			wantAuthority: AuthorityScreen,
			wantTier:      TierFull,
			wantFreshness: FreshnessNone,
			wantReason:    ReasonProcessExited,
		},

		// ---- Tier: advisory ----
		{
			name: "advisory evidence agreeing with the screen authors the lane",
			in: mutate(func(in *Input) {
				in.Capability = capabilityAt(TierAdvisory)
				in.Latest = stateReport(agentactivity.StateWorking, ReasonTurnStart)
			}),
			wantState:     agentactivity.StateWorking,
			wantAuthority: AuthorityLifecycle,
			wantTier:      TierAdvisory,
			wantFreshness: FreshnessFresh,
		},
		{
			name: "advisory evidence contradicting the screen defers to the screen",
			in: mutate(func(in *Input) {
				in.Capability = capabilityAt(TierAdvisory)
			}),
			wantState:     agentactivity.StateWorking,
			wantAuthority: AuthorityScreen,
			wantTier:      TierAdvisory,
			wantFreshness: FreshnessFresh,
			wantReason:    ReasonTierAdvisory,
		},
		{
			name: "advisory evidence speaks when the screen has no opinion",
			in: mutate(func(in *Input) {
				in.Capability = capabilityAt(TierAdvisory)
				in.Screen = screen(agentactivity.StateUnknown, "no-match")
			}),
			wantState:     agentactivity.StateBlocked,
			wantAuthority: AuthorityLifecycle,
			wantTier:      TierAdvisory,
			wantFreshness: FreshnessFresh,
		},
		{
			name: "a full claim without real traces is demoted to advisory",
			in: mutate(func(in *Input) {
				in.Capability.Evidence = EvidenceDocsOnly
				in.Latest = stateReport(agentactivity.StateWorking, ReasonTurnStart)
			}),
			wantState:      agentactivity.StateWorking,
			wantAuthority:  AuthorityLifecycle,
			wantTier:       TierAdvisory,
			wantFreshness:  FreshnessFresh,
			wantTierReason: ReasonCapabilityUnproved,
		},
		{
			name: "a full claim with incomplete coverage is demoted to advisory",
			in: mutate(func(in *Input) {
				in.Capability.Covered = []Transition{TransitionWorkStart, TransitionTurnComplete}
			}),
			wantState:      agentactivity.StateWorking,
			wantAuthority:  AuthorityScreen,
			wantTier:       TierAdvisory,
			wantFreshness:  FreshnessFresh,
			wantReason:     ReasonTierAdvisory,
			wantTierReason: ReasonCapabilityUnproved,
		},
		{
			name: "an unproved provider version starts advisory",
			in: mutate(func(in *Input) {
				in.ProviderInTestedRange = false
			}),
			wantState:      agentactivity.StateWorking,
			wantAuthority:  AuthorityScreen,
			wantTier:       TierAdvisory,
			wantFreshness:  FreshnessFresh,
			wantReason:     ReasonTierAdvisory,
			wantTierReason: ReasonProviderVersionUnproved,
		},
		{
			name: "an outdated asset outside the tested range drops to advisory",
			in: mutate(func(in *Input) {
				in.Status = StatusOutdated
				in.ProviderInTestedRange = false
			}),
			wantState:      agentactivity.StateWorking,
			wantAuthority:  AuthorityScreen,
			wantTier:       TierAdvisory,
			wantFreshness:  FreshnessFresh,
			wantReason:     ReasonTierAdvisory,
			wantTierReason: ReasonIntegrationOutdated,
		},
		{
			name: "an outdated asset still inside the tested range keeps full authority",
			in: mutate(func(in *Input) {
				in.Status = StatusOutdated
			}),
			wantState:     agentactivity.StateBlocked,
			wantAuthority: AuthorityLifecycle,
			wantTier:      TierFull,
			wantFreshness: FreshnessFresh,
		},

		// ---- Tier: session identity ----
		{
			name: "a session-identity source never authors a lane",
			in: mutate(func(in *Input) {
				in.Capability = capabilityAt(TierSessionIdentity)
			}),
			wantState:     agentactivity.StateWorking,
			wantAuthority: AuthorityScreen,
			wantTier:      TierSessionIdentity,
			wantFreshness: FreshnessNone,
			wantReason:    ReasonTierSessionIdentity,
		},

		// ---- Tier: screen fallback ----
		{
			name: "no integration installed is ordinary screen detection",
			in: mutate(func(in *Input) {
				in.Status = StatusNotInstalled
				in.Latest = nil
			}),
			wantState:     agentactivity.StateWorking,
			wantAuthority: AuthorityScreen,
			wantTier:      TierScreenFallback,
			wantFreshness: FreshnessNone,
			wantReason:    ReasonNoIntegration,
		},
		{
			name: "an integration removed mid-run says so",
			in: mutate(func(in *Input) {
				in.Status = StatusNotInstalled
			}),
			wantState:     agentactivity.StateWorking,
			wantAuthority: AuthorityScreen,
			wantTier:      TierScreenFallback,
			wantFreshness: FreshnessNone,
			wantReason:    ReasonIntegrationRemovedMid,
		},
		{
			name: "a missing provider cli has no integration to run",
			in: mutate(func(in *Input) {
				in.Status = StatusProviderMissing
			}),
			wantState:     agentactivity.StateWorking,
			wantAuthority: AuthorityScreen,
			wantTier:      TierScreenFallback,
			wantFreshness: FreshnessNone,
			wantReason:    ReasonProviderMissing,
		},
		{
			name: "an unsupported provider stays on screen detection",
			in: mutate(func(in *Input) {
				in.Status = StatusUnsupported
			}),
			wantState:     agentactivity.StateWorking,
			wantAuthority: AuthorityScreen,
			wantTier:      TierScreenFallback,
			wantFreshness: FreshnessNone,
			wantReason:    ReasonIntegrationUnsupported,
		},
		{
			name: "a damaged integration has no authority until repaired",
			in: mutate(func(in *Input) {
				in.Status = StatusNeedsRepair
			}),
			wantState:     agentactivity.StateWorking,
			wantAuthority: AuthorityScreen,
			wantTier:      TierScreenFallback,
			wantFreshness: FreshnessNone,
			wantReason:    ReasonIntegrationNeedsRepair,
		},
		{
			name:          "an unreadable store disables authority rather than guessing",
			in:            mutate(func(in *Input) { in.StoreUnavailable = true }),
			wantState:     agentactivity.StateWorking,
			wantAuthority: AuthorityScreen,
			wantTier:      TierScreenFallback,
			wantFreshness: FreshnessNone,
			wantReason:    ReasonStoreUnavailable,
		},
		{
			name:          "repeated invalid reports withdraw the source's authority",
			in:            mutate(func(in *Input) { in.InvalidReports = true }),
			wantState:     agentactivity.StateWorking,
			wantAuthority: AuthorityScreen,
			wantTier:      TierScreenFallback,
			wantFreshness: FreshnessNone,
			wantReason:    ReasonInvalidReports,
		},
	}

	seenTiers := map[Tier]bool{}
	seenReasons := map[FallbackReason]bool{}
	seenFreshness := map[Freshness]bool{}
	seenAuthority := map[Authority]bool{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Resolve(tt.in)
			e := got.Explanation

			if got.Result.State != tt.wantState {
				t.Fatalf("state = %q, want %q (evidence %q)", got.Result.State, tt.wantState, got.Result.Evidence)
			}
			if e.State != tt.wantState {
				t.Fatalf("explanation state = %q, want %q", e.State, tt.wantState)
			}
			if e.Authority != tt.wantAuthority {
				t.Fatalf("authority = %q, want %q", e.Authority, tt.wantAuthority)
			}
			if e.Tier != tt.wantTier {
				t.Fatalf("tier = %q, want %q", e.Tier, tt.wantTier)
			}
			if e.Freshness != tt.wantFreshness {
				t.Fatalf("freshness = %q, want %q", e.Freshness, tt.wantFreshness)
			}
			if e.FallbackReason != tt.wantReason {
				t.Fatalf("fallback reason = %q, want %q", e.FallbackReason, tt.wantReason)
			}
			if tt.wantTierReason != "" && e.TierReason != tt.wantTierReason {
				t.Fatalf("tier reason = %q, want %q", e.TierReason, tt.wantTierReason)
			}

			// A screen fallback must hand back the screen's own result
			// untouched. Rewriting it would defeat the whole point of falling
			// back to it.
			if e.Authority == AuthorityScreen && got.Result != tt.in.Screen {
				t.Fatalf("screen fallback altered the result: %+v", got.Result)
			}
			// A lifecycle answer must always explain itself.
			if e.Authority == AuthorityLifecycle && e.FallbackReason != ReasonNone {
				t.Fatalf("lifecycle authority reported a fallback reason %q", e.FallbackReason)
			}
			// The screen's opinion stays visible either way.
			if e.ScreenState != tt.in.Screen.State {
				t.Fatalf("screen state not preserved: %q", e.ScreenState)
			}

			seenTiers[e.Tier] = true
			seenAuthority[e.Authority] = true
			seenFreshness[e.Freshness] = true
			if e.FallbackReason != ReasonNone {
				seenReasons[e.FallbackReason] = true
			}
			if e.TierReason != ReasonNone {
				seenReasons[e.TierReason] = true
			}
		})
	}

	for _, tier := range Tiers() {
		if !seenTiers[tier] {
			t.Errorf("no arbitration row exercises tier %q", tier)
		}
	}
	for _, a := range Authorities() {
		if !seenAuthority[a] {
			t.Errorf("no arbitration row exercises authority %q", a)
		}
	}
	for _, f := range Freshnesses() {
		if !seenFreshness[f] {
			t.Errorf("no arbitration row exercises freshness %q", f)
		}
	}
	for _, r := range FallbackReasons() {
		if !seenReasons[r] {
			t.Errorf("no arbitration row exercises fallback reason %q", r)
		}
	}
}

// TestLifecycleAuthorityProducesPositiveEvidenceFlags pins the mapping from an
// authoritative report to the Visible* flags, because those flags are not
// cosmetic: VisibleIdle bypasses the tracker's idle debounce and VisibleBlocker
// is what agentstatus turns into an attention flag. A regression here would
// make hook-driven state quieter than the heuristics it replaced.
func TestLifecycleAuthorityProducesPositiveEvidenceFlags(t *testing.T) {
	for _, tc := range []struct {
		state        agentactivity.State
		reason       ReasonCode
		wantEvidence string
		wantIdle     bool
		wantWorking  bool
		wantBlocker  bool
	}{
		{agentactivity.StateWorking, ReasonTurnStart, "lifecycle.opencode.turn_start", false, true, false},
		{agentactivity.StateBlocked, ReasonPermissionRequest, "lifecycle.opencode.permission_request", false, false, true},
		{agentactivity.StateIdle, ReasonTurnComplete, "lifecycle.opencode.turn_complete", true, false, false},
	} {
		t.Run(string(tc.state), func(t *testing.T) {
			in := healthyInput()
			in.Latest = stateReport(tc.state, tc.reason)
			in.Screen = screen(agentactivity.StateUnknown, "no-match")
			got := Resolve(in).Result

			if got.Evidence != tc.wantEvidence {
				t.Fatalf("evidence = %q, want %q", got.Evidence, tc.wantEvidence)
			}
			if got.VisibleIdle != tc.wantIdle || got.VisibleWorking != tc.wantWorking || got.VisibleBlocker != tc.wantBlocker {
				t.Fatalf("visibility flags = %+v", got)
			}
			// A reported idle is positive evidence, never an inferred one, so it
			// must be allowed to announce completion.
			if got.FallbackIdle {
				t.Fatal("a reported lane must never be marked FallbackIdle")
			}
			if got.SkipStateUpdate {
				t.Fatal("a reported lane must never skip the state update")
			}
		})
	}
}

// TestResolveIsPureAndDefaultsItsPolicy proves the zero policy is filled in and
// that resolving twice cannot drift, which is what lets every surface call this
// on its own polling cadence without coordination.
func TestResolveIsPureAndDefaultsItsPolicy(t *testing.T) {
	in := healthyInput()
	first := Resolve(in)
	second := Resolve(in)
	if first != second {
		t.Fatalf("Resolve is not deterministic:\n%+v\n%+v", first, second)
	}
	if first.Explanation.FreshnessWindow != DefaultFreshnessPolicy().Blocked.String() {
		t.Fatalf("zero policy did not default: %q", first.Explanation.FreshnessWindow)
	}
}
