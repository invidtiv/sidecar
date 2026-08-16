package configui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// A Configuration page is a list of lines, some of which are controls. The
// builder is what keeps the three descriptions of a control — what it looks
// like, where the mouse can hit it, and what the keyboard does with it — from
// drifting apart: a page declares each control once and gets all three.
//
// Controls are rebuilt on every render, which is also what makes the keyboard
// follow the visible page: a row that is not on screen is not a control.

// control is one focusable, clickable thing in the detail pane.
type control struct {
	id string
	// key is a shortcut that runs this control from anywhere on the page —
	// the mockups' "C copy", "R recheck", "O open". Empty means the control is
	// reachable only through the cursor or the mouse.
	key string
	// cursor marks a control the row cursor stops on.
	cursor bool
	// clickless means a mouse click focuses the control but does not run it.
	// Enter still runs it. A settings row can be selected without flipping its
	// toggle; the ON/OFF pill is a separate control that does run on click.
	clickless bool
	run       func(*Model) tea.Cmd
}

// paneBuilder accumulates a detail pane's lines and controls together.
type paneBuilder struct {
	m *Model
	// originX is the pane's content origin in content-area coordinates, so hit
	// regions land where the lines are painted.
	originX int
	inner   int
	// height is the number of lines the pane can paint, so a page that pins
	// content to the bottom knows where the bottom is.
	height int
	lines  []string
	// stops counts the cursor stops declared so far, which is the position the
	// row cursor is expressed in. Comparing against it is what lets declare
	// decide focus while the control list is still half-built.
	stops int
}

func (m *Model) newPaneBuilder(originX, inner, height int) *paneBuilder {
	// The cursor's identity has to survive the rebuild: the indices it is
	// expressed in are about to be thrown away and recreated, and a list that
	// scrolled or gained a divider since the last frame will not recreate them
	// in the same order.
	m.captureFocus()
	m.controls = m.controls[:0]
	return &paneBuilder{m: m, originX: originX, inner: inner, height: height}
}

// captureFocus records, by id, the control the row cursor is on, from the
// controls the last completed frame declared.
func (m *Model) captureFocus() {
	m.focusedID = ""
	if !m.detailOwnsKeys() {
		return
	}
	if m.pendingFocus != "" {
		// A control asked for but not yet rendered is where the cursor is
		// going, so that is the cursor's identity for this frame. A list that
		// claims the cursor can then paint the right row on the frame that
		// first shows it, rather than one frame later.
		m.focusedID = m.pendingFocus
		return
	}
	if index := m.cursorControl(); index >= 0 && index < len(m.controls) {
		m.focusedID = m.controls[index].id
	}
}

// spacer pushes what follows to the bottom of the pane. following is how many
// lines the caller is about to paint; a two-row margin keeps the last of them
// clear of the panel's bottom border. On a pane too short to hold both the
// content and the block it does nothing, so nothing is ever pushed off screen
// to make room for a signature.
func (b *paneBuilder) spacer(following int) {
	const bottomMargin = 2
	for len(b.lines)+following+bottomMargin < b.height {
		b.lines = append(b.lines, "")
	}
}

// text appends plain lines. A block that carries its own newlines — a section
// header supplies the blank line above it, and several row renderers return a
// caption under their first line — becomes one entry per painted row. Every hit
// region and the pane's height clamp are measured from this index, so an entry
// that painted two rows while counting as one moved every control below it out
// from under the mouse.
func (b *paneBuilder) text(lines ...string) {
	for _, line := range lines {
		if strings.Contains(line, "\n") {
			b.lines = append(b.lines, strings.Split(line, "\n")...)
			continue
		}
		b.lines = append(b.lines, line)
	}
}

// controlWidth is ControlWidth against this pane's writable width, so a page
// declares the width it wants and the pane decides what it can give.
func (b *paneBuilder) controlWidth(preferred int) int {
	return ControlWidth(b.inner, preferred)
}

// help paints muted help aligned to the control column, wrapped to the pane
// rather than cut off at its edge. Help that ends in an ellipsis two thirds of
// the way through its sentence is not help, and a narrow terminal is exactly
// where a user needs the sentence most.
func (b *paneBuilder) help(text string) {
	b.text(WrapAt(text, b.inner, ControlColumn, Muted))
}

