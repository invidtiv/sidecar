package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/palette"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/plugins/tasks"
)

// This file is the end-to-end half of "sidecar must not advertise a key that
// does something else". The Tasks plugin's registration rule is unit-tested in
// its own package; here the REAL plugin is driven through the REAL footer and
// palette code, because those two surfaces are what the user actually reads,
// and both are built from registered bindings.
//
// internal/app importing internal/plugins/tasks is safe: the dependency only
// runs the other way for workspace, which internal/app does not import.

const tasksFixture = `{"type":"meta","version":2}
{"type":"section","id":"1a2b3c01","title":"Inbox","body":"Capture here first."}
{"type":"task","id":"1a2b3c02","parent":"1a2b3c01","state":"NEXT","priority":"B","title":"Wire the sidecar tab"}
`

// liveTasksModel boots the real Tasks plugin against an isolated store and
// returns a sidecar model with it as the active tab.
func liveTasksModel(t *testing.T) (Model, *tasks.Plugin) {
	t.Helper()

	root := t.TempDir()
	data := filepath.Join(root, "data")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "tasks.jsonl"), []byte(tasksFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	// Tasks snapshots the process environment when the plugin carries no
	// override, so isolation has to happen here.
	t.Setenv("HOME", root)
	t.Setenv("TASKS_DIR", data)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))

	km := keymap.NewRegistry()
	reg := plugin.NewRegistry(&plugin.Context{Keymap: km, WorkDir: root})

	p := tasks.New()
	if err := reg.Register(p); err != nil {
		t.Fatal(err)
	}
	if len(reg.Plugins()) == 0 {
		t.Fatal("tasks plugin did not register")
	}

	cmd := p.Start()
	if cmd == nil {
		t.Fatal("Start() returned no command")
	}
	ready, ok := cmd().(tasks.TasksReadyMsg)
	if !ok {
		t.Fatal("Start() did not produce TasksReadyMsg")
	}
	if ready.Err != nil || ready.Model == nil {
		t.Skipf("tasks is not buildable in this environment: %v", ready.Err)
	}
	p.Update(ready)

	m := Model{
		registry:     reg,
		keymap:       km,
		palette:      palette.New(),
		activePlugin: 0,
		ui:           &UIState{},
		ready:        true,
		width:        200,
		height:       40,
		cfg:          &config.Config{},
	}
	m.updateContext()
	return m, p
}

// TestTasksFooterAdvertisesNoKeyTheHostKeeps is the before/after of the bug:
// the footer used to read "… 1 Jump  2 Jump  3 Jump …" while `1`-`6` switched
// sidecar tabs.
func TestTasksFooterAdvertisesNoKeyTheHostKeeps(t *testing.T) {
	m, p := liveTasksModel(t)
	t.Cleanup(p.Stop)

	context := p.FocusContext()
	if context != "tasks-list" {
		t.Fatalf("expected the Tasks list context, got %q", context)
	}

	hints := m.pluginFooterHints(p, context)
	if len(hints) == 0 {
		t.Fatal("the Tasks tab lost its footer hints entirely")
	}

	var line []string
	for _, h := range hints {
		line = append(line, h.keys+" "+h.label)
	}
	rendered := strings.Join(line, "  ")
	t.Logf("tasks footer hints: %s", rendered)

	for _, key := range []string{"1", "2", "3", "4", "5", "6", "K", "W", "#", "q", "?"} {
		for _, h := range hints {
			for _, part := range strings.Split(h.keys, "/") {
				if part == key {
					t.Errorf("footer advertises %q (%s), but that key reaches sidecar, not Tasks", key, h.label)
				}
			}
		}
	}
}

// TestTasksCommandsSurviveTheKeysTheyLost is the other half: taking the key
// away is only acceptable because the command stays reachable.
func TestTasksCommandsSurviveTheKeysTheyLost(t *testing.T) {
	m, p := liveTasksModel(t)
	t.Cleanup(p.Stop)
	p.Update(tea.WindowSizeMsg{Width: 200, Height: 40})

	entriesByID := func() map[string]palette.PaletteEntry {
		byID := map[string]palette.PaletteEntry{}
		for _, e := range palette.BuildEntries(m.keymap, m.registry.Plugins(), m.activeContext, p.ID()) {
			if e.Context == p.FocusContext() {
				byID[e.CommandID] = e
			}
		}
		return byID
	}

	// Keys sidecar keeps. delete-selected also answers to `delete`, which
	// sidecar does not bind, so it legitimately keeps that one.
	refusedKeys := []string{"1", "2", "3", "4", "5", "6", "K", "W", "#"}
	checkEntry := func(byID map[string]palette.PaletteEntry, id string) {
		t.Helper()
		entry, ok := byID[id]
		if !ok {
			t.Errorf("%s is not in the palette; it lost its key AND its entry", id)
			return
		}
		for _, refused := range refusedKeys {
			if entry.Key == refused {
				t.Errorf("%s still advertises %q", id, refused)
			}
		}
		if entry.Description == "" {
			t.Errorf("%s has no description to find it by", id)
		}
	}

	// The view jumps used to live on `1`-`6`.
	byID := entriesByID()
	for _, id := range []string{"view-agenda", "view-next", "view-quadrants", "view-projects", "view-outline", "view-inbox"} {
		checkEntry(byID, id)
		if entry := byID[id]; entry.Key != "" {
			t.Errorf("%s should be keyless now, got %q", id, entry.Key)
		}
	}

	// Selecting one must actually run it. Switching to the Next view is the
	// proof: the fixture's only task is a NEXT, so the list is empty on Agenda
	// and populated afterwards.
	before := p.View(200, 40)
	updated, cmd := m.Update(palette.CommandSelectedMsg{CommandID: "view-next", Context: p.FocusContext()})
	if cmd != nil {
		if msg := cmd(); msg != nil {
			updated.(Model).ActivePlugin().Update(msg)
		}
	}
	after := p.View(200, 40)
	if after == before {
		t.Fatal("selecting view-next in the palette changed nothing; the command is not reachable")
	}
	if !strings.Contains(after, "Wire the sidecar tab") {
		t.Fatalf("the Next view did not open:\n%s", after)
	}

	// With a task selected, the selection-gated commands that used to sit on
	// `K`, `W` and `#` are in the palette too.
	byID = entriesByID()
	for _, id := range []string{"raise-priority", "set-work-ref-selected", "delete-selected"} {
		checkEntry(byID, id)
	}
}

// TestPaletteEntriesUnchangedForPluginsThatBindNothing guards the shared code
// path: BuildEntries now emits keyless entries, and the plugins that publish
// Commands() purely as documentation for keys they handle themselves must not
// suddenly fill the palette with inert rows.
func TestPaletteEntriesUnchangedForPluginsThatBindNothing(t *testing.T) {
	p := newRouterPlugin()
	p.commands = []plugin.Command{
		{ID: "documented-only", Name: "Doc", Context: "tasks-list"},
		{ID: "runnable", Name: "Run", Context: "tasks-list", Handler: func() tea.Cmd { return nil }},
	}
	m := routerTestModel(t, p)

	entries := palette.BuildEntries(m.keymap, m.registry.Plugins(), "tasks-list", "tasks")

	var ids []string
	for _, e := range entries {
		ids = append(ids, e.CommandID)
	}
	if len(entries) != 1 || entries[0].CommandID != "runnable" {
		t.Fatalf("palette entries = %v, want only the invocable keyless command", ids)
	}
	if entries[0].Key != "" {
		t.Fatalf("keyless entry advertises %q", entries[0].Key)
	}
}
