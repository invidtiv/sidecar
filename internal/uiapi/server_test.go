package uiapi

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/marcus/sidecar/internal/mobileproto"
)

const (
	testTailnetHost  = "node.example.ts.net"
	testTailnetLogin = "owner@example.com"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// fakeBackend echoes request lines and lets a request name how the stream
// ends, so the bridge can be tested without tmux.
type fakeBackend struct {
	mu        sync.Mutex
	query     mobileproto.CatalogQuery
	snapshot  mobileproto.CatalogSnapshot
	err       error
	eof       chan struct{}
	ctxEnded  chan struct{}
	stalled   chan struct{} // a stall request has stopped the backend reading
	release   chan struct{} // closing it ends every stall
	terminals int
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{eof: make(chan struct{}, 8), ctxEnded: make(chan struct{}, 8), stalled: make(chan struct{}, 8), release: make(chan struct{}),
		snapshot: mobileproto.CatalogSnapshot{HubID: "hub-<test>&", Generation: "g1"}}
}

func (b *fakeBackend) Sessions(_ context.Context, query mobileproto.CatalogQuery) (mobileproto.CatalogSnapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.query = query
	return b.snapshot, b.err
}

func (b *fakeBackend) ServeTerminal(ctx context.Context, input io.Reader, output io.Writer) error {
	b.mu.Lock()
	b.terminals++
	b.mu.Unlock()
	lines := make(chan string)
	go func() {
		scanner := bufio.NewScanner(input)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()
	for {
		select {
		case <-ctx.Done():
			notify(b.ctxEnded)
			return ctx.Err()
		case line, ok := <-lines:
			if !ok {
				notify(b.eof)
				return nil
			}
			switch line {
			case `{"cmd":"end"}`:
				return nil
			case `{"cmd":"stall"}`:
				// Stop reading requests, as a service busy with one does.
				notify(b.stalled)
				select {
				case <-b.release:
				case <-ctx.Done():
					notify(b.ctxEnded)
					return ctx.Err()
				}
				continue
			case `{"cmd":"fail"}`:
				return errors.New("backend exploded")
			case `{"cmd":"protocol"}`:
				_, _ = io.WriteString(output, `{"version":0,"type":"error","error":{"code":"invalid_request","message":"bad request"}}`+"\n")
				return errors.New("invalid request")
			}
			if _, err := io.WriteString(output, line+"\n"); err != nil {
				return err
			}
		}
	}
}

// notify records an event without blocking a backend whose test stopped
// counting.
func notify(events chan struct{}) {
	select {
	case events <- struct{}{}:
	default:
	}
}

type harness struct {
	t       *testing.T
	s       *Server
	backend *fakeBackend
	clock   *fakeClock
	local   *http.Client
	tailnet *http.Client
	browser *http.Client
	state   string
}

func shortTempDir(t *testing.T) string {
	t.Helper()
	// Unix socket paths are bounded; t.TempDir nests too deep on macOS.
	dir, err := os.MkdirTemp("", "uiapi")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func newHarness(t *testing.T, mutate ...func(*Options)) *harness {
	t.Helper()
	h := &harness{t: t, backend: newFakeBackend(), clock: &fakeClock{now: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)}, state: shortTempDir(t)}
	opts := Options{StateDir: h.state, Port: 0, Backend: h.backend, Version: "test", Now: h.clock.Now,
		Tailnet: &TailnetOptions{Host: testTailnetHost, Logins: []string{testTailnetLogin}}}
	for _, fn := range mutate {
		fn(&opts)
	}
	s, err := Start(opts)
	if err != nil {
		t.Fatal(err)
	}
	h.s = s
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	})
	h.local = unixClient(s.Endpoint().UnixSocket)
	if s.Endpoint().TailnetSocket != "" {
		h.tailnet = unixClient(s.Endpoint().TailnetSocket)
	}
	h.browser = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return h
}

func unixClient(path string) *http.Client {
	return &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (h *harness) ownOrigin() string { return h.s.BrowserURL() }

type req struct {
	method string
	path   string
	host   string
	body   string
	header map[string]string
}

func (h *harness) do(client *http.Client, base string, r req) (*http.Response, []byte) {
	h.t.Helper()
	if r.method == "" {
		r.method = http.MethodGet
	}
	var body io.Reader
	if r.body != "" {
		body = strings.NewReader(r.body)
	}
	request, err := http.NewRequest(r.method, base+r.path, body)
	if err != nil {
		h.t.Fatal(err)
	}
	if r.host != "" {
		request.Host = r.host
	}
	for key, value := range r.header {
		request.Header.Set(key, value)
	}
	response, err := client.Do(request)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	data, _ := io.ReadAll(response.Body)
	return response, data
}

func (h *harness) browserDo(r req) (*http.Response, []byte) {
	return h.do(h.browser, h.s.BrowserURL(), r)
}

func (h *harness) localDo(r req) (*http.Response, []byte) {
	return h.do(h.local, "http://sidecar.local", r)
}

func (h *harness) tailnetDo(r req) (*http.Response, []byte) {
	if r.host == "" {
		r.host = testTailnetHost
	}
	return h.do(h.tailnet, "http://sidecar.local", r)
}

func errorCode(t *testing.T, data []byte) string {
	t.Helper()
	var body ErrorBody
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("not an error body: %q", data)
	}
	return body.Error.Code
}

func expect(t *testing.T, response *http.Response, data []byte, status int, code string) {
	t.Helper()
	if response.StatusCode != status {
		t.Fatalf("status = %d, want %d; body %s", response.StatusCode, status, data)
	}
	if code != "" {
		if got := errorCode(t, data); got != code {
			t.Fatalf("code = %q, want %q; body %s", got, code, data)
		}
	}
}

// pairBrowser does what the pairing page's script does and returns the
// browser session token.
func (h *harness) pairBrowser() string {
	h.t.Helper()
	code := h.pairingCode("/")
	response, data := h.exchange(h.ownOrigin(), fragmentValue(h.t, code.URL, "code"), "/")
	expect(h.t, response, data, http.StatusOK, "")
	var exchanged PairingExchange
	if err := json.Unmarshal(data, &exchanged); err != nil || exchanged.Token == "" {
		h.t.Fatalf("exchange = %s (%v)", data, err)
	}
	return exchanged.Token
}

// exchange posts a pairing code as the pairing page would from origin.
func (h *harness) exchange(origin, code, next string) (*http.Response, []byte) {
	h.t.Helper()
	_, public := browserTestKey(h.t)
	body, _ := json.Marshal(PairingExchangeRequest{Code: code, Next: next, PublicKey: public})
	return h.browserDo(req{method: http.MethodPost, path: "/api/v0/pairing/exchange", body: string(body), header: mutationHeaders(origin, nil)})
}

