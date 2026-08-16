package configui

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
)

// applySave feeds a save command's result back through the host's path, which
// is what makes what is on screen what is on disk.
func applySave(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	saved, ok := cmd().(ConfigSavedMsg)
	if !ok {
		return
	}
	if saved.Err != "" {
		t.Fatalf("saving failed: %s", saved.Err)
	}
	state := m.host
	state.Config = loadSaved(t)
	m.SetHostState(state)
}

// choose drives a select control the way a user does: activate it to open the
// list, walk the arrows to the option, and press Enter.
func choose(t *testing.T, m *Model, controlID, optionID string) {
	t.Helper()
	// Activating a control whose list is already open closes it, which is what a
	// second click means; a test that only wants to choose starts from closed.
	m.closeDropdown()
	activate(t, m, controlID)
	m.View(160, 45)
	if m.dropdown == nil {
		t.Fatalf("activating %q did not open a list", controlID)
	}
	target := -1
	for i, option := range m.dropdown.options {
		if option.id == optionID {
			target = i
		}
	}
	if target < 0 {
		t.Fatalf("%q does not offer %q: %#v", controlID, optionID, m.dropdown.options)
	}
	for guard := 0; m.dropdown.cursor != target; guard++ {
		if guard > len(m.dropdown.options)+2 {
			t.Fatalf("the cursor would not move to %q", optionID)
		}
		code := tea.KeyDown
		if m.dropdown.cursor > target {
			code = tea.KeyUp
		}
		m.Key(tea.KeyPressMsg{Code: code})
	}
	_, cmd := m.Key(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.dropdown != nil {
		t.Fatal("Enter left the list open")
	}
	applySave(t, m, cmd)
	m.View(160, 45)
}

// Activating a selector opens a list rather than stepping to the next value,
// Enter commits what the cursor is on, and Escape leaves the setting alone.
func TestDropdownOpensCommitsAndCancels(t *testing.T) {
	m := workspaceFixture(t, nil)
	m.Open(PageWorkspaces)

	activate(t, m, regionOverviewScope)
	if m.dropdown == nil || m.dropdown.controlID != regionOverviewScope {
		t.Fatalf("activating the selector did not open its list: %#v", m.dropdown)
	}
	if got := loadSaved(t).Plugins.Workspace.OverviewWorktreeScope; got == config.OverviewWorktreeScopeWorktree {
		t.Fatal("opening the list changed the setting")
	}
	view := ansi.Strip(m.View(160, 45))
	if !strings.Contains(view, overviewProjectLabel) || !strings.Contains(view, overviewWorktreeLabel) {
		t.Fatalf("the open list did not show both choices:\n%s", view)
	}

	// Escape closes it without committing.
	if !m.Escape() {
		t.Fatal("Escape did not answer for the open list")
	}
	if m.dropdown != nil {
		t.Fatal("Escape left the list open")
	}
	if got := loadSaved(t).Plugins.Workspace.OverviewWorktreeScope; got == config.OverviewWorktreeScopeWorktree {
		t.Fatalf("Escape committed a value: %q", got)
	}

	// Enter on the moved cursor commits.
	choose(t, m, regionOverviewScope, config.OverviewWorktreeScopeWorktree)
	if got := loadSaved(t).Plugins.Workspace.OverviewWorktreeScope; got != config.OverviewWorktreeScopeWorktree {
		t.Fatalf("Enter did not commit the chosen value: %q", got)
	}
}

// An open list is drawn over the content behind it: the lines under the control
// still exist, but the cells the list covers are the list's.
func TestDropdownOverlaysContentBehindIt(t *testing.T) {
	m := workspaceFixture(t, nil)
	m.Open(PageWorkspaces)
	before := ansi.Strip(m.View(160, 45))
	covered := "Repo name prefix"
	if !strings.Contains(before, covered) {
		t.Fatalf("the fixture page does not contain %q:\n%s", covered, before)
	}
	beforeLines := len(strings.Split(before, "\n"))

	activate(t, m, regionDefaultAgent)
	after := ansi.Strip(m.View(160, 45))
	afterLines := strings.Split(after, "\n")
	if len(afterLines) != beforeLines {
		t.Fatalf("opening the list changed the pane height: %d → %d", beforeLines, len(afterLines))
	}
	if !strings.Contains(after, noneAgentLabel) {
		t.Fatalf("the open list is not painted:\n%s", after)
	}
	// The rows the list covers are the list's, not the page's.
	row := m.dropdown.row + 1
	if row >= len(afterLines) {
		t.Fatalf("the list was placed off the pane at row %d", row)
	}
	overlaid := afterLines[row]
	if !strings.Contains(overlaid, noneAgentLabel) {
		t.Fatalf("the row under the control does not hold the list:\n%s", overlaid)
	}
	// The setting whose row the list sits over is still on the page underneath
	// — the list floats, it does not push content away.
	if !strings.Contains(after, covered) {
		t.Fatalf("opening the list scrolled the page: %q disappeared", covered)
	}
}

// The list answers the mouse: a click on a row commits it, and a click
// somewhere else puts the list away without acting on what was clicked.
func TestDropdownMouseSelectsAndDismisses(t *testing.T) {
	m := workspaceFixture(t, nil)
	m.Open(PageWorkspaces)
	activate(t, m, regionOverviewScope)
	m.View(160, 45)

	index := -1
	for i, option := range m.dropdown.options {
		if option.id == config.OverviewWorktreeScopeWorktree {
			index = i
		}
	}
	region := regionFor(t, m, fmt.Sprintf("%s%d", regionDropdownItem, index))
	cmd := m.Mouse(tea.MouseClickMsg{X: region.Rect.X + 2, Y: region.Rect.Y, Button: tea.MouseLeft})
	if m.dropdown != nil {
		t.Fatal("clicking a row left the list open")
	}
	applySave(t, m, cmd)
	if got := loadSaved(t).Plugins.Workspace.OverviewWorktreeScope; got != config.OverviewWorktreeScopeWorktree {
		t.Fatalf("clicking a row saved %q", got)
	}

	// A click outside dismisses, and does not also run what it landed on.
	activate(t, m, regionDefaultAgent)
	m.View(160, 45)
	before := loadSaved(t).Plugins.Workspace.AutoCreateShell
	other := regionFor(t, m, regionAutoShell)
	m.Mouse(tea.MouseClickMsg{X: other.Rect.X + 1, Y: other.Rect.Y, Button: tea.MouseLeft})
	if m.dropdown != nil {
		t.Fatal("a click outside the list did not dismiss it")
	}
	if loadSaved(t).Plugins.Workspace.AutoCreateShell != before {
		t.Fatal("the dismissing click also toggled the control underneath")
	}
}

// A list longer than its window scrolls under the wheel, as the theme list
// does, and the selection stays inside the window it scrolled to.
func TestDropdownScrollsWithTheWheel(t *testing.T) {
	m := workspaceFixture(t, nil)
	m.Open(PageTerminal)
	activate(t, m, regionCaptureLimit)
	m.View(160, 45)
	if len(m.dropdown.options) <= dropdownMaxVisible {
		t.Skip("the capture-limit ladder is shorter than the window")
	}

	region := regionFor(t, m, fmt.Sprintf("%s%d", regionDropdownItem, m.dropdown.scroll))
	x, y := region.Rect.X+2, region.Rect.Y
	for i := 0; i < 6; i++ {
		m.Mouse(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelDown})
		m.View(160, 45)
	}
	if m.dropdown == nil {
		t.Fatal("the wheel closed the list")
	}
	if m.dropdown.scroll == 0 {
		t.Fatalf("the wheel did not scroll the list: cursor=%d scroll=%d", m.dropdown.cursor, m.dropdown.scroll)
	}
	if m.dropdown.cursor < m.dropdown.scroll || m.dropdown.cursor >= m.dropdown.scroll+m.dropdown.visibleRows() {
		t.Fatalf("the wheel scrolled the selection out of sight: cursor=%d scroll=%d", m.dropdown.cursor, m.dropdown.scroll)
	}
}

