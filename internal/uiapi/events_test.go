package uiapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/marcus/sidecar/internal/mobileproto"
)

type eventBackend struct {
	*fakeBackend
	changes        chan struct{}
	countsMu       sync.Mutex
	watches, calls int
	holder         *GeometryHolder
}

func (b *eventBackend) WatchCatalog(context.Context) (<-chan struct{}, error) {
	b.countsMu.Lock()
	b.watches++
	b.countsMu.Unlock()
	return b.changes, nil
}
func (b *eventBackend) Sessions(ctx context.Context, q mobileproto.CatalogQuery) (mobileproto.CatalogSnapshot, error) {
	b.countsMu.Lock()
	b.calls++
	b.countsMu.Unlock()
	return b.fakeBackend.Sessions(ctx, q)
}
func (b *eventBackend) GeometryHolder(context.Context, TerminalInfo) *GeometryHolder {
	b.countsMu.Lock()
	defer b.countsMu.Unlock()
	if b.holder == nil {
		return nil
	}
	h := *b.holder
	return &h
}
func (b *eventBackend) count() int { b.countsMu.Lock(); defer b.countsMu.Unlock(); return b.calls }
func (b *eventBackend) set(snapshot mobileproto.CatalogSnapshot) {
	b.mu.Lock()
	b.snapshot = snapshot
	b.mu.Unlock()
	notify(b.changes)
}
func eventsHarness(t *testing.T) (*harness, *eventBackend) {
	b := &eventBackend{fakeBackend: newFakeBackend(), changes: make(chan struct{}, 1)}
	h := newHarness(t, func(o *Options) { o.Backend = b })
	return h, b
}
func dialEvents(t *testing.T, h *harness, query string, headers http.Header, local bool) *websocket.Conn {
	t.Helper()
	opts := &websocket.DialOptions{HTTPHeader: headers}
	base := strings.Replace(h.s.BrowserURL(), "http:", "ws:", 1)
	if local {
		base = "ws://sidecar"
		opts.HTTPClient = unixClient(h.s.Endpoint().UnixSocket)
	}
	conn, _, err := websocket.Dial(context.Background(), base+eventsPath+query, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn
}
func readEvent(t *testing.T, c *websocket.Conn) EventMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	kind, data, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if kind != websocket.MessageText {
		t.Fatal("binary event")
	}
	var event EventMessage
	if err := json.Unmarshal(data, &event); err != nil {
		t.Fatal(err)
	}
	return event
}
func initialEvents(t *testing.T, c *websocket.Conn) {
	t.Helper()
	for i, kind := range []string{"hello", "catalog", "terminals"} {
		e := readEvent(t, c)
		if e.Type != kind || e.Seq != uint64(i+1) {
			t.Fatalf("initial %s: %+v", kind, e)
		}
		if kind == "terminals" && (e.Terminals == nil || len(*e.Terminals) != 0) {
			t.Fatalf("empty terminals must be []: %+v", e)
		}
	}
}
func snapshotRow(gen, state string, attention bool) mobileproto.CatalogSnapshot {
	return mobileproto.CatalogSnapshot{Generation: gen, Sections: []mobileproto.CatalogSection{{Rows: []mobileproto.CatalogRow{{ID: "row", DisplayName: "Build UI", Status: state, Live: true, Attention: attention}}}}}
}

