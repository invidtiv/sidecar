package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/plugin"
)

// The lifecycle guarantee is per host, not per the one host Tasks used to be:
// a project switch rebuilds the registry and must leave every hosted global
// plugin alone, however many there are.
func TestProjectSwitchRestartsNoGlobalPluginHost(t *testing.T) {
	isolateAppState(t)
	km := keymap.NewRegistry()
	registry := plugin.NewRegistry(&plugin.Context{Keymap: km, WorkDir: "/tmp/one", ProjectRoot: "/tmp/one"})
	for _, id := range []string{"files", "workspaces", "git", "notes"} {
		if err := registry.Register(&navigationPlugin{id: id}); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	features.Init(cfg)
	t.Cleanup(func() { features.Init(config.Default()) })

	m := New(registry, km, cfg, "", "/tmp/one", "/tmp/one", "git")
	first := &hostedTestPlugin{id: "tasks", context: "tasks-list"}
	second := &hostedTestPlugin{id: "recall", context: "recall-list"}
	installGlobalHost(&m, globalTabTasks, "Tasks", first)
	installGlobalHost(&m, "recall", "Recall", second)
	m.width, m.height, m.ready = 140, 40, true

	for _, cmd := range m.startGlobalHosts() {
		if cmd != nil {
			cmd()
		}
	}
	for _, host := range []*hostedTestPlugin{first, second} {
		if host.inits != 1 || host.starts != 1 {
			t.Fatalf("%s lifecycle after start: inits=%d starts=%d", host.id, host.inits, host.starts)
		}
	}

	firstUpdates, secondUpdates := first.updates, second.updates
	for i := 0; i < 3; i++ {
		m.registry.Reinit("/tmp/two", "/tmp/two")
	}
	for _, host := range []*hostedTestPlugin{first, second} {
		if host.inits != 1 || host.starts != 1 || host.stops != 0 {
			t.Fatalf("project switches disturbed %s: inits=%d starts=%d stops=%d",
				host.id, host.inits, host.starts, host.stops)
		}
	}

	// Both keep receiving forwarded messages afterwards, including the one
	// whose tab nobody is looking at: that is what keeps a global plugin's
	// watches and queues alive.
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	m = asAppModel(t, updated)
	if first.updates <= firstUpdates || second.updates <= secondUpdates {
		t.Fatalf("a host stopped receiving messages after a project switch: first=%d second=%d",
			first.updates, second.updates)
	}

	m.shutdown()
	m.shutdown()
	if first.stops != 1 || second.stops != 1 {
		t.Fatalf("stops after two shutdowns: first=%d second=%d", first.stops, second.stops)
	}
}

// A hosted global plugin's content deck is persisted under a root keyed by its
// plugin ID. Tasks' root is checked against its literal historical value on
// purpose: renaming it would not fail anything at compile time, it would
// silently orphan every layout a user has already saved.
func TestGlobalDeckRootIsPerPluginAndStableForTasks(t *testing.T) {
	if got := globalDeckRoot("tasks"); got != "@global-tasks" {
		t.Fatalf("Tasks deck root = %q, want the persisted @global-tasks", got)
	}
	if globalDeckRoot("recall") == globalDeckRoot("tasks") {
		t.Fatal("two global plugins share one deck root, so one would inherit the other's saved layout")
	}
}
