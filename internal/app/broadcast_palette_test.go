package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/broadcastmodal"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/palette"
	"github.com/marcus/sidecar/internal/plugin"
)

// broadcastHandlerSpy is the project Workspaces command as a Handler. The
// Sessions palette must not run it: overview is not a plugin, so an unmatched
// context would otherwise fall back to this and open the this-project modal
// on the hidden project tab.
type broadcastHandlerSpy struct {
	nativeTestPlugin
	opened bool
}

func (p *broadcastHandlerSpy) ID() string { return "workspace-manager" }

func (p *broadcastHandlerSpy) FocusContext() string { return "workspace-list" }

func (p *broadcastHandlerSpy) Commands() []plugin.Command {
	return []plugin.Command{{
		ID: broadcastmodal.CommandID, Name: broadcastmodal.CommandName,
		Context: "workspace-list",
		Handler: func() tea.Cmd {
			p.opened = true
			return nil
		},
	}}
}

func TestSessionsPaletteBroadcastOpensOverviewNotProjectPlugin(t *testing.T) {
	isolateAppState(t)
	cfg := config.Default()
	features.Init(cfg)
	features.SetOverride(features.AgentControl.Name, true)
	t.Cleanup(func() { features.Init(config.Default()) })
	cfg.Projects.List = []config.ProjectConfig{{Name: "one", Path: "/tmp/one"}}

	registry := plugin.NewRegistry(nil)
	spy := &broadcastHandlerSpy{}
	if err := registry.Register(spy); err != nil {
		t.Fatal(err)
	}
	m := New(registry, keymap.NewRegistry(), cfg, "", "/tmp/one", "/tmp/one", "workspace-manager")
	if m.overview == nil {
		t.Fatal("overview missing")
	}
	m.intro.Active, m.intro.Done = false, true
	m.width, m.height, m.ready = 140, 40, true
	m.scope = ScopeGlobal
	m.globalTab = GlobalSessions
	m.updateContext()
	if !m.globalWorkspacesVisible() {
		t.Fatal("fixture is not showing Sessions")
	}

	updated, _ := m.Update(palette.CommandSelectedMsg{
		CommandID: broadcastmodal.CommandID,
		Context:   "global-workspaces",
		Key:       "B",
	})
	m = asAppModel(t, updated)
	if spy.opened {
		t.Fatal("Sessions palette Broadcast invoked the project plugin handler")
	}
	if !m.overview.BroadcastOpen() {
		t.Fatal("Sessions palette Broadcast did not open the Sessions modal")
	}
}
