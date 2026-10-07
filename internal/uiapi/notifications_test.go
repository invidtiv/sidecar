package uiapi

import (
	"encoding/json"
	"github.com/marcus/sidecar/internal/agentstatus"
	"github.com/marcus/sidecar/internal/config"
	notification "github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/viewerlayout"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func isolatedNotificationConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	config.SetTestConfigPath(filepath.Join(dir, "sidecar", "config.json"))
	t.Cleanup(config.ResetTestConfigPath)
	if err := os.MkdirAll(filepath.Join(dir, "sidecar"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sidecar", "config.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestNotificationCentreStoreEventsAndActualReceipt(t *testing.T) {
	isolatedNotificationConfig(t)
	h, root := viewerHarness(t)
	seedScreen(t, h, root)
	c := dialEvents(t, h, "?viewer=uiRequestRelayV1&notifications=1", nil, true)
	_ = readEvent(t, c)
	identity := readEvent(t, c)
	id := identity.Viewer.ID
	presence(t, h, id, true)
	n := notification.Notification{ID: notification.NewID(), Source: notification.SourceSystem, Title: "API toast", CreatedAt: time.Now(), Sticky: true}
	raw, _ := json.Marshal(n)
	request := uirequest.Request{ID: uirequest.NewRequestID(), Action: uirequest.ActionNotify, CreatedAt: time.Now(), TTLMs: 15000, Origin: uirequest.Origin{WorkDir: root}, Payload: raw}
	if _, err := uirequest.WriteRequest(h.state, request); err != nil {
		t.Fatal(err)
	}
	for {
		event := readEvent(t, c)
		if event.Type == "notifications" && len(event.Notifications.Notifications) > 0 {
			break
		}
	}
	if acks, _ := uirequest.ReadAcks(h.state, request.ID, request.Action); len(acks) > 0 {
		t.Fatal("connected consumer alone was reported delivered")
	}
	body, _ := json.Marshal(NotificationReceiptRequest{ID: n.ID})
	r, b := h.localDo(req{method: "POST", path: notificationReceiptPath, body: string(body)})
	expect(t, r, b, 409, "claim_unavailable")
	r, b = h.localDo(req{method: "POST", path: notificationClaimPath, body: string(body)})
	expect(t, r, b, 200, "")
	var claim NotificationClaimResponse
	if err := json.Unmarshal(b, &claim); err != nil {
		t.Fatal(err)
	}
	if !claim.Claimed {
		t.Fatal("actual toast claim declined")
	}
	r, b = h.localDo(req{method: "POST", path: notificationClaimPath, body: string(body)})
	expect(t, r, b, 200, "")
	if err := json.Unmarshal(b, &claim); err != nil {
		t.Fatal(err)
	}
	if claim.Claimed {
		t.Fatal("duplicate channel claim")
	}
	r, b = h.localDo(req{method: "POST", path: notificationReceiptPath, body: string(body)})
	expect(t, r, b, 200, "")
	if ack := waitScreenAck(t, h, request); ack.Status != uirequest.StatusOpened {
		t.Fatal(ack)
	}
	mutation, _ := json.Marshal(NotificationMutation{ID: n.ID})
	r, b = h.localDo(req{method: "POST", path: notificationReadPath, body: string(mutation)})
	expect(t, r, b, 200, "")
	var snapshot NotificationSnapshot
	if err := json.Unmarshal(b, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Unread != 0 || snapshot.Notifications[0].ReadAt == nil || len(snapshot.ToastIDs) != 0 {
		t.Fatalf("read: %+v", snapshot)
	}
	r, b = h.localDo(req{method: "POST", path: notificationDismissPath, body: string(mutation)})
	expect(t, r, b, 200, "")
	if err := json.Unmarshal(b, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Notifications) != 0 {
		t.Fatal("dismiss retained in active centre")
	}
}
func TestAPIViewerLineOutcomeAndCancelledRequest(t *testing.T) {
	isolatedNotificationConfig(t)
	h, root := viewerHarness(t)
	seedScreen(t, h, root)
	c, id := screen(t, h)
	presence(t, h, id, true)
	store := viewerlayout.FileStore{Dir: filepath.Join(h.s.dir, "layouts")}
	var before string
	raw := uirequest.Request{ID: uirequest.NewRequestID(), Action: uirequest.ActionOpen, CreatedAt: time.Now(), TTLMs: 15000, Origin: uirequest.Origin{WorkDir: root, TmuxSession: "sidecar-sh-content-1"}, Target: uirequest.Target{Kind: "file", Value: "readme.md", Line: 1}}
	if _, err := uirequest.WriteRequest(h.state, raw); err != nil {
		t.Fatal(err)
	}
	event := nextUIRequest(t, c)
	body, _ := json.Marshal(ViewerAckRequest{ViewerID: id, ID: event.ID, Status: uirequest.StatusOpened, Line: &uirequest.LineAck{Requested: 1, Applied: true}})
	r, b := h.localDo(req{method: "POST", path: viewerAckPath, body: string(body)})
	expect(t, r, b, 200, "")
	ack := waitScreenAck(t, h, raw)
	if ack.Line == nil || !ack.Line.Applied || ack.Line.Requested != 1 {
		t.Fatal(ack)
	}
	_, before, _ = store.Get("local", root)
	raw.ID = uirequest.NewRequestID()
	if _, err := uirequest.WriteRequest(h.state, raw); err != nil {
		t.Fatal(err)
	}
	event = nextUIRequest(t, c)
	if err := uirequest.WithRequestLock(h.state, raw.ID, raw.Action, func() error { return uirequest.Cleanup(h.state, raw.ID, raw.Action) }); err != nil {
		t.Fatal(err)
	}
	ackScreen(t, h, id, event, 409)
	_, after, _ := store.Get("local", root)
	if before != after {
		t.Fatal("cancelled request committed a layout")
	}
}

func TestNotificationSettingsPersistAndLiveReadScopeRefusesMutation(t *testing.T) {
	isolatedNotificationConfig(t)
	h := newHarness(t)
	cfg := config.DefaultNotificationsConfig()
	cfg.Native.Mode = config.DeliveryAlways
	cfg.QuietHours.Enabled = true
	cfg.QuietHours.Start = "00:00"
	cfg.QuietHours.End = "00:00"
	disabled := false
	enabled := true
	cfg.Sources = map[string]config.NotificationSourceConfig{"system": {Toast: &disabled, Native: &enabled, Expiry: "30s"}}
	raw, _ := json.Marshal(cfg)
	r, b := h.localDo(req{method: "PUT", path: notificationSettingsPath, body: string(raw)})
	expect(t, r, b, 200, "")
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Notifications.Native.Mode != config.DeliveryAlways || !loaded.Notifications.QuietHours.Enabled || loaded.Notifications.Sources["system"].Expiry != "30s" {
		t.Fatal(loaded.Notifications)
	}
	store, err := notification.Open(h.state)
	if err != nil {
		t.Fatal(err)
	}
	n := notification.Notification{ID: notification.NewID(), Source: notification.SourceSystem, Title: "quiet", CreatedAt: time.Now(), Sticky: true}
	_, err = store.Post(n)
	_ = store.Close()
	if err != nil {
		t.Fatal(err)
	}
	r, b = h.localDo(req{path: notificationsPath})
	expect(t, r, b, 200, "")
	var snapshot NotificationSnapshot
	if err := json.Unmarshal(b, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.ToastIDs) != 0 || len(snapshot.DeliveryIDs) != 0 || snapshot.Delivery[n.ID].Native.Reason != notification.ReasonQuietHours {
		t.Fatalf("shared source/quiet rules not applied: %+v", snapshot)
	}
	r, b = h.localDo(req{method: "POST", path: "/api/v0/origins", body: `{"origin":"https://notification-read.example","scopes":["content:read"]}`})
	expect(t, r, b, 200, "")
	var registration OriginRegistration
	if err := json.Unmarshal(b, &registration); err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Authorization": "Bearer " + registration.Token, "Origin": "https://notification-read.example"}
	r, b = h.browserDo(req{path: notificationsPath, header: headers})
	expect(t, r, b, 200, "")
	headers["Content-Type"] = "application/json"
	headers["X-Sidecar-Request"] = "1"
	r, b = h.browserDo(req{method: "POST", path: notificationReadPath, body: `{"id":"ntf-any"}`, header: headers})
	expect(t, r, b, 403, "")
}

func TestNotificationStreamSeesSharedLaneNeedsInputAndDoneWithoutTUI(t *testing.T) {
	isolatedNotificationConfig(t)
	h := newHarness(t)
	c := dialEvents(t, h, "?notifications=1", nil, true)
	_ = readEvent(t, c)
	tracker := notification.LaneTracker{Debounce: time.Nanosecond}
	now := time.Now()
	o := notification.LaneObservation{Key: "lane-test", Label: "Agent", Origin: notification.Origin{TmuxSession: "private-agent"}, Presentation: agentstatus.Presentation{Lane: agentstatus.LaneWorking}}
	tracker.Observe([]notification.LaneObservation{o}, now)
	apply := func(events notification.LaneEvents) {
		store, err := notification.Open(h.state)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = store.Close() }()
		for _, n := range events.Post {
			if _, err := store.Post(n); err != nil {
				t.Fatal(err)
			}
		}
		for _, id := range events.Dismiss {
			if err := store.Dismiss(id); err != nil {
				t.Fatal(err)
			}
		}
	}
	o.Presentation.Lane = agentstatus.LaneBlocked
	tracker.Observe([]notification.LaneObservation{o}, now.Add(time.Millisecond))
	apply(tracker.Observe([]notification.LaneObservation{o}, now.Add(2*time.Millisecond)))
	for {
		event := readEvent(t, c)
		if event.Type == "notifications" && len(event.Notifications.Notifications) == 1 {
			if event.Notifications.Notifications[0].Transition.Class != notification.TransitionWaiting {
				t.Fatal(event)
			}
			break
		}
	}
	o.Presentation.Lane = agentstatus.LaneDone
	tracker.Observe([]notification.LaneObservation{o}, now.Add(3*time.Millisecond))
	apply(tracker.Observe([]notification.LaneObservation{o}, now.Add(4*time.Millisecond)))
	for {
		event := readEvent(t, c)
		if event.Type == "notifications" && len(event.Notifications.Notifications) == 1 && event.Notifications.Notifications[0].Transition.Class == notification.TransitionDone {
			break
		}
	}
}
