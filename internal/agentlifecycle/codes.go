package agentlifecycle

import "time"

// FallbackReason explains why lifecycle evidence did not author the pane's
// state, or why a source is exercising less authority than it nominally holds.
//
// Every one of these is user-visible in `sidecar agent explain`. That is the
// contract the M6 plan cares about most: when hooks are not driving state, the
// answer must be an actionable reason, never silence and never a frozen lane.
type FallbackReason string

const (
	// ReasonNone means lifecycle evidence authored the state; there was no
	// fallback to explain.
	ReasonNone FallbackReason = ""

	// -- Integration availability --

	ReasonNoIntegration          FallbackReason = "no_integration"
	ReasonProviderMissing        FallbackReason = "provider_missing"
	ReasonIntegrationUnsupported FallbackReason = "integration_unsupported"
	ReasonIntegrationNeedsRepair FallbackReason = "integration_needs_repair"
	ReasonIntegrationOutdated    FallbackReason = "integration_outdated"
	ReasonIntegrationRemovedMid  FallbackReason = "integration_removed"

	// -- Earned authority is lower than the lane needs --

	ReasonCapabilityUnproved      FallbackReason = "capability_unproved"
	ReasonProviderVersionUnproved FallbackReason = "provider_version_unproved"
	ReasonTierAdvisory            FallbackReason = "tier_advisory"
	ReasonTierSessionIdentity     FallbackReason = "tier_session_identity"

	// -- Evidence exists but does not apply --

	ReasonNoReport         FallbackReason = "no_report"
	ReasonReportStale      FallbackReason = "report_stale"
	ReasonAuthorityRelease FallbackReason = "authority_released"
	ReasonRunEnded         FallbackReason = "run_ended"

	// -- Identity discontinuity --

	ReasonRunMismatch          FallbackReason = "run_mismatch"
	ReasonProcessGenChanged    FallbackReason = "process_generation_mismatch"
	ReasonSessionMismatch      FallbackReason = "session_mismatch"
	ReasonPaneMismatch         FallbackReason = "pane_mismatch"
	ReasonServerIncarnationNew FallbackReason = "server_incarnation_changed"
	ReasonHostMismatch         FallbackReason = "host_mismatch"
	ReasonProviderMismatch     FallbackReason = "provider_mismatch"
	ReasonSourceMismatch       FallbackReason = "source_mismatch"
	ReasonProcessExited        FallbackReason = "process_exited"

	// -- Store health --

	ReasonStoreUnavailable FallbackReason = "store_unavailable"
	ReasonInvalidReports   FallbackReason = "invalid_reports"
)

// FallbackReasons is the frozen, ordered fallback-reason vocabulary.
//
// [ReasonNone] is excluded: it is the empty string, meaning "no fallback
// happened", and listing it would imply it is a reason a caller could render.
func FallbackReasons() []FallbackReason {
	return []FallbackReason{
		ReasonNoIntegration,
		ReasonProviderMissing,
		ReasonIntegrationUnsupported,
		ReasonIntegrationNeedsRepair,
		ReasonIntegrationOutdated,
		ReasonIntegrationRemovedMid,

		ReasonCapabilityUnproved,
		ReasonProviderVersionUnproved,
		ReasonTierAdvisory,
		ReasonTierSessionIdentity,

		ReasonNoReport,
		ReasonReportStale,
		ReasonAuthorityRelease,
		ReasonRunEnded,

		ReasonRunMismatch,
		ReasonProcessGenChanged,
		ReasonSessionMismatch,
		ReasonPaneMismatch,
		ReasonServerIncarnationNew,
		ReasonHostMismatch,
		ReasonProviderMismatch,
		ReasonSourceMismatch,
		ReasonProcessExited,

		ReasonStoreUnavailable,
		ReasonInvalidReports,
	}
}

// ErrorCode is the machine-readable outcome of a `sidecar agent report`,
// `end`, or `release` invocation.
//
// These commands are hook surfaces. They fail open from the agent's point of
// view: a non-zero code is diagnostic, and the provider's own operation must
// continue unchanged regardless. The codes exist so a human or agent debugging
// a silent integration can tell "your sequence went backwards" from "you are
// not inside a Sidecar shell" without parsing prose.
type ErrorCode string

