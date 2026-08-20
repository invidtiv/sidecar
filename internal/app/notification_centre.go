package app

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/ui"
)

// The notification centre is an app-level right panel: full height, owned by
// the shell, pushing the active surface left rather than floating over it. It
// is deliberately not a plugin and has no navbar tab — the header indicator and
// its shortcut are the only ways in — and it is not a modal: navigation keeps
// working underneath it, and it stays open until it is explicitly closed.
//
// Everything it draws goes through the shared grammar: ui.RenderHandle for the
// resize rail, ui.ReserveHeaderClose/ComposeHeaderClose for the close
// affordance, notify.GroupBySource for the section order, and the host footer
// for its key hints (notificationCentreCommands, never a hand-rendered footer).

const (
	// notificationCentreContext is the keymap/footer context the panel owns
	// while it has focus. It is registered in internal/keymap/bindings.go like
	// every other context, so its keys are reboundable and its footer hints are
	// derived rather than written out.
	notificationCentreContext = "notification-centre"

	// notificationCentreDefaultWidth is the panel's width before the user has
	// ever dragged it. It fits a title, a source section rule, a meta column,
	// and the body line of a two-line entry without crowding a 120-column
	// terminal's content — four columns wider than Phase 1's panel, which is
	// exactly what the gradient border and its padding take back.
	notificationCentreDefaultWidth = 38
	notificationCentreMinWidth     = 24
	notificationCentreMaxWidth     = 60
	// notificationCentreMinContent is the narrowest content region the panel is
	// willing to leave behind. A terminal that cannot spare it keeps its whole
	// width for content and the panel simply is not drawn — reopening happens
	// by itself when the terminal grows, because nothing about the open state
	// was thrown away.
	notificationCentreMinContent = 40
	// notificationCentreHandleWidth is the one column the resize rail owns.
	notificationCentreHandleWidth = 1
	// notificationCentreHandleHit widens the pointer target for the rail, the
	// same three-column allowance every other divider in sidecar uses.
	notificationCentreHandleHit = 3

	regionNotificationCentre       = "notification-centre"
	regionNotificationCentreHandle = "notification-centre-handle"
	regionNotificationCentreClose  = "notification-centre-close"
	regionNotificationCentreItem   = "notification-centre-item-"

	// notificationCentreFootnote describes notify.Retention. It is a sentence
	// about that constant, not a second rule.
	notificationCentreFootnote = "Dismissed items clear after 24h"
)

// notificationCentrePanelWidth is the panel's painted width, or 0 when the
// panel is closed or the terminal has nothing to spare. It resolves the
// persisted preference on first use — never in Init, where the terminal size is
// not known yet — and clamps it to what the current width can hold.
func (m Model) notificationCentrePanelWidth() int {
	if !m.notificationCentreOpen || !m.ready {
		return 0
	}
	width := m.notificationCentreWidth
	if width <= 0 {
		width = state.GetNotificationCentreWidth()
	}
	if width <= 0 {
		width = notificationCentreDefaultWidth
	}
	return clampNotificationCentreWidth(width, m.width)
}

// clampNotificationCentreWidth is the panel's sizing rule, kept state-free so
// the drag handler and the renderer cannot disagree about the bounds.
func clampNotificationCentreWidth(width, terminal int) int {
	spare := terminal - notificationCentreHandleWidth - notificationCentreMinContent
	if spare < notificationCentreMinWidth {
		return 0
	}
	width = min(width, notificationCentreMaxWidth)
	width = min(width, spare)
	return max(width, notificationCentreMinWidth)
}

// notificationCentreVisible reports that the panel is actually being drawn, as
// opposed to merely open on a terminal too narrow to hold it.
func (m Model) notificationCentreVisible() bool {
	return m.notificationCentrePanelWidth() > 0
}

// notificationCentreOwnsKeys reports that the panel is the focused surface. A
// panel that is open but not drawn owns nothing: the keys would act on a list
// the user cannot see.
func (m Model) notificationCentreOwnsKeys() bool {
	return m.notificationCentreFocused && m.notificationCentreVisible()
}

