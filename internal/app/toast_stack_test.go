package app

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/reveal"
)

// stackModel is a notifyModel sized for a real content region, with the reveal
// motion off so a test reads the settled screen rather than a frame of it. The
// motion has its own tests in internal/reveal.
func stackModel(t *testing.T) *Model {
	t.Helper()
	t.Cleanup(reveal.SetAnimatedForTests(false))
	m := notifyModel()
	m.width, m.height, m.ready = 100, 40, true
	return m
}

func postToast(t *testing.T, m *Model, source notify.SourceID, title string) {
	t.Helper()
	if cmd := m.postNotification(notify.Notification{Source: source, Title: title}); cmd == nil {
		t.Fatalf("posting %q failed", title)
	}
	m.syncToastReveal(time.Now())
}

// Design 1b: up to three blocks on screen, newest on top; anything beyond that
// queues and is not painted at all.
func TestToastColumnStacksThreeAndQueuesTheRest(t *testing.T) {
	m := stackModel(t)
	for _, s := range []notify.SourceID{notify.SourceAgent, notify.SourceSession, notify.SourceTD, notify.SourceTasks} {
		postToast(t, m, s, string(s)+" happened")
		time.Sleep(2 * time.Millisecond)
	}

	screen := ansi.Strip(m.renderToastOverlay(blankScreen(100, 40), 0, headerHeight, 100, 38))
	for _, want := range []string{"agent happened", "session happened", "td happened"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("block %q is not on screen:\n%s", want, screen)
		}
	}
	if strings.Contains(screen, "tasks happened") {
		t.Fatalf("a fourth block was painted instead of queued:\n%s", screen)
	}
	// Newest on top: the td block's title sits above the agent block's.
	if tdRow, agentRow := rowOf(screen, "td happened"), rowOf(screen, "agent happened"); tdRow >= agentRow {
		t.Fatalf("td at row %d, agent at row %d — newest is not on top", tdRow, agentRow)
	}
	// Each block is its own pointer target.
	if m.toastMouse.HitMap.Test(96, headerHeight) == nil {
		t.Fatal("the top block has no hit region")
	}
}

// The queued stack takes the slot the moment one frees, without waiting for a
// new post: the heartbeat's sweep reconciles the column.
func TestQueuedToastSurfacesWhenASlotFrees(t *testing.T) {
	m := stackModel(t)
	for _, s := range []notify.SourceID{notify.SourceAgent, notify.SourceSession, notify.SourceTD, notify.SourceTasks} {
		postToast(t, m, s, string(s)+" happened")
		time.Sleep(2 * time.Millisecond)
	}
	// Dismiss the oldest admitted block, exactly as a click on it would.
	if !m.dismissToastStack(toastKeyOf(t, m, "agent happened")) {
		t.Fatal("dismissing the agent block failed")
	}
	m.syncToastReveal(time.Now())

	screen := ansi.Strip(m.renderToastOverlay(blankScreen(100, 40), 0, headerHeight, 100, 38))
	if !strings.Contains(screen, "tasks happened") {
		t.Fatalf("the queued block did not take the freed slot:\n%s", screen)
	}
}

// A repeated message is one block with ×N and a peek line. This is the dedupe:
// five copies of one refusal are one block, not five. Note the identity is
// source *and* title — three *different* messages from one source are three
// blocks, which is what live use actually posts.
func TestARepeatedMessageCollapsesWithAPeekLine(t *testing.T) {
	m := stackModel(t)
	for range 3 {
		postToast(t, m, notify.SourceWaiting, "needs input")
		time.Sleep(2 * time.Millisecond)
	}

	screen := ansi.Strip(m.renderToastOverlay(blankScreen(100, 40), 0, headerHeight, 100, 38))
	if !strings.Contains(screen, "×3") {
		t.Fatalf("the collapsed block is missing its ×N:\n%s", screen)
	}
	if !strings.Contains(screen, "2 more") || !strings.Contains(screen, toastExpandKey) {
		t.Fatalf("the peek line is missing its count or its expand key:\n%s", screen)
	}
	if got := strings.Count(screen, "needs input"); got != 1 {
		t.Fatalf("a collapsed member was drawn anyway: %d copies on screen:\n%s", got, screen)
	}

	// The expand key opens it, and the members are listed.
	if !m.toggleToastExpand() {
		t.Fatal("the expand key did nothing with a collapsed block on screen")
	}
	// The key handler reconciles the column afterwards, which is what re-draws
	// the block at its new height; the cached block is only good for the shape
	// it was rendered for.
	m.syncToastReveal(time.Now())
	screen = ansi.Strip(m.renderToastOverlay(blankScreen(100, 40), 0, headerHeight, 100, 38))
	if got := strings.Count(screen, "needs input"); got != 3 {
		t.Fatalf("expanding listed %d of 3 members:\n%s", got, screen)
	}
	if !m.toggleToastExpand() {
		t.Fatal("the expand key did not collapse an expanded block")
	}
}

// Nothing collapsed on screen means the key belongs to whoever else wants it.
func TestExpandKeyFallsThroughWithNothingToExpand(t *testing.T) {
	m := stackModel(t)
	postToast(t, m, notify.SourceAgent, "one thing happened")
	if m.toggleToastExpand() {
		t.Fatal("the expand key claimed a press with nothing collapsed on screen")
	}
}