const (
	// ErrInvalidContext means the command is not running inside a Sidecar-
	// managed shell, or the claimed context does not match the live one. This
	// is also the ordinary quiet no-op case: a hook that fires outside Sidecar
	// exits successfully and silently rather than reporting.
	ErrInvalidContext ErrorCode = "lifecycle_invalid_context"
	// ErrInvalidReport means the record failed enum, bounds, or skew validation.
	ErrInvalidReport ErrorCode = "lifecycle_invalid_report"
	// ErrStaleSequence means the sequence did not advance within its key. The
	// report is a duplicate or arrived out of order; either way it is dropped
	// idempotently.
	ErrStaleSequence ErrorCode = "lifecycle_stale_sequence"
	// ErrRunMismatch means the report belongs to a run, process generation, or
	// session that is no longer the live one.
	ErrRunMismatch ErrorCode = "lifecycle_run_mismatch"
	// ErrUnsupportedSource means no capability is registered for this source.
	ErrUnsupportedSource ErrorCode = "lifecycle_unsupported_source"
	// ErrStoreFailed means the record could not be persisted.
	ErrStoreFailed ErrorCode = "lifecycle_store_failed"
)

// ErrorCodes is the frozen, ordered error vocabulary.
func ErrorCodes() []ErrorCode {
	return []ErrorCode{
		ErrInvalidContext,
		ErrInvalidReport,
		ErrStaleSequence,
		ErrRunMismatch,
		ErrUnsupportedSource,
		ErrStoreFailed,
	}
}

// Acceptance is what the report command tells its caller about a record it did
// take. It is not an error: a source can be accepted and still be advisory, and
// the distinction has to survive to the CLI or an integration author cannot
// tell "stored and authoritative" from "stored and ignored".
type Acceptance string

const (
	// AcceptedAuthoritative means the record was stored by a source that may
	// author state.
	AcceptedAuthoritative Acceptance = "accepted_authoritative"
	// AcceptedAdvisory means the record was stored but its source cannot
	// author state at its current tier.
	AcceptedAdvisory Acceptance = "accepted_advisory"
	// AcceptedDuplicate means an identical record was already present; the
	// append was a no-op.
	AcceptedDuplicate Acceptance = "accepted_duplicate"
)

// Acceptances is the frozen, ordered acceptance vocabulary.
func Acceptances() []Acceptance {
	return []Acceptance{AcceptedAuthoritative, AcceptedAdvisory, AcceptedDuplicate}
}

// Freshness is how a report's age compares to the policy for its lane.
type Freshness string

const (
	// FreshnessFresh means the report is inside its lane's window.
	FreshnessFresh Freshness = "fresh"
	// FreshnessStale means the report aged out and no longer authors state.
	FreshnessStale Freshness = "stale"
	// FreshnessReleased means authority was explicitly surrendered or the run
	// ended; age is irrelevant.
	FreshnessReleased Freshness = "released"
	// FreshnessNone means there is no applicable report at all.
	FreshnessNone Freshness = "none"
)

// Freshnesses is the frozen, ordered freshness vocabulary.
func Freshnesses() []Freshness {
	return []Freshness{FreshnessFresh, FreshnessStale, FreshnessReleased, FreshnessNone}
}

// FreshnessPolicy bounds how long a report may keep authoring a lane without
// any further event from its source.
//
// These are backstops, not the primary release mechanism. Authority normally
// ends on an identity discontinuity — process exit, pane replacement, server
// restart, session rotation, or an explicit release — all of which the resolver
// detects immediately and none of which waits for a timeout. The windows exist
// only for the case where a hook stops firing while everything else still looks
// live, and their job is to fail toward current screen evidence rather than
// hold a lane forever.
//
// The asymmetry is deliberate and follows what each lane means:
//
//   - Working is the risky lane, because a stale "working" hides a finished or
//     wedged agent. A single tool call rarely runs 30 minutes with no event
//     from any of the traced providers, so that is the window.
//   - Blocked and Idle are both states where the pane is legitimately quiet for
//     as long as the human is away, and where screen detection independently
//     agrees anyway (a permission prompt and an empty prompt box are the two
//     things the detectors read most reliably). Expiring them early would churn
//     authority for no gain, so they get a long window.
//
// DefaultFreshnessPolicy's values are provisional Phase A figures pinned by
// fixture. Phase D retunes them against measured per-provider event cadence,
// and the fixture makes that a deliberate, reviewed change.
type FreshnessPolicy struct {
	Working time.Duration `json:"working"`
	Blocked time.Duration `json:"blocked"`
	Idle    time.Duration `json:"idle"`
}

// DefaultFreshnessPolicy returns the shipped windows.
func DefaultFreshnessPolicy() FreshnessPolicy {
	return FreshnessPolicy{
		Working: 30 * time.Minute,
		Blocked: 8 * time.Hour,
		Idle:    8 * time.Hour,
	}
}
