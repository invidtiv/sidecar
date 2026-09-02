package config

import (
	"os"
	"path/filepath"
	"testing"
)

// detection.remoteManifests decides whether Sidecar ever fetches an
// agent-detection manifest at runtime. The property these tests pin is that the
// only way to turn it on is to spell one of the two accepted words or a real
// http(s) URL: everything else, including the values a user would plausibly
// guess at, resolves to off.

func TestRemoteCatalogURLResolvesOnlyTheAcceptedValues(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  string
	}{
		{"", ""},
		{"off", ""},
		{"OFF", ""},
		{"  off  ", ""},
		{"herdr.dev", HerdrCatalogURL},
		{"Herdr.Dev", HerdrCatalogURL},
		{"https://example.test/agent-detection/index.toml", "https://example.test/agent-detection/index.toml"},
		{"http://127.0.0.1:8080/index.toml", "http://127.0.0.1:8080/index.toml"},
	} {
		got, err := (DetectionConfig{RemoteManifests: tc.value}).RemoteCatalogURL()
		if err != nil {
			t.Fatalf("%q: %v", tc.value, err)
		}
		if got != tc.want {
			t.Fatalf("%q resolved to %q, want %q", tc.value, got, tc.want)
		}
	}
}

// TestAnUnrecognisedRemoteManifestsValueIsRefusedNotTreatedAsOn is the safety
// property. A typo could be read either way, and only one of the two readings
// can be wrong quietly: reading it as "on" would put a network fetch behind a
// setting nobody successfully turned on.
func TestAnUnrecognisedRemoteManifestsValueIsRefusedNotTreatedAsOn(t *testing.T) {
	for _, value := range []string{
		"on", "true", "1", "yes", "enabled",
		"herdr", "herdr.dev/agent-detection", "www.herdr.dev",
		"ftp://herdr.dev/index.toml", "file:///tmp/index.toml",
		"https://", "://", "not a url at all",
	} {
		got, err := (DetectionConfig{RemoteManifests: value}).RemoteCatalogURL()
		if err == nil {
			t.Fatalf("%q was accepted and resolved to %q", value, got)
		}
		if got != "" {
			t.Fatalf("%q was refused but still produced a URL %q", value, got)
		}
		if (DetectionConfig{RemoteManifests: value}).RemoteManifestsEnabled() {
			t.Fatalf("%q reported as enabled", value)
		}
	}
}

func TestRemoteManifestsIsOffByDefault(t *testing.T) {
	cfg := Default()
	if cfg.Detection.RemoteManifests != RemoteManifestsOff {
		t.Fatalf("default remoteManifests = %q, want %q", cfg.Detection.RemoteManifests, RemoteManifestsOff)
	}
	if cfg.Detection.RemoteManifestsEnabled() {
		t.Fatal("the default configuration reports runtime fetching as enabled")
	}
}

// TestLoadKeepsARefusedRemoteManifestsValueVerbatimAndOff pins both halves of
// what a typo does: fetching stays off, and the value the user actually wrote
// survives the load so a verb can show it back to them. Dropping it here left
// `sidecar agent manifests` unable to distinguish a typo from "off", which is
// the one thing it exists to tell them.
func TestLoadKeepsARefusedRemoteManifestsValueVerbatimAndOff(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		enabled          bool
	}{
		{name: "absent section", body: `{}`, want: RemoteManifestsOff},
		{name: "explicitly off", body: `{"detection":{"remoteManifests":"off"}}`, want: RemoteManifestsOff},
		{name: "herdr.dev", body: `{"detection":{"remoteManifests":"herdr.dev"}}`, want: RemoteManifestsHerdrDev, enabled: true},
		{name: "a URL", body: `{"detection":{"remoteManifests":"https://example.test/i.toml"}}`, want: "https://example.test/i.toml", enabled: true},
		{name: "a typo", body: `{"detection":{"remoteManifests":"on"}}`, want: "on"},
		{name: "an empty string", body: `{"detection":{"remoteManifests":""}}`, want: RemoteManifestsOff},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			SetTestConfigPath(path)
			t.Cleanup(ResetTestConfigPath)

			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Detection.RemoteManifests != tc.want {
				t.Fatalf("remoteManifests = %q, want %q", cfg.Detection.RemoteManifests, tc.want)
			}
			if got := cfg.Detection.RemoteManifestsEnabled(); got != tc.enabled {
				t.Fatalf("RemoteManifestsEnabled() = %v, want %v", got, tc.enabled)
			}
		})
	}
}
