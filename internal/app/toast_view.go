package app

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/overlay"
	"github.com/marcus/sidecar/internal/styles"
)

// Toasts are a bordered floating block in the top-right of the *content
// region*, composited with internal/overlay exactly as the command palette's
// surfaces are. Phase 1 draws one toast, with no stacking and no reveal — which
// is also the spec'd degraded mode (design frame 1h), so nothing here is thrown
// away when the reveal machine lands.

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

	// regionToast is the toast's whole block as a pointer target. Toasts are
	// click-to-dismiss: they take no focus, so the pointer route is the only
	// direct one, with the global `d` as the keyboard fallback.
	regionToast = "toast"
)

// visibleToast is the notification a toast is currently drawn for, if any.
// Newest wins: without stacking there is one slot, and the thing that just
// happened is the thing worth showing.
func (m Model) visibleToast(now time.Time) (notify.Notification, bool) {
	// A re-show wins the slot: the user asked for this one specifically, which
	// is a newer intent than whatever the store last posted.
	if n, ok := m.reshownToast(now); ok {
		return n, true
	}
	toastable := m.ToastableNotifications(now)
	if len(toastable) == 0 {
		return notify.Notification{}, false
	}
	return toastable[0], true
}

// renderToastOverlay composites the current toast onto an already-rendered
// screen. x0/y0 are the content region's top-left in screen cells and
// width/height its size — deliberately *not* m.width/m.height, so that when the
// notification centre reserves a right-hand column the toast follows the
// content's right edge inward instead of hiding underneath the panel.
func (m Model) renderToastOverlay(screen string, x0, y0, width, height int) string {
	m.clearToastRegion()
	if width < toastMinWidth+2*toastMarginX || height <= 0 {
		return screen
	}
	n, ok := m.visibleToast(time.Now())
	if !ok {
		return screen
	}
	block := renderToastBlock(n, min(toastMaxWidth, width-2*toastMarginX), time.Now())
	if block == "" {
		return screen
	}
	blockWidth := lipgloss.Width(block)
	x := x0 + width - toastMarginX - blockWidth
	if x < x0 {
		x = x0
	}
	y := y0 + toastMarginY
	m.registerToastRegion(x, y, blockWidth, lipgloss.Height(block))
	return overlay.Composite(screen, block, x, y)
}

// clearToastRegion retires the pointer target. A frame that draws no toast
// must leave no clickable hole behind it.
func (m Model) clearToastRegion() {
	if m.toastMouse != nil {
		m.toastMouse.HitMap.Clear()
	}
}

func (m Model) registerToastRegion(x, y, width, height int) {
	if m.toastMouse == nil || width <= 0 || height <= 0 {
		return
	}
	m.toastMouse.HitMap.AddRect(regionToast, x, y, width, height, nil)
}

// toastMouseEvent answers a press that landed on the toast. The whole block is
// one target and one action — dismiss — because a toast that takes no focus
// has nothing else it could mean, and a click that misses falls through to the
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
	if region := m.toastMouse.HitMap.Test(mi.X, mi.Y); region == nil || region.ID != regionToast {
		return false
	}
	return m.dismissVisibleToast()
}

// renderToastBlock draws one toast at the given outer width: source-hued
// border and rule line, title, body, the key row, and — unless the
// notification is sticky — the cell-drawn countdown.
func renderToastBlock(n notify.Notification, outerWidth int, now time.Time) string {
	if outerWidth < toastMinWidth {
		return ""
	}
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
	lines = append(lines,
		glyph+" "+lipgloss.NewStyle().Foreground(styles.TextPrimary).Bold(true).
			Render(ansi.Truncate(title, max(0, inner-2), "…")))
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

	lines = append(lines, "")
	lines = append(lines, toastKeyRow(inner))
	if countdown := toastCountdown(n, now); countdown != "" {
		lines = append(lines, countdown)
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

// toastKeyRow is design 1a's footer row, minus the keys a toast cannot honour.
// Snooze is deferred to Phase 6 (tasks own their own snoozing), and `enter
// open` is gone because a toast has no focus context: nothing routes `enter` to
// it, so advertising it was a promise the toast could not keep. What is left is
// what actually works — click the block, or press `d` where the focused context
// has not claimed it. Targets get their keyboard route through the centre in
// Phase 5.
func toastKeyRow(inner int) string {
	row := styles.Muted.Render("click") + styles.Muted.Render(" or ") +
		styles.KeyHint.Render("d") + styles.Muted.Render(" dismiss")
	if lipgloss.Width(row) > inner {
		row = styles.KeyHint.Render("d") + styles.Muted.Render(" dismiss")
	}
	return ansi.Truncate(row, inner, "")
}

// toastCountdown draws the `▰▰▰▱▱ 4s` meter. A sticky notification has no
// countdown — it stays until the user answers it — and neither does one whose
// expiry has already passed (it is about to leave on the next tick).
func toastCountdown(n notify.Notification, now time.Time) string {
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
	// read (plan 1.5 item 4).
	meter := lipgloss.NewStyle().Foreground(styles.TextSubtle).Render(strings.Repeat(toastCellFull, filled)) +
		lipgloss.NewStyle().Foreground(styles.BorderNormal).Render(strings.Repeat(toastCellEmpty, toastCountdownCells-filled))
	return meter + lipgloss.NewStyle().Foreground(styles.TextSubtle).Render(" "+toastRemaining(remaining))
}

// toastRemaining is the countdown's label. Design 1a shows seconds because its
// toasts live seconds; a `--expiry 5m` toast showed `290s`, which is a duration
// nobody reads at a glance. Seconds up to a minute, then minutes, then hours —
// always rounded up, so the label never reads 0.
func toastRemaining(remaining time.Duration) string {
	switch {
	case remaining < time.Minute:
		return fmt.Sprintf("%ds", int((remaining+time.Second-1)/time.Second))
	case remaining < time.Hour:
		return fmt.Sprintf("%dm", int((remaining+time.Minute-1)/time.Minute))
	default:
		return fmt.Sprintf("%dh", int((remaining+time.Hour-1)/time.Hour))
	}
}

// dismissVisibleToast answers `d` while a toast is on screen. It dismisses the
// notification outright rather than only hiding the toast: `d` in the centre
// means the same thing, and one key must not mean two things.
func (m *Model) dismissVisibleToast() bool {
	n, ok := m.visibleToast(time.Now())
	if !ok {
		return false
	}
	m.clearToastReshow()
	m.dismissNotification(n.ID)
	return true
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
