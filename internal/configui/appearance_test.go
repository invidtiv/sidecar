package configui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/mouse"
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
	// No test ever resolves a real installation or runs a package manager.
	m.SetInstallEnvironment(stubEnvironment(nil))
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

// A preview belongs to the screen that started it. Leaving Appearance by any
// route — the sidebar, or re-opening Configuration from the gear — puts the
// resolved theme back, so a preview can never survive as a silent change nobody
// on screen can undo.
func TestAppearancePreviewRestoredWhenLeavingThePage(t *testing.T) {
	previewThen := func(t *testing.T, leave func(m *Model)) {
		t.Helper()
		cfg := config.Default()
		cfg.UI.Theme = config.ThemeConfig{Name: "default"}
		m, _ := configFixture(t, cfg)
		m.Open(PageAppearance)

		m.View(160, 45)
		m.detailFocus = true
		m.focusPickerList()
		m.View(160, 45)

		picker := m.activePicker()
		if picker == nil {
			t.Fatal("Appearance has no theme picker")
		}
		// The live theme is process-wide state, so the baseline is the theme the
		// page would restore, not whatever a previous test left applied.
		theme.ApplyResolved(picker.restore)
		original := styles.GetCurrentThemeName()
		previewToADifferentTheme(t, picker, original)

		leave(m)
		if styles.GetCurrentThemeName() != original {
			t.Fatalf("leaving the page left %q applied, want %q", styles.GetCurrentThemeName(), original)
		}
		if picker.previewing {
			t.Fatal("the picker still claims to be previewing")
		}
	}

	t.Run("navigate", func(t *testing.T) {
		previewThen(t, func(m *Model) { m.Navigate(PageTerminal) })
	})
	t.Run("enter on a sidebar destination", func(t *testing.T) {
		previewThen(t, func(m *Model) {
			m.focus = focusSidebar
			m.cursor = indexOfPage(m.visiblePages(), PageTerminal)
			m.activateCursor()
			if m.Page() != PageTerminal {
				t.Fatalf("enter landed on %q", m.Page())
			}
		})
	})
	t.Run("sidebar click", func(t *testing.T) {
		previewThen(t, func(m *Model) { m.navigateFromSidebar(PageTerminal) })
	})
	t.Run("reopen", func(t *testing.T) {
		previewThen(t, func(m *Model) { m.Open(PageSetup) })
	})
}

// previewToADifferentTheme moves the picker until the live theme actually
// differs from the baseline, so a test about restoring a preview never passes
// on a preview that changed nothing.
func previewToADifferentTheme(t *testing.T, picker *themePicker, original string) {
	t.Helper()
	for i := 0; i < 5; i++ {
		if !picker.move(1) {
			break
		}
		if styles.GetCurrentThemeName() != original {
			if !picker.previewing {
				t.Fatal("the picker changed the live theme without marking a preview")
			}
			return
		}
	}
	t.Fatalf("the picker never previewed a theme other than %q", original)
}

// focusedThemeRows names every theme in the picker's visible window whose row
// painted itself as the selected one. It reads the rendered pane rather than
// the model, because "the whole top half of the list looks selected" is a
// statement about what was painted.
//
// Each row is judged on its own painted line, found through the hit region the
// row declared. Searching the whole view instead would count a name twice when
// two themes in the library share a display name, and would credit any other
// part of the page that happens to paint the same word.
func focusedThemeRows(t *testing.T, m *Model, rendered string) []string {
	t.Helper()
	picker := m.activePicker()
	if picker == nil {
		t.Fatal("no active theme picker")
	}
	focused := lipgloss.NewStyle().Foreground(styles.Primary).Bold(true)
	view := strings.Split(rendered, "\n")
	var names []string
	end := min(len(picker.filtered), picker.scroll+picker.rows)
	for i := picker.scroll; i < end; i++ {
		entry := picker.filtered[i]
		if entry.IsSeparator {
			continue
		}
		region := regionFor(t, m, fmt.Sprintf("%s%d", regionThemeRow, i))
		if region.Rect.Y >= len(view) {
			t.Fatalf("row %d claims line %d of a %d-line view", i, region.Rect.Y, len(view))
		}
		line := view[region.Rect.Y]
		if !strings.Contains(ansi.Strip(line), entry.Name) {
			t.Fatalf("row %d claims a line that does not hold %q:\n%s", i, entry.Name, ansi.Strip(line))
		}
		if strings.Contains(line, focused.Render(entry.Name)) {
			names = append(names, fmt.Sprintf("%s@%d", entry.Name, i))
		}
	}
	return names
}