func fragmentValue(t *testing.T, link, key string) string {
	t.Helper()
	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	values, err := url.ParseQuery(parsed.Fragment)
	if err != nil {
		t.Fatal(err)
	}
	return values.Get(key)
}

func (h *harness) pairingCode(next string) PairingCode {
	h.t.Helper()
	body, _ := json.Marshal(map[string]string{"next": next})
	response, data := h.localDo(req{method: http.MethodPost, path: "/api/v0/pairing/codes", body: string(body)})
	expect(h.t, response, data, http.StatusOK, "")
	var code PairingCode
	if err := json.Unmarshal(data, &code); err != nil {
		h.t.Fatal(err)
	}
	return code
}

func (h *harness) pairOrigin(origin string) string {
	h.t.Helper()
	response, data := h.localDo(req{method: http.MethodPost, path: "/api/v0/origins", body: `{"origin":"` + origin + `"}`})
	expect(h.t, response, data, http.StatusOK, "")
	var registration OriginRegistration
	if err := json.Unmarshal(data, &registration); err != nil {
		h.t.Fatal(err)
	}
	return registration.Token
}

func mutationHeaders(origin string, extra map[string]string) map[string]string {
	headers := map[string]string{"Origin": origin, "Content-Type": "application/json", "X-Sidecar-Request": "1"}
	for key, value := range extra {
		headers[key] = value
	}
	return headers
}

func TestHostGuardRefusesForeignHosts(t *testing.T) {
	h := newHarness(t)
	response, data := h.browserDo(req{path: "/api/v0/hello", host: "evil.example:80"})
	expect(t, response, data, http.StatusMisdirectedRequest, CodeHostRefused)
	session := h.pairBrowser()
	port := strings.TrimPrefix(h.s.BrowserURL(), "http://127.0.0.1")
	for _, host := range []string{"127.0.0.1" + port, "localhost" + port} {
		response, data = h.browserDo(req{path: "/api/v0/hello", host: host, header: map[string]string{"Authorization": "Bearer " + session}})
		expect(t, response, data, http.StatusOK, "")
	}
	response, data = h.tailnetDo(req{path: "/api/v0/hello", host: "127.0.0.1", header: map[string]string{tailscaleLoginHead: testTailnetLogin}})
	expect(t, response, data, http.StatusMisdirectedRequest, CodeHostRefused)
	for _, host := range []string{testTailnetHost, testTailnetHost + ":443"} {
		response, data = h.tailnetDo(req{path: "/api/v0/hello", host: host, header: map[string]string{tailscaleLoginHead: testTailnetLogin}})
		expect(t, response, data, http.StatusOK, "")
	}
	// The Local listener has no guards: curl --unix-socket sends any Host.
	response, data = h.localDo(req{path: "/api/v0/hello", host: "evil.example"})
	expect(t, response, data, http.StatusOK, "")
}

func TestOriginGuard(t *testing.T) {
	h := newHarness(t)
	session := h.pairBrowser()
	auth := map[string]string{"Authorization": "Bearer " + session}
	response, data := h.browserDo(req{path: "/api/v0/hello", header: map[string]string{"Authorization": "Bearer " + session, "Origin": "http://evil.example"}})
	expect(t, response, data, http.StatusForbidden, CodeOriginRefused)
	if response.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("refused origin received CORS headers")
	}
	// A mutation with no Origin needs a bearer token: nothing ambient can
	// carry it, so Node, curl and native clients may omit Origin.
	headers := mutationHeaders("", nil)
	delete(headers, "Origin")
	response, data = h.browserDo(req{method: http.MethodPost, path: "/api/v0/ws-tickets", body: "{}", header: headers})
	expect(t, response, data, http.StatusForbidden, CodeOriginRefused)
	headers = mutationHeaders("", auth)
	delete(headers, "Origin")
	response, data = h.browserDo(req{method: http.MethodPost, path: "/api/v0/ws-tickets", body: "{}", header: headers})
	expect(t, response, data, http.StatusOK, "")
	// The token is still validated; an invalid one gets no further.
	headers = mutationHeaders("", map[string]string{"Authorization": "Bearer forged"})
	delete(headers, "Origin")
	response, data = h.browserDo(req{method: http.MethodPost, path: "/api/v0/ws-tickets", body: "{}", header: headers})
	expect(t, response, data, http.StatusUnauthorized, CodeUnauthenticated)
	response, data = h.browserDo(req{method: http.MethodPost, path: "/api/v0/ws-tickets", body: "{}", header: mutationHeaders(h.ownOrigin(), auth)})
	expect(t, response, data, http.StatusOK, "")
	// The pairing exchange has no bearer token, so it always needs its Origin.
	code := h.pairingCode("/")
	headers = mutationHeaders("", nil)
	delete(headers, "Origin")
	response, data = h.browserDo(req{method: http.MethodPost, path: "/api/v0/pairing/exchange", body: `{"code":"` + code.Code + `"}`, header: headers})
	expect(t, response, data, http.StatusForbidden, CodeOriginRefused)
}

// Node's WebSocket and other non-browser clients send no Origin. With a
// bearer token that is fine in both HTTP and on the upgrade; a ticket alone is
// origin-bound and still needs one, and a present Origin must still match.
func TestBearerClientsMayOmitOrigin(t *testing.T) {
	h := newHarness(t)
	const app = "http://app.example:5173"
	token := h.pairOrigin(app)
	session := h.pairBrowser()
	for name, bearer := range map[string]string{"paired origin token": token, "session token": session} {
		response, data := h.browserDo(req{path: "/api/v0/status", header: map[string]string{"Authorization": "Bearer " + bearer}})
		expect(t, response, data, http.StatusOK, "")
		conn, err := h.dialBrowser(t, "", http.Header{"Authorization": {"Bearer " + bearer}})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		writeText(t, conn, `{"node":true}`)
		if got := readText(t, conn); got != `{"node":true}` {
			t.Fatalf("%s: echo = %q", name, got)
		}
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}
	waitForClients(t, h, 0)
	h.expectBrowserClose(t, "no origin, forged token", "", http.Header{"Authorization": {"Bearer forged"}}, CloseUnauthenticated)
	h.expectBrowserClose(t, "present origin that does not match", "", http.Header{"Authorization": {"Bearer " + token}, "Origin": {"http://evil.example"}}, CloseOriginRefused)
	response, data := h.browserDo(req{method: http.MethodPost, path: "/api/v0/ws-tickets", body: "{}", header: mutationHeaders(app, map[string]string{"Authorization": "Bearer " + token})})
	expect(t, response, data, http.StatusOK, "")
	var issued TicketResponse
	_ = json.Unmarshal(data, &issued)
	h.expectBrowserClose(t, "ticket without origin", "?ticket="+issued.Ticket, nil, CloseOriginRefused)
	h.expectBrowserClose(t, "ticket and bearer without origin", "?ticket="+issued.Ticket, http.Header{"Authorization": {"Bearer " + token}}, CloseOriginRefused)
	// Only Authorization: Bearer with a token earns the relaxation; any other
	// scheme, or an empty token, still needs an Origin.
	for _, authorization := range []string{"Basic " + token, "Token " + token, "Bearer", "Bearer   ", token} {
		headers := mutationHeaders("", map[string]string{"Authorization": authorization})
		delete(headers, "Origin")
		response, data := h.browserDo(req{method: http.MethodPost, path: "/api/v0/ws-tickets", body: "{}", header: headers})
		expect(t, response, data, http.StatusForbidden, CodeOriginRefused)
		h.expectBrowserClose(t, "upgrade with "+authorization, "", http.Header{"Authorization": {authorization}}, CloseOriginRefused)
	}
	// The Tailnet login is ambient, so the relaxation does not reach it.
	login := map[string]string{tailscaleLoginHead: testTailnetLogin, "Authorization": "Bearer " + token}
	headers := mutationHeaders("", login)
	delete(headers, "Origin")
	response, data = h.tailnetDo(req{method: http.MethodPost, path: "/api/v0/ws-tickets", body: "{}", header: headers})
	expect(t, response, data, http.StatusForbidden, CodeOriginRefused)
	conn := h.dialTailnet(t, http.Header{tailscaleLoginHead: {testTailnetLogin}, "Authorization": {"Bearer " + token}})
	if got, _ := closeStatus(t, conn); got != CloseOriginRefused {
		t.Fatalf("tailnet upgrade without origin closed %d", got)
	}
}

