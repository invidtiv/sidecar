package agentlifecycle

import (
	_ "embed"
	"encoding/json"
	"sync"
)

// capabilitiesJSON is the bundled capability registry.
//
// It lives beside the code rather than in testdata because it is shipped data,
// not a fixture: the resolver reads it at runtime to decide what an installed
// integration is allowed to author. Keeping one file means the evidence the
// tests police and the registry the binary trusts cannot drift — there is
// nothing to drift from.
//
// The prose companion, with the per-provider gap analysis and the traces behind
// each tier, is docs/reference/agent-lifecycle-capability-matrix.md.
//
//go:embed capabilities.json
var capabilitiesJSON []byte

var (
	capabilitiesOnce sync.Once
	capabilities     []Capability
)

// Capabilities returns the bundled registry.
//
// A malformed registry yields an empty list rather than a panic. That is the
// safe direction: with no capability entries every source resolves to
// [TierScreenFallback] and Sidecar behaves exactly as it did before hooks
// existed, which is a working product. Panicking on a bad embed would take the
// whole application down over a feature the user may not even use.
func Capabilities() []Capability {
	capabilitiesOnce.Do(func() {
		var parsed []Capability
		if err := json.Unmarshal(capabilitiesJSON, &parsed); err != nil {
			return
		}
		capabilities = parsed
	})
	out := make([]Capability, len(capabilities))
	copy(out, capabilities)
	return out
}

// CapabilityForSource returns the registry entry for one integration source.
//
// Lookup is by source rather than by provider because authority is granted to a
// source at a version, never to a provider name: one provider may over time
// have more than one integration shape, and the older one must not inherit what
// the newer one proved.
func CapabilityForSource(source string) (Capability, bool) {
	if source == "" {
		return Capability{}, false
	}
	for _, c := range Capabilities() {
		if c.Source == source {
			return c, true
		}
	}
	return Capability{}, false
}
