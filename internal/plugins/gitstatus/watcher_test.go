package gitstatus

import "testing"

func TestWatcherBufferedIndexEventUpgradesToHistory(t *testing.T) {
	w := &Watcher{events: make(chan WatchEvent, 1)}
	w.deliver(WatchEvent{History: false})
	w.deliver(WatchEvent{History: true})

	event := <-w.events
	if !event.History {
		t.Fatal("undrained index event lost the later history invalidation")
	}
	select {
	case extra := <-w.events:
		t.Fatalf("upgrade queued an extra event: %#v", extra)
	default:
	}
}
