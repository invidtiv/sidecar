package resourceprovider

// State is a provider instance's readiness. It is explicit rather than
// inferred, because "describe has not returned yet" and "this matcher was
// deleted" must never be the same value: restore has to be able to keep an
// armed resource tab through the first, and only the user's own close removes
// one.
type State string

const (
	// StateUnchecked is the starting state: configured, never described.
	StateUnchecked State = "unchecked"
	// StateReady means the last describe succeeded and its matchers are live.
	StateReady State = "ready"
	// StateTemporarilyFailed means describe failed in a way a retry might fix.
	StateTemporarilyFailed State = "temporarily-failed"
	// StateIncompatible means the provider answered but does not speak this
	// protocol version, or its describe result cannot be published.
	StateIncompatible State = "incompatible"
	// StateDisabled means configuration turned this instance off.
	StateDisabled State = "disabled"
	// StateRemoved means the instance is no longer in configuration. Armed
	// references to it are preserved, not pruned.
	StateRemoved State = "removed"
)

// Live reports whether an instance's matchers should be in the snapshot. Only
// a ready provider contributes; every other state contributes nothing while
// preserving whatever is already saved.
func (s State) Live() bool { return s == StateReady }

// stateForDescribeError classifies a failed describe. A provider that cannot
// be started, does not speak the protocol, or sent an unpublishable describe
// result is incompatible — a state the user has to act on. Anything else is
// temporary and worth rechecking.
func stateForDescribeError(err error) State {
	switch OutcomeCode(err) {
	case string(ReasonProtocol), string(ReasonInvalidDescribe), string(ReasonSpawn):
		return StateIncompatible
	case "invalid_config":
		return StateIncompatible
	default:
		return StateTemporarilyFailed
	}
}
