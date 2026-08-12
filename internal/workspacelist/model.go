package workspacelist

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/ui"
)

// RegionKind names a mouse target the list drew. Regions are reported from the
// same layout that rendered the rows, so a hit test can never disagree with
// what is on screen.
type RegionKind string

const (
	RegionRow    RegionKind = "workspacelist-row"
	RegionSort   RegionKind = "workspacelist-sort"
	RegionFilter RegionKind = "workspacelist-filter"
)

type Region struct {
	Kind          RegionKind
	ID            string
	X, Y, W, H    int
	VisibleIndex  int
	SectionHeader bool
}

// Model is the list state a consumer owns: the catalog projection, the chosen
// sort, the filter, and the selection/viewport. Selection is by stable ID, so a
// refresh, a filter keystroke, or a sort change moves the cursor with the item
// rather than with the row number.
type Model struct {
	items    []Item
	sortMode Sort
	filter   Filter

	selectedID string
	scroll     int
	visible    []Item
	rows       int
	twoLine    bool
	loading    bool
	failures   []string
	emptyText  string
}

func (m *Model) Filter() *Filter { return &m.filter }

func (m *Model) Sort() Sort { return m.sortMode }

func (m *Model) SetSort(mode Sort) {
	m.sortMode = mode
	m.reproject()
}

// CycleSort advances `s` through Activity → Project → Recent → Name.
func (m *Model) CycleSort() { m.SetSort(m.sortMode.Next()) }

// SetLoading marks that inventory is still arriving. Rows already collected
// stay on screen: incremental results are the point.
func (m *Model) SetLoading(loading bool) { m.loading = loading }

// SetFailures records per-project unavailability rows. They are presentation
// only; the list never retries or repairs anything.
func (m *Model) SetFailures(failures []string) {
	m.failures = append([]string(nil), failures...)
}

func (m *Model) SetEmptyText(text string) { m.emptyText = text }

// SetItems replaces the catalog projection, preserving the selected identity.
func (m *Model) SetItems(items []Item) {
	m.items = append([]Item(nil), items...)
	m.reproject()
}

func (m *Model) reproject() {
	previous := m.selectedID
	m.visible = Sorted(Filtered(m.items, m.filter.Query()), m.sortMode)
	if previous != "" && m.indexOf(previous) >= 0 {
		m.selectedID = previous
		m.ensureVisible()
		return
	}
	if len(m.visible) == 0 {
		m.selectedID, m.scroll = "", 0
		return
	}
	m.selectedID = m.visible[0].ID
	m.scroll = 0
}

func (m *Model) indexOf(id string) int {
	for i, item := range m.visible {
		if item.ID == id {
			return i
		}
	}
	return -1
}

func (m *Model) Items() []Item   { return append([]Item(nil), m.items...) }
func (m *Model) Visible() []Item { return append([]Item(nil), m.visible...) }
func (m *Model) Counts() (matched, total int) {
	return len(m.visible), len(m.items)
}

func (m *Model) Selected() (Item, bool) {
	index := m.indexOf(m.selectedID)
	if index < 0 {
		return Item{}, false
	}
	return m.visible[index], true
}

func (m *Model) SelectedID() string { return m.selectedID }

// SelectID moves the cursor to a stable identity when it is visible.
func (m *Model) SelectID(id string) bool {
	if m.indexOf(id) < 0 {
		return false
	}
	m.selectedID = id
	m.ensureVisible()
	return true
}

// Move steps the selection, clamping at both ends rather than wrapping.
func (m *Model) Move(delta int) bool {
	if len(m.visible) == 0 {
		return false
	}
	index := m.indexOf(m.selectedID)
	if index < 0 {
		index = 0
	}
	next := min(max(index+delta, 0), len(m.visible)-1)
	if next == index {
		return false
	}
	m.selectedID = m.visible[next].ID
	m.ensureVisible()
	return true
}

func (m *Model) Top() bool {
	if len(m.visible) == 0 {
		return false
	}
	changed := m.selectedID != m.visible[0].ID
	m.selectedID = m.visible[0].ID
	m.scroll = 0
	return changed
}

func (m *Model) Bottom() bool {
	if len(m.visible) == 0 {
		return false
	}
	last := m.visible[len(m.visible)-1].ID
	changed := m.selectedID != last
	m.selectedID = last
	m.ensureVisible()
	return changed
}

// FilterKey applies a key while the filter owns focus and reprojects when the
// query changed.
func (m *Model) FilterKey(key, text string) KeyResult {
	result := m.filter.HandleKey(key, text)
	if result == KeyHandled || result == KeyExit {
		m.reproject()
	}
	return result
}

// Reproject re-runs filter and sort after a caller changed the query directly
// (a paste, a programmatic clear). Selection is preserved by identity.
func (m *Model) Reproject() { m.reproject() }

// FocusFilter is the `/` entry point.
func (m *Model) FocusFilter() { m.filter.Focus() }

