package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAPIUIDirLoadsAndSurvivesConfigSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	SetTestConfigPath(path)
	t.Cleanup(ResetTestConfigPath)
	if err := os.WriteFile(path, []byte(`{"api":{"uiDir":"/tmp/ui bundle","tailnetLogins":["owner@example.com"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil || cfg.API.UIDir != "/tmp/ui bundle" {
		t.Fatalf("loaded API: %+v %v", cfg, err)
	}
	// A UI preference write must preserve the user-authored API section.
	if err := SaveUI(func(ui *UIConfig) { ui.ShowClock = false }); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load()
	if err != nil || cfg.API.UIDir != "/tmp/ui bundle" {
		t.Fatalf("saved API: %+v %v", cfg, err)
	}
}