func TestEventsCatalogTransitionsDebounceAndShutdown(t *testing.T) {
	h, b := eventsHarness(t)
	b.set(snapshotRow("g1", "working", false))
	c := dialEvents(t, h, "?sort=name&search=UI&host=local&show_idle_sessions=false", nil, true)
	initialEvents(t, c)
	b.mu.Lock()
	q := b.query
	b.mu.Unlock()
	if q.Sort != "name" || q.Search != "UI" || len(q.Hosts) != 1 || q.ShowIdleSessions == nil || *q.ShowIdleSessions {
		t.Fatalf("query not passed through: %+v", q)
	}
	// An invalidation of unchanged facts must not be delivered. A burst costs
	// one query, and no clock repeats the full catalog after it settles.
	time.Sleep(300 * time.Millisecond)
	before := b.count()
	for range 30 {
		notify(b.changes)
	}
	time.Sleep(600 * time.Millisecond)
	if calls := b.count(); calls != before+1 {
		t.Fatalf("burst collected %d times, wanted 1", calls-before)
	}
	time.Sleep(1100 * time.Millisecond)
	if b.count() != before+1 {
		t.Fatal("catalog polled without a change")
	}
	b.set(snapshotRow("g2", "blocked", true))
	e := readEvent(t, c)
	if e.Type != "catalog" || e.Seq != 4 || e.Catalog.Generation != "g2" {
		t.Fatalf("unchanged generation leaked or wrong update: %+v", e)
	}
	e = readEvent(t, c)
	if e.Type != "attention" || e.Seq != 5 || e.Attention.Kind != "needs_input" || e.Attention.CatalogID != "row" || e.Attention.Title != "Build UI" || e.Attention.Time.IsZero() {
		t.Fatalf("attention: %+v", e)
	}
	b.set(snapshotRow("g3", "working", false))
	e = readEvent(t, c)
	if e.Type != "catalog" || e.Seq != 6 {
		t.Fatalf("working: %+v", e)
	}
	b.set(snapshotRow("g4", "done", false))
	e = readEvent(t, c)
	if e.Type != "catalog" || e.Seq != 7 {
		t.Fatalf("done: %+v", e)
	}
	e = readEvent(t, c)
	if e.Type != "attention" || e.Attention.Kind != "finished" || e.Seq != 8 {
		t.Fatalf("finished: %+v", e)
	}
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		done <- h.s.Shutdown(ctx)
	}()
	e = readEvent(t, c)
	if e.Type != "shutdown" || e.Seq != 9 {
		t.Fatalf("shutdown: %+v", e)
	}
	code, _ := closeStatus(t, c)
	if code != CloseShuttingDown {
		t.Fatalf("shutdown code %d", code)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestEventsAttachmentsAndHolderChangesAreCredentialScoped(t *testing.T) {
	h, b := eventsHarness(t)
	token := h.pairBrowser()
	headers := http.Header{"Authorization": {"Bearer " + token}}
	c := dialEvents(t, h, "", headers, false)
	initialEvents(t, c)
	other, _ := h.s.clients.add("terminal", caller{listener: ListenerBrowser, client: "another"})
	other.observe([]byte(`{"type":"opened","target":{"session":"private","pane":"%2"}}`))
	defer h.s.clients.remove(other)
	resolved, _ := h.s.resolveBearer(token, "")
	resolved.listener = ListenerBrowser
	mine, _ := h.s.clients.add("terminal", resolved)
	defer h.s.clients.remove(mine)
	b.countsMu.Lock()
	b.holder = &GeometryHolder{Kind: "tui", Label: "TUI on aerie"}
	b.countsMu.Unlock()
	mine.observe([]byte(`{"type":"opened","target":{"session":"shared","pane":"%1","display_name":"Shell"}}`))
	e := readEvent(t, c)
	if e.Type != "terminals" || e.Terminals == nil || len(*e.Terminals) != 1 || (*e.Terminals)[0].Session != "shared" || (*e.Terminals)[0].Holder.Label != "TUI on aerie" {
		t.Fatalf("attachments: %+v", e)
	}
	b.countsMu.Lock()
	b.holder = &GeometryHolder{Kind: "mobile", Label: "iPhone"}
	b.countsMu.Unlock()
	e = readEvent(t, c)
	if e.Type != "terminals" || (*e.Terminals)[0].Holder.Label != "iPhone" {
		t.Fatalf("holder: %+v", e)
	}
	mine.observe([]byte(`{"type":"closed"}`))
	e = readEvent(t, c)
	if e.Type != "terminals" || e.Terminals == nil || len(*e.Terminals) != 0 {
		t.Fatalf("closed attachment: %+v", e)
	}
}

func TestEventsAuthAndQueryGuards(t *testing.T) {
	h, _ := eventsHarness(t)
	token := h.pairBrowser()
	app := "http://embed.example"
	paired := h.pairOrigin(app)
	for _, tc := range []struct {
		name, query string
		headers     http.Header
		code        websocket.StatusCode
	}{
		{"no origin", "", nil, CloseOriginRefused},
		{"unpaired", "", http.Header{"Origin": {h.ownOrigin()}}, CloseUnauthenticated},
		{"foreign origin", "", http.Header{"Authorization": {"Bearer " + token}, "Origin": {app}}, CloseOriginRefused},
		{"invalid query", "?sort=wrong", http.Header{"Authorization": {"Bearer " + token}}, CloseProtocolViolation},
		{"unknown query", "?every=1", http.Header{"Authorization": {"Bearer " + token}}, CloseProtocolViolation},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := dialEvents(t, h, tc.query, tc.headers, false)
			code, _ := closeStatus(t, c)
			if code != tc.code {
				t.Fatalf("close=%d want=%d", code, tc.code)
			}
		})
	}
	for _, bearer := range []string{token, paired} {
		c := dialEvents(t, h, "", http.Header{"Authorization": {"Bearer " + bearer}}, false)
		initialEvents(t, c)
		_ = c.CloseNow()
	}
	response, data := h.browserDo(req{method: "POST", path: "/api/v0/ws-tickets", body: "{}", header: mutationHeaders(app, map[string]string{"Authorization": "Bearer " + paired})})
	expect(t, response, data, 200, "")
	var ticket TicketResponse
	_ = json.Unmarshal(data, &ticket)
	c := dialEvents(t, h, "?ticket="+ticket.Ticket, http.Header{"Origin": {app}}, false)
	initialEvents(t, c)
	_ = c.CloseNow()
	c = dialEvents(t, h, "?ticket="+ticket.Ticket, http.Header{"Origin": {app}}, false)
	code, _ := closeStatus(t, c)
	if code != CloseUnauthenticated {
		t.Fatalf("reused ticket: %d", code)
	}
	opts := &websocket.DialOptions{HTTPClient: unixClient(h.s.Endpoint().TailnetSocket), HTTPHeader: http.Header{tailscaleLoginHead: {testTailnetLogin}, "Origin": {"https://" + testTailnetHost}}}
	c, _, err := websocket.Dial(context.Background(), "ws://"+testTailnetHost+eventsPath, opts)
	if err != nil {
		t.Fatal(err)
	}
	initialEvents(t, c)
	_ = c.CloseNow()
	opts.HTTPHeader.Del("Origin")
	c, _, err = websocket.Dial(context.Background(), "ws://"+testTailnetHost+eventsPath, opts)
	if err != nil {
		t.Fatal(err)
	}
	code, _ = closeStatus(t, c)
	if code != CloseOriginRefused {
		t.Fatalf("ambient tailnet missing origin: %d", code)
	}
	_, response, err = websocket.Dial(context.Background(), strings.Replace(h.s.BrowserURL(), "http:", "ws:", 1)+eventsPath, &websocket.DialOptions{Host: "evil.example"})
	if err == nil || response.StatusCode != 421 {
		t.Fatalf("Host guard: %v %+v", err, response)
	}
}

