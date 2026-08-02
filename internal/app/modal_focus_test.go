package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/plugin"
)

// interactiveTestPlugin stands in for a plugin holding a live tmux pane: it
// reports an interactive focus context and claims typed keys.
type interactiveTestPlugin struct {
	nativeTestPlugin
	keys []tea.Msg
}

func (p *interactiveTestPlugin) FocusContext() string    { return "workspace-interactive" }
func (p *interactiveTestPlugin) ConsumesTextInput() bool { return true }
func (p *interactiveTestPlugin) Update(msg tea.Msg) (plugin.Plugin, tea.Cmd) {
	if _, ok := msg.(tea.KeyPressMsg); ok {
		p.keys = append(p.keys, msg)
	}
	return p, nil
}

func interactiveTestModel(t *testing.T, p *interactiveTestPlugin) Model {
	t.Helper()
	reg := plugin.NewRegistry(nil)
	if err := reg.Register(p); err != nil {
		t.Fatal(err)
	}
	return Model{
		registry:      reg,
		keymap:        keymap.NewRegistry(),
		activePlugin:  0,
		ui:            &UIState{},
		ready:         true,
		width:         100,
		height:        30,
		activeContext: "workspace-interactive",
		cfg: &config.Config{
			Projects: config.ProjectsConfig{
				List: []config.ProjectConfig{{Name: "alpha", Path: "/tmp/alpha"}},
			},
		},
	}
}

// A modal opened over a live interactive pane must take keyboard focus: typed
// keys go to the modal's filter, not to tmux.
func TestModalTakesKeysFromInteractivePane(t *testing.T) {
	p := &interactiveTestPlugin{}
	m := interactiveTestModel(t, p)

	m.showProjectSwitcher = true
	m.initProjectSwitcher()
	m.updateContext()

	if m.activeContext != "project-switcher" {
		t.Fatalf("activeContext = %q, want project-switcher", m.activeContext)
	}

	m.handleKeyMsg(tea.KeyPressMsg{Code: 'a', Text: "a"})

	if len(p.keys) != 0 {
		t.Fatalf("plugin received %d keys while modal open, want 0", len(p.keys))
	}
	if got := m.projectSwitcherInput.Value(); got != "a" {
		t.Fatalf("modal filter = %q, want %q", got, "a")
	}
}

// Plugin traffic underneath an open modal (interactive panes poll constantly)
// must not steal the context back from the modal.
func TestPluginMessagesDoNotStealModalContext(t *testing.T) {
	p := &interactiveTestPlugin{}
	m := interactiveTestModel(t, p)

	m.showProjectSwitcher = true
	m.initProjectSwitcher()
	m.updateContext()

	m.Update(ToastMsg{Message: "poll"})

	if m.activeContext != "project-switcher" {
		t.Fatalf("activeContext = %q after plugin message, want project-switcher", m.activeContext)
	}
}

// Dismissing a modal without switching projects returns focus to the
// interactive pane, which never left interactive mode.
func TestClosingModalRestoresInteractiveFocus(t *testing.T) {
	p := &interactiveTestPlugin{}
	m := interactiveTestModel(t, p)

	m.showProjectSwitcher = true
	m.initProjectSwitcher()
	m.updateContext()

	m.handleKeyMsg(tea.KeyPressMsg{Code: tea.KeyEsc})

	if m.showProjectSwitcher {
		t.Fatal("modal still open after Esc")
	}
	if m.activeContext != "workspace-interactive" {
		t.Fatalf("activeContext = %q after closing modal, want workspace-interactive", m.activeContext)
	}

	p.keys = nil
	m.handleKeyMsg(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if len(p.keys) != 1 {
		t.Fatalf("plugin received %d keys after modal closed, want 1", len(p.keys))
	}
}
