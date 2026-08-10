package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDefault_TasksPosition(t *testing.T) {
	cfg := Default()
	if cfg.Plugins.Tasks.Position != TasksPositionAfterWorkspaces {
		t.Errorf("got position %q, want %q", cfg.Plugins.Tasks.Position, TasksPositionAfterWorkspaces)
	}
}

func TestLoadFrom_TasksPosition(t *testing.T) {
	tests := []struct {
		name string
		json string
		want string
	}{
		{"absent", `{}`, TasksPositionAfterWorkspaces},
		{"empty plugins.tasks", `{"plugins":{"tasks":{}}}`, TasksPositionAfterWorkspaces},
		{"after-notes", `{"plugins":{"tasks":{"position":"after-notes"}}}`, TasksPositionAfterNotes},
		{"after-workspaces", `{"plugins":{"tasks":{"position":"after-workspaces"}}}`, TasksPositionAfterWorkspaces},
		{"unknown coerced", `{"plugins":{"tasks":{"position":"after-mars"}}}`, TasksPositionAfterWorkspaces},
		{"empty string coerced", `{"plugins":{"tasks":{"position":""}}}`, TasksPositionAfterWorkspaces},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(tc.json), 0644); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadFrom(path)
			if err != nil {
				t.Fatalf("LoadFrom: %v", err)
			}
			if cfg.Plugins.Tasks.Position != tc.want {
				t.Errorf("got %q, want %q", cfg.Plugins.Tasks.Position, tc.want)
			}
		})
	}
}

func TestValidate_TasksPosition(t *testing.T) {
	cfg := Default()
	cfg.Plugins.Tasks.Position = "sideways"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if cfg.Plugins.Tasks.Position != TasksPositionAfterWorkspaces {
		t.Errorf("invalid position not coerced: %q", cfg.Plugins.Tasks.Position)
	}

	cfg.Plugins.Tasks.Position = TasksPositionAfterNotes
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if cfg.Plugins.Tasks.Position != TasksPositionAfterNotes {
		t.Errorf("valid position rewritten: %q", cfg.Plugins.Tasks.Position)
	}
}

func TestSave_TasksPositionRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	SetTestConfigPath(path)
	defer ResetTestConfigPath()

	cfg := Default()
	cfg.Plugins.Tasks.Position = TasksPositionAfterNotes
	if err := Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Plugins struct {
			Tasks struct {
				Position string `json:"position"`
			} `json:"tasks"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if raw.Plugins.Tasks.Position != TasksPositionAfterNotes {
		t.Errorf("saved position = %q, want %q", raw.Plugins.Tasks.Position, TasksPositionAfterNotes)
	}

	reloaded, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if reloaded.Plugins.Tasks.Position != TasksPositionAfterNotes {
		t.Errorf("round-tripped position = %q, want %q", reloaded.Plugins.Tasks.Position, TasksPositionAfterNotes)
	}
}

// The plugin's enablement lever is the tasks_plugin feature flag, and the
// embedded package resolves its own store. Neither may appear in sidecar's
// tasks config block.
func TestSave_TasksConfigHasNoEnabledOrStorePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	SetTestConfigPath(path)
	defer ResetTestConfigPath()

	if err := Save(Default()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Plugins struct {
			Tasks map[string]any `json:"tasks"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for key := range raw.Plugins.Tasks {
		if key != "position" {
			t.Errorf("unexpected key %q in plugins.tasks", key)
		}
	}
}
