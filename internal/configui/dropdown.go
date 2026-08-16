package configui

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/overlay"
	"github.com/marcus/sidecar/internal/styles"
)

// A select control in Configuration opens a real list that floats over the page
// behind it, the way the workspace create-worktree modal's branch picker does.
// Stepping to the next value on click — which is what every one of these
// controls used to do — hides the choices from the person being asked to choose.
//
// The mechanism is deliberately one thing, not one per page: a page declares its
// choices, and this file owns opening, moving, committing, dismissing, painting,
// and the hit regions the floating list needs.
//
// Where it is composited matters. The detail pane is built into a slice of
// lines, and buildDetail may run twice when a post-build fixup moves the row
// cursor. So the list is painted after the pane has settled — in renderDetail,
// against the final lines — which is what keeps a second build pass from
// registering the list's hit regions twice. The build passes only record where
// the open control landed.

const (
	// regionDropdownItem prefixes one row of an open list. The suffix is the
	// option's index.
	regionDropdownItem = "config-dropdown-item-"
	// regionDropdownMore covers the "n more" lines. They are not choices, but a
	// wheel notch over one belongs to the list.
	regionDropdownMore = "config-dropdown-more"
	// dropdownMaxVisible is how many choices a list shows before it scrolls.
	dropdownMaxVisible = 8
)

// dropdownOption is one choice in a select control.
type dropdownOption struct {
	// id is the stored value the choice means. It is what a page compares the
	// current setting against, so a label may change without changing what is
	// written to disk.
	id    string
	label string
	// desc is an optional quieter note after the label.
	desc string
}

// dropdownState is an open list. Exactly one can be open: it belongs to the
// control the user activated, and activating anything else closes it.
type dropdownState struct {
	// controlID is the selector the list hangs from.
	controlID string
	options   []dropdownOption
	cursor    int
	scroll    int
	// commit is what choosing an option means to the page that offered it —
	// a save, or a change to a draft.
	commit func(*Model, dropdownOption) tea.Cmd

	// Geometry, recorded by the build pass that painted the control. placed is
	// reset before every build: a list whose control is no longer on the page
	// has nothing to hang from and closes.
	placed bool
	// x is the column the list is left-aligned to, relative to the pane's
	// content origin; row is the index of the control's own line.
	x, row, width int
}

// dropdownOpen reports that a list is on screen.
func (m *Model) dropdownOpen() bool { return m.dropdown != nil }

// dropdownOpenFor reports that the open list belongs to a given control, which
// is how a selector knows to draw itself as expanded.
func (m *Model) dropdownOpenFor(id string) bool {
	return m.dropdown != nil && m.dropdown.controlID == id
}

// closeDropdown dismisses the list without committing.
func (m *Model) closeDropdown() bool {
	if m.dropdown == nil {
		return false
	}
	m.dropdown = nil
	return true
}

// openDropdown opens a control's list, or closes it if it is the one already
// open — activating a selector twice is how a user backs out of it. The list
// opens on the current value, so the first thing the arrows move from is what
// is configured now.
func (m *Model) openDropdown(controlID string, options []dropdownOption, currentID string, commit func(*Model, dropdownOption) tea.Cmd) tea.Cmd {
	if m.dropdownOpenFor(controlID) {
		m.closeDropdown()
		return nil
	}
	if len(options) == 0 {
		return nil
	}
	cursor := 0
	for i, option := range options {
		if option.id == currentID {
			cursor = i
			break
		}
	}
	m.dropdown = &dropdownState{
		controlID: controlID,
		options:   options,
		cursor:    cursor,
		commit:    commit,
	}
	m.dropdown.clamp()
	// The list answers the arrows and Enter, so the detail pane has to be where
	// the keyboard is — opening from a shortcut while the sidebar held it would
	// otherwise put a list on screen that the arrows walked straight past.
	m.detailFocus = true
	return nil
}