func TestMutationGuardRequiresJSONAndHeader(t *testing.T) {
	h := newHarness(t)
	auth := map[string]string{"Authorization": "Bearer " + h.pairBrowser()}
	for name, headers := range map[string]map[string]string{
		"missing header": {"Origin": h.ownOrigin(), "Content-Type": "application/json", "Authorization": auth["Authorization"]},
		"form post":      {"Origin": h.ownOrigin(), "Content-Type": "application/x-www-form-urlencoded", "X-Sidecar-Request": "1", "Authorization": auth["Authorization"]},
		"text plain":     {"Origin": h.ownOrigin(), "Content-Type": "text/plain", "X-Sidecar-Request": "1", "Authorization": auth["Authorization"]},
		"wrong value":    {"Origin": h.ownOrigin(), "Content-Type": "application/json", "X-Sidecar-Request": "yes", "Authorization": auth["Authorization"]},
	} {
		response, data := h.browserDo(req{method: http.MethodPost, path: "/api/v0/ws-tickets", body: "{}", header: headers})
		if response.StatusCode != http.StatusForbidden || errorCode(t, data) != CodeMutationRefused {
			t.Fatalf("%s: status %d body %s", name, response.StatusCode, data)
		}
	}
	response, data := h.browserDo(req{method: http.MethodPost, path: "/api/v0/ws-tickets", body: "{}",
		header: mutationHeaders(h.ownOrigin(), map[string]string{"Authorization": auth["Authorization"], "Content-Type": "application/json; charset=utf-8"})})
	expect(t, response, data, http.StatusOK, "")
}

func TestCORSOnlyForPairedOrigins(t *testing.T) {
	h := newHarness(t)
	const app = "http://app.example:5173"
	token := h.pairOrigin(app)
	bearer := map[string]string{"Origin": app, "Authorization": "Bearer " + token}
	response, data := h.browserDo(req{path: "/api/v0/hello", header: bearer})
	expect(t, response, data, http.StatusOK, "")
	if got := response.Header.Get("Access-Control-Allow-Origin"); got != app {
		t.Fatalf("ACAO = %q", got)
	}
	if response.Header.Get("Access-Control-Allow-Credentials") != "" {
		t.Fatal("CORS must not allow credentials")
	}
	// Preflight: paired gets the allowance, unpaired is refused.
	response, data = h.browserDo(req{method: http.MethodOptions, path: "/api/v0/ws-tickets", header: map[string]string{"Origin": app,
		"Access-Control-Request-Method": "POST", "Access-Control-Request-Headers": "authorization, content-type, x-sidecar-request"}})
	expect(t, response, data, http.StatusNoContent, "")
	if response.Header.Get("Access-Control-Allow-Origin") != app || response.Header.Get("Access-Control-Allow-Headers") != corsAllowedHeaders {
		t.Fatalf("preflight headers = %v", response.Header)
	}
	response, data = h.browserDo(req{method: http.MethodOptions, path: "/api/v0/ws-tickets", header: map[string]string{"Origin": "http://other.example",
		"Access-Control-Request-Method": "POST"}})
	expect(t, response, data, http.StatusForbidden, CodeOriginRefused)
	if response.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("unpaired preflight received CORS headers")
	}
	// The same-origin UI never gets CORS headers.
	session := h.pairBrowser()
	response, _ = h.browserDo(req{path: "/api/v0/hello", header: map[string]string{"Origin": h.ownOrigin(), "Authorization": "Bearer " + session}})
	if response.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("own origin received CORS headers")
	}
	// A token is bound to its origin; a paired origin cannot use the session token.
	other := "http://second.example"
	h.pairOrigin(other)
	response, data = h.browserDo(req{path: "/api/v0/hello", header: map[string]string{"Origin": other, "Authorization": "Bearer " + token}})
	expect(t, response, data, http.StatusForbidden, CodeOriginRefused)
	response, data = h.browserDo(req{path: "/api/v0/hello", header: map[string]string{"Origin": app, "Authorization": "Bearer " + session}})
	expect(t, response, data, http.StatusForbidden, CodeOriginRefused)
}

func TestLocalOnlyRoutesRefusedOnTCPAndTailnet(t *testing.T) {
	h := newHarness(t)
	auth := map[string]string{"Authorization": "Bearer " + h.pairBrowser()}
	for _, r := range []req{
		{method: http.MethodPost, path: "/api/v0/pairing/codes", body: "{}", header: mutationHeaders(h.ownOrigin(), auth)},
		{method: http.MethodPost, path: "/api/v0/origins", body: `{"origin":"http://x.example"}`, header: mutationHeaders(h.ownOrigin(), auth)},
		{path: "/api/v0/origins", header: auth},
		{method: http.MethodDelete, path: "/api/v0/origins?origin=http://x.example", header: mutationHeaders(h.ownOrigin(), auth)},
	} {
		response, data := h.browserDo(r)
		expect(t, response, data, http.StatusForbidden, CodeLocalOnly)
		r.header = map[string]string{tailscaleLoginHead: testTailnetLogin}
		if r.method != "" && r.method != http.MethodGet {
			r.header = mutationHeaders("https://"+testTailnetHost, r.header)
		}
		response, data = h.tailnetDo(r)
		expect(t, response, data, http.StatusForbidden, CodeLocalOnly)
	}
	// Tickets are for browsers; the Local listener has no use for them.
	response, data := h.localDo(req{method: http.MethodPost, path: "/api/v0/ws-tickets", body: "{}"})
	expect(t, response, data, http.StatusForbidden, CodeNotServedHere)
}

