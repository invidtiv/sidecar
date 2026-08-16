package configui

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/theme"
)

// The theme picker is one component with two homes: Appearance shows it as the
// page's theme table, and Add Project expands the same thing inline under its
// Theme field. Both get the same list, the same filter, the same swatches, the
// same live preview, and the same keyboard and mouse behavior, because there is
// only one of it.
//
// It owns no persistence. Selecting tells the page, and the page decides what
// that means: Appearance saves at the chosen scope, Add Project puts it in the
// draft and waits for Save.

const (
	regionThemeSearch = "config-theme-search"
	regionThemeRow    = "config-theme-row-"
	regionThemeGlobal = "config-theme-global"
	// regionThemeMore covers the "↑ n more above" / "↓ n more below" lines. They
	// are not controls, but they are part of the list as far as the wheel is
	// concerned: the first notch pushes "more above" under the pointer, and a
	// list that stops scrolling there scrolls exactly once.
	regionThemeMore = "config-theme-more"
)

// themePicker is the picker's state. A page holds one.
type themePicker struct {
	// inline marks the Add Project disclosure, which is denser and offers the
	// "Use global theme" action.
	inline bool
	// rows is how many themes are visible at once.
	rows int

	search   textinput.Model
	all      []theme.Entry
	filtered []theme.Entry
	cursor   int
	scroll   int

	// current is the entry the list badges as the one in force.
	current theme.Entry
	// restore is the theme to put back when the picker is abandoned. It is
	// captured when the picker opens, so Escape always returns to what the user
	// was actually looking at.
	restore theme.ResolvedTheme
	// previewing marks that the picker has changed the live theme.
	previewing bool

	// selectEntry is what choosing a theme means to the page hosting it.
	selectEntry func(*Model, theme.Entry) tea.Cmd
	// useGlobal, when set, adds the distinct reset action the inline picker
	// needs. Nil on Appearance, where "no theme" is not a choice.
	useGlobal func(*Model) tea.Cmd
}

// newThemePicker builds a picker over the whole theme library.
func newThemePicker(inline bool, rows int) *themePicker {
	search := textinput.New()
	search.Prompt = ""
	search.Placeholder = "Search themes…"
	search.CharLimit = 50
	picker := &themePicker{inline: inline, rows: rows}
	picker.search = search
	picker.all = theme.List()
	picker.filtered = picker.all
	return picker
}

// open points the picker at the theme in force and remembers what to restore.
func (p *themePicker) open(current theme.Entry, restore theme.ResolvedTheme) {
	p.current = current
	p.restore = restore
	p.previewing = false
	p.search.SetValue("")
	p.refilter()
	p.cursorTo(theme.IndexOf(p.filtered, current))
}

// refilter re-runs the query and keeps the cursor on something selectable.
func (p *themePicker) refilter() {
	p.filtered = theme.Filter(p.all, p.search.Value())
	p.cursorTo(theme.IndexOf(p.filtered, p.selected()))
	p.clamp()
}

func (p *themePicker) cursorTo(index int) {
	if index < 0 {
		index = 0
	}
	p.cursor = index
	p.skipSeparator(1)
	p.clamp()
}

// skipSeparator moves off a divider in the given direction.
func (p *themePicker) skipSeparator(step int) {
	for p.cursor >= 0 && p.cursor < len(p.filtered) && p.filtered[p.cursor].IsSeparator {
		p.cursor += step
	}
	if p.cursor >= len(p.filtered) {
		p.cursor = len(p.filtered) - 1
		for p.cursor > 0 && p.filtered[p.cursor].IsSeparator {
			p.cursor--
		}
	}
	if p.cursor < 0 {
		p.cursor = 0
	}
}

func (p *themePicker) clamp() {
	if p.cursor < 0 {
		p.cursor = 0
	}
	if p.cursor >= len(p.filtered) {
		p.cursor = max(0, len(p.filtered)-1)
	}
	if p.cursor < p.scroll {
		p.scroll = p.cursor
	}
	if p.cursor >= p.scroll+p.rows {
		p.scroll = p.cursor - p.rows + 1
	}
	if p.scroll > len(p.filtered)-p.rows {
		p.scroll = len(p.filtered) - p.rows
	}
	if p.scroll < 0 {
		p.scroll = 0
	}
}