// clamp keeps the cursor inside the options and the window over the cursor.
func (d *dropdownState) clamp() {
	if d.cursor < 0 {
		d.cursor = 0
	}
	if d.cursor >= len(d.options) {
		d.cursor = max(0, len(d.options)-1)
	}
	rows := d.visibleRows()
	if d.cursor < d.scroll {
		d.scroll = d.cursor
	}
	if d.cursor >= d.scroll+rows {
		d.scroll = d.cursor - rows + 1
	}
	if d.scroll > len(d.options)-rows {
		d.scroll = len(d.options) - rows
	}
	if d.scroll < 0 {
		d.scroll = 0
	}
}

// visibleRows is how many choices the window shows.
func (d *dropdownState) visibleRows() int {
	return min(dropdownMaxVisible, max(1, len(d.options)))
}

func (d *dropdownState) maxScroll() int {
	return max(0, len(d.options)-d.visibleRows())
}

func (d *dropdownState) atScrollBoundary(delta int) bool {
	if len(d.options) == 0 {
		return true
	}
	if delta < 0 {
		return d.cursor <= 0
	}
	return d.cursor >= len(d.options)-1
}

// move steps the cursor, stopping at either end rather than wrapping: a list is
// something to read, and a cursor that jumps from the bottom back to the top
// makes the end of it hard to find.
func (d *dropdownState) move(delta int) bool {
	next := d.cursor + delta
	if next < 0 {
		next = 0
	}
	if next >= len(d.options) {
		next = len(d.options) - 1
	}
	if next == d.cursor {
		return false
	}
	d.cursor = next
	d.clamp()
	return true
}

// selected is the option under the cursor.
func (d *dropdownState) selected() (dropdownOption, bool) {
	if d.cursor < 0 || d.cursor >= len(d.options) {
		return dropdownOption{}, false
	}
	return d.options[d.cursor], true
}

// commitDropdown chooses the option under the cursor and closes the list.
func (m *Model) commitDropdown() tea.Cmd {
	dropdown := m.dropdown
	if dropdown == nil {
		return nil
	}
	option, ok := dropdown.selected()
	m.dropdown = nil
	if !ok || dropdown.commit == nil {
		return nil
	}
	return dropdown.commit(m, option)
}

// dropdownKey answers every key while a list is open. It consumes what it does
// not use on purpose: while a list is on screen it is the innermost thing there,
// and a key that fell through it would act on — or close — the surface behind
// the list the user is still reading.
func (m *Model) dropdownKey(key string) (bool, tea.Cmd) {
	if m.dropdown == nil {
		return false, nil
	}
	switch key {
	case "down", "j", "ctrl+n":
		m.dropdown.move(1)
	case "up", "k", "ctrl+p":
		m.dropdown.move(-1)
	case "home", "g":
		m.dropdown.move(-len(m.dropdown.options))
	case "end", "G":
		m.dropdown.move(len(m.dropdown.options))
	case "pgup":
		m.dropdown.move(-dropdownMaxVisible)
	case "pgdown":
		m.dropdown.move(dropdownMaxVisible)
	case "enter", " ":
		return true, m.commitDropdown()
	case "esc", "escape":
		m.closeDropdown()
	}
	return true, nil
}

// clickDropdownItem selects the option a click landed on and commits it, so
// mouse and keyboard leave the surface in the same state.
func (m *Model) clickDropdownItem(id string) tea.Cmd {
	if m.dropdown == nil {
		return nil
	}
	index, err := strconv.Atoi(strings.TrimPrefix(id, regionDropdownItem))
	if err != nil {
		return nil
	}
	if index < 0 || index >= len(m.dropdown.options) {
		return nil
	}
	m.dropdown.cursor = index
	return m.commitDropdown()
}

// scrollDropdown answers a wheel notch over an open list. Like the theme list,
// the notch moves the selection rather than the window alone: the window
// follows the cursor, so scrolling the selection out of sight would only snap
// back on the next keypress.
func (m *Model) scrollDropdown(id string, delta int) bool {
	if m.dropdown == nil {
		return false
	}
	if !strings.HasPrefix(id, regionDropdownItem) && id != regionDropdownMore {
		return false
	}
	m.dropdown.move(delta)
	return true
}

