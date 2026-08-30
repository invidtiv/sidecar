package app

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/configui"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/notifydelivery"
	"github.com/marcus/sidecar/internal/plugin"
)

type fakeDeliveryCoordinator struct {
	requests []notifydelivery.Request
	removed  []notify.Notification
	result   notifydelivery.Result
	status   notifydelivery.Status
	probes   int
}

func (f *fakeDeliveryCoordinator) Deliver(_ context.Context, request notifydelivery.Request) notifydelivery.Result {
	f.requests = append(f.requests, request)
	return f.result
}

func (f *fakeDeliveryCoordinator) Status(context.Context) notifydelivery.Status {
	f.probes++
	return f.status
}

func (f *fakeDeliveryCoordinator) Remove(_ context.Context, n notify.Notification) error {
	f.removed = append(f.removed, n)
	return nil
}

func runTeaCmd(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, nested := range batch {
			runTeaCmd(nested)
		}
	}
}

func TestPostedNotificationSchedulesDeliveryOnlyForCreatedRecord(t *testing.T) {
	delivery := &fakeDeliveryCoordinator{}
	m := notifyModel()
	m.registry = plugin.NewRegistry(nil)
	m.notificationDelivery = delivery
	n := notify.Notification{ID: "ntf-direct", Source: notify.SourceWaiting, Title: "needs input", CreatedAt: time.Now().UTC()}
	if _, err := m.notifications.Post(n); err != nil {
		t.Fatal(err)
	}
	m.refreshNotifications()

	updated, cmd := m.update(notify.PostedMsg{Notification: n, Created: true, Reason: notify.PostCreated})
	if len(delivery.requests) != 0 {
		t.Fatal("delivery ran synchronously in Update")
	}
	runTeaCmd(cmd)
	if len(delivery.requests) != 1 || delivery.requests[0].Discovered || delivery.requests[0].Notification.ID != n.ID {
		t.Fatalf("direct delivery requests = %+v", delivery.requests)
	}

	m = modelPointer(updated)
	_, cmd = m.update(notify.PostedMsg{Notification: n, Created: false, Reason: notify.PostExistingID})
	runTeaCmd(cmd)
	if len(delivery.requests) != 1 {
		t.Fatalf("existing id replayed delivery: %+v", delivery.requests)
	}
}