// rowsBefore counts the selectable rows between the top of the visible window
// and index. Dividers are painted but never declared, so this — not the
// difference of two filtered indices — is how far into the pane's cursor stops
// a visible row sits.
func (p *themePicker) rowsBefore(index int) int {
	rows := 0
	for i := p.scroll; i < index && i < len(p.filtered); i++ {
		if !p.filtered[i].IsSeparator {
			rows++
		}
	}
	return rows
}

// scrollBy moves the cursor by whole rows, which is what the mouse wheel asks
// of the list. It reports whether anything moved. The window follows the
// cursor, so a wheel notch can never scroll the selection out of sight.
//
// One notch is three rows, and the preview it ends on is the only one worth
// applying: previewing each row a flick passes over would recolour the whole
// application three times for a gesture the user reads as one.
func (p *themePicker) scrollBy(delta int) bool {
	if delta == 0 {
		return false
	}
	step := 1
	if delta < 0 {
		step, delta = -1, -delta
	}
	moved := false
	for i := 0; i < delta; i++ {
		if !p.step(step) {
			break
		}
		moved = true
	}
	if moved {
		p.preview()
	}
	return moved
}

// selected is the entry under the picker's cursor.
func (p *themePicker) selected() theme.Entry {
	if p.cursor < 0 || p.cursor >= len(p.filtered) {
		return theme.Entry{}
	}
	return p.filtered[p.cursor]
}

// move steps the cursor and previews what it lands on. It reports false when
// the cursor is already at the end it was asked to move toward, which is the
// page's signal to move its own cursor out of the list.
func (p *themePicker) move(delta int) bool {
	if !p.step(delta) {
		return false
	}
	p.preview()
	return true
}

// step moves the cursor without previewing. Preview applies a theme across the
// whole surface, which is worth doing once for a gesture rather than once per
// row the gesture crossed.
func (p *themePicker) step(delta int) bool {
	if len(p.filtered) == 0 {
		return false
	}
	next := p.cursor + delta
	for next >= 0 && next < len(p.filtered) && p.filtered[next].IsSeparator {
		next += delta
	}
	if next < 0 || next >= len(p.filtered) {
		return false
	}
	p.cursor = next
	p.clamp()
	return true
}

// preview applies the selected theme live, across the whole surface.
func (p *themePicker) preview() {
	entry := p.selected()
	if entry.IsZero() {
		return
	}
	theme.Preview(entry)
	p.previewing = true
}

// restoreTheme puts back the theme the picker found in force.
func (p *themePicker) restoreTheme() {
	if !p.previewing {
		return
	}
	theme.ApplyResolved(p.restore)
	p.previewing = false
}

// countSummary is the result count the brief asks every theme list to show.
func (p *themePicker) countSummary() string {
	counts := theme.LibraryCounts()
	if strings.TrimSpace(p.search.Value()) == "" {
		return counts.Summary()
	}
	return fmt.Sprintf("%d of %d themes", theme.CountSelectable(p.filtered), counts.Total())
}

// --- rendering ----------------------------------------------------------

