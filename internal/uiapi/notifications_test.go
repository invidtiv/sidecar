package uiapi

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/marcus/sidecar/internal/agentstatus"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/contentservice"
	"github.com/marcus/sidecar/internal/mobileproto"
	notification "github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/viewerlayout"
	"os"
	"path/filepath"
	"sync/atomic"
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

type delayedNotificationContent struct {
	ContentBackend
	delay atomic.Bool
}

func (b *delayedNotificationContent) LookupProject(ctx context.Context, project, workspace string) (contentservice.Workspace, error) {
	if b.delay.Load() {
		time.Sleep(100 * time.Millisecond)
	}
	return b.ContentBackend.LookupProject(ctx, project, workspace)
}
func TestAPIViewerExpiryDuringAckValidationDoesNotCommit(t *testing.T) {
	isolatedNotificationConfig(t)
	h, root := viewerHarness(t)
	seedScreen(t, h, root)
	c, id := screen(t, h)
	presence(t, h, id, true)
	request := postScreenRequest(t, h, root, uirequest.ActionOpen, nil, "readme.md")
	event := nextUIRequest(t, c)
	store := viewerlayout.FileStore{Dir: filepath.Join(h.s.dir, "layouts")}
	_, before, _ := store.Get("local", root)
	h.s.viewer.mu.Lock()
	h.s.viewer.screens[id].pending[event.ID].event.ExpiresAt = time.Now().Add(40 * time.Millisecond)
	h.s.viewer.mu.Unlock()
	delayed := &delayedNotificationContent{ContentBackend: h.s.opts.Content}
	delayed.delay.Store(true)
	h.s.opts.Content = delayed
	ackScreen(t, h, id, event, 409)
	_, after, _ := store.Get("local", root)
	if before != after {
		t.Fatal("expired during validation but committed")
	}
	if ack := waitScreenAck(t, h, request); ack.Status != uirequest.StatusDeclined {
		t.Fatal(ack)
	}
}
func TestNotificationToastOwnerRequiresActualSubscribedConsumer(t *testing.T) {
	isolatedNotificationConfig(t)
	h, root := viewerHarness(t)
	seedScreen(t, h, root)
	_, id := screen(t, h)
	presence(t, h, id, true)
	v, ok := uirequest.ReadAPIViewer(h.state, time.Now())
	if !ok || v.HasCapability(uirequest.APIViewerNotifications) {
		t.Fatal("legacy screen suppressed toasts")
	}
	c := dialEvents(t, h, "?notifications=1", nil, true)
	_ = readEvent(t, c)
	v, ok = uirequest.ReadAPIViewer(h.state, time.Now())
	if !ok || !v.HasCapability(uirequest.APIViewerNotifications) {
		t.Fatal("subscribed toast owner absent")
	}
	_ = c.CloseNow()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		v, _ = uirequest.ReadAPIViewer(h.state, time.Now())
		if !v.HasCapability(uirequest.APIViewerNotifications) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("closed consumer retained toast ownership")
}

type notificationTargetContent struct {
	ContentBackend
	roots map[string]string
}

