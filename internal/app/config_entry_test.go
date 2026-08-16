package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/configui"
)

// collectMsgs runs a command far enough to see what it produced, including the
// batched commands Init returns.
func collectMsgs(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	switch typed := msg.(type) {
	case tea.BatchMsg:
		var out []tea.Msg
		for _, sub := range typed {
			out = append(out, collectMsgs(sub)...)
		}
		return out
	case nil:
		return nil
	default:
		return []tea.Msg{msg}
	}
}

// `sidecar setup` promises the ordinary app with Configuration open. The
// destination has to travel from the command into the model and open through
// the same message every other entry uses, so escape returns to the surface the
// user would have had.
func TestStartupConfigPageOpensConfigurationOverTheNormalSurface(t *testing.T) {
	m, _ := scopeBaselineModel(t, "git")
	m.startupConfigPage = configui.PageSetup
	priorPlugin, priorScope := m.activePlugin, m.scope

	var request *OpenConfigurationMsg
	for _, msg := range collectMsgs(m.Init()) {
		if typed, ok := msg.(OpenConfigurationMsg); ok {
			request = &typed
		}
	}
	if request == nil {
		t.Fatal("Init did not ask for Configuration")
	}
	if request.Page != configui.PageSetup {
		t.Fatalf("startup destination = %q, want %q", request.Page, configui.PageSetup)
	}

	updated, _ := m.Update(*request)
	m = asAppModel(t, updated)
	if !m.configOpen() {
		t.Fatal("Configuration did not open")
	}
	if m.config.Page() != configui.PageSetup {
		t.Fatalf("Configuration opened on %q, want Setup", m.config.Page())
	}

	m = typeKey(t, m, "esc")
	if m.configOpen() {
		t.Fatal("esc did not close Configuration")
	}
	if m.activePlugin != priorPlugin || m.scope != priorScope {
		t.Fatal("esc did not restore the startup surface")
	}
}

// An empty state's request lands on the same handler, and esc from there
// returns to the surface that sent it.
func TestOpenConfigurationMsgFromAnEmptyStateReturnsOnEscape(t *testing.T) {
	m, _ := scopeBaselineModel(t, "workspaces")
	priorPlugin := m.activePlugin

	updated, _ := m.Update(OpenConfigurationMsg{Page: configui.PageSetup})
	m = asAppModel(t, updated)
	if !m.configOpen() || m.config.Page() != configui.PageSetup {
		t.Fatalf("Configuration did not open on Setup: open=%v page=%q", m.configOpen(), m.config.Page())
	}

	m = typeKey(t, m, "esc")
	if m.configOpen() {
		t.Fatal("esc did not close Configuration")
	}
	if m.activePlugin != priorPlugin {
		t.Fatal("esc did not return to the surface that opened Configuration")
	}
}

// An unknown destination is not a reason to refuse a launch: Configuration
// falls back to its own default.
func TestOpenConfigurationMsgFallsBackToTheDefaultPage(t *testing.T) {
	m, _ := scopeBaselineModel(t, "git")
	updated, _ := m.Update(OpenConfigurationMsg{Page: configui.PageID("not-a-page")})
	m = asAppModel(t, updated)
	if !m.configOpen() || m.config.Page() != configui.DefaultPage {
		t.Fatalf("unknown page did not fall back to the default: open=%v page=%q", m.configOpen(), m.config.Page())
	}
}

// The project switcher's no-projects state keeps ctrl+a and gains a route into
// Setup. Both have to be visible, or the second is a secret.
func TestProjectSwitcherNoProjectsOffersSetup(t *testing.T) {
	m, _ := scopeBaselineModel(t, "git")
	m.cfg = config.Default()
	m.cfg.Projects.List = nil
	m.overview = nil
	m.globalTasks = nil

	m.showProjectSwitcher = true
	m.initProjectSwitcher()
	m.updateContext()

	view := m.viewContent()
	for _, want := range []string{"No projects configured", "Sidecar Setup", "ctrl+a"} {
		if !strings.Contains(view, want) {
			t.Fatalf("switcher empty state missing %q:\n%s", want, view)
		}
	}

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = asAppModel(t, updated)
	if m.showProjectSwitcher {
		t.Fatal("enter left the switcher open")
	}
	var opened bool
	for _, msg := range collectMsgs(cmd) {
		if request, ok := msg.(OpenConfigurationMsg); ok && request.Page == configui.PageSetup {
			opened = true
		}
	}
	if !opened {
		t.Fatal("enter did not ask for Configuration on Setup")
	}
}

// The global Sessions placeholder offers the route only when a project is
// actually missing; with projects configured it stays a plain placeholder.
func TestGlobalWorkspacesPlaceholderGating(t *testing.T) {
	m, _ := scopeBaselineModel(t, "git")
	m.overview = nil
	m.scope, m.globalTab = ScopeGlobal, GlobalSessions

	if strings.Contains(m.renderGlobalWorkspacesPlaceholder(120, 20), "Open Sidecar Setup") {
		t.Fatal("placeholder offered Setup while projects are configured")
	}

	m.cfg.Projects.List = nil
	if !strings.Contains(m.renderGlobalWorkspacesPlaceholder(120, 20), "Open Sidecar Setup") {
		t.Fatal("placeholder did not offer Setup with no projects configured")
	}
}
