package app

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/overview"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func TestGetProjectSelectorBounds_EmptyRepoNameUsesFallback(t *testing.T) {
	m := Model{
		width: 120,
		intro: IntroModel{
			RepoName: "",
		},
	}

	start, end, ok := m.getProjectSelectorBounds()
	if !ok || end != m.width || start >= end {
		t.Errorf("fallback selector bounds = %d-%d ok=%v", start, end, ok)
	}
}

func TestGetRepoNameBounds_NormalRepoName(t *testing.T) {
	m := Model{
		width: 120,
		intro: IntroModel{
			RepoName: "sidecar",
		},
	}

	start, end, ok := m.getProjectSelectorBounds()
	if !ok {
		t.Fatal("getRepoNameBounds() with normal repo name should return ok=true")
	}

	// The selector is pinned to the far-right edge.
	if start <= 0 {
		t.Errorf("start should be > 0, got %d", start)
	}
	if end <= start {
		t.Errorf("end should be > start, got start=%d, end=%d", start, end)
	}
	if end != m.width {
		t.Errorf("selector end = %d, want %d", end, m.width)
	}

	// The width should roughly match the repo name length
	width := end - start
	if width < len("sidecar") {
		t.Errorf("bounds width (%d) should be >= repo name length (%d)", width, len("sidecar"))
	}
}

func TestGetRepoNameBounds_LongRepoName(t *testing.T) {
	longName := "this-is-a-very-long-repository-name-that-might-cause-issues"
	m := Model{
		width: 120,
		intro: IntroModel{
			RepoName: longName,
		},
	}

	start, end, ok := m.getProjectSelectorBounds()
	if !ok {
		t.Fatal("getRepoNameBounds() with long repo name should return ok=true")
	}

	if start <= 0 {
		t.Errorf("start should be > 0, got %d", start)
	}
	if end <= start {
		t.Errorf("end should be > start, got start=%d, end=%d", start, end)
	}

	// The width should roughly match the long repo name
	width := end - start
	if width < len(longName) {
		t.Errorf("bounds width (%d) should be >= repo name length (%d)", width, len(longName))
	}
}

func TestProjectSelectorBoundsExistInBothScopes(t *testing.T) {
	cfg := config.Default()
	features.Init(cfg)
	t.Cleanup(func() { features.Init(config.Default()) })
	m := Model{
		cfg:      cfg,
		registry: plugin.NewRegistry(nil),
		ui:       &UIState{},
		intro:    IntroModel{RepoName: "sidecar", Done: true},
		overview: overview.New(workspaceinventory.Collector{}),
		width:    120,
	}
	if start, end, ok := m.getProjectSelectorBounds(); !ok || end != m.width || start >= end {
		t.Fatalf("project selector bounds = %d-%d ok=%v", start, end, ok)
	}
	m.scope = ScopeGlobal
	start, end, ok := m.getProjectSelectorBounds()
	if !ok || end != m.width || end <= start {
		t.Fatalf("global selector bounds = %d-%d ok=%v", start, end, ok)
	}
	header := ansi.Strip(m.renderHeader())
	if !strings.Contains(header, "Select Project ▾") {
		t.Fatalf("global header is missing the selector: %q", header)
	}
}

func TestRepoNameClick_OpensProjectSwitcher(t *testing.T) {
	cfg := &config.Config{
		Projects: config.ProjectsConfig{
			List: []config.ProjectConfig{},
		},
	}
	m := Model{
		intro: IntroModel{
			RepoName: "testrepo",
			Active:   false, // Animation complete
			Done:     true,
		},
		cfg:    cfg,
		ui:     &UIState{},
		width:  120,
		height: 40,
		ready:  true,
	}

	// Get the bounds for the repo name
	start, end, ok := m.getProjectSelectorBounds()
	if !ok {
		t.Fatal("getRepoNameBounds() should return ok=true")
	}

	// Click in the middle of the repo name area
	clickX := (start + end) / 2
	msg := tea.MouseClickMsg{
		X:      clickX,
		Y:      0, // Header is at Y=0
		Button: tea.MouseLeft,
	}

	// Process the mouse message
	newModel, _ := m.Update(msg)
	updated := newModel.(Model)

	if !updated.showProjectSwitcher {
		t.Errorf("clicking repo name at X=%d (bounds %d-%d) should open project switcher", clickX, start, end)
	}
	if updated.activeContext != "project-switcher" {
		t.Errorf("activeContext should be 'project-switcher', got %q", updated.activeContext)
	}
}

