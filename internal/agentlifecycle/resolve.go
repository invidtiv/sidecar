package agentlifecycle

import (
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
)

// Authority names which kind of evidence authored the effective state.
type Authority string

const (
	// AuthorityLifecycle means a provider's own lifecycle report authored the
	// lane.
	AuthorityLifecycle Authority = "lifecycle"
	// AuthorityScreen means the existing screen and process detectors authored
	// it, which is both the default and the safe destination of every failure.
	AuthorityScreen Authority = "screen"
)

// Authorities is the frozen, ordered authority vocabulary.
func Authorities() []Authority { return []Authority{AuthorityLifecycle, AuthorityScreen} }

// For returns the freshness window that applies to a lane. An unrecognised
// lane gets the Working window, which is the shortest — an unknown lane should
// expire soonest, not linger longest.
func (p FreshnessPolicy) For(state agentactivity.State) time.Duration {
	switch state {
	case agentactivity.StateBlocked:
		return p.Blocked
	case agentactivity.StateIdle:
		return p.Idle
	default:
		return p.Working
	}
}

// Input is everything the arbitration needs. It is a plain value: the resolver
// performs no I/O, reads no clock, and consults no global, so a table test can
// state an entire scenario as one literal.
type Input struct {
	// Now is the caller's clock reading.
	Now time.Time

	// Live is the identity of the pane and run being resolved right now,
	// derived from tmux and process inspection by the caller.
	Live Identity
	// ProcessAlive reports whether the matching provider process generation is
	// still running. It is what stops a crashed run from holding a lane until
	// its freshness window expires.
	ProcessAlive bool

	// Capability is the registry entry for the installed source. The zero
	// value means no integration.
	Capability Capability
	// Status is the installed state of that integration.
	Status IntegrationStatus
	// ProviderInTestedRange reports whether the detected provider version falls
	// inside the range the capability's tier was proved against.
	ProviderInTestedRange bool

	// Latest is the most recent valid report for the live key, of any kind, or
	// nil when there is none.
	Latest *Report
	// StoreUnavailable reports that the report store could not be read.
	StoreUnavailable bool
	// InvalidReports reports that the source produced repeated invalid records,
	// which withdraws its authority until it behaves again.
	InvalidReports bool

	// Screen is the result the existing detectors produced for this pane. It is
	// always computed: arbitration chooses between evidence, it never decides
	// whether to gather it.
	Screen agentactivity.Result

	// Policy bounds report freshness. The zero value means
	// DefaultFreshnessPolicy.
	Policy FreshnessPolicy
}

// Explanation is the diagnostic behind one resolution. It is the JSON contract
// of `sidecar agent explain` and the fact set the Configuration surface
// renders, so the two cannot disagree about why a pane is in the state it is.
type Explanation struct {
	SchemaVersion int `json:"schemaVersion"`

	// State is the effective lane after arbitration.
	State agentactivity.State `json:"state"`
	// Authority is which evidence authored it.
	Authority Authority `json:"authority"`
	// Tier is the authority the source could actually exercise.
	Tier Tier `json:"tier"`
	// Freshness describes the applicable report, if any.
	Freshness Freshness `json:"freshness"`
	// FallbackReason is empty when lifecycle evidence authored the state, and
	// otherwise names exactly why it did not.
	FallbackReason FallbackReason `json:"fallbackReason,omitempty"`
	// TierReason records why Tier is lower than the capability nominally
	// claims. It is reported independently of FallbackReason because a demoted
	// source that then agrees with the screen produces no fallback at all, and
	// "this integration is running advisory because its version is unproved"
	// would otherwise be invisible in exactly the case where someone is
	// wondering why their full-lifecycle integration is not driving state.
	TierReason FallbackReason `json:"tierReason,omitempty"`

	Provider          string            `json:"provider,omitempty"`
	Source            string            `json:"source,omitempty"`
	SourceVersion     string            `json:"sourceVersion,omitempty"`
	IntegrationStatus IntegrationStatus `json:"integrationStatus,omitempty"`

	// ScreenState and ScreenEvidence are always reported, including when
	// lifecycle evidence won. Keeping the screen's opinion visible is what
	// makes a disagreement diagnosable instead of invisible.
	ScreenState    agentactivity.State `json:"screenState"`
	ScreenEvidence string              `json:"screenEvidence,omitempty"`

	// ReportState, ReportKind, ReportReason, ReportSequence and ReportAt
	// describe the applicable report when there is one.
	ReportState    agentactivity.State `json:"reportState,omitempty"`
	ReportKind     Kind                `json:"reportKind,omitempty"`
	ReportReason   ReasonCode          `json:"reportReason,omitempty"`
	ReportOutcome  Outcome             `json:"reportOutcome,omitempty"`
	ReportSequence uint64              `json:"reportSequence,omitempty"`
	ReportAt       time.Time           `json:"reportAt,omitempty"`

	// ReportAge and FreshnessWindow are rendered durations ("2m30s") rather
	// than nanosecond integers, because the plan requires the exact timeout to
	// be *visible* in explain output and a human reading JSON should not have
	// to divide by a billion.
	ReportAge       string `json:"reportAge,omitempty"`
	FreshnessWindow string `json:"freshnessWindow,omitempty"`

	// Identity is the live pane and run this explanation is about.
	Identity Identity `json:"identity"`
	// ProcessAlive mirrors the liveness input, because "the hook looked fine
	// but the process is gone" is a common and confusing case.
	ProcessAlive bool `json:"processAlive"`
}