func TestPairingCodeIsSingleUseAndExpires(t *testing.T) {
	h := newHarness(t)
	code := h.pairingCode("/s/aerie/1")
	if !strings.HasPrefix(code.URL, h.s.BrowserURL()+"/pair#") || strings.Contains(code.URL, "?") {
		t.Fatalf("url = %q; the code must ride in the fragment", code.URL)
	}
	if !code.ExpiresAt.Equal(h.clock.Now().Add(60 * time.Second)) {
		t.Fatalf("expires_at = %v", code.ExpiresAt)
	}
	secret := fragmentValue(t, code.URL, "code")
	if secret != code.Code || fragmentValue(t, code.URL, "next") != "/s/aerie/1" {
		t.Fatalf("fragment of %q", code.URL)
	}
	// Loading the page (GET or HEAD) reads and consumes nothing, and sets no
	// cookie: the page's script does the exchange.
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		response, _ := h.browserDo(req{method: method, path: "/pair"})
		if response.StatusCode != http.StatusOK || len(response.Cookies()) != 0 || response.Header.Get("Set-Cookie") != "" {
			t.Fatalf("%s /pair: %d %v", method, response.StatusCode, response.Header)
		}
	}
	response, data := h.exchange(h.ownOrigin(), secret, "/s/aerie/1")
	expect(t, response, data, http.StatusOK, "")
	var exchanged PairingExchange
	if err := json.Unmarshal(data, &exchanged); err != nil || exchanged.Token == "" || exchanged.Next != "/s/aerie/1" {
		t.Fatalf("exchange = %s", data)
	}
	if len(response.Cookies()) != 0 {
		t.Fatal("the exchange set a cookie")
	}
	response, data = h.exchange(h.ownOrigin(), secret, "/")
	expect(t, response, data, http.StatusUnauthorized, CodePairingInvalid)

	expired := h.pairingCode("/")
	h.clock.Advance(61 * time.Second)
	response, data = h.exchange(h.ownOrigin(), expired.Code, "/")
	expect(t, response, data, http.StatusUnauthorized, CodePairingInvalid)

	// The token authenticates API reads; a forged one does not, and a cookie
	// carrying it means nothing.
	response, data = h.browserDo(req{path: "/api/v0/hello", header: map[string]string{"Authorization": "Bearer " + exchanged.Token}})
	expect(t, response, data, http.StatusOK, "")
	response, data = h.browserDo(req{path: "/api/v0/hello", header: map[string]string{"Authorization": "Bearer forged"}})
	expect(t, response, data, http.StatusUnauthorized, CodeUnauthenticated)
	response, data = h.browserDo(req{path: "/api/v0/hello", header: map[string]string{"Cookie": "sidecar_session_" + h.browserPort() + "=" + exchanged.Token}})
	expect(t, response, data, http.StatusUnauthorized, CodeUnauthenticated)
	// The pairing page and exchange are Browser routes only.
	response, data = h.localDo(req{path: "/pair"})
	expect(t, response, data, http.StatusForbidden, CodeNotServedHere)
	response, data = h.localDo(req{method: http.MethodPost, path: "/api/v0/pairing/exchange", body: "{}"})
	expect(t, response, data, http.StatusForbidden, CodeNotServedHere)
}

func TestPairingPageIsLockedDown(t *testing.T) {
	h := newHarness(t)
	response, data := h.browserDo(req{path: "/pair"})
	if response.StatusCode != http.StatusOK || !bytes.Contains(data, []byte("/api/v0/pairing/exchange")) || !bytes.Contains(data, []byte(SessionStorageKey)) {
		t.Fatalf("pair page: %d %q", response.StatusCode, data)
	}
	csp := response.Header.Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "script-src 'sha256-" + pairScriptHash + "'", "connect-src 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Fatalf("CSP %q lacks %q", csp, want)
		}
	}
	if response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("headers = %v", response.Header)
	}
	if !bytes.Contains(data, []byte("<script>"+pairScript+"</script>")) {
		t.Fatal("the served script is not the hashed one")
	}
}

func TestPairingExchangeIsAnOwnOriginMutation(t *testing.T) {
	h := newHarness(t)
	const app = "http://app.example:5173"
	h.pairOrigin(app)
	code := h.pairingCode("/")
	// A paired origin has its own token and may not mint a browser session.
	response, data := h.exchange(app, code.Code, "/")
	expect(t, response, data, http.StatusForbidden, CodeOriginRefused)
	// Foreign and missing origins, and a preflight-free form post, never reach it.
	response, data = h.exchange("http://evil.example", code.Code, "/")
	expect(t, response, data, http.StatusForbidden, CodeOriginRefused)
	body := `{"code":"` + code.Code + `"}`
	response, data = h.browserDo(req{method: http.MethodPost, path: "/api/v0/pairing/exchange", body: body, header: map[string]string{"Content-Type": "application/json", "X-Sidecar-Request": "1"}})
	expect(t, response, data, http.StatusForbidden, CodeOriginRefused)
	response, data = h.browserDo(req{method: http.MethodPost, path: "/api/v0/pairing/exchange", body: body, header: map[string]string{"Origin": h.ownOrigin(), "Content-Type": "text/plain"}})
	expect(t, response, data, http.StatusForbidden, CodeMutationRefused)
	// None of the refusals consumed the code.
	response, data = h.exchange(h.ownOrigin(), code.Code, "/")
	expect(t, response, data, http.StatusOK, "")
}

func TestSessionTokenIsBoundToItsOrigin(t *testing.T) {
	h := newHarness(t)
	const app = "http://app.example:5173"
	h.pairOrigin(app)
	token := h.pairBrowser()
	bearer := "Bearer " + token
	// No Origin (a same-origin GET) and the exact exchanging origin are accepted.
	response, data := h.browserDo(req{path: "/api/v0/hello", header: map[string]string{"Authorization": bearer}})
	expect(t, response, data, http.StatusOK, "")
	response, data = h.browserDo(req{path: "/api/v0/hello", header: map[string]string{"Authorization": bearer, "Origin": h.ownOrigin()}})
	expect(t, response, data, http.StatusOK, "")
	// The listener's other own origin, and a paired origin, are not that origin.
	port := h.browserPort()
	response, data = h.browserDo(req{path: "/api/v0/hello", host: "localhost:" + port, header: map[string]string{"Authorization": bearer, "Origin": "http://localhost:" + port}})
	expect(t, response, data, http.StatusForbidden, CodeOriginRefused)
	response, data = h.browserDo(req{path: "/api/v0/hello", header: map[string]string{"Authorization": bearer, "Origin": app}})
	expect(t, response, data, http.StatusForbidden, CodeOriginRefused)
	// The same-origin UI opens terminals through a ticket, like any client.
	response, data = h.browserDo(req{method: http.MethodPost, path: "/api/v0/ws-tickets", body: "{}", header: mutationHeaders(h.ownOrigin(), map[string]string{"Authorization": bearer})})
	expect(t, response, data, http.StatusOK, "")
	var issued TicketResponse
	_ = json.Unmarshal(data, &issued)
	h.expectBrowserClose(t, "session ticket from a paired origin", "?ticket="+issued.Ticket, http.Header{"Origin": {app}}, CloseOriginRefused)
}

