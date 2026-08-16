package configui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestSearchMatchesLabelPageAndKeywords(t *testing.T) {
	cases := map[string]PageID{
		"tmux":      PageSetup,      // keyword and label
		"nerd":      PageAppearance, // label
		"clipboard": PageTerminal,   // keyword only
	}
	for query, wantPage := range cases {
		matches := Search(query)
		if len(matches) == 0 {
			t.Fatalf("query %q matched nothing", query)
		}
		found := false
		for _, entry := range matches {
			if entry.Page == wantPage {
				found = true
			}
		}
		if !found {
			t.Fatalf("query %q did not reach %q: %#v", query, wantPage, matches)
		}
	}
	if got := Search("   "); got != nil {
		t.Fatalf("blank query matched %d entries", len(got))
	}
	if got := Search("zzzznotasetting"); got != nil {
		t.Fatalf("nonsense query matched %d entries", len(got))
	}
}

func TestSearchPagesFollowSidebarOrder(t *testing.T) {
	pages := SearchPages("tmux")
	order := map[PageID]int{}
	for i, page := range AllPages() {
		order[page.ID] = i
	}
	for i := 1; i < len(pages); i++ {
		if order[pages[i-1]] >= order[pages[i]] {
			t.Fatalf("filtered pages are out of sidebar order: %v", pages)
		}
	}
}

func TestEveryIndexEntryPointsAtARealPage(t *testing.T) {
	for _, entry := range Index() {
		if PageTitle(entry.Page) == "" {
			t.Fatalf("index entry %q points at unknown page %q", entry.Label, entry.Page)
		}
	}
}

// A child route keeps its parent's sidebar destination and returns to it.
func TestChildRouteReturnsToParent(t *testing.T) {
	m := New()
	m.Open(PageSetup)
	m.PushChild("repair-tmux", "Set up tmux")

	if route := m.Route(); !route.IsChild() || route.Title != "Set up tmux" {
		t.Fatalf("child route = %#v", route)
	}
	if m.Page() != PageSetup {
		t.Fatalf("child route moved the sidebar destination to %q", m.Page())
	}

	view := ansi.Strip(m.View(160, 40))
	if !strings.Contains(view, "Back to Sidecar Setup") {
		t.Fatalf("child route did not render its parent-return control:\n%s", view)
	}

	if !m.Back() {
		t.Fatal("Back() refused to return from a child route")
	}
	if m.Route().IsChild() || m.Page() != PageSetup {
		t.Fatalf("Back() landed on %#v", m.Route())
	}
	if m.Back() {
		t.Fatal("Back() popped past the page route")
	}
}

// Navigating to another destination abandons an open child route rather than
// nesting under it.
func TestNavigateAbandonsChildRoute(t *testing.T) {
	m := New()
	m.Open(PageSetup)
	m.PushChild("repair-tmux", "Set up tmux")
	m.Navigate(PageTerminal)
	if m.Route().IsChild() || m.Page() != PageTerminal {
		t.Fatalf("route after navigate = %#v", m.Route())
	}
}

