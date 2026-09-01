package hosts

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/hostproto"
)

func notifyMessage(key string) hostproto.Message {
	origin := hostproto.NotifyOrigin{ItemID: "a", ProjectKey: "/p", Session: "proj-claude", Path: "/p"}
	return hostproto.Message{Kind: hostproto.KindNotify, Notify: &hostproto.NotifyEvent{
		Key: key, OccurredAt: time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC),
		Class: hostproto.NotifyWaiting, Source: "waiting", Severity: "warning",
		Title: "Claude pane needs input", Body: "claude · proj/main", Sticky: true, Origin: origin,
	}}
}

// collectUpdates drains the client's channel until predicate holds, returning
// every update seen. It is the consumer's view: the events arrive as data on
// the stream that already exists, not through a second channel.
func collectUpdates(t *testing.T, client *Client, want int) []Update {
	t.Helper()
	var updates []Update
	var events int
	runUntil(t, client, func() bool {
		for {
			select {
			case update, ok := <-client.Updates():
				if !ok {
					return true
				}
				updates = append(updates, update)
				events += len(update.Notify)
			default:
				return events >= want
			}
		}
	})
	return updates
}

func TestClientExposesUIRequestWithoutFoldingIntoSnapshot(t *testing.T) {
	event := hostproto.UIRequest{
		ID: "req-1", Action: hostproto.UIRequestActionOpen, TTLMs: 1200,
		Origin: hostproto.UIRequestOrigin{TmuxSession: "proj-claude", HostID: "mac-mini"},
		Target: hostproto.UIRequestTarget{Kind: "file", Value: "README.md"},
	}
	stream := encodeStream(t,
		helloMessage(),
		snapshotMessage(agentItem("a", "working")),
		hostproto.Message{Kind: hostproto.KindUIRequest, UIRequest: &event},
	)
	dial, _ := scriptedDial(t, stream, "")
	client := NewClient(Host{ID: "mac-mini", Target: "mac-mini"}, ClientOptions{Dial: dial, MinBackoff: time.Hour})
	var got []hostproto.UIRequest
	runUntil(t, client, func() bool {
		for {
			select {
			case update, ok := <-client.Updates():
				if !ok {
					return true
				}
				got = append(got, update.UIRequest...)
			default:
				return len(got) >= 1
			}
		}
	})
	if len(got) != 1 || got[0].ID != "req-1" || got[0].Origin.HostID != "mac-mini" {
		t.Fatalf("ui requests = %+v", got)
	}
	if got := client.Health().State; got != StateOnline {
		t.Fatalf("health = %s after a ui request", got)
	}
	snapshot, ok := client.Snapshot()
	if !ok || len(snapshot.Projects) != 1 || len(snapshot.Projects[0].Items) != 1 {
		t.Fatalf("snapshot was disturbed: %+v", snapshot)
	}
}

