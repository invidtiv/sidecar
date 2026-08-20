package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/configui"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/theme"
)

type themeTestPlugin struct {
	id              string
	focused         bool
	themeNotices    int
	lastReceivedMsg tea.Msg
	lastTheme       string
}

func (p *themeTestPlugin) ID() string                 { return p.id }
func (p *themeTestPlugin) Name() string               { return p.id }
func (p *themeTestPlugin) Icon() string               { return "" }
func (p *themeTestPlugin) Init(*plugin.Context) error { return nil }
func (p *themeTestPlugin) Start() tea.Cmd             { return nil }
func (p *themeTestPlugin) Stop()                      {}
func (p *themeTestPlugin) Update(msg tea.Msg) (plugin.Plugin, tea.Cmd) {
	p.lastReceivedMsg = msg
	if _, ok := msg.(ThemeChangedMsg); ok {
		p.themeNotices++
		p.lastTheme = styles.GetCurrentTheme().Name
	}
	return p, nil
}
func (p *themeTestPlugin) View(int, int) string       { return "" }
func (p *themeTestPlugin) IsFocused() bool            { return p.focused }
func (p *themeTestPlugin) SetFocused(f bool)          { p.focused = f }
func (p *themeTestPlugin) Commands() []plugin.Command { return nil }
func (p *themeTestPlugin) FocusContext() string       { return "" }

