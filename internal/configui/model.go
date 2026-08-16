package configui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/styles"
)

// Keymap contexts owned by Configuration. They are registered in
// internal/keymap/bindings.go, which is what drives the footer, the help modal,
// and the command palette.
const (
	// ContextConfig is sidebar navigation and page-level actions.
	ContextConfig = "config"
	// ContextConfigEdit is an active editor — Search, or any future field.
	// Registered in the host's isTextInputContext so typing never leaks.
	ContextConfigEdit = "config-edit"
	// ContextConfigConfirm is a consequential change awaiting confirmation.
	ContextConfigConfirm = "config-confirm"
)

// Mouse region IDs. Later phases add their own with the same prefix.
const (
	regionSearch    = "config-search"
	regionNavPrefix = "config-nav-"
	regionResult    = "config-result-"
	regionBack      = "config-back"
)

const (
	sidebarPreferredWidth = 39
	sidebarMinWidth       = 22
)

// Model is the Configuration surface. It owns navigation, search, and the
// per-surface mouse hit map; it owns no configuration data.
type Model struct {
	router  *router
	search  textinput.Model
	focus   focusTarget
	results bool // the detail pane is showing search results

	cursor int // index into visiblePages()

	width, height int
	sidebarWidth  int

	mouse   *mouse.Handler
	hoverID string
}

type focusTarget uint8

const (
	focusSidebar focusTarget = iota
	focusSearch
)

// New builds the surface. It performs no I/O, so it is safe on the startup
// path; nothing here runs until the user opens Configuration.
func New() *Model {
	input := textinput.New()
	input.Placeholder = "Find a setting…"
	input.Prompt = ""
	m := &Model{
		router: newRouter(DefaultPage),
		search: input,
		mouse:  mouse.NewHandler(),
	}
	return m
}

// Open resets the surface onto a destination. Configuration always opens with
// the sidebar focused and no query, so Up/Down works immediately.
func (m *Model) Open(page PageID) {
	if PageTitle(page) == "" {
		page = DefaultPage
	}
	m.router = newRouter(page)
	m.search.SetValue("")
	m.search.Blur()
	m.focus = focusSidebar
	m.results = false
	m.hoverID = ""
	m.cursor = indexOfPage(m.visiblePages(), page)
}

// Page is the destination the sidebar highlights.
func (m *Model) Page() PageID { return m.router.page() }

// Route is the visible route, including any focused child.
func (m *Model) Route() Route { return m.router.current() }

// Navigate moves to a sidebar destination, abandoning any child route.
func (m *Model) Navigate(page PageID) {
	m.router.navigate(page)
	m.cursor = indexOfPage(m.visiblePages(), page)
	m.results = false
}

// PushChild opens a focused child route with parent-return behavior.
func (m *Model) PushChild(child ChildID, title string) { m.router.push(child, title) }

// Back returns from a focused child route to its parent. It reports false when
// there is nothing to return to.
func (m *Model) Back() bool { return m.router.back() }

// SearchFocused reports that Search has the keyboard, so every printable key
// belongs to it.
func (m *Model) SearchFocused() bool { return m.focus == focusSearch }

// SearchActive reports that a query is still narrowing the sidebar, whether or
// not the input has focus. Escape clears that before it closes Configuration.
func (m *Model) SearchActive() bool { return strings.TrimSpace(m.search.Value()) != "" }

// Query is the live search query.
func (m *Model) Query() string { return m.search.Value() }

// ClearSearch drops the query and restores the full sidebar.
func (m *Model) ClearSearch() {
	m.search.SetValue("")
	m.search.Blur()
	m.focus = focusSidebar
	m.results = false
	m.cursor = indexOfPage(m.visiblePages(), m.Page())
}

// FocusContext is the keymap context the surface owns right now.
func (m *Model) FocusContext() string {
	if m.SearchFocused() {
		return ContextConfigEdit
	}
	return ContextConfig
}

