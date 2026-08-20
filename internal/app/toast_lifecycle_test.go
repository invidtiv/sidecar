package app

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/reveal"
)

func paint(m *Model) string {
	return ansi.Strip(m.renderToastOverlay(blankScreen(100, 40), 0, headerHeight, 100, 38))
}

// Live-use regression. Three notifications arriving a second apart — the way an
// agent or `sidecar notify post` actually posts, all of them source `agent` —
// stack. They collapsed into one block before, so each arrival *replaced* the
// last on screen.
func TestArrivalsASecondApartStackRatherThanReplace(t *testing.T) {
	m := stackModel(t)
	for _, title := range []string{"Build started", "Tests green", "Deployed"} {
		postToast(t, m, notify.SourceAgent, title)
	}
	screen := paint(m)
	for _, want := range []string{"Build started", "Tests green", "Deployed"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("%q is not on screen — the arrivals replaced each other:\n%s", want, screen)
		}
	}
	if len(m.toastColumn) != 3 {
		t.Fatalf("the column holds %d blocks, want 3", len(m.toastColumn))
	}
	if strings.Contains(screen, "×") {
		t.Fatalf("three distinct messages collapsed into one block:\n%s", screen)
	}
}

// A record another process appended — the Sweep path, which is how a CLI post
// arrives when the file-RPC bus did not reach this instance — enters the column
// exactly as a PostMsg does, and does not disturb the block already on screen.
func TestSweepDiscoveredArrivalJoinsTheColumnWithoutRestartingTheOthers(t *testing.T) {
	m := stackModel(t)
	postToast(t, m, notify.SourceAgent, "Build started")
	first := m.toastReveals[m.toastColumn[0]]

	// Straight into the store, as another process's append looks from here.
	if _, err := m.notifications.Post(notify.Notification{Source: notify.SourceAgent, Title: "Tests green"}); err != nil {
		t.Fatalf("appending: %v", err)
	}
	m.refreshNotifications()
	m.syncToastReveal(time.Now())

	if len(m.toastColumn) != 2 {
		t.Fatalf("the swept record did not take a slot: %d blocks", len(m.toastColumn))
	}
	if got := m.toastReveals[notify.StackKeyFor(notify.Notification{Source: notify.SourceAgent, Title: "Build started"})]; got != first {
		t.Fatal("the block already on screen was rebuilt instead of kept")
	}
	screen := paint(m)
	if !strings.Contains(screen, "Build started") || !strings.Contains(screen, "Tests green") {
		t.Fatalf("both blocks should be on screen:\n%s", screen)
	}
}

// Design 1h, and the symptom that broke it: a block must never be painted whole
// before its reveal has released the rows. The renderer drew straight from the
// store, so a record that arrived between two syncs was painted at full height
// and then torn down to replay its entry — "all 3 appear, then disappear, then
// stack".
func TestABlockIsNeverPaintedAheadOfItsReveal(t *testing.T) {
	defer reveal.SetAnimatedForTests(true)()
	m := notifyModel()
	m.width, m.height, m.ready = 100, 40, true

	// The record exists, but nothing has synced: the machine has not admitted
	// it, so nothing is painted.
	if _, err := m.notifications.Post(notify.Notification{Source: notify.SourceAgent, Title: "Build started"}); err != nil {
		t.Fatalf("appending: %v", err)
	}
	m.refreshNotifications()
	if screen := paint(m); strings.Contains(screen, "Build started") {
		t.Fatalf("the block was painted before the reveal machine admitted it:\n%s", screen)
	}

	// Synced, the block arrives one row at a time and is clipped to them.
	m.syncToastReveal(time.Now())
	r := m.toastReveals[m.toastColumn[0]]
	if r.state.Phase() != reveal.Entering || r.state.Rows() != 1 {
		t.Fatalf("phase=%v rows=%d, want Entering/1", r.state.Phase(), r.state.Rows())
	}
	if got := strings.Count(paint(m), "\n"); got != 39 {
		t.Fatalf("the overlay changed the line count: %d", got)
	}
	full := strings.Count(r.block, "\n") + 1
	for r.state.Phase() == reveal.Entering {
		if r.state.Rows() > full {
			t.Fatalf("the reveal released %d rows of a %d-row block", r.state.Rows(), full)
		}
		m.advanceToastReveal(revealTickMsg{seq: m.toastRevealSeq})
	}
	if !strings.Contains(paint(m), "Build started") {
		t.Fatal("the block never finished arriving")
	}
}

// The other half of the same defect: on countdown end the block must stay
// painted until the retraction starts, then retract bottom-up. The renderer
// dropped the expired record before the machine had begun leaving, so the block
// blinked out and was re-painted only to play its exit.
func TestAnExpiredBlockRetractsWithoutBlinkingOut(t *testing.T) {
	defer reveal.SetAnimatedForTests(true)()
	m := notifyModel()
	m.width, m.height, m.ready = 100, 40, true

	now := time.Now().UTC()
	expires := now.Add(time.Second)
	if _, err := m.notifications.Post(notify.Notification{
		Source: notify.SourceAgent, Title: "Build started", CreatedAt: now, ExpiresAt: &expires,
	}); err != nil {
		t.Fatalf("posting: %v", err)
	}
	m.refreshNotifications()
	for range 40 {
		m.syncToastReveal(now)
		if m.toastReveals[m.toastColumn[0]].state.Phase() == reveal.Shown {
			break
		}
		m.advanceToastReveal(revealTickMsg{seq: m.toastRevealSeq})
	}
	if !strings.Contains(paint(m), "Build started") {
		t.Fatal("test setup: the block never settled on screen")
	}

	// One frame past the countdown, before anything has synced: the record is
	// no longer toastable, and the block is still fully painted. This is the
	// frame that used to go blank, because the renderer asked the store what
	// was on screen instead of the reveal machine.
	after := expires.Add(time.Millisecond)
	if len(notify.Toastable(m.notificationCache, after)) != 0 {
		t.Fatal("test setup: the record should have expired")
	}
	if !strings.Contains(paint(m), "Build started") {
		t.Fatal("the block vanished on the frame its record expired, before the retraction began")
	}

	m.syncToastReveal(after)
	r := m.toastReveals[m.toastColumn[0]]
	if r.state.Phase() != reveal.Leaving {
		t.Fatalf("phase=%v, want Leaving", r.state.Phase())
	}
	rows := r.state.Rows()
	if !strings.Contains(paint(m), "Build started") {
		t.Fatal("the block blinked out instead of retracting from where it was")
	}

	// And it retracts bottom-up, row by row, never jumping back to full height.
	for r.state.Phase() == reveal.Leaving {
		m.advanceToastReveal(revealTickMsg{seq: m.toastRevealSeq})
		if got := r.state.Rows(); got >= rows {
			t.Fatalf("the retraction went from %d rows to %d — it must only shrink", rows, got)
		}
		rows = r.state.Rows()
	}
	if strings.Contains(paint(m), "Build started") {
		t.Fatal("the block is still on screen after its retraction finished")
	}
}
