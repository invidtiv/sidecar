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
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// Baseline characterization of app entry/exit, the header's tab row and the
// keys that cross the global/project boundary, recorded before the global
// Overview work introduces an explicit app scope and its own global tabs
// (docs/plans/active/global-overview-workspaces.md, slice 0).
//
// These record the CURRENT behaviour, including the parts slice 1 deliberately
// changes: today Overview shows the project's plugin tabs with none active, and
// cycling/numeric keys leave the global space rather than moving between global
// tabs. When slice 1 lands, these expectations move with it — that is the point
// of recording them first.
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

// scopeBaselineModel builds an app model on four registered plugins with the
// Overview constructed but not yet entered.
func scopeBaselineModel(t *testing.T, active string) (Model, map[string]*navigationPlugin) {
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
	if !m.overviewActive || cmd == nil {
		t.Fatalf("K did not enter Overview: active=%v cmd=%v", m.overviewActive, cmd != nil)
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
		{"q", tea.KeyPressMsg{Code: 'q', Text: "q"}},
	} {
		entered := m
		entered.overviewActive = true
		entered.updateContext()
		updated, _ = entered.Update(key.msg)
		exited := asAppModel(t, updated)
		if exited.overviewActive {
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
}

func TestOverviewHeaderShowsProjectTabsWithNoneActive(t *testing.T) {
	m, _ := scopeBaselineModel(t, "git")
	projectTitle, projectTabs, _, _ := m.headerLayout()
	if len(projectTabs) != 4 {
		t.Fatalf("project tabs = %d, want one per plugin", len(projectTabs))
	}
	if !strings.Contains(ansi.Strip(projectTitle), "one") {
		t.Fatalf("project title = %q, want the project name", ansi.Strip(projectTitle))
	}

	m.overviewActive = true
	m.updateContext()
	title, tabs, _, _ := m.headerLayout()
	if !strings.Contains(ansi.Strip(title), "Overview") {
		t.Fatalf("global title = %q, want Overview", ansi.Strip(title))
	}
	if len(tabs) != len(projectTabs) {
		t.Fatalf("global tabs = %d, want today's project tab row of %d", len(tabs), len(projectTabs))
	}
	// Today the project tab row is still rendered in the global space, and the
	// active plugin's tab is drawn inactive because no project tab is current.
	for i, tab := range tabs {
		inactive := styles.RenderTab(m.registry.Plugins()[i].Name(), i, len(tabs), false, false)
		if tab.text != inactive {
			t.Fatalf("tab %d rendered active while Overview owns the screen", i)
		}
	}
	if got := len(m.getTabBounds()); got != len(tabs) {
		t.Fatalf("tab bounds = %d, want one per rendered tab (%d)", got, len(tabs))
	}
}

func TestOverviewTabClickAndNumberAndCycleKeysLeaveTheGlobalSpace(t *testing.T) {
	t.Run("tab click", func(t *testing.T) {
		m, _ := scopeBaselineModel(t, "git")
		m.overviewActive = true
		m.updateContext()
		bounds := m.getTabBounds()
		if len(bounds) < 4 {
			t.Fatalf("tab bounds = %#v", bounds)
		}
		target := bounds[3]
		updated, _ := m.Update(tea.MouseClickMsg{X: (target.Start + target.End) / 2, Y: 0, Button: tea.MouseLeft})
		clicked := asAppModel(t, updated)
		if clicked.overviewActive || clicked.activePlugin != 3 {
			t.Fatalf("tab click: overview=%v plugin=%d, want project space on plugin 3",
				clicked.overviewActive, clicked.activePlugin)
		}
	})

	numbers := map[string]int{"1": 0, "3": 2, "4": 3}
	for key, want := range numbers {
		t.Run("number "+key, func(t *testing.T) {
			m, _ := scopeBaselineModel(t, "git")
			m.overviewActive = true
			m.updateContext()
			updated, _ := m.Update(tea.KeyPressMsg{Code: rune(key[0]), Text: key})
			pressed := asAppModel(t, updated)
			if pressed.overviewActive || pressed.activePlugin != want {
				t.Fatalf("%s: overview=%v plugin=%d, want project space on %d",
					key, pressed.overviewActive, pressed.activePlugin, want)
			}
		})
	}

	cycles := map[string]int{"`": 0, "]": 0, "~": 2, "[": 2}
	for key, want := range cycles {
		t.Run("cycle "+key, func(t *testing.T) {
			// Start on the last plugin so forward cycling wraps visibly and
			// backward cycling steps to its neighbour.
			m, _ := scopeBaselineModel(t, "notes")
			m.overviewActive = true
			m.updateContext()
			updated, _ := m.Update(tea.KeyPressMsg{Code: rune(key[0]), Text: key})
			pressed := asAppModel(t, updated)
			if pressed.overviewActive {
				t.Fatalf("%s stayed in the global space", key)
			}
			if pressed.activePlugin != want {
				t.Fatalf("%s: plugin = %d, want %d", key, pressed.activePlugin, want)
			}
		})
	}
}

func TestProjectSwitcherFromOverviewRoutesByDestinationKind(t *testing.T) {
	m, plugins := scopeBaselineModel(t, "git")
	m.overviewActive = true
	m.updateContext()
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
	if cmd == nil || !m.overviewActive || m.showProjectSwitcher {
		t.Fatalf("Overview destination: cmd=%v active=%v modal=%v",
			cmd != nil, m.overviewActive, m.showProjectSwitcher)
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
	if m.overviewActive {
		t.Fatal("project destination stayed in the global space")
	}
	if cmd == nil {
		t.Fatal("project destination produced no command")
	}
	if _, ok := cmd().(ToastMsg); !ok {
		t.Fatal("switching to the current project should report it, not re-switch")
	}
	if m.activePlugin != 2 || m.activeContext != "git" {
		t.Fatalf("project destination changed the plugin: plugin=%d context=%q", m.activePlugin, m.activeContext)
	}
}
