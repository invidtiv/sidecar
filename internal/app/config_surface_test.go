package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/configchecks"
	"github.com/marcus/sidecar/internal/configui"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/version"
)

func typeKey(t *testing.T, m Model, key string) Model {
	t.Helper()
	var msg tea.KeyPressMsg
	switch key {
	case "esc":
		msg = tea.KeyPressMsg{Code: tea.KeyEsc}
	case "enter":
		msg = tea.KeyPressMsg{Code: tea.KeyEnter}
	case "down":
		msg = tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		msg = tea.KeyPressMsg{Code: tea.KeyUp}
	default:
		runes := []rune(key)
		if len(runes) != 1 {
			t.Fatalf("typeKey: %q is not a single character", key)
		}
		msg = tea.KeyPressMsg{Code: runes[0], Text: key}
	}
	updated, _ := m.Update(msg)
	return asAppModel(t, updated)
}

// Opening Configuration must remember the exact surface underneath, and Escape
// must put it back — the same contract leaveOverview keeps for the global space.
func TestConfigurationTakeoverAndEscapeRestoresPriorSurface(t *testing.T) {
	m, plugins := scopeBaselineModel(t, "git")
	m.ActivePlugin().SetFocused(true)
	priorPlugin, priorScope, priorContext := m.activePlugin, m.scope, m.activeContext
	inits := totalInits(plugins)

	m = typeKey(t, m, ",")
	if !m.configOpen() {
		t.Fatal("comma did not open Configuration")
	}
	if m.config.Page() != configui.DefaultPage {
		t.Fatalf("Configuration opened on %q, want %q", m.config.Page(), configui.DefaultPage)
	}
	if m.activeContext != "config" {
		t.Fatalf("active context = %q, want config", m.activeContext)
	}
	if m.activePlugin != priorPlugin || m.scope != priorScope {
		t.Fatalf("takeover moved the surface underneath: plugin=%d scope=%v", m.activePlugin, m.scope)
	}
	if got := totalInits(plugins); got != inits {
		t.Fatalf("takeover reinitialized plugins: %d -> %d", inits, got)
	}
	if plugins["git"].focused {
		t.Fatal("covered plugin kept focus while Configuration owned the screen")
	}

	// Walk to another destination so the restore cannot be a coincidence.
	m = typeKey(t, m, "down")
	m = typeKey(t, m, "enter")
	if m.config.Page() == configui.DefaultPage {
		t.Fatal("down+enter did not navigate to another destination")
	}

	m = typeKey(t, m, "esc")
	if m.configOpen() {
		t.Fatal("esc did not close Configuration")
	}
	if m.activePlugin != priorPlugin || m.scope != priorScope || m.activeContext != priorContext {
		t.Fatalf("escape did not restore the prior surface: plugin=%d scope=%v ctx=%q",
			m.activePlugin, m.scope, m.activeContext)
	}
	if !plugins["git"].focused {
		t.Fatal("escape did not restore focus to the covered plugin")
	}
}

// Escape clears the query first and only then closes the surface.
func TestConfigurationEscapeClearsSearchBeforeClosing(t *testing.T) {
	m, _ := scopeBaselineModel(t, "git")
	m = typeKey(t, m, ",")
	m = typeKey(t, m, "/")
	for _, key := range []string{"t", "m", "u", "x"} {
		m = typeKey(t, m, key)
	}
	if !m.config.SearchActive() || m.config.Query() != "tmux" {
		t.Fatalf("query = %q, want tmux", m.config.Query())
	}
	if m.activeContext != "config-edit" {
		t.Fatalf("context while typing = %q, want config-edit", m.activeContext)
	}

	m = typeKey(t, m, "esc")
	if !m.configOpen() {
		t.Fatal("first esc closed Configuration instead of clearing the search")
	}
	if m.config.SearchActive() {
		t.Fatal("first esc did not clear the query")
	}
	m = typeKey(t, m, "esc")
	if m.configOpen() {
		t.Fatal("second esc did not close Configuration")
	}
}