func TestClientExposesForwardedNotifications(t *testing.T) {
	stream := encodeStream(t, helloMessage(), snapshotMessage(agentItem("a", "blocked")), notifyMessage("event-key-1"))
	dial, _ := scriptedDial(t, stream, "")
	client := NewClient(Host{ID: "mac-mini", Target: "mac-mini"}, ClientOptions{Dial: dial, MinBackoff: time.Hour})

	updates := collectUpdates(t, client, 1)
	var events []hostproto.NotifyEvent
	for _, update := range updates {
		if len(update.Notify) > 0 && update.HostID != "mac-mini" {
			t.Errorf("event arrived under host %q", update.HostID)
		}
		events = append(events, update.Notify...)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if events[0].Key != "event-key-1" || events[0].Title != "Claude pane needs input" {
		t.Errorf("event = %+v", events[0])
	}
}

// A notification is a thing that happened, not a fact about the host. Folding
// one into the retained snapshot would make it something a later reader could
// rediscover — the replay this protocol is shaped to prevent.
func TestForwardedNotificationsDoNotEnterTheSnapshot(t *testing.T) {
	stream := encodeStream(t, helloMessage(), snapshotMessage(agentItem("a", "blocked")), notifyMessage("event-key-1"))
	dial, _ := scriptedDial(t, stream, "")
	client := NewClient(Host{ID: "mac-mini", Target: "mac-mini"}, ClientOptions{Dial: dial, MinBackoff: time.Hour})

	collectUpdates(t, client, 1)
	snapshot, ok := client.Snapshot()
	if !ok {
		t.Fatal("no snapshot")
	}
	if len(snapshot.Projects) != 1 || len(snapshot.Projects[0].Items) != 1 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

// Snapshots and status events carry lanes, and a viewer that derived
// notifications from them would announce every reconnect's arrival state.
func TestSnapshotsAndEventsProduceNoNotifications(t *testing.T) {
	status := hostproto.Message{Kind: hostproto.KindEvent, Event: &hostproto.Event{
		Kind: hostproto.EventStatus, Generation: 2, ProjectKey: "/p", ItemID: "a",
		From: "working", To: "blocked", Item: ptr(agentItem("a", "blocked")),
	}}
	stream := encodeStream(t, helloMessage(), snapshotMessage(agentItem("a", "working")), status)
	dial, _ := scriptedDial(t, stream, "")
	client := NewClient(Host{ID: "mac-mini", Target: "mac-mini"}, ClientOptions{Dial: dial, MinBackoff: time.Hour})

	var events int
	runUntil(t, client, func() bool {
		for {
			select {
			case update, ok := <-client.Updates():
				if !ok {
					return true
				}
				events += len(update.Notify)
			default:
				snapshot, ok := client.Snapshot()
				return ok && snapshot.Generation == 2
			}
		}
	})
	if events != 0 {
		t.Errorf("state produced %d notification(s)", events)
	}
}

// A malformed payload must not merely be dropped into silence: the host is not
// speaking this protocol correctly, and the row has to say so.
func TestClientRefusesAnOutOfBoundsNotification(t *testing.T) {
	line := `{"proto":2,"kind":"notify","seq":3,"notify":{"key":"k","occurredAt":"2026-08-30T09:00:00Z","class":"waiting","title":"` +
		strings.Repeat("x", hostproto.MaxNotifyTitleBytes+1) + `","origin":{"itemId":"a"}}}` + "\n"
	stream := encodeStream(t, helloMessage(), snapshotMessage(agentItem("a", "working"))) + line
	dial, _ := scriptedDial(t, stream, "")
	client := NewClient(Host{ID: "mac-mini", Target: "mac-mini"}, ClientOptions{Dial: dial, MinBackoff: time.Hour})

	runUntil(t, client, func() bool { return client.Health().State == StateNotProtocol })
	if detail := client.Health().Detail; !strings.Contains(detail, "exceeds") {
		t.Errorf("detail %q does not name the violation", detail)
	}
}

// The update channel is lossy on purpose — a superseded snapshot is worth
// dropping. An alert is not: a dropped notification is a notification the user
// never gets, so it rides the next update that does get through.
func TestForwardedNotificationsSurviveADroppedUpdate(t *testing.T) {
	client := NewClient(Host{ID: "h", Target: "h"}, ClientOptions{Dial: func(context.Context) (*Conn, error) { return nil, nil }})
	defer client.Close()
	// Fill the buffer so the next publish is dropped.
	for i := 0; i < cap(client.updates); i++ {
		client.updates <- Update{HostID: "h"}
	}
	event := *notifyMessage("dropped-key").Notify
	client.applyNotify(&event)

	// Drain what the consumer had queued; the event was not in any of it.
	for i := 0; i < cap(client.updates); i++ {
		if update := <-client.updates; len(update.Notify) != 0 {
			t.Fatalf("a queued update already carried the event: %+v", update)
		}
	}
	client.publish(Update{HostID: "h"})
	update := <-client.updates
	if len(update.Notify) != 1 || update.Notify[0].Key != "dropped-key" {
		t.Errorf("the dropped notification was lost: %+v", update.Notify)
	}
}
