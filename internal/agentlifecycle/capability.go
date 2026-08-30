package agentlifecycle

// Tier is how much a specific integration source, at a specific version, is
// trusted to author state. It is earned from recorded real traces, never from
// documentation or a happy-path demo.
type Tier string

const (
	// TierFull means the source proved coverage for work, blocking,
	// unblocking, completion, cancellation, session change, and process exit.
	// A fresh same-run report authors the lane; screen evidence stays
	// diagnostic and cannot reverse it.
	TierFull Tier = "full"
	// TierAdvisory means the source has useful explicit events but known
	// lifecycle gaps. A fresh event may strengthen a matching transition, but a
	// missing event must never suppress screen or process detection — otherwise
	// the gaps become silent freezes.
	TierAdvisory Tier = "advisory"
	// TierSessionIdentity means the source reliably identifies the provider
	// session but not its state. It enriches diagnostics and future resume
	// behavior only; screen and process detection stay the sole authority.
	TierSessionIdentity Tier = "session-identity"
	// TierScreenFallback means no valid integration evidence applies and
	// existing detector and tracker behavior is unchanged.
	TierScreenFallback Tier = "screen-fallback"
)

// Tiers is the frozen, ordered authority-tier vocabulary, strongest first.
func Tiers() []Tier {
	return []Tier{TierFull, TierAdvisory, TierSessionIdentity, TierScreenFallback}
}

// Transition is one observable lifecycle moment a capability claim is measured
// against. The capability matrix records, per provider, which of these real
// traces actually covered — so "full lifecycle" is a checklist result rather
// than an adjective.
type Transition string

const (
	TransitionWorkStart        Transition = "work_start"
	TransitionToolUse          Transition = "tool_use"
	TransitionBlockedOnRequest Transition = "blocked_on_request"
	TransitionUnblocked        Transition = "unblocked"
	TransitionTurnComplete     Transition = "turn_complete"
	TransitionCancelled        Transition = "cancelled"
	TransitionSessionIdentity  Transition = "session_identity"
	TransitionSubagent         Transition = "subagent"
	TransitionProcessExit      Transition = "process_exit"
)

// Transitions is the frozen, ordered transition vocabulary.
func Transitions() []Transition {
	return []Transition{
		TransitionWorkStart,
		TransitionToolUse,
		TransitionBlockedOnRequest,
		TransitionUnblocked,
		TransitionTurnComplete,
		TransitionCancelled,
		TransitionSessionIdentity,
		TransitionSubagent,
		TransitionProcessExit,
	}
}

// FullLifecycleTransitions is the subset a source must cover with real traces
// before it may hold [TierFull].
//
// TransitionToolUse and TransitionSubagent are deliberately excluded. Tool use
// is a refinement of work_start, not a separate lane, and subagent aggregation
// has no proved cross-provider rule yet — the M6 plan explicitly refuses to let
// child activity block or complete the parent without one. Requiring either
// would deny full authority to an otherwise complete integration for reasons
// unrelated to the lanes Sidecar actually renders.
func FullLifecycleTransitions() []Transition {
	return []Transition{
		TransitionWorkStart,
		TransitionBlockedOnRequest,
		TransitionUnblocked,
		TransitionTurnComplete,
		TransitionCancelled,
		TransitionSessionIdentity,
		TransitionProcessExit,
	}
}

// EvidenceQuality records how a capability claim was established. It exists so
// the matrix cannot quietly present a reading of the documentation as a
// measurement.
type EvidenceQuality string

const (
	// EvidenceRealTrace means sanitized traces were captured from the real
	// provider CLI at a recorded version.
	EvidenceRealTrace EvidenceQuality = "real-trace"
	// EvidenceDocsOnly means the contract comes from official documentation
	// with no local trace. A docs-only source may not hold TierFull.
	EvidenceDocsOnly EvidenceQuality = "docs-only"
	// EvidenceNone means no released integration API was found at all.
	EvidenceNone EvidenceQuality = "none"
)

// EvidenceQualities is the frozen, ordered evidence vocabulary.
func EvidenceQualities() []EvidenceQuality {
	return []EvidenceQuality{EvidenceRealTrace, EvidenceDocsOnly, EvidenceNone}
}

// Capability is the bundled registry entry describing what one integration
// source is trusted to do, and on what evidence.
type Capability struct {
	SchemaVersion int `json:"schemaVersion"`

	// Provider is the catalog agent kind this source integrates with.
	Provider string `json:"provider"`
	// Source is the integration identifier that appears in Report.Source.
	Source string `json:"source"`
	// AssetVersion is the bundled integration asset's version.
	AssetVersion string `json:"assetVersion"`

	// Tier is the authority this source has earned.
	Tier Tier `json:"tier"`
	// Evidence records how that tier was established.
	Evidence EvidenceQuality `json:"evidence"`

	// MinProviderVersion is the oldest provider version the source supports.
	MinProviderVersion string `json:"minProviderVersion,omitempty"`
	// TestedProviderRange is the provider version range the tier was actually
	// proved against. A provider outside it is not refused, but it does not
	// inherit TierFull either — see [Capability.TierFor].
	TestedProviderRange string `json:"testedProviderRange,omitempty"`

	// Covered lists the transitions real evidence covered.
	Covered []Transition `json:"covered,omitempty"`
	// KnownGaps names, in prose, what the source cannot observe. It is
	// diagnostic text shown to the user, not a machine contract.
	KnownGaps []string `json:"knownGaps,omitempty"`

	// OrderingGuaranteed records whether the provider guarantees event
	// ordering. When false, the resolver relies entirely on Report.Sequence,
	// which the hook assigns locally.
	OrderingGuaranteed bool `json:"orderingGuaranteed"`

	// SourceDoc is the upstream documentation URL the contract was read from.
	SourceDoc string `json:"sourceDoc,omitempty"`
	// SourceVersionNote records the provider version the evidence was gathered
	// at, in human form.
	SourceVersionNote string `json:"sourceVersionNote,omitempty"`
}