func TestRepoNameClick_FocusesProjectFilter(t *testing.T) {
	cfg := &config.Config{
		Projects: config.ProjectsConfig{
			List: []config.ProjectConfig{
				{Name: "alpha", Path: "/tmp/alpha"},
				{Name: "bravo", Path: "/tmp/bravo"},
			},
		},
	}
	m := Model{
		intro:    IntroModel{RepoName: "testrepo", Done: true},
		cfg:      cfg,
		ui:       &UIState{},
		registry: plugin.NewRegistry(nil),
		keymap:   keymap.NewRegistry(),
		width:    120,
		height:   40,
		ready:    true,
	}

	start, end, ok := m.getProjectSelectorBounds()
	if !ok {
		t.Fatal("getRepoNameBounds() should return ok=true")
	}
	click := tea.MouseClickMsg{X: (start + end) / 2, Y: 0, Button: tea.MouseLeft}
	newModel, _ := m.Update(click)
	newModel.View()

	// A physical click produces a release after the press that opens the modal.
	newModel, _ = newModel.Update(tea.MouseReleaseMsg{X: click.X, Y: click.Y, Button: tea.MouseLeft})
	newModel.View()
	newModel, _ = newModel.Update(tea.KeyPressMsg{Code: 'b', Text: "b"})
	var updated Model
	switch value := newModel.(type) {
	case Model:
		updated = value
	case *Model:
		updated = *value
	default:
		t.Fatalf("updated model = %T, want app.Model or *app.Model", newModel)
	}

	if got := updated.projectSwitcherInput.Value(); got != "b" {
		t.Fatalf("project filter = %q, want %q", got, "b")
	}
	if got := len(updated.projectSwitcherFiltered); got != 1 {
		t.Fatalf("filtered projects = %d, want 1", got)
	}
}

func TestRepoNameClick_BlockedDuringIntro(t *testing.T) {
	cfg := &config.Config{
		Projects: config.ProjectsConfig{
			List: []config.ProjectConfig{},
		},
	}
	m := Model{
		intro: IntroModel{
			RepoName: "testrepo",
			Active:   true, // Animation still running
			Done:     false,
		},
		cfg:      cfg,
		ui:       &UIState{},
		registry: plugin.NewRegistry(nil),
		width:    120,
		height:   40,
		ready:    true,
	}

	start, end, ok := m.getProjectSelectorBounds()
	if !ok {
		t.Fatal("getRepoNameBounds() should return ok=true")
	}

	clickX := (start + end) / 2
	msg := tea.MouseClickMsg{
		X:      clickX,
		Y:      0,
		Button: tea.MouseLeft,
	}

	newModel, _ := m.Update(msg)
	updated := newModel.(Model)

	if updated.showProjectSwitcher {
		t.Error("clicking repo name during intro animation should NOT open project switcher")
	}
}

func TestRepoNameClick_OutsideBounds(t *testing.T) {
	cfg := &config.Config{
		Projects: config.ProjectsConfig{
			List: []config.ProjectConfig{},
		},
	}
	m := Model{
		intro: IntroModel{
			RepoName: "testrepo",
			Active:   false,
			Done:     true,
		},
		cfg:      cfg,
		ui:       &UIState{},
		registry: plugin.NewRegistry(nil),
		width:    120,
		height:   40,
		ready:    true,
	}

	start, _, ok := m.getProjectSelectorBounds()
	if !ok {
		t.Fatal("getRepoNameBounds() should return ok=true")
	}

	// Click before the repo name area (in the "Sidecar" text)
	msg := tea.MouseClickMsg{
		X:      start - 5,
		Y:      0,
		Button: tea.MouseLeft,
	}

	newModel, _ := m.Update(msg)
	updated := newModel.(Model)

	if updated.showProjectSwitcher {
		t.Error("clicking outside repo name bounds should NOT open project switcher")
	}
}