// Typing into Search must not reach any global shortcut, mirroring
// key_precedence_test.go's protection for the other text-input contexts.
func TestConfigurationSearchTypingDoesNotTriggerGlobalShortcuts(t *testing.T) {
	m, plugins := scopeBaselineModel(t, "git")
	m = typeKey(t, m, ",")
	m = typeKey(t, m, "/")

	// Every one of these is a live global binding: quit, palette, diagnostics,
	// project switcher, theme switcher, tab numbers, overview.
	for _, key := range []string{"q", "?", "!", "@", "#", "K", "1", "2", "W", "i", "r", ","} {
		m = typeKey(t, m, key)
		if m.hasModal() {
			t.Fatalf("typing %q opened a modal", key)
		}
		if !m.configOpen() || !m.config.SearchFocused() {
			t.Fatalf("typing %q left the search field", key)
		}
		if m.inGlobalScope() {
			t.Fatalf("typing %q entered the global space", key)
		}
	}
	if want := "q?!@#K12WIr,"; !strings.EqualFold(m.config.Query(), want) {
		t.Fatalf("query = %q, want the typed characters %q", m.config.Query(), want)
	}
	if plugins["git"].keyInputs != 0 {
		t.Fatalf("keys leaked to the covered plugin: %d", plugins["git"].keyInputs)
	}
}

func TestConfigurationKeysDoNotReachHiddenPlugin(t *testing.T) {
	m, plugins := scopeBaselineModel(t, "git")
	m = typeKey(t, m, ",")
	before := plugins["git"].keyInputs
	for _, key := range []string{"x", "z", "down", "enter"} {
		m = typeKey(t, m, key)
	}
	if plugins["git"].keyInputs != before {
		t.Fatalf("Configuration forwarded keys to the hidden plugin: %d -> %d", before, plugins["git"].keyInputs)
	}
	if !m.configOpen() {
		t.Fatal("Configuration closed unexpectedly")
	}
}

// The gear is a real hit region beside the selector, at every width.
func TestHeaderGearBoundsAndClickOpenConfiguration(t *testing.T) {
	for _, width := range []int{minWidth, 100, 200} {
		m, _ := scopeBaselineModel(t, "git")
		m.width, m.height, m.ready = width, 40, true

		start, end, ok := m.getGearBounds()
		if !ok || start >= end {
			t.Fatalf("width=%d gear bounds = %d-%d ok=%v", width, start, end, ok)
		}
		selectorStart, selectorEnd, ok := m.getProjectSelectorBounds()
		if !ok || end > selectorStart || selectorEnd != width {
			t.Fatalf("width=%d gear %d-%d selector %d-%d", width, start, end, selectorStart, selectorEnd)
		}
		painted := ansi.Strip(ansi.Truncate(ansi.TruncateLeft(m.renderHeader(), start, ""), end-start, ""))
		if !strings.Contains(painted, headerGear) {
			t.Fatalf("width=%d gear bounds paint %q", width, painted)
		}
		for _, bounds := range m.getTabBounds() {
			if bounds.End > start {
				t.Fatalf("width=%d tab %#v overlaps the gear at %d", width, bounds, start)
			}
		}

		updated, _ := m.Update(tea.MouseClickMsg{X: start, Y: 0, Button: tea.MouseLeft})
		clicked := asAppModel(t, updated)
		if !clicked.configOpen() {
			t.Fatalf("width=%d gear click did not open Configuration", width)
		}
		if clicked.config.Page() != configui.DefaultPage {
			t.Fatalf("width=%d gear opened %q", width, clicked.config.Page())
		}
	}
}

// A context that binds comma for itself keeps it: the Workspaces diff leaf
// cycles target tabs with it and must not be hijacked into Configuration.
func TestCommaYieldsToAContextThatBindsIt(t *testing.T) {
	m, _ := scopeBaselineModel(t, "workspaces")
	keymap.RegisterDefaults(m.keymap)
	m.activeContext = "workspace-diff"

	m = typeKey(t, m, ",")
	if m.configOpen() {
		t.Fatal("comma opened Configuration from a context that binds it")
	}
}

// The footer is derived from the registered config bindings, not hand-painted.
func TestConfigurationFooterHintsComeFromBindings(t *testing.T) {
	m, _ := scopeBaselineModel(t, "git")
	keymap.RegisterDefaults(m.keymap)
	m = typeKey(t, m, ",")

	hints := m.footerHints()
	if len(hints) == 0 {
		t.Fatal("Configuration footer produced no hints")
	}
	byLabel := map[string]string{}
	for _, hint := range hints {
		byLabel[hint.label] = hint.keys
	}
	for label, wantKey := range map[string]string{
		"Sections": "down",
		"Change":   "enter",
		"Search":   "/",
		"Return":   "esc",
	} {
		keys, ok := byLabel[label]
		if !ok {
			t.Fatalf("footer is missing %q: %#v", label, hints)
		}
		if !strings.Contains(keys, wantKey) {
			t.Fatalf("footer hint %q keys = %q, want %q", label, keys, wantKey)
		}
	}
}

