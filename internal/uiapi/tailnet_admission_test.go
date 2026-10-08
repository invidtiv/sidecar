package uiapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// A peer that hangs up before whois answers must not cancel the lookup: the
// answer is cached, so dropping connections cannot buy a fresh subprocess
// each time.
func TestWhoisLookupSurvivesCallerHangups(t *testing.T) {
	var calls atomic.Int32
	cache := newWhoisCache(context.Background(), func(ctx context.Context, _ netip.Addr) (TailnetPeer, error) {
		calls.Add(1)
		select {
		case <-time.After(50 * time.Millisecond):
			return TailnetPeer{}, errors.New("peer not found")
		case <-ctx.Done():
			return TailnetPeer{}, ctx.Err()
		}
	}, time.Now)
	addr := netip.MustParseAddr("100.100.1.1")
	for i := 0; i < 50; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
		_, _ = cache.get(ctx, addr)
		cancel()
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("50 abandoned attempts from one address made %d lookups, want 1", got)
	}
}

// A waiter whose own context is live gets the real answer, whatever happened
// to the caller that started the lookup.
func TestWhoisWaiterGetsTheRealAnswer(t *testing.T) {
	release := make(chan struct{})
	cache := newWhoisCache(context.Background(), func(ctx context.Context, _ netip.Addr) (TailnetPeer, error) {
		select {
		case <-release:
			return TailnetPeer{Login: "a@example.com"}, nil
		case <-ctx.Done():
			return TailnetPeer{}, ctx.Err()
		}
	}, time.Now)
	addr := netip.MustParseAddr("100.100.1.2")
	first, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	var firstErr, waiterErr error
	var waiterPeer TailnetPeer
	wg.Add(1)
	go func() { defer wg.Done(); _, firstErr = cache.get(first, addr) }()
	time.Sleep(10 * time.Millisecond)
	wg.Add(1)
	go func() { defer wg.Done(); waiterPeer, waiterErr = cache.get(context.Background(), addr) }()
	time.Sleep(10 * time.Millisecond)
	cancel()
	time.Sleep(10 * time.Millisecond)
	close(release)
	wg.Wait()
	if !errors.Is(firstErr, context.Canceled) {
		t.Fatalf("first caller: %v", firstErr)
	}
	if waiterErr != nil || waiterPeer.Login != "a@example.com" {
		t.Fatalf("waiter: %+v, %v", waiterPeer, waiterErr)
	}
}

// Only Tailscale's ranges arrive over the tailnet; anything else (a LAN host
// sending to the tailnet address, a local process on another interface) is
// refused before whois runs.
func TestDirectTailnetRefusesNonTailnetSources(t *testing.T) {
	h := newDirectHarnessWith(t, func(o *TailnetOptions) { o.peerPrefixes = nil })
	response, data := h.directDo(req{path: "/api/v0/hello"})
	expect(t, response, data, http.StatusUnauthorized, CodeUnauthenticated)
	if calls := h.fake.count(func(f *fakeTailnet) int { return f.whoisCalls }); calls != 0 {
		t.Fatalf("whois ran %d times for a non-tailnet source", calls)
	}
}

// A tagged node belongs to no person: it trusts only logins named in config.
func TestDirectTailnetTaggedNodeNeedsConfiguredLogins(t *testing.T) {
	h := newDirectHarness(t, func(f *fakeTailnet) { f.node.Tags = []string{"tag:server"} })
	status := h.s.TailnetStatus()
	if status.State != TailnetStateWaiting || !strings.Contains(status.Message, "api.tailnetLogins") || len(h.fake.listens) != 0 {
		t.Fatalf("status = %+v", status)
	}
	// Named logins compare without case, and are not pinned to the owner ID.
	h = newDirectHarnessWith(t, func(o *TailnetOptions) { o.Logins = []string{"Owner@Example.COM"} }, func(f *fakeTailnet) {
		f.node.Tags = []string{"tag:server"}
		peer := ownerPeer()
		peer.UserID = 7
		f.peers[loopbackPeer] = peer
	})
	if state := h.s.TailnetStatus().State; state != TailnetStateListening {
		t.Fatalf("state = %s", state)
	}
	response, data := h.directDo(req{path: "/api/v0/hello"})
	expect(t, response, data, http.StatusOK, "")
}