// centreRow is one body line of the panel. Section headers, spacers, and the
// empty state carry item = -1; only item rows are selectable. An entry is two
// rows — title and body — that both carry the same item index, so the cursor
// highlight covers the pair and a click on either selects the same
// notification.
type centreRow struct {
	text string
	item int
}

// notificationCentreItems is the flat, source-grouped list the panel shows:
// the same order the section rules are drawn in, so an index into it is an
// index into what the user sees.
func (m Model) notificationCentreItems() []notify.Notification {
	var out []notify.Notification
	for _, group := range notify.GroupBySource(notify.Active(m.notificationCache)) {
		out = append(out, group.Items...)
	}
	return out
}

// notificationCentreBody builds the scrollable rows at a given inner width.
func (m Model) notificationCentreBody(inner int, now time.Time) []centreRow {
	groups := notify.GroupBySource(notify.Active(m.notificationCache))
	if len(groups) == 0 {
		return []centreRow{{text: styles.Muted.Render(ansi.Truncate("Nothing to catch up on.", inner, "…")), item: -1}}
	}
	var rows []centreRow
	index := 0
	for i, group := range groups {
		if i > 0 {
			rows = append(rows, centreRow{text: "", item: -1})
		}
		rows = append(rows, centreRow{text: notificationSectionRule(group, inner), item: -1})
		for _, n := range group.Items {
			for _, line := range m.notificationCentreItemLines(n, inner, index, now) {
				rows = append(rows, centreRow{text: line, item: index})
			}
			index++
		}
	}
	return rows
}

// notificationSectionRule draws design 1c's section header: the source glyph
// and label in the source hue, then a rule that fills the row.
func notificationSectionRule(group notify.Group, inner int) string {
	hue := notify.ChromeColor(group.Source.ID, notify.SeverityInfo)
	label := notify.Glyph(group.Source.ID) + " " + group.Source.Label
	styled := lipgloss.NewStyle().Foreground(hue).Bold(true).Render(label)
	rest := inner - lipgloss.Width(styled) - 1
	if rest < 1 {
		return ansi.Truncate(styled, inner, "")
	}
	return styled + " " + lipgloss.NewStyle().Foreground(hue).Render(strings.Repeat("─", rest))
}

// notificationCentreItemLines is one notification as the two rows plan 1.5
// asks for: the unread dot, the title and its age in the meta column, then the
// body indented underneath. The body row is what keeps a call to action from
// being lost to a truncated title — a notification whose whole content is its
// title still renders as a single row, so a list of short items stays dense.
func (m Model) notificationCentreItemLines(n notify.Notification, inner, index int, now time.Time) []string {
	lines := []string{m.notificationCentreTitleLine(n, inner, index, now)}
	body := strings.Join(strings.Fields(strings.TrimSpace(n.Body)), " ")
	if body == "" {
		return lines
	}
	// The body aligns under the title, past the two columns the unread dot
	// occupies, so the two rows read as one entry.
	const indent = "  "
	bodyWidth := max(1, inner-len(indent))
	row := indent + lipgloss.NewStyle().Foreground(styles.TextSubtle).
		Render(ansi.Truncate(body, bodyWidth, "…"))
	return append(lines, m.styleCentreRow(row, inner, index))
}

// styleCentreRow applies the cursor highlight to a row of the selected entry.
func (m Model) styleCentreRow(row string, inner, index int) string {
	if m.notificationCentreOwnsKeys() && index == m.notificationCentreCursor {
		return lipgloss.NewStyle().Background(styles.SurfaceRaised).
			Render(padNotificationRow(row, inner))
	}
	return row
}

