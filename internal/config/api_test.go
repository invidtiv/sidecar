package config

import (
	"encoding/json"
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

func TestSaveAPIUIDirPreservesOtherSettingsAndClears(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	SetTestConfigPath(path)
	t.Cleanup(ResetTestConfigPath)
	seed := `{"api":{"uiDir":"old","tailnetLogins":["owner@example.com"],"future":{"enabled":true}},"prompts":{"custom":"keep"},"ui":{"showClock":false,"futureUI":"keep"}}`
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"/tmp/new ui", ""} {
		if err := SaveAPIUIDir(dir); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load()
		if err != nil || cfg.API.UIDir != dir || len(cfg.API.TailnetLogins) != 1 {
			t.Fatalf("API config: %+v %v", cfg, err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var raw struct {
			API     map[string]json.RawMessage `json:"api"`
			Prompts map[string]string          `json:"prompts"`
			UI      map[string]json.RawMessage `json:"ui"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatal(err)
		}
		var future map[string]bool
		if err := json.Unmarshal(raw.API["future"], &future); err != nil {
			t.Fatal(err)
		}
		if !future["enabled"] || raw.Prompts["custom"] != "keep" || string(raw.UI["futureUI"]) != `"keep"` {
			t.Fatalf("lost unrelated keys: %s", data)
		}
		if _, exists := raw.API["uiDir"]; exists != (dir != "") {
			t.Fatalf("uiDir clearing: %s", data)
		}
	}
}

func TestSaveAPIUIDirRejectsBrokenConfigWithoutWriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	SetTestConfigPath(path)
	t.Cleanup(ResetTestConfigPath)
	for _, seed := range []string{`{"api":`, `{"api":42}`} {
		if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := SaveAPIUIDir("/tmp/ui"); err == nil {
			t.Fatal("accepted broken config")
		}
		after, err := os.ReadFile(path)
		if err != nil || string(after) != seed {
			t.Fatalf("changed broken config: %s %v", after, err)
		}
	}
}
