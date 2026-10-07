package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/hostnotify"
	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/uiapi"
)

func TestAPIEventsUsageAndUnavailable(t *testing.T) {
	apiStateTree(t, t.TempDir())
	for _, args := range [][]string{{"api", "events"}, {"api", "events", "--stdio", "--sort", "bad"}, {"api", "events", "--stdio", "--unknown", "x"}} {
		if code, _, _ := runAPICLI(t, args...); code != 2 {
			t.Fatalf("%v exit=%d", args, code)
		}
	}
	if code, _, _ := runAPICLI(t, "api", "events", "--stdio"); code != 1 {
		t.Fatalf("unavailable exit=%d", code)
	}
	if code, _, _ := runAPICLI(t, "api", "events", "--help"); code != 0 {
		t.Fatalf("help exit=%d", code)
	}
}
func TestAPIEventsJSONLBridge(t *testing.T) {
	state := apiStateTree(t, t.TempDir())
	server, err := uiapi.Start(uiapi.Options{StateDir: state, Port: 0, Backend: staticAPIBackend{}, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Shutdown(context.Background()) }()
	r, w := io.Pipe()
	defer func() { _ = r.Close() }()
	defer func() { _ = w.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	env := defaultEnv(w, io.Discard)
	env.Ctx = ctx
	done := make(chan int, 1)
	go func() {
		done <- runAPIEvents(env, []string{"--stdio", "--sort", "name", "--host", "local", "--host", "remote"})
		_ = w.Close()
	}()
	scanner := bufio.NewScanner(r)
	for i, kind := range []string{"hello", "catalog", "terminals"} {
		if !scanner.Scan() {
			t.Fatalf("missing %s: %v", kind, scanner.Err())
		}
		var m uiapi.EventMessage
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		if m.Type != kind || m.Seq != uint64(i+1) {
			t.Fatalf("line: %+v", m)
		}
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("cancel exit=%d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("bridge did not stop")
	}
}
func TestCatalogObservationDigestIgnoresClockAndPreviewButTracksFacts(t *testing.T) {
	snap := hostproto.Snapshot{Generation: 1, ObservedAt: time.Now(), ServerIncarnation: 7, Projects: []hostproto.Project{{Key: "project", Items: []hostproto.Item{{ID: "row", Name: "Shell", ObservedAt: time.Now(), Preview: "output", Agent: &hostproto.Presentation{Lane: "working", CapturedAt: time.Now()}}}}}}
	first := catalogObservationDigest(snap)
	snap.Generation++
	snap.ObservedAt = snap.ObservedAt.Add(time.Second)
	item := &snap.Projects[0].Items[0]
	item.Preview = "new output"
	item.ObservedAt = snap.ObservedAt
	item.Agent.CapturedAt = snap.ObservedAt
	if catalogObservationDigest(snap) != first {
		t.Fatal("observation/preview churn invalidates catalog")
	}
	if item.Agent.CapturedAt != snap.ObservedAt {
		t.Fatal("digest mutated source")
	}
	item.Agent.Attention = true
	if catalogObservationDigest(snap) == first {
		t.Fatal("same-lane attention change invisible")
	}
	item.Agent.Attention = false
	item.Agent.Lane = "done"
	if catalogObservationDigest(snap) == first {
		t.Fatal("agent transition invisible")
	}
	item.Agent.Lane = "working"
	item.Name = "Renamed"
	if catalogObservationDigest(snap) == first {
		t.Fatal("shell rename invisible")
	}
	before := remoteCatalogObservationDigest(hosts.Update{HostID: "host", Incarnation: 1, Snapshot: &snap, Health: hosts.Health{State: hosts.StateOnline}})
	after := remoteCatalogObservationDigest(hosts.Update{HostID: "host", Incarnation: 1, Snapshot: &snap, Health: hosts.Health{State: hosts.StateUnreachable}})
	if before == after {
		t.Fatal("host disconnect invisible")
	}
}

// The API observes real forwarded outcomes even when the host snapshot is unchanged.
// Store authority, opt-in and dedupe are shared with the TUI, and sockets see the centre.
func TestAPIRemoteUpdateReachesNotificationSnapshotAndLiveEvents(t *testing.T) {
	state := apiStateTree(t, t.TempDir())
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "config.json")
	config.SetTestConfigPath(configPath)
	t.Cleanup(config.ResetTestConfigPath)
	if err := os.WriteFile(configPath, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	b := &mobileBackend{env: Env{StateDir: state}}
	aliases := map[string]string{}
	now := time.Now().UTC()
	event := hostproto.NotifyEvent{Key: "waiting-key", OccurredAt: now, Class: hostproto.NotifyWaiting, Source: "waiting", Title: "Remote needs input", Sticky: true, Origin: hostproto.NotifyOrigin{ItemID: "item", ProjectKey: "/remote/root", Session: "same-name", Path: "/remote/root"}}
	update := hosts.Update{HostID: "remote-A", Notify: []hostproto.NotifyEvent{event}}
	if err := b.persistRemoteNotifications(update, aliases); err != nil {
		t.Fatal(err)
	}
	if all, _ := notify.ReadAll(notify.Path(state)); len(all) != 0 {
		t.Fatal("managed SSH opt-out created records")
	}
	if err := config.SaveNotifications(func(cfg *config.NotificationsConfig) { cfg.SSH.ManagedHosts = true }); err != nil {
		t.Fatal(err)
	}
	server, err := uiapi.Start(uiapi.Options{StateDir: state, Port: 0, Backend: staticAPIBackend{}, Version: "remote-notify-test"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Shutdown(context.Background()) }()
	client := uiapi.NewLocalClientForSocket(server.Endpoint())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws://sidecar.local/api/v0/events?notifications=1", &websocket.DialOptions{HTTPClient: client.HTTPClient()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
	readSnapshot := func(count int) uiapi.NotificationSnapshot {
		t.Helper()
		for {
			var message uiapi.EventMessage
			if err := wsjson.Read(ctx, conn, &message); err != nil {
				t.Fatal(err)
			}
			if message.Notifications != nil && len(message.Notifications.Notifications) == count {
				return *message.Notifications
			}
		}
	}
	_ = readSnapshot(0)
	if err := b.persistRemoteNotifications(update, aliases); err != nil {
		t.Fatal(err)
	}
	snapshot := readSnapshot(1)
	if n := snapshot.Notifications[0]; n.ID != notify.RemoteID("remote-A", event.Key) || n.Origin.HostID != "remote-A" || n.Origin.TmuxSession != "same-name" || n.Origin.ProjectKey != "root" {
		t.Fatalf("owning host/project lost: %+v", n)
	}
	if err := b.persistRemoteNotifications(update, aliases); err != nil {
		t.Fatal(err)
	}
	duplicate := event
	duplicate.Key = "other-observer-key"
	if err := b.persistRemoteNotifications(hosts.Update{HostID: "remote-A", Notify: []hostproto.NotifyEvent{duplicate}}, aliases); err != nil {
		t.Fatal(err)
	}
	var viaHTTP uiapi.NotificationSnapshot
	if err := client.Do(ctx, http.MethodGet, "/api/v0/notifications", nil, &viaHTTP); err != nil {
		t.Fatal(err)
	}
	if len(viaHTTP.Notifications) != 1 {
		t.Fatalf("duplicate forwarded alert: %+v", viaHTTP)
	}
	// The same session and event key on another host are a different owner.
	if err := b.persistRemoteNotifications(hosts.Update{HostID: "remote-B", Notify: []hostproto.NotifyEvent{event}}, aliases); err != nil {
		t.Fatal(err)
	}
	_ = readSnapshot(2)
	if err := b.persistRemoteNotifications(hosts.Update{HostID: "remote-A", Notify: []hostproto.NotifyEvent{{Withdraws: duplicate.Key}}}, aliases); err != nil {
		t.Fatal(err)
	}
	snapshot = readSnapshot(1)
	if snapshot.Notifications[0].Origin.HostID != "remote-B" {
		t.Fatal("withdrawal crossed host authority")
	}
	if err := b.persistRemoteNotifications(hosts.Update{HostID: "remote-B", Notify: []hostproto.NotifyEvent{{WithdrawsTransition: true, Class: event.Class, Origin: event.Origin}}}, aliases); err != nil {
		t.Fatal(err)
	}
	_ = readSnapshot(0)
	stale := event
	stale.Key = "stale-key"
	stale.OccurredAt = now.Add(-notify.LiveEventGrace - time.Second)
	if err := b.persistRemoteNotifications(hosts.Update{HostID: "remote-A", Notify: []hostproto.NotifyEvent{stale}}, aliases); err != nil {
		t.Fatal(err)
	}
	if err := client.Do(ctx, http.MethodGet, "/api/v0/notifications", nil, &viaHTTP); err != nil {
		t.Fatal(err)
	}
	if len(viaHTTP.Notifications) != 0 || len(aliases) != 0 {
		t.Fatalf("stale replay or retained wait aliases: %+v %v", viaHTTP, aliases)
	}
	// A reconnect withdrawal and a subsequent new episode can share one update.
	old, ok := hostnotify.Notification("remote-C", event, time.Now().UTC())
	if !ok {
		t.Fatal("old event setup refused")
	}
	old.CreatedAt = time.Now().Add(-30 * time.Second)
	store, err := notify.Open(state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Post(old); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	next := event
	next.Key = "new-episode"
	next.Title = "A new remote wait"
	next.OccurredAt = time.Now().UTC()
	if err := b.persistRemoteNotifications(hosts.Update{HostID: "remote-C", Notify: []hostproto.NotifyEvent{{WithdrawsTransition: true, Class: event.Class, Origin: event.Origin}, next}}, aliases); err != nil {
		t.Fatal(err)
	}
	if err := client.Do(ctx, http.MethodGet, "/api/v0/notifications", nil, &viaHTTP); err != nil {
		t.Fatal(err)
	}
	if len(viaHTTP.Notifications) != 1 || viaHTTP.Notifications[0].Title != next.Title {
		t.Fatalf("withdrawal retired the new episode: %+v", viaHTTP)
	}
}