func (m *Model) ensureVisible() {
	if m.rows <= 0 {
		return
	}
	index := m.indexOf(m.selectedID)
	if index < 0 {
		return
	}
	if index < m.scroll {
		m.scroll = index
	} else if index >= m.scroll+m.rows {
		m.scroll = index - m.rows + 1
	}
	m.scroll = min(max(m.scroll, 0), max(0, len(m.visible)-m.rows))
}

// ScrollOffset is the first visible row index. Consumers read it to prove the
// viewport survived a refresh; nothing else depends on it.
func (m *Model) ScrollOffset() int { return m.scroll }

// Scroll moves the viewport without moving the selection (wheel behaviour).
func (m *Model) Scroll(delta int) {
	m.scroll = min(max(m.scroll+delta, 0), max(0, len(m.visible)-max(1, m.rows)))
}

// RenderOptions describes the box the list is drawn into.
type RenderOptions struct {
	Width, Height int
	Title         string
	Focused       bool
	Now           time.Time
}

// Rendered is the drawn list plus the regions it registered.
type Rendered struct {
	View    string
	Regions []Region
}

// twoLineWidth is the sidebar width below which a row degrades to one
// ANSI-safe truncated line instead of a name/subtitle pair.
const twoLineWidth = 34

// Render draws the list: header with the active sort, filter row, activity
// group headings, rows, and a scrollbar. Group headings scroll with their rows
// so the heading a row sits under is always the heading above it.
func (m *Model) Render(opts RenderOptions) Rendered {
	width, height := opts.Width, opts.Height
	if width < 1 || height < 1 {
		return Rendered{}
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	m.twoLine = width >= twoLineWidth
	rowHeight := 1
	if m.twoLine {
		rowHeight = 2
	}

	title := opts.Title
	if title == "" {
		title = "Workspaces"
	}
	sortLabel := m.sortMode.Label()
	gap := max(1, width-ansi.StringWidth(title)-ansi.StringWidth(sortLabel))
	header := styles.Title.Render(title) + strings.Repeat(" ", gap) + styles.Muted.Render(sortLabel)
	lines := []string{fit(header, width)}
	regions := []Region{{Kind: RegionSort, X: width - ansi.StringWidth(sortLabel), Y: 0, W: ansi.StringWidth(sortLabel), H: 1}}

	matched, total := m.Counts()
	lines = append(lines, fit(m.filter.RenderRow(width, matched, total), width))
	regions = append(regions, Region{Kind: RegionFilter, X: 0, Y: 1, W: width, H: 1})

	body := max(0, height-len(lines))
	// Reserve one column for the scrollbar so rows never sit under it.
	rowWidth := max(1, width-1)
	sections := Grouped(m.visible, m.sortMode)
	headingRows := 0
	if m.sortMode == SortActivity {
		headingRows = len(sections)
	}
	// A project whose inventory could not be read is a row, not a leftover. Its
	// lines are reserved out of the body before the item viewport is sized, so a
	// catalog longer than the pane — the normal multi-project case — cannot make
	// an unavailable project silently vanish. The reservation is bounded: a long
	// outage list collapses into a count rather than pushing the catalog off the
	// screen.
	failureRows := len(m.failures)
	if failureRows > 0 {
		limit := max(0, body-1)
		if len(m.visible) > 0 {
			limit = min(limit, max(1, body/3))
		}
		failureRows = min(failureRows, limit)
	}
	listBody := max(0, body-failureRows)
	m.rows = max(1, (listBody-headingRows)/rowHeight)
	m.ensureVisible()

	var rowLines []string
	index := 0
	y := len(lines)
	for _, section := range sections {
		sectionStart := index
		sectionEnd := index + len(section.Items)
		index = sectionEnd
		if sectionEnd <= m.scroll || sectionStart >= m.scroll+m.rows {
			continue
		}
		if m.sortMode == SortActivity && section.Group != "" {
			heading := styles.Muted.Render(string(section.Group)) + " " + styles.Muted.Render(fmt.Sprintf("%d", len(section.Items)))
			rowLines = append(rowLines, fit(heading, rowWidth))
			y++
		}
		for offset, item := range section.Items {
			position := sectionStart + offset
			if position < m.scroll || position >= m.scroll+m.rows {
				continue
			}
			selected := item.ID == m.selectedID
			row := m.renderRow(item, selected, opts.Focused, rowWidth, now)
			rowLines = append(rowLines, row...)
			regions = append(regions, Region{Kind: RegionRow, ID: item.ID, X: 0, Y: y, W: rowWidth, H: len(row), VisibleIndex: position})
			y += len(row)
		}
	}

	if len(m.visible) == 0 {
		// An empty list must say which kind of empty it is: a query that matches
		// nothing reads very differently from a catalog that is still loading.
		switch {
		case m.filter.Active():
			rowLines = append(rowLines, fit(NoMatchRow(rowWidth, m.filter.Query()), rowWidth))
		case m.loading:
			rowLines = append(rowLines, fit(styles.Muted.Render("Loading workspaces…"), rowWidth))
		case m.emptyText != "":
			rowLines = append(rowLines, fit(styles.Muted.Render(m.emptyText), rowWidth))
		}
	}

	if len(rowLines) > listBody {
		rowLines = rowLines[:listBody]
	}
	scrollbar := ui.RenderScrollbar(ui.ScrollbarParams{
		TotalItems:   len(m.visible),
		ScrollOffset: m.scroll,
		VisibleItems: m.rows,
		TrackHeight:  len(rowLines),
	})
	content := lipgloss.JoinHorizontal(lipgloss.Top, strings.Join(rowLines, "\n"), scrollbar)
	if len(rowLines) > 0 {
		lines = append(lines, strings.Split(content, "\n")...)
	}
	lines = append(lines, m.failureLines(failureRows, width)...)
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	return Rendered{View: strings.Join(lines[:height], "\n"), Regions: regions}
}

// failureLines renders the per-project unavailable rows that fit in the space
// reserved for them. When more projects failed than there is room for, the last
// reserved line becomes a count: the user still learns that inventory is
// incomplete, which is the whole point of the row.
func (m *Model) failureLines(rows, width int) []string {
	if rows <= 0 || len(m.failures) == 0 {
		return nil
	}
	if len(m.failures) <= rows {
		out := make([]string, 0, len(m.failures))
		for _, failure := range m.failures {
			out = append(out, fit(styles.Muted.Render("⚠ "+failure), width))
		}
		return out
	}
	out := make([]string, 0, rows)
	for _, failure := range m.failures[:rows-1] {
		out = append(out, fit(styles.Muted.Render("⚠ "+failure), width))
	}
	remaining := len(m.failures) - (rows - 1)
	return append(out, fit(styles.Muted.Render(fmt.Sprintf("⚠ +%d more projects unavailable", remaining)), width))
}

// renderRow draws one item. Two lines where the width supports it: name and
// relative age, then the textual project identity with provider and status.
// Project colour reinforces identity but is never the only differentiator —
// the project name is always spelled out.
func (m *Model) renderRow(item Item, selected, focused bool, width int, now time.Time) []string {
	hue := styles.ProjectHue(item.ProjectKey)
	age := RelativeAge(item.ChangedAt, now)

	if !m.twoLine {
		text := " " + item.Name
		if item.Project != "" {
			text += " · " + item.Project
		}
		line := fit(text, width)
		if selected {
			return []string{selectionStyle(focused).Width(width).Render(line)}
		}
		return []string{styles.ListItemNormal.Width(width).Render(line)}
	}

	name := item.Name
	if maxName := width - ansi.StringWidth(age) - 4; maxName > 0 && ansi.StringWidth(name) > maxName {
		name = ansi.Truncate(name, maxName, "…")
	}
	line1 := " " + name
	if pad := width - ansi.StringWidth(line1) - ansi.StringWidth(age) - 1; pad > 0 && age != "" {
		line1 += strings.Repeat(" ", pad) + age
	}
	detail := []string{item.Project}
	if item.Provider != "" {
		detail = append(detail, item.Provider)
	}
	if item.Status != "" {
		detail = append(detail, item.Status)
	}
	if item.Detail != "" {
		detail = append(detail, item.Detail)
	}
	line2 := "   " + strings.Join(detail, " · ")

	if selected {
		style := selectionStyle(focused)
		return []string{style.Width(width).Render(fit(line1, width) + "\n" + fit(line2, width))}
	}
	styledProject := lipgloss.NewStyle().Foreground(hue).Render(item.Project)
	styledDetail := styledProject
	if rest := strings.Join(detail[1:], " · "); rest != "" {
		styledDetail += styles.Muted.Render(" · " + rest)
	}
	return []string{styles.ListItemNormal.Width(width).Render(fit(line1, width) + "\n" + fit("   "+styledDetail, width))}
}

func selectionStyle(focused bool) lipgloss.Style {
	if focused {
		return styles.ListItemSelected
	}
	return lipgloss.NewStyle().Background(styles.BgSecondary).Foreground(styles.TextSecondary)
}

func fit(line string, width int) string {
	if width < 1 {
		return ""
	}
	if ansi.StringWidth(line) > width {
		line = ansi.Truncate(line, width, "…")
	}
	if gap := width - ansi.StringWidth(line); gap > 0 {
		line += strings.Repeat(" ", gap)
	}
	return line
}

// RelativeAge formats freshness in the same small units the Agents board uses,
// so one item does not read "3m" on one tab and "3 minutes" on the other.
func RelativeAge(changedAt, now time.Time) string {
	if changedAt.IsZero() {
		return ""
	}
	d := now.Sub(changedAt)
	if d < 0 {
		d = 0
	}
	switch {
	case d < 5*time.Second:
		return "now"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// RegionAt resolves a click to the region drawn under it, last registered
// winning — the same rule the workspace plugin's hit map uses.
func RegionAt(regions []Region, x, y int) (Region, bool) {
	for i := len(regions) - 1; i >= 0; i-- {
		r := regions[i]
		if x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H {
			return r, true
		}
	}
	return Region{}, false
}
