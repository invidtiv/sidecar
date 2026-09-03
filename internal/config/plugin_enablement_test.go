package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// plugins.<id>.enabled is a tri-state: written true, written false, or absent.
// "Absent" is what keeps the deprecated tasks_plugin and notes_plugin flags
// answering for a config that predates the unified key, so a save that has
// nothing to say about a plugin must not invent an answer for it.
func TestSaveLeavesAnUnwrittenPluginSwitchAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	SetTestConfigPath(path)
	t.Cleanup(ResetTestConfigPath)

	if err := Save(Default()); err != nil {
		t.Fatal(err)
	}
	raw := readRawPlugins(t, path)
	for _, id := range []string{"tasks", "notes"} {
		section, ok := raw[id]
		if !ok {
			continue
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(section, &fields); err != nil {
			t.Fatalf("unmarshal plugins.%s: %v", id, err)
		}
		if _, wrote := fields["enabled"]; wrote {
			t.Fatalf("Save wrote plugins.%s.enabled for a config that never set it: %s", id, section)
		}
	}

	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Plugins.Tasks.Enabled != nil || loaded.Plugins.Notes.Enabled != nil {
		t.Fatalf("an unwritten switch loaded as a decision: tasks=%v notes=%v",
			loaded.Plugins.Tasks.Enabled, loaded.Plugins.Notes.Enabled)
	}
}

// Writing the unified switch must not cost anything else in the file: not the
// deprecated feature flag it replaces (a read-only alias the user keeps), not
// the plugin's other settings, and not a section Sidecar does not manage.
func TestSavePluginsWritesTheSwitchAndDropsNothingElse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	initial := []byte(`{
  "prompts": [{"name": "My Prompt", "body": "do the thing"}],
  "features": {"flags": {"tasks_plugin": true, "notes_plugin": true}},
  "plugins": {
    "tasks": {"position": "after-notes"},
    "notes": {"defaultEditor": "pane"}
  }
}`)
	if err := os.WriteFile(path, initial, 0o600); err != nil {
		t.Fatal(err)
	}
	SetTestConfigPath(path)
	t.Cleanup(ResetTestConfigPath)

	off := false
	if err := SavePlugins(func(p *PluginsConfig) { p.Tasks.Enabled = &off }); err != nil {
		t.Fatalf("SavePlugins: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal saved config: %v", err)
	}
	if _, ok := raw["prompts"]; !ok {
		t.Error("writing a plugin switch dropped the unmanaged prompts section")
	}

	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Plugins.Tasks.Enabled == nil || *loaded.Plugins.Tasks.Enabled {
		t.Fatalf("plugins.tasks.enabled = %v, want a written false", loaded.Plugins.Tasks.Enabled)
	}
	// The flag is a read-only alias: the user set it once and keeps it, even
	// though the key now outranks it.
	if !loaded.Features.Flags["tasks_plugin"] || !loaded.Features.Flags["notes_plugin"] {
		t.Fatalf("the deprecated flags were rewritten: %v", loaded.Features.Flags)
	}
	// The switch is not the only thing under plugins.<id>.
	if loaded.Plugins.Tasks.Position != TasksPositionAfterNotes {
		t.Errorf("plugins.tasks.position = %q, want it preserved", loaded.Plugins.Tasks.Position)
	}
	if loaded.Plugins.Notes.DefaultEditor != NotesEditorPane {
		t.Errorf("plugins.notes.defaultEditor = %q, want it preserved", loaded.Plugins.Notes.DefaultEditor)
	}
	// Notes said nothing, so it still says nothing.
	if loaded.Plugins.Notes.Enabled != nil {
		t.Errorf("writing the Tasks switch invented a Notes answer: %v", *loaded.Plugins.Notes.Enabled)
	}
}

func readRawPlugins(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Plugins map[string]json.RawMessage `json:"plugins"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal saved config: %v", err)
	}
	return raw.Plugins
}
