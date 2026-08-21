package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/palette"
	"github.com/marcus/sidecar/internal/plugin"
)

// routerTestPlugin stands in for a plugin that routes its own keys — the Tasks
// tab is the first, and the only one today. It is deliberately written against
// the plugin.KeyRouter contract rather than against Tasks, so this file proves
// the host's precedence rules without dragging a task store into an app test.
type routerTestPlugin struct {
	nativeTestPlugin

	id        string
	context   string
	blocks    bool
	textInput bool
	claims    map[string]bool
	rootQuit  bool

	keys     []string
	commands []plugin.Command
}

type blockerOnlyTestPlugin struct {
	nativeTestPlugin
	blocks bool
}

func (p *blockerOnlyTestPlugin) BlocksGlobalKeys() bool { return p.blocks }

func newRouterPlugin(claimed ...string) *routerTestPlugin {
	p := &routerTestPlugin{
		context:  "tasks-list",
		claims:   map[string]bool{},
		rootQuit: true,
	}
	for _, key := range claimed {
		p.claims[key] = true
	}
	return p
}

func (p *routerTestPlugin) ID() string {
	if p.id != "" {
		return p.id
	}
	return "tasks"
}

func (p *routerTestPlugin) FocusContext() string { return p.context }
func (p *routerTestPlugin) ConsumesTextInput() bool {
	return p.textInput
}
func (p *routerTestPlugin) BlocksGlobalKeys() bool { return p.blocks }
func (p *routerTestPlugin) ClaimsKey(key string) bool {
	return p.claims[key]
}
func (p *routerTestPlugin) QuitKeyExits() bool         { return p.rootQuit }
func (p *routerTestPlugin) Commands() []plugin.Command { return p.commands }
func (p *routerTestPlugin) Update(msg tea.Msg) (plugin.Plugin, tea.Cmd) {
	switch typed := msg.(type) {
	case tea.KeyPressMsg:
		p.keys = append(p.keys, typed.String())
	case tea.PasteMsg:
		p.keys = append(p.keys, "paste:"+typed.Content)
	}
	return p, nil
}

func routerTestModel(t *testing.T, p plugin.Plugin) Model {
	t.Helper()
	reg := plugin.NewRegistry(nil)
	if err := reg.Register(p); err != nil {
		t.Fatal(err)
	}
	m := Model{
		registry:     reg,
		keymap:       keymap.NewRegistry(),
		palette:      palette.New(),
		activePlugin: 0,
		ui:           &UIState{},
		ready:        true,
		width:        120,
		height:       30,
		cfg: &config.Config{
			Projects: config.ProjectsConfig{
				List: []config.ProjectConfig{{Name: "alpha", Path: "/tmp/alpha"}},
			},
		},
	}
	m.updateContext()
	return m
}

func TestWorkspaceInteractiveKeepsBareBackslashForTheTerminal(t *testing.T) {
	p := newRouterPlugin()
	p.id = "workspace-manager"
	p.context = "workspace-interactive"
	m := routerTestModel(t, p)
	keymap.RegisterDefaults(m.keymap)
	m.updateContext()

	m.handleKeyMsg(tea.KeyPressMsg{Code: '\\', Text: "\\"})
	wantOnlyPluginKey(t, p, "\\")
}

func TestNotesInlineEditorKeepsCtrlYForTheTerminal(t *testing.T) {
	p := newRouterPlugin()
	p.id = "notes"
	p.context = "notes-inline-edit"
	m := routerTestModel(t, p)
	keymap.RegisterDefaults(m.keymap)
	m.updateContext()

	m.handleKeyMsg(tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl})
	wantOnlyPluginKey(t, p, "ctrl+y")
}