func (h *directHarness) openTerminal(t *testing.T) *websocket.Conn {
	t.Helper()
	conn, _, err := h.dial(t, http.Header{"Origin": {directOrigin()}})
	if err != nil {
		t.Fatal(err)
	}
	writeText(t, conn, `{"open":true}`)
	if got := readText(t, conn); got != `{"open":true}` {
		t.Fatalf("echo = %q", got)
	}
	return conn
}

// An open stream is re-admitted periodically: once the device stops
// qualifying, its terminal closes with 4401.
func TestDirectTailnetRecheckClosesStreamsThatNoLongerQualify(t *testing.T) {
	h := newDirectHarness(t)
	conn := h.openTerminal(t)
	h.fake.set(func(f *fakeTailnet) {
		peer := ownerPeer()
		peer.Tags = []string{"tag:server"}
		f.peers[loopbackPeer] = peer
	})
	h.clock.Advance(tailnetWhoisTTL + time.Second)
	if code, _ := closeStatus(t, conn); code != CloseUnauthenticated {
		t.Fatalf("stream from a device that became tagged: close %d, want %d", code, CloseUnauthenticated)
	}
}

// When who may connect changes (here the node's owner), streams admitted
// under the old rule close at once, without waiting for a recheck.
func TestDirectTailnetClosesStreamsWhenAllowedLoginsChange(t *testing.T) {
	h := newDirectHarnessWith(t, func(o *TailnetOptions) { o.recheckInterval = time.Hour })
	conn := h.openTerminal(t)
	h.fake.set(func(f *fakeTailnet) { f.node.OwnerID, f.node.OwnerLogin = 43, "new-owner@example.com" })
	if code, _ := closeStatus(t, conn); code != CloseUnauthenticated {
		t.Fatalf("stream after the owner changed: close %d, want %d", code, CloseUnauthenticated)
	}
}

type logRecorder struct {
	mu    sync.Mutex
	lines []string
}

func (r *logRecorder) logf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, fmt.Sprintf(format, args...))
}

func (r *logRecorder) all() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.lines, "\n")
}

func (r *logRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.lines)
}

// Any reachable peer can fail handshakes or be refused as often as it likes;
// none of that may grow the service log without bound.
func TestTailnetLogsAreBounded(t *testing.T) {
	recorder := &logRecorder{}
	clock := &fakeClock{now: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)}
	logs := newTailnetLogs(recorder.logf)
	logs.now = clock.Now
	for i := 0; i < 1000; i++ {
		_, _ = logs.Write([]byte("http: TLS handshake error from 100.64.0.9:5555: EOF\n"))
	}
	if recorder.count() != 0 {
		t.Fatalf("handshake errors were logged: %s", recorder.all())
	}
	for i := 0; i < 5; i++ {
		_, _ = logs.Write([]byte("http: Accept error: something\n"))
	}
	if recorder.count() != 1 {
		t.Fatalf("other errors not rate limited: %s", recorder.all())
	}
	logs.summary()
	if !strings.Contains(recorder.all(), "1000 TLS handshakes failed and 4 further server errors") {
		t.Fatalf("summary = %s", recorder.all())
	}
	before := recorder.count()
	logs.summary()
	if recorder.count() != before {
		t.Fatal("an empty summary was logged")
	}

	addr := netip.MustParseAddr("100.64.0.9")
	for i := 0; i < 100; i++ {
		logs.refused(addr, TailnetPeer{}, "whois failed")
	}
	if got := recorder.count() - before; got != 1 {
		t.Fatalf("refusals logged %d times in a minute", got)
	}
	clock.Advance(tailnetRefusalLogEvery + time.Second)
	logs.refused(addr, TailnetPeer{}, "whois failed")
	if got := recorder.count() - before; got != 2 {
		t.Fatalf("refusal after a minute: %d lines", got)
	}

	peer := ownerPeer()
	for i := 0; i < 10; i++ {
		logs.admitted(addr, peer)
	}
	if !strings.Contains(recorder.all(), "admitted owner@example.com on laptop (100.64.0.9)") || recorder.count()-before != 3 {
		t.Fatalf("admission log = %s", recorder.all())
	}
}
