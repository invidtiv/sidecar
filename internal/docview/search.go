package docview

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/ui"
)

// In-file search for a document pane.
//
// It lives here, beside select.go and for the same reason: one Model is the
// file pane in the project workspace AND in the global Workspaces browser, so a
// rule written in a host is a rule the other surface does not have. Everything
// search means — what matches, what n and N do, where the viewport lands, what
// the bar says, and who wins when a match sits under a selection — is decided
// once, here, and inherited.
//
// The coordinate space is the laid-out row, not the file's bytes. A source line
// is tab-expanded and, with wrap on, split across several visual rows, so a
// match is stored against the *joined plain text of the rows a content line
// occupies*: the concatenation of the drawn rows with their styling stripped.
// That is the text the user is actually looking at, which makes two otherwise
// fiddly cases fall out for free — a query that lands inside a tab-expanded
// stretch, and a match that straddles a wrap boundary and must paint on both of
// the rows it covers.

// Match is one hit, in the coordinate space described above: Line indexes the
// content lines of the current layout (banner rows included, because they are
// on screen and searchable), and Start/End are byte offsets into that line's
// joined plain text.
type Match struct {
	Line     int
	StartCol int
	EndCol   int
}

// searchPhase is the two-phase vim model: type a query, commit it, then
// navigate. It matches the file browser's content search exactly, because a
// user moving between the two surfaces should not have to learn it twice.
type searchPhase int

const (
	searchOff searchPhase = iota
	searchTyping
	searchCommitted
)

// searchState is everything a live search knows.
type searchState struct {
	phase   searchPhase
	query   string
	matches []Match
	cursor  int

	// key is the layout the matches were computed against. A match names rows,
	// so a re-read, a wrap toggle or a width change invalidates it the same way
	// it invalidates a selection — but unlike a selection a search survives the
	// change: the matches are recomputed and the cursor is clamped, so toggling
	// wrap while committed keeps the user on (or beside) the match they were on.
	key   layoutKey
	valid bool
}

// scrollMargin is how many rows of context a jump-to-match keeps between the
// match and the edge of the viewport, and the band inside which an already
// visible match is left alone rather than re-centred.
const scrollMargin = 2

// StartSearch opens the search bar in its typing phase with an empty query.
func (m *Model) StartSearch() {
	if m == nil {
		return
	}
	m.search = searchState{phase: searchTyping}
	m.clampScroll()
}

// CloseSearch ends the search and drops its matches.
func (m *Model) CloseSearch() {
	if m == nil {
		return
	}
	m.search = searchState{}
	m.clampScroll()
}

// SearchActive reports whether the search bar is on screen and owns the pane's
// keys. A host asks this to decide whether a keypress is search's before it is
// anyone else's.
func (m *Model) SearchActive() bool { return m != nil && m.search.phase != searchOff }

// searchActive is the same question asked from inside a render, where m is
// known non-nil and the state must not be resolved through a method that could
// re-enter layout.
func (m *Model) searchActive() bool { return m.search.phase != searchOff }

// SearchCommitted reports whether the query has been committed with enter, so
// n/N navigate rather than typing into the query.
func (m *Model) SearchCommitted() bool { return m != nil && m.search.phase == searchCommitted }

// SearchQuery is the query as typed.
func (m *Model) SearchQuery() string {
	if m == nil {
		return ""
	}
	return m.search.query
}

// SearchMatches is the current match set, recomputed first if the document or
// its layout moved underneath it.
func (m *Model) SearchMatches() []Match {
	if m == nil || !m.searchActive() {
		return nil
	}
	m.ensureMatches()
	return m.search.matches
}

// SearchMatchIndex is the 0-based cursor into SearchMatches.
func (m *Model) SearchMatchIndex() int {
	if m == nil {
		return 0
	}
	m.ensureMatches()
	return m.search.cursor
}

