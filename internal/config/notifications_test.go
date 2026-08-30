package config

import (
	"encoding/json"
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

func boolPtr(v bool) *bool { return &v }

func TestSaveNotificationsPreservesUnknownRootsAndSources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	SetTestConfigPath(path)
	t.Cleanup(ResetTestConfigPath)
	if err := os.WriteFile(path, []byte(`{
  "futureRoot": {"keep": true},
  "notifications": {
    "native": {"mode": "off", "provider": "auto"},
    "sound": {"mode": "off"},
    "quietHours": {"enabled": false, "start": "22:00", "end": "08:00"},
    "sources": {"future-source": {"native": false, "sound": "none", "expiry": "19s"}}
  }
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveNotifications(func(cfg *NotificationsConfig) {
		cfg.Native.Mode = DeliveryBackground
		cfg.Sources["waiting"] = NotificationSourceConfig{Native: boolPtr(true), Sound: SoundAttention, Expiry: "sticky"}
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["futureRoot"]; !ok {
		t.Fatal("targeted notification save dropped an unrelated root key")
	}
	reloaded, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Notifications.Native.Mode != DeliveryBackground {
		t.Fatalf("native mode = %q", reloaded.Notifications.Native.Mode)
	}
	if got := reloaded.Notifications.Sources["future-source"].Expiry; got != "19s" {
		t.Fatalf("unknown source was not preserved: %q", got)
	}
}

func TestSaveNotificationsRejectsInvalidEditWithoutTouchingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	SetTestConfigPath(path)
	t.Cleanup(ResetTestConfigPath)
	if err := Save(Default()); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveNotifications(func(cfg *NotificationsConfig) { cfg.Sound.Mode = "sometimes" }); err == nil {
		t.Fatal("invalid mode was saved")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("failed validation changed config.json")
	}
	if err := SaveNotifications(func(cfg *NotificationsConfig) { cfg.Sound.AttentionPath = "missing.wav" }); err == nil {
		t.Fatal("missing custom path was saved")
	}
	after, _ = os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("failed path validation changed config.json")
	}
}

func TestAbsentNotificationsSectionResolvesToNoOverrides(t *testing.T) {
	cfg := Default()
	if got := cfg.Notifications.SourceExpiries(); got != nil {
		t.Fatalf("default config carries overrides: %v", got)
	}
}
