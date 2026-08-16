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
	run    func(*Model) tea.Cmd
}

// paneBuilder accumulates a detail pane's lines and controls together.
type paneBuilder struct {
	m *Model
	// originX is the pane's content origin in content-area coordinates, so hit
	// regions land where the lines are painted.
	originX int
	inner   int
	lines   []string
}

func (m *Model) newPaneBuilder(originX, inner int) *paneBuilder {
	m.controls = m.controls[:0]
	return &paneBuilder{m: m, originX: originX, inner: inner}
}

// text appends plain lines.
func (b *paneBuilder) text(lines ...string) {
	b.lines = append(b.lines, lines...)
}

// blank appends an empty line.
func (b *paneBuilder) blank() { b.lines = append(b.lines, "") }

// declare registers a control and returns the interaction state its renderer
// should use. Declaration order is cursor order.
func (b *paneBuilder) declare(id, key string, cursor bool, run func(*Model) tea.Cmd) State {
	index := len(b.m.controls)
	b.m.controls = append(b.m.controls, control{id: id, key: key, cursor: cursor, run: run})
	state := State{Hovered: b.m.hoverID == id}
	if cursor && b.m.detailOwnsKeys() && b.m.cursorControl() == index {
		state.Focused = true
		state.Hovered = false
	}
	return state
}

// row declares a control and paints it. render receives the control's state and
// may return more than one line; the whole block is the mouse target, which is
// what makes a two-line repair row one control rather than a title with a
// caption underneath it.
func (b *paneBuilder) row(id, key string, run func(*Model) tea.Cmd, render func(State) string) {
	state := b.declare(id, key, true, run)
	block := render(state)
	lines := strings.Split(block, "\n")
	y := len(b.lines)
	b.lines = append(b.lines, lines...)
	b.m.mouse.HitMap.AddRect(id, b.originX, 1+y, b.inner, len(lines), nil)
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
func (m *Model) runShortcut(key string) (bool, tea.Cmd) {
	for i, c := range m.controls {
		if c.key != "" && c.key == key {
			if c.cursor {
				m.focusControlIndex(i)
			}
			return true, m.runControl(i)
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
