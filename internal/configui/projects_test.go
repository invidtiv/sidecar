package configui

import (
	"fmt"
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

// projectFixture is a model with two configured projects in real directories.
func projectFixture(t *testing.T) (*Model, string, string) {
	t.Helper()
	first := t.TempDir()
	second := t.TempDir()
	cfg := config.Default()
	cfg.Projects.List = []config.ProjectConfig{
		{Name: "alpha", Path: first},
		{Name: "beta", Path: second},
	}
	m, _ := configFixture(t, cfg)
	m.SetHostState(HostState{
		Config:      loadSaved(t),
		ProjectDir:  first,
		ProjectPath: first,
		OpenInApps:  []OpenInApp{{ID: "vscode", Name: "VS Code"}, {ID: "finder", Name: "Finder"}},
	})
	return m, first, second
}

// reload feeds a completed save back the way the host does.
func reload(t *testing.T, m *Model, msg tea.Msg) {
	t.Helper()
	saved, ok := msg.(ConfigSavedMsg)
	if !ok {
		t.Fatalf("expected a save, got %#v", msg)
	}
	if saved.Err != "" {
		t.Fatalf("save failed: %s", saved.Err)
	}
	state := m.host
	state.Config = loadSaved(t)
	m.SetHostState(state)
}

func TestProjectsPageListsAndDetails(t *testing.T) {
	m, first, _ := projectFixture(t)
	m.Open(PageProjects)
	view := ansi.Strip(m.View(160, 45))

	for _, want := range []string{"2 configured", "A  Add project", "alpha", "beta", "CURRENT", "Location", "Uses global", "Uses Sidecar default"} {
		if !strings.Contains(view, want) {
			t.Fatalf("Projects is missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "scan") {
		t.Fatalf("Projects explained a disk scan it does not do:\n%s", view)
	}
	if m.selectedProject() == nil || m.selectedProject().Path != first {
		t.Fatal("Projects did not select a project by default")
	}
}

// Removing is a confirmed change, and nothing happens until it is confirmed.
func TestRemoveProjectRequiresConfirmation(t *testing.T) {
	m, first, _ := projectFixture(t)
	m.Open(PageProjects)
	m.View(160, 45)

	handled, cmd := m.Key(tea.KeyPressMsg{Code: 'd', Text: "d"})
	if !handled {
		t.Fatal("d did not reach the Remove action")
	}
	if cmd != nil {
		t.Fatal("d wrote something before the user confirmed")
	}
	if m.confirm == nil {
		t.Fatal("Remove did not raise a confirmation")
	}
	if m.FocusContext() != ContextConfigConfirm {
		t.Fatalf("confirmation reported context %q", m.FocusContext())
	}
	if len(loadSaved(t).Projects.List) != 2 {
		t.Fatal("Remove changed the configuration before confirmation")
	}

	// Escape answers no.
	if !m.Escape() {
		t.Fatal("Escape did not dismiss the confirmation")
	}
	if len(loadSaved(t).Projects.List) != 2 {
		t.Fatal("cancelling removed the project anyway")
	}

	m.View(160, 45)
	m.Key(tea.KeyPressMsg{Code: 'd', Text: "d"})
	m.View(160, 45)
	_, cmd = m.Key(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if cmd == nil {
		t.Fatal("confirming did not remove the project")
	}
	reload(t, m, cmd())
	list := loadSaved(t).Projects.List
	if len(list) != 1 || list[0].Path == first {
		t.Fatalf("remove left %#v", list)
	}
}

// Reordering is a real setting: it persists.
func TestReorderProjectsPersists(t *testing.T) {
	m, first, second := projectFixture(t)
	m.Open(PageProjects)
	m.View(160, 45)
	m.detailFocus = true

	handled, cmd := m.Key(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift})
	if !handled || cmd == nil {
		t.Fatalf("shift+down did not reorder: handled=%v cmd=%v", handled, cmd != nil)
	}
	reload(t, m, cmd())
	list := loadSaved(t).Projects.List
	if list[0].Path != second || list[1].Path != first {
		t.Fatalf("order after shift+down: %q, %q", list[0].Name, list[1].Name)
	}
	// The moved project stays selected.
	if selected := m.selectedProject(); selected == nil || selected.Path != first {
		t.Fatalf("reorder lost the selection: %#v", m.selectedProject())
	}
}

// Add Project validates the same way the project switcher's add flow does.
func TestAddProjectValidationMatrix(t *testing.T) {
	m, first, _ := projectFixture(t)
	usable := t.TempDir()
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name, location, want string
	}{
		{"", usable, "Name is required"},
		{"gamma", "", "Path is required"},
		{"gamma", filepath.Join(usable, "missing"), "Path does not exist"},
		{"gamma", file, "Path is not a directory"},
		{"ALPHA", usable, "Project name already exists"},
		{"gamma", first, "Project path already configured"},
		{"gamma", usable, ""},
	}
	for _, tc := range cases {
		got := config.ValidateProject(m.projects(), tc.name, tc.location, -1)
		if got != tc.want {
			t.Fatalf("validate(%q, %q) = %q, want %q", tc.name, tc.location, got, tc.want)
		}
	}

	// A rejected save leaves the route open and the configuration untouched.
	m.OpenAddProject()
	m.View(160, 45)
	form := m.addProject
	form.name.SetValue("alpha")
	form.location.SetValue(usable)
	if cmd := m.saveProjectForm(); cmd != nil {
		t.Fatal("a duplicate name was saved")
	}
	if form.message == "" || !m.Route().IsChild() {
		t.Fatalf("rejected save left message=%q child=%v", form.message, m.Route().IsChild())
	}

	// An accepted save returns to Projects with the new project selected.
	form.name.SetValue("gamma")
	cmd := m.saveProjectForm()
	if cmd == nil {
		t.Fatal("a valid draft did not save")
	}
	if m.Route().IsChild() {
		t.Fatal("saving stayed on the Add Project route")
	}
	reload(t, m, cmd())
	if selected := m.selectedProject(); selected == nil || selected.Name != "gamma" {
		t.Fatalf("Projects did not select the new project: %#v", m.selectedProject())
	}
}

// Completion never enumerates before the user types, and Tab accepts without
// submitting.
func TestLocationCompletionRules(t *testing.T) {
	m, _, _ := projectFixture(t)
	root := t.TempDir()
	for _, name := range []string{"sidecar", "sidecar-notes", "other"} {
		if err := os.Mkdir(filepath.Join(root, name), 0755); err != nil {
			t.Fatal(err)
		}
	}

	m.OpenAddProject()
	m.View(160, 45)
	if m.editingID() != regionFormLocation {
		t.Fatalf("the deep link focused %q, want Location", m.editingID())
	}
	if len(m.addProject.completions) != 0 || len(m.pending) != 0 {
		t.Fatal("an empty Location asked for a directory listing")
	}

	m.addProject.location.SetValue(filepath.Join(root, "side"))
	m.requestCompletions()
	if len(m.pending) != 1 {
		t.Fatalf("typing a prefix raised %d completion requests", len(m.pending))
	}
	msg := m.pending[0]().(completionsMsg)
	m.pending = nil
	m.applyCompletions(msg)
	if len(m.addProject.completions) != 2 {
		t.Fatalf("completions = %#v", m.addProject.completions)
	}

	// Tab accepts the highlighted candidate and does not submit.
	handled, _ := m.Key(tea.KeyPressMsg{Code: tea.KeyTab})
	if !handled {
		t.Fatal("Tab did not reach the Location field")
	}
	if got := m.addProject.location.Value(); got != filepath.Join(root, "sidecar") {
		t.Fatalf("Tab put %q in the field", got)
	}
	if !m.Route().IsChild() {
		t.Fatal("Tab submitted the form")
	}
	if len(loadSaved(t).Projects.List) != 2 {
		t.Fatal("Tab wrote to the configuration")
	}
}

// The inline picker is a disclosure: it preserves the draft and the sidebar
// destination, and Escape collapses it back to the Theme field.
func TestInlineThemePickerPreservesDraft(t *testing.T) {
	m, _, _ := projectFixture(t)
	m.OpenAddProject()
	m.View(160, 45)
	m.addProject.name.SetValue("gamma")
	m.addProject.location.SetValue(t.TempDir())
	m.closeEditor()

	m.toggleInlineThemePicker()
	m.View(160, 45)
	if m.addProject.picker == nil {
		t.Fatal("Theme did not expand the picker")
	}
	if m.Page() != PageProjects || m.Route().Child != ChildAddProject {
		t.Fatalf("expanding the picker changed the route: %#v", m.Route())
	}
	view := ansi.Strip(m.View(160, 45))
	if !strings.Contains(view, "Use global theme") || !strings.Contains(view, "gamma") {
		t.Fatalf("the inline picker lost the draft or the reset action:\n%s", view)
	}

	picker := m.activePicker()
	if picker == nil {
		t.Fatal("the inline picker is not the active picker")
	}
	picker.cursorTo(theme.IndexOf(picker.filtered, theme.Entry{Name: "Dracula", IsBuiltIn: true, ThemeKey: "dracula"}))
	if cmd := picker.selectEntry(m, picker.selected()); cmd != nil {
		t.Fatal("selecting a theme in the draft wrote to the configuration")
	}
	if m.addProject.themeEntry.ThemeKey != "dracula" {
		t.Fatalf("draft theme = %#v", m.addProject.themeEntry)
	}
	if project := loadSaved(t).Projects.List[0]; project.Theme != nil {
		t.Fatal("an unsaved draft changed a project's theme")
	}

	if !m.Escape() {
		t.Fatal("Escape did not collapse the picker")
	}
	if m.addProject == nil {
		t.Fatal("collapsing the picker discarded the draft")
	}
	if m.addProject.picker != nil || !m.Route().IsChild() {
		t.Fatalf("Escape left picker=%v child=%v", m.addProject.picker != nil, m.Route().IsChild())
	}
	if m.addProject.name.Value() != "gamma" {
		t.Fatalf("the draft's name became %q", m.addProject.name.Value())
	}

	// A second Escape leaves the route without writing anything.
	if !m.Escape() {
		t.Fatal("Escape did not leave the route")
	}
	if m.Route().IsChild() || m.addProject != nil {
		t.Fatal("Escape did not return to Projects")
	}
	if len(loadSaved(t).Projects.List) != 2 {
		t.Fatal("abandoning the draft wrote a project")
	}
}

// Editing a project writes the rename, the theme override, and the open-in
// preference through the same boundary.
func TestEditProjectSaves(t *testing.T) {
	m, first, _ := projectFixture(t)
	m.Open(PageProjects)
	m.View(160, 45)
	m.OpenEditProject(first)
	m.View(160, 45)
	if !m.Route().IsChild() || m.addProject == nil || !m.addProject.edit {
		t.Fatalf("Edit did not open the form: %#v", m.Route())
	}

	m.addProject.name.SetValue("alpha-renamed")
	m.addProject.themeEntry = theme.Entry{Name: "Nord", IsBuiltIn: true, ThemeKey: "nord"}
	m.cycleFormOpenIn()
	cmd := m.saveProjectForm()
	if cmd == nil {
		t.Fatal("the edit did not save")
	}
	reload(t, m, cmd())

	project := loadSaved(t).Projects.List[0]
	if project.Name != "alpha-renamed" {
		t.Fatalf("name = %q", project.Name)
	}
	if project.Theme == nil || project.Theme.Name != "nord" {
		t.Fatalf("theme = %#v", project.Theme)
	}
	if project.OpenIn != "vscode" {
		t.Fatalf("openIn = %q", project.OpenIn)
	}
	if project.Path != first {
		t.Fatalf("path changed to %q", project.Path)
	}
}

// The diagnostics deep link opens Add Project with Location focused.
func TestAddProjectDeepLinkFocusesLocation(t *testing.T) {
	m, _, _ := projectFixture(t)
	m.Open(PageSetup)
	m.OpenAddProject()
	if m.Page() != PageProjects || m.Route().Child != ChildAddProject {
		t.Fatalf("deep link landed on %#v", m.Route())
	}
	if m.editingID() != regionFormLocation {
		t.Fatalf("deep link focused %q", m.editingID())
	}
	if m.FocusContext() != ContextConfigEdit {
		t.Fatalf("focused field reported context %q", m.FocusContext())
	}
	view := ansi.Strip(m.View(160, 45))
	if !strings.Contains(view, "Back to Projects") {
		t.Fatalf("Add Project has no parent-return control:\n%s", view)
	}
}

// Expanding the picker puts the keyboard in the theme list, so ↑/↓ previews and
// Enter selects without an extra step the mockup does not show.
func TestInlinePickerTakesTheKeyboardOnOpen(t *testing.T) {
	m, _, _ := projectFixture(t)
	m.OpenAddProject()
	m.View(160, 45)
	m.closeEditor()

	m.toggleInlineThemePicker()
	m.View(160, 45)
	if !m.pickerOwnsKeys() {
		t.Fatal("the expanded picker did not take the arrows")
	}

	picker := m.activePicker()
	before := picker.selected()
	if handled, _ := m.Key(tea.KeyPressMsg{Code: tea.KeyDown}); !handled {
		t.Fatal("down did not reach the picker")
	}
	m.View(160, 45)
	if picker.selected().Same(before) {
		t.Fatal("down did not move the picker's selection")
	}

	if handled, cmd := m.Key(tea.KeyPressMsg{Code: tea.KeyEnter}); !handled || cmd != nil {
		t.Fatalf("enter in the picker: handled=%v wrote=%v", handled, cmd != nil)
	}
	if m.addProject.themeEntry.IsZero() {
		t.Fatal("enter did not put the theme in the draft")
	}
	view := ansi.Strip(m.View(160, 45))
	if !strings.Contains(view, m.addProject.themeEntry.Name) {
		t.Fatalf("the Theme field does not show the chosen theme:\n%s", view)
	}
}

// The back control is Escape with a mouse: it must tear the draft down the same
// way. Leaving the form behind stranded its theme preview and left a stale draft
// that swallowed the next Escape.
func TestBackControlAbandonsTheDraftLikeEscape(t *testing.T) {
	m, _, _ := projectFixture(t)
	m.OpenAddProject()
	m.View(160, 45)
	m.closeEditor()
	m.toggleInlineThemePicker()
	m.View(160, 45)

	picker := m.activePicker()
	if picker == nil {
		t.Fatal("the inline picker did not open")
	}
	// The live theme is process-wide state, so the baseline is what the draft
	// would restore rather than whatever a previous test left applied.
	theme.ApplyResolved(picker.restore)
	original := styles.GetCurrentThemeName()
	previewToADifferentTheme(t, picker, original)

	index := -1
	for i, c := range m.controls {
		if c.id == regionBack {
			index = i
		}
	}
	if index < 0 {
		t.Fatal("the form did not render a back control")
	}
	m.runControl(index)

	if m.Route().IsChild() {
		t.Fatalf("the back control did not leave the form: %#v", m.Route())
	}
	if m.addProject != nil {
		t.Fatal("the back control left the draft behind")
	}
	if styles.GetCurrentThemeName() != original {
		t.Fatalf("the back control left %q previewed, want %q", styles.GetCurrentThemeName(), original)
	}
	// With nothing left on screen to dismiss, Escape is the host's signal to
	// close Configuration rather than being swallowed by the stale draft.
	if m.Escape() {
		t.Fatal("Escape after the back control was swallowed by a stale draft")
	}
}

// Arrowing the project list selects one project: the row that looks selected,
// and the one the detail block below describes. The page used to decide that
// twice — once from the pane's row cursor and once from its own selection
// index — which painted two rows as selected and described the wrong one.
func TestProjectListSelectsExactlyOneRow(t *testing.T) {
	m, _, second := projectFixture(t)
	m.Open(PageProjects)
	m.View(160, 45)
	m.detailFocus = true
	m.focusControlByID(regionProjectRow + "0")
	m.View(160, 45)

	// Down onto the second project.
	m.Key(tea.KeyPressMsg{Code: 'j', Text: "j"})
	view := m.View(160, 45)

	// Each row is judged on the line it painted, found through its own hit
	// region. Matching against the whole view would credit the detail block
	// below the list, which paints the selected project's name in the same
	// style — and so would pass with no row focused at all.
	focused := titleStyle()
	lines := strings.Split(view, "\n")
	var selected []string
	for i, project := range m.projects() {
		region := regionFor(t, m, fmt.Sprintf("%s%d", regionProjectRow, i))
		if region.Rect.Y >= len(lines) {
			t.Fatalf("row %d claims line %d of a %d-line view", i, region.Rect.Y, len(lines))
		}
		line := lines[region.Rect.Y]
		if !strings.Contains(ansi.Strip(line), project.Name) {
			t.Fatalf("row %d claims a line that does not hold %q:\n%s", i, project.Name, ansi.Strip(line))
		}
		if strings.Contains(line, focused.Render(project.Name)) {
			selected = append(selected, project.Name)
		}
	}
	if len(selected) != 1 || selected[0] != "beta" {
		t.Fatalf("rows painted as selected = %v, want exactly [beta]:\n%s", selected, ansi.Strip(view))
	}
	if got := m.selectedProject(); got == nil || got.Path != second {
		t.Fatalf("the detail block follows %#v, want the row the cursor is on", got)
	}
}
