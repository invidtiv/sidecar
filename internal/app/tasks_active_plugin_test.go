package app

import (
	"path/filepath"
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/state"
)

// registryWith builds a registry whose plugin order mirrors what the assembly
// would produce, without importing the plugin implementations.
func registryWith(t *testing.T, ids ...string) *plugin.Registry {
	t.Helper()
	reg := plugin.NewRegistry(&plugin.Context{Keymap: keymap.NewRegistry()})
	for _, id := range ids {
		if err := reg.Register(&navigationPlugin{id: id}); err != nil {
			t.Fatal(err)
		}
	}
	return reg
}

func TestNew_RestoresTasksTab(t *testing.T) {
	reg := registryWith(t, "td-monitor", "workspace-manager", "tasks", "notes")
	m := New(reg, keymap.NewRegistry(), config.Default(), "", "/tmp/p", "/tmp/p", "tasks")

	if m.activePlugin != 2 {
		t.Fatalf("activePlugin = %d, want 2", m.activePlugin)
	}
	if got := m.ActivePlugin().ID(); got != "tasks" {
		t.Errorf("active plugin = %q, want tasks", got)
	}
}

// A persisted "tasks" active plugin must degrade to a real tab when the
// tasks_plugin feature is off and the plugin was never registered.
func TestNew_PersistedTasksDegradesWhenFlagOff(t *testing.T) {
	reg := registryWith(t, "td-monitor", "workspace-manager")
	m := New(reg, keymap.NewRegistry(), config.Default(), "", "/tmp/p", "/tmp/p", "tasks")

	if m.activePlugin != 0 {
		t.Fatalf("activePlugin = %d, want 0", m.activePlugin)
	}
	if got := m.ActivePlugin(); got == nil || got.ID() != "td-monitor" {
		t.Errorf("expected fallback to the first tab, got %v", got)
	}
}

// The Tasks tab rides sidecar's existing per-project active-plugin state; no
// tasks-specific persistence is involved.
func TestActivePluginState_TasksRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := state.InitWithDir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.InitWithDir(filepath.Dir(config.ConfigPath())) })

	workdir := filepath.Join(dir, "project")
	if err := state.SetActivePlugin(workdir, "tasks"); err != nil {
		t.Fatal(err)
	}
	if got := state.GetActivePluginForWorkDir(workdir, workdir); got != "tasks" {
		t.Fatalf("restored active plugin = %q, want tasks", got)
	}

	reg := registryWith(t, "workspace-manager", "tasks")
	m := New(reg, keymap.NewRegistry(), config.Default(), "", workdir, workdir,
		state.GetActivePluginForWorkDir(workdir, workdir))
	if got := m.ActivePlugin().ID(); got != "tasks" {
		t.Errorf("active plugin = %q, want tasks", got)
	}
}