// While a list is open the keys that would close Configuration belong to the
// list: q and , must not take the whole surface away from a user who is still
// choosing.
func TestDropdownConsumesSurfaceClosingKeys(t *testing.T) {
	m := workspaceFixture(t, nil)
	m.Open(PageWorkspaces)
	activate(t, m, regionOverviewScope)

	for _, key := range []rune{'q', ',', '/'} {
		handled, _ := m.Key(tea.KeyPressMsg{Code: key, Text: string(key)})
		if !handled {
			t.Fatalf("%q fell through an open list", string(key))
		}
		if m.dropdown == nil {
			t.Fatalf("%q closed the list", string(key))
		}
	}
	if m.SearchFocused() {
		t.Fatal("a key reached Search through the open list")
	}
}

// The list survives a frame that builds the pane twice, and its rows are
// registered once: a doubled hit region is a click that lands on the wrong row.
func TestDropdownRegistersItsRowsOnce(t *testing.T) {
	m := workspaceFixture(t, nil)
	m.Open(PageWorkspaces)
	activate(t, m, regionDefaultAgent)
	// A pending focus request is what makes buildDetail run twice.
	m.pendingFocus = regionOverviewScope
	m.View(160, 45)

	if m.dropdown == nil {
		t.Fatal("a second build pass closed the list")
	}
	seen := map[string]int{}
	for _, region := range m.mouse.HitMap.Regions() {
		if strings.HasPrefix(region.ID, regionDropdownItem) {
			seen[region.ID]++
		}
	}
	if len(seen) == 0 {
		t.Fatal("the open list registered no hit regions")
	}
	for id, count := range seen {
		if count != 1 {
			t.Fatalf("region %q registered %d times", id, count)
		}
	}
}

