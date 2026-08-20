package app

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/ui"
	"github.com/marcus/sidecar/internal/overlay"
	"github.com/marcus/sidecar/internal/reveal"
	"github.com/marcus/sidecar/internal/styles"
)

// Toasts are a bordered floating block in the top-right of the *content
// region*, composited with internal/overlay exactly as the command palette's
// surfaces are. Since Phase 3 there is a column of them: up to
// notify.DefaultSlots blocks, newest on top, same-source toasts collapsed into
// one block with a `×N` and a peek line (design 1b), each block entering and
// leaving through the row machine in internal/reveal (design 1h). This file
// draws; toast_stack.go decides what is on screen and animates it.

const (
	// toastMaxWidth is the widest a toast gets on a roomy terminal. Narrower
	// content regions shrink it; below toastMinWidth there is no room for a
	// bordered block at all and the toast is skipped for that frame (the
	// notification is still in the centre and still counts in the corner).
	toastMaxWidth = 44
	toastMinWidth = 24
	// toastMargin is the gap between the block and the top/right edges of the
	// content region.
	toastMarginX = 1
	toastMarginY = 0
	// toastCountdownCells is the width of the meter in design 1a. The cells are
	// deliberately the small `▪`/`▫` pair rather than the design's full-height
	// `▰`/`▱`: with a dim hue they read as a receding progress hint instead of
	// competing with the title for attention (plan 1.5 item 4). The tick
	// behaviour behind them is unchanged — one cell per slice of the lifetime,
	// off the same 1s heartbeat.
	toastCountdownCells = 5
	toastCellFull       = "▪"
	toastCellEmpty      = "▫"

	// toastCloseGlyph is the close affordance at the top-right of every block
	// (polish round 2). It replaces the key row: the block says what it can do
	// by showing the control rather than by spending a row on prose. `d` and a
	// click anywhere on the block still dismiss.
	toastCloseGlyph = "×"

	// regionToast prefixes one block's pointer target (regionToastFor adds the
	// source). Toasts are click-to-dismiss: they take no focus, so the pointer
	// route is the only direct one, with the global `d` as the keyboard
	// fallback.
	regionToast = "toast"
	// regionToastClose is the `×` button's own target, registered after (and so
	// on top of) its block's, and carrying the same stack key: pressing it
	// dismisses exactly that block.
	regionToastClose = "toast-close"

	// toastExpandKey opens a collapsed stack. Design 1b says `tab`; Phase 2
	// gave `tab` to the focus cycle whenever the centre is open, so the expand
	// affordance took the `alt+…` family the centre's own guaranteed key
	// (`alt+n`) already lives in.
	toastExpandKey = "alt+e"
	// toastExpandedMembers caps how many hidden titles an expanded block lists,
	// so expanding a chatty source cannot push the column off the screen.
	toastExpandedMembers = 4
)

// visibleToast is the notification the *top* block is drawn for, if any. It is
// the "what would `d` act on" question, not "what is on screen": with stacking
// there are up to notify.DefaultSlots blocks, and the top one is the thing that
// just happened.
func (m Model) visibleToast() (notify.Notification, bool) {
	column := m.toastColumnBlocks()
	if len(column) == 0 {
		return notify.Notification{}, false
	}
	return column[0].stack.Lead(), true
}

// toastBlockWidth is the outer width every block in the column is drawn at, or
// 0 when the content region has no room for a bordered block at all. One width
// for the whole column: blocks of different widths would not read as a stack.
func (m Model) toastBlockWidth() int {
	width := m.contentWidth()
	if width < toastMinWidth+2*toastMarginX {
		return 0
	}
	return min(toastMaxWidth, width-2*toastMarginX)
}