// --- declaration --------------------------------------------------------

// selectRow paints a labelled selector that opens a real list, and is the only
// way a page should offer a choice between fixed values. width is the control's
// preferred width; the pane narrows it if it has to.
func (b *paneBuilder) selectRow(id, label string, width int, options []dropdownOption, currentID string, commit func(*Model, dropdownOption) tea.Cmd) {
	current := dropdownLabel(options, currentID)
	b.selectRowValue(id, label, current, width, options, currentID, commit)
}

// selectRowValue is selectRow for a control whose closed label is not simply the
// selected option's — the project form's "Remember the last app used", which
// describes the absence of a choice rather than naming one.
func (b *paneBuilder) selectRowValue(id, label, current string, width int, options []dropdownOption, currentID string, commit func(*Model, dropdownOption) tea.Cmd) {
	fieldWidth := b.controlWidth(width)
	state := b.declare(id, "", true, func(m *Model) tea.Cmd {
		return m.openDropdown(id, options, currentID, commit)
	})
	state, arrow := b.m.dropdownControlState(id, state)
	line := FormRow(label, SelectorWidthArrow(current, arrow, fieldWidth, state), state)
	y := len(b.lines)
	b.lines = append(b.lines, strings.Split(line, "\n")...)
	b.m.mouse.HitMap.AddRect(id, b.originX, 1+y, b.inner, 1, nil)
	b.placeDropdown(id, ControlColumn, y, fieldWidth)
}

// dropdownControlState is how a selector draws itself given its list: ▾ while
// the list is closed, ▴ while it is open, and focused for as long as it is open
// whatever the row cursor is doing — an open list is where the keyboard is.
//
// Every selector goes through here, including the one that cannot use
// selectRowValue because it sits mid-line, so no site can drift into describing
// its own open state differently from the rest.
func (m *Model) dropdownControlState(id string, state State) (State, string) {
	if !m.dropdownOpenFor(id) {
		return state, "▾"
	}
	state.Focused = true
	state.Hovered = false
	return state, "▴"
}

// placeDropdown records where an open list should be painted: x is the column
// it aligns to and row is the line its control sits on, both relative to the
// pane's content. A control that is not the open one does nothing here, and a
// build in which the open control was never painted leaves the list unplaced,
// which closes it.
func (b *paneBuilder) placeDropdown(id string, x, row, width int) {
	if !b.m.dropdownOpenFor(id) {
		return
	}
	b.m.dropdown.placed = true
	b.m.dropdown.x = x
	b.m.dropdown.row = row
	b.m.dropdown.width = width
}

// dropdownLabel is what a stored value is called in a list of options.
//
// A value the list does not offer is reported as it is stored, never as the
// first option: a control that claims a value the configuration does not hold is
// lying about the setting, and a list that then opens on that claim turns a
// stray Enter into a silent change. A page that can hold off-ladder values —
// the capture limit, which Advanced accepts as free text — passes its own label
// through selectRowValue instead.
func dropdownLabel(options []dropdownOption, id string) string {
	for _, option := range options {
		if option.id == id {
			return option.label
		}
	}
	return id
}

// --- painting -----------------------------------------------------------

