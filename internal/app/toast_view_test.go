package app

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/notify"
)

func clickAt(x, y int) tea.MouseMsg {
	return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}
}

func blankScreen(w, h int) string {
	return strings.TrimRight(strings.Repeat(strings.Repeat(" ", w)+"\n", h), "\n")
}

// A toast is a bordered block in the top-right of the content region, carrying
// the title, the body, and — in the title row — the countdown cells and the
// close button.
func TestToastIsDrawnTopRightOfTheContentRegion(t *testing.T) {
	m := notifyModel()
	m.width, m.height, m.ready = 100, 30, true
	m.postNotification(notify.Notification{
		Source: notify.SourceAgent,
		Title:  "Agent finished",
		Body:   "sidecar: tests green",
	})
	syncToasts(t, m)

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
	for _, want := range []string{"Agent finished", "sidecar: tests green", toastCloseGlyph, toastCellFull} {
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
	block := ansi.Strip(renderToastBlock(oneToastStack(n), 40, time.Now(), false))
	if strings.Contains(block, toastCellFull) || strings.Contains(block, toastCellEmpty) {
		t.Fatalf("a sticky toast drew a countdown:\n%s", block)
	}
	// It still shows the close button — that is all a sticky block carries.
	if !strings.Contains(block, toastCloseGlyph) {
		t.Fatalf("a sticky toast has no close button:\n%s", block)
	}
}

// The `×` is its own hit region on top of the block's, and it dismisses exactly
// the block it belongs to.
func TestToastCloseButtonDismissesItsOwnBlock(t *testing.T) {
	m := notifyModel()
	m.width, m.height, m.ready = 100, 30, true
	m.postNotification(notify.Notification{Source: notify.SourceAgent, Title: "Agent finished"})
	m.postNotification(notify.Notification{Source: notify.SourceSystem, Title: "System note"})
	syncToasts(t, m)
	m.renderToastOverlay(blankScreen(100, 30), 0, headerHeight, 100, 28)

	// Close the *agent* block, which is not the one on top: the button belongs
	// to its own block, not to whatever the newest one happens to be.
	agent := toastKeyOf(t, m, "Agent finished")
	want := regionToastClose + ":" + string(agent)
	clicked := false
	for _, r := range m.toastMouse.HitMap.Regions() {
		if r.ID != want {
			continue
		}
		clicked = true
		if got := m.toastMouse.HitMap.Test(r.Rect.X, r.Rect.Y); got == nil || got.ID != want {
			t.Fatalf("the × is covered by its block: %v", got)
		}
		if !m.toastMouseEvent(clickAt(r.Rect.X, r.Rect.Y)) {
			t.Fatal("a click on the × did not dismiss")
		}
	}
	if !clicked {
		t.Fatalf("no close region for the agent block: %+v", m.toastMouse.HitMap.Regions())
	}
	live := m.ToastableNotifications(time.Now())
	if len(live) != 1 || live[0].Title != "System note" {
		t.Fatalf("the × dismissed the wrong block: %+v", live)
	}
}

// The countdown loses a cell as the expiry approaches, off the 1s heartbeat.
func TestCountdownTicksDown(t *testing.T) {
	created := time.Now().UTC()
	expires := created.Add(5 * time.Second)
	n := notify.Notification{CreatedAt: created, ExpiresAt: &expires}
	full := ansi.Strip(toastCountdownMeter(n, created))
	late := ansi.Strip(toastCountdownMeter(n, created.Add(4*time.Second)))
	if strings.Count(full, toastCellFull) != toastCountdownCells {
		t.Fatalf("a fresh toast should be full: %q", full)
	}
	if strings.Count(late, toastCellFull) != 1 {
		t.Fatalf("countdown at 1s remaining = %q", late)
	}
	// Cells only since polish round 2 — no numeric label anywhere in the meter.
	if strings.ContainsAny(late, "0123456789") {
		t.Fatalf("the meter still carries a numeric time: %q", late)
	}
	if toastCountdownMeter(n, expires) != "" {
		t.Fatalf("an expired toast still drew a countdown")
	}
}

// `d` dismisses the visible toast outright: the same key means the same thing
// in the centre.
func TestDismissVisibleToast(t *testing.T) {
	m := notifyModel()
	m.width, m.height, m.ready = 100, 30, true
	m.postNotification(notify.Notification{Source: notify.SourceSystem, Title: "saved"})
	syncToasts(t, m)
	if !m.dismissVisibleToast() {
		t.Fatal("d did not dismiss the toast on screen")
	}
	if len(m.ToastableNotifications(time.Now())) != 0 {
		t.Fatal("the toast survived dismissal")
	}
	// The block is retracting now, which every dismissal path reconciles
	// before the next frame; a second `d` must not claim it.
	syncToasts(t, m)
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

// The source-hued rule under the title used to be rendered two cells wider than
// the toast's interior, which wrapped a `──` stub onto the row below it in every
// toast. Every row of the block is exactly the block's width, and the interior
// is exactly outerWidth-4.
func TestToastRowsFitTheBlockInterior(t *testing.T) {
	n := notify.Normalize(notify.Notification{
		Source: notify.SourceAgent,
		Title:  "Agent finished",
		Body:   "review the diff",
	}, time.Now())

	for _, outer := range []int{toastMinWidth, 32, toastMaxWidth} {
		block := renderToastBlock(oneToastStack(n), outer, time.Now(), false)
		if got := lipgloss.Width(block); got != outer {
			t.Fatalf("outer=%d: block width = %d", outer, got)
		}
		lines := strings.Split(block, "\n")
		for i, line := range lines {
			if got := lipgloss.Width(line); got != outer {
				t.Fatalf("outer=%d: row %d width = %d, want %d (%q)", outer, i, got, outer, line)
			}
		}
		// The rule row (wherever the wrapped title leaves it) fills the
		// interior exactly, and no stub of it survives onto the next row.
		wantRule := strings.Repeat("─", outer-4)
		ruleRow := -1
		for i, line := range lines {
			if strings.Contains(line, wantRule) {
				ruleRow = i
				break
			}
		}
		if ruleRow < 0 {
			t.Fatalf("outer=%d: no row carries a %d-cell rule: %q", outer, outer-4, block)
		}
		if strings.Contains(ansi.Strip(lines[ruleRow+1]), "──") {
			t.Fatalf("outer=%d: the rule overflowed onto the next row: %q", outer, lines[ruleRow+1])
		}
	}
}

// Polish round 2 dropped the key row and the standalone countdown row: a
// plain notification is border + title + rule + body + border, and nothing
// advertises keys in prose.
func TestToastHasNoKeyRowOrCountdownRow(t *testing.T) {
	n := notify.Normalize(notify.Notification{
		Source: notify.SourceAgent,
		Title:  "Agent finished",
		Body:   "review the diff",
	}, time.Now())
	block := ansi.Strip(renderToastBlock(oneToastStack(n), 40, time.Now(), false))
	if strings.Contains(block, "dismiss") {
		t.Fatalf("the key row survived:\n%s", block)
	}
	if got := len(strings.Split(block, "\n")); got != 5 {
		t.Fatalf("block is %d rows, want 5:\n%s", got, block)
	}
	// The countdown is in the title row, left of the ×, and nowhere else.
	rows := strings.Split(block, "\n")
	if !strings.Contains(rows[1], toastCellFull) || !strings.Contains(rows[1], toastCloseGlyph) {
		t.Fatalf("title row does not carry the meter and the ×: %q", rows[1])
	}
	for i, row := range rows {
		if i == 1 {
			continue
		}
		if strings.ContainsAny(row, toastCellFull+toastCellEmpty) {
			t.Fatalf("row %d still draws countdown cells: %q", i, row)
		}
	}
}

// Plan 1.5 item 5: a toast is click-to-dismiss. The whole block is the target,
// because a toast has no focus and nothing else a click could mean.
func TestClickingAToastDismissesIt(t *testing.T) {
	m := notifyModel()
	m.width, m.height, m.ready = 100, 30, true
	m.postNotification(notify.Notification{Source: notify.SourceAgent, Title: "Agent finished"})
	syncToasts(t, m)
	m.renderToastOverlay(blankScreen(100, 30), 0, headerHeight, 100, 28)

	region := m.toastMouse.HitMap.Test(96, headerHeight)
	if region == nil || region.ID != regionToastFor(toastKeyOf(t, m, "Agent finished")) {
		t.Fatalf("no toast hit region under the block's top-right corner: %v", region)
	}
	if !m.toastMouseEvent(clickAt(96, headerHeight)) {
		t.Fatal("a click on the toast did not dismiss it")
	}
	if len(m.ToastableNotifications(time.Now())) != 0 {
		t.Fatal("the toast survived the click")
	}
	// A frame that draws no toast leaves no clickable hole behind it. The
	// block is still retracting, so it is still painted — but a block on its
	// way out is no longer a target.
	syncToasts(t, m)
	m.renderToastOverlay(blankScreen(100, 30), 0, headerHeight, 100, 28)
	if m.toastMouse.HitMap.Test(96, headerHeight) != nil {
		t.Fatal("the hit region outlived the toast")
	}
	if m.toastMouseEvent(clickAt(96, headerHeight)) {
		t.Fatal("a click claimed a toast that is not on screen")
	}
}

// A click anywhere else is not the toast's business: it falls through to the
// content untouched.
func TestClickingBesideAToastIsNotClaimed(t *testing.T) {
	m := notifyModel()
	m.width, m.height, m.ready = 100, 30, true
	m.postNotification(notify.Notification{Source: notify.SourceAgent, Title: "Agent finished"})
	m.renderToastOverlay(blankScreen(100, 30), 0, headerHeight, 100, 28)
	if m.toastMouseEvent(clickAt(2, 20)) {
		t.Fatal("the toast claimed a click in the content")
	}
	if len(m.ToastableNotifications(time.Now())) != 1 {
		t.Fatal("a click in the content dismissed the toast")
	}
}

// A toast must never change key routing: it has no focus context, so the
// focused surface before and after a post is the same one.
func TestAToastNeverTakesFocus(t *testing.T) {
	m := centreTestModel(t, &sizingPlugin{id: "files"})
	before := m.activeContext
	m.postNotification(notify.Notification{Source: notify.SourceAgent, Title: "Agent finished"})
	m.updateContext()
	if m.activeContext != before {
		t.Fatalf("posting a toast moved focus: %q -> %q", before, m.activeContext)
	}
	if m.notificationCentreFocused || m.notificationCentreOpen {
		t.Fatal("posting a toast opened or focused the centre")
	}
}

// oneToastStack is the single-notification stack most of these tests draw: the
// collapse rules have their own tests, and a block of one is what the vast
// majority of real toasts are.
func oneToastStack(n notify.Notification) notify.Stack {
	return notify.Stack{Source: notify.SourceOf(n.Source).ID, Members: []notify.Notification{n}, First: n.CreatedAt}
}