// buildThemePicker paints the picker and declares its controls. indent is the
// left inset: the inline picker aligns under its form field rather than under
// the pane edge.
func (m *Model) buildThemePicker(b *paneBuilder, p *themePicker, indent int) {
	pad := strings.Repeat(" ", indent)

	// Search. It is a control in its own right: Enter or / gives it the
	// keyboard, and it looks editable before it has it.
	searchState := b.declare(regionThemeSearch, "", true, func(m *Model) tea.Cmd {
		m.focusPickerSearch()
		return nil
	})
	if m.editingID() == regionThemeSearch {
		searchState.Focused = true
	}
	fieldWidth := max(12, b.inner-indent-len(p.countSummary())-4)
	var field string
	if m.editingID() == regionThemeSearch {
		field = Field(&p.search, fieldWidth, searchState)
	} else {
		value := p.search.Value()
		if value == "" {
			value = "/  Find a theme…"
		}
		field = StaticField(value, fieldWidth, searchState)
	}
	countText := mutedStyle().Render(p.countSummary())
	gap := b.inner - indent - ansi.StringWidth(field) - ansi.StringWidth(countText)
	if gap < 1 {
		gap = 1
	}
	y := len(b.lines)
	b.lines = append(b.lines, pad+field+strings.Repeat(" ", gap)+countText)
	b.m.mouse.HitMap.AddRect(regionThemeSearch, b.originX+indent, 1+y, ansi.StringWidth(field), 1, nil)

	b.blank()

	if len(p.filtered) == 0 {
		b.text(pad + mutedStyle().Render("No themes match that search."))
		return
	}

	// The list. Only the visible window is declared, so the keyboard can only
	// reach a row that is on screen; the picker owns movement inside it.
	//
	// Because the picker owns movement, it owns the cursor while the cursor is
	// in it: the row it has selected is the row the pane's cursor is on, and
	// the pane paints exactly that one. Deciding the highlight twice — once
	// from the control index, once from the picker's own index into a filtered
	// list that also holds dividers — is what made a click halfway down the
	// list light up the rows above it as well.
	b.claimCursor(regionThemeRow, p.rowsBefore(p.cursor))
	if p.scroll > 0 {
		b.moreLine(regionThemeMore, pad+mutedStyle().Render(fmt.Sprintf("↑ %d more above", p.scroll)))
	}
	end := min(len(p.filtered), p.scroll+p.rows)
	for i := p.scroll; i < end; i++ {
		entry := p.filtered[i]
		if entry.IsSeparator {
			// A divider is not a control, but it is part of the list under the
			// wheel: the library divider sits in the middle of the window the
			// page opens on, and a notch over it must not be dropped.
			b.moreLine(regionThemeMore, pad+mutedStyle().Render("── "+entry.SeparatorText+" ──"))
			continue
		}
		index := i
		id := fmt.Sprintf("%s%d", regionThemeRow, index)
		b.row(id, "", func(m *Model) tea.Cmd {
			picker := m.activePicker()
			if picker == nil {
				return nil
			}
			picker.cursorTo(index)
			picker.preview()
			if picker.selectEntry == nil {
				return nil
			}
			return picker.selectEntry(m, picker.selected())
		}, func(state State) string {
			return themeRow(entry, p.current, b.inner-indent, state, pad)
		})
	}
	if remaining := len(p.filtered) - end; remaining > 0 {
		b.moreLine(regionThemeMore, pad+mutedStyle().Render(fmt.Sprintf("↓ %d more below", remaining)))
	}

	if p.useGlobal != nil {
		b.blank()
		globalState := b.declare(regionThemeGlobal, "g", true, func(m *Model) tea.Cmd {
			picker := m.activePicker()
			if picker == nil || picker.useGlobal == nil {
				return nil
			}
			return picker.useGlobal(m)
		})
		pill := Button("G  Use global theme", false, globalState)
		y := len(b.lines)
		b.lines = append(b.lines, pad+pill)
		b.m.mouse.HitMap.AddRect(regionThemeGlobal, b.originX+indent, 1+y, ansi.StringWidth(pill), 1, nil)
	}
}

// themeRow paints one theme: its four-color swatch, its name, and what the list
// has to say about it on the right.
func themeRow(entry, current theme.Entry, width int, state State, pad string) string {
	name := entry.Name
	nameStyle := lipgloss.NewStyle().Foreground(styles.TextSecondary)
	switch {
	case state.Focused:
		nameStyle = lipgloss.NewStyle().Foreground(styles.Primary).Bold(true)
	case state.Hovered:
		nameStyle = lipgloss.NewStyle().Foreground(styles.TextPrimary)
	case entry.Same(current):
		nameStyle = lipgloss.NewStyle().Foreground(styles.Success).Bold(true)
	}

	right := theme.Label(entry)
	rightRendered := mutedStyle().Render(right)
	if entry.Same(current) {
		label := "CURRENT"
		rightRendered = Badge(label, false)
	}

	left := pad + "  " + Swatch(theme.Swatch(entry)) + "  " + nameStyle.Render(name)
	gap := width + len(pad) - ansi.StringWidth(left) - ansi.StringWidth(rightRendered)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + rightRendered
}

// Swatch renders a theme's four identifying colors as solid blocks.
func Swatch(colors []string) string {
	if len(colors) == 0 {
		return "    "
	}
	var sb strings.Builder
	for _, color := range colors {
		sb.WriteString(lipgloss.NewStyle().Background(lipgloss.Color(color)).Render(" "))
	}
	return sb.String()
}

// --- keyboard -----------------------------------------------------------

