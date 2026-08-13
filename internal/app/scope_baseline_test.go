package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/overview"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// Baseline characterization of app entry/exit, the header's tab row and the
// keys that cross the global/project boundary, recorded before the global
// Overview work introduces an explicit app scope and its own global tabs
// (docs/plans/active/global-overview-workspaces.md, slice 0).
//
// The entry/exit and switcher-routing cases below are unchanged from the
// baseline recording. The header and tab-routing cases moved with slice 1, which
// gave the global space its own tabs: Overview no longer paints the project's
// plugin tab row with nothing active, and cycling/numeric keys now move between
// the tabs of the space the user is in.
//
// Deliberately not duplicated here, because the behaviour already has a home:
//   - K / brand-click toggling — TestLogoClickAndKToggleOverview;
//   - exit keys and key/paste containment over an interactive plugin —
//     TestOverviewExitKeysWorkOverInteractivePlugin,
//     TestOverviewSwallowsUnhandledKeys, TestOverviewSwallowsPasteAndUnknownSequences;
//   - context restoration on exit — TestExitOverviewRestoresPluginContext;
//   - pinned/filtered switcher destinations, lazy activation and cursor parity —
//     TestOverviewPinnedFilteredAndActivationIsLazy,
//     TestProjectSwitcherLinkedWorktreeCursorFlagParity;
//   - header repo-name click and Overview switcher entry — TestRepoNameClick_*,
//     TestOverviewHeaderMouseOpensSwitcher;
//   - narrow header truncation with Overview open — TestCompactOverviewKeepsAppHeaderAndFooterAt72x30;
//   - validated cross-project navigation and its race handling — the
//     TestOverview*Navigation/Validation cases in overview_test.go;
//   - Overview selection, scrolling, activation and collection cadence —
//     internal/overview/model_test.go and visual_test.go.

// isolateAppState points sidecar state at a temp directory so tests that
// persist preferences cannot write the real state.json. Cleanup clears the
// remembered tab so a later New() does not restore another test's last switch.
func isolateAppState(t *testing.T) {
	t.Helper()
	if err := state.InitWithDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.SetLastGlobalTab("") })
}

// scopeBaselineModel builds an app model on four registered plugins with the
// Overview constructed but not yet entered.
func scopeBaselineModel(t *testing.T, active string) (Model, map[string]*navigationPlugin) {
	t.Helper()
	isolateAppState(t)
	return newScopeBaselineModel(t, active)
}

// newScopeBaselineModel is scopeBaselineModel without resetting persisted
// state, so a restart can be simulated by constructing a second model.
func newScopeBaselineModel(t *testing.T, active string) (Model, map[string]*navigationPlugin) {
	t.Helper()
	cfg := config.Default()
	features.Init(cfg)
	t.Cleanup(func() { features.Init(config.Default()) })
	cfg.Projects.List = []config.ProjectConfig{{Name: "one", Path: "/tmp/one"}, {Name: "two", Path: "/tmp/two"}}

	registry := plugin.NewRegistry(nil)
	plugins := map[string]*navigationPlugin{}
	for _, name := range []string{"files", "workspaces", "git", "notes"} {
		p := &navigationPlugin{id: name}
		if err := registry.Register(p); err != nil {
			t.Fatal(err)
		}
		plugins[name] = p
	}
	m := New(registry, keymap.NewRegistry(), cfg, "", "/tmp/one", "/tmp/one", active)
	m.overview = overview.New(workspaceinventory.Collector{Runner: &countingOverviewRunner{}})
	m.intro.Active, m.intro.Done = false, true
	m.intro.RepoName = "one"
	m.width, m.height, m.ready = 140, 40, true
	m.updateContext()
	return m, plugins
}

func totalInits(plugins map[string]*navigationPlugin) int {
	total := 0
	for _, p := range plugins {
		total += p.inits
	}
	return total
}