// HandleSearchKey answers a key while search owns the pane. It reports whether
// the key was consumed; a live search consumes every key it is offered, which
// is what makes esc the only way out and stops a query containing "n" from
// scrolling the document.
//
// The returned command exists for hosts to batch: search itself has no side
// effects outside the model today.
func (m *Model) HandleSearchKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	if m == nil || !m.searchActive() {
		return false, nil
	}
	press, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return false, nil
	}
	key := press.String()

	// Esc always leaves search entirely, from either phase.
	if key == "esc" {
		m.CloseSearch()
		return true, nil
	}

	if m.search.phase == searchTyping {
		switch key {
		case "enter":
			if m.search.query != "" {
				m.search.phase = searchCommitted
			}
		case "backspace":
			if m.search.query != "" {
				runes := []rune(m.search.query)
				m.search.query = string(runes[:len(runes)-1])
				m.updateMatches()
			}
		default:
			// Everything printable is query text, including n and N: while
			// typing there is nothing to navigate yet.
			if text := ui.PrintableKeyText(press); text != "" {
				m.search.query += text
				m.updateMatches()
			}
		}
		return true, nil
	}

	// Committed: n/N walk the matches, the ordinary scroll keys still scroll,
	// and enter leaves search where it is standing.
	m.ensureMatches()
	switch key {
	case "n":
		if len(m.search.matches) > 0 {
			m.search.cursor = (m.search.cursor + 1) % len(m.search.matches)
			m.scrollToMatch()
		}
	case "N":
		if len(m.search.matches) > 0 {
			m.search.cursor--
			if m.search.cursor < 0 {
				m.search.cursor = len(m.search.matches) - 1
			}
			m.scrollToMatch()
		}
	case "enter":
		m.CloseSearch()
	case "j", "down", "k", "up", "ctrl+d", "pgdown", "ctrl+u", "pgup", "g", "home", "G", "end":
		m.HandleKey(press)
	}
	return true, nil
}

// updateMatches recomputes from scratch after the query changed, and lands the
// viewport on the first hit.
func (m *Model) updateMatches() {
	m.computeMatches()
	m.search.cursor = 0
	if len(m.search.matches) > 0 {
		m.scrollToMatch()
	}
}

// ensureMatches recomputes the matches when the layout they were found in is no
// longer the one on screen — a live re-read, a wrap toggle, a resize. The cursor
// is clamped rather than reset, so a committed search survives the change with
// the user still on a match rather than thrown back to the first one.
func (m *Model) ensureMatches() {
	if !m.searchActive() {
		return
	}
	if m.search.valid && m.search.key == m.currentLayoutKey() {
		return
	}
	cursor := m.search.cursor
	m.computeMatches()
	m.search.cursor = min(max(cursor, 0), max(len(m.search.matches)-1, 0))
}

// computeMatches scans the drawn text case-insensitively.
func (m *Model) computeMatches() {
	display := m.display()
	m.search.matches = nil
	m.search.cursor = 0
	m.search.key = m.currentLayoutKey()
	m.search.valid = true
	if m.search.query == "" {
		return
	}

	query := strings.ToLower(m.search.query)
	for line := 0; line < display.lineCount(); line++ {
		text := strings.ToLower(display.lineText(line))
		for start := 0; start < len(text); {
			idx := strings.Index(text[start:], query)
			if idx < 0 {
				break
			}
			at := start + idx
			m.search.matches = append(m.search.matches, Match{
				Line: line, StartCol: at, EndCol: at + len(query),
			})
			// Overlapping hits are real hits: "aa" in "aaa" is two of them.
			start = at + 1
		}
	}
}

// scrollToMatch brings the current match into view, vim-style: a match already
// comfortably on screen does not move the viewport at all, and one that is not
// lands a margin in from the edge it came from.
//
// With wrap on the match's row is not the line's first row — a hit late in a
// long wrapped line is several rows down — so the row is resolved through the
// layout rather than assumed.
func (m *Model) scrollToMatch() {
	if m.search.cursor >= len(m.search.matches) {
		return
	}
	row := m.matchRow(m.search.matches[m.search.cursor])
	height := m.contentHeight()
	if height <= 0 {
		return
	}
	top, bottom := m.scroll+scrollMargin, m.scroll+height-scrollMargin
	if row >= top && row < bottom {
		return
	}
	if row < top {
		m.scroll = row - scrollMargin
	} else {
		m.scroll = row - height + scrollMargin + 1
	}
	m.clampScroll()
}

// matchRow is the visual row a match starts on.
func (m *Model) matchRow(match Match) int {
	display := m.display()
	first, last := display.lineRows(match.Line)
	offset := 0
	for row := first; row < last; row++ {
		width := len(display.rowText(row))
		if match.StartCol < offset+width || row == last-1 {
			return row
		}
		offset += width
	}
	return first
}

