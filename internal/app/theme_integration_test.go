package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/theme"
)

type themeTestPlugin struct {
	id             string
	focused        bool
	themeNotices   int
	lastReceivedMsg tea.Msg
}

func (p *themeTestPlugin) ID() string   { return p.id }
func (p *themeTestPlugin) Name() string { return p.id }
func (p *themeTestPlugin) Icon() string { return "" }
func (p *themeTestPlugin) Init(*plugin.Context) error { return nil }
func (p *themeTestPlugin) Start() tea.Cmd { return nil }
func (p *themeTestPlugin) Stop() {}
func (p *themeTestPlugin) Update(msg tea.Msg) (plugin.Plugin, tea.Cmd) {
	p.lastReceivedMsg = msg
	if _, ok := msg.(ThemeChangedMsg); ok {
		p.themeNotices++
	}
	return p, nil
}
func (p *themeTestPlugin) View(int, int) string { return "" }
func (p *themeTestPlugin) IsFocused() bool      { return p.focused }
func (p *themeTestPlugin) SetFocused(f bool)    { p.focused = f }
func (p *themeTestPlugin) Commands() []plugin.Command { return nil }
func (p *themeTestPlugin) FocusContext() string { return "" }

func newTestThemeModel(plugins ...plugin.Plugin) *Model {
	km := keymap.NewRegistry()
	keymap.RegisterDefaults(km)

	pCtx := &plugin.Context{
		WorkDir: "/test/project",
		Keymap:  km,
	}
	reg := plugin.NewRegistry(pCtx)
	for _, p := range plugins {
		reg.Register(p)
	}

	cfg := &config.Config{
		UI: config.UIConfig{
			Theme: config.ThemeConfig{Name: "sidecar-modern"},
		},
	}

	m := New(reg, km, cfg, "dev", "/test/project", "/test/project", "")
	m.width = 120
	m.height = 40
	return &m
}

func TestThemeSwitcherLivePreviewAndEscapeRestore(t *testing.T) {
	t.Cleanup(func() {
		styles.ApplyTheme("sidecar-modern")
	})
	styles.ApplyTheme("sidecar-modern")

	plug := &themeTestPlugin{id: "td-monitor"}
	m := newTestThemeModel(plug)

	// Open theme switcher
	m.showThemeSwitcher = true
	m.initThemeSwitcher()

	// Initial theme is sidecar-modern
	if styles.GetCurrentTheme().Name != "sidecar-modern" {
		t.Fatalf("initial theme = %q, want sidecar-modern", styles.GetCurrentTheme().Name)
	}
	initialNotices := plug.themeNotices

	// Move down to preview the next theme
	m.handleKeyMsg(tea.KeyPressMsg{Code: tea.KeyDown})

	previewTheme := styles.GetCurrentTheme().Name
	if previewTheme == "sidecar-modern" {
		t.Fatalf("theme after KeyDown did not change: %q", previewTheme)
	}
	if plug.themeNotices <= initialNotices {
		t.Errorf("expected plugin to receive ThemeChangedMsg on preview, got notices=%d", plug.themeNotices)
	}

	// Escape restores original theme
	m.handleKeyMsg(tea.KeyPressMsg{Code: tea.KeyEsc})

	if m.showThemeSwitcher {
		t.Error("expected theme switcher to be closed after Esc")
	}
	if styles.GetCurrentTheme().Name != "sidecar-modern" {
		t.Errorf("restored theme = %q, want sidecar-modern", styles.GetCurrentTheme().Name)
	}
}

func TestThemeNotificationReachesInactivePlugins(t *testing.T) {
	t.Cleanup(func() {
		styles.ApplyTheme("sidecar-modern")
	})
	styles.ApplyTheme("sidecar-modern")

	p1 := &themeTestPlugin{id: "active-plug", focused: true}
	p2 := &themeTestPlugin{id: "inactive-plug", focused: false}
	m := newTestThemeModel(p1, p2)

	notices1 := p1.themeNotices
	notices2 := p2.themeNotices

	// Apply a new resolved theme
	m.applyResolvedTheme(theme.ResolvedTheme{BaseName: "dracula"})

	if p1.themeNotices != notices1+1 {
		t.Errorf("active plugin received %d notices, want %d", p1.themeNotices, notices1+1)
	}
	if p2.themeNotices != notices2+1 {
		t.Errorf("inactive plugin received %d notices, want %d", p2.themeNotices, notices2+1)
	}
}

func TestThemeChangedMsgInAppUpdateBroadcast(t *testing.T) {
	t.Cleanup(func() {
		styles.ApplyTheme("sidecar-modern")
	})
	styles.ApplyTheme("sidecar-modern")

	p := &themeTestPlugin{id: "test-plug"}
	m := newTestThemeModel(p)

	initial := p.themeNotices
	_, _ = m.Update(msg.ThemeChangedMsg{})

	if p.themeNotices != initial+1 {
		t.Errorf("plugin received %d notices after Update(ThemeChangedMsg), want %d", p.themeNotices, initial+1)
	}
}

func TestProjectSwitchingAppliesResolvedThemeAndBroadcasts(t *testing.T) {
	t.Cleanup(func() {
		styles.ApplyTheme("sidecar-modern")
	})
	styles.ApplyTheme("sidecar-modern")

	p := &themeTestPlugin{id: "td-monitor"}
	m := newTestThemeModel(p)

	// Configure project with specific theme
	projectPath := t.TempDir()
	m.cfg.Projects.List = []config.ProjectConfig{
		{
			Path: projectPath,
			Name: "test-proj",
			Theme: &config.ThemeConfig{
				Name: "nord",
			},
		},
	}

	noticesBefore := p.themeNotices
	m.switchProjectWithSelection(projectPath, nil, nil, false)

	if styles.GetCurrentTheme().Name != "nord" {
		t.Errorf("active theme after project switch = %q, want nord", styles.GetCurrentTheme().Name)
	}
	if p.themeNotices <= noticesBefore {
		t.Errorf("expected plugin to receive theme notification during project switch, got %d", p.themeNotices)
	}
}

func TestProjectAddThemePreviewAndRestore(t *testing.T) {
	t.Cleanup(func() {
		styles.ApplyTheme("sidecar-modern")
	})
	styles.ApplyTheme("sidecar-modern")

	p := &themeTestPlugin{id: "td-monitor"}
	m := newTestThemeModel(p)

	m.initProjectAdd()
	m.projectAddThemeMode = true
	m.projectAddThemeFiltered = []string{"(use global)", "dracula", "nord"}
	m.projectAddThemeCursor = 1 // select dracula

	noticesBefore := p.themeNotices
	m.previewProjectAddTheme()

	if styles.GetCurrentTheme().Name != "dracula" {
		t.Errorf("previewed theme = %q, want dracula", styles.GetCurrentTheme().Name)
	}
	if p.themeNotices <= noticesBefore {
		t.Errorf("expected plugin to receive theme notification on preview, got %d", p.themeNotices)
	}

	// Press Esc to cancel and restore
	m.handleProjectAddThemePickerKeys(tea.KeyPressMsg{Code: tea.KeyEsc})

	if styles.GetCurrentTheme().Name != "sidecar-modern" {
		t.Errorf("restored theme = %q, want sidecar-modern", styles.GetCurrentTheme().Name)
	}
}