// renderToastOverlay composites the toast column onto an already-rendered
// screen. x0/y0 are the content region's top-left in screen cells and
// width/height its size — deliberately *not* m.width/m.height, so that when the
// notification centre reserves a right-hand column the toasts follow the
// content's right edge inward instead of hiding underneath the panel.
//
// Blocks are painted top-down, newest first (1b), each clipped to the rows its
// reveal has released (1h). A block that does not fit in the remaining height
// is not painted at all — and therefore, by the read gate, never marked read.
func (m Model) renderToastOverlay(screen string, x0, y0, width, height int) string {
	m.clearToastRegion()
	if width < toastMinWidth+2*toastMarginX || height <= 0 || m.overlaysSuppressed() {
		return screen
	}
	now := time.Now()
	blockWidth := min(toastMaxWidth, width-2*toastMarginX)
	y := y0 + toastMarginY
	// The column is the reveal machine's, top to bottom, retracting blocks
	// included — never the store's live stacks. Painting from the store meant a
	// record that arrived between two syncs was drawn whole and then torn down
	// to replay its entry, and a record that expired vanished for a frame and
	// was re-painted only to play its exit.
	for _, r := range m.toastColumnBlocks() {
		block := r.block
		// The cache is only good for the width it was rendered at. A terminal
		// resize, and the centre panel opening, closing or being dragged, all
		// change the content region without going through syncToastReveal —
		// redraw rather than paint a stale-width block.
		if block != "" && lipgloss.Width(block) != blockWidth {
			block = renderToastBlock(r.stack, blockWidth, now, m.toastExpanded)
		}
		if block == "" {
			continue
		}
		block = r.state.Clip(block)
		if block == "" {
			continue
		}
		bw, bh := lipgloss.Width(block), lipgloss.Height(block)
		if y+bh > y0+height {
			break
		}
		x := x0 + width - toastMarginX - bw
		if x < x0 {
			x = x0
		}
		// A block still retracting stays painted until its last row goes, but
		// it is no longer a target: it has already been answered, and a click
		// on a block on its way out must not claim the press.
		if r.state.Phase() != reveal.Leaving {
			m.registerToastRegion(regionToastFor(r.stack.Key), x, y, bw, bh)
			// The `×` sits on the title row, one cell in from the interior's
			// right edge (border + padding = 2 cells of chrome). It is
			// registered after the block, so the later region wins the press
			// and the close button is a real target rather than a picture of
			// one. It is only a target once that row is actually painted.
			if r.state.Rows() > 1 {
				m.registerToastRegion(regionToastClose+":"+string(r.stack.Key), x+bw-2, y+1, 1, 1)
			}
		}
		screen = overlay.Composite(screen, block, x, y)
		y += bh + toastGapY
	}
	return screen
}

// clearToastRegion retires the pointer target. A frame that draws no toast
// must leave no clickable hole behind it.
func (m Model) clearToastRegion() {
	if m.toastMouse != nil {
		m.toastMouse.HitMap.Clear()
	}
}

func (m Model) registerToastRegion(id string, x, y, width, height int) {
	if m.toastMouse == nil || width <= 0 || height <= 0 {
		return
	}
	m.toastMouse.HitMap.AddRect(id, x, y, width, height, nil)
}

// regionToastFor is one block's pointer target. Each block in the column is
// its own target so a click dismisses the block under the pointer rather than
// whatever happens to be on top.
func regionToastFor(key notify.StackKey) string { return regionToast + ":" + string(key) }

func toastKeyForRegion(id string) (notify.StackKey, bool) {
	if rest, ok := strings.CutPrefix(id, regionToastClose+":"); ok {
		return notify.StackKey(rest), true
	}
	rest, ok := strings.CutPrefix(id, regionToast+":")
	if !ok {
		return "", false
	}
	return notify.StackKey(rest), true
}

// toastMouseEvent answers a press that landed on a toast. A block is one
// target and one action — dismiss — because a toast that takes no focus has
// nothing else it could mean, and a click that misses falls through to the
// content untouched.
func (m *Model) toastMouseEvent(msg tea.MouseMsg) bool {
	if m.toastMouse == nil {
		return false
	}
	click, ok := msg.(tea.MouseClickMsg)
	if !ok || click.Mouse().Button != tea.MouseLeft {
		return false
	}
	mi := click.Mouse()
	region := m.toastMouse.HitMap.Test(mi.X, mi.Y)
	if region == nil {
		return false
	}
	key, ok := toastKeyForRegion(region.ID)
	if !ok {
		return false
	}
	return m.dismissToastStack(key)
}