func TestEventsSourceSharedAndTerminalLimitIndependent(t *testing.T) {
	h, b := eventsHarness(t)
	token := h.pairBrowser()
	headers := http.Header{"Authorization": {"Bearer " + token}}
	c := dialEvents(t, h, "", headers, false)
	initialEvents(t, c)
	second := dialEvents(t, h, "", headers, false)
	initialEvents(t, second)
	b.countsMu.Lock()
	watches := b.watches
	b.countsMu.Unlock()
	if watches != 1 {
		t.Fatalf("%d observers for two clients", watches)
	}
	caller, _ := h.s.resolveBearer(token, "")
	caller.listener = ListenerBrowser
	for range maxTerminalsPerClient {
		if _, ok := h.s.clients.add("terminal", caller); !ok {
			t.Fatal("events used terminal slots")
		}
	}
	if _, ok := h.s.clients.add("terminal", caller); ok {
		t.Fatal("terminal limit lost")
	}
	b.set(snapshotRow("next", "working", false))
	for _, conn := range []*websocket.Conn{c, second} {
		e := readEvent(t, conn)
		if e.Type != "catalog" || e.Catalog.Generation != "next" {
			t.Fatalf("fanout: %+v", e)
		}
	}
}

func TestPendingEventsCoalesceSlowWriter(t *testing.T) {
	p := newEventPending()
	for i := range 100 {
		s := snapshotRow(string(rune(i+1)), "working", false)
		p.put(EventMessage{Type: "catalog", Catalog: &s})
		terms := []EventTerminal{{Session: s.Generation}}
		p.put(EventMessage{Type: "terminals", Terminals: &terms})
		p.put(EventMessage{Type: "attention", Attention: &AttentionEvent{Kind: "needs_input", CatalogID: "row", Title: s.Generation}})
	}
	out := p.take()
	if len(out) != 3 || out[0].Catalog.Generation != string(rune(100)) || out[1].Attention.Title != string(rune(100)) || (*out[2].Terminals)[0].Session != string(rune(100)) {
		t.Fatalf("pending not latest-wins: %+v", out)
	}
	if len(p.take()) != 0 {
		t.Fatal("pending repeated")
	}
}