// selectedThemeRow is the picker's selection written the way focusedThemeRows
// reports a row, so the two can be compared without a shared name matching two
// different themes.
func selectedThemeRow(m *Model) string {
	picker := m.activePicker()
	return fmt.Sprintf("%s@%d", picker.selected().Name, picker.cursor)
}

// A theme halfway down a scrolled list is the only row that looks selected, and
// it is the row the picker says is selected. Focus used to be decided twice —
// once against the control index while the control list was still half-built,
// which made every control at or above the cursor answer to it, and once
// against the picker's own index into a filtered list that also holds dividers
// — so clicking mid-list lit up everything above it as well.
func TestThemeListSelectsExactlyOneRow(t *testing.T) {
	cfg := config.Default()
	cfg.UI.Theme = config.ThemeConfig{Name: "default"}
	m, _ := configFixture(t, cfg)
	m.Open(PageAppearance)
	m.View(160, 45)
	m.detailFocus = true
	m.focusPickerList()
	m.View(160, 45)

	picker := m.activePicker()
	// Far enough in that the window has scrolled and the library divider is
	// behind us: both of the index spaces that used to disagree.
	for i := 0; i < 10; i++ {
		m.Key(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	rendered := m.View(160, 45)
	if picker.scroll == 0 {
		t.Fatalf("the list did not scroll: cursor=%d scroll=%d", picker.cursor, picker.scroll)
	}
	focused := focusedThemeRows(t, m, rendered)
	want := selectedThemeRow(m)
	if len(focused) != 1 || focused[0] != want {
		t.Fatalf("keyboard mid-list: rows painted as selected = %v, want exactly [%s]", focused, want)
	}

	// The same after a click halfway down the visible window, which is how the
	// bug was reported.
	target := picker.scroll + 4
	for picker.filtered[target].IsSeparator {
		target++
	}
	region := regionFor(t, m, fmt.Sprintf("%s%d", regionThemeRow, target))
	m.Mouse(tea.MouseClickMsg{X: region.Rect.X + 4, Y: region.Rect.Y, Button: tea.MouseLeft})
	rendered = m.View(160, 45)
	focused = focusedThemeRows(t, m, rendered)
	want = selectedThemeRow(m)
	if len(focused) != 1 || focused[0] != want {
		t.Fatalf("after a mid-list click: rows painted as selected = %v, want exactly [%s]", focused, want)
	}
	if picker.cursor != target {
		t.Fatalf("the click selected row %d but the row clicked was %d", picker.cursor, target)
	}
}

// The wheel scrolls the theme list. It is the only list in Configuration long
// enough to need it, and it scrolled under the keyboard but not under the
// mouse.
func TestThemeListScrollsWithTheWheel(t *testing.T) {
	cfg := config.Default()
	cfg.UI.Theme = config.ThemeConfig{Name: "default"}
	m, _ := configFixture(t, cfg)
	m.Open(PageAppearance)
	m.View(160, 45)
	m.detailFocus = true
	m.focusPickerList()
	m.View(160, 45)

	picker := m.activePicker()
	region := regionFor(t, m, fmt.Sprintf("%s%d", regionThemeRow, picker.scroll))
	x, y := region.Rect.X+4, region.Rect.Y

	for i := 0; i < 4; i++ {
		m.Mouse(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelDown})
		m.View(160, 45)
	}
	if picker.scroll == 0 {
		t.Fatalf("the wheel did not scroll the list: cursor=%d scroll=%d", picker.cursor, picker.scroll)
	}
	down := picker.cursor
	if down <= 0 {
		t.Fatalf("the wheel did not move the selection: cursor=%d", down)
	}
	// The selection stays inside the window the wheel scrolled to, and stays
	// the only row painted as selected.
	if picker.cursor < picker.scroll || picker.cursor >= picker.scroll+picker.rows {
		t.Fatalf("the wheel scrolled the selection out of sight: cursor=%d scroll=%d rows=%d", picker.cursor, picker.scroll, picker.rows)
	}
	rendered := m.View(160, 45)
	if focused := focusedThemeRows(t, m, rendered); len(focused) != 1 || focused[0] != selectedThemeRow(m) {
		t.Fatalf("after wheeling: rows painted as selected = %v, want exactly [%s]", focused, selectedThemeRow(m))
	}

	for i := 0; i < 8; i++ {
		m.Mouse(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelUp})
		m.View(160, 45)
	}
	if picker.scroll != 0 || picker.cursor != 0 {
		t.Fatalf("wheeling back up left the list at cursor=%d scroll=%d, want the top", picker.cursor, picker.scroll)
	}

	// A notch outside the list is not the list's to answer.
	m.Mouse(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelDown})
	m.View(160, 45)
	moved := picker.cursor
	m.Mouse(tea.MouseWheelMsg{X: 0, Y: 0, Button: tea.MouseWheelDown})
	m.View(160, 45)
	if picker.cursor != moved {
		t.Fatalf("a wheel notch outside the list moved it from %d to %d", moved, picker.cursor)
	}
}