func (b *notificationTargetContent) LookupProject(_ context.Context, project, workspace string) (contentservice.Workspace, error) {
	root := b.roots[project+"\x00"+workspace]
	if root == "" {
		return contentservice.Workspace{}, fmt.Errorf("missing workspace")
	}
	return contentservice.Workspace{Root: root}, nil
}
func TestNotificationTargetsUseOriginLinkedWorkspaceAndRefuseMissingOrigin(t *testing.T) {
	isolatedNotificationConfig(t)
	main, linked := t.TempDir(), t.TempDir()
	raw, _ := json.Marshal(map[string]any{"projects": map[string]any{"list": []map[string]string{{"name": "Project alias", "path": main}}}})
	if err := os.WriteFile(config.ConfigPath(), raw, 0600); err != nil {
		t.Fatal(err)
	}
	selector := "opaque-content-selector"
	content := &notificationTargetContent{roots: map[string]string{"Project alias\x00": main, "Project alias\x00" + selector: linked}}
	h := newHarness(t, func(o *Options) { o.Content = content })
	h.backend.snapshot = mobileproto.CatalogSnapshot{Hosts: []mobileproto.CatalogHost{{ID: "local", Local: true}}, Sections: []mobileproto.CatalogSection{{Rows: []mobileproto.CatalogRow{{ID: "linked", OwnerHostID: "local", ProjectName: "Project alias", Path: linked, Session: "private-session", ContentWorkspaceID: selector}}}}}
	all := []notification.Notification{{Origin: notification.Origin{WorkDir: filepath.Join(linked, "src"), TmuxSession: "private-session"}, Targets: []notification.Target{{Kind: notification.TargetFile, Value: "main.go", Line: 7}}}, {Origin: notification.Origin{WorkDir: "/deleted-worktree", TmuxSession: "gone-session"}, Targets: []notification.Target{{Kind: notification.TargetFile, Value: "main.go"}}}}
	h.s.projectNotificationTargets(all)
	got := all[0].Targets[0]
	if got.Project != "Project alias" || got.Workspace != selector || got.Value != "src/main.go" || got.Line != 7 || got.RoutingError != "" {
		t.Fatalf("origin authority lost: %+v", got)
	}
	if all[1].Targets[0].RoutingError == "" || all[1].Targets[0].Project != "" {
		t.Fatal("missing origin silently retargeted")
	}

	qualified := []notification.Notification{{Targets: []notification.Target{{Kind: notification.TargetFile, Project: main, Value: "README.md"}}}}
	h.s.projectNotificationTargets(qualified)
	if got := qualified[0].Targets[0]; got.Project != "Project alias" || got.Workspace != "" || got.RoutingError != "" {
		t.Fatalf("documented absolute project qualifier refused: %+v", got)
	}
	// A linked row without a usable selector must never become the main checkout.
	h.backend.snapshot.Sections[0].Rows[0].ContentWorkspaceID = ""
	missingSelector := []notification.Notification{{Origin: notification.Origin{WorkDir: linked, TmuxSession: "private-session"}, Targets: []notification.Target{{Kind: notification.TargetFile, Value: "main.go"}}}}
	h.s.projectNotificationTargets(missingSelector)
	if missingSelector[0].Targets[0].RoutingError == "" {
		t.Fatal("missing linked selector silently retargeted to main checkout")
	}
}

func TestNotificationCapabilityNegotiatesFromHTTPHello(t *testing.T) {
	isolatedNotificationConfig(t)
	h := newHarness(t)
	response, data := h.localDo(req{path: "/api/v0/hello"})
	if response.StatusCode != 200 {
		t.Fatalf("hello: %d %s", response.StatusCode, data)
	}
	var hello Hello
	if err := json.Unmarshal(data, &hello); err != nil {
		t.Fatal(err)
	}
	for _, cap := range hello.Capabilities {
		if cap == "notifications" {
			return
		}
	}
	t.Fatalf("HTTP hello cannot negotiate notifications: %v", hello.Capabilities)
}

func TestNotificationRemoteSessionTargetUsesExactOwningCatalogIdentity(t *testing.T) {
	isolatedNotificationConfig(t)
	h := newHarness(t)
	h.backend.snapshot = mobileproto.CatalogSnapshot{Hosts: []mobileproto.CatalogHost{{ID: "local", Local: true}, {ID: "remote"}}, Sections: []mobileproto.CatalogSection{{Rows: []mobileproto.CatalogRow{{ID: "local-opaque", OwnerHostID: "local", Session: "same-name", Path: "/remote/root"}, {ID: "remote-opaque", OwnerHostID: "remote", Session: "same-name", Path: "/remote/root", ProjectName: "Remote alias", ContentWorkspaceID: "opaque-remote-workspace"}}}}}
	all := []notification.Notification{{Origin: notification.Origin{HostID: "remote", TmuxSession: "same-name", WorkDir: "/remote/root/src"}, Transition: &notification.TransitionMetadata{Class: notification.TransitionWaiting}}}
	h.s.projectNotificationTargets(all)
	target := all[0].Targets[0]
	if target.Value != "remote-opaque" || target.Project != "Remote alias" || target.Workspace != "opaque-remote-workspace" || target.RoutingError != "" {
		t.Fatalf("remote authority lost: %+v", target)
	}
	h.backend.snapshot.Sections[0].Rows = h.backend.snapshot.Sections[0].Rows[:1]
	all[0].Targets = nil
	h.s.projectNotificationTargets(all)
	if all[0].Targets[0].RoutingError == "" {
		t.Fatal("missing remote session silently selected local namesake")
	}
}
