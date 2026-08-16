package configui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/theme"
)

// configFixture points the config package at a temp file and returns the model
// wired to it, so a save in a test writes to the temp file and nothing else.
func configFixture(t *testing.T, cfg *config.Config) (*Model, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	data, err := json.Marshal(map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	config.SetTestConfigPath(path)
	t.Cleanup(config.ResetTestConfigPath)
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}

	m := New()
	m.SetHostState(HostState{Config: loaded})
	return m, path
}

func loadSaved(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	return cfg
}

// run executes the command a control returned, feeding a save back through the
// same path the host uses.
func run(t *testing.T, m *Model, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	return cmd()
}

func TestThemePickerFilterCountAndSwatch(t *testing.T) {
	picker := newThemePicker(false, 8)
	all := theme.CountSelectable(picker.filtered)
	if all < 2 {
		t.Fatalf("theme library only offered %d themes", all)
	}
	counts := theme.LibraryCounts()
	if !strings.Contains(picker.countSummary(), "built-in") {
		t.Fatalf("unfiltered count = %q, want the library summary", picker.countSummary())
	}
	if counts.Total() != all {
		t.Fatalf("library counts %d disagree with the list's %d", counts.Total(), all)
	}

	picker.search.SetValue("dracula")
	picker.refilter()
	if got := theme.CountSelectable(picker.filtered); got == 0 || got == all {
		t.Fatalf("filter matched %d of %d themes", got, all)
	}
	if !strings.Contains(picker.countSummary(), "of") {
		t.Fatalf("filtered count = %q, want an \"n of m\" summary", picker.countSummary())
	}
	for _, entry := range picker.filtered {
		if entry.IsSeparator {
			t.Fatal("a filtered list kept the library separator")
		}
		if !strings.Contains(strings.ToLower(entry.Name), "dracula") {
			t.Fatalf("filter kept a non-match: %q", entry.Name)
		}
	}

	// Every selectable entry identifies itself with four colors.
	for _, entry := range theme.List() {
		if entry.IsSeparator {
			continue
		}
		if swatch := theme.Swatch(entry); len(swatch) != 4 {
			t.Fatalf("theme %q has a %d-color swatch", entry.Name, len(swatch))
		}
	}
}

// Moving through the list previews; leaving without saving puts the previous
// theme back.
func TestAppearancePreviewRestoresOnEscape(t *testing.T) {
	cfg := config.Default()
	cfg.UI.Theme = config.ThemeConfig{Name: "default"}
	m, _ := configFixture(t, cfg)
	m.Open(PageAppearance)
	original := styles.GetCurrentThemeName()

	m.View(160, 45)
	m.detailFocus = true
	m.focusPickerList()
	m.View(160, 45)

	picker := m.activePicker()
	if picker == nil {
		t.Fatal("Appearance has no theme picker")
	}
	if !picker.move(1) {
		t.Fatal("the picker refused to move")
	}
	if !picker.previewing {
		t.Fatal("moving through the list did not preview")
	}
	if styles.GetCurrentThemeName() == original {
		t.Fatal("preview did not change the live theme")
	}

	if !m.Escape() {
		t.Fatal("Escape did not claim the preview")
	}
	if styles.GetCurrentThemeName() != original {
		t.Fatalf("Escape left %q applied, want %q", styles.GetCurrentThemeName(), original)
	}
}

