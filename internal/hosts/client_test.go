package hosts

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/hostproto"
)

// scriptedDial serves a fixed stream and then HOLDS THE CONNECTION OPEN, which
// is what a real serve process does: it is a live stream, not a file. A reader
// that reaches EOF immediately makes every session end the instant it starts,
// so the client is always mid-reconnect and no steady state is observable.
//
// Nothing here touches ssh, a network, or a second machine.
func scriptedDial(t *testing.T, stream string, stderr string) (Dialer, *int) {
	t.Helper()
	var calls int
	var mu sync.Mutex
	return func(ctx context.Context) (*Conn, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		reader, closeReader := heldOpenReader(stream)
		return &Conn{
			Stdout: reader,
			Stderr: func() string { return stderr },
			Close:  closeReader,
		}, nil
	}, &calls
}

// heldOpenReader yields stream, then blocks until closed — a pipe that is
// still connected but quiet.
func heldOpenReader(stream string) (io.Reader, func()) {
	pipeReader, pipeWriter := io.Pipe()
	go func() {
		if stream != "" {
			_, _ = io.WriteString(pipeWriter, stream)
		}
	}()
	return pipeReader, func() { _ = pipeWriter.Close(); _ = pipeReader.Close() }
}

func encodeStream(t *testing.T, messages ...hostproto.Message) string {
	t.Helper()
	var buffer strings.Builder
	encoder := hostproto.NewEncoder(&buffer)
	for _, msg := range messages {
		if err := encoder.Encode(msg); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
	return buffer.String()
}

func helloMessage() hostproto.Message {
	return hostproto.Message{Kind: hostproto.KindHello, Proto: hostproto.Version, Hello: &hostproto.Hello{
		Proto: hostproto.Version, Version: "v1", Host: "mac-mini", TmuxPresent: true, ServerRunning: true,
	}}
}

func snapshotMessage(items ...hostproto.Item) hostproto.Message {
	return hostproto.Message{Kind: hostproto.KindSnapshot, Snapshot: &hostproto.Snapshot{
		Generation: 1,
		Projects:   []hostproto.Project{{Key: "/p", Name: "proj", Root: "/p", Items: items}},
	}}
}

func agentItem(id, lane string) hostproto.Item {
	return hostproto.Item{
		ID: id, ProjectKey: "/p", Kind: "shell", Name: id, Provider: "claude", Live: true,
		Agent: &hostproto.Presentation{Lane: lane, Label: lane},
	}
}

// runUntil drives a client until predicate holds or the deadline passes.
func runUntil(t *testing.T, client *Client, predicate func() bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Run(ctx)
	defer client.Close()
	deadline := time.After(5 * time.Second)
	for {
		if predicate() {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("condition never held; health=%+v", client.Health())
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestClientGoesOnlineOnFirstSnapshot(t *testing.T) {
	dial, _ := scriptedDial(t, encodeStream(t, helloMessage(), snapshotMessage(agentItem("a", "working"))), "")
	client := NewClient(Host{ID: "mac-mini", Target: "mac-mini"}, ClientOptions{Dial: dial, MinBackoff: time.Hour})

	runUntil(t, client, func() bool { _, ok := client.Snapshot(); return ok })
	if got := client.Health().State; got != StateOnline {
		t.Errorf("state = %q, want online", got)
	}
	snapshot, _ := client.Snapshot()
	if len(snapshot.Projects) != 1 || len(snapshot.Projects[0].Items) != 1 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if hello := client.Health().Hello; hello == nil || hello.Host != "mac-mini" {
		t.Errorf("hello not retained: %+v", hello)
	}
}

// TestClientRefusesAnIncompatibleProtocol is the version gate: a viewer must
// never half-read a stream it does not understand.
func TestClientRefusesAnIncompatibleProtocol(t *testing.T) {
	future := hostproto.Message{Kind: hostproto.KindHello, Hello: &hostproto.Hello{Proto: hostproto.Version + 1}}
	// Encode manually so the envelope carries the future version too.
	line := fmt.Sprintf(`{"proto":%d,"kind":"hello","seq":1,"hello":{"proto":%d}}`+"\n",
		hostproto.Version+1, hostproto.Version+1)
	_ = future
	dial, _ := scriptedDial(t, line+encodeStream(t, snapshotMessage()), "")
	client := NewClient(Host{ID: "h", Target: "h"}, ClientOptions{Dial: dial, MinBackoff: time.Hour})

	runUntil(t, client, func() bool { return client.Health().State == StateProtocol })
	if _, ok := client.Snapshot(); ok {
		t.Error("a snapshot was accepted from an incompatible stream")
	}
	if fix := client.Health().Fix(); !strings.Contains(fix, "update") {
		t.Errorf("fix %q does not say what to do", fix)
	}
}

func TestClientReportsNoTmuxFromTheHello(t *testing.T) {
	hello := helloMessage()
	hello.Hello.TmuxPresent = false
	dial, _ := scriptedDial(t, encodeStream(t, hello), "")
	client := NewClient(Host{ID: "h", Target: "h"}, ClientOptions{Dial: dial, MinBackoff: time.Hour})

	runUntil(t, client, func() bool { return client.Health().State == StateNoTmux })
	if fix := client.Health().Fix(); !strings.Contains(fix, "tmux") {
		t.Errorf("fix %q does not name tmux", fix)
	}
}

// dialThatEnds serves a stream and then closes, which is what a serve process
// that exits — or never started — looks like.
func dialThatEnds(stream, stderr string) Dialer {
	return func(context.Context) (*Conn, error) {
		return &Conn{
			Stdout: strings.NewReader(stream),
			Stderr: func() string { return stderr },
			Close:  func() {},
		}, nil
	}
}

// TestClientNamesTheShellContaminationFailure. A login profile writing to
// stdout is the single most likely first-run failure, and "unreachable" would
// send the user to look at the network.
func TestClientNamesTheShellContaminationFailure(t *testing.T) {
	client := NewClient(Host{ID: "h", Target: "h"}, ClientOptions{
		Dial: dialThatEnds("\x1b]697;PreExec\x07"+encodeStream(t, helloMessage()), ""), MinBackoff: time.Hour,
	})

	runUntil(t, client, func() bool { return client.Health().State == StateNotProtocol })
	if fix := client.Health().Fix(); !strings.Contains(fix, "stdout") {
		t.Errorf("fix %q does not explain the contamination", fix)
	}
}

func TestClientNamesAMissingSidecarFromStderr(t *testing.T) {
	client := NewClient(Host{ID: "h", Target: "h"}, ClientOptions{
		Dial: dialThatEnds("", "zsh:1: command not found: sidecar"), MinBackoff: time.Hour,
	})

	runUntil(t, client, func() bool { return client.Health().State == StateNoSidecar })
	if fix := client.Health().Fix(); !strings.Contains(fix, "install") {
		t.Errorf("fix %q does not say to install anything", fix)
	}
}

func TestClientReportsUnreachableWhenDialFails(t *testing.T) {
	dial := func(context.Context) (*Conn, error) {
		return nil, fmt.Errorf("ssh: connect to host h port 22: Operation timed out")
	}
	client := NewClient(Host{ID: "h", Target: "h"}, ClientOptions{Dial: dial, MinBackoff: time.Millisecond, MaxBackoff: time.Millisecond})

	runUntil(t, client, func() bool { return client.Health().State == StateUnreachable })
	if fix := client.Health().Fix(); !strings.Contains(fix, "ssh") {
		t.Errorf("fix %q does not mention ssh", fix)
	}
}

// TestClientReconnects: a dropped link is recoverable, not terminal.
func TestClientReconnects(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	dial := func(context.Context) (*Conn, error) {
		mu.Lock()
		calls++
		attempt := calls
		mu.Unlock()
		if attempt == 1 {
			return nil, fmt.Errorf("connection refused")
		}
		return &Conn{
			Stdout: strings.NewReader(encodeStream(t, helloMessage(), snapshotMessage(agentItem("a", "idle")))),
			Close:  func() {},
		}, nil
	}
	client := NewClient(Host{ID: "h", Target: "h"}, ClientOptions{
		Dial: dial, MinBackoff: time.Millisecond, MaxBackoff: 5 * time.Millisecond,
	})
	runUntil(t, client, func() bool { return client.Health().State == StateOnline })
	mu.Lock()
	defer mu.Unlock()
	if calls < 2 {
		t.Errorf("dialed %d times; the client did not retry", calls)
	}
}

// TestBackoffGrowsAndIsCapped. A machine that is off for the weekend must not
// be probed every two seconds for two days.
func TestBackoffGrowsAndIsCapped(t *testing.T) {
	client := NewClient(Host{ID: "h", Target: "h"}, ClientOptions{
		Dial:       func(context.Context) (*Conn, error) { return nil, io.EOF },
		MinBackoff: time.Second, MaxBackoff: 8 * time.Second,
	})
	for attempts, want := range map[int]time.Duration{
		1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second,
		4: 8 * time.Second, 5: 8 * time.Second, 50: 8 * time.Second,
	} {
		if got := client.backoff(attempts); got != want {
			t.Errorf("backoff(%d) = %v, want %v", attempts, got, want)
		}
	}
}

// TestClientAppliesEventsToTheRetainedSnapshot is what lets a caller read a
// whole picture instead of replaying deltas itself.
func TestClientAppliesEventsToTheRetainedSnapshot(t *testing.T) {
	stream := encodeStream(t,
		helloMessage(),
		snapshotMessage(agentItem("a", "working"), agentItem("b", "idle")),
		hostproto.Message{Kind: hostproto.KindEvent, Event: &hostproto.Event{
			Kind: hostproto.EventStatus, Generation: 2, ProjectKey: "/p", ItemID: "a",
			From: "working", To: "blocked", Item: ptr(agentItem("a", "blocked")),
		}},
		hostproto.Message{Kind: hostproto.KindEvent, Event: &hostproto.Event{
			Kind: hostproto.EventDisappear, Generation: 3, ProjectKey: "/p", ItemID: "b",
		}},
		hostproto.Message{Kind: hostproto.KindEvent, Event: &hostproto.Event{
			Kind: hostproto.EventAppear, Generation: 4, ProjectKey: "/p", ItemID: "c",
			Item: ptr(agentItem("c", "working")),
		}},
	)
	dial, _ := scriptedDial(t, stream, "")
	client := NewClient(Host{ID: "h", Target: "h"}, ClientOptions{Dial: dial, MinBackoff: time.Hour})

	runUntil(t, client, func() bool {
		snapshot, ok := client.Snapshot()
		return ok && snapshot.Generation == 4
	})
	snapshot, _ := client.Snapshot()
	lanes := map[string]string{}
	for _, item := range snapshot.Projects[0].Items {
		lanes[item.ID] = item.Agent.Lane
	}
	if lanes["a"] != "blocked" {
		t.Errorf("status event not applied: %v", lanes)
	}
	if _, present := lanes["b"]; present {
		t.Errorf("disappear event not applied: %v", lanes)
	}
	if lanes["c"] != "working" {
		t.Errorf("appear event not applied: %v", lanes)
	}
}

// TestServerEventUpdatesTheIncarnation without touching rows: a restart is
// reported, never used to delete anything (td-8d18de).
func TestServerEventUpdatesTheIncarnation(t *testing.T) {
	stream := encodeStream(t,
		helloMessage(),
		snapshotMessage(agentItem("a", "working")),
		hostproto.Message{Kind: hostproto.KindEvent, Event: &hostproto.Event{
			Kind: hostproto.EventServer, Generation: 2, ServerIncarnation: 99,
		}},
	)
	dial, _ := scriptedDial(t, stream, "")
	client := NewClient(Host{ID: "h", Target: "h"}, ClientOptions{Dial: dial, MinBackoff: time.Hour})

	runUntil(t, client, func() bool {
		snapshot, ok := client.Snapshot()
		return ok && snapshot.ServerIncarnation == 99
	})
	snapshot, _ := client.Snapshot()
	if len(snapshot.Projects[0].Items) != 1 {
		t.Errorf("a server event removed rows: %+v", snapshot.Projects[0].Items)
	}
}

// TestEventForAnUnknownProjectIsDropped. Connecting mid-cycle is normal; a
// synthesised half-row would be indistinguishable from a real one.
func TestEventForAnUnknownProjectIsDropped(t *testing.T) {
	other := agentItem("z", "working")
	other.ProjectKey = "/somewhere-else"
	stream := encodeStream(t,
		helloMessage(),
		snapshotMessage(agentItem("a", "working")),
		hostproto.Message{Kind: hostproto.KindEvent, Event: &hostproto.Event{
			Kind: hostproto.EventAppear, Generation: 2, ProjectKey: "/somewhere-else",
			ItemID: "z", Item: &other,
		}},
	)
	dial, _ := scriptedDial(t, stream, "")
	client := NewClient(Host{ID: "h", Target: "h"}, ClientOptions{Dial: dial, MinBackoff: time.Hour})

	runUntil(t, client, func() bool {
		snapshot, ok := client.Snapshot()
		return ok && snapshot.Generation == 2
	})
	snapshot, _ := client.Snapshot()
	if len(snapshot.Projects) != 1 {
		t.Errorf("an unknown project was invented: %+v", snapshot.Projects)
	}
}

// TestMarkStaleIfQuiet: an online host that stops sending must say so rather
// than presenting old rows as current.
func TestMarkStaleIfQuiet(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	dial, _ := scriptedDial(t, encodeStream(t, helloMessage(), snapshotMessage(agentItem("a", "working"))), "")
	client := NewClient(Host{ID: "h", Target: "h"}, ClientOptions{
		Dial: dial, Now: clock, StaleAfter: time.Minute, MinBackoff: time.Hour,
	})
	runUntil(t, client, func() bool { return client.Health().State == StateOnline })

	if client.MarkStaleIfQuiet() {
		t.Fatal("a host that just reported was marked stale")
	}
	now = now.Add(2 * time.Minute)
	if !client.MarkStaleIfQuiet() {
		t.Fatal("a quiet host was not marked stale")
	}
	if got := client.Health().State; got != StateStale {
		t.Errorf("state = %q, want stale", got)
	}
	// Rows must still show — last-known is more useful than a blank host, as
	// long as it is labelled.
	if !client.Health().State.Shows() {
		t.Error("a stale host hides its rows")
	}
	if client.Health().State.Healthy() {
		t.Error("a stale host reports itself healthy")
	}
}

// TestPublishNeverBlocks. A consumer that stops reading must not freeze the
// host's reader loop; it gets the newest state on its next read.
func TestPublishNeverBlocks(t *testing.T) {
	client := NewClient(Host{ID: "h", Target: "h"}, ClientOptions{
		Dial: func(context.Context) (*Conn, error) { return nil, io.EOF },
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			client.publish(Update{HostID: "h"})
		}
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("publish blocked with nobody reading")
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	client := NewClient(Host{ID: "h", Target: "h"}, ClientOptions{
		Dial: func(context.Context) (*Conn, error) { return nil, io.EOF },
	})
	client.Close()
	client.Close()
}

// TestRunStopsOnClose: the reconnect loop must not outlive the app.
func TestRunStopsOnClose(t *testing.T) {
	client := NewClient(Host{ID: "h", Target: "h"}, ClientOptions{
		Dial:       func(context.Context) (*Conn, error) { return nil, io.EOF },
		MinBackoff: time.Millisecond, MaxBackoff: time.Millisecond,
	})
	stopped := make(chan struct{})
	go func() { client.Run(context.Background()); close(stopped) }()
	time.Sleep(20 * time.Millisecond)
	client.Close()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after Close")
	}
}

func ptr[T any](v T) *T { return &v }
