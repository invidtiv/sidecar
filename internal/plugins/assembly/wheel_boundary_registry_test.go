package assembly

import (
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/plugin"
)

// everythingOn plans with every plugin switch and feature flag enabled, so the
// registry is checked against the complete set of registrable plugins rather
// than the default subset.
func everythingOn(t *testing.T) []Entry {
	t.Helper()
	initFeatures(t, map[string]bool{
		features.ConversationsPlugin.Name: true,
		features.NotesPlugin.Name:         true,
	})
	cfg := config.Default()
	cfg.Plugins.TDMonitor.Enabled = true
	cfg.Plugins.GitStatus.Enabled = true
	cfg.Plugins.FileBrowser.Enabled = true
	cfg.Plugins.Conversations.Enabled = true
	return Plan(cfg)
}

// TestEveryRegistrablePluginHasAWheelBoundaryPolicy is the gate: a new plugin
// cannot join tab order without an explicit declared boundary policy.
func TestEveryRegistrablePluginHasAWheelBoundaryPolicy(t *testing.T) {
	for _, e := range everythingOn(t) {
		if _, ok := WheelBoundaryPolicyFor(e.ID); !ok {
			t.Errorf("plugin %q is registrable but has no WheelBoundaryRegistry row; declare covered, externally-owned, or a named exclusion", e.ID)
		}
	}
}

// TestWheelBoundaryRegistryMatchesReality keeps the declarations honest in both
// directions: a covered surface must implement the contract, and an excluded
// surface that starts implementing it must be reclassified rather than left
// misdescribed.
func TestWheelBoundaryRegistryMatchesReality(t *testing.T) {
	for _, s := range WheelBoundaryRegistry {
		if s.Probe == nil {
			if s.Policy != WheelCovered {
				t.Errorf("%s: a non-plugin surface may only be declared covered here; %q needs its own ledger", s.ID, s.Policy)
			}
			continue
		}
		_, implements := s.Probe.(plugin.WheelBoundaryConsumer)
		switch s.Policy {
		case WheelCovered:
			if !implements {
				t.Errorf("%s is declared covered but does not implement plugin.WheelBoundaryConsumer", s.ID)
			}
		case WheelExternallyOwned, WheelDeprecatedExclusion:
			if implements {
				t.Errorf("%s implements plugin.WheelBoundaryConsumer but is still declared %q; update its registry row", s.ID, s.Policy)
			}
		default:
			t.Errorf("%s has unknown policy %q", s.ID, s.Policy)
		}
	}
}

func TestWheelBoundaryRegistryRowsAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range WheelBoundaryRegistry {
		if s.ID == "" || s.Surface == "" {
			t.Errorf("registry row %+v needs an ID and a surface description", s)
		}
		if seen[s.ID] && s.ID != "tasks-project" {
			t.Errorf("duplicate registry row for %q", s.ID)
		}
		seen[s.ID] = true
		if s.Policy != WheelCovered && s.Note == "" {
			t.Errorf("%s is not covered, so it must record why and what would change that", s.ID)
		}
	}
}

// TestConversationsIsANamedDeprecatedExclusion pins the one exclusion the plan
// calls out by name, so removing the plugin or undeprecating it forces a
// deliberate decision here.
func TestConversationsIsANamedDeprecatedExclusion(t *testing.T) {
	row, ok := WheelBoundaryPolicyFor(IDConversations)
	if !ok {
		t.Fatal("Conversations must stay recorded as a named exclusion, not silently omitted")
	}
	if row.Policy != WheelDeprecatedExclusion {
		t.Errorf("Conversations policy = %q, want %q", row.Policy, WheelDeprecatedExclusion)
	}
}