func TestAttentionDoesNotReplayBaselineOrStaleRows(t *testing.T) {
	current := snapshotRow("initial", "blocked", true)
	if len(attentionChanges(nil, &current, time.Now())) != 0 {
		t.Fatal("initial attention replayed")
	}
	old := snapshotRow("old", "working", false)
	current.Sections[0].Rows[0].Stale = true
	if len(attentionChanges(&old, &current, time.Now())) != 0 {
		t.Fatal("stale attention emitted")
	}
	current = snapshotRow("same", "working", false)
	if len(attentionChanges(&old, &current, time.Now())) != 0 {
		t.Fatal("unchanged state alerted")
	}
}

func TestPendingAttentionOverflowIsExplicit(t *testing.T) {
	p := newEventPending()
	for i := range mobileproto.MaxCatalogRows*2 + 1 {
		p.put(EventMessage{Type: "attention", Attention: &AttentionEvent{CatalogID: fmt.Sprint(i), Kind: "needs_input"}})
	}
	out := p.take()
	if len(out) != mobileproto.MaxCatalogRows*2+1 || out[len(out)-1].Error == nil || out[len(out)-1].Error.Code != mobileproto.ErrorOverflow {
		t.Fatal("attention bound silently lost notifications")
	}
}

func TestEventsFixtureTranscript(t *testing.T) {
	data, err := os.ReadFile("../../testdata/ui-api/v0/events-stream.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	initial := []string{"hello", "catalog", "terminals"}
	var catalog *mobileproto.CatalogSnapshot
	for i, line := range lines {
		var m EventMessage
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&m); err != nil {
			t.Fatal(err)
		}
		if m.Seq != uint64(i+1) || m.APIVersion != APIVersion {
			t.Fatalf("fixture sequence/version: %+v", m)
		}
		if i < len(initial) && m.Type != initial[i] {
			t.Fatalf("fixture initial ordering: %s, want %s", m.Type, initial[i])
		}
		switch m.Type {
		case "hello":
			if i != 0 || m.APIInstance == "" {
				t.Fatal("hello must be first")
			}
		case "catalog":
			if m.Catalog == nil || m.Catalog.Generation == "" {
				t.Fatal("catalog missing")
			}
			catalog = m.Catalog
			for _, section := range catalog.Sections {
				for _, row := range section.Rows {
					if row.Path != "/workspace/demo" {
						t.Fatal("event catalog lost owner path")
					}
				}
			}
		case "attention":
			if m.Attention == nil || (m.Attention.Kind != "needs_input" && m.Attention.Kind != "finished") || m.Attention.CatalogID == "" || m.Attention.Time.IsZero() {
				t.Fatal("attention missing")
			}
			if catalog == nil || len(catalog.Sections) != 1 || len(catalog.Sections[0].Rows) != 1 {
				t.Fatal("attention must follow the current row's catalog")
			}
			row := catalog.Sections[0].Rows[0]
			if row.ID != m.Attention.CatalogID || (m.Attention.Kind == "needs_input" && !row.Attention) || (m.Attention.Kind == "finished" && row.Status != "done") {
				t.Fatal("fixture alert precedes its catalog transition")
			}
		case "terminals":
			if m.Terminals == nil {
				t.Fatal("terminals missing")
			}
		case "error":
			if m.Error == nil {
				t.Fatal("error missing")
			}
		case "shutdown":
			if i != len(lines)-1 {
				t.Fatal("shutdown must be last")
			}
		default:
			t.Fatalf("unknown event type %q", m.Type)
		}
	}
}