func TestOpensOnSidebarNotSearch(t *testing.T) {
	m := New()
	m.Open(PageSetup)
	if m.SearchFocused() {
		t.Fatal("Configuration opened with Search focused")
	}
	if m.FocusContext() != ContextConfig {
		t.Fatalf("focus context = %q", m.FocusContext())
	}

	handled, _ := m.Key(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if !handled || m.cursor != 1 {
		t.Fatalf("j did not move the sidebar cursor: handled=%v cursor=%d", handled, m.cursor)
	}

	handled, _ = m.Key(tea.KeyPressMsg{Code: '/', Text: "/"})
	if !handled || !m.SearchFocused() || m.FocusContext() != ContextConfigEdit {
		t.Fatalf("slash did not focus Search: focused=%v ctx=%q", m.SearchFocused(), m.FocusContext())
	}
}

// Up from the first filtered result returns to Search; Down leaves it again.
func TestSearchAndListExchangeFocus(t *testing.T) {
	m := New()
	m.Open(PageSetup)
	m.Key(tea.KeyPressMsg{Code: '/', Text: "/"})
	for _, r := range "tmux" {
		m.Key(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if !m.SearchActive() || m.Query() != "tmux" {
		t.Fatalf("query = %q", m.Query())
	}

	m.Key(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.SearchFocused() || m.cursor != 0 {
		t.Fatalf("down did not land on the first result: focused=%v cursor=%d", m.SearchFocused(), m.cursor)
	}
	m.Key(tea.KeyPressMsg{Code: tea.KeyUp})
	if !m.SearchFocused() {
		t.Fatal("up from the first result did not return to Search")
	}

	m.ClearSearch()
	if m.SearchActive() || m.SearchFocused() {
		t.Fatal("ClearSearch left the query or the focus behind")
	}
	if len(m.visiblePages()) != len(AllPages()) {
		t.Fatal("ClearSearch did not restore the full sidebar")
	}
}

// The view must never exceed its allocated box, at any size.
func TestViewRespectsItsBox(t *testing.T) {
	m := New()
	m.Open(PageSetup)
	for _, size := range [][2]int{{60, 24}, {100, 30}, {200, 50}, {30, 10}} {
		view := m.View(size[0], size[1])
		lines := strings.Split(view, "\n")
		if len(lines) != size[1] {
			t.Fatalf("size=%v height = %d", size, len(lines))
		}
		for i, line := range lines {
			if width := ansi.StringWidth(line); width > size[0] {
				t.Fatalf("size=%v line %d width = %d", size, i, width)
			}
		}
	}
}

// Search holds the keyboard even with nothing typed into it. Escape there means
// "leave the field", not "close Configuration": the sidebar gets the keyboard
// back and the surface stays open.
func TestEscapeFromEmptySearchReturnsToTheSidebar(t *testing.T) {
	m := New()
	m.Open(PageSetup)
	m.Key(tea.KeyPressMsg{Code: '/', Text: "/"})
	if !m.SearchFocused() {
		t.Fatal("slash did not focus Search")
	}
	if !m.Escape() {
		t.Fatal("Escape from an empty Search asked the host to close Configuration")
	}
	if m.SearchFocused() {
		t.Fatal("Escape left the keyboard in Search")
	}
	if m.FocusContext() != ContextConfig {
		t.Fatalf("focus context after Escape = %q", m.FocusContext())
	}
	// With the sidebar focused and nothing else on screen, the next Escape is
	// the host's signal to close.
	if m.Escape() {
		t.Fatal("the second Escape did not reach the host")
	}
}

// Arrowing the sidebar is the same move as clicking a section: the detail pane
// follows immediately, and the keyboard stays in the navigation pane.
func TestSidebarArrowsNavigateImmediately(t *testing.T) {
	m := New()
	m.Open(PageSetup)
	pages := m.visiblePages()

	m.Key(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.cursor != 1 {
		t.Fatalf("down moved the cursor to %d, want 1", m.cursor)
	}
	if m.Page() != pages[1] {
		t.Fatalf("down left the detail pane on %q, want %q", m.Page(), pages[1])
	}
	if m.detailFocus || m.SearchFocused() {
		t.Fatalf("down moved focus out of the sidebar: detail=%v search=%v", m.detailFocus, m.SearchFocused())
	}

	m.Key(tea.KeyPressMsg{Code: tea.KeyUp})
	if m.cursor != 0 || m.Page() != pages[0] {
		t.Fatalf("up landed on cursor=%d page=%q", m.cursor, m.Page())
	}

	// Enter on the destination already showing moves into it rather than
	// navigating to where the user already is. Walk to a page with controls on
	// it, since a page with none has nothing to move into.
	for m.cursor < len(pages)-1 {
		m.View(120, 40)
		if m.hasDetailControls() {
			break
		}
		m.Key(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	m.View(120, 40)
	if !m.hasDetailControls() {
		t.Fatal("no destination rendered a control to move into")
	}
	page := m.Page()
	m.Key(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.detailFocus {
		t.Fatal("enter on the current destination did not move into the page")
	}
	if m.Page() != page {
		t.Fatalf("enter navigated away to %q", m.Page())
	}
}

// A query is the exception: the detail pane belongs to the results while one is
// active, so the cursor steps through matches and Enter opens the one it is on.
func TestSearchResultArrowsStillNeedEnter(t *testing.T) {
	m := New()
	m.Open(PageSetup)
	m.Key(tea.KeyPressMsg{Code: '/', Text: "/"})
	for _, r := range "tmux" {
		m.Key(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	m.Key(tea.KeyPressMsg{Code: tea.KeyDown}) // search -> first result
	before := m.Page()
	m.Key(tea.KeyPressMsg{Code: tea.KeyDown})
	if !m.results {
		t.Fatal("arrowing the results replaced them with a page")
	}
	if m.Page() != before {
		t.Fatalf("arrowing the results navigated to %q", m.Page())
	}
	m.Key(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.results {
		t.Fatal("enter on a result did not open it")
	}
	if m.Page() != m.visiblePages()[m.cursor] {
		t.Fatalf("enter opened %q, want %q", m.Page(), m.visiblePages()[m.cursor])
	}
}

// Reopen puts the user back where they were; the first open of a session and an
// explicitly named page both ignore it.
func TestReopenRestoresTheLastPosition(t *testing.T) {
	m := New()
	m.Reopen()
	if m.Page() != DefaultPage {
		t.Fatalf("first Reopen of the session opened %q, want the default", m.Page())
	}

	m.Key(tea.KeyPressMsg{Code: tea.KeyDown})
	page, cursor := m.Page(), m.cursor
	m.Close()

	m.Reopen()
	if m.Page() != page || m.cursor != cursor {
		t.Fatalf("Reopen landed on %q/%d, want %q/%d", m.Page(), m.cursor, page, cursor)
	}
	m.Open(PageAbout)
	if m.Page() != PageAbout {
		t.Fatalf("a named open landed on %q", m.Page())
	}
}

// The detail pane's selection is restored with the focus that gives it meaning:
// a remembered row nothing is looking at would be state that lies.
func TestReopenRestoresTheDetailSelection(t *testing.T) {
	m := New()
	m.Open(PageAppearance)
	m.View(120, 40)
	if !m.hasDetailControls() {
		t.Fatal("Appearance rendered no controls to select")
	}
	m.Key(tea.KeyPressMsg{Code: tea.KeyTab}) // into the page
	m.Key(tea.KeyPressMsg{Code: tea.KeyDown})
	m.View(120, 40)
	if !m.detailFocus || m.rowCursor == 0 {
		t.Fatalf("setup did not land inside the page: detail=%v row=%d", m.detailFocus, m.rowCursor)
	}
	row := m.rowCursor

	m.Close()
	m.Reopen()
	m.View(120, 40)
	if !m.detailFocus || m.rowCursor != row {
		t.Fatalf("Reopen restored detail=%v row=%d, want true/%d", m.detailFocus, m.rowCursor, row)
	}
}

// A remembered position is a page, never an index into a list a query was
// filtering: reopening after closing mid-search must not leave the sidebar
// highlighting one destination while the detail pane shows another.
func TestReopenAfterClosingMidSearchAgreesWithTheDetailPane(t *testing.T) {
	m := New()
	m.Open(PageSetup)
	m.Key(tea.KeyPressMsg{Code: '/', Text: "/"})
	for _, r := range "theme" {
		m.Key(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	m.Key(tea.KeyPressMsg{Code: tea.KeyDown}) // search -> first result
	m.Key(tea.KeyPressMsg{Code: tea.KeyDown}) // second result
	if m.cursor == 0 || m.Page() != PageSetup {
		t.Fatalf("setup: cursor=%d page=%q, want a moved cursor on the unchanged page", m.cursor, m.Page())
	}

	m.Close()
	m.Reopen()
	if m.SearchActive() {
		t.Fatal("Reopen carried the query across")
	}
	pages := m.visiblePages()
	if pages[m.cursor] != m.Page() {
		t.Fatalf("sidebar highlights %q while the detail pane shows %q", pages[m.cursor], m.Page())
	}
}