// Decision is the resolver's output: the result every surface then feeds to its
// own ordinary [agentactivity.Tracker], plus the explanation.
type Decision struct {
	Result      agentactivity.Result
	Explanation Explanation
}

// LifecycleEvidence builds the evidence string for a lifecycle-authored
// result. It follows the existing "<provider>.<what matched>" convention the
// screen detectors use, prefixed so the two can never be confused when they
// appear side by side in a log or a fixture.
func LifecycleEvidence(provider string, reason ReasonCode) string {
	if provider == "" {
		provider = "unknown"
	}
	if reason == "" {
		reason = ReasonUnspecified
	}
	return "lifecycle." + provider + "." + string(reason)
}

// Resolve arbitrates between lifecycle reports and screen observation.
//
// It is the single authority decision in Sidecar: project worktree polling,
// project managed-shell polling, and workspaceinventory all call it after
// building their ordinary observation, so no surface can hold a private
// opinion about which evidence wins.
//
// The order of the checks below is the contract, not an implementation detail.
// Cheap disqualifications come first so that a pane with no integration costs
// almost nothing, and identity checks come before freshness so that a report
// from a replaced process reports the mismatch rather than the more generic
// staleness that would also be true.
func Resolve(in Input) Decision {
	policy := in.Policy
	if policy == (FreshnessPolicy{}) {
		policy = DefaultFreshnessPolicy()
	}

	exp := Explanation{
		SchemaVersion:     SchemaVersion,
		Authority:         AuthorityScreen,
		Tier:              TierScreenFallback,
		Freshness:         FreshnessNone,
		Provider:          in.Live.Provider,
		IntegrationStatus: in.Status,
		ScreenState:       in.Screen.State,
		ScreenEvidence:    in.Screen.Evidence,
		Identity:          in.Live,
		ProcessAlive:      in.ProcessAlive,
	}

	// fallback finishes the explanation with the screen's result untouched.
	// Every early return goes through here, which is what guarantees a reason
	// is always populated when lifecycle evidence did not win.
	fallback := func(reason FallbackReason) Decision {
		exp.State = in.Screen.State
		exp.FallbackReason = reason
		return Decision{Result: in.Screen, Explanation: exp}
	}

	if in.StoreUnavailable {
		return fallback(ReasonStoreUnavailable)
	}
	if in.InvalidReports {
		return fallback(ReasonInvalidReports)
	}

	tier, tierReason := in.Capability.TierFor(in.Status, in.ProviderInTestedRange)
	exp.Tier = tier
	exp.TierReason = tierReason
	exp.Source = in.Capability.Source
	exp.SourceVersion = in.Capability.AssetVersion
	if tier == TierScreenFallback {
		// An integration that vanished while a run was still reporting is a
		// different problem from one that was never installed, and it is the
		// one worth naming: it tells the user their agent's state just stopped
		// being deterministic, rather than that it never was.
		if tierReason == ReasonNoIntegration && in.Latest != nil {
			return fallback(ReasonIntegrationRemovedMid)
		}
		return fallback(tierReason)
	}
	if tier == TierSessionIdentity {
		return fallback(ReasonTierSessionIdentity)
	}

	if in.Latest == nil {
		return fallback(ReasonNoReport)
	}
	r := *in.Latest

	exp.ReportState = r.State
	exp.ReportKind = r.Kind
	exp.ReportReason = r.Reason
	exp.ReportOutcome = r.Outcome
	exp.ReportSequence = r.Sequence
	exp.ReportAt = r.ObservedAt
	exp.SourceVersion = r.SourceVersion

	if r.Source != in.Capability.Source {
		return fallback(ReasonSourceMismatch)
	}
	if reason, ok := identityMismatch(in.Live, r.Identity); !ok {
		return fallback(reason)
	}
	if !in.ProcessAlive {
		return fallback(ReasonProcessExited)
	}

	switch r.Kind {
	case KindRelease:
		exp.Freshness = FreshnessReleased
		return fallback(ReasonAuthorityRelease)
	case KindEnd:
		exp.Freshness = FreshnessReleased
		return fallback(ReasonRunEnded)
	case KindSession:
		// The source is alive and matching, but the last thing it said carried
		// no lane. There is nothing to author from.
		return fallback(ReasonNoReport)
	}

	window := policy.For(r.State)
	age := in.Now.Sub(r.ObservedAt)
	exp.FreshnessWindow = window.String()
	if age > 0 {
		exp.ReportAge = age.String()
	} else {
		exp.ReportAge = time.Duration(0).String()
	}
	if age > window {
		exp.Freshness = FreshnessStale
		return fallback(ReasonReportStale)
	}
	exp.Freshness = FreshnessFresh

	// Advisory sources may confirm what the screen already sees, and may speak
	// when the screen has no opinion, but may never contradict it. A gap in an
	// advisory integration has to degrade into ordinary detection rather than
	// into a wrong answer.
	if tier == TierAdvisory &&
		in.Screen.State != r.State &&
		in.Screen.State != agentactivity.StateUnknown {
		return fallback(ReasonTierAdvisory)
	}

	exp.Authority = AuthorityLifecycle
	exp.State = r.State
	return Decision{Result: lifecycleResult(in.Live.Provider, r), Explanation: exp}
}

