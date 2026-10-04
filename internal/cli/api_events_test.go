package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/hosts"
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