// decorateRow paints one row on its way to the screen.
//
// Precedence is settled here rather than per host, because both surfaces draw
// the same row: **selection wins over search**, for the whole row it covers. A
// selection is a thing the user is holding right now and is about to copy, and
// striping a match's colours through it would make it unclear what is actually
// selected. Within search, the current match wins over the others. A search
// with no live selection paints its matches normally, which is the ordinary
// case.
func (m *Model) decorateRow(row string, visualRow int) string {
	if decorated := m.selection.DecorateRow(row, visualRow); decorated != row {
		return decorated
	}
	return m.highlightRow(row, visualRow)
}

// highlightRow injects the match styling for whatever part of this row is
// matched, including the tail or head of a match that straddles a wrap
// boundary. Styling already on the row (highlighted source, rendered markdown)
// is preserved: the highlight is injected between the escape sequences rather
// than replacing them.
func (m *Model) highlightRow(row string, visualRow int) string {
	if !m.searchActive() || m.search.query == "" {
		return row
	}
	m.ensureMatches()
	display := m.display()
	line, offset := display.rowLine(visualRow)
	if line < 0 {
		return row
	}
	width := len(display.rowText(visualRow))

	var ranges []matchRange
	for i, match := range m.search.matches {
		if match.Line != line {
			continue
		}
		start, end := match.StartCol-offset, match.EndCol-offset
		if end <= 0 || start >= width {
			continue
		}
		ranges = append(ranges, matchRange{
			index: i,
			start: max(start, 0),
			end:   min(end, width),
		})
	}
	if len(ranges) == 0 {
		return row
	}
	return injectHighlights(row, ranges, m.search.cursor)
}

// searchBar is the one row docview owns when search is live: `/ query█ (3/17)
// [n/N]`. Rendering it here rather than in each host is what makes overview
// inherit the whole feature without a line of surface code.
func (m *Model) searchBar() string {
	m.ensureMatches()
	cursor := "█"
	info := ""
	if m.search.phase == searchCommitted {
		cursor = ""
	}
	switch {
	case len(m.search.matches) > 0:
		info = fmt.Sprintf(" (%d/%d)", m.search.cursor+1, len(m.search.matches))
		if m.search.phase == searchCommitted {
			info += " [n/N]"
		}
	case m.search.query != "":
		info = " (0 matches)"
	}
	// MarginBottom is dropped: the bar is exactly one row, and the margin the
	// modal title style carries would make View return a row more than the
	// height it was given.
	style := styles.ModalTitle.MarginBottom(0)
	return style.Render(fmt.Sprintf(" / %s%s%s", m.search.query, cursor, info))
}

// lineCount is how many content lines the layout holds.
func (d displayRows) lineCount() int { return max(len(d.lineStarts)-1, 0) }

// lineRows is the half-open range of visual rows content line i occupies.
func (d displayRows) lineRows(line int) (int, int) {
	if line < 0 || line+1 >= len(d.lineStarts) {
		return 0, 0
	}
	return d.lineStarts[line], d.lineStarts[line+1]
}

// rowText is one visual row's plain text: what is drawn, without the styling it
// is drawn with. Byte offsets into it are what a Match holds.
func (d displayRows) rowText(row int) string {
	if row < 0 || row >= len(d.rows) {
		return ""
	}
	return ansi.Strip(d.rows[row])
}

// lineText is a content line as the user sees it: its visual rows' plain text,
// concatenated. With wrap off that is the whole tab-expanded line; with wrap on
// it is the line minus whatever the wrapper consumed at the break points, which
// is exactly right — a query cannot match text that was not drawn.
func (d displayRows) lineText(line int) string {
	first, last := d.lineRows(line)
	if last-first == 1 {
		return d.rowText(first)
	}
	var b strings.Builder
	for row := first; row < last; row++ {
		b.WriteString(d.rowText(row))
	}
	return b.String()
}