func TestPairNextRefusesOpenRedirects(t *testing.T) {
	h := newHarness(t)
	for _, next := range []string{"//evil.example/", "https://evil.example/", "/\\evil.example", "\\\\evil.example", "javascript:alert(1)", "evil", "/a\nb", "http:/x"} {
		body, _ := json.Marshal(map[string]string{"next": next})
		response, data := h.localDo(req{method: http.MethodPost, path: "/api/v0/pairing/codes", body: string(body)})
		expect(t, response, data, http.StatusBadRequest, CodeInvalidRequest)

		// A tampered fragment is refused at the exchange without spending the code.
		code := h.pairingCode("/")
		response, data = h.exchange(h.ownOrigin(), code.Code, next)
		expect(t, response, data, http.StatusBadRequest, CodeInvalidRequest)
		response, data = h.exchange(h.ownOrigin(), code.Code, "/")
		expect(t, response, data, http.StatusOK, "")
	}
	code := h.pairingCode("")
	if fragmentValue(t, code.URL, "next") != "/" {
		t.Fatalf("default next missing: %s", code.URL)
	}
}

func TestOriginsFileIsPrivateAndHashOnly(t *testing.T) {
	h := newHarness(t)
	token := h.pairOrigin("HTTP://App.Example:80/")
	path := filepath.Join(Dir(h.state), originsFileName)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("origins.json mode = %v", info.Mode().Perm())
	}
	data, _ := os.ReadFile(path)
	if bytes.Contains(data, []byte(token)) {
		t.Fatal("origins.json contains the plaintext token")
	}
	sum := sha256.Sum256([]byte(token))
	var file originsFile
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Origins) != 1 || file.Origins[0].Origin != "http://app.example" || file.Origins[0].TokenSHA256 != hex.EncodeToString(sum[:]) || file.Origins[0].Scopes[0] != ScopeFull {
		t.Fatalf("origins.json = %s", data)
	}
	response, list := h.localDo(req{path: "/api/v0/origins"})
	expect(t, response, list, http.StatusOK, "")
	if bytes.Contains(list, []byte(token)) || bytes.Contains(list, []byte("token")) {
		t.Fatalf("listing exposes tokens: %s", list)
	}
	// Re-pairing rotates; the old token stops working.
	rotated := h.pairOrigin("http://app.example")
	response, body := h.browserDo(req{path: "/api/v0/hello", header: map[string]string{"Origin": "http://app.example", "Authorization": "Bearer " + token}})
	expect(t, response, body, http.StatusUnauthorized, CodeUnauthenticated)
	response, body = h.browserDo(req{path: "/api/v0/hello", header: map[string]string{"Origin": "http://app.example", "Authorization": "Bearer " + rotated}})
	expect(t, response, body, http.StatusOK, "")
	response, body = h.localDo(req{method: http.MethodDelete, path: "/api/v0/origins?origin=http://app.example"})
	expect(t, response, body, http.StatusOK, "")
	response, body = h.browserDo(req{path: "/api/v0/hello", header: map[string]string{"Origin": "http://app.example", "Authorization": "Bearer " + rotated}})
	expect(t, response, body, http.StatusForbidden, CodeOriginRefused)
	response, body = h.localDo(req{method: http.MethodDelete, path: "/api/v0/origins?origin=http://app.example"})
	expect(t, response, body, http.StatusNotFound, CodeOriginNotFound)
	response, body = h.localDo(req{method: http.MethodPost, path: "/api/v0/origins", body: `{"origin":"http://a.example","scopes":["future:write"]}`})
	expect(t, response, body, http.StatusBadRequest, CodeInvalidRequest)
	response, body = h.localDo(req{method: http.MethodPost, path: "/api/v0/origins", body: `{"origin":"http://a.example/path"}`})
	expect(t, response, body, http.StatusBadRequest, CodeInvalidRequest)
}

func TestTailnetIdentityHeaderHonoredOnlyOnTailnet(t *testing.T) {
	h := newHarness(t)
	response, data := h.browserDo(req{path: "/api/v0/hello", header: map[string]string{tailscaleLoginHead: testTailnetLogin}})
	expect(t, response, data, http.StatusUnauthorized, CodeUnauthenticated)
	response, data = h.tailnetDo(req{path: "/api/v0/hello", header: map[string]string{tailscaleLoginHead: testTailnetLogin}})
	expect(t, response, data, http.StatusOK, "")
	response, data = h.tailnetDo(req{path: "/api/v0/hello", header: map[string]string{tailscaleLoginHead: "intruder@example.com"}})
	expect(t, response, data, http.StatusForbidden, CodeLoginRefused)
	response, data = h.tailnetDo(req{path: "/api/v0/hello"})
	expect(t, response, data, http.StatusUnauthorized, CodeUnauthenticated)
	// Static files on the tailnet need the login too.
	response, data = h.tailnetDo(req{path: "/"})
	expect(t, response, data, http.StatusUnauthorized, CodeUnauthenticated)
	// A browser session token does not stand in for a tailnet login.
	session := h.pairBrowser()
	response, data = h.tailnetDo(req{path: "/api/v0/hello", header: map[string]string{"Authorization": "Bearer " + session}})
	expect(t, response, data, http.StatusUnauthorized, CodeUnauthenticated)
}