// The surface must stay inside its allocated rows and columns at every size, or
// the app's header scrolls off screen.
func TestConfigurationFrameRespectsContentBox(t *testing.T) {
	for _, size := range [][2]int{{minWidth, minHeight}, {100, 30}, {200, 50}} {
		m, _ := scopeBaselineModel(t, "git")
		m.width, m.height, m.ready = size[0], size[1], true
		m = typeKey(t, m, ",")

		contentHeight := m.height - headerHeight - footerHeight
		content := m.renderContent(m.width, contentHeight)
		lines := strings.Split(content, "\n")
		if len(lines) != contentHeight {
			t.Fatalf("size=%v content height = %d, want %d", size, len(lines), contentHeight)
		}
		for i, line := range lines {
			if width := ansi.StringWidth(line); width > m.width {
				t.Fatalf("size=%v line %d width = %d, want <= %d", size, i, width, m.width)
			}
		}
		frame := m.viewContent()
		if got := len(strings.Split(frame, "\n")); got != m.height {
			t.Fatalf("size=%v frame height = %d, want %d", size, got, m.height)
		}
		if !strings.Contains(ansi.Strip(frame), "Configuration") {
			t.Fatalf("size=%v frame lost the Configuration sidebar", size)
		}
	}
}

// A search result navigates to its page; the sidebar shows only matching pages.
func TestConfigurationSearchFiltersSidebarAndNavigates(t *testing.T) {
	m, _ := scopeBaselineModel(t, "git")
	m.width, m.height, m.ready = 160, 45, true
	m = typeKey(t, m, ",")
	m = typeKey(t, m, "/")
	for _, key := range []string{"t", "m", "u", "x"} {
		m = typeKey(t, m, key)
	}

	content := ansi.Strip(m.renderContent(m.width, m.height-headerHeight-footerHeight))
	if !strings.Contains(content, "matching settings") {
		t.Fatalf("filtered sidebar has no result count:\n%s", content)
	}
	if strings.Contains(content, "About Sidecar") {
		t.Fatalf("filtered sidebar still lists non-matching pages:\n%s", content)
	}

	m = typeKey(t, m, "down") // first result
	if m.config.SearchFocused() {
		t.Fatal("down from Search did not move to the first result")
	}
	m = typeKey(t, m, "enter")
	if page := m.config.Page(); page != configui.PageSetup {
		t.Fatalf("enter on the first tmux result opened %q", page)
	}
	if !m.config.SearchActive() {
		t.Fatal("navigating from a result dropped the query")
	}
}

// Configuration asks the host for the things it does not own. The host must
// answer them itself rather than letting them fall through to the plugins.
func TestHostAnswersConfigurationRequests(t *testing.T) {
	m, _ := scopeBaselineModel(t, "git")
	m = typeKey(t, m, ",")

	// A completed run reaches the surface's cache.
	results := configchecks.Results{{ID: configchecks.CheckTmux, Title: "tmux", OK: true, Summary: "Version 3.5 available"}}
	updated, _ := m.Update(configui.ChecksMsg{Results: results})
	m = asAppModel(t, updated)
	if !m.config.ChecksReady() {
		t.Fatal("the host did not deliver the completed checks to the surface")
	}
	if got, _ := m.config.Checks().Get(configchecks.CheckTmux); !got.OK {
		t.Fatalf("cached tmux result = %#v", got)
	}

	// An install shell is an ordinary shell with typed-but-unexecuted text: the
	// host closes Configuration, focuses Workspaces, and asks for the shell.
	updated, cmd := m.Update(configui.OpenShellMsg{Command: "brew install tmux"})
	m = asAppModel(t, updated)
	if m.configOpen() {
		t.Fatal("opening an install shell left Configuration covering the screen")
	}
	var sawFocus, sawShell bool
	for _, msg := range drain(cmd) {
		switch typed := msg.(type) {
		case FocusPluginByIDMsg:
			sawFocus = typed.PluginID == "workspace-manager"
		case OpenPrefilledShellMsg:
			sawShell = typed.Command == "brew install tmux"
		}
	}
	if !sawFocus || !sawShell {
		t.Fatalf("install shell request produced focus=%v shell=%v", sawFocus, sawShell)
	}
}