// Enter saves at the scope the page is set to: global by default, the chosen
// project when project scope is selected.
func TestAppearanceScopeRoutesTheSave(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Projects.List = []config.ProjectConfig{{Name: "fixture", Path: dir}}
	m, _ := configFixture(t, cfg)
	m.SetHostState(HostState{Config: loadSaved(t), ProjectDir: dir, ProjectPath: dir})
	m.Open(PageAppearance)
	m.View(160, 45)

	entry := theme.Entry{Name: "Dracula", IsBuiltIn: true, ThemeKey: "dracula"}

	if msg := run(t, m, m.saveAppearanceTheme(entry)); msg == nil {
		t.Fatal("saving produced no message")
	} else if saved, ok := msg.(ConfigSavedMsg); !ok || saved.Err != "" {
		t.Fatalf("global save = %#v", msg)
	}
	saved := loadSaved(t)
	if saved.UI.Theme.Name != "dracula" {
		t.Fatalf("global theme = %q", saved.UI.Theme.Name)
	}
	if saved.Projects.List[0].Theme != nil {
		t.Fatal("a global save wrote a project override")
	}

	m.SetHostState(HostState{Config: saved, ProjectDir: dir, ProjectPath: dir})
	m.setThemeScope(true, dir)
	if msg := run(t, m, m.saveAppearanceTheme(theme.Entry{Name: "Nord", IsBuiltIn: true, ThemeKey: "nord"})); msg == nil {
		t.Fatal("project save produced no message")
	}
	saved = loadSaved(t)
	if saved.Projects.List[0].Theme == nil || saved.Projects.List[0].Theme.Name != "nord" {
		t.Fatalf("project theme = %#v", saved.Projects.List[0].Theme)
	}
	if saved.UI.Theme.Name != "dracula" {
		t.Fatalf("a project save changed the global theme to %q", saved.UI.Theme.Name)
	}
}

// The scope selector is only offered when the project the user is working in is
// one Sidecar knows about.
func TestThemeScopeNeedsAConfiguredProject(t *testing.T) {
	cfg := config.Default()
	m, _ := configFixture(t, cfg)
	m.Open(PageAppearance)
	view := ansi.Strip(m.View(160, 45))
	if !strings.Contains(view, "Add a project to create a per-project override") {
		t.Fatalf("Appearance offered a project scope with no configured project:\n%s", view)
	}
	if len(m.scopeProjects()) != 0 {
		t.Fatal("scopeProjects offered a scope with no configured project")
	}
}

// Toggles write immediately, through the config boundary.
func TestInterfaceTogglesSave(t *testing.T) {
	cfg := config.Default()
	cfg.UI.NerdFontsEnabled = false
	cfg.UI.ShowClock = false
	m, _ := configFixture(t, cfg)
	m.Open(PageAppearance)
	m.View(160, 45)

	for _, id := range []string{regionNerdFont, regionClock} {
		index := -1
		for i, c := range m.controls {
			if c.id == id {
				index = i
			}
		}
		if index < 0 {
			t.Fatalf("Appearance did not render %s", id)
		}
		if msg := run(t, m, m.runControl(index)); msg == nil {
			t.Fatalf("%s produced no save", id)
		}
	}
	saved := loadSaved(t)
	if !saved.UI.NerdFontsEnabled || !saved.UI.ShowClock {
		t.Fatalf("toggles did not persist: nerd=%v clock=%v", saved.UI.NerdFontsEnabled, saved.UI.ShowClock)
	}
}

// Typing in a field must never reach a page shortcut: while an editor is open
// the surface reports the config-edit context and swallows the key.
func TestFieldTypingDoesNotLeakToPageShortcuts(t *testing.T) {
	cfg := config.Default()
	m, _ := configFixture(t, cfg)
	m.Open(PageAppearance)
	m.View(160, 45)

	m.editTerminalTitle()
	if m.FocusContext() != ContextConfigEdit {
		t.Fatalf("editing a field reported context %q", m.FocusContext())
	}
	for _, r := range "ardg" {
		handled, _ := m.Key(tea.KeyPressMsg{Code: r, Text: string(r)})
		if !handled {
			t.Fatalf("%q escaped the open editor", r)
		}
	}
	if got := m.appearance().title.Value(); !strings.HasSuffix(got, "ardg") {
		t.Fatalf("field value = %q, want the typed characters", got)
	}
	if m.Route().IsChild() {
		t.Fatal("typing opened a route")
	}
	if m.confirm != nil {
		t.Fatal("typing raised a confirmation")
	}
}