func TestStatusUIDirIsLocalOnly(t *testing.T) {
	for _, configured := range []bool{false, true} {
		t.Run(fmt.Sprintf("configured=%t", configured), func(t *testing.T) {
			ui := ""
			if configured {
				ui = t.TempDir()
				if err := os.WriteFile(filepath.Join(ui, "index.html"), []byte("UI"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			h := newHarness(t, func(o *Options) { o.UIDir = ui })
			token := h.pairBrowser()
			for name, do := range map[string]func(req) (*http.Response, []byte){"local": h.localDo, "browser": h.browserDo, "tailnet": h.tailnetDo} {
				r, body := do(req{path: "/api/v0/status", header: map[string]string{"Authorization": "Bearer " + token, tailscaleLoginHead: testTailnetLogin}})
				if r.StatusCode != http.StatusOK {
					t.Fatalf("%s: %d %s", name, r.StatusCode, body)
				}
				var status map[string]json.RawMessage
				if err := json.Unmarshal(body, &status); err != nil {
					t.Fatal(err)
				}
				var gotConfigured bool
				if err := json.Unmarshal(status["ui_configured"], &gotConfigured); err != nil || gotConfigured != configured {
					t.Fatalf("%s configured: %s %v", name, body, err)
				}
				_, hasPath := status["ui_dir"]
				if hasPath != (name == "local") {
					t.Fatalf("%s path exposure: %s", name, body)
				}
				if name == "local" {
					var got string
					if err := json.Unmarshal(status["ui_dir"], &got); err != nil || got != ui {
						t.Fatalf("local dir: %s %v", body, err)
					}
				} else if ui != "" && bytes.Contains(body, []byte(ui)) {
					t.Fatalf("%s leaked home path: %s", name, body)
				}
			}
		})
	}
}

func TestSessionsMapsQueryAndEncodesLikeTheCLI(t *testing.T) {
	h := newHarness(t)
	response, data := h.localDo(req{path: "/api/v0/sessions?sort=name&search=side&host=a&host=b&provider=codex&state=working&show_idle_sessions=false"})
	expect(t, response, data, http.StatusOK, "")
	h.backend.mu.Lock()
	query := h.backend.query
	h.backend.mu.Unlock()
	if query.Sort != "name" || query.Search != "side" || strings.Join(query.Hosts, ",") != "a,b" || query.Providers[0] != "codex" ||
		query.States[0] != "working" || query.ShowIdleSessions == nil || *query.ShowIdleSessions {
		t.Fatalf("query = %+v", query)
	}
	var want bytes.Buffer
	_ = json.NewEncoder(&want).Encode(h.backend.snapshot)
	if !bytes.Equal(data, want.Bytes()) {
		t.Fatalf("body %q != CLI encoding %q", data, want.Bytes())
	}
	for _, bad := range []string{"?bogus=1", "?show_idle_sessions=maybe", "?sort=a&sort=b"} {
		response, data = h.localDo(req{path: "/api/v0/sessions" + bad})
		expect(t, response, data, http.StatusBadRequest, CodeInvalidRequest)
	}
	h.backend.mu.Lock()
	h.backend.err = errors.New("catalog unavailable")
	h.backend.mu.Unlock()
	response, data = h.localDo(req{path: "/api/v0/sessions"})
	expect(t, response, data, http.StatusServiceUnavailable, CodeBackend)
}

func TestSingleInstanceAndEndpointLifecycle(t *testing.T) {
	h := newHarness(t)
	path := EndpointPath(h.state)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("endpoint.json mode = %v", info.Mode().Perm())
	}
	for _, socket := range []string{h.s.Endpoint().UnixSocket, h.s.Endpoint().TailnetSocket} {
		info, err := os.Stat(socket)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("socket %s mode = %v, %v", socket, info.Mode().Perm(), err)
		}
	}
	endpoint, err := ReadEndpoint(h.state)
	if err != nil || endpoint.APIInstance != h.s.Endpoint().APIInstance || endpoint.PID != os.Getpid() {
		t.Fatalf("endpoint = %+v, %v", endpoint, err)
	}
	_, err = Start(Options{StateDir: h.state, Port: 0, Backend: h.backend})
	var running *AlreadyRunningError
	if !errors.As(err, &running) || running.PID != os.Getpid() {
		t.Fatalf("second Start = %v", err)
	}
	if err := h.s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("endpoint.json survived shutdown: %v", err)
	}
	if _, err := ReadEndpoint(h.state); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("ReadEndpoint after shutdown = %v", err)
	}
	// The lock is released: a successor starts on the same tree.
	next, err := Start(Options{StateDir: h.state, Port: 0, Backend: h.backend})
	if err != nil {
		t.Fatal(err)
	}
	_ = next.Shutdown(context.Background())
}

func TestStaticUIWithSPAFallbackAndNoUIPage(t *testing.T) {
	ui := t.TempDir()
	if err := os.WriteFile(filepath.Join(ui, "index.html"), []byte("<p>app</p>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ui, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ui, "assets", "app.js"), []byte("console.log(1)"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, func(o *Options) { o.UIDir = ui })
	for path, want := range map[string]string{"/": "<p>app</p>", "/assets/app.js": "console.log(1)", "/s/aerie/42": "<p>app</p>", "/assets": "<p>app</p>"} {
		response, data := h.browserDo(req{path: path})
		if response.StatusCode != http.StatusOK || string(data) != want {
			t.Fatalf("%s: %d %q", path, response.StatusCode, data)
		}
	}
	// Traversal never escapes DIR: net/http refuses the path outright.
	response, data := h.browserDo(req{path: "/../../etc/passwd"})
	if response.StatusCode == http.StatusOK && !bytes.Equal(data, []byte("<p>app</p>")) {
		t.Fatalf("traversal served %q", data)
	}
	response, data = h.browserDo(req{path: "/api/v0/nope"})
	expect(t, response, data, http.StatusNotFound, CodeNotFound)

	plain := newHarness(t)
	response, data = plain.browserDo(req{path: "/"})
	for _, want := range []string{"API is running", "sidecar api service install --ui DIR", "https://sidecar.haplab.com/docs/build-your-own-ui"} {
		if response.StatusCode != http.StatusOK || !bytes.Contains(data, []byte(want)) {
			t.Fatalf("no-UI root: %d %q", response.StatusCode, data)
		}
	}
	if response.Header.Get("Content-Type") != "text/html; charset=utf-8" || bytes.Contains(data, []byte("<script")) {
		t.Fatalf("no-UI HTML: %v %q", response.Header, data)
	}
	response, data = plain.browserDo(req{path: "/anything"})
	if response.StatusCode != http.StatusOK || !bytes.Contains(data, []byte("sidecar api open")) {
		t.Fatalf("no-UI page: %d %q", response.StatusCode, data)
	}
}

// --- terminal stream ---

func (h *harness) dialLocal(t *testing.T) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.Dial(context.Background(), "ws://sidecar.local"+terminalPath, &websocket.DialOptions{HTTPClient: unixClient(h.s.Endpoint().UnixSocket)})
	if err != nil {
		t.Fatal(err)
	}
	conn.SetReadLimit(mobileproto.MaxLineBytes)
	return conn
}

func (h *harness) dialBrowser(t *testing.T, query string, header http.Header) (*websocket.Conn, error) {
	t.Helper()
	u := "ws" + strings.TrimPrefix(h.s.BrowserURL(), "http") + terminalPath + query
	conn, _, err := websocket.Dial(context.Background(), u, &websocket.DialOptions{HTTPHeader: header})
	return conn, err
}

func readText(t *testing.T, conn *websocket.Conn) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	kind, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if kind != websocket.MessageText {
		t.Fatalf("kind = %v", kind)
	}
	return string(data)
}