// TestKeyPrecedence walks every level of the documented precedence order and
// every row of the Tasks key-conflict table.
func TestKeyPrecedence(t *testing.T) {
	tests := []struct {
		name  string
		level int
		key   tea.KeyPressMsg
		setup func(m *Model, p *routerTestPlugin)
		check func(t *testing.T, m *Model, p *routerTestPlugin)
	}{
		{
			name:  "level 1: an open sidecar modal beats a claiming plugin",
			level: 1,
			key:   tea.KeyPressMsg{Code: '@', Text: "@"},
			setup: func(m *Model, p *routerTestPlugin) {
				p.claims["@"] = true
				m.showProjectSwitcher = true
				m.initProjectSwitcher()
				m.updateContext()
			},
			check: func(t *testing.T, m *Model, p *routerTestPlugin) {
				if len(p.keys) != 0 {
					t.Fatalf("plugin saw %v while a sidecar modal was open", p.keys)
				}
				// The modal's own `@` closes it. What matters is that the key
				// was the modal's to interpret, not the claiming plugin's.
				if m.showProjectSwitcher {
					t.Fatal("the modal did not handle its own key")
				}
			},
		},
		{
			name:  "level 1: an open sidecar modal beats a blocking overlay",
			level: 1,
			key:   tea.KeyPressMsg{Code: 'x', Text: "x"},
			setup: func(m *Model, p *routerTestPlugin) {
				p.blocks = true
				m.showProjectSwitcher = true
				m.initProjectSwitcher()
				m.updateContext()
			},
			check: func(t *testing.T, m *Model, p *routerTestPlugin) {
				if len(p.keys) != 0 {
					t.Fatalf("plugin saw %v under an open modal", p.keys)
				}
				if got := m.projectSwitcherInput.Value(); got != "x" {
					t.Fatalf("modal filter = %q, want the typed key", got)
				}
			},
		},
		{
			name:  "level 2: a blocking overlay keeps sidecar globals out",
			level: 2,
			key:   tea.KeyPressMsg{Code: '#', Text: "#"},
			setup: func(m *Model, p *routerTestPlugin) {
				p.context = "tasks-modal"
				p.blocks = true
				m.updateContext()
			},
			check: func(t *testing.T, m *Model, p *routerTestPlugin) {
				if m.showThemeSwitcher {
					t.Fatal("# opened the theme switcher over a plugin overlay")
				}
				wantOnlyPluginKey(t, p, "#")
			},
		},
		{
			name:  "level 2: a blocking overlay keeps q for itself",
			level: 2,
			key:   tea.KeyPressMsg{Code: 'q', Text: "q"},
			setup: func(m *Model, p *routerTestPlugin) {
				p.context = "tasks-modal"
				p.blocks = true
				p.rootQuit = false
				m.updateContext()
			},
			check: func(t *testing.T, m *Model, p *routerTestPlugin) {
				if m.showQuitConfirm {
					t.Fatal("q quit sidecar out from under a plugin overlay")
				}
				wantOnlyPluginKey(t, p, "q")
			},
		},
		{
			name:  "level 2: ctrl+c still reaches the host from an overlay",
			level: 2,
			key:   tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl},
			setup: func(m *Model, p *routerTestPlugin) {
				p.blocks = true
				p.textInput = true
				m.updateContext()
			},
			check: func(t *testing.T, m *Model, p *routerTestPlugin) {
				if !m.showQuitConfirm {
					t.Fatal("ctrl+c must always reach sidecar's quit flow")
				}
				if len(p.keys) != 0 {
					t.Fatalf("plugin saw %v; ctrl+c belongs to the host", p.keys)
				}
			},
		},
		{
			name:  "level 3: @ goes to the plugin's context picker",
			level: 3,
			key:   tea.KeyPressMsg{Code: '@', Text: "@"},
			setup: func(m *Model, p *routerTestPlugin) { p.claims["@"] = true },
			check: func(t *testing.T, m *Model, p *routerTestPlugin) {
				if m.showProjectSwitcher {
					t.Fatal("@ opened sidecar's project switcher; Tasks' context picker wins")
				}
				wantOnlyPluginKey(t, p, "@")
			},
		},
		{
			// The mechanism, not the Tasks mapping: Tasks gave the number row
			// back to sidecar's tab switcher (see
			// TestNumberKeysSwitchSidecarTabsFromTheTasksTab), but a plugin
			// that does claim a number must still win it.
			name:  "level 3: a claimed number key beats sidecar's tab switcher",
			level: 3,
			key:   tea.KeyPressMsg{Code: '3', Text: "3"},
			setup: func(m *Model, p *routerTestPlugin) { p.claims["3"] = true },
			check: func(t *testing.T, m *Model, p *routerTestPlugin) {
				if m.activePlugin != 0 {
					t.Fatalf("activePlugin = %d; a claimed number key must not switch tabs", m.activePlugin)
				}
				wantOnlyPluginKey(t, p, "3")
			},
		},
		{
			name:  "level 3: tab belongs to the plugin",
			level: 3,
			key:   tea.KeyPressMsg{Code: tea.KeyTab},
			setup: func(m *Model, p *routerTestPlugin) { p.claims["tab"] = true },
			check: func(t *testing.T, m *Model, p *routerTestPlugin) {
				wantOnlyPluginKey(t, p, "tab")
			},
		},
		{
			name:  "level 3: M reaches the plugin's model selector",
			level: 3,
			key:   tea.KeyPressMsg{Code: 'M', Text: "M"},
			setup: func(m *Model, p *routerTestPlugin) { p.claims["M"] = true },
			check: func(t *testing.T, m *Model, p *routerTestPlugin) {
				wantOnlyPluginKey(t, p, "M")
			},
		},
		{
			name:  "level 3: A reaches the plugin's agent activity",
			level: 3,
			key:   tea.KeyPressMsg{Code: 'A', Text: "A"},
			setup: func(m *Model, p *routerTestPlugin) { p.claims["A"] = true },
			check: func(t *testing.T, m *Model, p *routerTestPlugin) {
				wantOnlyPluginKey(t, p, "A")
			},
		},
		{
			name:  "level 3: a claimed K keeps the Overview closed",
			level: 3,
			key:   tea.KeyPressMsg{Code: 'K', Text: "K"},
			setup: func(m *Model, p *routerTestPlugin) { p.claims["K"] = true },
			check: func(t *testing.T, m *Model, p *routerTestPlugin) {
				if m.inGlobalScope() {
					t.Fatal("K opened the Overview over a plugin that claims it")
				}
				wantOnlyPluginKey(t, p, "K")
			},
		},
		{
			name:  "level 3: an unavailable command does not claim its key",
			level: 3,
			key:   tea.KeyPressMsg{Code: '@', Text: "@"},
			setup: func(m *Model, p *routerTestPlugin) { p.claims["@"] = false },
			check: func(t *testing.T, m *Model, p *routerTestPlugin) {
				if !m.showProjectSwitcher {
					t.Fatal("an unclaimed @ must fall through to sidecar's project switcher")
				}
				if len(p.keys) != 0 {
					t.Fatalf("plugin saw %v for a key it did not claim", p.keys)
				}
			},
		},
		{
			name:  "level 3 is not consulted for ctrl+c even if claimed",
			level: 3,
			key:   tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl},
			setup: func(m *Model, p *routerTestPlugin) { p.claims["ctrl+c"] = true },
			check: func(t *testing.T, m *Model, p *routerTestPlugin) {
				if !m.showQuitConfirm {
					t.Fatal("a plugin must not be able to claim ctrl+c")
				}
			},
		},
		{
			name:  "level 4: ? opens sidecar's merged help",
			level: 4,
			key:   tea.KeyPressMsg{Code: '?', Text: "?"},
			setup: func(m *Model, p *routerTestPlugin) {},
			check: func(t *testing.T, m *Model, p *routerTestPlugin) {
				if !m.showPalette {
					t.Fatal("? must open sidecar's merged contextual help")
				}
				if len(p.keys) != 0 {
					t.Fatalf("plugin saw %v; ? belongs to the host", p.keys)
				}
			},
		},
		{
			name:  "level 4: q runs sidecar's quit flow from a plugin root context",
			level: 4,
			key:   tea.KeyPressMsg{Code: 'q', Text: "q"},
			setup: func(m *Model, p *routerTestPlugin) {},
			check: func(t *testing.T, m *Model, p *routerTestPlugin) {
				if !m.showQuitConfirm {
					t.Fatal("q must reach sidecar's quit flow; an embedded plugin never exits the app")
				}
				if len(p.keys) != 0 {
					t.Fatalf("plugin saw %v; q belongs to the host here", p.keys)
				}
			},
		},
		{
			name:  "level 4: backtick still switches sidecar tabs",
			level: 4,
			key:   tea.KeyPressMsg{Code: '`', Text: "`"},
			setup: func(m *Model, p *routerTestPlugin) {},
			check: func(t *testing.T, m *Model, p *routerTestPlugin) {
				if len(p.keys) != 0 {
					t.Fatalf("plugin saw %v; backtick cycles sidecar tabs", p.keys)
				}
			},
		},
		{
			name:  "level 5: unbound input is forwarded to the plugin",
			level: 5,
			key:   tea.KeyPressMsg{Code: 'y', Text: "y"},
			setup: func(m *Model, p *routerTestPlugin) {},
			check: func(t *testing.T, m *Model, p *routerTestPlugin) {
				wantOnlyPluginKey(t, p, "y")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := newRouterPlugin()
			m := routerTestModel(t, p)
			test.setup(&m, p)
			m.handleKeyMsg(test.key)
			test.check(t, &m, p)
		})
	}
}

