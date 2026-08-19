package docview

import (
	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/textselect"
)

// Text selection for a document pane.
//
// The binding lives here, once, because one Model is the file pane in the
// project workspace AND in the global Workspaces browser. A pane host says
// where its leaf was drawn and forwards the pointer and key events it already
// routes; everything about what a gesture means, what is highlighted and what
// reaches the clipboard is decided here and inherited by both surfaces. A rule
// written in a host would be a rule the other surface does not have.
//
// The coordinate space is the laid-out visual row: [displayRows] has already
// wrapped and tab-expanded the document, so the engine never sees wrapping and
// never has to guess where a tab stop landed. Rows are numbered absolutely, so a
// selection survives scrolling, and the gutter is not part of a row's text at
// all, so it can be neither selected nor copied.

// SetSelection binds the host's shared selection settings: the chords the pane
// answers, and whether finishing a drag copies without being asked.
func (m *Model) SetSelection(keys textselect.Keys, copyOnSelect bool) {
	if m == nil {
		return
	}
	m.selection.Keys = keys
	m.selection.CopyOnSelect = copyOnSelect
}

// SetOrigin records where the host last drew this document's content box, in
// the coordinate space its mouse events arrive in. A pane that has not been
// drawn has no origin, so it hit-tests as empty rather than as the top-left
// corner of the screen.
func (m *Model) SetOrigin(x, y int) {
	if m == nil {
		return
	}
	m.originX, m.originY = x, y
}

// SelectionKeys are the chords this document answers, for a host phrasing the
// copy its own notification type carries.
func (m *Model) SelectionKeys() textselect.Keys {
	if m == nil {
		return textselect.Keys{}
	}
	return m.selection.Keys
}

// HasSelection reports whether anything in this document is selected. It is
// asked by hosts outside a render — a key, a click elsewhere — so it settles the
// expiry itself rather than reporting a selection the next frame will drop.
func (m *Model) HasSelection() bool {
	if m == nil {
		return false
	}
	m.expireSelection()
	return m.selection.HasSelection()
}

// ClearSelection drops the selection and any gesture editing it.
func (m *Model) ClearSelection() {
	if m == nil {
		return
	}
	m.selection.Clear()
}

// AbandonSelection ends a gesture whose release never arrived — the pointer left
// the window, a modal opened, focus moved.
func (m *Model) AbandonSelection() textselect.Result {
	if m == nil {
		return textselect.Result{}
	}
	return m.selection.Abandon()
}

// SelectionText is the selection as the user sees it: the visible rows, without
// the styling they were drawn with and without the gutter in front of them.
func (m *Model) SelectionText() []string {
	if m == nil {
		return nil
	}
	m.expireSelection()
	return m.selection.SelectedText(selectionSource{m})
}

// HandleSelectionMouse advances the selection gesture and reports what the host
// owes for it.
//
// A drag that has run past an edge is scrolled here rather than by the host:
// the window it is asking for is this model's own, and a host that moved it
// would be the second place that knows how far a document can scroll. The rows
// it reveals are selected by the next motion event, as the engine's contract
// says, and the applied delta is reported so a host that persists its scroll
// offset can see that it moved.
func (m *Model) HandleSelectionMouse(action mouse.MouseAction) textselect.Result {
	if m == nil {
		return textselect.Result{}
	}
	result := m.selection.HandleMouse(action, selectionSource{m})
	if result.AutoScroll != 0 {
		m.Scroll(result.AutoScroll)
	}
	return result
}

// HandleSelectionKey answers the chords that act on the selection: copy, and
// select-all.
func (m *Model) HandleSelectionKey(msg tea.KeyMsg) textselect.Result {
	if m == nil {
		return textselect.Result{}
	}
	return m.selection.HandleKey(msg, selectionSource{m})
}

// expireSelection drops a selection whose rows are about to be replaced.
//
// A selection names visual rows, and everything the layout key tracks changes
// which text is on which row: a new document, a live re-read, a wrap toggle, a
// width that re-wraps. Hanging the check off the layout the renderer is already
// keying on means a new reason to re-lay out cannot forget to clear it.
func (m *Model) expireSelection() {
	key := m.currentLayoutKey()
	if m.selectionKey == key {
		return
	}
	m.selectionKey = key
	m.selection.Clear()
}

// selectionSource is the document as the selection engine reads it: where its
// text is drawn, what its rows say, and how far it is scrolled.
type selectionSource struct{ m *Model }

var _ textselect.Source = selectionSource{}

// ContentRect is the content box minus the gutter, which is what makes the
// gutter unselectable: a press on a line number lands outside the surface
// entirely and stays the host's ordinary click.
func (s selectionSource) ContentRect() mouse.Rect {
	m := s.m
	gutter := m.display().gutterWidth
	return mouse.Rect{
		X: m.originX + gutter,
		Y: m.originY,
		W: max(m.width-gutter, 0),
		H: m.height,
	}
}

func (s selectionSource) Line(i int) string {
	rows := s.m.display().rows
	if i < 0 || i >= len(rows) {
		return ""
	}
	return rows[i]
}

func (s selectionSource) LineCount() int { return len(s.m.display().rows) }

func (s selectionSource) Scroll() int { return s.m.scroll }

// TabWidth is zero because the rows hold no tabs: the layout expanded them in
// the column space they are drawn in, which the engine could not have
// reproduced from a tab width alone — it does not know the gutter is in front
// of them.
func (s selectionSource) TabWidth() int { return 0 }