// notificationCentreTitleLine is an entry's first row.
func (m Model) notificationCentreTitleLine(n notify.Notification, inner, index int, now time.Time) string {
	meta := notificationAge(n.CreatedAt, now)
	mark := "  "
	if !n.Read() {
		mark = lipgloss.NewStyle().Foreground(notify.ChromeColor(n.Source, n.Severity)).Render("●") + " "
	}
	title := strings.TrimSpace(n.Title)
	if title == "" {
		title = n.SourceInfo().Label
	}
	titleWidth := max(1, inner-lipgloss.Width(mark)-lipgloss.Width(meta)-1)
	titleStyle := lipgloss.NewStyle().Foreground(styles.TextSecondary)
	if !n.Read() {
		titleStyle = lipgloss.NewStyle().Foreground(styles.TextPrimary)
	}
	body := titleStyle.Render(ansi.Truncate(title, titleWidth, "…"))
	gap := inner - lipgloss.Width(mark) - lipgloss.Width(body) - lipgloss.Width(meta)
	if gap < 1 {
		gap = 1
	}
	row := mark + body + strings.Repeat(" ", gap) + styles.Muted.Render(meta)
	return m.styleCentreRow(row, inner, index)
}

// notificationAge is the compact meta column of design 1c: "now", "4m", "3h",
// "2d". Nothing in internal/ui answers this today and the two plugin-local
// "x ago" helpers are prose, not a column, so this stays local until a shared
// one exists to adopt.
func notificationAge(created, now time.Time) string {
	d := now.UTC().Sub(created.UTC())
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// renderNotificationCentre paints the reserved right column — the resize rail
// and the panel — for a content region of the given height. It also registers
// the panel's hit regions, in screen coordinates, so the shell can route a
// press without a second geometry.
func (m Model) renderNotificationCentre(height int) string {
	panelWidth := m.notificationCentrePanelWidth()
	// Three rows is the least a bordered pane can be drawn in; below that the
	// panel yields rather than degrading into a borderless one.
	if panelWidth <= 0 || height < 3 {
		return ""
	}
	// The panel wears the same gradient border every other content pane wears
	// (styles.RenderPanel), so it reads as a pane of the shell rather than a
	// second kind of surface. That border costs two columns and two rows, and
	// its one column of padding either side costs two more columns — the
	// interior is what is left.
	inner := max(1, panelWidth-4)
	now := time.Now()

	handle := ui.RenderHandle(height, true,
		ui.HandleStateFrom(m.notificationCentreHoverHandle, m.notificationCentreDragging()))

	// Title row: the panel names itself and carries the shared close control.
	title := lipgloss.NewStyle().Foreground(styles.TextPrimary).Bold(true).
		Render(ansi.Truncate("Notifications", inner, "…"))
	reserve := ui.ReserveHeaderClose(inner)
	titleRow := ui.ComposeHeaderClose(padNotificationRow(title, reserve.TabsWidth), inner, m.notificationCentreHoverClose)

	// Two header rows (title, rule), a spacer, and the footnote sit outside the
	// scrolled body.
	footnote := styles.Muted.Render(ansi.Truncate(notificationCentreFootnote, inner, "…"))
	// The border takes the outermost row top and bottom; everything below is
	// laid out against the interior.
	interiorHeight := max(0, height-2)
	lines := []string{titleRow, lipgloss.NewStyle().Foreground(styles.BorderNormal).Render(strings.Repeat("─", inner))}
	// Title, rule, and — where there is room — a spacer and the footnote sit
	// outside the scrolled body.
	bodyHeight := max(0, interiorHeight-4)

	rows := m.notificationCentreBody(inner, now)
	scroll := m.notificationCentreScrollFor(rows, bodyHeight)
	for i := scroll; i < len(rows) && i < scroll+bodyHeight; i++ {
		lines = append(lines, rows[i].text)
	}
	for len(lines) < max(0, interiorHeight-2) {
		lines = append(lines, "")
	}
	if interiorHeight >= 4 {
		lines = append(lines, "", footnote)
	}
	if len(lines) > interiorHeight {
		lines = lines[:interiorHeight]
	}

	body := make([]string, 0, len(lines))
	for _, line := range lines {
		body = append(body, padNotificationRow(line, inner))
	}
	panel := styles.RenderPanel(strings.Join(body, "\n"), panelWidth, height,
		m.notificationCentreFocused)

	m.registerNotificationCentreRegions(height, rows, scroll, bodyHeight, reserve)
	return lipgloss.JoinHorizontal(lipgloss.Top, handle, panel)
}

// notificationCentreScrollFor keeps the cursor's row on screen without ever
// scrolling past the end of the list.
func (m Model) notificationCentreScrollFor(rows []centreRow, bodyHeight int) int {
	if bodyHeight <= 0 || len(rows) <= bodyHeight {
		return 0
	}
	scroll := min(m.notificationCentreScroll, len(rows)-bodyHeight)
	scroll = max(0, scroll)
	// An entry is two rows, so the cursor occupies a span: scroll to bring the
	// first row into view going up, and the last one going down, so an entry's
	// body is never the half that falls off the edge.
	cursorRow, cursorEnd := -1, -1
	for i, row := range rows {
		if row.item != m.notificationCentreCursor {
			continue
		}
		if cursorRow < 0 {
			cursorRow = i
		}
		cursorEnd = i
	}
	if cursorRow < 0 {
		return scroll
	}
	if cursorRow < scroll {
		return cursorRow
	}
	if cursorEnd >= scroll+bodyHeight {
		return min(cursorEnd-bodyHeight+1, len(rows)-bodyHeight)
	}
	return scroll
}

// registerNotificationCentreRegions publishes the panel's pointer targets in
// screen coordinates. Order is the shared rule: the widest target first, the
// resize rail last so a press one cell off the edge resizes rather than
// selecting.
func (m Model) registerNotificationCentreRegions(height int, rows []centreRow, scroll, bodyHeight int, reserve ui.HeaderClose) {
	if m.notificationCentreMouse == nil {
		return
	}
	hits := m.notificationCentreMouse.HitMap
	hits.Clear()
	panelWidth := m.notificationCentrePanelWidth()
	panelX := m.width - panelWidth
	handleX := panelX - notificationCentreHandleWidth

	hits.AddRect(regionNotificationCentre, panelX, headerHeight, panelWidth, height, nil)

	// The interior starts one row down and two columns in: the gradient border
	// takes a row and a column, the panel's padding another column. Body rows
	// then start after the title row and its rule. Both rows of a two-line
	// entry carry the same item id, so clicking either selects the entry.
	for i := scroll; i < len(rows) && i < scroll+bodyHeight; i++ {
		if rows[i].item < 0 {
			continue
		}
		y := headerHeight + 1 + 2 + (i - scroll)
		hits.AddRect(fmt.Sprintf("%s%d", regionNotificationCentreItem, rows[i].item),
			panelX, y, panelWidth, 1, nil)
	}

	if reserve.CloseW > 0 {
		hits.AddRect(regionNotificationCentreClose, panelX+2+reserve.CloseCol, headerHeight+1, reserve.CloseW, 1, nil)
	}

	hitX := max(0, handleX-(notificationCentreHandleHit-1)/2)
	hits.AddRect(regionNotificationCentreHandle, hitX, headerHeight, notificationCentreHandleHit, height, nil)
}

func (m Model) notificationCentreDragging() bool {
	return m.notificationCentreMouse != nil &&
		m.notificationCentreMouse.DragRegion() == regionNotificationCentreHandle
}

// padNotificationRow pads or truncates a styled row to exactly width columns.
func padNotificationRow(row string, width int) string {
	if width <= 0 {
		return ""
	}
	row = ansi.Truncate(row, width, "")
	if gap := width - lipgloss.Width(row); gap > 0 {
		row += strings.Repeat(" ", gap)
	}
	return row
}

// notificationCentreCommands are the panel's footer/palette commands. The
// footer derives its hints from these plus the registered bindings, exactly as
// a plugin's footer does — the panel never renders a footer of its own.
func (m Model) notificationCentreCommands() []plugin.Command {
	return []plugin.Command{
		{ID: "cursor-down", Name: "Move", Context: notificationCentreContext, Priority: 1},
		{ID: "select", Name: "Details", Context: notificationCentreContext, Priority: 2},
		{ID: "dismiss", Name: "Dismiss", Context: notificationCentreContext, Priority: 3},
		{ID: "dismiss-group", Name: "Group", Context: notificationCentreContext, Priority: 4},
		{ID: "close-notification-centre", Name: "Close", Context: notificationCentreContext, Priority: 5},
		{ID: "focus-content", Name: "Content", Description: "Move focus on to the content", Context: notificationCentreContext, Priority: 6},
	}
}

// notificationCentreKey answers the keys the panel owns while it has focus. It
// claims only its list keys: everything else — tab switches, the project
// selector, quit — keeps working underneath, which is what makes the panel a
// panel rather than a modal.
func (m *Model) notificationCentreKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	items := m.notificationCentreItems()
	m.clampNotificationCentreCursor(len(items))
	key := msg.String()
	if key == "tab" || key == "shift+tab" {
		// The centre is a stop on the shell's focus cycle, so tab out of it is
		// the cycle moving on — not a key the surface underneath should also
		// act on. Consuming it here is what makes the stop symmetric with tab
		// in: one press per stop. See notificationCentreTabKey for the way in.
		return true, m.leaveNotificationCentreFocus(key == "shift+tab")
	}
	if notificationCentreReleasesFocus(key) {
		// A navigation key means the user is going somewhere else. Hand the
		// keyboard back to the content and let the key run its ordinary course —
		// without closing the panel, which stays open until it is closed. Mouse
		// was the only way back before this, which trapped a keyboard-only user
		// on a panel whose j/k/d kept driving a list they had navigated away
		// from. `N` brings focus back (see the global key handler).
		m.blurNotificationCentre()
		return false, nil
	}
	switch key {
	case "esc":
		return true, m.closeNotificationCentre()
	case "j", "down":
		if m.notificationCentreCursor < len(items)-1 {
			m.notificationCentreCursor++
		}
		m.readSelectedNotification()
		return true, nil
	case "k", "up":
		if m.notificationCentreCursor > 0 {
			m.notificationCentreCursor--
		}
		m.readSelectedNotification()
		return true, nil
	case "d":
		if selected, ok := m.selectedNotification(items); ok {
			m.dismissNotification(selected.ID)
			m.clampNotificationCentreCursor(len(m.notificationCentreItems()))
		}
		return true, nil
	case "D":
		if selected, ok := m.selectedNotification(items); ok {
			source := notify.SourceOf(selected.Source).ID
			for _, n := range items {
				if notify.SourceOf(n.Source).ID == source {
					m.dismissNotification(n.ID)
				}
			}
			m.notificationCentreCursor = 0
			m.clampNotificationCentreCursor(len(m.notificationCentreItems()))
		}
		return true, nil
	case "enter":
		// "View details": re-present the selected notification as a toast, so
		// the body and key row the centre's two lines truncate can be read in
		// full. It is presentation only — no re-post, no un-dismiss — and the
		// centre stays open and focused behind it. Phase 5 rebinds `enter` to
		// target activation and re-show moves to a secondary key.
		if selected, ok := m.selectedNotification(items); ok {
			m.reshowNotification(selected, time.Now())
			m.readSelectedNotification()
		}
		return true, nil
	}
	return false, nil
}