// A list belongs to the control it hangs from: leaving the page takes it away
// rather than leaving it floating over somewhere else.
func TestDropdownClosesWhenItsPageIsLeft(t *testing.T) {
	m := workspaceFixture(t, nil)
	m.Open(PageWorkspaces)
	activate(t, m, regionOverviewScope)
	m.Navigate(PageTerminal)
	m.View(160, 45)
	if m.dropdown != nil {
		t.Fatal("navigating away left a list open")
	}
}

// Every selector site saves through the same mechanism.
func TestPanelRefreshSelectorSaves(t *testing.T) {
	features.Init(config.Default())
	m := workspaceFixture(t, func(cfg *config.Config) {
		cfg.Plugins.GitStatus.Enabled = true
	})
	m.Open(PagePanels)
	choose(t, m, regionPanelGitRefresh, (15 * time.Second).String())
	if got := loadSaved(t).Plugins.GitStatus.RefreshInterval; got != 15*time.Second {
		t.Fatalf("the refresh interval saved %s", got)
	}
}

func TestCaptureLimitSelectorSaves(t *testing.T) {
	m := workspaceFixture(t, nil)
	m.Open(PageTerminal)
	choose(t, m, regionCaptureLimit, fmt.Sprintf("%d", 8*1024*1024))
	if got := loadSaved(t).Plugins.Workspace.TmuxCaptureMaxBytes; got != 8*1024*1024 {
		t.Fatalf("the preview limit saved %d", got)
	}
}

func TestDefaultAgentSelectorSaves(t *testing.T) {
	m := workspaceFixture(t, func(cfg *config.Config) {
		cfg.Plugins.Workspace.Agents = []string{"claude", "grok"}
	})
	m.Open(PageWorkspaces)
	choose(t, m, regionDefaultAgent, "grok")
	if got := loadSaved(t).Plugins.Workspace.DefaultAgentType; got != "grok" {
		t.Fatalf("the default agent saved %q", got)
	}
	// The closed control shows the value that was chosen.
	if view := ansi.Strip(m.View(160, 45)); !strings.Contains(view, "Grok") {
		t.Fatalf("the closed selector does not show the chosen value:\n%s", view)
	}
}

