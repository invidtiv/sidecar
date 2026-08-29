package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/reveal"
	"github.com/marcus/sidecar/internal/uirequest"
)

// syncToasts is the frame boundary the app has and a test otherwise does not:
// every path that changes the record set calls syncToastReveal before the next
// render, because the reveal machine — not the store — is what the renderer,
// the read gate and the dismiss key read. The row motion is pinned off so a
// block is painted whole on the frame it is synced; internal/reveal tests the
// motion itself.
func syncToasts(t *testing.T, m *Model) {
	t.Helper()
	restore := reveal.SetAnimatedForTests(false)
	defer restore()
	m.syncToastReveal(time.Now())
}

func notifyModel() *Model {
	m := &Model{notifications: notify.NewMemStore(), toastMouse: mouse.NewHandler()}
	m.ui = NewUIState()
	return m
}

func TestPostDismissAndSweep(t *testing.T) {
	m := notifyModel()

	if cmd := m.postNotification(notify.Notification{Source: notify.SourceAgent, Title: "done"}); cmd == nil {
		t.Fatalf("posting should broadcast the stored record")
	} else if posted, ok := cmd().(notify.PostedMsg); !ok {
		t.Fatalf("expected a PostedMsg broadcast")
	} else if !posted.Created || posted.Reason != notify.PostCreated {
		t.Fatalf("new post result = %+v", posted)
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

func TestExistingPostBroadcastsWithoutClaimingCreation(t *testing.T) {
	m := notifyModel()
	n := notify.Notification{ID: "fixed", Source: notify.SourceAgent, Title: "once"}
	first := m.postNotification(n)
	if first == nil {
		t.Fatal("first post returned nil")
	}
	second := m.postNotification(n)
	if second == nil {
		t.Fatal("existing post must still reconcile its authoritative id")
	}
	posted, ok := second().(notify.PostedMsg)
	if !ok || posted.Created || posted.Reason != notify.PostExistingID || posted.Notification.ID != "fixed" {
		t.Fatalf("existing post result = %+v", posted)
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

// Live-use regression: a `sidecar notify post` run from anywhere inside the
// project must reach the instance showing it. Ownership was exact path
// equality, so a post from a subdirectory — where an agent shell nearly always
// is — was disowned, the CLI fell back to writing the log, and it told the user
// no Sidecar instance was running while one was on screen in front of them.
func TestNotifyRequestOwnershipCoversSubdirectories(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "internal", "app")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	m := notifyModel()
	m.ui.WorkDir = root

	for _, dir := range []string{root, nested} {
		req := uirequest.Request{
			Action: uirequest.ActionNotify,
			Origin: uirequest.Origin{WorkDir: dir},
		}
		if !m.ownsNotifyRequest(req) {
			t.Fatalf("a post from %q was disowned by the instance showing %q", dir, root)
		}
	}

	// Containment, not prefix matching: a sibling directory is another project.
	sibling := t.TempDir()
	if m.ownsNotifyRequest(uirequest.Request{
		Action: uirequest.ActionNotify,
		Origin: uirequest.Origin{WorkDir: sibling},
	}) {
		t.Fatalf("a post from an unrelated directory %q was claimed", sibling)
	}
	if m.ownsNotifyRequest(uirequest.Request{
		Action: uirequest.ActionNotify,
		Origin: uirequest.Origin{WorkDir: root + "-other"},
	}) {
		t.Fatal("a sibling whose name merely starts with the project's was claimed")
	}
}
