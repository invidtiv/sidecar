package app

import (
	"testing"

	"github.com/marcus/sidecar/internal/agentactivity/manifests"
	"github.com/marcus/sidecar/internal/config"
)

// The app-level gate on the opt-in runtime catalog fetch.
//
// The manifests package has its own test that FetchFromConfig builds no HTTP
// client with the setting off, and that is one layer below what actually
// decides: fetchDetectionManifestsCmd is what runs at startup, and if it
// returned a command with the setting off there would be a goroutine parked on
// the first-ready-frame latch for the life of the process, holding a config
// value nobody asked it to act on. Nothing below it would ever say so, because
// the command's own body is what consults the setting a second time.

// TestNoDetectionFetchCommandExistsWithTheSettingOff is the app half of the
// exit gate: with the setting off there is no command at all.
func TestNoDetectionFetchCommandExistsWithTheSettingOff(t *testing.T) {
	before := manifests.HTTPClientsBuilt()
	for _, value := range []string{"", config.RemoteManifestsOff, "OFF", "on", "yes please"} {
		cfg := config.Default()
		cfg.Detection.RemoteManifests = value
		if cmd := fetchDetectionManifestsCmd(cfg); cmd != nil {
			t.Fatalf("remoteManifests=%q returned a fetch command", value)
		}
	}
	if cmd := fetchDetectionManifestsCmd(nil); cmd != nil {
		t.Fatal("a nil config returned a fetch command")
	}
	if got := manifests.HTTPClientsBuilt(); got != before {
		t.Fatalf("HTTP clients built with fetching off: %d, want %d", got, before)
	}
}

// TestADetectionFetchCommandExistsWithTheSettingOn is the other half, and it
// stops the assertion above from passing because the gate refuses everything.
// The command is not run: doing so would wait on the first-ready-frame latch,
// which is the ordering rule this command exists to obey.
func TestADetectionFetchCommandExistsWithTheSettingOn(t *testing.T) {
	for _, value := range []string{config.RemoteManifestsHerdrDev, "https://example.test/index.toml"} {
		cfg := config.Default()
		cfg.Detection.RemoteManifests = value
		if cmd := fetchDetectionManifestsCmd(cfg); cmd == nil {
			t.Fatalf("remoteManifests=%q returned no fetch command", value)
		}
	}
}
