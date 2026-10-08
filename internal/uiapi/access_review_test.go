package uiapi

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	notification "github.com/marcus/sidecar/internal/notify"
)

// stalledRequest sends a request's headers and the first bytes of its body,
// leaving the handler authenticated and blocked reading the rest.
type stalledRequest struct {
	t    *testing.T
	conn net.Conn
	body string
}

func (h *harness) stallBrowserRequest(method, path, token, body string) *stalledRequest {
	h.t.Helper()
	addr := strings.TrimPrefix(h.s.BrowserURL(), "http://")
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { _ = conn.Close() })
	head := fmt.Sprintf("%s %s HTTP/1.1\r\nHost: %s\r\nOrigin: %s\r\nContent-Type: application/json\r\nX-Sidecar-Request: 1\r\nAuthorization: Bearer %s\r\nContent-Length: %d\r\n\r\n",
		method, path, addr, h.ownOrigin(), token, len(body))
	if _, err := conn.Write([]byte(head + body[:3])); err != nil {
		h.t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond) // dispatch has authenticated; the handler waits in decodeBody
	return &stalledRequest{t: h.t, conn: conn, body: body}
}

func (s *stalledRequest) finish() (int, string) {
	s.t.Helper()
	if _, err := s.conn.Write([]byte(s.body[3:])); err != nil {
		s.t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(s.conn), nil)
	if err != nil {
		s.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
}

// A browser session that is revoked while its approve (or deny) body is in
// flight must not complete it: admission is decided after the body arrives,
// under the revocation lock.
func TestAccessMutationsRecheckTheCallerAfterTheBody(t *testing.T) {
	h := newHarness(t)
	_, paired := h.pairBrowserKey()
	_, attacker := h.requestAccess("attacker's second key")
	_, other := h.requestAccess("someone else")
	approve := h.stallBrowserRequest(http.MethodPost, accessApprovePath, paired.Token, fmt.Sprintf(`{"code":%q}`, attacker.Code))
	deny := h.stallBrowserRequest(http.MethodPost, accessDenyPath, paired.Token, fmt.Sprintf(`{"request_id":%q}`, other.RequestID))

	r, b := h.localDo(req{method: http.MethodDelete, path: devicesPath + "/" + paired.RegistrationID})
	expect(t, r, b, http.StatusOK, "")

	for name, stalled := range map[string]*stalledRequest{"approve": approve, "deny": deny} {
		status, body := stalled.finish()
		if status != http.StatusUnauthorized || errorCode(t, []byte(body)) != CodeUnauthenticated {
			t.Fatalf("%s after revocation = %d %s", name, status, body)
		}
	}
	if status := h.accessStatus(attacker); status.Status != accessStatusPending {
		t.Fatalf("the revoked browser's approval landed: %+v", status)
	}
	if status := h.accessStatus(other); status.Status != accessStatusPending {
		t.Fatalf("the revoked browser's denial landed: %+v", status)
	}
	records, err := readSessions(h.s.auth.sessionPath)
	if err != nil || len(records) != 0 {
		t.Fatalf("registrations after revocation = %v (%v)", records, err)
	}
}

// A revoked session cannot revoke other devices with a request authorized
// before its revocation either.
func TestDeviceRevokeRechecksTheCaller(t *testing.T) {
	h := newHarness(t)
	_, first := h.pairBrowserKey()
	_, second := h.pairBrowserKey()
	// Revoke first's registration behind its back, then use its bearer as
	// though the request had been authorized just before.
	c, result := h.s.resolveBearer(first.Token, h.ownOrigin())
	if result != bearerOK {
		t.Fatal("bearer did not resolve")
	}
	r, b := h.localDo(req{method: http.MethodDelete, path: devicesPath + "/" + first.RegistrationID})
	expect(t, r, b, http.StatusOK, "")
	c.listener = ListenerBrowser
	recorder := &responseRecorder{header: http.Header{}}
	request, _ := http.NewRequest(http.MethodDelete, devicesPath+"/"+second.RegistrationID, nil)
	h.s.handleRevokeDevice(recorder, request, c)
	if recorder.status != http.StatusUnauthorized {
		t.Fatalf("revoked caller revoked a device: %d %s", recorder.status, recorder.body.String())
	}
	h.expectBearer(second.Token, http.StatusOK)
}

type responseRecorder struct {
	header http.Header
	status int
	body   strings.Builder
}

func (r *responseRecorder) Header() http.Header         { return r.header }
func (r *responseRecorder) Write(b []byte) (int, error) { return r.body.Write(b) }
func (r *responseRecorder) WriteHeader(status int)      { r.status = status }

// A non-stream request that stops sending its body is cut off by the read
// deadline; streams are not.
func TestNonStreamRequestsHaveABodyReadDeadline(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.RequestReadTimeout = 300 * time.Millisecond })
	token := h.pairBrowser()
	addr := strings.TrimPrefix(h.s.BrowserURL(), "http://")
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	head := fmt.Sprintf("POST %s HTTP/1.1\r\nHost: %s\r\nOrigin: %s\r\nContent-Type: application/json\r\nX-Sidecar-Request: 1\r\nAuthorization: Bearer %s\r\nContent-Length: 100\r\n\r\n{\"co", accessApprovePath, addr, h.ownOrigin(), token)
	if _, err := conn.Write([]byte(head)); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	start := time.Now()
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("no answer to a stalled body: %v", err)
	}
	data, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusRequestTimeout || errorCode(t, data) != CodeRequestTimeout {
		t.Fatalf("stalled body = %d %s", resp.StatusCode, data)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("the stalled request was held for %v", elapsed)
	}
	// An events stream outlives the deadline.
	conn2 := dialEvents(t, h, "", http.Header{"Authorization": {"Bearer " + token}}, false)
	initialEvents(t, conn2)
	time.Sleep(500 * time.Millisecond)
	h.requestAccess("after the deadline")
	if event := readEvent(t, conn2); event.Type != "access_requested" {
		t.Fatalf("stream event after the deadline = %+v", event)
	}
}