func newTestThemeModel(plugins ...plugin.Plugin) *Model {
	km := keymap.NewRegistry()
	keymap.RegisterDefaults(km)

	pCtx := &plugin.Context{
		WorkDir: "/test/project",
		Keymap:  km,
	}
	reg := plugin.NewRegistry(pCtx)
	for _, p := range plugins {
		if err := reg.Register(p); err != nil {
			panic(err)
		}
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
	previewNotices := plug.themeNotices

	// Escape restores original theme
	m.handleKeyMsg(tea.KeyPressMsg{Code: tea.KeyEsc})

	if m.showThemeSwitcher {
		t.Error("expected theme switcher to be closed after Esc")
	}
	if styles.GetCurrentTheme().Name != "sidecar-modern" {
		t.Errorf("restored theme = %q, want sidecar-modern", styles.GetCurrentTheme().Name)
	}
	if plug.themeNotices != previewNotices+1 {
		t.Errorf("expected a second ThemeChangedMsg on Esc restore, notices=%d want %d", plug.themeNotices, previewNotices+1)
	}
	if plug.lastTheme != "sidecar-modern" {
		t.Errorf("plugin last theme = %q, want sidecar-modern", plug.lastTheme)
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

func TestNotifyThemeChangedSkipsUnchangedPalette(t *testing.T) {
	t.Cleanup(func() {
		styles.ApplyTheme("sidecar-modern")
	})
	styles.ApplyTheme("sidecar-modern")

	p := &themeTestPlugin{id: "td-monitor"}
	m := newTestThemeModel(p)

	m.applyResolvedTheme(theme.ResolvedTheme{BaseName: "sidecar-modern"})
	notices := p.themeNotices
	if notices == 0 {
		t.Fatal("expected an initial theme notification")
	}

	if cmd := m.notifyThemeChanged(); cmd != nil {
		t.Error("notifyThemeChanged returned a cmd for an unchanged palette")
	}
	if p.themeNotices != notices {
		t.Errorf("unchanged palette re-notified: notices=%d want %d", p.themeNotices, notices)
	}

	m.applyResolvedTheme(theme.ResolvedTheme{BaseName: "dracula"})
	if p.themeNotices != notices+1 {
		t.Errorf("palette change notices=%d, want %d", p.themeNotices, notices+1)
	}
}

func TestConfigKeyDoesNotNotifyWhenPaletteUnchanged(t *testing.T) {
	t.Cleanup(func() {
		styles.ApplyTheme("sidecar-modern")
	})
	styles.ApplyTheme("sidecar-modern")

	p := &themeTestPlugin{id: "td-monitor"}
	m := newTestThemeModel(p)
	m.openConfiguration(configui.PageAbout)
	m.notifyThemeChanged()
	notices := p.themeNotices

	_, _ = m.configKey(tea.KeyPressMsg{Code: '/'})
	_, _ = m.configKey(tea.KeyPressMsg{Text: "z", Code: 'z'})
	if p.themeNotices != notices {
		t.Errorf("search typing re-notified theme: notices=%d want %d", p.themeNotices, notices)
	}
}

func TestConfigMousePathNotifiesOnLivePaletteChange(t *testing.T) {
	t.Cleanup(func() {
		styles.ApplyTheme("sidecar-modern")
	})
	styles.ApplyTheme("sidecar-modern")

	p := &themeTestPlugin{id: "td-monitor"}
	m := newTestThemeModel(p)
	m.openConfiguration(configui.PageAppearance)
	m.notifyThemeChanged()
	notices := p.themeNotices

	theme.Preview(theme.Entry{Name: "Dracula", IsBuiltIn: true, ThemeKey: "dracula"})
	if p.themeNotices != notices {
		t.Fatalf("Preview itself notified plugins; notices=%d want %d", p.themeNotices, notices)
	}

	next, _ := m.Update(tea.MouseMotionMsg{X: 1, Y: headerHeight + 1})
	updated := next.(Model)
	m = &updated
	if p.themeNotices != notices+1 {
		t.Errorf("mouse path did not notify after palette change: notices=%d want %d", p.themeNotices, notices+1)
	}
	if p.lastTheme != "dracula" {
		t.Errorf("plugin last theme = %q, want dracula", p.lastTheme)
	}

	// A later motion with the same palette must not notify again.
	_, _ = m.Update(tea.MouseMotionMsg{X: 2, Y: headerHeight + 2})
	if p.themeNotices != notices+1 {
		t.Errorf("unchanged mouse path re-notified: notices=%d want %d", p.themeNotices, notices+1)
	}
}

type valueThemePlugin struct {
	id  string
	gen int
}

type themeCmdSentinel struct{}

func (p valueThemePlugin) ID() string                 { return p.id }
func (p valueThemePlugin) Name() string               { return p.id }
func (p valueThemePlugin) Icon() string               { return "" }
func (p valueThemePlugin) Init(*plugin.Context) error { return nil }
func (p valueThemePlugin) Start() tea.Cmd             { return nil }
func (p valueThemePlugin) Stop()                      {}
func (p valueThemePlugin) View(int, int) string       { return "" }
func (p valueThemePlugin) IsFocused() bool            { return false }
func (p valueThemePlugin) SetFocused(bool)            {}
func (p valueThemePlugin) Commands() []plugin.Command { return nil }
func (p valueThemePlugin) FocusContext() string       { return "" }
func (p valueThemePlugin) Update(msg tea.Msg) (plugin.Plugin, tea.Cmd) {
	if _, ok := msg.(ThemeChangedMsg); ok {
		p.gen++
		return p, func() tea.Msg { return themeCmdSentinel{} }
	}
	return p, nil
}

func TestNotifyThemeChangedPersistsReplacementAndBatchesCmds(t *testing.T) {
	t.Cleanup(func() {
		styles.ApplyTheme("sidecar-modern")
	})
	styles.ApplyTheme("sidecar-modern")

	m := newTestThemeModel(valueThemePlugin{id: "value"})
	cmd := m.notifyThemeChanged()
	if cmd == nil {
		t.Fatal("expected batched cmd from value plugin Update")
	}
	if _, ok := cmd().(themeCmdSentinel); !ok {
		t.Fatal("notifyThemeChanged did not return the plugin Update cmd")
	}

	got := m.registry.Get("value")
	vp, ok := got.(valueThemePlugin)
	if !ok {
		t.Fatalf("registry stored %T, want valueThemePlugin", got)
	}
	if vp.gen != 1 {
		t.Errorf("replacement gen = %d, want 1 (write into Plugins() copy was discarded)", vp.gen)
	}
}