func writeText(t *testing.T, conn *websocket.Conn, text string) {
	t.Helper()
	if err := conn.Write(context.Background(), websocket.MessageText, []byte(text)); err != nil {
		t.Fatal(err)
	}
}

func closeStatus(t *testing.T, conn *websocket.Conn) (websocket.StatusCode, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		_, _, err := conn.Read(ctx)
		if err != nil {
			var closeErr websocket.CloseError
			if errors.As(err, &closeErr) {
				return closeErr.Code, closeErr.Reason
			}
			t.Fatalf("read ended without a close frame: %v", err)
		}
	}
}

func TestTerminalBridgesTextMessagesToJSONL(t *testing.T) {
	h := newHarness(t)
	conn := h.dialLocal(t)
	defer func() { _ = conn.CloseNow() }()
	writeText(t, conn, `{"version":0,"type":"hello","request_id":"1"}`)
	if got := readText(t, conn); got != `{"version":0,"type":"hello","request_id":"1"}` {
		t.Fatalf("echo = %q", got)
	}
	writeText(t, conn, `{"version":0,"type":"status","request_id":"2"}`)
	if got := readText(t, conn); got != `{"version":0,"type":"status","request_id":"2"}` {
		t.Fatalf("echo = %q", got)
	}
	status := h.s.status()
	if len(status.Clients) != 1 || status.Clients[0].Kind != "terminal" || status.Clients[0].Listener != ListenerLocal {
		t.Fatalf("status clients = %+v", status.Clients)
	}
	// Closing the socket is EOF for the stream.
	_ = conn.Close(websocket.StatusNormalClosure, "")
	select {
	case <-h.backend.eof:
	case <-time.After(5 * time.Second):
		t.Fatal("backend never saw EOF after the socket closed")
	}
}

func TestTerminalRefusesBinaryAndMultilineMessages(t *testing.T) {
	h := newHarness(t)
	for name, send := range map[string]func(*websocket.Conn) error{
		"binary": func(c *websocket.Conn) error {
			return c.Write(context.Background(), websocket.MessageBinary, []byte(`{}`))
		},
		"multiline": func(c *websocket.Conn) error {
			return c.Write(context.Background(), websocket.MessageText, []byte("{}\n{}"))
		},
		"trailing": func(c *websocket.Conn) error {
			return c.Write(context.Background(), websocket.MessageText, []byte("{}\n"))
		},
		"empty": func(c *websocket.Conn) error { return c.Write(context.Background(), websocket.MessageText, nil) },
	} {
		conn := h.dialLocal(t)
		if err := send(conn); err != nil {
			t.Fatal(err)
		}
		code, reason := closeStatus(t, conn)
		if code != CloseProtocolViolation || reason == "" {
			t.Fatalf("%s: close %d %q", name, code, reason)
		}
		select {
		case <-h.backend.eof:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: backend never saw EOF", name)
		}
	}
}

func TestTerminalCloseCodesFollowTheBackend(t *testing.T) {
	h := newHarness(t)
	for _, tc := range []struct {
		send string
		code websocket.StatusCode
	}{
		{`{"cmd":"end"}`, websocket.StatusNormalClosure},
		{`{"cmd":"fail"}`, websocket.StatusInternalError},
		{`{"cmd":"protocol"}`, CloseProtocolViolation},
	} {
		conn := h.dialLocal(t)
		writeText(t, conn, tc.send)
		if tc.code == CloseProtocolViolation {
			if got := readText(t, conn); !strings.Contains(got, "invalid_request") {
				t.Fatalf("final error envelope not delivered before close: %q", got)
			}
		}
		code, reason := closeStatus(t, conn)
		if code != tc.code || reason == "" {
			t.Fatalf("%s: close %d %q, want %d", tc.send, code, reason, tc.code)
		}
	}
}