func TestRenderHeader_ClockFollowsConfig(t *testing.T) {
	now := time.Date(2025, 1, 1, 14, 30, 0, 0, time.UTC)
	reg := plugin.NewRegistry(nil)

	t.Run("enabled", func(t *testing.T) {
		m := Model{
			showClock: true,
			ui:        &UIState{Clock: now},
			registry:  reg,
			width:     120,
			intro:     IntroModel{Done: true},
		}
		header := m.renderHeader()
		if !strings.Contains(ansi.Strip(header), "14:30") {
			t.Errorf("header should contain the clock when showClock is true: %q", ansi.Strip(header))
		}
	})

	t.Run("narrow drops the clock before anything else", func(t *testing.T) {
		m := Model{
			showClock: true,
			ui:        &UIState{Clock: now},
			registry:  reg,
			width:     34,
			intro:     IntroModel{Done: true},
		}
		plain := ansi.Strip(m.renderHeader())
		if strings.Contains(plain, "14:30") {
			t.Errorf("narrow header kept the clock: %q", plain)
		}
		if !strings.Contains(plain, headerGear) {
			t.Errorf("narrow header dropped the gear: %q", plain)
		}
	})

	t.Run("false", func(t *testing.T) {
		m := Model{
			showClock: false,
			ui:        &UIState{Clock: now},
			registry:  reg,
			width:     120,
			intro:     IntroModel{Done: true},
		}
		header := m.renderHeader()
		if strings.Contains(header, "14:30") {
			t.Error("header should not contain clock when showClock is false")
		}
	})
}

func TestNarrowHeaderHitRegionsMatchPaintedGeometry(t *testing.T) {
	original := styles.PillTabsEnabled
	t.Cleanup(func() { styles.PillTabsEnabled = original })

	for _, pills := range []bool{false, true} {
		styles.PillTabsEnabled = pills
		for _, scope := range []AppScope{ScopeProject, ScopeGlobal} {
			m, _ := scopeModelWithTasks(t)
			m.scope = scope
			m.globalTab = GlobalSessions
			m.width, m.height, m.ready = minWidth, minHeight, true
			m.intro.Active, m.intro.Done = false, true
			m.updateContext()

			header := m.renderHeader()
			selectorStart, selectorEnd, ok := m.getProjectSelectorBounds()
			if !ok || selectorEnd != m.width {
				t.Fatalf("pills=%v scope=%v selector=%d-%d ok=%v", pills, scope, selectorStart, selectorEnd, ok)
			}
			if _, _, restoreOK := m.getProjectRestoreBounds(); restoreOK {
				t.Fatalf("pills=%v scope=%v narrow header retained optional restore control", pills, scope)
			}
			for _, bounds := range m.getTabBounds() {
				if bounds.Start < 0 || bounds.Start >= bounds.End || bounds.End > selectorStart {
					t.Fatalf("pills=%v scope=%v tab=%#v overlaps selector %d-%d", pills, scope, bounds, selectorStart, selectorEnd)
				}
				painted := ansi.Strip(ansi.Truncate(ansi.TruncateLeft(header, bounds.Start, ""), bounds.End-bounds.Start, ""))
				if !strings.Contains(painted, m.tabLabel(bounds.Tab)) {
					t.Fatalf("pills=%v scope=%v bounds=%#v paint=%q header=%q", pills, scope, bounds, painted, ansi.Strip(header))
				}

				candidate := m
				updated, _ := candidate.Update(tea.MouseClickMsg{X: (bounds.Start + bounds.End) / 2, Y: 0, Button: tea.MouseLeft})
				clicked := asAppModel(t, updated)
				if bounds.Tab.scope == ScopeGlobal {
					if !clicked.inGlobalScope() || clicked.globalTab != bounds.Tab.global {
						t.Fatalf("pills=%v scope=%v global click %#v routed to scope=%v tab=%v", pills, scope, bounds, clicked.scope, clicked.globalTab)
					}
				} else if clicked.inGlobalScope() || clicked.activePlugin != bounds.Tab.plugin {
					t.Fatalf("pills=%v project click %#v routed to scope=%v plugin=%d", pills, bounds, clicked.scope, clicked.activePlugin)
				}
			}

			updated, _ := m.Update(tea.MouseClickMsg{X: (selectorStart + selectorEnd) / 2, Y: 0, Button: tea.MouseLeft})
			selected := asAppModel(t, updated)
			if !selected.showProjectSwitcher || selected.scope != scope {
				t.Fatalf("pills=%v scope=%v selector click: modal=%v resulting scope=%v", pills, scope, selected.showProjectSwitcher, selected.scope)
			}
		}
	}
}

