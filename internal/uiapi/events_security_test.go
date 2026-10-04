package uiapi

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Stall the actual server-side socket write, rather than depending on kernel
// buffer sizes to simulate a peer that stopped reading.
type eventStalledConn struct {
	net.Conn
	entered   chan struct{}
	closed    chan struct{}
	once      sync.Once
	closeOnce sync.Once
}

func (c *eventStalledConn) Write(p []byte) (int, error) {
	if len(p) > 4096 {
		c.once.Do(func() { close(c.entered) })
		<-c.closed
		return 0, net.ErrClosed
	}
	return c.Conn.Write(p)
}

func (c *eventStalledConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

type eventStalledListener struct {
	net.Listener
	entered, closed chan struct{}
}

func (l eventStalledListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &eventStalledConn{Conn: c, entered: l.entered, closed: l.closed}, nil
}

func TestEventsRevocationInterruptsStalledWrite(t *testing.T) {
	h, b := eventsHarness(t)
	token := h.pairBrowser()
	server := httptest.NewUnstartedServer(&listenerHandler{s: h.s, kind: ListenerBrowser})
	entered, closed := make(chan struct{}), make(chan struct{})
	server.Listener = eventStalledListener{Listener: server.Listener, entered: entered, closed: closed}
	h.s.browserHosts[server.Listener.Addr().String()] = true
	server.Start()
	t.Cleanup(server.Close)
	conn, _, err := websocket.Dial(context.Background(), strings.Replace(server.URL, "http:", "ws:", 1)+eventsPath, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + token}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	initialEvents(t, conn)
	snapshot := snapshotRow("large", "working", false)
	row := snapshot.Sections[0].Rows[0]
	snapshot.Sections[0].Rows = nil
	for i := range 64 {
		row.ID = "row-" + strconv.Itoa(i)
		snapshot.Sections[0].Rows = append(snapshot.Sections[0].Rows, row)
	}
	b.set(snapshot)
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("socket write did not stall")
	}
	h.revokeSessions("")
	// Close has a five-second control-write bound. Revocation must interrupt
	// the data writer independently, not wait for its fifteen-second timeout.
	select {
	case <-closed:
	case <-time.After(7 * time.Second):
		t.Fatal("revocation left the event socket writing under a revoked credential")
	}
	waitForClients(t, h, 0)
}

func TestEventsRejectAmbientAndMisboundCredentials(t *testing.T) {
	h, _ := eventsHarness(t)
	token := h.pairBrowser()
	app := "http://events-attacker.example"
	h.pairOrigin(app)
	takeTicket := func() string {
		r, data := h.browserDo(req{method: http.MethodPost, path: "/api/v0/ws-tickets", body: "{}", header: mutationHeaders(h.ownOrigin(), map[string]string{"Authorization": "Bearer " + token})})
		expect(t, r, data, 200, "")
		var ticket TicketResponse
		if err := json.Unmarshal(data, &ticket); err != nil {
			t.Fatal(err)
		}
		return ticket.Ticket
	}
	for _, tc := range []struct {
		name, query string
		headers     http.Header
		code        websocket.StatusCode
	}{
		{"basic without Origin", "", http.Header{"Authorization": {"Basic dXNlcjpwYXNz"}}, CloseOriginRefused},
		{"empty bearer without Origin", "", http.Header{"Authorization": {"Bearer "}}, CloseOriginRefused},
		{"forged tailnet header on Browser", "", http.Header{"Origin": {h.ownOrigin()}, tailscaleLoginHead: {testTailnetLogin}}, CloseUnauthenticated},
		{"ticket without Origin", "?ticket=" + takeTicket(), nil, CloseOriginRefused},
		{"ticket plus bearer without Origin", "?ticket=" + takeTicket(), http.Header{"Authorization": {"Bearer " + token}}, CloseOriginRefused},
		{"ticket with another allowed Origin", "?ticket=" + takeTicket(), http.Header{"Origin": {app}}, CloseOriginRefused},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := dialEvents(t, h, tc.query, tc.headers, false)
			if code, _ := closeStatus(t, c); code != tc.code {
				t.Fatalf("close=%d want=%d", code, tc.code)
			}
		})
	}
	expired := takeTicket()
	h.clock.Advance(31 * time.Second)
	c := dialEvents(t, h, "?ticket="+expired, http.Header{"Origin": {h.ownOrigin()}}, false)
	if code, _ := closeStatus(t, c); code != CloseUnauthenticated {
		t.Fatalf("expired ticket close=%d", code)
	}
	spent := takeTicket()
	c = dialEvents(t, h, "?ticket="+spent, http.Header{"Origin": {h.ownOrigin()}}, false)
	initialEvents(t, c)
	_ = c.CloseNow()
	terminal, err := h.dialBrowser(t, "?ticket="+spent, http.Header{"Origin": {h.ownOrigin()}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = terminal.CloseNow() })
	if code, _ := closeStatus(t, terminal); code != CloseUnauthenticated {
		t.Fatalf("events ticket replayed on terminal: %d", code)
	}
	for _, headers := range []http.Header{
		{"Origin": {"https://" + testTailnetHost}},
		{"Origin": {"https://" + testTailnetHost}, tailscaleLoginHead: {"attacker@example.com"}},
		{"Origin": {"https://" + testTailnetHost}, "Authorization": {"Bearer " + token}},
	} {
		c, _, err := websocket.Dial(context.Background(), "ws://"+testTailnetHost+eventsPath, &websocket.DialOptions{HTTPClient: h.tailnet, HTTPHeader: headers})
		if err != nil {
			t.Fatal(err)
		}
		if code, _ := closeStatus(t, c); code != CloseUnauthenticated {
			t.Fatalf("tailnet accepted headers %v: %d", headers, code)
		}
		_ = c.CloseNow()
	}
}

func TestEventsPerCredentialLimitOnBrowserAndTailnet(t *testing.T) {
	for _, listener := range []Listener{ListenerBrowser, ListenerTailnet} {
		t.Run(string(listener), func(t *testing.T) {
			h, _ := eventsHarness(t)
			token := h.pairBrowser()
			headers := http.Header{"Authorization": {"Bearer " + token}}
			base := strings.Replace(h.s.BrowserURL(), "http:", "ws:", 1)
			opts := &websocket.DialOptions{HTTPHeader: headers}
			if listener == ListenerTailnet {
				base = "ws://" + testTailnetHost
				opts = &websocket.DialOptions{HTTPClient: h.tailnet, HTTPHeader: http.Header{"Origin": {"https://" + testTailnetHost}, tailscaleLoginHead: {testTailnetLogin}}}
			}
			var conns []*websocket.Conn
			t.Cleanup(func() {
				for _, c := range conns {
					_ = c.CloseNow()
				}
			})
			dial := func() *websocket.Conn {
				c, _, err := websocket.Dial(context.Background(), base+eventsPath, opts)
				if err != nil {
					t.Fatal(err)
				}
				conns = append(conns, c)
				return c
			}
			for range maxTerminalsPerClient {
				initialEvents(t, dial())
			}
			if code, _ := closeStatus(t, dial()); code != CloseTooManyTerminals {
				t.Fatalf("17th events socket close=%d", code)
			}
			_ = conns[0].Close(websocket.StatusNormalClosure, "")
			waitForClients(t, h, maxTerminalsPerClient-1)
			initialEvents(t, dial())
		})
	}
}