// lifecycleResult converts an authoritative report into the result the trackers
// consume.
//
// The Visible* flags are all set for the matching lane, and that is deliberate
// rather than incidental. In screen detection those flags mean "the evidence
// for this lane was positively seen rather than inferred", and they control
// real behavior: VisibleIdle bypasses the tracker's 400ms idle debounce, and
// VisibleBlocker is what agentstatus turns into an attention flag. A provider
// telling Sidecar its own turn ended or its own permission prompt opened is the
// strongest positive evidence available, so treating it as anything less would
// make hook-driven state slower and quieter than the heuristics it replaces.
//
// FallbackIdle stays false for the same reason: it marks an idle that was
// assumed from the absence of a match and therefore must not announce
// completion. A reported idle is the opposite of an assumption.
func lifecycleResult(provider string, r Report) agentactivity.Result {
	return agentactivity.Result{
		State:          r.State,
		Evidence:       LifecycleEvidence(provider, r.Reason),
		VisibleIdle:    r.State == agentactivity.StateIdle,
		VisibleWorking: r.State == agentactivity.StateWorking,
		VisibleBlocker: r.State == agentactivity.StateBlocked,
	}
}

// identityMismatch compares the live identity with a report's, returning the
// first field that differs.
//
// The order runs outermost to innermost — host, server, pane, provider, run,
// process generation, session — so the reported reason is the broadest true
// statement about the discontinuity. A tmux server restart reports
// server_incarnation_changed rather than the run_mismatch that is also
// technically true but far less useful to a person reading it.
//
// An empty session fingerprint on either side is not a mismatch: session
// identity is optional, and a provider that has not yet reported one must not
// be treated as having rotated it.
func identityMismatch(live, reported Identity) (FallbackReason, bool) {
	switch {
	case live.Host != reported.Host:
		return ReasonHostMismatch, false
	case live.ServerIncarnation != reported.ServerIncarnation:
		return ReasonServerIncarnationNew, false
	case live.PaneID != reported.PaneID:
		return ReasonPaneMismatch, false
	case live.Provider != reported.Provider:
		return ReasonProviderMismatch, false
	case live.RunID != reported.RunID:
		return ReasonRunMismatch, false
	case live.ProcessGeneration != reported.ProcessGeneration:
		return ReasonProcessGenChanged, false
	case live.SessionFingerprint != "" && reported.SessionFingerprint != "" &&
		live.SessionFingerprint != reported.SessionFingerprint:
		return ReasonSessionMismatch, false
	}
	return ReasonNone, true
}