// CoversFullLifecycle reports whether Covered includes every transition in
// [FullLifecycleTransitions].
func (c Capability) CoversFullLifecycle() bool {
	have := make(map[Transition]bool, len(c.Covered))
	for _, t := range c.Covered {
		have[t] = true
	}
	for _, need := range FullLifecycleTransitions() {
		if !have[need] {
			return false
		}
	}
	return true
}

// TierFor returns the tier this capability may exercise given the integration
// status observed on the machine right now, plus the reason when that is lower
// than the capability's nominal tier.
//
// The demotion rules are the whole point of versioning authority:
//
//   - A source whose claimed tier is not backed by real traces, or whose
//     coverage is incomplete, cannot exercise TierFull however it is declared.
//     This is checked here rather than trusted at registry-authoring time so a
//     mistaken registry entry degrades safely instead of silently taking over.
//   - An asset needing repair, missing, or unsupported has no authority.
//   - An outdated asset keeps its last proved tier only while the provider is
//     still inside the tested range; otherwise it drops to advisory.
//   - A provider version outside the tested range starts advisory regardless.
func (c Capability) TierFor(status IntegrationStatus, providerInRange bool) (Tier, FallbackReason) {
	switch status {
	case StatusProviderMissing:
		return TierScreenFallback, ReasonProviderMissing
	case StatusNotInstalled:
		return TierScreenFallback, ReasonNoIntegration
	case StatusUnsupported:
		return TierScreenFallback, ReasonIntegrationUnsupported
	case StatusNeedsRepair:
		return TierScreenFallback, ReasonIntegrationNeedsRepair
	}

	tier := c.Tier
	reason := ReasonNone

	// An unearned full claim is demoted here, not honored and audited later.
	if tier == TierFull && (c.Evidence != EvidenceRealTrace || !c.CoversFullLifecycle()) {
		return TierAdvisory, ReasonCapabilityUnproved
	}
	// An outdated asset keeps its proved tier while the provider is still
	// inside the tested range; outside it, the asset's age is the more
	// actionable of the two true statements, so it is reported first.
	if tier == TierFull && !providerInRange {
		if status == StatusOutdated {
			return TierAdvisory, ReasonIntegrationOutdated
		}
		return TierAdvisory, ReasonProviderVersionUnproved
	}
	return tier, reason
}

// IntegrationStatus is the installed state of one provider integration on this
// machine.
type IntegrationStatus string

const (
	// StatusProviderMissing means the provider CLI itself is not installed.
	StatusProviderMissing IntegrationStatus = "provider-missing"
	// StatusNotInstalled means the provider is present but Sidecar's
	// integration has not been installed.
	StatusNotInstalled IntegrationStatus = "not-installed"
	// StatusCurrent means the installed asset matches the bundled version.
	StatusCurrent IntegrationStatus = "current"
	// StatusOutdated means an older Sidecar asset is installed.
	StatusOutdated IntegrationStatus = "outdated"
	// StatusNeedsRepair means the asset or its configuration entry is present
	// but damaged, modified, or inconsistent.
	StatusNeedsRepair IntegrationStatus = "needs-repair"
	// StatusUnsupported means Sidecar ships no integration for this provider.
	StatusUnsupported IntegrationStatus = "unsupported"
)

// IntegrationStatuses is the frozen, ordered integration-status vocabulary.
func IntegrationStatuses() []IntegrationStatus {
	return []IntegrationStatus{
		StatusProviderMissing,
		StatusNotInstalled,
		StatusCurrent,
		StatusOutdated,
		StatusNeedsRepair,
		StatusUnsupported,
	}
}

// IntegrationReport is the JSON contract behind `sidecar agent integration
// list` and `status`, and behind the Configuration → Agents → Integrations
// route. Both surfaces render this one record: that is what keeps them from
// drifting into two answers.
type IntegrationReport struct {
	SchemaVersion int               `json:"schemaVersion"`
	Provider      string            `json:"provider"`
	Source        string            `json:"source"`
	Status        IntegrationStatus `json:"status"`

	// BundledVersion is the asset version this Sidecar build ships.
	BundledVersion string `json:"bundledVersion,omitempty"`
	// InstalledVersion is the asset version currently on disk.
	InstalledVersion string `json:"installedVersion,omitempty"`
	// ProviderVersion is the detected provider CLI version.
	ProviderVersion string `json:"providerVersion,omitempty"`
	// ProviderInTestedRange reports whether ProviderVersion falls inside the
	// capability's proved range.
	ProviderInTestedRange bool `json:"providerInTestedRange"`

	// EffectiveTier is what this integration may actually exercise now.
	EffectiveTier Tier `json:"effectiveTier"`
	// TierReason explains any demotion from the capability's nominal tier.
	TierReason FallbackReason `json:"tierReason,omitempty"`

	// TargetPaths are the exact user-level files an install or uninstall would
	// touch. They are shown before any mutation is offered.
	TargetPaths []string `json:"targetPaths,omitempty"`
	// KnownGaps is carried through from the capability for display.
	KnownGaps []string `json:"knownGaps,omitempty"`
	// Message is a sanitized, actionable diagnostic.
	Message string `json:"message,omitempty"`
}