func TestOverviewEntryAndExitKeepTheExactProjectDestination(t *testing.T) {
	m, plugins := scopeBaselineModel(t, "git")
	if m.activePlugin != 2 {
		t.Fatalf("active plugin = %d, want git at 2", m.activePlugin)
	}
	workDir, projectRoot, active := m.ui.WorkDir, m.ui.ProjectRoot, m.activePlugin
	inits := totalInits(plugins)

	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'k', Text: "K", Mod: tea.ModShift})
	m = asAppModel(t, updated)
	if !m.inGlobalScope() || cmd == nil {
		t.Fatalf("K did not enter Overview: active=%v cmd=%v", m.inGlobalScope(), cmd != nil)
	}
	if m.ui.WorkDir != workDir || m.ui.ProjectRoot != projectRoot || m.activePlugin != active {
		t.Fatalf("entry moved the project underneath: work=%q root=%q plugin=%d",
			m.ui.WorkDir, m.ui.ProjectRoot, m.activePlugin)
	}
	if got := totalInits(plugins); got != inits {
		t.Fatalf("entry reinitialized plugins: inits %d -> %d", inits, got)
	}
	if m.activeContext != "overview" || m.activeDestinationName() != "Overview" {
		t.Fatalf("entry did not name the global destination: context=%q name=%q",
			m.activeContext, m.activeDestinationName())
	}

	for _, key := range []struct {
		name string
		msg  tea.KeyPressMsg
	}{
		{"K", tea.KeyPressMsg{Code: 'k', Text: "K", Mod: tea.ModShift}},
		{"esc", tea.KeyPressMsg{Code: tea.KeyEsc}},
	} {
		entered := m
		entered.scope = ScopeGlobal
		entered.updateContext()
		updated, _ = entered.Update(key.msg)
		exited := asAppModel(t, updated)
		if exited.inGlobalScope() {
			t.Fatalf("%s did not leave Overview", key.name)
		}
		if exited.activePlugin != active || exited.ui.WorkDir != workDir || exited.ui.ProjectRoot != projectRoot {
			t.Fatalf("%s returned to the wrong destination: plugin=%d work=%q root=%q",
				key.name, exited.activePlugin, exited.ui.WorkDir, exited.ui.ProjectRoot)
		}
		if exited.activeContext != "git" {
			t.Fatalf("%s left context %q, want the project plugin's own", key.name, exited.activeContext)
		}
		if got := totalInits(plugins); got != inits {
			t.Fatalf("%s reinitialized plugins: inits %d -> %d", key.name, inits, got)
		}
	}

	entered := m
	entered.scope = ScopeGlobal
	entered.updateContext()
	updated, _ = entered.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	quitting := asAppModel(t, updated)
	if !quitting.inGlobalScope() || !quitting.showQuitConfirm {
		t.Fatalf("q from Overview: global=%v quit=%v", quitting.inGlobalScope(), quitting.showQuitConfirm)
	}
}

// A project Workspaces Output pane and global Workspaces can name the same tmux
// pane with different geometry. Crossing the scope boundary must therefore
// transfer terminal ownership, not merely change which pixels are rendered.
func TestGlobalScopeSuspendsAndRestoresTheCoveredProjectTerminal(t *testing.T) {
	m, plugins := scopeBaselineModel(t, "workspaces")
	m.globalTab = GlobalWorkspaces
	project := plugins["workspaces"]
	project.SetFocused(true) // the startup focus transition in the real app
	_, _ = project.Update(plugin.PluginFocusedMsg{})
	inits := totalInits(plugins)

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'k', Text: "K", Mod: tea.ModShift})
	m = asAppModel(t, updated)
	if !m.inGlobalScope() || project.focused || project.terminalOpen {
		t.Fatalf("global entry kept covered project ownership: global=%v focused=%v terminal=%v",
			m.inGlobalScope(), project.focused, project.terminalOpen)
	}

	// Window geometry and terminal deliveries are still broadcast for ordinary
	// background plugin state, but the covered terminal must act on neither.
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 111, Height: 35})
	m = asAppModel(t, updated)
	updated, _ = m.Update(tty.SessionDeadMsg{})
	m = asAppModel(t, updated)
	if project.terminalResizes != 0 || project.terminalMsgs != 0 {
		t.Fatalf("covered project terminal handled global traffic: resizes=%d messages=%d",
			project.terminalResizes, project.terminalMsgs)
	}

	updated, focusCmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = asAppModel(t, updated)
	if m.inGlobalScope() || !project.focused || project.terminalOpen {
		t.Fatalf("project return focus state before notification is wrong: global=%v focused=%v terminal=%v",
			m.inGlobalScope(), project.focused, project.terminalOpen)
	}
	if focusCmd == nil {
		t.Fatal("project return emitted no focus notification for lifecycle reconciliation")
	}
	updated, _ = m.Update(focusCmd())
	m = asAppModel(t, updated)
	if !project.terminalOpen {
		t.Fatal("project focus notification did not restore terminal ownership")
	}
	if got := totalInits(plugins); got != inits {
		t.Fatalf("scope ownership transfer reinitialized plugins: %d -> %d", inits, got)
	}

	updated, _ = m.Update(tea.WindowSizeMsg{Width: 112, Height: 36})
	m = asAppModel(t, updated)
	updated, _ = m.Update(tty.SessionDeadMsg{})
	_ = asAppModel(t, updated)
	if project.terminalResizes != 1 || project.terminalMsgs != 1 {
		t.Fatalf("restored project terminal missed traffic: resizes=%d messages=%d",
			project.terminalResizes, project.terminalMsgs)
	}
	if got := project.focusChanges; len(got) < 3 || got[len(got)-2] || !got[len(got)-1] {
		t.Fatalf("focus ownership transitions = %v, want false then true", got)
	}
}