// regionFor is the hit region the last render declared for a control.
func regionFor(t *testing.T, m *Model, id string) mouse.Region {
	t.Helper()
	for _, region := range m.mouse.HitMap.Regions() {
		if region.ID == id {
			return region
		}
	}
	t.Fatalf("no hit region for %q", id)
	return mouse.Region{}
}

// A page that shrinks under a stale row cursor still paints a focused control.
// Focus is exact — the control whose stop is the cursor's — so a cursor left
// past the end of a shorter page matched nothing, and the frame showed nothing
// selected until the next event nudged it.
func TestShrunkPageStillPaintsAFocusedRow(t *testing.T) {
	cfg := config.Default()
	m, _ := configFixture(t, cfg)
	m.Open(PageAppearance)
	m.View(160, 45)
	m.detailFocus = true
	// Whatever the page had, it has fewer controls than this.
	m.rowCursor = 99

	m.View(160, 45)
	stops := m.cursorControls()
	if len(stops) == 0 {
		t.Fatal("Appearance declared no cursor stops")
	}
	if m.rowCursor != len(stops)-1 {
		t.Fatalf("row cursor = %d, want it clamped to the last of %d stops", m.rowCursor, len(stops))
	}
	if focused := focusedControlIDs(t, m, 160, 45); len(focused) != 1 {
		t.Fatalf("controls painted as focused = %v, want exactly one", focused)
	}
}

// focusedControlIDs is every control the pane painted as focused. It asks by
// difference rather than by guessing at styles: the same pane painted with the
// detail cursor away from it is the unfocused baseline, and any control whose
// line differs between the two is one the pane marked.
func focusedControlIDs(t *testing.T, m *Model, width, height int) []string {
	t.Helper()
	focusedView := strings.Split(m.View(width, height), "\n")
	lineOf := map[string]int{}
	order := []string{}
	for _, c := range m.controls {
		if !c.cursor {
			continue
		}
		lineOf[c.id] = regionFor(t, m, c.id).Rect.Y
		order = append(order, c.id)
	}

	held := m.detailFocus
	m.detailFocus = false
	plainView := strings.Split(m.View(width, height), "\n")
	m.detailFocus = held
	m.View(width, height)

	var ids []string
	for _, id := range order {
		y := lineOf[id]
		if y >= len(focusedView) || y >= len(plainView) {
			continue
		}
		if focusedView[y] != plainView[y] {
			ids = append(ids, id)
		}
	}
	return ids
}

// The wheel over a library divider belongs to the list. Dividers are painted,
// not declared, so a notch over one used to fall through to nothing — and the
// page opens with a divider inside the visible window.
func TestWheelOverADividerScrollsTheList(t *testing.T) {
	cfg := config.Default()
	m, _ := configFixture(t, cfg)
	m.Open(PageAppearance)
	m.View(160, 45)
	m.detailFocus = true
	m.focusPickerList()
	m.View(160, 45)

	picker := m.activePicker()
	divider := -1
	end := min(len(picker.filtered), picker.scroll+picker.rows)
	for i := picker.scroll; i < end; i++ {
		if picker.filtered[i].IsSeparator {
			divider = i
		}
	}
	if divider < 0 {
		t.Fatal("the opening window holds no divider to wheel over")
	}
	// The divider's region is the one covering its line; find it by walking the
	// rows around it.
	above := regionFor(t, m, fmt.Sprintf("%s%d", regionThemeRow, divider-1))
	before := picker.cursor
	m.Mouse(tea.MouseWheelMsg{X: above.Rect.X + 4, Y: above.Rect.Y + 1, Button: tea.MouseWheelDown})
	m.View(160, 45)
	if picker.cursor == before {
		t.Fatalf("a notch over the divider was dropped: cursor still %d", before)
	}
}

