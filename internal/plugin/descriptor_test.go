package plugin

import (
	"testing"

	"github.com/marcus/sidecar/internal/config"
)

func TestDescriptorEnablement(t *testing.T) {
	on := func(*config.Config) bool { return true }
	off := func(*config.Config) bool { return false }
	cfg := config.Default()

	tests := []struct {
		name               string
		d                  Descriptor
		enabled, preferred bool
	}{
		{"no switch is always on", Descriptor{}, true, true},
		{"enabled answers both when there is no separate preference",
			Descriptor{Enabled: off}, false, false},
		{"a preference can differ from the effective answer",
			Descriptor{Enabled: off, Preference: on}, false, true},
		{"an enabled plugin with a preference still reads its own switch",
			Descriptor{Enabled: on, Preference: off}, true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.d.IsEnabled(cfg); got != tc.enabled {
				t.Errorf("IsEnabled = %v, want %v", got, tc.enabled)
			}
			if got := tc.d.IsPreferred(cfg); got != tc.preferred {
				t.Errorf("IsPreferred = %v, want %v", got, tc.preferred)
			}
		})
	}

	// A nil config is the default config, not a panic: the CLI reads
	// descriptors before it has loaded anything.
	if !(Descriptor{Enabled: on}).IsEnabled(nil) {
		t.Error("nil config did not fall back to the defaults")
	}
}

func TestDescriptorPlacementsAndSwitch(t *testing.T) {
	d := Descriptor{Placements: []Placement{PlacementTab}}
	if !d.HasPlacement(PlacementTab) || d.HasPlacement(PlacementPanes) {
		t.Fatalf("placements = %v", d.Placements)
	}
	if d.HasSwitch() {
		t.Fatal("a descriptor with no SetEnabled reported a switch")
	}
	d.SetEnabled = func(*config.PluginsConfig, bool) {}
	if !d.HasSwitch() {
		t.Fatal("a descriptor with SetEnabled reported no switch")
	}
	if d.NeedsCommand() {
		t.Fatal("an in-repo descriptor claimed it needs a command")
	}
	d.Integration.Executable = "tasks"
	if !d.NeedsCommand() {
		t.Fatal("a descriptor with an executable claimed it needs none")
	}
}