func TestTerminalShutdownClosesWith4409(t *testing.T) {
	h := newHarness(t)
	conn := h.dialLocal(t)
	writeText(t, conn, `{"x":1}`)
	readText(t, conn)
	done := make(chan error, 1)
	go func() { done <- h.s.Shutdown(context.Background()) }()
	code, _ := closeStatus(t, conn)
	if code != CloseShuttingDown {
		t.Fatalf("close = %d", code)
	}
	select {
	case <-h.backend.ctxEnded:
	case <-time.After(5 * time.Second):
		t.Fatal("backend stream was not canceled on shutdown")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestTerminalBrowserAuthAndTickets(t *testing.T) {
	h := newHarness(t)
	const app = "http://app.example:5173"
	token := h.pairOrigin(app)
	ticket := func() string {
		response, data := h.browserDo(req{method: http.MethodPost, path: "/api/v0/ws-tickets", body: "{}", header: mutationHeaders(app, map[string]string{"Authorization": "Bearer " + token})})
		expect(t, response, data, http.StatusOK, "")
		var issued TicketResponse
		_ = json.Unmarshal(data, &issued)
		if !issued.ExpiresAt.Equal(h.clock.Now().Add(30 * time.Second)) {
			t.Fatalf("ticket expiry = %v", issued.ExpiresAt)
		}
		return issued.Ticket
	}
	origin := func(o string) http.Header { return http.Header{"Origin": {o}} }

	// Unpaired origin: 4403 even with a valid-looking ticket.
	conn, err := h.dialBrowser(t, "?ticket="+ticket(), origin("http://evil.example"))
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := closeStatus(t, conn); code != CloseOriginRefused {
		t.Fatalf("evil origin close = %d", code)
	}
	// No credential: 4401.
	conn, err = h.dialBrowser(t, "", origin(app))
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := closeStatus(t, conn); code != CloseUnauthenticated {
		t.Fatalf("no credential close = %d", code)
	}
	// A ticket works once.
	issued := ticket()
	conn, err = h.dialBrowser(t, "?ticket="+issued, origin(app))
	if err != nil {
		t.Fatal(err)
	}
	writeText(t, conn, `{"ok":true}`)
	if got := readText(t, conn); got != `{"ok":true}` {
		t.Fatalf("echo = %q", got)
	}
	status := h.s.status()
	if len(status.Clients) != 1 || status.Clients[0].Auth != "ticket" || status.Clients[0].Origin != app {
		t.Fatalf("status = %+v", status.Clients)
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")
	conn, err = h.dialBrowser(t, "?ticket="+issued, origin(app))
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := closeStatus(t, conn); code != CloseUnauthenticated {
		t.Fatalf("reused ticket close = %d", code)
	}
	// An expired ticket is refused.
	stale := ticket()
	h.clock.Advance(31 * time.Second)
	conn, err = h.dialBrowser(t, "?ticket="+stale, origin(app))
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := closeStatus(t, conn); code != CloseUnauthenticated {
		t.Fatalf("expired ticket close = %d", code)
	}
	// A ticket is bound to the origin it was issued to.
	h.pairOrigin("http://second.example")
	misused := ticket()
	conn, err = h.dialBrowser(t, "?ticket="+misused, origin("http://second.example"))
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := closeStatus(t, conn); code != CloseOriginRefused {
		t.Fatalf("cross-origin ticket close = %d", code)
	}
	// The same-origin UI takes the same path: its session token buys a ticket,
	// and a cookie carrying the token opens nothing.
	session := h.pairBrowser()
	h.expectBrowserClose(t, "session token as a cookie", "", http.Header{"Origin": {h.ownOrigin()}, "Cookie": {"sidecar_session_" + h.browserPort() + "=" + session}}, CloseUnauthenticated)
	response, data := h.browserDo(req{method: http.MethodPost, path: "/api/v0/ws-tickets", body: "{}", header: mutationHeaders(h.ownOrigin(), map[string]string{"Authorization": "Bearer " + session})})
	expect(t, response, data, http.StatusOK, "")
	var own TicketResponse
	_ = json.Unmarshal(data, &own)
	conn, err = h.dialBrowser(t, "?ticket="+own.Ticket, http.Header{"Origin": {h.ownOrigin()}})
	if err != nil {
		t.Fatal(err)
	}
	writeText(t, conn, `{"session":true}`)
	if got := readText(t, conn); got != `{"session":true}` {
		t.Fatalf("echo = %q", got)
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")
}

func TestTerminalStatusTracksControl(t *testing.T) {
	client := &trackedClient{}
	client.observe([]byte(`{"version":0,"type":"resolved","request_id":"r","target":{"session":"sidecar-sh-a","pane":"%1","display_name":"A","workspace_id":"w"}}`))
	client.observe([]byte(`{"version":0,"type":"opened","request_id":"o","attachment_handle":"h"}`))
	if !client.open || client.term.Control || client.term.Session != "sidecar-sh-a" {
		t.Fatalf("after open: %+v", client.term)
	}
	client.observe([]byte(`{"version":0,"type":"control","request_id":"c","control":true}`))
	client.observe([]byte(`{"version":0,"type":"reset","reason":"resize"}`))
	client.observe([]byte(`{"version":0,"type":"frame","render_vt_base64":"AAAA"}`))
	if !client.term.Control {
		t.Fatal("a resize reset revoked control")
	}
	client.observe([]byte(`{"version":0,"type":"reset","reason":"presence_timeout"}`))
	if client.term.Control {
		t.Fatal("a revoking reset kept control")
	}
	client.observe([]byte(`{"version":0,"type":"closed","request_id":"x"}`))
	if client.open {
		t.Fatal("closed attachment still open")
	}
}

func TestDedicatedTailnetHTTPSPortGuards(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.Tailnet.HTTPSPort = 7861 })
	host := testTailnetHost + ":7861"
	origin := "https://" + host
	for _, tc := range []struct {
		name, host, origin, login string
		status                    int
		code                      string
	}{
		{"owner", host, origin, testTailnetLogin, 200, ""},
		{"same-origin GET", host, "", testTailnetLogin, 200, ""},
		{"no login", host, origin, "", 401, CodeUnauthenticated},
		{"other login", host, origin, "other@example.com", 403, "tailnet_login_refused"},
		{"bare host", testTailnetHost, "", testTailnetLogin, 421, CodeHostRefused},
		{"standard HTTPS host", testTailnetHost + ":443", "", testTailnetLogin, 421, CodeHostRefused},
		{"different port", testTailnetHost + ":7862", "", testTailnetLogin, 421, CodeHostRefused},
		{"foreign host", "evil.example:7861", "", testTailnetLogin, 421, CodeHostRefused},
		{"svc origin", host, "https://" + testTailnetHost, testTailnetLogin, 403, CodeOriginRefused},
		{"HTTP origin", host, "http://" + host, testTailnetLogin, 403, CodeOriginRefused},
		{"other HTTPS port", host, "https://" + testTailnetHost + ":7862", testTailnetLogin, 403, CodeOriginRefused},
		{"foreign origin", host, "https://evil.example:7861", testTailnetLogin, 403, CodeOriginRefused},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, b := h.tailnetDo(req{path: "/api/v0/hello", host: tc.host, header: map[string]string{"Origin": tc.origin, tailscaleLoginHead: tc.login}})
			expect(t, r, b, tc.status, tc.code)
			if tc.status == 421 && !strings.Contains(string(b), origin) {
				t.Fatalf("wrong canonical origin: %s", b)
			}
		})
	}
	for _, path := range []string{eventsPath, "/api/v0/terminal"} {
		for _, tc := range []struct {
			origin, login string
			code          websocket.StatusCode
		}{
			{origin, "", CloseUnauthenticated},
			{"https://" + testTailnetHost, testTailnetLogin, CloseOriginRefused},
			{"https://evil.example", testTailnetLogin, CloseOriginRefused},
		} {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			c, _, err := websocket.Dial(ctx, "ws://"+host+path, &websocket.DialOptions{HTTPClient: h.tailnet, HTTPHeader: http.Header{"Origin": {tc.origin}, tailscaleLoginHead: {tc.login}}})
			if err != nil {
				cancel()
				t.Fatal(err)
			}
			_, _, err = c.Read(ctx)
			_ = c.CloseNow()
			cancel()
			if websocket.CloseStatus(err) != tc.code {
				t.Fatalf("%s origin %q: %v", path, tc.origin, err)
			}
		}
	}
	r, b := h.tailnetDo(req{method: "POST", path: "/api/v0/ws-tickets", host: host, body: "{}", header: mutationHeaders(origin, map[string]string{tailscaleLoginHead: testTailnetLogin})})
	expect(t, r, b, 200, "")
	r, b = h.tailnetDo(req{method: "POST", path: "/api/v0/ws-tickets", host: host, body: "{}", header: map[string]string{"Origin": origin, tailscaleLoginHead: testTailnetLogin, "Content-Type": "application/json"}})
	expect(t, r, b, 403, CodeMutationRefused)
}

func TestTailnetHTTPSPortValidation(t *testing.T) {
	for _, port := range []int{-1, 65536} {
		_, err := Start(Options{StateDir: shortTempDir(t), Port: 0, Backend: newFakeBackend(), Tailnet: &TailnetOptions{Host: testTailnetHost, Logins: []string{testTailnetLogin}, HTTPSPort: port}})
		if err == nil || !strings.Contains(err.Error(), "tailnetHTTPSPort") {
			t.Fatalf("port %d: %v", port, err)
		}
	}
}
