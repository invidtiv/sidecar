package app

import (
	"fmt"
	"strings"
	"time"

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
	// toastCountdownCells is the width of the `▰▰▰▱▱` meter in design 1a.
	toastCountdownCells = 5
	toastCellFull       = "▰"
	toastCellEmpty      = "▱"
)

// visibleToast is the notification a toast is currently drawn for, if any.
// Newest wins: without stacking there is one slot, and the thing that just
// happened is the thing worth showing.
func (m Model) visibleToast(now time.Time) (notify.Notification, bool) {
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
	return overlay.Composite(screen, block, x, y0+toastMarginY)
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

// toastKeyRow is design 1a's footer row. Snooze is deliberately absent: it is
// deferred to Phase 6, and tasks own their own snoozing.
func toastKeyRow(inner int) string {
	row := styles.KeyHint.Render("enter") + styles.Muted.Render(" open") +
		styles.Muted.Render(" · ") +
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
	meter := lipgloss.NewStyle().Foreground(styles.TextSecondary).Render(strings.Repeat(toastCellFull, filled)) +
		lipgloss.NewStyle().Foreground(styles.TextSubtle).Render(strings.Repeat(toastCellEmpty, toastCountdownCells-filled))
	return meter + styles.Muted.Render(" "+toastRemaining(remaining))
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
	m.dismissNotification(n.ID)
	return true
}
