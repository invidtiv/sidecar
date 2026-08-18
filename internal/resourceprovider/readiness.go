package resourceprovider

import (
	"errors"

	"github.com/marcus/sidecar/internal/resource"
)

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

// authoritativeDescribeFailure reports whether a failed describe is the
// provider authoritatively saying it has no matchers.
//
// A typed error response is authoritative: the provider ran, understood the
// request, and answered. A transport failure — a crash, a timeout, output the
// host could not parse, a matcher set that failed validation — is not: the host
// simply has no new answer, and the protocol says it must keep the old one
// rather than invent an empty answer on the provider's behalf.
func authoritativeDescribeFailure(err error) bool {
	var terr *TransportError
	if errors.As(err, &terr) {
		return false
	}
	var rerr *resource.Error
	return errors.As(err, &rerr)
}

// stateForDescribeError classifies a failed describe. A provider that cannot
// be started, does not speak the protocol, or sent an unpublishable describe
// result is incompatible — a state the user has to act on. Anything else is
// temporary and worth rechecking.
func stateForDescribeError(err error) State {
	switch OutcomeCode(err) {
	case string(ReasonProtocol), string(ReasonInvalidDescribe), string(ReasonSpawn), string(ReasonShape):
		return StateIncompatible
	case string(resource.CodeInvalidConfig), string(resource.CodeInvalidRequest):
		return StateIncompatible
	default:
		return StateTemporarilyFailed
	}
}
