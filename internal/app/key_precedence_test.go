package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
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

	context   string
	blocks    bool
	textInput bool
	claims    map[string]bool
	rootQuit  bool

	keys     []string
	commands []plugin.Command
}

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

func (p *routerTestPlugin) ID() string           { return "tasks" }
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
			name:  "level 3: number keys select plugin views, not sidecar tabs",
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
				if m.overviewActive {
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
