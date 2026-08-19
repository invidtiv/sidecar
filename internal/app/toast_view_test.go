package app

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/notify"
)

func blankScreen(w, h int) string {
	return strings.TrimRight(strings.Repeat(strings.Repeat(" ", w)+"\n", h), "\n")
}

// A toast is a bordered block in the top-right of the content region, carrying
// the title, the body, the key row, and a countdown.
func TestToastIsDrawnTopRightOfTheContentRegion(t *testing.T) {
	m := notifyModel()
	m.width, m.height, m.ready = 100, 30, true
	m.postNotification(notify.Notification{
		Source: notify.SourceAgent,
		Title:  "Agent finished",
		Body:   "sidecar: tests green",
	})

	screen := ansi.Strip(m.renderToastOverlay(blankScreen(100, 30), 0, headerHeight, 100, 28))
	lines := strings.Split(screen, "\n")
	if len(lines) != 30 {
		t.Fatalf("the overlay changed the line count: %d", len(lines))
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got != 100 {
			t.Fatalf("line %d is %d columns wide, want 100", i, got)
		}
	}
	if strings.Contains(lines[0], "Agent finished") {
		t.Fatal("the toast painted over the header row")
	}
	for _, want := range []string{"Agent finished", "sidecar: tests green", "dismiss", "▰"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("the toast is missing %q:\n%s", want, screen)
		}
	}
	// Top-right: the block's right edge is one column short of the region's.
	row := lines[headerHeight]
	if strings.TrimRight(row, " ") == "" {
		t.Fatalf("nothing drawn on the toast's first row:\n%s", screen)
	}
	if end := ansi.StringWidth(strings.TrimRight(row, " ")); end != 99 {
		t.Fatalf("toast right edge at column %d, want 99:\n%s", end, screen)
	}
}

// A sticky notification waits for the user, so it shows no countdown.
func TestStickyToastHasNoCountdown(t *testing.T) {
	n := notify.Normalize(notify.Notification{
		Source: notify.SourceWaiting,
		Title:  "Agent is waiting",
	}, time.Now())
	if !n.Sticky {
		t.Fatalf("the waiting source should be sticky by default")
	}
	block := ansi.Strip(renderToastBlock(n, 40, time.Now()))
	if strings.Contains(block, "▰") || strings.Contains(block, "▱") {
		t.Fatalf("a sticky toast drew a countdown:\n%s", block)
	}
}

// The countdown loses a cell as the expiry approaches, off the 1s heartbeat.
func TestCountdownTicksDown(t *testing.T) {
	created := time.Now().UTC()
	expires := created.Add(5 * time.Second)
	n := notify.Notification{CreatedAt: created, ExpiresAt: &expires}
	full := ansi.Strip(toastCountdown(n, created))
	late := ansi.Strip(toastCountdown(n, created.Add(4*time.Second)))
	if strings.Count(full, toastCellFull) != toastCountdownCells {
		t.Fatalf("a fresh toast should be full: %q", full)
	}
	if strings.Count(late, toastCellFull) != 1 || !strings.Contains(late, "1s") {
		t.Fatalf("countdown at 1s remaining = %q", late)
	}
	if toastCountdown(n, expires) != "" {
		t.Fatalf("an expired toast still drew a countdown")
	}
}

// `d` dismisses the visible toast outright: the same key means the same thing
// in the centre.
func TestDismissVisibleToast(t *testing.T) {
	m := notifyModel()
	m.postNotification(notify.Notification{Source: notify.SourceSystem, Title: "saved"})
	if !m.dismissVisibleToast() {
		t.Fatal("d did not dismiss the toast on screen")
	}
	if len(m.ToastableNotifications(time.Now())) != 0 {
		t.Fatal("the toast survived dismissal")
	}
	if m.dismissVisibleToast() {
		t.Fatal("d claimed a toast with nothing on screen")
	}
}

// The header indicator counts unread notifications and empties to a dot.
func TestHeaderIndicator(t *testing.T) {
	m := notifyModel()
	if got := ansi.Strip(m.renderHeaderIndicator()); got != "·" {
		t.Fatalf("empty indicator = %q, want ·", got)
	}
	m.postNotification(notify.Notification{Source: notify.SourceAgent, Title: "one"})
	m.postNotification(notify.Notification{Source: notify.SourceAgent, Title: "two"})
	if got := ansi.Strip(m.renderHeaderIndicator()); got != "●2" {
		t.Fatalf("indicator = %q, want ●2", got)
	}
	if got := ansi.StringWidth(ansi.Strip(m.renderHeaderIndicator())); got > headerIndicatorMaxWidth {
		t.Fatalf("indicator is %d cells wide, want at most %d", got, headerIndicatorMaxWidth)
	}
}

// The indicator is the centre's only route in, so it registers a hit region and
// the click toggles the panel.
func TestIndicatorHitRegionTogglesTheCentre(t *testing.T) {
	m := notifyModel()
	m.width, m.height, m.ready = 200, 40, true
	start, end, ok := m.getNotificationIndicatorBounds()
	if !ok || end <= start {
		t.Fatalf("indicator bounds = %d-%d ok=%v", start, end, ok)
	}
	gearStart, _, _ := m.getGearBounds()
	if end != gearStart-1 {
		t.Fatalf("indicator end = %d, want one column left of the gear (%d)", end, gearStart-1)
	}
	m.toggleNotificationCentre()
	if !m.notificationCentreOpen {
		t.Fatal("the toggle did not open the centre")
	}
	m.toggleNotificationCentre()
	if m.notificationCentreOpen {
		t.Fatal("the toggle did not close the centre")
	}
}