// rowLine reports which content line a visual row belongs to and how many bytes
// of that line's text precede the row, or -1 for a row outside the document.
func (d displayRows) rowLine(row int) (int, int) {
	if row < 0 || row >= len(d.rows) {
		return -1, 0
	}
	// The starts are ascending, so a linear scan would be O(lines) per row on a
	// large file drawn every frame. Binary search the line, then walk only that
	// line's own rows.
	lo, hi := 0, d.lineCount()-1
	line := -1
	for lo <= hi && line < 0 {
		mid := (lo + hi) / 2
		start, end := d.lineRows(mid)
		switch {
		case row < start:
			hi = mid - 1
		case row >= end:
			lo = mid + 1
		default:
			line = mid
		}
	}
	if line < 0 {
		return -1, 0
	}
	first, _ := d.lineRows(line)
	offset := 0
	for r := first; r < row; r++ {
		offset += len(d.rowText(r))
	}
	return line, offset
}

// matchRange is a highlight to inject, in plain-text byte offsets into one row.
type matchRange struct {
	index int // position in the match list, so the current match can be told apart
	start int
	end   int
}

// Highlight style prefixes must be read through functions, never captured in a
// var: styles.SearchMatch and styles.SearchMatchCurrent are package-level
// variables that ApplyTheme reassigns, so a captured copy would freeze the
// highlight on the default theme's colours. See internal/themecheck.
func searchMatchPrefix() string { return ansiPrefix(styles.SearchMatch.Render) }

func searchMatchCurrentPrefix() string { return ansiPrefix(styles.SearchMatchCurrent.Render) }

// injectHighlights walks an ANSI-styled row and opens/closes highlight
// sequences at the given plain-text byte offsets, leaving the row's own styling
// in place.
func injectHighlights(row string, ranges []matchRange, current int) string {
	if len(ranges) == 0 {
		return row
	}
	var out strings.Builder
	out.Grow(len(row) + len(ranges)*24)

	visible, next := 0, 0
	open := false
	// active is the row's own styling that is in force at this point: closing a
	// highlight resets everything, so whatever the row had opened has to be
	// re-emitted or the rest of a syntax-highlighted line loses its colour.
	active := ""
	for i := 0; i < len(row); {
		// ANSI escape sequences pass through and count as no visible bytes.
		if row[i] == '\x1b' && i+1 < len(row) && row[i+1] == '[' {
			j := i + 2
			for j < len(row) && !isANSITerminator(row[j]) {
				j++
			}
			if j < len(row) {
				j++
			}
			seq := row[i:j]
			if seq == "\x1b[0m" || seq == "\x1b[m" {
				active = ""
			} else {
				active += seq
			}
			out.WriteString(seq)
			i = j
			continue
		}
		if open && next < len(ranges) && visible >= ranges[next].end {
			out.WriteString("\x1b[0m")
			out.WriteString(active)
			open = false
			next++
		}
		// Skip any range this column is already past — an overlapped match whose
		// whole span fell inside the one just closed.
		for !open && next < len(ranges) && visible >= ranges[next].end {
			next++
		}
		// A match may start inside the one before it ("aa" twice in "aaa"), so
		// the test is >=, not ==: the later match still gets painted from here.
		if !open && next < len(ranges) && visible >= ranges[next].start {
			open = true
			if ranges[next].index == current {
				out.WriteString(searchMatchCurrentPrefix())
			} else {
				out.WriteString(searchMatchPrefix())
			}
		}
		out.WriteByte(row[i])
		visible++
		i++
	}
	if open {
		out.WriteString("\x1b[0m")
	}
	return out.String()
}

func isANSITerminator(b byte) bool { return b >= 0x40 && b <= 0x7E }

// ansiPrefix renders a marker through a style and returns just the escape
// sequences that precede it.
func ansiPrefix(render func(...string) string) string {
	const marker = "\x00"
	prefix, _, found := strings.Cut(render(marker), marker)
	if !found {
		return ""
	}
	return prefix
}

// SearchCommands is the footer vocabulary of a live in-file search, in one
// place so the project workspace and the global Workspaces browser cannot
// advertise different keys for the same bar. The caller supplies its own focus
// context; the IDs are what internal/keymap registers the bindings under, and they are
// the Files plugin's own so one feature has one vocabulary everywhere.
func SearchCommands(context string) []plugin.Command {
	return []plugin.Command{
		{ID: "confirm", Name: "Go", Description: "Jump to match", Context: context, Priority: 1},
		{ID: "next-match", Name: "Next", Description: "Next match", Context: context, Priority: 2},
		{ID: "prev-match", Name: "Prev", Description: "Previous match", Context: context, Priority: 3},
		{ID: "cancel", Name: "Cancel", Description: "Cancel search", Context: context, Priority: 4},
	}
}