// Commands describes what the footer and palette may advertise here. Keys come
// from the registered bindings, never from this list.
func (m *Model) Commands() []plugin.Command {
	return []plugin.Command{
		{ID: "cursor-down", Name: "Sections", Category: plugin.CategoryNavigation, Context: ContextConfig, Priority: 1},
		{ID: "select", Name: "Change", Category: plugin.CategoryActions, Context: ContextConfig, Priority: 2},
		{ID: "search", Name: "Search", Category: plugin.CategorySearch, Context: ContextConfig, Priority: 3},
		{ID: "close-configuration", Name: "Return", Category: plugin.CategoryNavigation, Context: ContextConfig, Priority: 4},
		{ID: "first-result", Name: "First result", Category: plugin.CategoryNavigation, Context: ContextConfigEdit, Priority: 1},
		{ID: "select", Name: "Open setting", Category: plugin.CategoryActions, Context: ContextConfigEdit, Priority: 2},
		{ID: "clear-search", Name: "Clear search", Category: plugin.CategorySearch, Context: ContextConfigEdit, Priority: 3},
	}
}

// visiblePages is the sidebar's current destination list: everything, or only
// the pages a non-empty query matches.
func (m *Model) visiblePages() []PageID {
	if m.SearchActive() {
		return SearchPages(m.search.Value())
	}
	var pages []PageID
	for _, page := range AllPages() {
		pages = append(pages, page.ID)
	}
	return pages
}

func indexOfPage(pages []PageID, target PageID) int {
	for i, page := range pages {
		if page == target {
			return i
		}
	}
	return 0
}

// Key handles a key press. It reports whether the surface consumed it; the host
// keeps unconsumed keys away from the plugin hidden underneath.
func (m *Model) Key(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	key := msg.String()

	if m.SearchFocused() {
		switch key {
		case "down", "ctrl+n", "tab":
			m.focusSidebarList()
			return true, nil
		case "up", "ctrl+p":
			return true, nil
		case "enter":
			m.focusSidebarList()
			m.activateCursor()
			return true, nil
		}
		var cmd tea.Cmd
		m.search, cmd = m.search.Update(msg)
		// The detail pane follows the query: results while one is being typed,
		// the page again once it is cleared.
		m.results = m.SearchActive()
		m.clampCursor()
		return true, cmd
	}

	switch key {
	case "/":
		m.focusSearch()
		return true, nil
	case "tab":
		if m.SearchActive() || m.focus == focusSidebar {
			m.focusSearch()
			return true, nil
		}
	case "down", "j", "ctrl+n":
		pages := m.visiblePages()
		if m.cursor < len(pages)-1 {
			m.cursor++
		}
		return true, nil
	case "up", "k", "ctrl+p":
		if m.cursor == 0 {
			// Mirrors the mockup's search flow: Down from Search reaches the
			// first result, Up from the first result returns to it. With no
			// query there is nothing above the list, so the cursor stays put.
			if m.SearchActive() {
				m.focusSearch()
			}
			return true, nil
		}
		m.cursor--
		return true, nil
	case "home", "g":
		m.cursor = 0
		return true, nil
	case "end", "G":
		m.cursor = max(0, len(m.visiblePages())-1)
		return true, nil
	case "enter":
		m.activateCursor()
		return true, nil
	}
	return false, nil
}

func (m *Model) focusSearch() {
	m.focus = focusSearch
	m.search.Focus()
	m.results = m.SearchActive()
}

func (m *Model) focusSidebarList() {
	m.focus = focusSidebar
	m.search.Blur()
	m.clampCursor()
	if m.SearchActive() {
		m.results = true
	}
}

