package overview

import "github.com/marcus/sidecar/internal/hosts"

// Reading the running registry's host health from outside the browser.
//
// The Remote Hosts page in Configuration shows each registered machine's live
// condition, and it must read it from here rather than probing: a settings
// screen that opened its own ssh connection would be a second answer to a
// question the registry is already answering, and the two would disagree the
// moment one of them was slower than the other.

// HostCondition is one registered machine's health, paired with the name the
// browser knows it by. It is a projection for callers outside this package, so
// nothing here exposes a client or a way to start one.
type HostCondition struct {
	ID     string
	Health hosts.Health
}

// HostConditions returns every registered host's current health, in the same
// order the browser groups remote rows in. It is empty when the feature is off
// or nothing is registered, because in that case no registry exists at all.
func (m *Model) HostConditions() []HostCondition {
	if len(m.hostHealth) == 0 {
		return nil
	}
	order := m.hostOrder()
	out := make([]HostCondition, 0, len(order))
	for _, id := range order {
		out = append(out, HostCondition{ID: id, Health: m.hostHealth[id]})
	}
	return out
}
