package app

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/uirequest"
)

func notifyModel() *Model {
	m := &Model{notifications: notify.NewMemStore(), toastMouse: mouse.NewHandler()}
	m.ui = NewUIState()
	return m
}

func TestPostDismissAndSweep(t *testing.T) {
	m := notifyModel()

	if cmd := m.postNotification(notify.Notification{Source: notify.SourceAgent, Title: "done"}); cmd == nil {
		t.Fatalf("posting should broadcast the stored record")
	} else if _, ok := cmd().(notify.PostedMsg); !ok {
		t.Fatalf("expected a PostedMsg broadcast")
	}
	if m.UnreadNotifications() != 1 {
		t.Fatalf("unread = %d, want 1", m.UnreadNotifications())
	}
	if len(m.ToastableNotifications(time.Now())) != 1 {
		t.Fatalf("a fresh notification should be toastable")
	}

	id := m.Notifications()[0].ID
	m.readNotification(id)
	if m.UnreadNotifications() != 0 {
		t.Fatalf("reading should clear the unread count")
	}
	m.dismissNotification(id)
	if len(notify.Active(m.Notifications())) != 0 {
		t.Fatalf("a dismissed notification leaves the centre")
	}

	m.sweepNotifications(time.Now())
	if len(m.Notifications()) != 1 {
		t.Fatalf("sweeping inside the retention window keeps the record")
	}
	m.sweepNotifications(time.Now().Add(notify.Retention + time.Minute))
	if len(m.Notifications()) != 0 {
		t.Fatalf("sweeping past retention should compact it away")
	}
}

func TestPostWithNoStoreIsSafe(t *testing.T) {
	m := &Model{}
	if cmd := m.postNotification(notify.Notification{Title: "x"}); cmd != nil {
		t.Fatalf("a model with no store posts nothing")
	}
	m.dismissNotification("anything")
	m.sweepNotifications(time.Now())
	if m.UnreadNotifications() != 0 || m.Notifications() != nil {
		t.Fatalf("a model with no store has no notifications")
	}
}

func TestOwnsNotifyRequest(t *testing.T) {
	m := notifyModel()
	m.ui.WorkDir = t.TempDir()
	m.ui.ProjectRoot = m.ui.WorkDir

	if !m.ownsNotifyRequest(uirequest.Request{}) {
		t.Fatalf("an unaddressed request is for everyone")
	}
	if !m.ownsNotifyRequest(uirequest.Request{Origin: uirequest.Origin{WorkDir: m.ui.WorkDir}}) {
		t.Fatalf("a request for this workdir is ours")
	}
	if m.ownsNotifyRequest(uirequest.Request{Origin: uirequest.Origin{WorkDir: "/somewhere/else", ProjectKey: "other"}}) {
		t.Fatalf("a request for another project is not ours")
	}
}

func TestHandleNotifyRequestPostsPayload(t *testing.T) {
	// Acks are written into the state tree, so pin it: a test must never write
	// into the developer's live Sidecar state.
	config.SetTestStateDir(t.TempDir())
	t.Cleanup(func() { config.SetTestStateDir("") })

	m := notifyModel()
	payload, err := json.Marshal(notify.Notification{ID: "ntf-rpc", Source: notify.SourceTD, Title: "from the bus"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := uirequest.Request{
		Action:  uirequest.ActionNotify,
		Target:  uirequest.Target{Kind: uirequest.TargetKindNotification},
		Origin:  uirequest.Origin{TmuxSession: "sidecar-sh-1"},
		Payload: payload,
	}
	if cmd := m.handleNotifyRequest(req); cmd == nil {
		t.Fatalf("a posted request should broadcast")
	}
	all := m.Notifications()
	if len(all) != 1 || all[0].ID != "ntf-rpc" {
		t.Fatalf("payload not stored: %+v", all)
	}
	if all[0].Origin.TmuxSession != "sidecar-sh-1" {
		t.Fatalf("the request's origin should identify the poster: %+v", all[0].Origin)
	}

	// Another shell may not dismiss it; the poster may.
	other := uirequest.Request{
		Action: uirequest.ActionNotify,
		Target: uirequest.Target{Kind: uirequest.TargetKindNotification, Value: "ntf-rpc"},
		Origin: uirequest.Origin{TmuxSession: "sidecar-sh-2"},
	}
	m.handleNotifyRequest(other)
	if m.Notifications()[0].Dismissed() {
		t.Fatalf("a foreign shell must not be able to dismiss")
	}
	mine := other
	mine.Origin.TmuxSession = "sidecar-sh-1"
	m.handleNotifyRequest(mine)
	if !m.Notifications()[0].Dismissed() {
		t.Fatalf("the poster should be able to dismiss")
	}
}