// drain runs a command and everything it batches.
func drain(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, inner := range batch {
			out = append(out, drain(inner)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// A field on a Configuration page owns typed characters exactly as Search does:
// the same global bindings must not fire while Add Project's Location is open.
func TestConfigurationFieldTypingDoesNotTriggerGlobalShortcuts(t *testing.T) {
	m, plugins := scopeBaselineModel(t, "git")
	m = typeKey(t, m, ",")
	m.config.OpenAddProject()
	m.updateContext()
	if m.activeContext != "config-edit" {
		t.Fatalf("an open field reported context %q", m.activeContext)
	}

	for _, key := range []string{"q", "?", "!", "@", "#", "K", "1", "W", "r", ",", "a", "d"} {
		m = typeKey(t, m, key)
		if m.hasModal() {
			t.Fatalf("typing %q opened a modal", key)
		}
		if !m.configOpen() {
			t.Fatalf("typing %q closed Configuration", key)
		}
		if m.inGlobalScope() {
			t.Fatalf("typing %q entered the global space", key)
		}
	}
	if plugins["git"].keyInputs != 0 {
		t.Fatalf("keys leaked to the covered plugin: %d", plugins["git"].keyInputs)
	}
	if route := m.config.Route(); !route.IsChild() {
		t.Fatalf("typing left the Add Project route: %#v", route)
	}
}

// A save reported by the surface is reloaded by the host, which is what makes a
// setting take effect without a restart.
func TestHostReloadsAfterConfigurationSave(t *testing.T) {
	m, _ := scopeBaselineModel(t, "git")
	m = typeKey(t, m, ",")

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	config.SetTestConfigPath(path)
	t.Cleanup(config.ResetTestConfigPath)

	cfg := config.Default()
	cfg.UI.ShowClock = true
	cfg.UI.NerdFontsEnabled = true
	cfg.UI.TerminalTitle = "{dir}"
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	original := styles.PillTabsEnabled
	t.Cleanup(func() { styles.PillTabsEnabled = original })

	updated, _ := m.Update(configui.ConfigSavedMsg{Notice: "Header clock on"})
	m = asAppModel(t, updated)
	if !m.showClock {
		t.Fatal("the host did not pick up the saved clock setting")
	}
	if m.titleTemplate != "{dir}" {
		t.Fatalf("title template = %q", m.titleTemplate)
	}
	if !styles.PillTabsEnabled {
		t.Fatal("the host did not apply the saved Nerd Font setting")
	}
}

// About hands an available update to the updater Sidecar already has, and
// Configuration stays open underneath so closing the updater restores About.
func TestConfigurationOpensTheExistingUpdater(t *testing.T) {
	m, _ := scopeBaselineModel(t, "git")
	m = typeKey(t, m, ",")

	updated, _ := m.Update(configui.OpenUpdaterMsg{})
	m = asAppModel(t, updated)
	if m.updateModalState != UpdateModalPreview {
		t.Fatalf("update modal state = %v, want the existing preview", m.updateModalState)
	}
	if !m.configOpen() {
		t.Fatal("handing off to the updater closed Configuration, so returning would not restore About")
	}
}

// The docs link goes to the desktop opener; Sidecar renders no web content.
func TestConfigurationOpensDocumentationWithTheOSOpener(t *testing.T) {
	m, _ := scopeBaselineModel(t, "git")
	m = typeKey(t, m, ",")
	_, cmd := m.Update(configui.OpenURLMsg{URL: configui.DocsURL})
	if cmd == nil {
		t.Fatal("the documentation link produced no command")
	}
}

// About's version and update status come from the running app, not from a copy.
func TestConfigurationHostStateCarriesVersionAndUpdateStatus(t *testing.T) {
	m, _ := scopeBaselineModel(t, "git")
	m.currentVersion = "1.2.3"
	state := m.configHostState()
	if state.Version != "1.2.3" {
		t.Fatalf("host state version = %q", state.Version)
	}
	if state.Update.Checked {
		t.Fatal("an unchecked release check was reported as settled")
	}

	m.setProductStatus(version.ProductStatusMsg{Target: version.Target{
		Product:        version.ProductSidecar,
		CurrentVersion: "1.2.3",
		LatestVersion:  "1.3.0",
		HasUpdate:      true,
	}})
	state = m.configHostState()
	if !state.Update.Checked || !state.Update.Available || state.Update.LatestVersion != "1.3.0" {
		t.Fatalf("host state update = %#v", state.Update)
	}
}
