package configui

import (
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
)

// These lock in the defects the tmux-drive proof of the Configuration surface
// found (td-fd3796). Each one is a thing the surface claimed on screen and did
// not do.

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;:?]*[a-zA-Z]`)

// paintedRow finds the content-area row a piece of text was painted on, in the
// same coordinates the host forwards mouse events in.
func paintedRow(t *testing.T, view, needle string) int {
	t.Helper()
	for y, line := range strings.Split(view, "\n") {
		if strings.Contains(ansiPattern.ReplaceAllString(line, ""), needle) {
			return y
		}
	}
	t.Fatalf("%q was never painted:\n%s", needle, ansiPattern.ReplaceAllString(view, ""))
	return -1
}

// A section header carries its own blank line, so it paints two rows. Counting
// it as one moved every control below it out from under the mouse.
func TestHitRegionsLandOnTheRowsTheyArePaintedOn(t *testing.T) {
	m, _ := configFixture(t, config.Default())
	m.Open(PageAppearance)
	view := m.View(160, 45)

	row := paintedRow(t, view, "Header clock")
	var found bool
	for _, region := range m.mouse.HitMap.Regions() {
		if region.ID != regionClock {
			continue
		}
		found = true
		if row < region.Rect.Y || row >= region.Rect.Y+region.Rect.H {
			t.Fatalf("Header clock painted on row %d, clickable on rows %d-%d",
				row, region.Rect.Y, region.Rect.Y+region.Rect.H-1)
		}
	}
	if !found {
		t.Fatal("Header clock registered no hit region at all")
	}
}

// The mockups print the shortcut as a capital in every control label while the
// footer advertises the lowercase key. Both have to work.
func TestControlKeysAnswerToTheCapitalTheirLabelPrints(t *testing.T) {
	m, _ := configFixture(t, config.Default())
	m.Open(PageProjects)
	m.View(160, 45)

	handled, _ := m.Key(tea.KeyPressMsg{Code: 'A', Text: "A"})
	if !handled {
		t.Fatal("A did nothing on a page whose control is labelled \"A  Add project\"")
	}
	if !m.Route().IsChild() {
		t.Fatalf("A did not open Add project; route = %+v", m.Route())
	}
}

// Down out of Search lands on the first match, which is what the results pane
// tells the user it does — not on wherever clamping leaves a stale cursor.
func TestDownFromSearchLandsOnTheFirstMatch(t *testing.T) {
	m, _ := configFixture(t, config.Default())
	m.Open(PageAbout)
	m.View(160, 45)
	m.focusSearch()
	m.search.SetValue("tmux")

	m.Key(tea.KeyPressMsg{Code: tea.KeyDown})
	pages := m.visiblePages()
	if len(pages) == 0 {
		t.Fatal("the query matched no pages")
	}
	if m.cursor != 0 {
		t.Fatalf("cursor = %d (%q), want the first match %q", m.cursor, pages[m.cursor], pages[0])
	}
}

// Handing the keyboard back to navigation has to take it away from the detail
// pane, or Enter runs a control the cursor is no longer on.
func TestFocusingNavigationReleasesTheDetailPane(t *testing.T) {
	m, _ := configFixture(t, config.Default())
	m.Open(PageAppearance)
	m.View(160, 45)
	m.detailFocus = true

	m.focusSearch()
	if m.detailOwnsKeys() {
		t.Fatal("Search has the keyboard but the detail pane still owns Enter")
	}

	m.detailFocus = true
	m.focusSidebarList()
	if m.detailOwnsKeys() {
		t.Fatal("the sidebar has the keyboard but the detail pane still owns Enter")
	}
}

// Reordering moves the selection with the project. The detail block and Remove
// both act on the selection, so a selection that stays on an index removes the
// neighbour of the project the user just moved.
func TestReorderKeepsTheSelectionOnTheMovedProject(t *testing.T) {
	cfg := config.Default()
	cfg.Projects.List = []config.ProjectConfig{
		{Name: "alpha", Path: "/tmp/alpha"},
		{Name: "beta", Path: "/tmp/beta"},
	}
	m, _ := configFixture(t, cfg)
	m.Open(PageProjects)
	m.View(160, 45)
	m.projectsPage().cursor = 1

	run(t, m, m.moveSelectedProject(-1))
	m.SetHostState(HostState{Config: loadSaved(t)})
	m.View(160, 45)

	selected := m.selectedProject()
	if selected == nil || selected.Name != "beta" {
		t.Fatalf("selection after reorder = %+v, want beta", selected)
	}
}

// The page's own keys are advertised in copy that is on screen whichever pane
// holds the cursor, so they answer from either.
func TestReorderAnswersWhileNavigationHasTheCursor(t *testing.T) {
	cfg := config.Default()
	cfg.Projects.List = []config.ProjectConfig{
		{Name: "alpha", Path: "/tmp/alpha"},
		{Name: "beta", Path: "/tmp/beta"},
	}
	m, _ := configFixture(t, cfg)
	m.Open(PageProjects)
	m.View(160, 45)
	m.detailFocus = false
	m.projectsPage().cursor = 1

	handled, cmd := m.key(tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModShift})
	if !handled || cmd == nil {
		t.Fatal("shift+up did nothing while the sidebar held the cursor")
	}
}

// A control never renders wider than the pane it is in: a narrow terminal gets
// a smaller field, not a field with its right half cut off.
func TestControlsFitTheirPane(t *testing.T) {
	if got := ControlWidth(80, 48); got != 48 {
		t.Fatalf("a wide pane shrank a control: %d", got)
	}
	if got := ControlWidth(50, 48); got != 50-ControlColumn {
		t.Fatalf("a narrow pane kept the declared width: %d", got)
	}
	if got := ControlWidth(20, 48); got < minControlWidth {
		t.Fatalf("a tiny pane produced an unusable control: %d", got)
	}
}

// Help wraps to the pane instead of being cut off mid-sentence, and every line
// stays in the control column.
func TestHelpWrapsInsideThePane(t *testing.T) {
	text := "Read once when Sidecar starts, so a change takes effect after a restart."
	wrapped := WrapAt(text, 60, ControlColumn, Muted)
	lines := strings.Split(wrapped, "\n")
	if len(lines) < 2 {
		t.Fatalf("help did not wrap: %q", wrapped)
	}
	var rebuilt []string
	for _, line := range lines {
		plain := ansiPattern.ReplaceAllString(line, "")
		if len(plain) > 60 {
			t.Fatalf("wrapped line ran past the pane: %q", plain)
		}
		if !strings.HasPrefix(plain, strings.Repeat(" ", ControlColumn)) {
			t.Fatalf("wrapped line left the control column: %q", plain)
		}
		rebuilt = append(rebuilt, strings.TrimSpace(plain))
	}
	if strings.Join(rebuilt, " ") != text {
		t.Fatalf("wrapping changed the sentence: %q", strings.Join(rebuilt, " "))
	}
}