func wantOnlyPluginKey(t *testing.T, p *routerTestPlugin, key string) {
	t.Helper()
	if len(p.keys) != 1 || p.keys[0] != key {
		t.Fatalf("plugin received %v, want exactly [%s]", p.keys, key)
	}
}

// A plugin in a text-input context must receive every printable key, including
// the ones sidecar binds globally. Anything sidecar swallows here is a
// character the user typed and never got.
func TestTextInputKeysAreNeverStolen(t *testing.T) {
	keys := []struct {
		name string
		msg  tea.KeyPressMsg
		want string
	}{
		{"space", tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}, "space"},
		{"digit", tea.KeyPressMsg{Code: '1', Text: "1"}, "1"},
		{"at sign", tea.KeyPressMsg{Code: '@', Text: "@"}, "@"},
		{"question mark", tea.KeyPressMsg{Code: '?', Text: "?"}, "?"},
		{"quit letter", tea.KeyPressMsg{Code: 'q', Text: "q"}, "q"},
		{"hash", tea.KeyPressMsg{Code: '#', Text: "#"}, "#"},
		{"backtick", tea.KeyPressMsg{Code: '`', Text: "`"}, "`"},
		{"unicode", tea.KeyPressMsg{Code: 'é', Text: "é"}, "é"},
		{"cjk", tea.KeyPressMsg{Code: '日', Text: "日"}, "日"},
		{"emoji", tea.KeyPressMsg{Code: '🙂', Text: "🙂"}, "🙂"},
	}

	for _, key := range keys {
		t.Run(key.name, func(t *testing.T) {
			p := newRouterPlugin()
			p.textInput = true
			p.context = "tasks-prompt"
			m := routerTestModel(t, p)

			m.handleKeyMsg(key.msg)
			wantOnlyPluginKey(t, p, key.want)
		})
	}
}