func TestWideHeaderGlobalRestoreAndSelectorGeometry(t *testing.T) {
	original := styles.PillTabsEnabled
	t.Cleanup(func() { styles.PillTabsEnabled = original })

	for _, pills := range []bool{false, true} {
		styles.PillTabsEnabled = pills
		m, plugins := scopeBaselineModel(t, "git")
		m.scope = ScopeGlobal
		m.globalTab = GlobalSessions
		m.width, m.height, m.ready = 160, 40, true
		m.updateContext()
		inits := totalInits(plugins)

		layout := m.headerGeometry()
		plain := ansi.Strip(m.renderHeader())
		if !strings.Contains(plain, "↖ one") || !strings.Contains(plain, "Select Project ▾") {
			t.Fatalf("pills=%v global header = %q", pills, plain)
		}
		if strings.Contains(layout.right, "\x1b[48;") || strings.Contains(layout.right, "\ue0b6") || strings.Contains(layout.right, "\ue0b4") {
			t.Fatalf("pills=%v global right controls are not transparent: %q", pills, layout.right)
		}
		restoreStart, restoreEnd, ok := m.getProjectRestoreBounds()
		if !ok || restoreStart >= restoreEnd {
			t.Fatalf("pills=%v restore bounds = %d-%d ok=%v", pills, restoreStart, restoreEnd, ok)
		}
		selectorStart, selectorEnd, ok := m.getProjectSelectorBounds()
		if !ok || restoreEnd >= selectorStart || selectorEnd != m.width {
			t.Fatalf("pills=%v restore=%d-%d selector=%d-%d", pills, restoreStart, restoreEnd, selectorStart, selectorEnd)
		}
		for _, bounds := range m.getTabBounds() {
			if bounds.End > restoreStart {
				t.Fatalf("pills=%v tab %#v overlaps restore %d-%d", pills, bounds, restoreStart, restoreEnd)
			}
		}

		restoredModel, _ := m.Update(tea.MouseClickMsg{X: (restoreStart + restoreEnd) / 2, Y: 0, Button: tea.MouseLeft})
		restored := asAppModel(t, restoredModel)
		if restored.inGlobalScope() || restored.activePlugin != m.activePlugin || restored.ui.WorkDir != m.ui.WorkDir || totalInits(plugins) != inits {
			t.Fatalf("pills=%v restore reinitialized or changed project: global=%v plugin=%d work=%q inits=%d", pills, restored.inGlobalScope(), restored.activePlugin, restored.ui.WorkDir, totalInits(plugins))
		}

		selectedModel, _ := m.Update(tea.MouseClickMsg{X: (selectorStart + selectorEnd) / 2, Y: 0, Button: tea.MouseLeft})
		selected := asAppModel(t, selectedModel)
		if !selected.inGlobalScope() || !selected.showProjectSwitcher {
			t.Fatalf("pills=%v global selector click changed scope or missed modal", pills)
		}

		project := m
		project.scope = ScopeProject
		project.updateContext()
		if _, _, ok := project.getProjectRestoreBounds(); ok {
			t.Fatalf("pills=%v project scope exposed restore bounds", pills)
		}
		projectLayout := project.headerGeometry()
		if strings.Contains(ansi.Strip(project.renderHeader()), "↖ one") {
			t.Fatalf("pills=%v project scope painted global restore", pills)
		}
		if pills && !strings.Contains(projectLayout.right, "\ue0b6") {
			t.Fatalf("pills=%v project selector lost its pill styling: %q", pills, projectLayout.right)
		}
	}
}

