package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNotificationsSectionIsReadFromTheConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{
	  "notifications": {
	    "sources": {
	      "agent": {"expiry": "20s"},
	      "waiting": {"expiry": "sticky"},
	      "system": {"expiry": "nonsense"}
	    }
	  }
	}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("an unreadable expiry must not fail the load: %v", err)
	}
	expiries := cfg.Notifications.SourceExpiries()
	if got := expiries["agent"]; got != 20*time.Second {
		t.Fatalf("agent expiry = %s, want 20s", got)
	}
	if got, ok := expiries["waiting"]; !ok || got != StickyExpiry {
		t.Fatalf("waiting expiry = %s (present=%v), want sticky", got, ok)
	}
	if _, ok := expiries["system"]; ok {
		t.Fatal("an unparseable expiry must be skipped, not guessed at")
	}
}

func TestAbsentNotificationsSectionResolvesToNoOverrides(t *testing.T) {
	cfg := Default()
	if got := cfg.Notifications.SourceExpiries(); got != nil {
		t.Fatalf("default config carries overrides: %v", got)
	}
}
