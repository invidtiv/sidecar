package workspace

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/app"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/configui"
	"github.com/marcus/sidecar/internal/plugin"
)

// emptyListPlugin is a Workspaces list with nothing in it, which is the only
// state the contextual prompt can appear in.
func emptyListPlugin(t *testing.T, cfg *config.Config, tmux bool) *Plugin {
	t.Helper()
	previous := tmuxAvailable
	tmuxAvailable = func() bool { return tmux }
	t.Cleanup(func() { tmuxAvailable = previous })

	p := New()
	p.ctx = &plugin.Context{WorkDir: t.TempDir(), ProjectRoot: t.TempDir(), Config: cfg}
	p.width, p.height = 140, 40
	p.viewMode = ViewModeList
	p.sidebarVisible = true
	p.sidebarWidth = 34
	p.activePane = PaneSidebar
	return p
}

func configWithProjects() *config.Config {
	cfg := config.Default()
	cfg.Projects.List = []config.ProjectConfig{{Name: "one", Path: "/tmp/one"}}
	return cfg
}

// With a project configured and tmux present, an empty list is not a setup
// failure: the user simply has no workspaces yet, and n is the answer. Hijacking
// that into a Configuration route would send them away from the thing they came
// here to do.
func TestEmptyWorkspacesKeepsCreateAdviceWhenNothingIsMissing(t *testing.T) {
	p := emptyListPlugin(t, configWithProjects(), true)

	view := p.renderSidebarContent(34, 20)
	if !strings.Contains(view, "Press 'n' to create one") {
		t.Fatalf("empty list lost its create advice:\n%s", view)
	}
	if strings.Contains(view, "Sidecar Setup") {
		t.Fatalf("empty list offered Setup with nothing missing:\n%s", view)
	}
	if p.setupPromptActive() {
		t.Fatal("setupPromptActive with nothing missing")
	}
	if cmd := p.openSetupCmd(); cmd != nil {
		t.Fatal("openSetupCmd produced a command with nothing missing")
	}
}

// With no project configured, "press n" is advice that cannot work, so the
// empty state offers the one route that changes it — and says which key.
func TestEmptyWorkspacesOffersSetupWhenNoProjectIsConfigured(t *testing.T) {
	cfg := config.Default()
	cfg.Projects.List = nil
	p := emptyListPlugin(t, cfg, true)

	view := p.renderSidebarContent(34, 20)
	if !strings.Contains(view, "Sidecar Setup") || !strings.Contains(view, "Enter") {
		t.Fatalf("blocked empty state missing the contextual action:\n%s", view)
	}
	if strings.Contains(view, "Press 'n' to create one") {
		t.Fatalf("blocked empty state kept advice that cannot work:\n%s", view)
	}
	if !p.setupPromptActive() {
		t.Fatal("setupPromptActive false with no project configured")
	}

	msg := p.openSetupCmd()()
	request, ok := msg.(app.OpenConfigurationMsg)
	if !ok {
		t.Fatalf("openSetupCmd produced %T, want app.OpenConfigurationMsg", msg)
	}
	if request.Page != configui.PageSetup || !request.AddProject {
		t.Fatalf("request = %+v; want Setup with AddProject", request)
	}
}

// Without tmux no shell can be created at all, whatever the project list says.
// That is a repair, not a project to add.
func TestEmptyWorkspacesOffersSetupWhenTmuxIsMissing(t *testing.T) {
	p := emptyListPlugin(t, configWithProjects(), false)

	if !p.setupPromptActive() {
		t.Fatal("setupPromptActive false with tmux missing")
	}
	view := p.renderSidebarContent(34, 20)
	if !strings.Contains(view, "tmux") {
		t.Fatalf("blocked empty state did not name tmux:\n%s", view)
	}
	request := p.openSetupCmd()().(app.OpenConfigurationMsg)
	if request.Page != configui.PageSetup || request.AddProject {
		t.Fatalf("request = %+v; want Setup without the Add Project route", request)
	}
}

// Enter is the advertised key, and it only acts while the prompt is the thing
// on screen.
func TestEnterOpensConfigurationOnlyFromTheBlockedEmptyState(t *testing.T) {
	cfg := config.Default()
	cfg.Projects.List = nil
	p := emptyListPlugin(t, cfg, true)

	cmd := p.handleListKeys(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter did nothing in the blocked empty state")
	}
	if _, ok := cmd().(app.OpenConfigurationMsg); !ok {
		t.Fatal("enter did not ask for Configuration")
	}

	ready := emptyListPlugin(t, configWithProjects(), true)
	if cmd := ready.handleListKeys(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil {
		if _, ok := cmd().(app.OpenConfigurationMsg); ok {
			t.Fatal("enter opened Configuration from an ordinary empty list")
		}
	}
}

// The pill is a real mouse target, not a picture of one.
func TestBlockedEmptyStateRegistersItsPillAsAHitRegion(t *testing.T) {
	cfg := config.Default()
	cfg.Projects.List = nil
	p := emptyListPlugin(t, cfg, true)

	p.renderSidebarContent(34, 20)
	var found bool
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if region.ID == regionOpenSetupButton {
			found = true
		}
	}
	if !found {
		t.Fatal("the Open Sidecar Setup pill has no hit region")
	}
}