// Appearance's scope pill is a select control too: choosing a project from its
// list is what moves a theme save onto that project.
func TestThemeScopeSelectorChoosesAProject(t *testing.T) {
	dir := t.TempDir()
	other := t.TempDir()
	cfg := config.Default()
	cfg.Projects.List = []config.ProjectConfig{
		{Name: "fixture", Path: dir},
		{Name: "second", Path: other},
	}
	m, _ := configFixture(t, cfg)
	m.SetHostState(HostState{Config: loadSaved(t), ProjectDir: dir, ProjectPath: dir})
	m.Open(PageAppearance)

	activate(t, m, regionScopeProject)
	if m.dropdown == nil {
		t.Fatal("the scope pill did not open a list")
	}
	view := ansi.Strip(m.View(160, 45))
	if !strings.Contains(view, "second") {
		t.Fatalf("the open list does not offer the other project:\n%s", view)
	}

	choose(t, m, regionScopeProject, other)
	state := m.appearance()
	if !state.projectScope || state.projectPath != other {
		t.Fatalf("choosing a project left scope=%v path=%q", state.projectScope, state.projectPath)
	}
}

// A configured value the ladder does not offer is reported as it is stored. The
// closed control used to claim the first rung, so Terminal said "256 KB" for a
// 3 MB limit typed on Advanced — and opening the list parked the cursor there,
// turning a stray Enter into a silent downgrade.
func TestCaptureLimitReportsAnOffLadderValue(t *testing.T) {
	features.Init(config.Default())
	m := workspaceFixture(t, func(cfg *config.Config) {
		cfg.Plugins.Workspace.TmuxCaptureMaxBytes = 3 * 1024 * 1024
	})
	m.Open(PageTerminal)
	view := ansi.Strip(m.View(160, 45))
	if !strings.Contains(view, "3 MB") {
		t.Fatalf("the closed control does not report the configured limit:\n%s", view)
	}
	if strings.Contains(view, "Preview limit               256 KB") {
		t.Fatalf("the closed control claimed the first rung:\n%s", view)
	}

	// Opening the list parks the cursor on the nearest rung at or below the
	// stored value, never on the smallest one: Enter must not be a silent
	// downgrade to 256 KB.
	activate(t, m, regionCaptureLimit)
	option, ok := m.dropdown.selected()
	if !ok {
		t.Fatal("the list opened with nothing selected")
	}
	if option.id != strconv.Itoa(2*1024*1024) {
		t.Fatalf("the list opened on %q, want the 2 MB rung", option.id)
	}
}

// dropdownLabel never claims an option the configuration does not hold.
func TestDropdownLabelDoesNotClaimTheFirstOption(t *testing.T) {
	options := []dropdownOption{{id: "a", label: "Apple"}, {id: "b", label: "Pear"}}
	if got := dropdownLabel(options, "b"); got != "Pear" {
		t.Fatalf("dropdownLabel(b) = %q", got)
	}
	if got := dropdownLabel(options, "zz"); got != "zz" {
		t.Fatalf("an unknown value was reported as %q, want it reported as stored", got)
	}
	if got := dropdownLabel(nil, ""); got != "" {
		t.Fatalf("dropdownLabel with no options = %q", got)
	}
}

// A pane too narrow to paint the list closes it instead of leaving an invisible
// list swallowing every key.
func TestDropdownClosesWhenThePaneIsTooNarrow(t *testing.T) {
	m := workspaceFixture(t, nil)
	m.Open(PageWorkspaces)
	activate(t, m, regionOverviewScope)
	if m.dropdown == nil {
		t.Fatal("the list did not open")
	}
	m.View(61, 30)
	if m.dropdown != nil {
		t.Fatal("a list with nowhere to be painted stayed open")
	}
	// The surface answers again: the keys are no longer being swallowed.
	handled, _ := m.Key(tea.KeyPressMsg{Code: tea.KeyDown})
	if !handled {
		t.Fatal("the surface stopped answering after the narrow render")
	}
}