// Browsers open their events stream with a ticket. A ticket bought by a
// browser session is an approver's stream; one bought by a paired origin is
// not.
func TestTicketStreamsHearAccessRequestsOnlyForSessions(t *testing.T) {
	h, _ := eventsHarness(t)
	_, paired := h.pairBrowserKey()
	buy := func(token, origin string) string {
		r, data := h.browserDo(req{method: http.MethodPost, path: "/api/v0/ws-tickets", body: "{}", header: mutationHeaders(origin, map[string]string{"Authorization": "Bearer " + token})})
		expect(t, r, data, http.StatusOK, "")
		var ticket TicketResponse
		_ = json.Unmarshal(data, &ticket)
		return ticket.Ticket
	}
	session := dialEvents(t, h, "?ticket="+buy(paired.Token, h.ownOrigin()), http.Header{"Origin": {h.ownOrigin()}}, false)
	initialEvents(t, session)
	const app = "http://app.example:5173"
	appToken := h.pairOrigin(app)
	origin := dialEvents(t, h, "?ticket="+buy(appToken, app), http.Header{"Origin": {app}}, false)
	initialEvents(t, origin)
	_, created := h.requestAccess("Firefox")
	if event := readEvent(t, session); event.Type != "access_requested" || event.AccessRequest.RequestID != created.RequestID {
		t.Fatalf("session ticket stream = %+v", event)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, data, err := origin.Read(ctx); err == nil {
		t.Fatalf("a paired origin's ticket stream heard an access request: %s", data)
	}
	if !mayApprove(caller{auth: "ticket", client: "session:abc"}) || !mayApprove(caller{auth: "ticket", client: "tailnet:a@b"}) || mayApprove(caller{auth: "ticket", client: "origin:" + app}) {
		t.Fatal("ticket approver rule")
	}
}

// A paired origin can neither see nor settle the access notification.
func TestPairedOriginsCannotReadOrDismissAccessNotifications(t *testing.T) {
	h := newHarness(t)
	const app = "http://app.example:5173"
	appToken := h.pairOrigin(app)
	h.requestAccess("Safari")
	notes := h.accessNotifications()
	if len(notes) != 1 {
		t.Fatalf("notes = %+v", notes)
	}
	id := notes[0].ID
	appHeaders := func(mutation bool) map[string]string {
		headers := map[string]string{"Authorization": "Bearer " + appToken, "Origin": app}
		if mutation {
			headers["Content-Type"], headers["X-Sidecar-Request"] = "application/json", "1"
		}
		return headers
	}
	r, b := h.browserDo(req{path: notificationsPath, header: appHeaders(false)})
	expect(t, r, b, http.StatusOK, "")
	if strings.Contains(string(b), id) || strings.Contains(string(b), "access_request") {
		t.Fatalf("a paired origin read the access notification: %s", b)
	}
	r, b = h.browserDo(req{method: http.MethodPost, path: notificationDismissPath, body: `{"id":"` + id + `"}`, header: appHeaders(true)})
	expect(t, r, b, http.StatusNotFound, CodeNotFound)
	r, b = h.browserDo(req{method: http.MethodPost, path: notificationReadPath, body: `{"ids":["` + id + `"]}`, header: appHeaders(true)})
	expect(t, r, b, http.StatusOK, "")
	r, b = h.browserDo(req{method: http.MethodPost, path: notificationClaimPath, body: `{"id":"` + id + `"}`, header: appHeaders(true)})
	expect(t, r, b, http.StatusNotFound, CodeNotFound)
	notes = h.accessNotifications()
	if len(notes) != 1 || notes[0].Read() {
		t.Fatalf("a paired origin settled the access notification: %+v", notes)
	}
	// A browser session still sees it.
	token := h.pairBrowser()
	r, b = h.browserDo(req{path: notificationsPath, header: map[string]string{"Authorization": "Bearer " + token}})
	expect(t, r, b, http.StatusOK, "")
	if !strings.Contains(string(b), id) {
		t.Fatal("a browser session cannot see the access notification")
	}
}

// One sweep timer, armed for the earliest expiry, however many requests
// arrive; the notification store is not reopened while the note is live.
func TestAccessChurnCostsOneTimerAndNoStoreReopen(t *testing.T) {
	h := newHarness(t)
	_, pub := browserTestKey(t)
	first := h.requestAccessWithKey(pub, "x")
	for i := 0; i < 20; i++ {
		h.clock.Advance(accessReplaceInterval)
		h.requestAccessWithKey(pub, "x")
		h.requestAccess("y")
	}
	if n := h.s.auth.pendingAccessCount(); n != 2 {
		t.Fatalf("pending = %d", n)
	}
	h.s.sweepMu.Lock()
	armed, at := h.s.sweepTimer != nil, h.s.sweepAt
	h.s.sweepMu.Unlock()
	if !armed || !at.Equal(first.ExpiresAt) {
		t.Fatalf("sweep timer armed=%v at %v, want the first expiry %v", armed, at, first.ExpiresAt)
	}
	// The note is assumed live between rechecks: dismissing it by hand is
	// noticed at the next recheck, not on every request.
	notes := h.accessNotifications()
	if len(notes) != 1 {
		t.Fatalf("notes = %d", len(notes))
	}
	store, err := notification.Open(h.state)
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Dismiss(notes[0].ID)
	_ = store.Close()
	h.s.accessMu.Lock()
	h.s.accessNoteChecked = h.clock.Now()
	h.s.accessMu.Unlock()
	h.requestAccess("z")
	if len(h.accessNotifications()) != 0 {
		t.Fatal("the store was consulted before the recheck interval")
	}
	h.clock.Advance(accessNoteRecheck)
	h.requestAccess("w")
	if len(h.accessNotifications()) != 1 {
		t.Fatal("a dismissed note was not reposted after the recheck interval")
	}
}

// A request that lands between a change and the withdrawal keeps its
// notification: the pending check is made under accessMu.
func TestAccessWithdrawalRechecksPending(t *testing.T) {
	h := newHarness(t)
	_, first := h.requestAccess("a")
	r, b := h.localApprove(first.Code)
	expect(t, r, b, http.StatusOK, "")
	if len(h.accessNotifications()) != 0 {
		t.Fatal("not withdrawn")
	}
	h.requestAccess("b")
	// A late withdrawal from the earlier change must not take the new
	// request's notification.
	h.s.afterAccessChange()
	if len(h.accessNotifications()) != 1 {
		t.Fatal("a late withdrawal dismissed a pending request's notification")
	}
	h.s.withdrawAccessNotification(true)
	if len(h.accessNotifications()) != 0 {
		t.Fatal("forced withdrawal")
	}
}

func TestBlankLabelsAreEmpty(t *testing.T) {
	for _, in := range []string{"\u3164\u3164", "a\u034f", "\u034f\u034f", " \u115f\u1160 ", "\u2800", "\ufeff\u200b", "\u0301"} {
		got := cleanDeviceLabel(in)
		if in == "a\u034f" {
			if got != in {
				t.Fatalf("%q -> %q", in, got)
			}
			continue
		}
		if got != "" {
			t.Fatalf("blank label %q -> %q", in, got)
		}
	}
	for in, want := range map[string]string{"a\u202eb": "ab", "a\x1b[31mb": "a[31mb", "a\u0085b": "a b", "a\U000E0041b": "ab", "Safari\u3164on iPad": "Safari on iPad"} {
		if got := cleanDeviceLabel(in); got != want {
			t.Fatalf("%q -> %q, want %q", in, got, want)
		}
	}
}

func TestMultipleWaitingWarning(t *testing.T) {
	if MultipleWaiting(1) != "" || !strings.Contains(MultipleWaiting(2), "2 browsers are waiting; make sure the code is the one on your screen") {
		t.Fatal("multiple-waiting warning")
	}
}