func TestPendingAttentionByteBoundAndReplacementAccounting(t *testing.T) {
	p := newEventPending()
	p.put(EventMessage{Type: "attention", Attention: &AttentionEvent{CatalogID: "row", Kind: "needs_input", Title: strings.Repeat("x", maxPendingAttentionBytes)}})
	out := p.take()
	if len(out) != 1 || out[0].Type != "error" || out[0].Error.Code != mobileproto.ErrorOverflow {
		t.Fatal("oversized attention not bounded")
	}
	for range 100 {
		p.put(EventMessage{Type: "attention", Attention: &AttentionEvent{CatalogID: "row", Kind: "needs_input", Title: strings.Repeat("x", 10000)}})
	}
	out = p.take()
	if len(out) != 1 || out[0].Type != "attention" {
		t.Fatal("replacements accumulated a byte budget")
	}
}

func TestEventsRevocationClosesOnlyRevokedCredential(t *testing.T) {
	for _, kind := range []string{"session", "origin"} {
		t.Run(kind, func(t *testing.T) {
			h, b := eventsHarness(t)
			token := h.pairBrowser()
			if kind == "origin" {
				token = h.pairOrigin("http://events.example")
			}
			c := dialEvents(t, h, "", http.Header{"Authorization": {"Bearer " + token}}, false)
			initialEvents(t, c)
			otherToken := h.pairOrigin("http://other-events.example")
			other := dialEvents(t, h, "", http.Header{"Authorization": {"Bearer " + otherToken}}, false)
			initialEvents(t, other)
			terminal, err := h.dialBrowser(t, "", http.Header{"Authorization": {"Bearer " + token}})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = terminal.CloseNow() }()
			writeText(t, terminal, `{"ready":true}`)
			readText(t, terminal)
			if kind == "session" {
				if result := h.revokeSessions(""); result.Revoked != 1 || result.TerminalsClosed != 1 {
					t.Fatalf("event stream counted as a terminal: %+v", result)
				}
			} else {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				if _, err := NewLocalClientForSocket(h.s.Endpoint()).RevokeOrigin(ctx, "http://events.example"); err != nil {
					t.Fatal(err)
				}
			}
			for _, conn := range []*websocket.Conn{c, terminal} {
				if code, _ := closeStatus(t, conn); code != CloseUnauthenticated {
					t.Fatalf("revoked %s stream close = %d, want 4401", kind, code)
				}
			}
			waitForClients(t, h, 1)
			b.set(snapshotRow("after-revocation", "working", false))
			if e := readEvent(t, other); e.Type != "catalog" || e.Catalog.Generation != "after-revocation" {
				t.Fatalf("other credential affected: %+v", e)
			}
		})
	}
}

func TestEventsTicketRevokedBetweenAuthorizationAndAdmission(t *testing.T) {
	h, b := eventsHarness(t)
	token := h.pairBrowser()
	response, data := h.browserDo(req{method: http.MethodPost, path: "/api/v0/ws-tickets", body: "{}", header: mutationHeaders(h.ownOrigin(), map[string]string{"Authorization": "Bearer " + token})})
	expect(t, response, data, http.StatusOK, "")
	var ticket TicketResponse
	if err := json.Unmarshal(data, &ticket); err != nil {
		t.Fatal(err)
	}
	// Hold admission after authorization redeems the ticket. Sign-out wins
	// under that same lock; the stale grant must not register or collect rows.
	h.s.credentialMu.Lock()
	locked := true
	defer func() {
		if locked {
			h.s.credentialMu.Unlock()
		}
	}()
	c := dialEvents(t, h, "?ticket="+ticket.Ticket, http.Header{"Origin": {h.ownOrigin()}}, false)
	deadline := time.Now().Add(2 * time.Second)
	for {
		h.s.auth.mu.Lock()
		remaining := len(h.s.auth.tickets)
		h.s.auth.mu.Unlock()
		if remaining == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("authorization did not redeem the ticket")
		}
		time.Sleep(time.Millisecond)
	}
	h.s.auth.revokeSessions("")
	h.s.clients.revoke(h.s.clients.sessionKeys(""))
	h.s.credentialMu.Unlock()
	locked = false
	if code, _ := closeStatus(t, c); code != CloseUnauthenticated {
		t.Fatalf("stale admission close = %d, want 4401", code)
	}
	if b.count() != 0 {
		t.Fatal("revoked grant collected catalog data")
	}
	waitForClients(t, h, 0)
}