func TestHeaderControlsOnlyActivateOnPaintedRow(t *testing.T) {
	type region struct {
		name       string
		x          int
		wantAction func(before, after Model) bool
	}
	for _, scope := range []AppScope{ScopeProject, ScopeGlobal} {
		m, _ := scopeModelWithTasks(t)
		m.scope = scope
		m.globalTab = GlobalSessions
		m.width, m.height, m.ready = 160, 40, true
		m.intro.Active, m.intro.Done = false, true
		m.updateContext()

		var regions []region
		if start, end, ok := m.getLogoBounds(); ok {
			regions = append(regions, region{"logo", (start + end) / 2, func(before, after Model) bool { return before.scope != after.scope }})
		}
		if start, end, ok := m.getProjectRestoreBounds(); ok {
			regions = append(regions, region{"restore", (start + end) / 2, func(_ Model, after Model) bool { return !after.inGlobalScope() }})
		}
		if start, end, ok := m.getProjectSelectorBounds(); ok {
			regions = append(regions, region{"selector", (start + end) / 2, func(_ Model, after Model) bool { return after.showProjectSwitcher }})
		}
		for _, bounds := range m.getTabBounds() {
			bounds := bounds
			regions = append(regions, region{"tab " + m.tabLabel(bounds.Tab), (bounds.Start + bounds.End) / 2, func(_ Model, after Model) bool {
				if bounds.Tab.scope == ScopeGlobal {
					return after.inGlobalScope() && after.globalTab == bounds.Tab.global
				}
				return !after.inGlobalScope() && after.activePlugin == bounds.Tab.plugin
			}})
		}

		for _, target := range regions {
			t.Run(target.name, func(t *testing.T) {
				spacerModel, _ := m.Update(tea.MouseClickMsg{X: target.x, Y: 1, Button: tea.MouseLeft})
				spacer := asAppModel(t, spacerModel)
				if spacer.scope != m.scope || spacer.globalTab != m.globalTab || spacer.activePlugin != m.activePlugin || spacer.showProjectSwitcher {
					t.Fatalf("scope=%v %s activated from blank spacer: scope=%v tab=%v plugin=%d switcher=%v", scope, target.name, spacer.scope, spacer.globalTab, spacer.activePlugin, spacer.showProjectSwitcher)
				}

				paintedModel, _ := m.Update(tea.MouseClickMsg{X: target.x, Y: 0, Button: tea.MouseLeft})
				painted := asAppModel(t, paintedModel)
				if !target.wantAction(m, painted) {
					t.Fatalf("scope=%v %s did not activate on painted row", scope, target.name)
				}
			})
		}
	}
}

func TestHeaderSpacerClicksDoNotReachPlugins(t *testing.T) {
	m, plugins := scopeBaselineModel(t, "git")
	m.scope = ScopeProject
	m.width, m.height, m.ready = 160, 40, true
	git := plugins["git"]
	before := git.mouseClicks

	_, _ = m.Update(tea.MouseClickMsg{X: 20, Y: 1, Button: tea.MouseLeft})
	if git.mouseClicks != before {
		t.Fatalf("spacer click reached the project plugin: clicks=%d", git.mouseClicks)
	}

	_, _ = m.Update(tea.MouseClickMsg{X: 20, Y: headerHeight, Button: tea.MouseLeft})
	if git.mouseClicks != before+1 {
		t.Fatalf("content click did not reach the project plugin: clicks=%d", git.mouseClicks)
	}
}