// notificationCentreReleasesFocus names the keys that move the user somewhere
// else in the shell. The panel is not a modal, so these keep working while it
// has focus — but working means the surface they select gets the keyboard, not
// just the screen.
func notificationCentreReleasesFocus(key string) bool {
	switch key {
	case "1", "2", "3", "4", "5", "6", "7", "8", "9", "0",
		// tab and shift+tab are not here: the centre is a stop on the focus
		// cycle and answers them itself (notificationCentreKey), rather than
		// releasing them to run twice.
		"[", "]", "`", "~",
		"K", "@", "W", "^", "?", ",":
		return true
	}
	return false
}

// readSelectedNotification marks the item under the cursor read. Selecting a
// notification in the centre is the user seeing it — which is what stops the
// header counter climbing forever, and what stops an unexpired notification
// toasting again after a restart.
func (m *Model) readSelectedNotification() {
	// Only what is actually on screen counts as read: on a terminal too narrow
	// to honour the panel it stays open but paints nothing, and reading a row
	// nobody can see is the same bug as reading a toast that never rendered.
	if !m.notificationCentreVisible() {
		return
	}
	items := m.notificationCentreItems()
	selected, ok := m.selectedNotification(items)
	if !ok || selected.Read() {
		return
	}
	m.readNotification(selected.ID)
}