func TestSelectingTheCoveredProjectFromGlobalRestoresItsTerminalOnce(t *testing.T) {
	m, plugins := scopeBaselineModel(t, "workspaces")
	project := plugins["workspaces"]
	project.SetFocused(true)
	_, _ = project.Update(plugin.PluginFocusedMsg{})
	beforeNotices := project.focusNotices
	inits := totalInits(plugins)

	// Use the real entry transition: directly assigning ScopeGlobal would skip
	// the suspension this regression exists to protect.
	_ = m.enterOverview()
	if !m.inGlobalScope() || project.focused || project.terminalOpen {
		t.Fatalf("entry did not suspend covered project: global=%v focused=%v terminal=%v",
			m.inGlobalScope(), project.focused, project.terminalOpen)
	}
	m.initProjectSwitcher()
	var current projectSwitcherDestination
	for _, destination := range m.projectSwitcherFiltered {
		if destination.Kind == destinationProject && destination.Path == m.ui.WorkDir {
			current = destination
			break
		}
	}
	if current.Path == "" {
		t.Fatal("switcher did not contain the covered project")
	}

	focusCmd := m.activateProjectSwitcherDestination(current)
	if m.inGlobalScope() || !project.focused || project.terminalOpen {
		t.Fatalf("selection did not begin project restoration: global=%v focused=%v terminal=%v",
			m.inGlobalScope(), project.focused, project.terminalOpen)
	}
	if focusCmd == nil {
		t.Fatal("covered-project selection emitted no focus reconciliation")
	}
	msg := focusCmd()
	if _, ok := msg.(plugin.PluginFocusedMsg); !ok {
		t.Fatalf("covered-project selection emitted %T, want exactly PluginFocusedMsg", msg)
	}
	updated, more := m.Update(msg)
	m = asAppModel(t, updated)
	if more != nil {
		t.Fatalf("focus reconciliation produced extra work: %T", more())
	}
	if !project.terminalOpen || project.focusNotices != beforeNotices+1 {
		t.Fatalf("terminal restoration: open=%v notices=%d want=%d",
			project.terminalOpen, project.focusNotices, beforeNotices+1)
	}
	if m.statusMsg != "Already on this project" {
		t.Fatalf("same-project notice = %q", m.statusMsg)
	}
	if got := totalInits(plugins); got != inits {
		t.Fatalf("same-project return reinitialized plugins: %d -> %d", inits, got)
	}
}