func TestLongProjectSelectorPreservesNarrowLeftAnchorAndExactBounds(t *testing.T) {
	original := styles.PillTabsEnabled
	t.Cleanup(func() { styles.PillTabsEnabled = original })

	cases := []struct {
		name     string
		repo     string
		worktree *WorktreeInfo
	}{
		{"long repo", strings.Repeat("repository-", 12), nil},
		{"long worktree", "sidecar", &WorktreeInfo{Branch: strings.Repeat("feature-", 16), IsMain: false}},
	}
	for _, tc := range cases {
		for _, pills := range []bool{false, true} {
			styles.PillTabsEnabled = pills
			m, _ := scopeModelWithTasks(t)
			m.scope = ScopeProject
			m.width, m.height, m.ready = minWidth, minHeight, true
			m.intro = IntroModel{RepoName: tc.repo, Done: true}
			m.cachedWorktreeInfo = tc.worktree
			m.updateContext()

			header := m.renderHeader()
			plain := ansi.Strip(header)
			if lipgloss.Width(header) != minWidth || !strings.Contains(plain, "Sidecar") {
				t.Fatalf("%s pills=%v malformed header width/text: %d %q", tc.name, pills, lipgloss.Width(header), plain)
			}
			for _, label := range []string{"Sessions", "Activity", "Tasks"} {
				if !strings.Contains(plain, label) {
					t.Fatalf("%s pills=%v long selector displaced %s: %q", tc.name, pills, label, plain)
				}
			}
			logoStart, logoEnd, ok := m.getLogoBounds()
			if !ok || logoStart != 0 || logoEnd <= logoStart {
				t.Fatalf("%s pills=%v logo bounds=%d-%d ok=%v", tc.name, pills, logoStart, logoEnd, ok)
			}
			logoPaint := ansi.Strip(ansi.Truncate(header, logoEnd, ""))
			if !strings.Contains(logoPaint, "Sidecar") {
				t.Fatalf("%s pills=%v logo bounds cover invisible columns: %q", tc.name, pills, logoPaint)
			}

			selectorStart, selectorEnd, ok := m.getProjectSelectorBounds()
			if !ok || selectorEnd != minWidth || selectorStart < logoEnd || selectorStart >= selectorEnd {
				t.Fatalf("%s pills=%v selector=%d-%d logo=%d-%d", tc.name, pills, selectorStart, selectorEnd, logoStart, logoEnd)
			}
			selectorPaint := ansi.Strip(ansi.Truncate(ansi.TruncateLeft(header, selectorStart, ""), selectorEnd-selectorStart, ""))
			if !strings.Contains(selectorPaint, "▾") {
				t.Fatalf("%s pills=%v selector lost right edge/arrow: %q header=%q", tc.name, pills, selectorPaint, plain)
			}
			for _, bounds := range m.getTabBounds() {
				if bounds.Start < logoEnd || bounds.End > selectorStart || bounds.Start >= bounds.End {
					t.Fatalf("%s pills=%v tab %#v overlaps anchor/selector", tc.name, pills, bounds)
				}
				painted := ansi.Strip(ansi.Truncate(ansi.TruncateLeft(header, bounds.Start, ""), bounds.End-bounds.Start, ""))
				if !strings.Contains(painted, m.tabLabel(bounds.Tab)) {
					t.Fatalf("%s pills=%v tab %#v covers %q", tc.name, pills, bounds, painted)
				}
			}
		}
	}
}

func TestGlobalRestoreOmittedWithoutProjectName(t *testing.T) {
	m, _ := scopeBaselineModel(t, "git")
	m.scope = ScopeGlobal
	m.intro.RepoName = ""
	m.width = 160
	if _, _, ok := m.getProjectRestoreBounds(); ok {
		t.Fatal("empty covered-project name produced restore bounds")
	}
	if strings.Contains(ansi.Strip(m.renderHeader()), "↖") {
		t.Fatal("empty covered-project name painted a restore control")
	}
}

func TestIntroActive_SetFalseAfterCompletion(t *testing.T) {
	m := Model{
		intro: IntroModel{
			RepoName:    "testrepo",
			Active:      true,
			Done:        true,
			RepoOpacity: 1.0, // Fully faded in
		},
	}

	// Process IntroTickMsg when animation is complete
	msg := IntroTickMsg{}
	newModel, _ := m.Update(msg)
	updated := newModel.(Model)

	if updated.intro.Active {
		t.Error("intro.Active should be false after animation completes")
	}
}