// compositeDropdown draws an open list over the pane that has already been
// built, and registers the list's hit regions. It is called once per frame,
// after the pane has settled, so the regions can never be doubled by a second
// build pass.
//
// The list floats: it is drawn onto the lines that are already there rather than
// inserted between them, so nothing behind it moves when it opens.
func (m *Model) compositeDropdown(lines []string, originX, inner, height int) []string {
	dropdown := m.dropdown
	if dropdown == nil {
		return lines
	}
	if !dropdown.placed {
		// The control the list belongs to is not on this page any more.
		m.dropdown = nil
		return lines
	}
	dropdown.placed = false
	dropdown.clamp()

	width := dropdown.width
	if available := inner - dropdown.x; width > available {
		width = available
	}
	// A list with nowhere to be painted is closed rather than left open and
	// invisible: an open list swallows every key, so one the user cannot see is
	// a surface that has stopped answering with no way to tell why.
	if width < 8 || height < 2 || dropdown.row >= height || dropdown.row < 0 {
		m.dropdown = nil
		return lines
	}

	rows := m.dropdownLines(dropdown, width)
	if len(rows) == 0 {
		m.dropdown = nil
		return lines
	}

	// Below the control if it fits, above it if not, and pinned inside the pane
	// if neither does: a list that ran off the bottom would simply be invisible.
	y := dropdown.row + 1
	if y+len(rows) > height {
		if above := dropdown.row - len(rows); above >= 0 {
			y = above
		} else {
			y = max(0, height-len(rows))
		}
	}

	// The pane may be shorter than the lines the page painted; the list needs
	// real lines under it to draw onto.
	for len(lines) < min(height, y+len(rows)) {
		lines = append(lines, "")
	}

	block := overlay.Composite(strings.Join(lines, "\n"), strings.Join(rows, "\n"), dropdown.x, y)
	painted := strings.Split(block, "\n")

	// Only rows that are actually on screen get a region. clampLines discards
	// anything past the pane's height, and a region under a discarded row is a
	// click that commits a value the user never saw.
	for i := range rows {
		if y+i >= len(painted) || y+i >= height {
			break
		}
		m.mouse.HitMap.AddRect(m.dropdownRowID(dropdown, i), originX+dropdown.x, 1+y+i, width, 1, nil)
	}
	return painted
}

// dropdownRowID names the region a painted row answers to: a choice by its
// option index, a "more" line by the shared id the wheel reads.
func (m *Model) dropdownRowID(d *dropdownState, painted int) string {
	index := painted
	if d.scroll > 0 {
		if painted == 0 {
			return regionDropdownMore
		}
		index--
	}
	option := d.scroll + index
	if option >= len(d.options) || option >= d.scroll+d.visibleRows() {
		return regionDropdownMore
	}
	return fmt.Sprintf("%s%d", regionDropdownItem, option)
}

// dropdownLines renders the floating list: the window of choices, plus a quiet
// count at either end when there are more than the window shows.
func (m *Model) dropdownLines(d *dropdownState, width int) []string {
	rows := make([]string, 0, d.visibleRows()+2)
	if d.scroll > 0 {
		rows = append(rows, dropdownMoreLine(fmt.Sprintf("↑ %d more", d.scroll), width))
	}
	end := min(len(d.options), d.scroll+d.visibleRows())
	for i := d.scroll; i < end; i++ {
		option := d.options[i]
		state := State{
			Focused: i == d.cursor,
			Hovered: m.hoverID == fmt.Sprintf("%s%d", regionDropdownItem, i),
		}
		rows = append(rows, DropdownRow(option.label, option.desc, width, state))
	}
	if remaining := len(d.options) - end; remaining > 0 {
		rows = append(rows, dropdownMoreLine(fmt.Sprintf("↓ %d more", remaining), width))
	}
	return rows
}

func dropdownMoreLine(text string, width int) string {
	return lipgloss.NewStyle().
		Foreground(styles.TextMuted).
		Background(styles.BgTertiary).
		Width(width).
		Render("  " + text)
}

// DropdownRow paints one choice in an open list. The list is opaque — every row
// carries its own background — because it is drawn over content that is still
// there underneath.
func DropdownRow(label, desc string, width int, state State) string {
	style := lipgloss.NewStyle().
		Foreground(styles.TextPrimary).
		Background(styles.BgTertiary)
	cursor := "  "
	switch {
	case state.Focused:
		style = lipgloss.NewStyle().
			Foreground(styles.OnPrimaryColor).
			Background(styles.Primary).
			Bold(true)
		cursor = "▸ "
	case state.Hovered:
		style = lipgloss.NewStyle().
			Foreground(styles.TextPrimary).
			Background(styles.SurfaceRaised)
	}
	text := label
	if desc != "" {
		text += "  " + desc
	}
	text = cursor + text
	if ansi.StringWidth(text) > width {
		text = ansi.Truncate(text, width, "…")
	}
	return style.Width(width).Render(text)
}