// A bracketed paste is one message carrying many runes; it must reach the
// plugin whole.
func TestPasteReachesATextInputPlugin(t *testing.T) {
	p := newRouterPlugin()
	p.textInput = true
	p.context = "tasks-prompt"
	m := routerTestModel(t, p)

	m.handlePaste(tea.PasteMsg{Content: "schedule 1 @home ? #tag"})

	if len(p.keys) != 1 || p.keys[0] != "paste:schedule 1 @home ? #tag" {
		t.Fatalf("plugin received %v, want the whole pasted string", p.keys)
	}
}

// A paste belongs to the focused tab alone. Broadcasting it put the same text
// into every background plugin's text input, so pasting into a workspace
// terminal also typed into the Tasks prompt.
func TestPasteReachesOnlyTheActivePlugin(t *testing.T) {
	active := newRouterPlugin()
	active.id = "workspace"
	active.context = "workspace-interactive"
	background := newRouterPlugin()
	background.textInput = true
	background.context = "tasks-prompt"

	reg := plugin.NewRegistry(nil)
	for _, p := range []*routerTestPlugin{active, background} {
		if err := reg.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	m := routerTestModel(t, active)
	m.registry = reg
	m.activePlugin = 0
	m.updateContext()

	m.handlePaste(tea.PasteMsg{Content: "ls -la"})

	if len(active.keys) != 1 || active.keys[0] != "paste:ls -la" {
		t.Fatalf("active plugin received %v, want the paste", active.keys)
	}
	if len(background.keys) != 0 {
		t.Fatalf("background plugin received %v, want nothing", background.keys)
	}
}

// Multi-rune input arriving as separate key presses accumulates in order.
func TestMultiRuneTypingReachesThePlugin(t *testing.T) {
	p := newRouterPlugin()
	p.textInput = true
	p.context = "tasks-filter"
	m := routerTestModel(t, p)

	for _, r := range "1 @q?" {
		m.handleKeyMsg(tea.KeyPressMsg{Code: r, Text: string(r)})
	}

	want := []string{"1", "space", "@", "q", "?"}
	if len(p.keys) != len(want) {
		t.Fatalf("plugin received %v, want %v", p.keys, want)
	}
	for i := range want {
		if p.keys[i] != want[i] {
			t.Fatalf("plugin received %v, want %v", p.keys, want)
		}
	}
}

// The six plugins that do not implement plugin.KeyRouter must behave exactly as
// they did before contextual precedence existed: sidecar's globals fire first
// and unbound keys fall through.
func TestPluginsWithoutAKeyRouterAreUnaffected(t *testing.T) {
	tests := []struct {
		name  string
		key   tea.KeyPressMsg
		check func(t *testing.T, m *Model, p *nativeTestPlugin)
	}{
		{
			name: "@ opens the project switcher",
			key:  tea.KeyPressMsg{Code: '@', Text: "@"},
			check: func(t *testing.T, m *Model, p *nativeTestPlugin) {
				if !m.showProjectSwitcher {
					t.Fatal("@ no longer opens the project switcher")
				}
			},
		},
		{
			name: "? opens the palette",
			key:  tea.KeyPressMsg{Code: '?', Text: "?"},
			check: func(t *testing.T, m *Model, p *nativeTestPlugin) {
				if !m.showPalette {
					t.Fatal("? no longer opens the palette")
				}
			},
		},
		{
			name: "# opens the theme switcher",
			key:  tea.KeyPressMsg{Code: '#', Text: "#"},
			check: func(t *testing.T, m *Model, p *nativeTestPlugin) {
				if !m.showThemeSwitcher {
					t.Fatal("# no longer opens the theme switcher")
				}
			},
		},
		{
			name: "! opens diagnostics",
			key:  tea.KeyPressMsg{Code: '!', Text: "!"},
			check: func(t *testing.T, m *Model, p *nativeTestPlugin) {
				if !m.showDiagnostics {
					t.Fatal("! no longer opens diagnostics")
				}
			},
		},
		{
			name: "unbound keys still reach the plugin",
			key:  tea.KeyPressMsg{Code: 'z', Text: "z"},
			check: func(t *testing.T, m *Model, p *nativeTestPlugin) {
				if len(p.seen) == 0 {
					t.Fatal("unbound key no longer reaches the plugin")
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := &nativeTestPlugin{}
			m := routerTestModel(t, p)
			m.handleKeyMsg(test.key)
			test.check(t, &m, p)
		})
	}
}

// A palette entry for a plugin command runs the plugin's handler even though
// the command was never registered with a keymap handler. Without this the
// merged help surface would list Tasks commands that do nothing when chosen.
func TestPaletteRunsPluginCommandHandlers(t *testing.T) {
	p := newRouterPlugin()
	var ran []string
	p.commands = []plugin.Command{
		{
			ID:      "toggle-model",
			Name:    "Cycle",
			Context: "tasks-detail",
			Handler: func() tea.Cmd { ran = append(ran, "detail"); return nil },
		},
		{
			ID:      "toggle-model",
			Name:    "Cycle",
			Context: "tasks-list",
			Handler: func() tea.Cmd { ran = append(ran, "list"); return nil },
		},
	}
	m := routerTestModel(t, p)
	m.showPalette = true

	updated, _ := m.Update(palette.CommandSelectedMsg{CommandID: "toggle-model", Context: "tasks-list"})
	m = updated.(Model)

	if len(ran) != 1 || ran[0] != "list" {
		t.Fatalf("palette ran %v, want the handler declared for the selected context", ran)
	}
	if m.showPalette {
		t.Fatal("palette stayed open after running a command")
	}
}

// The Workspaces list and leftover preview chrome no longer take "i", so
// the palette and help advertise it as find-TD-task again.
func TestTheIssueLookupAdvertisesIOnWorkspaces(t *testing.T) {
	for _, context := range []string{"workspace-list", "workspace-preview", "global-workspaces"} {
		t.Run(context, func(t *testing.T) {
			p := newRouterPlugin()
			m := routerTestModel(t, p)
			keymap.RegisterDefaults(m.keymap)
			m.activeContext = context

			var entry palette.PaletteEntry
			for _, e := range palette.BuildEntries(m.keymap, m.surfacePlugins(), context, "global") {
				if e.CommandID == "open-issue" {
					entry = e
				}
			}
			if entry.CommandID == "" {
				t.Fatal("the palette does not offer the issue lookup at all")
			}
			if entry.Key != "i" {
				t.Fatalf("the palette advertises %q for the issue lookup, want i", entry.Key)
			}

			var help strings.Builder
			m.renderBindingSection(&help, "global")
			var found bool
			for _, line := range strings.Split(ansi.Strip(help.String()), "\n") {
				if !strings.Contains(line, formatCommandName("open-issue")) {
					continue
				}
				found = true
				if !strings.Contains(line, "i") {
					t.Fatalf("help does not advertise i for the issue lookup: %q", line)
				}
			}
			if !found {
				t.Fatal("help does not list the issue lookup")
			}

			m.showPalette = true
			updated, _ := m.Update(palette.CommandSelectedMsg{CommandID: "open-issue", Context: "global"})
			if !updated.(Model).showIssueInput {
				t.Fatal("the palette could not open the issue lookup")
			}
		})
	}
}

// A command the palette cannot resolve must not panic or run something else.
func TestPaletteIgnoresUnknownCommands(t *testing.T) {
	p := newRouterPlugin()
	m := routerTestModel(t, p)
	m.showPalette = true

	updated, _ := m.Update(palette.CommandSelectedMsg{CommandID: "no-such-command", Context: "tasks-list"})
	m = updated.(Model)

	if m.showPalette {
		t.Fatal("palette stayed open")
	}
}

// Global host commands selected in the palette execute their respective modal or action.
func TestPaletteRunsHostCommands(t *testing.T) {
	tests := []struct {
		commandID string
		assert    func(t *testing.T, m Model)
	}{
		{
			commandID: "switch-project",
			assert: func(t *testing.T, m Model) {
				if !m.showProjectSwitcher {
					t.Fatal("expected showProjectSwitcher to be true")
				}
			},
		},
		{
			commandID: "switch-theme",
			assert: func(t *testing.T, m Model) {
				if !m.showThemeSwitcher {
					t.Fatal("expected showThemeSwitcher to be true")
				}
			},
		},
		{
			commandID: "quit",
			assert: func(t *testing.T, m Model) {
				if !m.showQuitConfirm {
					t.Fatal("expected showQuitConfirm to be true")
				}
			},
		},
		{
			commandID: "toggle-diagnostics",
			assert: func(t *testing.T, m Model) {
				if !m.showDiagnostics {
					t.Fatal("expected showDiagnostics to be true")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.commandID, func(t *testing.T) {
			p := newRouterPlugin()
			m := routerTestModel(t, p)
			m.showPalette = true

			updated, _ := m.Update(palette.CommandSelectedMsg{CommandID: tt.commandID, Context: "global"})
			var after Model
			if mPtr, ok := updated.(*Model); ok {
				after = *mPtr
			} else {
				after = updated.(Model)
			}
			if after.showPalette {
				t.Fatal("palette stayed open")
			}
			tt.assert(t, after)
		})
	}
}

// A plugin command with a Key binding executes via fallback to the active plugin.
func TestPaletteContextualKeyFallback(t *testing.T) {
	p := newRouterPlugin()
	m := routerTestModel(t, p)
	m.showPalette = true

	updated, _ := m.Update(palette.CommandSelectedMsg{CommandID: "custom-action", Context: "test-plugin", Key: "x"})
	var after Model
	if mPtr, ok := updated.(*Model); ok {
		after = *mPtr
	} else {
		after = updated.(Model)
	}
	if after.showPalette {
		t.Fatal("palette stayed open")
	}
	if len(p.keys) != 1 {
		t.Fatalf("expected 1 forwarded key, got %d", len(p.keys))
	}
	if p.keys[0] != "x" {
		t.Fatalf("forwarded key = %q, want 'x'", p.keys[0])
	}
}

// TestTheHostRefusesItsReservedKeysWhateverARouterClaims is the guarantee the
// precedence comment used to only assert.
//
// The plan's non-negotiables — "Sidecar quit flow wins; embedded Tasks never
// exits the app" and "`?` → sidecar merged help" — were being kept by the Tasks
// plugin filtering those keys on its own side. That is defence in the wrong
// layer: a plugin is exactly the thing that cannot be trusted to decide whether
// the user can quit. This drives a deliberately misbehaving router that claims
// all three.
func TestTheHostRefusesItsReservedKeysWhateverARouterClaims(t *testing.T) {
	tests := []struct {
		name  string
		key   tea.KeyPressMsg
		check func(t *testing.T, m *Model)
	}{
		{
			name: "ctrl+c",
			key:  tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl},
			check: func(t *testing.T, m *Model) {
				if !m.showQuitConfirm {
					t.Fatal("a claiming plugin swallowed ctrl+c")
				}
			},
		},
		{
			name: "q",
			key:  tea.KeyPressMsg{Code: 'q', Text: "q"},
			check: func(t *testing.T, m *Model) {
				if !m.showQuitConfirm {
					t.Fatal("a claiming plugin swallowed sidecar's quit flow")
				}
			},
		},
		{
			name: "question mark",
			key:  tea.KeyPressMsg{Code: '?', Text: "?"},
			check: func(t *testing.T, m *Model) {
				if !m.showPalette {
					t.Fatal("a claiming plugin swallowed the merged help")
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// A router that claims every key the host reserves, from an
			// ordinary (non-overlay) context where it has no business doing so.
			p := newRouterPlugin("ctrl+c", "q", "?")
			m := routerTestModel(t, p)

			m.handleKeyMsg(test.key)

			test.check(t, &m)
			if len(p.keys) != 0 {
				t.Fatalf("plugin received %v; the host's reserved keys are not claimable", p.keys)
			}
		})
	}
}

// Every key the host reserves must actually be one the host handles. A reserved
// key nothing acts on would be a key silently dropped instead of forwarded.
func TestEveryReservedKeyIsAKeyTheHostHandles(t *testing.T) {
	for key := range keymap.HostReservedKeys {
		if !keymap.GlobalKeys[key] {
			t.Errorf("%q is reserved from plugins but is not a sidecar global key", key)
		}
	}
}

func hasKeyMsg(msgs []tea.Msg, want tea.KeyPressMsg) bool {
	for _, m := range msgs {
		if k, ok := m.(tea.KeyPressMsg); ok && k.String() == want.String() {
			return true
		}
	}
	return false
}

// TestGlobalKeysAreTheOnesTheHostActuallyHandles pins keymap.GlobalKeys against
// the real key handler, so the list a plugin reasons about cannot drift from
// the switch statement it describes.
func TestGlobalKeysAreTheOnesTheHostActuallyHandles(t *testing.T) {
	press := func(t *testing.T, key tea.KeyPressMsg) *nativeTestPlugin {
		t.Helper()
		p := &nativeTestPlugin{}
		m := routerTestModel(t, p)
		m.handleKeyMsg(key)
		return p
	}

	for key := range keymap.GlobalKeys {
		if key == "q" {
			// `q` is the one global whose handling is context-dependent by
			// design: it opens the quit flow from a root context and is
			// forwarded from a sub-view, which is what quitKeyExits decides.
			// The reserved-keys test above covers it directly.
			continue
		}
		t.Run("global/"+key, func(t *testing.T) {
			msg := tea.KeyPressMsg{Code: rune(key[0]), Text: key}
			if key == "ctrl+c" {
				msg = tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
			}
			if p := press(t, msg); hasKeyMsg(p.seen, msg) {
				t.Fatalf("%q is listed as a sidecar global but was forwarded to the plugin", key)
			}
		})
	}

	// The other half of the claim: keys not on the list reach the plugin. `r`
	// is the interesting one — it is a sidecar binding, but it yields to any
	// plugin context isGlobalRefreshContext does not name, so a plugin may take
	// it without contradicting the host.
	for _, key := range []string{"r", "j", "y", "M", "A", "z"} {
		t.Run("forwarded/"+key, func(t *testing.T) {
			if keymap.GlobalKeys[key] {
				t.Fatalf("test premise: %q is on the global list", key)
			}
			p := press(t, tea.KeyPressMsg{Code: rune(key[0]), Text: key})
			if !hasKeyMsg(p.seen, tea.KeyPressMsg{Code: rune(key[0]), Text: key}) {
				t.Fatalf("%q is not a sidecar global but never reached the plugin", key)
			}
		})
	}
}

// TestAUserOverrideOutranksAPluginClaim makes the escape hatch plan § 1.4
// documents actually exist: "change the mapping through Sidecar's keymap
// override rather than forking the Tasks registry".
//
// Level 3 runs before keymap.Handle, and user overrides were only consulted
// inside it, so for precisely the keys a plugin claims — the keys anyone would
// want to remap — the override was unreachable.
func TestAUserOverrideOutranksAPluginClaim(t *testing.T) {
	p := newRouterPlugin("1")
	m := routerTestModel(t, p)

	var ran int
	m.keymap.RegisterCommand(keymap.Command{
		ID:      "switch-tab-1",
		Handler: func() tea.Cmd { ran++; return nil },
	})
	m.keymap.SetUserOverride("1", "switch-tab-1")

	m.handleKeyMsg(tea.KeyPressMsg{Code: '1', Text: "1"})

	if ran != 1 {
		t.Fatalf("the user override ran %d times, want 1", ran)
	}
	if len(p.keys) != 0 {
		t.Fatalf("plugin received %v despite a user override for the key", p.keys)
	}
}

// An override naming a command nothing registered is not a claim on the key:
// the plugin still gets it, rather than the keystroke vanishing.
func TestAnUnresolvableOverrideLeavesThePluginClaimAlone(t *testing.T) {
	p := newRouterPlugin("1")
	m := routerTestModel(t, p)
	m.keymap.SetUserOverride("1", "no-such-command")

	m.handleKeyMsg(tea.KeyPressMsg{Code: '1', Text: "1"})

	wantOnlyPluginKey(t, p, "1")
}

// Consulting overrides at level 3 must not reorder the ladder for anyone else:
// a plugin with no key router still meets sidecar's global switch first.
func TestAUserOverrideDoesNotJumpAheadOfTheGlobalSwitch(t *testing.T) {
	p := &nativeTestPlugin{}
	m := routerTestModel(t, p)

	var ran int
	m.keymap.RegisterCommand(keymap.Command{
		ID:      "surprise",
		Handler: func() tea.Cmd { ran++; return nil },
	})
	m.keymap.SetUserOverride("@", "surprise")

	m.handleKeyMsg(tea.KeyPressMsg{Code: '@', Text: "@"})

	if ran != 0 {
		t.Fatal("an override displaced a sidecar global for a plugin that claims nothing")
	}
	if !m.showProjectSwitcher {
		t.Fatal("@ no longer opens the project switcher")
	}
}

// tabCycleTestModel builds a two-tab model whose first tab is the key-routing
// plugin, with sidecar's real default bindings loaded. Bracket tab cycling is
// driven by those bindings, so a registry without them proves nothing.
func tabCycleTestModel(t *testing.T, p *routerTestPlugin) Model {
	t.Helper()
	m := routerTestModel(t, p)
	if err := m.registry.Register(&nativeTestPlugin{}); err != nil {
		t.Fatal(err)
	}
	keymap.RegisterDefaults(m.keymap)
	m.updateContext()
	return m
}

// TestNumberKeysSwitchSidecarTabsFromTheTasksTab is the revision the owner made
// after living with the shipped mapping: `1`-`6` selected a Tasks view inside
// the Tasks tab, and switching tabs by number is muscle memory everywhere else.
// Tasks no longer claims them (shadowableGlobals is `@` alone), so they reach
// sidecar's global switch like they do in every other tab.
func TestNumberKeysSwitchSidecarTabsFromTheTasksTab(t *testing.T) {
	// Claims exactly what the revised conflict table leaves to Tasks.
	p := newRouterPlugin("@", "tab", "M", "A", "left", "right")
	m := tabCycleTestModel(t, p)

	m.handleKeyMsg(tea.KeyPressMsg{Code: '2', Text: "2"})

	if m.activePlugin != 1 {
		t.Fatalf("activePlugin = %d after `2`, want 1: the number row switches sidecar tabs", m.activePlugin)
	}
	if len(p.keys) != 0 {
		t.Fatalf("plugin saw %v; the number row is sidecar's", p.keys)
	}
}

// `←`/`→` are what Tasks keeps for stepping between its own views, so sidecar
// must not intercept them in a Tasks context.
func TestArrowKeysReachTheTasksTab(t *testing.T) {
	p := newRouterPlugin("left", "right")
	m := tabCycleTestModel(t, p)

	m.handleKeyMsg(tea.KeyPressMsg{Code: tea.KeyRight})
	m.handleKeyMsg(tea.KeyPressMsg{Code: tea.KeyLeft})

	if m.activePlugin != 0 {
		t.Fatalf("activePlugin = %d; arrows must not move sidecar", m.activePlugin)
	}
	if len(p.keys) != 2 || p.keys[0] != "right" || p.keys[1] != "left" {
		t.Fatalf("plugin saw %v, want [right left]", p.keys)
	}
}

// `[`/`]` cycle sidecar tabs from every ordinary plugin context.
func TestBracketsCycleSidecarTabsAcrossPluginContexts(t *testing.T) {
	for _, context := range []string{"tasks-list", "file-browser-tree", "file-browser-preview", "workspace-preview", "git-diff", "conversations-main"} {
		t.Run(context, func(t *testing.T) {
			p := newRouterPlugin()
			p.context = context
			m := tabCycleTestModel(t, p)

			m.handleKeyMsg(tea.KeyPressMsg{Code: ']', Text: "]"})
			if m.activePlugin != 1 {
				t.Fatalf("activePlugin = %d after `]`, want 1", m.activePlugin)
			}
			if len(p.keys) != 0 {
				t.Fatalf("plugin saw %v; brackets cycle sidecar tabs in %s", p.keys, context)
			}

			// `[` steps the other way, from a fresh model: the tab `]` landed
			// on is a different plugin with a context of its own.
			back := newRouterPlugin()
			back.context = context
			bm := tabCycleTestModel(t, back)
			bm.handleKeyMsg(tea.KeyPressMsg{Code: '[', Text: "["})
			if bm.activePlugin != 1 {
				t.Fatalf("activePlugin = %d after `[`, want 1 (wrapped to the previous tab)", bm.activePlugin)
			}
			if len(back.keys) != 0 {
				t.Fatalf("plugin saw %v; brackets cycle sidecar tabs in %s", back.keys, context)
			}
		})
	}
}

// A bracket typed into a text input is a bracket. Level 2 forwards it before
// the global switch is reached.
func TestBracketsTypedIntoATasksTextInputReachTheTab(t *testing.T) {
	for _, context := range []string{"tasks-prompt", "tasks-filter", "tasks-form", "tasks-context-picker"} {
		t.Run(context, func(t *testing.T) {
			p := newRouterPlugin()
			p.context = context
			p.textInput = true
			m := tabCycleTestModel(t, p)

			m.handleKeyMsg(tea.KeyPressMsg{Code: '[', Text: "["})
			m.handleKeyMsg(tea.KeyPressMsg{Code: ']', Text: "]"})

			if m.activePlugin != 0 {
				t.Fatalf("activePlugin = %d; a typed bracket switched tabs in %s", m.activePlugin, context)
			}
			if len(p.keys) != 2 || p.keys[0] != "[" || p.keys[1] != "]" {
				t.Fatalf("plugin saw %v, want [\"[\" \"]\"] in %s", p.keys, context)
			}
		})
	}
}

// The same holds under a Tasks overlay (level 2's other half): a modal that
// blocks globals keeps the bracket.
func TestBracketsUnderATasksOverlayReachTheTab(t *testing.T) {
	p := newRouterPlugin()
	p.context = "tasks-modal"
	p.blocks = true
	m := tabCycleTestModel(t, p)

	m.handleKeyMsg(tea.KeyPressMsg{Code: '[', Text: "["})

	if m.activePlugin != 0 {
		t.Fatal("a bracket switched tabs from under a Tasks overlay")
	}
	wantOnlyPluginKey(t, p, "[")
}

func TestBracketsUnderAPluginOverlayWithoutKeyRouterReachThePlugin(t *testing.T) {
	p := &blockerOnlyTestPlugin{blocks: true}
	m := routerTestModel(t, p)

	m.handleKeyMsg(tea.KeyPressMsg{Code: ']', Text: "]"})

	if m.activePlugin != 0 {
		t.Fatal("a bracket switched tabs from under a plugin-owned overlay")
	}
	if len(p.seen) != 1 {
		t.Fatalf("plugin saw %d messages, want the bracket", len(p.seen))
	}
}

// i is Sidecar's find-TD-task shortcut on the Workspaces list and leftover
// preview chrome. It must not be swallowed as "enter interactive".
func TestIssueLookupOpensFromTheProjectWorkspacesList(t *testing.T) {
	for _, context := range []string{"workspace-list", "workspace-preview"} {
		t.Run(context, func(t *testing.T) {
			p := newRouterPlugin()
			p.id = "workspace-manager"
			p.context = context
			m := routerTestModel(t, p)
			keymap.RegisterDefaults(m.keymap)
			m.updateContext()

			m.handleKeyMsg(tea.KeyPressMsg{Code: 'i', Text: "i"})
			if !m.showIssueInput {
				t.Fatal("\"i\" did not open the issue modal from the Workspaces list")
			}
		})
	}
}

func TestIssueModalStillOpensWhereNoContextBindsTheKey(t *testing.T) {
	p := newRouterPlugin()
	p.context = "tasks-list"
	m := routerTestModel(t, p)
	keymap.RegisterDefaults(m.keymap)
	m.updateContext()

	m.handleKeyMsg(tea.KeyPressMsg{Code: 'i', Text: "i"})
	if !m.showIssueInput {
		t.Fatal("\"i\" no longer opens the issue modal")
	}
}

// A pane being typed into gets a footer of exits only. The tab numbers and the
// help key are going to the pane, so advertising them would be a lie.
func TestTypingFooterAdvertisesOnlyTheWaysOut(t *testing.T) {
	p := newRouterPlugin()
	p.id = "workspace-manager"
	p.context = "workspace-interactive"
	p.commands = []plugin.Command{
		{ID: "exit-interactive", Name: "Exit", Description: "Exit interactive mode", Context: "workspace-interactive", Priority: 1},
	}
	m := routerTestModel(t, p)
	keymap.RegisterDefaults(m.keymap)
	m.keymap.RegisterPluginBinding("ctrl+\\", "exit-interactive", "workspace-interactive")
	m.updateContext()

	hints := m.footerHints()
	if len(hints) == 0 {
		t.Fatal("the interactive footer offers no way out at all")
	}
	for _, hint := range hints {
		if hint.label == "help" || hint.label == "plugins" || hint.label == "tabs" {
			t.Fatalf("the interactive footer advertises %q, a key that goes to the pane: %#v", hint.label, hints)
		}
	}
}