// activePicker is the picker the visible route owns, or nil.
func (m *Model) activePicker() *themePicker {
	route := m.Route()
	// The form's picker is only the active one while the form is the route on
	// screen. A draft left behind by a route that was popped without tearing it
	// down would otherwise answer for a page it is not on.
	if m.addProject != nil && m.addProject.picker != nil && isProjectFormRoute(route) {
		return m.addProject.picker
	}
	if route.IsChild() {
		return nil
	}
	if m.Page() == PageAppearance {
		return m.appearancePicker()
	}
	return nil
}

// pickerOwnsKeys reports that the row cursor is inside a picker's list, so the
// arrows belong to the theme list rather than to the page's own controls.
func (m *Model) pickerOwnsKeys() bool {
	picker := m.activePicker()
	if picker == nil || m.editing() {
		return false
	}
	index := m.cursorControl()
	if index < 0 || index >= len(m.controls) {
		return false
	}
	return strings.HasPrefix(m.controls[index].id, regionThemeRow)
}

// pickerKey answers the keys a focused theme list owns. It reports false for
// anything it does not claim — including a move that runs off the end of the
// list, which is how the cursor leaves it.
func (m *Model) pickerKey(key string) (bool, tea.Cmd) {
	picker := m.activePicker()
	if picker == nil {
		return false, nil
	}
	switch key {
	case "down", "j", "ctrl+n":
		return picker.move(1), nil
	case "up", "k", "ctrl+p":
		return picker.move(-1), nil
	case "/":
		m.focusPickerSearch()
		return true, nil
	case "enter":
		if picker.selectEntry == nil {
			return true, nil
		}
		return true, picker.selectEntry(m, picker.selected())
	}
	return false, nil
}

// focusPickerSearch gives the picker's filter the keyboard. Typing filters
// live, Down moves into the results, and Escape leaves the query behind
// rather than closing anything.
func (m *Model) focusPickerSearch() {
	picker := m.activePicker()
	if picker == nil {
		return
	}
	m.openEditor(&editorState{
		id:    regionThemeSearch,
		input: &picker.search,
		change: func(m *Model) {
			if picker := m.activePicker(); picker != nil {
				picker.refilter()
			}
		},
		submit: func(m *Model) (tea.Cmd, bool) {
			m.focusPickerList()
			return nil, false
		},
		cancel: func(m *Model) {
			if picker := m.activePicker(); picker != nil {
				picker.search.SetValue("")
				picker.refilter()
			}
		},
		keys: func(m *Model, msg tea.KeyPressMsg) (bool, tea.Cmd) {
			switch msg.String() {
			case "down", "tab":
				m.closeEditor()
				m.focusPickerList()
				return true, nil
			}
			return false, nil
		},
	})
	m.focusControlByID(regionThemeSearch)
}

// focusPickerList puts the row cursor on the picker's selected row.
func (m *Model) focusPickerList() {
	picker := m.activePicker()
	if picker == nil {
		return
	}
	m.detailFocus = true
	m.focusControlByID(fmt.Sprintf("%s%d", regionThemeRow, picker.cursor))
}

// syncPickerCursor keeps a picker's own selection in step with the page's row
// cursor when the cursor arrives from outside the list, so entering the list
// previews what it lands on exactly as moving inside it does.
func (m *Model) syncPickerCursor() {
	picker := m.activePicker()
	if picker == nil || m.editing() {
		return
	}
	index := m.cursorControl()
	if index < 0 || index >= len(m.controls) {
		return
	}
	id := m.controls[index].id
	if !strings.HasPrefix(id, regionThemeRow) {
		return
	}
	row, err := strconv.Atoi(strings.TrimPrefix(id, regionThemeRow))
	if err != nil || row == picker.cursor {
		return
	}
	picker.cursorTo(row)
	picker.preview()
}

// focusControlByID puts the row cursor on a control by id. A control that the
// last frame did not contain — a picker row that is only about to exist, a
// field on a route just opened — is remembered and focused as soon as the pane
// declares it, so "focus this" never silently misses.
func (m *Model) focusControlByID(id string) {
	if m.focusRenderedControl(id) {
		m.pendingFocus = ""
		return
	}
	m.pendingFocus = id
}

// focusRenderedControl focuses a control the last frame actually declared.
func (m *Model) focusRenderedControl(id string) bool {
	for i, c := range m.controls {
		if c.id == id && c.cursor {
			m.focusControlIndex(i)
			return true
		}
	}
	return false
}