// renderToastBlock draws one stack at the given outer width: source-hued
// border and rule line, the lead's title (with `×N` when the stack collapsed
// several), its body, the peek or expanded list, the key row, and — unless the
// lead is sticky — the cell-drawn countdown.
func renderToastBlock(s notify.Stack, outerWidth int, now time.Time, expanded bool) string {
	if outerWidth < toastMinWidth || s.Count() == 0 {
		return ""
	}
	n := s.Lead()
	source := n.SourceInfo()
	// One helper answers "what does this source look like" for the toast, the
	// status flash, and the centre, so an error looks like an error everywhere.
	hue := notify.ChromeColor(n.Source, n.Severity)
	// Border (2) plus one column of padding either side.
	inner := outerWidth - 4
	if inner < 1 {
		return ""
	}

	var lines []string
	title := strings.TrimSpace(n.Title)
	if title == "" {
		title = source.Label
	}
	glyph := notify.RenderGlyph(n.Source, n.Severity)
	// `×N` (1b) is the collapse made visible: it is the difference between
	// "one thing happened" and "five did, here is the latest".
	count := ""
	if s.Count() > 1 {
		count = lipgloss.NewStyle().Foreground(hue).Render(fmt.Sprintf(" ×%d", s.Count()))
	}
	// The title row carries the controls (polish round 2): the countdown cells
	// and the close button live at its right edge, so the block spends no rows
	// on a key hint or a standalone meter. A sticky notification has no
	// countdown and shows just the `×`.
	right := lipgloss.NewStyle().Foreground(styles.TextSubtle).Render(toastCloseGlyph)
	if cells := toastCountdownMeter(n, now); cells != "" {
		right = cells + " " + right
	}
	rightWidth := lipgloss.Width(right)
	titleWidth := max(0, inner-2-lipgloss.Width(count)-rightWidth-1)
	left := glyph + " " + lipgloss.NewStyle().Foreground(styles.TextPrimary).Bold(true).
		Render(ansi.Truncate(title, titleWidth, "…")) + count
	gap := max(1, inner-lipgloss.Width(left)-rightWidth)
	lines = append(lines, left+strings.Repeat(" ", gap)+right)
	lines = append(lines, lipgloss.NewStyle().Foreground(hue).Render(strings.Repeat("─", inner)))

	if body := strings.TrimSpace(n.Body); body != "" {
		wrapped := ansi.Wrap(body, inner, "")
		for i, line := range strings.Split(wrapped, "\n") {
			if i >= 3 {
				break
			}
			lines = append(lines, lipgloss.NewStyle().Foreground(styles.TextSecondary).Render(line))
		}
	}

	if hidden := s.Hidden(); hidden > 0 {
		if expanded {
			for i, member := range s.Members[1:] {
				if i >= toastExpandedMembers {
					lines = append(lines, styles.Muted.Render(
						fmt.Sprintf("· %d more", hidden-toastExpandedMembers)))
					break
				}
				lines = append(lines, styles.Muted.Render("· ")+
					lipgloss.NewStyle().Foreground(styles.TextSecondary).
						Render(ansi.Truncate(strings.TrimSpace(member.Title), max(1, inner-2), "…")))
			}
		} else {
			lines = append(lines, toastPeekRow(hidden, inner))
		}
	}

	// The block background must survive the styled spans inside each line (the
	// bold title's reset was leaving the title row on the terminal's default
	// background, visibly lighter than the rest of the block). ui.RowBackground
	// re-asserts it after every inner reset — the same fix the centre's
	// selection highlight uses — so lipgloss's Background below only has the
	// border and padding cells left to cover.
	for i, line := range lines {
		lines[i] = ui.RowBackground(line, inner, styles.BgSecondary)
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(hue).
		Background(styles.BgSecondary).
		Padding(0, 1).
		// lipgloss counts the border *and* the padding inside Width, so this is
		// the block's outer width and the text area it leaves is exactly
		// outerWidth-4 == inner. Passing inner+2 here left a 38-cell interior for
		// 40-cell rows, which is what wrapped a `──` stub off the rule line onto
		// the next row of every toast.
		Width(outerWidth).
		Render(strings.Join(lines, "\n"))
}

// The key row design 1a specified ("click or d dismiss") is gone as of polish
// round 2, together with the standalone countdown row: two rows per block spent
// on prose about controls that are discoverable from the block itself. Both
// routes still work — click anywhere on the block, the `×` at its top-right, or
// the global `d` where the focused context has not claimed it.

// toastPeekRow is design 1b's "▾ 2 more · tab expand" line, with the key the
// expand affordance actually has: `tab` belongs to the focus cycle whenever the
// centre is open (Phase 2), and one key cannot mean two things.
func toastPeekRow(hidden, inner int) string {
	row := styles.Muted.Render(fmt.Sprintf("▾ %d more · ", hidden)) +
		styles.KeyHint.Render(toastExpandKey) + styles.Muted.Render(" expand")
	if lipgloss.Width(row) > inner {
		row = styles.Muted.Render(fmt.Sprintf("▾ %d more", hidden))
	}
	return ansi.Truncate(row, inner, "")
}

// toastCountdownMeter draws the `▪▪▪▫▫` cells that now sit in the title row,
// left of the `×`. Cells only: the numeric label went with the row it used to
// share (polish round 2) — the meter answers "is this about to go" without
// asking to be read. A sticky notification has no countdown — it stays until
// the user answers it — and neither does one whose expiry has already passed
// (it is about to leave on the next tick).
func toastCountdownMeter(n notify.Notification, now time.Time) string {
	if n.Sticky || n.ExpiresAt == nil {
		return ""
	}
	total := n.ExpiresAt.Sub(n.CreatedAt)
	remaining := n.ExpiresAt.Sub(now.UTC())
	if total <= 0 || remaining <= 0 {
		return ""
	}
	if remaining > total {
		remaining = total
	}
	// One cell per slice of the lifetime, ticking down a cell at a time off the
	// 1s heartbeat. Anything still running rounds up, so a live toast never
	// shows an empty meter.
	filled := int(float64(toastCountdownCells) * float64(remaining) / float64(total))
	if rem := float64(toastCountdownCells) * float64(remaining) / float64(total); rem > float64(filled) {
		filled++
	}
	filled = max(1, min(toastCountdownCells, filled))
	// Both halves sit below the body text in weight: the countdown is a hint
	// about how long the block will linger, not information the user has to
	// read (plan 1.5 item 4). The filled cells sit on the RIGHT, against the
	// `×`, and the meter empties left-to-right — a fuse burning down toward
	// the close button (Marcus, 2026-08-19).
	return lipgloss.NewStyle().Foreground(styles.BorderNormal).Render(strings.Repeat(toastCellEmpty, toastCountdownCells-filled)) +
		lipgloss.NewStyle().Foreground(styles.TextSubtle).Render(strings.Repeat(toastCellFull, filled))
}

// dismissVisibleToast answers `d` while a toast is on screen: it acts on the
// top block, the one the user just watched arrive. It dismisses the
// notifications outright rather than only hiding the block — `d` in the centre
// means the same thing, and one key must not mean two things.
func (m *Model) dismissVisibleToast() bool {
	if m.overlaysSuppressed() {
		return false
	}
	for _, r := range m.toastColumnBlocks() {
		// A block already retracting has been answered; `d` means the one above
		// it, or nothing.
		if r.state.Phase() == reveal.Leaving {
			continue
		}
		return m.dismissToastStack(r.stack.Key)
	}
	return false
}

// dismissToastStack clears one block. A collapsed block dismisses **all** its
// members, not just the lead: the block is one object with one `×N` on it, and
// making the user dismiss the same block five times to clear five members
// would be a worse bargain than the centre's own `D dismiss group`, which this
// mirrors. Anything dismissed is still in the store's 24h window.
func (m *Model) dismissToastStack(key notify.StackKey) bool {
	now := time.Now()
	for _, r := range m.toastColumnBlocks() {
		s := r.stack
		if s.Key != key || r.state.Phase() == reveal.Leaving {
			continue
		}
		if reshown, ok := m.reshownToast(now); ok && s.Lead().ID == reshown.ID {
			// The re-show is presentation only; dismissing it must not dismiss
			// the record it was copied from unless that record is toasting too.
			m.clearToastReshow()
			return true
		}
		for _, member := range s.Members {
			m.dismissNotification(member.ID)
		}
		return true
	}
	return false
}

// reshownToast is the "view details" slot: the notification the user selected
// `enter` on in the centre, re-presented as a toast for one countdown. It is a
// copy, not a store record — re-showing a dismissed notification shows it
// again without un-dismissing it, and re-showing a read one does not make it
// unread.
func (m Model) reshownToast(now time.Time) (notify.Notification, bool) {
	if m.toastReshow == nil || !now.UTC().Before(m.toastReshowUntil) {
		return notify.Notification{}, false
	}
	return *m.toastReshow, true
}

// reshowNotification puts a notification back on screen as a toast. The copy
// gets a fresh created/expires pair so the countdown starts over; sticky
// notifications re-show sticky, exactly as they toasted the first time.
func (m *Model) reshowNotification(n notify.Notification, now time.Time) {
	copyOf := n
	copyOf.CreatedAt = now.UTC()
	copyOf.DismissedAt = nil
	if n.Sticky {
		copyOf.ExpiresAt = nil
		// A sticky re-show still has to end: the user can dismiss it, but the
		// slot must not be held forever by a presentation-only copy.
		m.toastReshowUntil = now.UTC().Add(notify.ExpiryFor(notify.SourceSystem))
	} else {
		expiry := notify.ExpiryFor(n.Source)
		if expiry <= 0 {
			expiry = notify.ExpiryFor(notify.SourceSystem)
		}
		expires := copyOf.CreatedAt.Add(expiry)
		copyOf.ExpiresAt = &expires
		m.toastReshowUntil = expires
	}
	m.toastReshow = &copyOf
}

func (m *Model) clearToastReshow() {
	m.toastReshow = nil
	m.toastReshowUntil = time.Time{}
}