// note paints muted prose indented with the section's rows, wrapped the same
// way.
func (b *paneBuilder) note(text string) {
	b.text(WrapAt(text, b.inner, RowIndent, Muted))
}

// lead paints a page's introductory or closing prose at the pane's left edge,
// wrapped rather than truncated.
func (b *paneBuilder) lead(text string) {
	b.text(WrapAt(text, b.inner, 0, Muted))
}

// blank appends an empty line.
func (b *paneBuilder) blank() { b.lines = append(b.lines, "") }

// declare registers a control and returns the interaction state its renderer
// should use. Declaration order is cursor order.
//
// This is the only place a control is told it is focused. Focus is decided
// against the builder's own count of cursor stops rather than against
// cursorControl(), which reads the finished control list: mid-build that list
// ends at the control being declared, so its clamp made every control at or
// above the cursor answer "that is me" — which is what painted the whole top of
// a page, and the whole top of the theme list, as selected.
func (b *paneBuilder) declare(id, key string, cursor bool, run func(*Model) tea.Cmd) State {
	return b.declareControl(id, key, cursor, false, run)
}

// declareClickless registers a control the mouse can focus without activating.
func (b *paneBuilder) declareClickless(id, key string, cursor bool, run func(*Model) tea.Cmd) State {
	return b.declareControl(id, key, cursor, true, run)
}

func (b *paneBuilder) declareControl(id, key string, cursor, clickless bool, run func(*Model) tea.Cmd) State {
	b.m.controls = append(b.m.controls, control{id: id, key: key, cursor: cursor, clickless: clickless, run: run})
	state := State{Hovered: b.m.hoverID == id}
	if cursor {
		stop := b.stops
		b.stops++
		if b.m.detailOwnsKeys() && stop == b.m.rowCursor {
			state.Focused = true
			state.Hovered = false
		}
	}
	return state
}

// hovering reports that the pointer is over any of the given regions, so a row
// and the pills that sit on it can share one hover highlight.
func (b *paneBuilder) hovering(ids ...string) bool {
	for _, id := range ids {
		if b.m.hoverID == id {
			return true
		}
	}
	return false
}

// claimCursor lets a list that keeps its own selection — the theme picker,
// which scrolls a window over hundreds of themes and moves inside it with its
// own keys — put the pane's row cursor on the row it has selected, so the two
// can never name different rows. rowsAhead is how many cursor stops the list is
// about to declare before the selected one.
//
// It does nothing unless the row cursor is already inside the list: a cursor
// the user has moved out to the rest of the page must not be dragged back in.
func (b *paneBuilder) claimCursor(prefix string, rowsAhead int) {
	if !strings.HasPrefix(b.m.focusedID, prefix) {
		return
	}
	b.m.rowCursor = b.stops + rowsAhead
}

// row declares a control and paints it. render receives the control's state and
// may return more than one line; the whole block is the mouse target, which is
// what makes a two-line repair row one control rather than a title with a
// caption underneath it.
func (b *paneBuilder) row(id, key string, run func(*Model) tea.Cmd, render func(State) string) {
	b.paintRow(id, b.declare(id, key, true, run), render)
}

// focusRow is a row the mouse selects without activating. Enter still runs it.
func (b *paneBuilder) focusRow(id, key string, run func(*Model) tea.Cmd, render func(State) string) {
	b.paintRow(id, b.declareClickless(id, key, true, run), render)
}

func (b *paneBuilder) paintRow(id string, state State, render func(State) string) {
	block := render(state)
	lines := strings.Split(block, "\n")
	y := len(b.lines)
	b.lines = append(b.lines, lines...)
	b.m.mouse.HitMap.AddRect(id, b.originX, 1+y, b.inner, len(lines), nil)
}

const toggleSuffix = "-toggle"

// toggleRow paints a labelled ON/OFF setting. The row is selectable; only the
// pill itself toggles on click. Enter on the row still toggles.
func (b *paneBuilder) toggleRow(id, label string, on bool, run func(*Model) tea.Cmd) {
	toggleID := id + toggleSuffix
	rowState := b.declareClickless(id, "", true, run)
	toggleState := b.declare(toggleID, "", false, run)
	if rowState.Focused {
		toggleState.Focused = true
		toggleState.Hovered = false
	} else if b.hovering(toggleID) {
		rowState.Hovered = true
		toggleState.Hovered = true
	}
	pill := Toggle(on, toggleState)
	y := len(b.lines)
	b.lines = append(b.lines, FormRow(label, pill, rowState))
	b.m.mouse.HitMap.AddRect(id, b.originX, 1+y, b.inner, 1, nil)
	b.m.mouse.HitMap.AddRect(toggleID, b.originX+ControlColumn, 1+y, ansi.StringWidth(pill), 1, nil)
}

