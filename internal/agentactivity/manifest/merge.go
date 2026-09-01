package manifest

import (
	"fmt"
	"strings"
)

// OverlayIDPrefix is the namespace every Sidecar-authored overlay rule id must
// carry. It is not decoration: without it an overlay rule added today can
// collide with an upstream rule id added next month, and the collision would
// silently *replace* upstream's rule rather than adding ours. Replacing or
// disabling an upstream rule is a deliberate act and uses that rule's own id,
// which is the one case the prefix is not required.
const OverlayIDPrefix = "sidecar."

// ParseOverlay decodes a Sidecar overlay file. An overlay has a manifest's
// shape plus one key a manifest may not carry: `disable = true` on a rule,
// which removes the upstream rule with that id.
func ParseOverlay(data []byte) (*Manifest, error) {
	m, err := parse(data, true)
	if err != nil {
		return nil, err
	}
	if err := ValidateOverlay(m); err != nil {
		return nil, err
	}
	return m, nil
}

// ValidateOverlay applies the manifest validator to an overlay, exempting
// disabled rules: a rule that only says "remove the upstream rule with this id"
// has no matchers, no state, and no region to validate.
func ValidateOverlay(m *Manifest) error {
	if m == nil {
		return fmt.Errorf("overlay is nil")
	}
	if len(m.Rules) == 0 {
		return fmt.Errorf("overlay must contain at least one rule")
	}
	live := &Manifest{ID: m.ID, Version: m.Version, MinEngineVersion: m.MinEngineVersion}
	for i := range m.Rules {
		rule := &m.Rules[i]
		if strings.TrimSpace(rule.ID) == "" {
			return fmt.Errorf("overlay rule id must not be empty")
		}
		if rule.Disabled() {
			gate := rule.RootGate()
			if gateHasAnyMatcher(&gate) || rule.State != nil {
				return fmt.Errorf("overlay rule %s uses disable together with rule content; disable removes a rule, it does not edit one", rule.ID)
			}
			continue
		}
		live.Rules = append(live.Rules, *rule)
	}
	if len(live.Rules) == 0 {
		return nil
	}
	if err := Validate(live); err != nil {
		return err
	}
	// Validate parses each rule's region into its own copy; carry that back so
	// the caller's rules are ready to compile.
	for i := range live.Rules {
		for j := range m.Rules {
			if m.Rules[j].ID == live.Rules[i].ID && !m.Rules[j].Disabled() {
				m.Rules[j].region = live.Rules[i].region
				break
			}
		}
	}
	return nil
}

// Merge layers a Sidecar overlay over a vendored upstream manifest.
//
// The rules, from docs/plans/active/herdr-detection-parity.md ("Overlay merge"):
//
//   - an overlay rule whose id equals an upstream rule id replaces it in place,
//     keeping upstream's position in the file so a priority tie still breaks the
//     way it did before;
//   - `disable = true` on an upstream id removes that rule;
//   - any other overlay rule is appended, and its id must start with
//     `sidecar.`;
//   - the merged result is validated with the same limits as a plain manifest,
//     so an overlay cannot smuggle a file past the caps.
//
// upstream is never mutated. The merged manifest keeps upstream's id, version,
// and min_engine_version: an overlay is an amendment to a vendored file, not a
// new file, and reporting the overlay's own version as the manifest version
// would make a sync diff unreadable.
func Merge(upstream, overlay *Manifest) (*Manifest, error) {
	if upstream == nil {
		return nil, fmt.Errorf("upstream manifest is nil")
	}
	if overlay == nil {
		clone := *upstream
		clone.Rules = append([]Rule(nil), upstream.Rules...)
		return &clone, nil
	}

	merged := *upstream
	merged.Rules = append([]Rule(nil), upstream.Rules...)
	merged.Aliases = append([]string(nil), upstream.Aliases...)

	index := make(map[string]int, len(merged.Rules))
	for i := range merged.Rules {
		index[merged.Rules[i].ID] = i
	}

	removed := make(map[int]bool)
	for i := range overlay.Rules {
		rule := overlay.Rules[i]
		at, known := index[rule.ID]
		switch {
		case rule.Disabled():
			if !known {
				return nil, fmt.Errorf("overlay disables unknown rule %q; upstream has no rule with that id", rule.ID)
			}
			removed[at] = true
		case known:
			// Replacement keeps upstream's slot. The overlay's own Disable
			// pointer is dropped so the merged manifest is a plain manifest.
			rule.Disable = nil
			merged.Rules[at] = rule
		default:
			if !strings.HasPrefix(rule.ID, OverlayIDPrefix) {
				return nil, fmt.Errorf("overlay rule %q adds a new rule and must be prefixed %q; only a rule that replaces or disables an upstream rule may use an upstream id",
					rule.ID, OverlayIDPrefix)
			}
			rule.Disable = nil
			merged.Rules = append(merged.Rules, rule)
			index[rule.ID] = len(merged.Rules) - 1
		}
	}

	if len(removed) > 0 {
		kept := merged.Rules[:0:0]
		for i := range merged.Rules {
			if removed[i] {
				continue
			}
			kept = append(kept, merged.Rules[i])
		}
		merged.Rules = kept
	}

	// The merged file is held to exactly the limits a vendored file is held to.
	// AllowIncompatibleRegex is set because the merged tree legitimately still
	// carries upstream's `\p{Alphabetic}` rules wherever an overlay has not
	// rewritten them; Compile records those per rule instead.
	if err := ValidateWith(&merged, ValidateOptions{AllowIncompatibleRegex: true}); err != nil {
		return nil, fmt.Errorf("merged manifest is invalid: %w", err)
	}
	return &merged, nil
}