func TestSweepOffersOnlyNewlyDiscoveredRecords(t *testing.T) {
	dir := t.TempDir()
	appStore, err := notify.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	externalStore, err := notify.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := notifyModel()
	m.notifications = appStore
	m.refreshNotifications()
	now := time.Now().UTC()
	posted, err := externalStore.Post(notify.Notification{ID: "ntf-swept", Source: notify.SourceAgent, Title: "fresh", CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	discovered := m.sweepNotifications(now)
	if len(discovered) != 1 || discovered[0].ID != posted.ID {
		t.Fatalf("discovered = %+v", discovered)
	}
	if again := m.sweepNotifications(now.Add(time.Second)); len(again) != 0 {
		t.Fatalf("same record rediscovered: %+v", again)
	}

	// Startup records are already in the render cache and never count as a
	// fresh sweep discovery, even when they are unread.
	restarted := notifyModel()
	restarted.notifications = appStore
	restarted.refreshNotifications()
	if backlog := restarted.sweepNotifications(now.Add(2 * time.Second)); len(backlog) != 0 {
		t.Fatalf("startup backlog was offered: %+v", backlog)
	}
}

func TestSweepCancelsNewlyDiscoveredDismissedRecordWithoutDelivering(t *testing.T) {
	dir := t.TempDir()
	appStore, err := notify.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	externalStore, err := notify.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	delivery := &fakeDeliveryCoordinator{}
	m := notifyModel()
	m.notifications = appStore
	m.notificationDelivery = delivery
	m.refreshNotifications()
	now := time.Now().UTC()
	posted, err := externalStore.Post(notify.Notification{ID: "ntf-dismissed-between", Source: notify.SourceAgent, Title: "brief", CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := externalStore.Dismiss(posted.ID); err != nil {
		t.Fatal(err)
	}
	if discovered := m.sweepNotifications(now); len(discovered) != 0 {
		t.Fatalf("dismissed record offered for delivery: %+v", discovered)
	}
	cmds := m.takeNotificationDeliveryCmds()
	if len(cmds) != 1 {
		t.Fatalf("cancellation commands = %d", len(cmds))
	}
	runTeaCmd(cmds[0])
	if len(delivery.requests) != 0 || len(delivery.removed) != 1 || delivery.removed[0].ID != posted.ID {
		t.Fatalf("requests=%+v cancellations=%+v", delivery.requests, delivery.removed)
	}
}

func TestDelayedPostedMsgAfterDismissNeverDelivers(t *testing.T) {
	delivery := &fakeDeliveryCoordinator{}
	m := notifyModel()
	m.registry = plugin.NewRegistry(nil)
	m.notificationDelivery = delivery
	n := notify.Notification{ID: "ntf-delayed", Source: notify.SourceAgent, Title: "brief", CreatedAt: time.Now().UTC()}
	if _, err := m.notifications.Post(n); err != nil {
		t.Fatal(err)
	}
	m.refreshNotifications()
	m.dismissNotification(n.ID)
	// Model the delayed PostedMsg independently of the dismissal command. The
	// authoritative store state must be enough to suppress it.
	m.notificationDeliveryCmds = nil
	_, cmd := m.update(notify.PostedMsg{Notification: n, Created: true, Reason: notify.PostCreated})
	runTeaCmd(cmd)
	if len(delivery.requests) != 0 || len(delivery.removed) != 1 {
		t.Fatalf("requests=%+v cancellations=%+v", delivery.requests, delivery.removed)
	}
}

// The direct-terminal transport must not write to the terminal itself from a
// delivery goroutine: the renderer owns the screen, and bytes written from
// under it land inside a frame. The app collects them and returns them as raw
// output so Bubble Tea emits them between frames.
func TestTerminalNotificationBytesReachTheRendererRatherThanTheTerminal(t *testing.T) {
	writer := &terminalNotifyWriter{}
	sequence := "\x1b]9;needs input\x07"
	delivery := &writingDeliveryCoordinator{write: func() { _, _ = writer.Write([]byte(sequence)) }}
	m := notifyModel()
	m.notificationDelivery = delivery
	m.terminalNotifyWriter = writer
	n := notify.Notification{ID: "ntf-terminal", Source: notify.SourceWaiting, Title: "needs input", CreatedAt: time.Now().UTC()}
	if _, err := m.notifications.Post(n); err != nil {
		t.Fatal(err)
	}
	m.refreshNotifications()

	msg := m.deliverNotificationCmd(n, false)()
	if msg == nil {
		t.Fatal("the written sequence was dropped instead of being handed to the renderer")
	}
	if got := writer.drain(); got != "" {
		t.Fatalf("the sequence was left buffered as well as emitted: %q", got)
	}

	// A delivery that writes nothing — the ordinary local case — must not push
	// an empty raw message through the renderer.
	quiet := &fakeDeliveryCoordinator{}
	m.notificationDelivery = quiet
	if msg := m.deliverNotificationCmd(n, false)(); msg != nil {
		t.Fatalf("a silent delivery produced renderer output: %#v", msg)
	}
}

// A remote wait can outlive the serve process that announced it, so the answer
// arrives naming the transition rather than the record ID. It has to reach the
// live record, dismiss exactly one, and cancel that record's native banner.
func TestDismissByTransitionRetiresTheLiveRecordOnly(t *testing.T) {
	delivery := &fakeDeliveryCoordinator{}
	m := notifyModel()
	m.notificationDelivery = delivery
	const dedupe = "mac-mini:abc123:waiting"
	waiting := notify.Notification{
		ID: "ntf-remote-one", Source: notify.SourceWaiting, Sticky: true, Title: "needs input",
		CreatedAt: time.Now().UTC(),
		Transition: &notify.TransitionMetadata{
			Class: notify.TransitionWaiting, LaneKey: "mac-mini\x1fw1", DedupeKey: dedupe,
		},
	}
	other := notify.Notification{
		ID: "ntf-remote-two", Source: notify.SourceWaiting, Sticky: true, Title: "different agent",
		CreatedAt: time.Now().UTC(),
		Transition: &notify.TransitionMetadata{
			Class: notify.TransitionWaiting, LaneKey: "mac-mini\x1fw2", DedupeKey: "mac-mini:def456:waiting",
		},
	}
	for _, n := range []notify.Notification{waiting, other} {
		if _, err := m.notifications.Post(n); err != nil {
			t.Fatal(err)
		}
	}
	m.refreshNotifications()

	m.dismissTransition(dedupe)
	got, ok := m.findNotification(waiting.ID)
	if !ok || !got.Dismissed() {
		t.Fatalf("the named transition was not dismissed: found=%v record=%+v", ok, got)
	}
	untouched, ok := m.findNotification(other.ID)
	if !ok || untouched.Dismissed() {
		t.Fatal("dismissing one transition retired another workspace's wait")
	}
	if len(m.takeNotificationDeliveryCmds()) != 1 {
		t.Fatal("dismissing a transition did not cancel its native banner")
	}

	// An unknown key, and a repeat of one already dismissed, are both no-ops
	// rather than reaching for the next record.
	m.dismissTransition("mac-mini:missing:waiting")
	m.dismissTransition(dedupe)
	if cmds := m.takeNotificationDeliveryCmds(); len(cmds) != 0 {
		t.Fatalf("a withdrawal matching nothing produced %d cancellation(s)", len(cmds))
	}
}

// An explicit Test delivers through the same providers as real work. If its
// sequence is not emitted it does not merely fail quietly — it stays buffered
// and rides out with the next unrelated notification, putting two sequences on
// the terminal, one of them stale.
func TestExplicitTestEmitsItsOwnTerminalSequenceRatherThanLeakingIt(t *testing.T) {
	writer := &terminalNotifyWriter{}
	delivery := &writingDeliveryCoordinator{write: func() { _, _ = writer.Write([]byte("\x1b]9;TEST\x07")) }}
	m := notifyModel()
	m.notificationDelivery = delivery
	m.terminalNotifyWriter = writer

	cmd, handled := m.configSurfaceMsg(configui.TestNotificationDeliveryMsg{Event: notifydelivery.TestWaiting})
	if !handled || cmd == nil {
		t.Fatalf("the test action was not handled: handled=%v cmd=%v", handled, cmd)
	}
	result, ok := cmd().(configui.NotificationTestResultMsg)
	if !ok {
		t.Fatalf("test command produced %#v", result)
	}
	resultCmd, handled := m.configSurfaceMsg(result)
	if !handled || resultCmd == nil {
		t.Fatal("the test result produced no command")
	}
	if !strings.Contains(teaMsgString(t, resultCmd), "TEST") {
		t.Fatal("the explicit test reported delivered without emitting its sequence")
	}
	if left := writer.drain(); left != "" {
		t.Fatalf("the test sequence stayed buffered and would ride the next delivery: %q", left)
	}
}

// teaMsgString renders every message a command produces, flattening a batch,
// so a test can assert on raw output without depending on Bubble Tea's
// internal message types.
func teaMsgString(t *testing.T, cmd tea.Cmd) string {
	t.Helper()
	if cmd == nil {
		return ""
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var all strings.Builder
		for _, sub := range batch {
			all.WriteString(teaMsgString(t, sub))
		}
		return all.String()
	}
	return fmt.Sprintf("%v", msg)
}

type writingDeliveryCoordinator struct {
	fakeDeliveryCoordinator
	write func()
}

func (c *writingDeliveryCoordinator) Deliver(ctx context.Context, request notifydelivery.Request) notifydelivery.Result {
	c.write()
	return c.fakeDeliveryCoordinator.Deliver(ctx, request)
}

func TestQueuedDeliveryRevalidatesDismissedStateBeforeProviderWork(t *testing.T) {
	delivery := &fakeDeliveryCoordinator{}
	m := notifyModel()
	m.notificationDelivery = delivery
	n := notify.Notification{ID: "ntf-revalidate", Source: notify.SourceAgent, Title: "brief", CreatedAt: time.Now().UTC()}
	if _, err := m.notifications.Post(n); err != nil {
		t.Fatal(err)
	}
	m.refreshNotifications()
	cmd := m.deliverNotificationCmd(n, false)
	m.dismissNotification(n.ID)
	m.notificationDeliveryCmds = nil
	runTeaCmd(cmd)
	if len(delivery.requests) != 0 || len(delivery.removed) != 1 {
		t.Fatalf("requests=%+v cancellations=%+v", delivery.requests, delivery.removed)
	}
}

func TestStickyDismissalQueuesNativeRemovalAsCommand(t *testing.T) {
	delivery := &fakeDeliveryCoordinator{}
	m := notifyModel()
	m.notificationDelivery = delivery
	result, err := m.notifications.Post(notify.Notification{
		ID: "ntf-wait", Source: notify.SourceWaiting, Sticky: true, Title: "waiting",
		Origin: notify.Origin{TmuxSession: "sidecar-sh-one"},
	})
	if err != nil {
		t.Fatal(err)
	}
	m.refreshNotifications()
	m.dismissNotification(result.ID)
	if len(delivery.removed) != 0 {
		t.Fatal("native removal ran synchronously")
	}
	cmds := m.takeNotificationDeliveryCmds()
	if len(cmds) != 1 {
		t.Fatalf("queued removal commands = %d", len(cmds))
	}
	runTeaCmd(cmds[0])
	if len(delivery.removed) != 1 || delivery.removed[0].ID != result.ID {
		t.Fatalf("removed = %+v", delivery.removed)
	}
}

func modelPointer(model tea.Model) *Model {
	switch typed := model.(type) {
	case Model:
		return &typed
	case *Model:
		return typed
	default:
		return nil
	}
}
