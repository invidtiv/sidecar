package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/reveal"
)

// A flash is a single line in the top-right of the content region, and it
// never becomes a notification: nothing is stored, nothing is counted.
func TestFlashIsOneLineAndIsNeverStored(t *testing.T) {
	m := notifyModel()
	m.width, m.height, m.ready = 100, 30, true

	cmd := m.showFlash(FlashMsg{Text: "Copied"})
	if cmd == nil {
		t.Fatal("a flash must schedule its own retirement")
	}
	if len(m.Notifications()) != 0 || m.UnreadNotifications() != 0 {
		t.Fatal("a flash must not reach the notification store")
	}

	screen := ansi.Strip(m.renderFlashOverlay(blankScreen(100, 30), 0, headerHeight, 100, 28))
	lines := strings.Split(screen, "\n")
	if len(lines) != 30 {
		t.Fatalf("the overlay changed the line count: %d", len(lines))
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got != 100 {
			t.Fatalf("line %d is %d columns wide, want 100", i, got)
		}
	}
	painted := 0
	for _, line := range lines {
		if strings.Contains(line, "Copied") {
			painted++
		}
	}
	if painted != 1 {
		t.Fatalf("the flash painted %d rows, want exactly 1:\n%s", painted, screen)
	}
	row := lines[headerHeight]
	if !strings.Contains(row, "Copied") {
		t.Fatalf("the flash is not on the content region's first row:\n%s", screen)
	}
	if end := ansi.StringWidth(strings.TrimRight(row, " ")); end != 99 {
		t.Fatalf("flash right edge at column %d, want 99:\n%s", end, screen)
	}
	if !strings.Contains(row, notify.Glyph(notify.SourceSystem)) {
		t.Fatalf("the flash is missing its source glyph:\n%s", screen)
	}
}

// A new flash replaces the one on screen instead of queueing behind it, and
// the old flash's ticks stop applying.
func TestANewFlashReplacesTheOneOnScreen(t *testing.T) {
	t.Cleanup(reveal.SetAnimatedForTests(true))
	m := notifyModel()
	m.width, m.height, m.ready = 100, 30, true

	m.showFlash(FlashMsg{Text: "first"})
	stale := flashTickMsg{seq: m.flash.seq}
	m.showFlash(FlashMsg{Text: "second"})

	if m.flash.text != "second" {
		t.Fatalf("flash text = %q, want the newest", m.flash.text)
	}
	if cmd := m.advanceFlash(stale); cmd != nil {
		t.Fatal("a tick tagged for a replaced flash must be dropped")
	}
	if m.flash.frame != 0 {
		t.Fatalf("a stale tick advanced the live flash to frame %d", m.flash.frame)
	}
}

// The flash shares the toast's corner but never paints over it.
func TestFlashSitsBelowAToast(t *testing.T) {
	m := notifyModel()
	m.width, m.height, m.ready = 100, 30, true
	m.postNotification(notify.Notification{Source: notify.SourceAgent, Title: "Agent finished"})
	m.showFlash(FlashMsg{Text: "Copied"})

	screen := m.renderToastOverlay(blankScreen(100, 30), 0, headerHeight, 100, 28)
	screen = ansi.Strip(m.renderFlashOverlay(screen, 0, headerHeight, 100, 28))
	lines := strings.Split(screen, "\n")

	toastRows := 0
	flashRow := -1
	for i, line := range lines {
		if strings.Contains(line, "Agent finished") {
			toastRows = i
		}
		if strings.Contains(line, "Copied") {
			flashRow = i
		}
	}
	if flashRow < 0 {
		t.Fatalf("the flash was not drawn:\n%s", screen)
	}
	if flashRow <= toastRows {
		t.Fatalf("the flash (row %d) must sit below the toast (row %d):\n%s", flashRow, toastRows, screen)
	}
}

// The fade is an interpolation with a beginning and an end, and it retires the
// flash exactly once.
func TestFlashFadeRunsInAndOutThenRetires(t *testing.T) {
	if !flashAnimated() {
		t.Skip("this terminal is in the degraded, no-fade mode")
	}
	first := flashAlpha(0)
	if first <= 0 || first >= 1 {
		t.Fatalf("the first frame should be partly faded in, got %v", first)
	}
	if got := flashAlpha(flashFadeSteps); got != 1 {
		t.Fatalf("alpha at full strength = %v, want 1", got)
	}
	if got := flashAlpha(flashTotalFrames() - 1); got <= 0 || got >= 1 {
		t.Fatalf("the last frame should be partly faded out, got %v", got)
	}
	if got := flashAlpha(flashTotalFrames()); got != 0 {
		t.Fatalf("alpha past the end = %v, want 0", got)
	}

	m := notifyModel()
	m.width, m.height, m.ready = 100, 30, true
	m.showFlash(FlashMsg{Text: "Copied"})
	for i := 0; i < flashTotalFrames()-1; i++ {
		if cmd := m.advanceFlash(flashTickMsg{seq: m.flash.seq}); cmd == nil {
			t.Fatalf("the animation stopped early, at frame %d", i)
		}
	}
	if cmd := m.advanceFlash(flashTickMsg{seq: m.flash.seq}); cmd != nil {
		t.Fatal("the last frame must not schedule another tick")
	}
	if m.flash.active {
		t.Fatal("the flash should be gone once its animation has run")
	}
	if got := m.renderFlashLine(60); got != "" {
		t.Fatalf("a retired flash still renders %q", got)
	}
}

// Blending is the mechanism the fade uses; its endpoints must be exact.
func TestBlendColorEndpoints(t *testing.T) {
	base := notify.ResolveHue(notify.HueMuted)
	toward := notify.ResolveHue(notify.HueError)
	if blendColor(base, toward, 1) != toward {
		t.Fatal("alpha 1 must be the target colour exactly")
	}
	if blendColor(base, toward, 0) != base {
		t.Fatal("alpha 0 must be the base colour exactly")
	}
	mid := blendColor(base, toward, 0.5)
	if mid == base || mid == toward {
		t.Fatal("a mid alpha must be a genuinely interpolated colour")
	}
}