func (m *Model) clampNotificationCentreCursor(count int) {
	if count <= 0 {
		m.notificationCentreCursor = 0
		return
	}
	m.notificationCentreCursor = max(0, min(m.notificationCentreCursor, count-1))
}

func (m *Model) selectedNotification(items []notify.Notification) (notify.Notification, bool) {
	if m.notificationCentreCursor < 0 || m.notificationCentreCursor >= len(items) {
		return notify.Notification{}, false
	}
	return items[m.notificationCentreCursor], true
}

// notificationCentreMouseEvent routes a pointer event that belongs to the
// reserved column: the close affordance, a list row, or the resize rail. It
// reports false for anything outside, which the shell then routes to the
// content as usual — and that path is what returns focus to the content
// *without* closing the panel.
func (m *Model) notificationCentreMouseEvent(msg tea.MouseMsg) (bool, tea.Cmd) {
	if !m.notificationCentreVisible() || m.notificationCentreMouse == nil {
		return false, nil
	}
	mi := msg.Mouse()

	if _, isMotion := msg.(tea.MouseMotionMsg); isMotion && !m.notificationCentreMouse.IsDragging() {
		region := m.notificationCentreMouse.HitMap.Test(mi.X, mi.Y)
		m.notificationCentreHoverHandle = region != nil && region.ID == regionNotificationCentreHandle
		m.notificationCentreHoverClose = region != nil && region.ID == regionNotificationCentreClose
		return m.notificationCentreHoverHandle || m.notificationCentreHoverClose, nil
	}

	action := m.notificationCentreMouse.HandleMouse(msg)
	switch action.Type {
	case mouse.ActionDrag:
		if action.DragStartID != regionNotificationCentreHandle {
			return false, nil
		}
		// The rail is on the panel's left edge, so dragging left widens it.
		width := clampNotificationCentreWidth(
			m.notificationCentreMouse.DragStartValue()-action.DragDX, m.width)
		if width > 0 && width != m.notificationCentreWidth {
			m.notificationCentreWidth = width
			return true, tea.Batch(m.emitContentSize()...)
		}
		return true, nil
	case mouse.ActionDragEnd:
		if action.DragStartID != regionNotificationCentreHandle {
			return false, nil
		}
		// The handler has already ended the drag; all that is left is to make
		// the width the user chose survive a restart.
		_ = state.SetNotificationCentreWidth(m.notificationCentreWidth)
		return true, nil
	case mouse.ActionClick, mouse.ActionDoubleClick, mouse.ActionTripleClick:
		if action.Region == nil {
			return false, nil
		}
		switch {
		case action.Region.ID == regionNotificationCentreHandle:
			m.notificationCentreMouse.StartDrag(action.X, action.Y,
				regionNotificationCentreHandle, m.notificationCentrePanelWidth())
			return true, nil
		case action.Region.ID == regionNotificationCentreClose:
			return true, m.closeNotificationCentre()
		case strings.HasPrefix(action.Region.ID, regionNotificationCentreItem):
			var index int
			if _, err := fmt.Sscanf(action.Region.ID, regionNotificationCentreItem+"%d", &index); err == nil {
				m.notificationCentreCursor = index
			}
			m.focusNotificationCentre()
			m.readSelectedNotification()
			return true, nil
		case action.Region.ID == regionNotificationCentre:
			m.focusNotificationCentre()
			return true, nil
		}
	}
	return false, nil
}