func (m *Model) clampCursor() {
	pages := m.visiblePages()
	if m.cursor >= len(pages) {
		m.cursor = max(0, len(pages)-1)
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// activateCursor opens the destination under the cursor. With a query active
// this is the "Enter on a search result" path: it navigates to the page and
// leaves the query in place so the user can keep stepping through matches.
func (m *Model) activateCursor() {
	pages := m.visiblePages()
	if len(pages) == 0 {
		return
	}
	m.clampCursor()
	m.router.navigate(pages[m.cursor])
	m.results = false
}

// Mouse handles a mouse event whose coordinates are local to the content area.
func (m *Model) Mouse(msg tea.MouseMsg) tea.Cmd {
	action := m.mouse.HandleMouse(msg)
	switch action.Type {
	case mouse.ActionHover:
		m.hoverID = ""
		if action.Region != nil {
			m.hoverID = action.Region.ID
		}
	case mouse.ActionClick, mouse.ActionDoubleClick:
		if action.Region == nil {
			return nil
		}
		switch id := action.Region.ID; {
		case id == regionSearch:
			m.focusSearch()
		case id == regionBack:
			m.Back()
		case strings.HasPrefix(id, regionNavPrefix):
			page := PageID(strings.TrimPrefix(id, regionNavPrefix))
			m.focus = focusSidebar
			m.search.Blur()
			m.cursor = indexOfPage(m.visiblePages(), page)
			m.router.navigate(page)
			m.results = false
		case strings.HasPrefix(id, regionResult):
			if page, ok := action.Region.Data.(PageID); ok {
				m.focus = focusSidebar
				m.search.Blur()
				m.cursor = indexOfPage(m.visiblePages(), page)
				m.router.navigate(page)
				m.results = false
			}
		}
	}
	return nil
}

// View renders the whole Configuration content area. It never returns more rows
// than height: the host's header and footer must stay on screen.
func (m *Model) View(width, height int) string {
	m.width, m.height = width, height
	m.mouse.Clear()
	if width < 8 || height < 3 {
		return lipgloss.NewStyle().Width(width).Height(height).MaxHeight(height).Render("")
	}

	sidebarWidth := sidebarPreferredWidth
	if maxSidebar := width / 2; sidebarWidth > maxSidebar {
		sidebarWidth = maxSidebar
	}
	if sidebarWidth < sidebarMinWidth {
		sidebarWidth = min(sidebarMinWidth, width/2)
	}
	if sidebarWidth < 8 {
		sidebarWidth = width
	}
	m.sidebarWidth = sidebarWidth
	detailWidth := width - sidebarWidth

	sidebar := styles.RenderPanel(m.renderSidebar(sidebarWidth, height), sidebarWidth, height, m.SearchFocused())
	if detailWidth < 8 {
		// Too narrow for two panes: the sidebar is the navigation, so it wins
		// and the detail pane is dropped rather than clipped into nonsense.
		return lipgloss.NewStyle().Width(width).Height(height).MaxHeight(height).Render(sidebar)
	}
	detail := styles.RenderPanelWithGradient(
		m.renderDetail(detailWidth, height, sidebarWidth),
		detailWidth, height, styles.GetActiveGradient(),
	)
	row := lipgloss.JoinHorizontal(lipgloss.Top, sidebar, detail)
	return lipgloss.NewStyle().Width(width).Height(height).MaxHeight(height).Render(row)
}

// paneContentWidth is the writable width inside a panel: two border columns and
// one column of padding on each side.
func paneContentWidth(paneWidth int) int {
	return max(0, paneWidth-4)
}

// renderSidebar paints the navigation pane and registers its hit regions.
// Regions are in content-area coordinates: the host offsets mouse events past
// the header before forwarding them.
func (m *Model) renderSidebar(paneWidth, paneHeight int) string {
	inner := paneContentWidth(paneWidth)
	originX := 2 // border + padding
	var lines []string

	add := func(line string) int {
		lines = append(lines, line)
		return len(lines) - 1
	}

	add(PaneTitle("Configuration"))
	add("")

	m.search.SetWidth(max(1, inner-8))
	inputStyle := lipgloss.NewStyle().Foreground(styles.TextMuted)
	if m.SearchActive() {
		inputStyle = lipgloss.NewStyle().
			Foreground(styles.TextPrimary).
			Background(styles.BgTertiary)
	}
	if m.SearchFocused() {
		inputStyle = inputStyle.Foreground(styles.TextPrimary).Background(styles.BgTertiary)
	}
	if m.hoverID == regionSearch && !m.SearchFocused() {
		inputStyle = inputStyle.Background(styles.SurfaceRaised)
	}
	label := lipgloss.NewStyle().Foreground(styles.TextSecondary).Render("Search ")
	searchLine := label + inputStyle.Render(m.search.View())
	searchY := add(searchLine)
	m.mouse.HitMap.AddRect(regionSearch, originX, 1+searchY, inner, 1, nil)

	matches := Search(m.search.Value())
	if m.SearchActive() {
		count := fmt.Sprintf("  %d matching settings", len(matches))
		if len(matches) == 1 {
			count = "  1 matching setting"
		}
		add(lipgloss.NewStyle().Foreground(styles.Warning).Render(count))
	}

	visible := m.visiblePages()
	inList := make(map[PageID]bool, len(visible))
	for _, page := range visible {
		inList[page] = true
	}

	index := 0
	for _, group := range Groups() {
		shown := make([]Page, 0, len(group.Pages))
		for _, page := range group.Pages {
			if inList[page.ID] {
				shown = append(shown, page)
			}
		}
		if len(shown) == 0 {
			continue
		}
		add("")
		add(PaneTitle(group.Title))
		for _, page := range shown {
			state := State{
				Focused: m.focus == focusSidebar && index == m.cursor,
				Hovered: m.hoverID == regionNavPrefix+string(page.ID),
			}
			// The active destination stays visibly highlighted on every page,
			// including while the cursor is elsewhere or Search has focus.
			text := page.Title
			if page.ID == m.Page() && !state.Focused {
				text = lipgloss.NewStyle().Foreground(styles.Primary).Bold(true).Render(page.Title)
			}
			y := add(ListRow(text, inner, state))
			m.mouse.HitMap.AddRect(regionNavPrefix+string(page.ID), originX, 1+y, inner, 1, page.ID)
			index++
		}
	}

	if len(visible) == 0 {
		add("")
		add(Muted("No matching settings"))
	}

	return clampLines(lines, paneHeight-2, inner)
}

// renderDetail paints the detail pane. offsetX is where the pane starts in the
// content area, so its hit regions land where they are painted.
func (m *Model) renderDetail(paneWidth, paneHeight, offsetX int) string {
	inner := paneContentWidth(paneWidth)
	originX := offsetX + 2
	var lines []string

	if m.SearchActive() && m.results {
		lines = append(lines, PaneTitle("Search results"), "")
		lines = append(lines, Muted("Use ↓ to move to the first matching setting, or Esc to clear the filter."))
		matches := Search(m.search.Value())
		lastPage := PageID("")
		for i, entry := range matches {
			if entry.Page != lastPage {
				lines = append(lines, "", PaneTitle(PageTitle(entry.Page)))
				lastPage = entry.Page
			}
			id := fmt.Sprintf("%s%d", regionResult, i)
			state := State{Hovered: m.hoverID == id}
			lines = append(lines, ListRow(entry.Label, inner, state))
			m.mouse.HitMap.AddRect(id, originX, 1+len(lines)-1, inner, 1, entry.Page)
		}
		return clampLines(lines, paneHeight-2, inner)
	}

	route := m.Route()
	if route.IsChild() {
		state := State{Hovered: m.hoverID == regionBack}
		lines = append(lines, BackBar(route.Title, m.router.parentLabel(), inner, state), "")
		m.mouse.HitMap.AddRect(regionBack, originX, 1, inner, 1, nil)
		lines = append(lines, Muted("This focused repair route arrives in a later phase."))
		return clampLines(lines, paneHeight-2, inner)
	}

	lines = append(lines, pageBody(route.Page, inner)...)
	return clampLines(lines, paneHeight-2, inner)
}

// clampLines enforces the pane's height contract and keeps every line inside
// its width, so a pane can never push the host's header off screen.
func clampLines(lines []string, height, width int) string {
	if height < 0 {
		height = 0
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	out := make([]string, len(lines))
	for i, line := range lines {
		if width > 0 && ansi.StringWidth(line) > width {
			line = ansi.Truncate(line, width, "…")
		}
		out[i] = line
	}
	return strings.Join(out, "\n")
}