// The Phase 1 read gate has to survive stacking: a queued block was never
// painted, so its expiry must not mark it read. A collapsed member was not
// painted either — only the lead was.
func TestQueuedAndCollapsedToastsAreNotMarkedRead(t *testing.T) {
	m := stackModel(t)
	for _, s := range []notify.SourceID{notify.SourceAgent, notify.SourceSession, notify.SourceTD} {
		postToast(t, m, s, string(s)+" happened")
		time.Sleep(2 * time.Millisecond)
	}
	postToast(t, m, notify.SourceTasks, "queued task")
	postToast(t, m, notify.SourceTasks, "queued task two")

	m.sweepNotifications(time.Now())
	for _, n := range m.Notifications() {
		painted := m.toastPainted[n.ID]
		switch n.Title {
		case "queued task", "queued task two":
			if painted {
				t.Fatalf("%q was queued, never painted, and was marked seen", n.Title)
			}
		}
	}
	// The admitted leads are painted, which is what lets their expiry read them.
	painted := 0
	for _, n := range m.Notifications() {
		if m.toastPainted[n.ID] {
			painted++
		}
	}
	if painted != 3 {
		t.Fatalf("painted %d notifications, want the three admitted leads", painted)
	}
}

// Design 1g's suppress-while-resizing, and the resize storm deferred from
// Phase 1: while a rail is being dragged neither floating tier paints, and
// nothing on the suppressed frame counts as seen.
func TestResizeDragSuppressesTheFloatingTiers(t *testing.T) {
	m := stackModel(t)
	postToast(t, m, notify.SourceAgent, "agent happened")
	m.showFlash(FlashMsg{Text: "Saved"})

	m.notificationCentreOpen = true
	m.notificationCentreWidth = notificationCentreDefaultWidth
	m.notificationCentreMouse = mouse.NewHandler()
	m.notificationCentreMouse.StartDrag(60, 5, regionNotificationCentreHandle, m.notificationCentreWidth)
	if !m.overlaysSuppressed() {
		t.Fatal("a live rail drag did not suppress the overlays")
	}

	screen := ansi.Strip(m.renderToastOverlay(blankScreen(100, 40), 0, headerHeight, 100, 38))
	if strings.Contains(screen, "agent happened") {
		t.Fatalf("a toast painted over a rail drag:\n%s", screen)
	}
	screen = ansi.Strip(m.renderFlashOverlay(blankScreen(100, 40), 0, headerHeight, 100, 38))
	if strings.Contains(screen, "Saved") {
		t.Fatalf("a flash painted over a rail drag:\n%s", screen)
	}
	m.sweepNotifications(time.Now())
	for id, painted := range m.toastPainted {
		if painted {
			t.Fatalf("%s was recorded as painted while suppressed", id)
		}
	}
	// The notification is not lost: it is in the store and counts in the header.
	if m.UnreadNotifications() != 1 {
		t.Fatalf("unread = %d, want the suppressed notification still counted", m.UnreadNotifications())
	}
}

// A block the content region has no room for is not on screen, and the read
// gate must treat "no room" exactly as it treats "queued": never painted,
// therefore never read by its own expiry. Before this, a short terminal (or one
// too narrow for a bordered block at all) silently read everything posted to it.
func TestToastsThatDoNotFitAreNeverMarkedPainted(t *testing.T) {
	m := stackModel(t)
	// Room for the header, the footer and one block, and no more.
	m.height = headerHeight + footerHeight + 9
	for _, s := range []notify.SourceID{notify.SourceAgent, notify.SourceSession} {
		postToast(t, m, s, string(s)+" happened")
		time.Sleep(2 * time.Millisecond)
	}
	m.sweepNotifications(time.Now())
	painted := 0
	for _, n := range m.Notifications() {
		if m.toastPainted[n.ID] {
			painted++
		}
	}
	if painted != 1 {
		t.Fatalf("painted %d blocks in a region with room for one", painted)
	}

	// A content region too narrow for a bordered block paints nothing at all.
	narrow := stackModel(t)
	narrow.width = toastMinWidth
	postToast(t, narrow, notify.SourceAgent, "agent happened")
	narrow.sweepNotifications(time.Now())
	for id, ok := range narrow.toastPainted {
		if ok {
			t.Fatalf("%s was recorded as painted on a terminal with no room for a toast", id)
		}
	}
}

// The rendered block is cached at the width it was drawn for. The content
// region moves without the column being re-synced — a terminal resize, the
// centre opening — so a stale cache must not be painted.
func TestToastBlockFollowsTheContentWidth(t *testing.T) {
	m := stackModel(t)
	postToast(t, m, notify.SourceAgent, "agent happened")
	wide := ansi.Strip(m.renderToastOverlay(blankScreen(100, 40), 0, headerHeight, 100, 38))

	// The centre opens: the content region narrows below toastMaxWidth.
	narrow := ansi.Strip(m.renderToastOverlay(blankScreen(100, 40), 0, headerHeight, 30, 38))
	if widthOf(wide, "agent happened") == widthOf(narrow, "agent happened") {
		t.Fatalf("the block kept its old width after the content region narrowed:\n%s", narrow)
	}
	if strings.Contains(narrow, strings.Repeat("─", toastMaxWidth-2)) {
		t.Fatalf("a full-width rule survived into a narrow content region:\n%s", narrow)
	}
}

// widthOf is the length of the line the needle is on, trailing blanks removed.
func widthOf(screen, needle string) int {
	for _, line := range strings.Split(screen, "\n") {
		if strings.Contains(line, needle) {
			return len(strings.TrimRight(line, " "))
		}
	}
	return -1
}

func rowOf(screen, needle string) int {
	for i, line := range strings.Split(screen, "\n") {
		if strings.Contains(line, needle) {
			return i
		}
	}
	return -1
}

// toastKeyOf resolves a block's identity from a title, so a test names a
// notification the way a user would rather than reconstructing the key.
func toastKeyOf(t *testing.T, m *Model, title string) notify.StackKey {
	t.Helper()
	for _, n := range m.notificationCache {
		if n.Title == title {
			return notify.StackKeyFor(n)
		}
	}
	t.Fatalf("no notification titled %q in the store", title)
	return ""
}