// focusNotificationCentre gives the panel the keyboard without changing
// whether it is open.
func (m *Model) focusNotificationCentre() {
	m.notificationCentreFocused = true
	m.updateContext()
}

// blurNotificationCentre hands the keyboard back to the content. It never
// closes the panel: clicking into a plugin returns focus and leaves the centre
// exactly where it was.
func (m *Model) blurNotificationCentre() {
	if !m.notificationCentreFocused {
		return
	}
	m.notificationCentreFocused = false
	m.updateContext()
}

// The centre as a focus-cycle stop
//
// With the panel open, `tab` treats it as one more window of the shell: the
// surface underneath cycles its own panes as it always has, and when the next
// press would wrap that surface's ring, the centre takes the keyboard instead.
// A further `tab` hands it back to the window the ring resumes at. There is no
// second cycle and no second key-routing scheme — the surface answers "am I at
// my ring's end?" through plugin.FocusCycler, and shift+tab runs the same rule
// in reverse. When the panel is closed (or open on a terminal too narrow to
// draw it) nothing here fires and tab is exactly what it was.
//
// A surface that does not implement FocusCycler keeps `tab` whenever its
// context binds the key; the centre only joins where the key is otherwise
// unclaimed. `alt+n`, `N`, and clicking remain the direct routes in from
// anywhere.