func TestScopeOwnsTheHeaderTabRow(t *testing.T) {
	m, _ := scopeBaselineModel(t, "git")
	projectTitle, projectTabs, _, _ := m.headerLayout()
	if len(projectTabs) != 4 {
		t.Fatalf("project tabs = %d, want one per plugin", len(projectTabs))
	}
	if !strings.Contains(ansi.Strip(projectTitle), "one") {
		t.Fatalf("project title = %q, want the project name", ansi.Strip(projectTitle))
	}
	if strings.Contains(projectTitle, styles.BarChipActive.Render("one")) {
		t.Fatal("project repo name should stay subtitle text, not a filled pill")
	}
	for i, tab := range projectTabs {
		if tab.ref.scope != ScopeProject || tab.ref.plugin != i {
			t.Fatalf("project tab %d = %#v, want the plugin at that index", i, tab.ref)
		}
	}
	active := styles.RenderTab(m.registry.Plugins()[2].Name(), 2, 4, true, false)
	if projectTabs[2].text != active {
		t.Fatal("the active project plugin's tab is not drawn active")
	}

	m.scope = ScopeGlobal
	m.updateContext()
	title, tabs, _, _ := m.headerLayout()
	if !strings.Contains(ansi.Strip(title), "Overview") {
		t.Fatalf("global title = %q, want Overview", ansi.Strip(title))
	}
	if !strings.Contains(title, styles.BarChipActive.Render("Overview")) {
		t.Fatalf("global title is missing the Overview pill: %q", ansi.Strip(title))
	}
	// The global space owns its own tabs, and only its own. Tasks is absent
	// because its feature is off in this fixture.
	want := []GlobalTab{GlobalAgents, GlobalWorkspaces}
	if len(tabs) != len(want) {
		t.Fatalf("global tabs = %d, want %d", len(tabs), len(want))
	}
	for i, tab := range tabs {
		if tab.ref.scope != ScopeGlobal || tab.ref.global != want[i] {
			t.Fatalf("global tab %d = %#v, want %v", i, tab.ref, want[i])
		}
		if !strings.Contains(ansi.Strip(tab.text), want[i].Name()) {
			t.Fatalf("global tab %d text = %q, want %q", i, ansi.Strip(tab.text), want[i].Name())
		}
	}
	// The visible global tab is the active one; nothing renders a project tab
	// behind it.
	activeGlobal := styles.RenderTab(GlobalAgents.Name(), 0, len(want), true, false)
	if tabs[0].text != activeGlobal {
		t.Fatal("the visible global tab is not drawn active")
	}
	bounds := m.getTabBounds()
	if len(bounds) != len(tabs) {
		t.Fatalf("tab bounds = %d, want one per rendered tab (%d)", len(bounds), len(tabs))
	}
	for i, b := range bounds {
		if !b.Tab.same(tabs[i].ref) {
			t.Fatalf("hit region %d = %#v, want the tab it painted (%#v)", i, b.Tab, tabs[i].ref)
		}
	}
}

// Slice 1 replaces the recorded baseline: tab clicks, the number row, and
// backtick/bracket cycling now move between the tabs of the space the user is
// in, and never cross the scope boundary.
func TestTabClickNumberAndCycleKeysStayInsideTheActiveScope(t *testing.T) {
	t.Run("tab click", func(t *testing.T) {
		m, plugins := scopeBaselineModel(t, "git")
		m.scope = ScopeGlobal
		m.updateContext()
		inits := totalInits(plugins)
		bounds := m.getTabBounds()
		if len(bounds) != 2 {
			t.Fatalf("tab bounds = %#v", bounds)
		}
		target := bounds[1]
		updated, _ := m.Update(tea.MouseClickMsg{X: (target.Start + target.End) / 2, Y: 0, Button: tea.MouseLeft})
		clicked := asAppModel(t, updated)
		if !clicked.inGlobalScope() || clicked.globalTab != GlobalWorkspaces {
			t.Fatalf("tab click: global=%v tab=%v, want the global Workspaces tab",
				clicked.inGlobalScope(), clicked.globalTab)
		}
		if clicked.activePlugin != 2 || clicked.ui.WorkDir != "/tmp/one" || totalInits(plugins) != inits {
			t.Fatalf("tab click disturbed the project: plugin=%d work=%q inits=%d",
				clicked.activePlugin, clicked.ui.WorkDir, totalInits(plugins))
		}
	})

	// In the global space the number row addresses global tabs; a number with
	// no tab behind it does nothing rather than reaching a project plugin.
	globalNumbers := map[string]struct {
		tab    GlobalTab
		global bool
	}{
		"1": {GlobalAgents, true},
		"2": {GlobalWorkspaces, true},
		"3": {GlobalAgents, true}, // no third tab: Tasks is disabled here
	}
	for key, want := range globalNumbers {
		t.Run("global number "+key, func(t *testing.T) {
			m, plugins := scopeBaselineModel(t, "git")
			m.scope = ScopeGlobal
			m.updateContext()
			inits := totalInits(plugins)
			updated, _ := m.Update(tea.KeyPressMsg{Code: rune(key[0]), Text: key})
			pressed := asAppModel(t, updated)
			if pressed.inGlobalScope() != want.global || pressed.globalTab != want.tab {
				t.Fatalf("%s: global=%v tab=%v, want %v/%v",
					key, pressed.inGlobalScope(), pressed.globalTab, want.global, want.tab)
			}
			if pressed.activePlugin != 2 || totalInits(plugins) != inits {
				t.Fatalf("%s disturbed the project: plugin=%d inits=%d", key, pressed.activePlugin, totalInits(plugins))
			}
		})
	}

	// In project space the number row keeps addressing plugin tabs.
	projectNumbers := map[string]int{"1": 0, "3": 2, "4": 3}
	for key, want := range projectNumbers {
		t.Run("project number "+key, func(t *testing.T) {
			m, _ := scopeBaselineModel(t, "git")
			updated, _ := m.Update(tea.KeyPressMsg{Code: rune(key[0]), Text: key})
			pressed := asAppModel(t, updated)
			if pressed.inGlobalScope() || pressed.activePlugin != want {
				t.Fatalf("%s: global=%v plugin=%d, want project space on %d",
					key, pressed.inGlobalScope(), pressed.activePlugin, want)
			}
		})
	}

	globalCycles := map[string]GlobalTab{"`": GlobalWorkspaces, "]": GlobalWorkspaces, "~": GlobalWorkspaces, "[": GlobalWorkspaces}
	for key, want := range globalCycles {
		t.Run("global cycle "+key, func(t *testing.T) {
			m, _ := scopeBaselineModel(t, "notes")
			m.scope = ScopeGlobal
			m.updateContext()
			updated, _ := m.Update(tea.KeyPressMsg{Code: rune(key[0]), Text: key})
			pressed := asAppModel(t, updated)
			if !pressed.inGlobalScope() {
				t.Fatalf("%s left the global space", key)
			}
			if pressed.globalTab != want {
				t.Fatalf("%s: tab = %v, want %v", key, pressed.globalTab, want)
			}
			if pressed.activePlugin != 3 {
				t.Fatalf("%s moved the project plugin to %d", key, pressed.activePlugin)
			}
		})
	}

	projectCycles := map[string]int{"`": 0, "]": 0, "~": 2, "[": 2}
	for key, want := range projectCycles {
		t.Run("project cycle "+key, func(t *testing.T) {
			// Start on the last plugin so forward cycling wraps visibly and
			// backward cycling steps to its neighbour.
			m, _ := scopeBaselineModel(t, "notes")
			updated, _ := m.Update(tea.KeyPressMsg{Code: rune(key[0]), Text: key})
			pressed := asAppModel(t, updated)
			if pressed.inGlobalScope() {
				t.Fatalf("%s entered the global space", key)
			}
			if pressed.activePlugin != want {
				t.Fatalf("%s: plugin = %d, want %d", key, pressed.activePlugin, want)
			}
		})
	}
}

