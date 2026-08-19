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
	// ever dragged it. It fits a title, a source section rule, and a meta
	// column without crowding a 120-column terminal's content.
	notificationCentreDefaultWidth = 34
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
// empty state carry item = -1; only item rows are selectable.
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
			rows = append(rows, centreRow{
				text: m.notificationCentreItemLine(n, inner, index, now),
				item: index,
			})
			index++
		}
	}
	return rows
}

// notificationSectionRule draws design 1c's section header: the source glyph
// and label in the source hue, then a rule that fills the row.
func notificationSectionRule(group notify.Group, inner int) string {
	hue := notify.ResolveHue(group.Source.Hue)
	label := group.Source.Glyph + " " + group.Source.Label
	styled := lipgloss.NewStyle().Foreground(hue).Bold(true).Render(label)
	rest := inner - lipgloss.Width(styled) - 1
	if rest < 1 {
		return ansi.Truncate(styled, inner, "")
	}
	return styled + " " + lipgloss.NewStyle().Foreground(hue).Render(strings.Repeat("─", rest))
}

// notificationCentreItemLine is one notification: the unread dot, the title,
// and its age in the meta column on the right.
func (m Model) notificationCentreItemLine(n notify.Notification, inner, index int, now time.Time) string {
	meta := notificationAge(n.CreatedAt, now)
	mark := "  "
	if !n.Read() {
		mark = lipgloss.NewStyle().Foreground(notify.SourceColor(n.Source)).Render("●") + " "
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
	if m.notificationCentreOwnsKeys() && index == m.notificationCentreCursor {
		return lipgloss.NewStyle().Background(styles.SurfaceRaised).Render(ansi.Truncate(row, inner, ""))
	}
	return row
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
	if panelWidth <= 0 || height <= 0 {
		return ""
	}
	inner := max(1, panelWidth-2)
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
	lines := []string{titleRow, lipgloss.NewStyle().Foreground(styles.BorderNormal).Render(strings.Repeat("─", inner))}
	bodyHeight := max(0, height-4)

	rows := m.notificationCentreBody(inner, now)
	scroll := m.notificationCentreScrollFor(rows, bodyHeight)
	for i := scroll; i < len(rows) && i < scroll+bodyHeight; i++ {
		lines = append(lines, rows[i].text)
	}
	for len(lines) < max(0, height-2) {
		lines = append(lines, "")
	}
	if height >= 4 {
		lines = append(lines, "", footnote)
	}
	if len(lines) > height {
		lines = lines[:height]
	}

	body := make([]string, 0, len(lines))
	for _, line := range lines {
		body = append(body, " "+padNotificationRow(line, inner)+" ")
	}
	panel := strings.Join(body, "\n")

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
	cursorRow := -1
	for i, row := range rows {
		if row.item == m.notificationCentreCursor {
			cursorRow = i
			break
		}
	}
	if cursorRow < 0 {
		return scroll
	}
	if cursorRow < scroll {
		return cursorRow
	}
	if cursorRow >= scroll+bodyHeight {
		return min(cursorRow-bodyHeight+1, len(rows)-bodyHeight)
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

	// Body rows start after the title row and its rule, and are inset by the
	// panel's one column of padding.
	for i := scroll; i < len(rows) && i < scroll+bodyHeight; i++ {
		if rows[i].item < 0 {
			continue
		}
		y := headerHeight + 2 + (i - scroll)
		hits.AddRect(fmt.Sprintf("%s%d", regionNotificationCentreItem, rows[i].item),
			panelX, y, panelWidth, 1, nil)
	}

	if reserve.CloseW > 0 {
		hits.AddRect(regionNotificationCentreClose, panelX+1+reserve.CloseCol, headerHeight, reserve.CloseW, 1, nil)
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
		{ID: "dismiss", Name: "Dismiss", Context: notificationCentreContext, Priority: 2},
		{ID: "dismiss-group", Name: "Group", Context: notificationCentreContext, Priority: 3},
		{ID: "close-notification-centre", Name: "Close", Context: notificationCentreContext, Priority: 4},
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
		// Deliberately a no-op in Phase 1: the targets a notification carries
		// are stored but not yet activated (plan Phase 5). The key is consumed
		// so it cannot mean something else here by accident.
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
		"[", "]", "`", "~", "tab", "shift+tab",
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