// panelToggle paints a two-line surface switch the way Panels & Integrations
// does: title and pill on the first line, muted detail underneath. Clicking
// the row focuses it; clicking the pill toggles.
func (b *paneBuilder) panelToggle(id, title, badge, detail string, on bool, run func(*Model) tea.Cmd) {
	toggleID := id + toggleSuffix
	rowState := b.declareClickless(id, "", true, run)
	toggleState := b.declare(toggleID, "", false, run)
	if rowState.Focused {
		toggleState.Focused = true
		toggleState.Hovered = false
	} else if b.hovering(toggleID) {
		rowState.Hovered = true
		toggleState.Hovered = true
	}
	pill := Toggle(on, toggleState)
	block := PanelRow(title, badge, detail, pill, b.inner, rowState)
	lines := strings.Split(block, "\n")
	y := len(b.lines)
	b.lines = append(b.lines, lines...)
	b.m.mouse.HitMap.AddRect(id, b.originX, 1+y, b.inner, len(lines), nil)
	b.m.mouse.HitMap.AddRect(toggleID, b.originX+b.inner-ansi.StringWidth(pill), 1+y, ansi.StringWidth(pill), 1, nil)
}

// buttonSpec is one pill in a button row.
type buttonSpec struct {
	id      string
	key     string
	label   string
	primary bool
	run     func(*Model) tea.Cmd
}

// buttons paints a row of action pills, each its own control with its own hit
// region. The visible key in the label and the registered shortcut are the same
// string, so the mockup's footer and the keyboard cannot disagree.
func (b *paneBuilder) buttons(specs ...buttonSpec) {
	rendered := make([]string, 0, len(specs))
	states := make([]State, 0, len(specs))
	for _, spec := range specs {
		state := b.declare(spec.id, spec.key, true, spec.run)
		states = append(states, state)
		rendered = append(rendered, Button(spec.label, spec.primary, state))
	}
	line := ButtonRow(rendered...)
	y := len(b.lines)
	b.lines = append(b.lines, line)

	x := b.originX + RowIndent
	for i, pill := range rendered {
		width := ansi.StringWidth(pill)
		b.m.mouse.HitMap.AddRect(specs[i].id, x, 1+y, width, 1, nil)
		x += width + 2 // ButtonRow joins with two spaces
	}
}

// pillRegions registers hit regions for controls already rendered along one
// line at the shared row indent, and hangs any open list from the pill it
// belongs to. The region and the list read the same accumulated column, so a
// control that sits mid-line cannot end up with its list under a neighbour.
// Pills are joined with two spaces, exactly as ButtonRow joins them.
func (b *paneBuilder) pillRegions(y int, ids, pills []string, listWidth int) {
	paneX := RowIndent
	for i, id := range ids {
		width := ansi.StringWidth(pills[i])
		b.m.mouse.HitMap.AddRect(id, b.originX+paneX, 1+y, width, 1, nil)
		b.placeDropdown(id, paneX, y, max(width, listWidth))
		paneX += width + 2
	}
}

// rightControl paints a control pinned to the right of a header line, the way
// Diagnostics' Recheck and a child route's Back control sit.
//
// It is deliberately not a cursor stop: the row cursor should start on the work
// the page is about, and both of these controls have their own key (R, and
// Escape) plus a mouse target.
func (b *paneBuilder) rightControl(left, id, key, label string, run func(*Model) tea.Cmd) {
	state := b.declare(id, key, false, run)
	pill := Button(label, false, state)
	pad := b.inner - ansi.StringWidth(left) - ansi.StringWidth(pill)
	if pad < 1 {
		pad = 1
	}
	y := len(b.lines)
	b.lines = append(b.lines, left+strings.Repeat(" ", pad)+pill)
	b.m.mouse.HitMap.AddRect(id, b.originX+b.inner-ansi.StringWidth(pill), 1+y, ansi.StringWidth(pill), 1, nil)
}