func TestProjectSwitcherFromOverviewRoutesByDestinationKind(t *testing.T) {
	m, plugins := scopeBaselineModel(t, "git")
	plugins["git"].SetFocused(true)
	_ = m.enterOverview()
	m.initProjectSwitcher()
	if len(m.projectSwitcherFiltered) != 3 || m.projectSwitcherCursor != 0 {
		t.Fatalf("switcher from Overview: destinations=%d cursor=%d",
			len(m.projectSwitcherFiltered), m.projectSwitcherCursor)
	}
	destinations := m.projectSwitcherFiltered
	inits := totalInits(plugins)

	// The pinned Overview destination re-enters the global space and starts a
	// fresh collection without touching the project underneath.
	cmd := m.activateProjectSwitcherDestination(destinations[0])
	if !m.inGlobalScope() || m.showProjectSwitcher {
		t.Fatalf("Overview destination: cmd=%v active=%v modal=%v",
			cmd != nil, m.inGlobalScope(), m.showProjectSwitcher)
	}
	if m.ui.WorkDir != "/tmp/one" || m.activePlugin != 2 || totalInits(plugins) != inits {
		t.Fatalf("Overview destination disturbed the project: work=%q plugin=%d inits=%d",
			m.ui.WorkDir, m.activePlugin, totalInits(plugins))
	}

	// A project destination leaves the global space. Switching to the project
	// already under the Overview is refused as "already on this project", which
	// is what keeps the remembered destination intact.
	same := destinations[1]
	if same.Kind != destinationProject || same.Path != "/tmp/one" {
		t.Fatalf("second destination = %#v, want the current project", same)
	}
	cmd = m.activateProjectSwitcherDestination(same)
	if m.inGlobalScope() {
		t.Fatal("project destination stayed in the global space")
	}
	if cmd == nil {
		t.Fatal("project destination produced no command")
	}
	if _, ok := cmd().(plugin.PluginFocusedMsg); !ok {
		t.Fatal("switching to the current project should restore focus, not re-switch")
	}
	if m.statusMsg != "Already on this project" {
		t.Fatal("switching to the current project lost its notice")
	}
	if m.activePlugin != 2 || m.activeContext != "git" {
		t.Fatalf("project destination changed the plugin: plugin=%d context=%q", m.activePlugin, m.activeContext)
	}
}
