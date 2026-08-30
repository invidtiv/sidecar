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

func TestSSHNotificationDeliveryDefaultsOff(t *testing.T) {
	cfg := Default()
	if cfg.Notifications.SSH.ManagedHosts {
		t.Fatal("managed-host delivery must default off so an upgrade never moves remote text onto the local desktop")
	}
	if cfg.Notifications.SSH.Terminal != TerminalNotifierOff {
		t.Fatalf("terminal transport = %q, want off", cfg.Notifications.SSH.Terminal)
	}
}

func TestSSHNotificationSectionIsReadAndValidated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{
	  "notifications": {"ssh": {"managedHosts": true, "terminal": "kitty"}}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Notifications.SSH.ManagedHosts {
		t.Fatal("managedHosts was not read from the file")
	}
	if cfg.Notifications.SSH.Terminal != TerminalNotifierKitty {
		t.Fatalf("terminal = %q, want kitty", cfg.Notifications.SSH.Terminal)
	}

	for _, name := range []TerminalNotifier{
		TerminalNotifierOff, TerminalNotifierAuto, TerminalNotifierGhostty,
		TerminalNotifierITerm2, TerminalNotifierWezTerm, TerminalNotifierKitty,
	} {
		valid := Default().Notifications
		valid.SSH.Terminal = name
		if err := ValidateNotifications(valid, path); err != nil {
			t.Fatalf("terminal %q must be accepted: %v", name, err)
		}
	}
	invalid := Default().Notifications
	invalid.SSH.Terminal = "bell"
	if err := ValidateNotifications(invalid, path); err == nil {
		t.Fatal("an unsupported terminal must be refused rather than falling back to a generic escape")
	}
}

func TestAbsentSSHKeysLeaveTheDefaultsInForce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"notifications": {"ssh": {}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Notifications.SSH.ManagedHosts {
		t.Fatal("an empty ssh section must not enable managed-host delivery")
	}
	if cfg.Notifications.SSH.Terminal != TerminalNotifierOff {
		t.Fatalf("terminal = %q, want the default off", cfg.Notifications.SSH.Terminal)
	}
}

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