// notificationCentreFocusCycler is the surface whose Tab ring the centre is
// currently extending, or nil when the focused surface owns Tab outright.
func (m *Model) notificationCentreFocusCycler() plugin.FocusCycler {
	if m.configOpen() {
		return nil
	}
	if m.inGlobalScope() {
		if m.globalWorkspacesVisible() {
			if cycler, ok := any(m.overview).(plugin.FocusCycler); ok {
				return cycler
			}
		}
		return nil
	}
	if p := m.ActivePlugin(); p != nil {
		if cycler, ok := p.(plugin.FocusCycler); ok {
			return cycler
		}
	}
	return nil
}

// notificationCentreTabKey answers `tab` on the way *into* the panel. It
// reports whether the key was consumed, and must run before the focused
// surface sees the key.
func (m *Model) notificationCentreTabKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	key := msg.String()
	if key != "tab" && key != "shift+tab" {
		return false, nil
	}
	if m.hasModal() || !m.notificationCentreVisible() || m.notificationCentreFocused {
		return false, nil
	}
	// Anything typing, or any surface holding the whole keyboard, keeps tab:
	// a completion or a field cycle is not a focus cycle.
	if m.consumesTextInput() || m.pluginBlocksGlobalKeys() || isTextInputContext(m.activeContext) {
		return false, nil
	}
	switch m.activeContext {
	case "workspace-interactive", "file-browser-inline-edit", "notes-inline-edit":
		return false, nil
	}
	// A context that has bound tab to something that is not a pane cycle —
	// accepting a completion, moving to the next field, toggling a search
	// option — owns the key outright, ring or no ring.
	if cmd, bound := m.keymap.CommandForContextKey(m.activeContext, key); bound &&
		cmd != "next-pane" && cmd != "switch-pane" {
		return false, nil
	}
	reverse := key == "shift+tab"
	if cycler := m.notificationCentreFocusCycler(); cycler != nil {
		if !cycler.AtFocusCycleEnd(reverse) {
			return false, nil
		}
		m.focusNotificationCentre()
		m.readSelectedNotification()
		return true, nil
	}
	// No ring to extend: the centre takes tab only where the focused context
	// has not bound it to something of its own.
	if m.contextRebindsKey(key) || m.pluginClaimsKey(key) {
		return false, nil
	}
	m.focusNotificationCentre()
	m.readSelectedNotification()
	return true, nil
}

// leaveNotificationCentreFocus is the other half: tab out of the panel returns
// the keyboard to the window the surface's ring resumes at, leaving the panel
// open exactly where it was.
func (m *Model) leaveNotificationCentreFocus(reverse bool) tea.Cmd {
	m.blurNotificationCentre()
	if cycler := m.notificationCentreFocusCycler(); cycler != nil {
		return cycler.FocusCycleStart(reverse)
	}
	return nil
}
