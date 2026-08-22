package configui

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/theme"
	"github.com/marcus/sidecar/internal/ui"
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
	// regionThemeList is the bordered list as a whole, including its frame and
	// scrollbar. The wheel answers anywhere over that box so a notch cannot
	// fall through a divider or the track.
	regionThemeList = "config-theme-list"
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

	// bar is the scrollbar the last build of the list reported, gesture any
	// drag in flight on it. The window position is view state; nothing here
	// is persisted.
	bar     themeScrollbar
	gesture themeScrollGesture
}

// themeScrollbar is what one build of the picker's list learned about its
// scrollbar: the params it was drawn with and where its track sits in
// content-area coordinates. has is false when every visible theme fits and no
// regions exist.
type themeScrollbar struct {
	params ui.ScrollbarParams
	trackX int
	trackY int
	has    bool
}

// themeScrollGesture carries one drag on that bar: the press-time params and
// track origin, so re-renders cannot shift the mapping under the pointer, and
// where within the thumb the grab landed.
type themeScrollGesture struct {
	active    bool
	params    ui.ScrollbarParams
	trackY    int
	grabDelta int
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

// maxScroll is the last scroll offset that still fills the window.
func (p *themePicker) maxScroll() int {
	return max(0, len(p.filtered)-p.rows)
}

// setViewport moves the visible window to next and keeps the cursor on the
// same visual row inside it: the list scrolls, the highlight stays put, and
// the theme under the row is the one a following preview will apply. It
// reports false when nothing moved. Both the wheel and the scrollbar gestures
// scroll through here, so a notch and a drag cannot disagree about where the
// highlight lands.
func (p *themePicker) setViewport(next int) bool {
	if len(p.filtered) == 0 {
		return false
	}
	if next < 0 {
		next = 0
	}
	if maxScroll := p.maxScroll(); next > maxScroll {
		next = maxScroll
	}
	if next == p.scroll {
		return false
	}
	p.scroll = next
	// The highlight stays at the top of the window so scrolling down and
	// back up are the same motion, even when a library divider sits there.
	p.cursor = p.scroll
	p.skipSeparator(1)
	if p.cursor >= p.scroll+p.rows {
		p.cursor = min(p.scroll+p.rows-1, len(p.filtered)-1)
		p.skipSeparator(-1)
	}
	return true
}

// scrollWindow answers one wheel notch. The preview it ends on is the only one
// worth applying: previewing each row a flick passes over would recolour the
// whole application three times for a gesture the user reads as one.
func (p *themePicker) scrollWindow(delta int) bool {
	if delta == 0 || len(p.filtered) == 0 {
		return false
	}
	if !p.setViewport(p.scroll + delta) {
		return false
	}
	p.preview()
	return true
}

// atScrollBoundary reports that a wheel notch in the given direction cannot
// move the window. delta is negative for up.
func (p *themePicker) atScrollBoundary(delta int) bool {
	if len(p.filtered) == 0 {
		return true
	}
	if delta < 0 {
		return p.scroll <= 0
	}
	return p.scroll >= p.maxScroll()
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

	// The list is a boxed window with its own scrollbar. Only the visible
	// window is declared, so the keyboard can only reach a row that is on
	// screen; the picker owns movement inside it.
	//
	// Because the picker owns movement, it owns the cursor while the cursor is
	// in it: the row it has selected is the row the pane's cursor is on, and
	// the pane paints exactly that one. Deciding the highlight twice — once
	// from the control index, once from the picker's own index into a filtered
	// list that also holds dividers — is what made a click halfway down the
	// list light up the rows above it as well.
	b.claimCursor(regionThemeRow, p.rowsBefore(p.cursor))

	listWidth := max(12, b.inner-indent)
	rowWidth := max(8, listWidth-3) // border columns + scrollbar
	listFocused := m.detailOwnsKeys() && strings.HasPrefix(m.focusedID, regionThemeRow)

	type paintedRow struct {
		id     string
		offset int
	}
	var hits []paintedRow
	body := make([]string, 0, p.rows)
	end := min(len(p.filtered), p.scroll+p.rows)
	for i := p.scroll; i < end; i++ {
		entry := p.filtered[i]
		offset := len(body)
		if entry.IsSeparator {
			body = append(body, padDisplay(mutedStyle().Render("── "+entry.SeparatorText+" ──"), rowWidth))
			continue
		}
		index := i
		id := fmt.Sprintf("%s%d", regionThemeRow, index)
		state := b.declare(id, "", true, func(m *Model) tea.Cmd {
			return m.clickThemeRow(index)
		})
		body = append(body, themeRow(entry, p.current, rowWidth, state))
		hits = append(hits, paintedRow{id: id, offset: offset})
	}
	for len(body) < p.rows {
		body = append(body, strings.Repeat(" ", rowWidth))
	}

	box, geom := themeListBox(body, p.scroll, len(p.filtered), p.rows, listWidth,
		listFocused || b.hovering(regionThemeList), m.scrollbarStyle(b))
	y = len(b.lines)
	b.lines = append(b.lines, prefixLines(box, pad)...)
	b.m.mouse.HitMap.AddRect(regionThemeList, b.originX+indent, 1+y, listWidth, len(box), nil)
	for _, hit := range hits {
		b.m.mouse.HitMap.AddRect(hit.id, b.originX+indent+1, 1+y+1+hit.offset, rowWidth, 1, nil)
	}
	// The bar's two targets go in after the row rects: HitMap.Test scans
	// reverse, so a press on the track or thumb is answered by the bar and
	// never selects a theme row beneath its column. Nothing is registered when
	// every theme fits.
	p.bar = themeScrollbar{}
	if geom.HasThumb {
		trackX := b.originX + indent + 1 + rowWidth
		trackY := 1 + y + 1
		p.bar = themeScrollbar{
			params: ui.ScrollbarParams{
				TotalItems:   len(p.filtered),
				ScrollOffset: p.scroll,
				VisibleItems: p.rows,
				TrackHeight:  len(body),
			},
			trackX: trackX,
			trackY: trackY,
			has:    true,
		}
		b.m.mouse.HitMap.AddRect(ui.RegionScrollbarTrack, trackX, trackY, 1, geom.TrackRect.Dy(), nil)
		b.m.mouse.HitMap.AddRect(ui.RegionScrollbarThumb, trackX, trackY+geom.ThumbRect.Min.Y, 1, geom.ThumbRect.Dy(), nil)
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

// clickThemeRow is what a mouse click on a painted theme means. On Appearance
// it previews and waits for Enter to save: clicking used to write immediately,
// after which Escape had no preview left to restore and closed Configuration
// instead. The inline picker still commits the draft, because choosing is what
// that disclosure is for.
func (m *Model) clickThemeRow(index int) tea.Cmd {
	picker := m.activePicker()
	if picker == nil {
		return nil
	}
	picker.cursorTo(index)
	picker.preview()
	if picker.inline && picker.selectEntry != nil {
		return picker.selectEntry(m, picker.selected())
	}
	return nil
}

// pressThemeScrollbar begins the bar's gesture. Pressing the thumb grabs it at
// the pressed row; pressing the track jumps so the grabbed point becomes the
// thumb anchor — and either way the same drag continues from there, which is
// what makes a track click feel like catching a moving thumb.
func (m *Model) pressThemeScrollbar(picker *themePicker, action mouse.MouseAction) {
	bar := picker.bar
	if !bar.has || action.Region == nil {
		return
	}
	row := action.Y - bar.trackY
	offset := picker.scroll
	grab := row - ui.RowForOffset(bar.params, offset)
	if action.Region.ID == ui.RegionScrollbarTrack {
		offset = ui.OffsetAtRow(bar.params, row)
		grab = 0
		picker.setViewport(offset)
	}
	picker.gesture = themeScrollGesture{active: true, params: bar.params, trackY: bar.trackY, grabDelta: grab}
	m.mouse.StartDrag(action.X, action.Y, action.Region.ID, offset)
}

// dragThemeScrollbar maps the pointer row back onto the window through the
// shared inverse mapping, preserving where within the thumb the gesture
// grabbed. OffsetAtRow clamps past both ends of the track without ending
// anything.
func (m *Model) dragThemeScrollbar(picker *themePicker, action mouse.MouseAction) {
	gesture := picker.gesture
	if !gesture.active {
		return
	}
	picker.setViewport(ui.OffsetAtRow(gesture.params, action.Y-gesture.trackY-gesture.grabDelta))
}

// settleThemeScrollbar ends the gesture and previews once. A whole drag reads
// as one gesture, so the recolour waits for it rather than chasing every row
// the pointer crossed.
func (m *Model) settleThemeScrollbar(picker *themePicker) {
	if !picker.gesture.active {
		return
	}
	picker.gesture = themeScrollGesture{}
	picker.preview()
}

// dropThemeScrollbarGesture recovers a scrollbar drag whose release was lost —
// released outside the window, or focus stolen mid-gesture. The drag machinery
// has already ended its half on the first button-less motion; this drops ours
// quietly, without previewing, because the pointer is elsewhere now.
func (m *Model) dropThemeScrollbarGesture() {
	picker := m.activePicker()
	if picker != nil && picker.gesture.active && !m.mouse.IsDragging() {
		picker.gesture = themeScrollGesture{}
	}
}

// scrollbarStyle derives the bar's hover/drag emphasis from the page's pointer
// state, following the divider's HandleState convention.
func (m *Model) scrollbarStyle(b *paneBuilder) ui.ScrollbarStyle {
	dragging := isThemeScrollbarDrag(m.mouse.DragRegion())
	hovering := !dragging && b.hovering(ui.RegionScrollbarThumb, ui.RegionScrollbarTrack)
	state := ui.HandleStateFrom(hovering, dragging)
	return ui.ScrollbarStyle{Thumb: state, Track: state}
}

// themeListBox frames the visible window and hangs a scrollbar on its right
// edge. The frame is what makes focus and scroll position obvious; it also
// keeps the list's geometry stable so a "more above" line cannot jump the
// rows out from under the pointer. The bar renders with whatever pointer
// emphasis style carries — idle output stays byte-identical to plain
// RenderScrollbar — and reports where its thumb landed for hit registration.
func themeListBox(rows []string, scroll, total, visible, width int, focused bool, style ui.ScrollbarStyle) ([]string, ui.Geometry) {
	if width < 8 {
		width = 8
	}
	inner := width - 2
	rowWidth := max(1, inner-1)
	fitted := make([]string, len(rows))
	for i, row := range rows {
		if ansi.StringWidth(row) > rowWidth {
			fitted[i] = ansi.Truncate(row, rowWidth, "…")
		} else {
			fitted[i] = padDisplay(row, rowWidth)
		}
	}
	barLines, geom := ui.RenderScrollbarWithState(ui.ScrollbarParams{
		TotalItems:   total,
		ScrollOffset: scroll,
		VisibleItems: visible,
		TrackHeight:  len(fitted),
	}, style)
	bar := strings.Split(barLines, "\n")
	body := make([]string, len(fitted))
	for i, row := range fitted {
		track := " "
		if i < len(bar) {
			track = bar[i]
		}
		body[i] = row + track
	}
	fg := styles.BorderNormal
	if focused {
		fg = styles.BorderActive
	}
	boxed := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(fg).
		Render(strings.Join(body, "\n"))
	return strings.Split(boxed, "\n"), geom
}

// isThemeScrollbarDrag reports that a drag in flight began on the theme list's
// scrollbar, so its motion belongs to the bar rather than to whatever region
// the pointer is over now.
func isThemeScrollbarDrag(id string) bool {
	return id == ui.RegionScrollbarThumb || id == ui.RegionScrollbarTrack
}

func prefixLines(lines []string, pad string) []string {
	if pad == "" {
		return lines
	}
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = pad + line
	}
	return out
}

// themeRow paints one theme: its four-color swatch, its name, and what the list
// has to say about it on the right.
func themeRow(entry, current theme.Entry, width int, state State) string {
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

	var rightRendered string
	if entry.Same(current) {
		rightRendered = Badge("CURRENT", false)
	} else if right := theme.Label(entry); right != "" {
		rightRendered = mutedStyle().Render(right)
	}

	left := " " + Swatch(theme.Swatch(entry)) + "  " + nameStyle.Render(name)
	if rightRendered == "" {
		return HighlightRow(padDisplay(left, width), width, state)
	}
	return HighlightRow(padRight(left, rightRendered, width), width, state)
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