// A pane too short to hold the control closes the list rather than pinning it
// over unrelated rows — where its regions would commit a value for a control
// the user cannot even see.
func TestDropdownClosesWhenItsControlScrollsOffAShortPane(t *testing.T) {
	features.Init(config.Default())
	m := workspaceFixture(t, nil)
	m.Open(PageTerminal)
	activate(t, m, regionCaptureLimit)
	m.View(160, 45)
	if m.dropdown == nil {
		t.Fatal("the list did not open")
	}

	m.View(160, 12)
	if m.dropdown != nil {
		t.Fatal("the list stayed open with its control off the pane")
	}
	for _, region := range m.mouse.HitMap.Regions() {
		if strings.HasPrefix(region.ID, regionDropdownItem) {
			t.Fatalf("a closed list left a live region at %#v", region.Rect)
		}
	}
}

// Every region an open list registers is on a row the pane actually paints.
func TestDropdownRegionsStayInsideThePane(t *testing.T) {
	features.Init(config.Default())
	m := workspaceFixture(t, nil)
	m.Open(PageTerminal)
	activate(t, m, regionCaptureLimit)
	rendered := ansi.Strip(m.View(160, 20))
	painted := len(strings.Split(rendered, "\n"))
	for _, region := range m.mouse.HitMap.Regions() {
		if !strings.HasPrefix(region.ID, regionDropdownItem) && region.ID != regionDropdownMore {
			continue
		}
		if region.Rect.Y > painted {
			t.Fatalf("region %q sits at row %d of a %d-row pane", region.ID, region.Rect.Y, painted)
		}
	}
}

// The wheel belongs entirely to an open list: a notch that misses it must not
// move the theme list's cursor out from under the control the list hangs from.
func TestDropdownSwallowsTheWheel(t *testing.T) {
	cfg := config.Default()
	cfg.UI.Theme = config.ThemeConfig{Name: "default"}
	dir := t.TempDir()
	cfg.Projects.List = []config.ProjectConfig{{Name: "fixture", Path: dir}}
	m, _ := configFixture(t, cfg)
	m.SetHostState(HostState{Config: loadSaved(t), ProjectDir: dir, ProjectPath: dir})
	m.Open(PageAppearance)
	m.View(160, 45)

	activate(t, m, regionScopeProject)
	m.View(160, 45)
	picker := m.activePicker()
	before := picker.cursor
	row := regionFor(t, m, fmt.Sprintf("%s%d", regionThemeRow, picker.scroll))
	for i := 0; i < 3; i++ {
		m.Mouse(tea.MouseWheelMsg{X: row.Rect.X + 4, Y: row.Rect.Y, Button: tea.MouseWheelDown})
		m.View(160, 45)
	}
	if picker.cursor != before {
		t.Fatalf("a wheel notch moved the theme list from %d to %d while a list was open", before, picker.cursor)
	}
	if m.dropdown == nil {
		t.Fatal("the wheel closed the open list")
	}
}

// A pane too small to paint anything at all cannot hold a list either.
func TestDropdownClosesWhenThePaneIsDropped(t *testing.T) {
	m := workspaceFixture(t, nil)
	m.Open(PageWorkspaces)
	activate(t, m, regionOverviewScope)
	m.View(6, 2) // below the surface's minimum: nothing is painted
	if m.dropdown != nil {
		t.Fatal("a list survived a pane that painted nothing")
	}

	m.Open(PageWorkspaces)
	activate(t, m, regionOverviewScope)
	m.View(24, 40) // sidebar only: the detail pane is dropped
	if m.dropdown != nil {
		t.Fatal("a list survived the detail pane being dropped")
	}
}
