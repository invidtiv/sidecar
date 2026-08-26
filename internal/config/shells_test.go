package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTombstoneRetentionWindow(t *testing.T) {
	tests := []struct {
		raw  string
		want time.Duration
	}{
		{"", DefaultTombstoneRetention},
		{"14d", 14 * 24 * time.Hour},
		{"1d", 24 * time.Hour},
		{"0.5d", 12 * time.Hour},
		{"336h", 336 * time.Hour},
		{"90m", 90 * time.Minute},
		{"forever", KeepTombstonesForever},
		{"Never", KeepTombstonesForever},
		{"off", KeepTombstonesForever},
		{"0", KeepTombstonesForever},
		{"0s", KeepTombstonesForever},
		// Unreadable values fall back rather than failing the load; the failure
		// mode of "reject it" would be a user losing their shell records to a
		// typo.
		{"two weeks", DefaultTombstoneRetention},
		{"-3d", DefaultTombstoneRetention},
		{"-5h", DefaultTombstoneRetention},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got := ShellsConfig{TombstoneRetention: tt.raw}.TombstoneRetentionWindow()
			if got != tt.want {
				t.Fatalf("TombstoneRetentionWindow(%q) = %v; want %v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestLoadShellsSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"shells":{"tombstoneRetention":"30d"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Shells.TombstoneRetention != "30d" {
		t.Fatalf("Shells.TombstoneRetention = %q", cfg.Shells.TombstoneRetention)
	}
	if got := cfg.Shells.TombstoneRetentionWindow(); got != 30*24*time.Hour {
		t.Fatalf("window = %v; want 720h", got)
	}
}

// An absent section leaves the default in place — the same rule the other
// pointer-typed sections follow.
func TestLoadWithoutShellsSectionKeepsDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Shells.TombstoneRetentionWindow(); got != DefaultTombstoneRetention {
		t.Fatalf("window = %v; want %v", got, DefaultTombstoneRetention)
	}
}

// Save preserves keys it does not manage, and `shells` is one of them: a user
// who set a retention window must not lose it the next time Sidecar writes a
// theme.
func TestSavePreservesShellsSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"shells":{"tombstoneRetention":"30d"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	SetTestConfigPath(path)
	t.Cleanup(ResetTestConfigPath)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Shells.TombstoneRetention != "30d" {
		t.Fatalf("Shells.TombstoneRetention after save = %q", reloaded.Shells.TombstoneRetention)
	}
}