// Filtering and then wheeling the results scrolls the list. The keyboard stays
// in the field: scrolling is a passive gesture, not a decision to stop typing.
func TestWheelScrollsWhileTheFilterHasTheKeyboard(t *testing.T) {
	cfg := config.Default()
	m, _ := configFixture(t, cfg)
	m.Open(PageAppearance)
	m.View(160, 45)
	m.detailFocus = true
	m.focusPickerSearch()
	m.View(160, 45)
	for _, r := range "dark" {
		m.Key(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	m.View(160, 45)

	picker := m.activePicker()
	if !m.editing() {
		t.Fatal("the filter does not hold the keyboard")
	}
	region := regionFor(t, m, fmt.Sprintf("%s%d", regionThemeRow, picker.scroll))
	before := picker.cursor
	m.Mouse(tea.MouseWheelMsg{X: region.Rect.X + 4, Y: region.Rect.Y, Button: tea.MouseWheelDown})
	m.View(160, 45)
	if picker.cursor == before {
		t.Fatal("the wheel did not scroll the filtered list")
	}
	if !m.editing() {
		t.Fatal("scrolling took the keyboard out of the filter")
	}
}

// Clicking a theme while the filter still holds the keyboard selects that
// theme, and it stays selected. The field used to take the row cursor straight
// back, leaving the click's row lit for one frame and nothing selected after.
func TestClickingARowThroughTheFilterSelectsIt(t *testing.T) {
	cfg := config.Default()
	m, _ := configFixture(t, cfg)
	m.Open(PageAppearance)
	m.View(160, 45)
	m.detailFocus = true
	m.focusPickerSearch()
	m.View(160, 45)
	for _, r := range "dark" {
		m.Key(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	m.View(160, 45)

	picker := m.activePicker()
	target := picker.scroll + 2
	if target >= len(picker.filtered) {
		target = len(picker.filtered) - 1
	}
	region := regionFor(t, m, fmt.Sprintf("%s%d", regionThemeRow, target))
	m.Mouse(tea.MouseClickMsg{X: region.Rect.X + 4, Y: region.Rect.Y, Button: tea.MouseLeft})

	// Two frames: the one the click produced, and the one after it.
	for i := 0; i < 2; i++ {
		rendered := m.View(160, 45)
		focused := focusedThemeRows(t, m, rendered)
		if len(focused) != 1 || focused[0] != selectedThemeRow(m) {
			t.Fatalf("frame %d after clicking through the filter: rows painted as selected = %v, want exactly [%s]", i+1, focused, selectedThemeRow(m))
		}
	}
	if m.editing() {
		t.Fatal("clicking a row left the filter holding the keyboard")
	}
	if picker.cursor != target {
		t.Fatalf("the click selected row %d, want %d", picker.cursor, target)
	}
}

// A wheel notch is three rows and applies one theme, not three. Preview
// recolours the whole application, and a flick used to do it once per row it
// passed over.
func TestWheelPreviewsOncePerNotch(t *testing.T) {
	cfg := config.Default()
	cfg.UI.Theme = config.ThemeConfig{Name: "default"}
	m, _ := configFixture(t, cfg)
	m.Open(PageAppearance)
	m.View(160, 45)
	m.detailFocus = true
	m.focusPickerList()
	m.View(160, 45)

	picker := m.activePicker()
	applied := styles.GetCurrentThemeName()
	if !picker.step(1) {
		t.Fatal("the picker refused to step")
	}
	if got := styles.GetCurrentThemeName(); got != applied {
		t.Fatalf("stepping applied %q on its own, want no preview until the gesture ends", got)
	}
	picker.cursorTo(0)

	region := regionFor(t, m, fmt.Sprintf("%s%d", regionThemeRow, picker.scroll))
	m.Mouse(tea.MouseWheelMsg{X: region.Rect.X + 4, Y: region.Rect.Y, Button: tea.MouseWheelDown})
	m.View(160, 45)
	// The notch left exactly the theme it landed on applied.
	if got, want := styles.GetCurrentThemeName(), theme.ThemeConfig(picker.selected()).Name; got != want {
		t.Fatalf("after one notch the applied theme is %q, want the row it landed on (%q)", got, want)
	}
}

// Closing Configuration puts back a theme it was only previewing. Escape
// restores it and so does leaving the page; the close key has to as well, or a
// theme nobody saved outlives the surface that could undo it.
func TestClosingRestoresAPreview(t *testing.T) {
	cfg := config.Default()
	cfg.UI.Theme = config.ThemeConfig{Name: "default"}
	m, _ := configFixture(t, cfg)
	m.Open(PageAppearance)
	// Whatever an earlier test left applied, the surface opens on the theme the
	// configuration resolves to, and that is what closing owes back.
	theme.ApplyResolved(theme.ResolveTheme(loadSaved(t), ""))
	original := styles.GetCurrentThemeName()
	m.View(160, 45)
	m.detailFocus = true
	m.focusPickerList()
	m.View(160, 45)

	if !m.activePicker().move(1) {
		t.Fatal("the picker refused to move")
	}
	if styles.GetCurrentThemeName() == original {
		t.Fatal("moving did not preview")
	}
	m.Close()
	if got := styles.GetCurrentThemeName(); got != original {
		t.Fatalf("closing left %q applied, want %q", got, original)
	}
}