// rightControlPrimary is rightControl for an action the page recommends — the
// Projects page's Add project, which is the point of the page rather than an
// afterthought, so it is both a cursor stop and a pill with its own key.
func (b *paneBuilder) rightControlPrimary(left, id, key, label string, run func(*Model) tea.Cmd) {
	state := b.declare(id, key, true, run)
	pill := Button(label, true, state)
	pad := b.inner - ansi.StringWidth(left) - ansi.StringWidth(pill)
	if pad < 1 {
		pad = 1
	}
	y := len(b.lines)
	b.lines = append(b.lines, left+strings.Repeat(" ", pad)+pill)
	b.m.mouse.HitMap.AddRect(id, b.originX+b.inner-ansi.StringWidth(pill), 1+y, ansi.StringWidth(pill), 1, nil)
}

// cursorControls are the controls the row cursor visits, in order.
func (m *Model) cursorControls() []int {
	var out []int
	for i, c := range m.controls {
		if c.cursor {
			out = append(out, i)
		}
	}
	return out
}

// cursorControl is the index into controls the cursor is on, or -1.
func (m *Model) cursorControl() int {
	visits := m.cursorControls()
	if len(visits) == 0 {
		return -1
	}
	index := m.rowCursor
	if index >= len(visits) {
		index = len(visits) - 1
	}
	if index < 0 {
		index = 0
	}
	return visits[index]
}

// detailOwnsKeys reports that the detail pane has the arrows and Enter. A child
// route always owns them: it is the only thing on screen the user came for.
func (m *Model) detailOwnsKeys() bool {
	return m.Route().IsChild() || m.detailFocus
}

// hasDetailControls reports whether the current page offers anything to focus.
func (m *Model) hasDetailControls() bool { return len(m.cursorControls()) > 0 }

// moveRowCursor moves within the detail pane's controls.
func (m *Model) moveRowCursor(delta int) {
	visits := m.cursorControls()
	if len(visits) == 0 {
		return
	}
	m.rowCursor += delta
	if m.rowCursor < 0 {
		m.rowCursor = 0
	}
	if m.rowCursor >= len(visits) {
		m.rowCursor = len(visits) - 1
	}
}

// clampRowCursor keeps the cursor inside the controls the last render produced.
// A page whose contents changed under it — a resolved check, a collapsed
// section — must not leave the cursor pointing past the end.
func (m *Model) clampRowCursor() {
	visits := len(m.cursorControls())
	if visits == 0 {
		m.rowCursor = 0
		m.detailFocus = false
		return
	}
	if m.rowCursor >= visits {
		m.rowCursor = visits - 1
	}
	if m.rowCursor < 0 {
		m.rowCursor = 0
	}
}

// runControl runs a control by index into controls.
func (m *Model) runControl(index int) tea.Cmd {
	if index < 0 || index >= len(m.controls) {
		return nil
	}
	run := m.controls[index].run
	if run == nil {
		return nil
	}
	return run(m)
}

// runShortcut runs the control a key belongs to, if any. Shortcuts stay live
// whether or not the detail pane holds the cursor: the footer advertises them
// for the page, not for a focus state the user cannot see.
// The visible key in a control's label is the mockups' capital — "A  Add
// project", "R  Recheck", "G  Use global theme" — while the registered shortcut
// is the lowercase letter the footer advertises. Both must work: a surface that
// prints A and answers only to a is lying about its own keyboard. An exact
// match still wins, so a page could bind the two cases separately if it ever
// needed to.
func (m *Model) runShortcut(key string) (bool, tea.Cmd) {
	for i, c := range m.controls {
		if c.key != "" && c.key == key {
			if c.cursor {
				m.focusControlIndex(i)
			}
			return true, m.runControl(i)
		}
	}
	if lowered := strings.ToLower(key); lowered != key {
		for i, c := range m.controls {
			if c.key != "" && c.key == lowered {
				if c.cursor {
					m.focusControlIndex(i)
				}
				return true, m.runControl(i)
			}
		}
	}
	return false, nil
}

// focusControlIndex puts the row cursor on a control by its controls index.
func (m *Model) focusControlIndex(index int) {
	for position, visit := range m.cursorControls() {
		if visit == index {
			m.rowCursor = position
			return
		}
	}
}
