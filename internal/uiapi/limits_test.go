package uiapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestKeepaliveDropsHalfOpenConnections(t *testing.T) {
	h := newHarness(t, func(o *Options) {
		o.KeepaliveInterval = 40 * time.Millisecond
		o.KeepaliveTimeout = 80 * time.Millisecond
	})
	// A live client answers pings because it is reading.
	live := h.dialLocal(t)
	defer func() { _ = live.CloseNow() }()
	messages := make(chan string, 4)
	go func() {
		for {
			_, data, err := live.Read(context.Background())
			if err != nil {
				close(messages)
				return
			}
			messages <- string(data)
		}
	}()
	// A half-open client never reads, so it never answers a ping.
	silent := h.dialLocal(t)
	defer func() { _ = silent.CloseNow() }()
	select {
	case <-h.backend.eof:
	case <-time.After(5 * time.Second):
		t.Fatal("a peer that stopped answering pings kept its stream")
	}
	time.Sleep(300 * time.Millisecond) // several more intervals
	if err := live.Write(context.Background(), websocket.MessageText, []byte(`{"alive":true}`)); err != nil {
		t.Fatalf("live client was dropped: %v", err)
	}
	select {
	case got, ok := <-messages:
		if !ok || got != `{"alive":true}` {
			t.Fatalf("live echo = %q, %v", got, ok)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("live client got no echo")
	}
	if clients := h.s.status().Clients; len(clients) != 1 {
		t.Fatalf("clients after keepalive = %+v", clients)
	}
}

func TestTerminalsPerClientAreCapped(t *testing.T) {
	h := newHarness(t)
	const app = "http://app.example:5173"
	token := h.pairOrigin(app)
	header := http.Header{"Origin": {app}, "Authorization": {"Bearer " + token}}
	var open []*websocket.Conn
	defer func() {
		for _, conn := range open {
			_ = conn.CloseNow()
		}
	}()
	for i := 0; i < maxTerminalsPerClient; i++ {
		conn, err := h.dialBrowser(t, "", header)
		if err != nil {
			t.Fatal(err)
		}
		open = append(open, conn)
	}
	waitForClients(t, h, maxTerminalsPerClient)
	h.expectBrowserClose(t, "one terminal too many", "", header, websocket.StatusCode(4429))
	// Another client is not affected, and neither is Local.
	other := "http://second.example"
	otherToken := h.pairOrigin(other)
	conn, err := h.dialBrowser(t, "", http.Header{"Origin": {other}, "Authorization": {"Bearer " + otherToken}})
	if err != nil {
		t.Fatal(err)
	}
	open = append(open, conn)
	for i := 0; i <= maxTerminalsPerClient; i++ {
		open = append(open, h.dialLocal(t))
	}
	waitForClients(t, h, 2*maxTerminalsPerClient+2)
	// Closing one frees its slot.
	_ = open[0].Close(websocket.StatusNormalClosure, "")
	waitForClients(t, h, 2*maxTerminalsPerClient+1)
	conn, err = h.dialBrowser(t, "", header)
	if err != nil {
		t.Fatal(err)
	}
	open = append(open, conn)
	waitForClients(t, h, 2*maxTerminalsPerClient+2)
}

func waitForClients(t *testing.T, h *harness, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := len(h.s.status().Clients)
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("clients = %d, want %d", got, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestTicketsPerClientAreCapped(t *testing.T) {
	h := newHarness(t)
	const app = "http://app.example:5173"
	token := h.pairOrigin(app)
	take := func(origin, token string) (*http.Response, []byte) {
		return h.browserDo(req{method: http.MethodPost, path: "/api/v0/ws-tickets", body: "{}", header: mutationHeaders(origin, map[string]string{"Authorization": "Bearer " + token})})
	}
	var first TicketResponse
	for i := 0; i < maxTicketsPerClient; i++ {
		response, data := take(app, token)
		expect(t, response, data, http.StatusOK, "")
		if i == 0 {
			_ = json.Unmarshal(data, &first)
		}
	}
	response, data := take(app, token)
	expect(t, response, data, http.StatusTooManyRequests, CodeTooMany)
	// One origin holding its whole allowance leaves the shared pool usable.
	other := "http://second.example"
	otherToken := h.pairOrigin(other)
	response, data = take(other, otherToken)
	expect(t, response, data, http.StatusOK, "")
	session := h.pairBrowser()
	response, data = take(h.ownOrigin(), session)
	expect(t, response, data, http.StatusOK, "")
	// Redeeming a ticket frees its slot; so does expiry.
	conn, err := h.dialBrowser(t, "?ticket="+first.Ticket, http.Header{"Origin": {app}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseNow() }()
	waitForClients(t, h, 1)
	response, data = take(app, token)
	expect(t, response, data, http.StatusOK, "")
	response, data = take(app, token)
	expect(t, response, data, http.StatusTooManyRequests, CodeTooMany)
	h.clock.Advance(31 * time.Second)
	response, data = take(app, token)
	expect(t, response, data, http.StatusOK, "")
}
