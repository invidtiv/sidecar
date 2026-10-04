package uiapi

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// These tests attack the guards from the threat model in ui-api.md: a
// malicious page in the user's browser, DNS rebinding, and races on the
// single-use credentials.

func (h *harness) browserPort() string {
	return strings.TrimPrefix(h.s.BrowserURL(), "http://127.0.0.1:")
}

func TestHostGuardRefusesNearMisses(t *testing.T) {
	h := newHarness(t)
	auth := map[string]string{"Authorization": "Bearer " + h.pairBrowser()}
	port := h.browserPort()
	for _, host := range []string{
		"localhost." + ":" + port, // trailing dot
		"127.0.0.1",               // no port
		"127.0.0.1:1",             // another port
		"[::1]:" + port,           // the IPv6 loopback is not bound
		"127.0.0.1:" + port + ".",
		"evil.example:" + port, // rebinding keeps the port
		"127.0.0.1:" + port + "@evil.example",
	} {
		response, data := h.browserDo(req{path: "/api/v0/hello", host: host, header: auth})
		expect(t, response, data, http.StatusMisdirectedRequest, CodeHostRefused)
		// Static files and /pair sit behind the same guard.
		response, data = h.browserDo(req{path: "/", host: host})
		expect(t, response, data, http.StatusMisdirectedRequest, CodeHostRefused)
	}
	for _, host := range []string{testTailnetHost + ".", testTailnetHost + ":8443", "127.0.0.1", "node", "evil.example"} {
		response, data := h.tailnetDo(req{path: "/api/v0/hello", host: host, header: map[string]string{tailscaleLoginHead: testTailnetLogin}})
		expect(t, response, data, http.StatusMisdirectedRequest, CodeHostRefused)
	}
	// DNS names are case-insensitive on the tailnet.
	response, data := h.tailnetDo(req{path: "/api/v0/hello", host: strings.ToUpper(testTailnetHost), header: map[string]string{tailscaleLoginHead: testTailnetLogin}})
	expect(t, response, data, http.StatusOK, "")
}

func TestHostGuardRefusesAMissingHost(t *testing.T) {
	h := newHarness(t)
	conn, err := net.Dial("tcp", strings.TrimPrefix(h.s.BrowserURL(), "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	// HTTP/1.0 may omit Host; net/http then hands the handler an empty one.
	if _, err := conn.Write([]byte("GET /api/v0/hello HTTP/1.0\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusMisdirectedRequest {
		t.Fatalf("missing Host answered %d", response.StatusCode)
	}
}

func TestTerminalUpgradeHostGuard(t *testing.T) {
	h := newHarness(t)
	u := "ws" + strings.TrimPrefix(h.s.BrowserURL(), "http") + terminalPath
	_, response, err := websocket.Dial(context.Background(), u, &websocket.DialOptions{Host: "evil.example:" + h.browserPort(),
		HTTPHeader: http.Header{"Origin": {"http://evil.example:" + h.browserPort()}}})
	if err == nil || response == nil || response.StatusCode != http.StatusMisdirectedRequest {
		t.Fatalf("rebinding upgrade: err %v response %+v", err, response)
	}
}

// expectClose dials the Browser listener and expects the stream to be refused
// with code.
func (h *harness) expectBrowserClose(t *testing.T, name, query string, header http.Header, code websocket.StatusCode) {
	t.Helper()
	conn, err := h.dialBrowser(t, query, header)
	if err != nil {
		t.Fatalf("%s: dial: %v", name, err)
	}
	if got, _ := closeStatus(t, conn); got != code {
		t.Fatalf("%s: close %d, want %d", name, got, code)
	}
}

func TestTerminalCrossSiteHijackingIsRefused(t *testing.T) {
	h := newHarness(t)
	session := "Bearer " + h.pairBrowser()
	const app = "http://app.example:5173"
	h.pairOrigin(app)
	for name, tc := range map[string]struct {
		header http.Header
		code   websocket.StatusCode
	}{
		// Omitting Origin is allowed only with a bearer token (see
		// TestBearerClientsMayOmitOrigin); without one it is refused.
		"no origin, no token": {http.Header{}, CloseOriginRefused},
		// Sandboxed iframes and file: pages send Origin: null.
		"null origin": {http.Header{"Authorization": {session}, "Origin": {"null"}}, CloseOriginRefused},
		// Another port on the same host is same-site; with no cookie there is
		// nothing ambient for it to carry, and its Origin differs anyway.
		"same-site page": {http.Header{"Authorization": {session}, "Origin": {"http://localhost:3000"}}, CloseOriginRefused},
		"rebound origin": {http.Header{"Authorization": {session}, "Origin": {"http://evil.example:" + h.browserPort()}}, CloseOriginRefused},
		// The browser session token is bound to the origin that exchanged it.
		"paired origin with the session token": {http.Header{"Authorization": {session}, "Origin": {app}}, CloseOriginRefused},
		"forged token":                         {http.Header{"Authorization": {"Bearer forged"}, "Origin": {h.ownOrigin()}}, CloseUnauthenticated},
		"no credential":                        {http.Header{"Origin": {h.ownOrigin()}}, CloseUnauthenticated},
	} {
		h.expectBrowserClose(t, name, "", tc.header, tc.code)
	}
	if clients := h.s.status().Clients; len(clients) != 0 {
		t.Fatalf("refused upgrades left clients: %+v", clients)
	}
}

func TestTerminalBearerOnUpgrade(t *testing.T) {
	h := newHarness(t)
	const app = "http://app.example:5173"
	token := h.pairOrigin(app)
	h.pairOrigin("http://second.example")
	h.expectBrowserClose(t, "token from another origin", "", http.Header{"Origin": {"http://second.example"}, "Authorization": {"Bearer " + token}}, CloseOriginRefused)
	h.expectBrowserClose(t, "unknown token", "", http.Header{"Origin": {app}, "Authorization": {"Bearer nope"}}, CloseUnauthenticated)
	h.expectBrowserClose(t, "basic auth", "", http.Header{"Origin": {app}, "Authorization": {"Basic " + token}}, CloseUnauthenticated)
	conn, err := h.dialBrowser(t, "", http.Header{"Origin": {app}, "Authorization": {"Bearer " + token}})
	if err != nil {
		t.Fatal(err)
	}
	writeText(t, conn, `{"bearer":true}`)
	if got := readText(t, conn); got != `{"bearer":true}` {
		t.Fatalf("echo = %q", got)
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")
}

func (h *harness) dialTailnet(t *testing.T, header http.Header) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.Dial(context.Background(), "ws://"+testTailnetHost+terminalPath,
		&websocket.DialOptions{HTTPClient: unixClient(h.s.Endpoint().TailnetSocket), HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

func TestTailnetTerminalNeedsOriginAndLogin(t *testing.T) {
	h := newHarness(t)
	own := "https://" + testTailnetHost
	for name, tc := range map[string]struct {
		header http.Header
		code   websocket.StatusCode
	}{
		"evil origin":   {http.Header{"Origin": {"https://evil.example"}, tailscaleLoginHead: {testTailnetLogin}}, CloseOriginRefused},
		"no origin":     {http.Header{tailscaleLoginHead: {testTailnetLogin}}, CloseOriginRefused},
		"browser port":  {http.Header{"Origin": {h.ownOrigin()}, tailscaleLoginHead: {testTailnetLogin}}, CloseOriginRefused},
		"no login":      {http.Header{"Origin": {own}}, CloseUnauthenticated},
		"foreign login": {http.Header{"Origin": {own}, tailscaleLoginHead: {"intruder@example.com"}}, CloseUnauthenticated},
	} {
		conn := h.dialTailnet(t, tc.header)
		if got, _ := closeStatus(t, conn); got != tc.code {
			t.Fatalf("%s: close %d, want %d", name, got, tc.code)
		}
	}
	conn := h.dialTailnet(t, http.Header{"Origin": {own}, tailscaleLoginHead: {testTailnetLogin}})
	writeText(t, conn, `{"tailnet":true}`)
	if got := readText(t, conn); got != `{"tailnet":true}` {
		t.Fatalf("echo = %q", got)
	}
	status := h.s.status()
	if len(status.Clients) != 1 || status.Clients[0].Auth != "tailnet" || status.Clients[0].Login != testTailnetLogin {
		t.Fatalf("status = %+v", status.Clients)
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")
}

func TestTailnetMutationsNeedOwnOriginAndJSON(t *testing.T) {
	h := newHarness(t)
	login := map[string]string{tailscaleLoginHead: testTailnetLogin}
	own := "https://" + testTailnetHost
	// A malicious page on another tailnet device: the proxy adds the login,
	// the browser adds its own Origin.
	response, data := h.tailnetDo(req{method: http.MethodPost, path: "/api/v0/ws-tickets", body: "{}", header: mutationHeaders("https://evil.example", login)})
	expect(t, response, data, http.StatusForbidden, CodeOriginRefused)
	// A simple (preflight-free) request from the own origin still fails the
	// mutation guard.
	response, data = h.tailnetDo(req{method: http.MethodPost, path: "/api/v0/ws-tickets", body: "{}",
		header: map[string]string{"Origin": own, "Content-Type": "text/plain", tailscaleLoginHead: testTailnetLogin}})
	expect(t, response, data, http.StatusForbidden, CodeMutationRefused)
	response, data = h.tailnetDo(req{method: http.MethodPost, path: "/api/v0/ws-tickets", body: "{}", header: mutationHeaders("", login)})
	expect(t, response, data, http.StatusForbidden, CodeOriginRefused)
	response, data = h.tailnetDo(req{method: http.MethodPost, path: "/api/v0/ws-tickets", body: "{}", header: mutationHeaders(own, login)})
	expect(t, response, data, http.StatusOK, "")
	// GETs that name a foreign origin are refused too, so a cross-origin read
	// never reaches the handler.
	response, data = h.tailnetDo(req{path: "/api/v0/sessions", header: map[string]string{"Origin": "https://evil.example", tailscaleLoginHead: testTailnetLogin}})
	expect(t, response, data, http.StatusForbidden, CodeOriginRefused)
}

func TestTicketIsBoundToItsListener(t *testing.T) {
	h := newHarness(t)
	const app = "http://app.example:5173"
	h.pairOrigin(app)
	// A paired origin takes a ticket on the Tailnet listener...
	response, data := h.tailnetDo(req{method: http.MethodPost, path: "/api/v0/ws-tickets", body: "{}",
		header: mutationHeaders(app, map[string]string{tailscaleLoginHead: testTailnetLogin})})
	expect(t, response, data, http.StatusOK, "")
	var issued TicketResponse
	if err := json.Unmarshal(data, &issued); err != nil {
		t.Fatal(err)
	}
	// ...and cannot spend it on the Browser listener, where no login vouches.
	h.expectBrowserClose(t, "tailnet ticket on browser", "?ticket="+url.QueryEscape(issued.Ticket), http.Header{"Origin": {app}}, CloseUnauthenticated)
}

func TestPairingCodeRedeemsOnceUnderConcurrency(t *testing.T) {
	h := newHarness(t)
	code := h.pairingCode("/")
	body := `{"code":"` + code.Code + `"}`
	const attempts = 24
	var wg sync.WaitGroup
	statuses := make(chan int, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			request, _ := http.NewRequest(http.MethodPost, h.s.BrowserURL()+"/api/v0/pairing/exchange", strings.NewReader(body))
			for key, value := range mutationHeaders(h.ownOrigin(), nil) {
				request.Header.Set(key, value)
			}
			response, err := h.browser.Do(request)
			if err != nil {
				statuses <- 0
				return
			}
			_ = response.Body.Close()
			statuses <- response.StatusCode
		}()
	}
	wg.Wait()
	close(statuses)
	paired := 0
	for status := range statuses {
		switch status {
		case http.StatusOK:
			paired++
		case http.StatusUnauthorized:
		default:
			t.Fatalf("unexpected status %d", status)
		}
	}
	if paired != 1 {
		t.Fatalf("%d of %d concurrent redemptions paired a browser", paired, attempts)
	}
}

func TestTicketRedeemsOnceUnderConcurrency(t *testing.T) {
	store := newAuthStore(time.Now)
	ticket, _, err := store.issueTicket(grant{listener: ListenerBrowser, auth: "session", origin: "http://127.0.0.1:1", client: "session:x"})
	if err != nil {
		t.Fatal(err)
	}
	const attempts = 32
	var wg sync.WaitGroup
	var mu sync.Mutex
	won := 0
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok := store.redeemTicket(ticket); ok {
				mu.Lock()
				won++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if won != 1 {
		t.Fatalf("%d redemptions of one ticket succeeded", won)
	}
}

func TestPairNextEncodedVariantsStayOnThisOrigin(t *testing.T) {
	h := newHarness(t)
	base, _ := url.Parse(h.s.BrowserURL())
	for _, next := range []string{"/%2F%2Fevil.example/", "/%5Cevil.example", "/.//evil.example", "/..//evil.example", "/%09/evil.example", "/?next=//evil.example", "/#//evil.example"} {
		code := h.pairingCode("/")
		response, data := h.exchange(h.ownOrigin(), code.Code, next)
		if response.StatusCode == http.StatusBadRequest {
			continue
		}
		expect(t, response, data, http.StatusOK, "")
		var exchanged PairingExchange
		if err := json.Unmarshal(data, &exchanged); err != nil {
			t.Fatal(err)
		}
		// The page navigates to new URL(next, location.origin) and checks the
		// origin again; the server's answer must already resolve on-origin.
		if strings.HasPrefix(exchanged.Next, "//") || strings.ContainsRune(exchanged.Next, '\\') {
			t.Fatalf("next %q came back as %q", next, exchanged.Next)
		}
		target, err := base.Parse(exchanged.Next)
		if err != nil || target.Host != base.Host || target.Scheme != base.Scheme {
			t.Fatalf("next %q resolves off origin to %q (%v)", next, exchanged.Next, err)
		}
	}
}

func TestStaticUIDoesNotFollowSymlinksOutOfDir(t *testing.T) {
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("top secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	ui := t.TempDir()
	if err := os.WriteFile(filepath.Join(ui, "index.html"), []byte("<p>app</p>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(ui, "leak.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(ui, "out")); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, func(o *Options) { o.UIDir = ui })
	// Static files on the Browser listener need no credential, so anything
	// they reach is readable by every local user.
	for _, path := range []string{"/leak.txt", "/out/secret.txt"} {
		response, data := h.browserDo(req{path: path})
		if strings.Contains(string(data), "top secret") {
			t.Fatalf("%s served a file outside --ui: %d %q", path, response.StatusCode, data)
		}
	}
}

func TestTerminalAbruptDisconnectIsEndOfStream(t *testing.T) {
	h := newHarness(t)
	conn := h.dialLocal(t)
	writeText(t, conn, `{"x":1}`)
	readText(t, conn)
	// No close frame: the TCP/Unix connection just drops.
	_ = conn.CloseNow()
	select {
	case <-h.backend.eof:
	case <-time.After(5 * time.Second):
		t.Fatal("backend never saw EOF after an abrupt disconnect")
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(h.s.status().Clients) != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("client still registered: %+v", h.s.status().Clients)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// On the Tailnet listener the credential is ambient: tailscaled vouches for
// every request from the user's device, framed or not. A page on another site
// that frames the UI could then steer clicks and keys into a live terminal, so
// the UI may only be framed by itself.
func TestStaticUIRefusesCrossSiteFraming(t *testing.T) {
	ui := t.TempDir()
	if err := os.WriteFile(filepath.Join(ui, "index.html"), []byte("<p>app</p>"), 0o644); err != nil {
		t.Fatal(err)
	}
	withUI := newHarness(t, func(o *Options) { o.UIDir = ui })
	plain := newHarness(t)
	check := func(name string, response *http.Response) {
		t.Helper()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d", name, response.StatusCode)
		}
		if csp := response.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'self'") {
			t.Fatalf("%s: Content-Security-Policy = %q", name, csp)
		}
		if xfo := response.Header.Get("X-Frame-Options"); xfo != "SAMEORIGIN" {
			t.Fatalf("%s: X-Frame-Options = %q", name, xfo)
		}
	}
	login := map[string]string{tailscaleLoginHead: testTailnetLogin}
	for _, h := range []*harness{withUI, plain} {
		response, _ := h.tailnetDo(req{path: "/", header: login})
		check("tailnet /", response)
		response, _ = h.tailnetDo(req{path: "/s/aerie/1", header: login})
		check("tailnet fallback", response)
		response, _ = h.browserDo(req{path: "/"})
		check("browser /", response)
	}
}

func (h *harness) revokeSessions(query string) SessionRevocation {
	h.t.Helper()
	response, data := h.localDo(req{method: http.MethodDelete, path: "/api/v0/pairing/sessions" + query})
	expect(h.t, response, data, http.StatusOK, "")
	var revocation SessionRevocation
	if err := json.Unmarshal(data, &revocation); err != nil {
		h.t.Fatal(err)
	}
	return revocation
}

func (h *harness) bearerStatus(token string) int {
	h.t.Helper()
	response, _ := h.browserDo(req{path: "/api/v0/status", header: map[string]string{"Authorization": "Bearer " + token}})
	return response.StatusCode
}

// Browser sessions can be signed out without a restart: one origin's, or
// all of them. A revoked token gets 401, its unused tickets stop working, and
// its open terminals close with 4401. Paired origins are not sessions.
func TestRevokeBrowserSessions(t *testing.T) {
	h := newHarness(t)
	const app = "http://app.example:5173"
	appToken := h.pairOrigin(app)
	numeric := h.pairBrowser() // bound to http://127.0.0.1:<port>
	localhost := strings.Replace(h.ownOrigin(), "127.0.0.1", "localhost", 1)
	response, data := h.exchange(localhost, fragmentValue(t, h.pairingCode("/").URL, "code"), "/")
	expect(t, response, data, http.StatusOK, "")
	var named PairingExchange
	if err := json.Unmarshal(data, &named); err != nil {
		t.Fatal(err)
	}

	sessionTerminal, err := h.dialBrowser(t, "", http.Header{"Authorization": {"Bearer " + numeric}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sessionTerminal.CloseNow() }()
	writeText(t, sessionTerminal, `{"before":true}`)
	if got := readText(t, sessionTerminal); got != `{"before":true}` {
		t.Fatalf("echo = %q", got)
	}
	appTerminal, err := h.dialBrowser(t, "", http.Header{"Authorization": {"Bearer " + appToken}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = appTerminal.CloseNow() }()
	response, data = h.browserDo(req{method: http.MethodPost, path: "/api/v0/ws-tickets", body: "{}", header: mutationHeaders(h.ownOrigin(), map[string]string{"Authorization": "Bearer " + numeric})})
	expect(t, response, data, http.StatusOK, "")
	var ticket TicketResponse
	_ = json.Unmarshal(data, &ticket)
	waitForClients(t, h, 2)

	// Only the localhost origin's session.
	if got := h.revokeSessions("?origin=" + url.QueryEscape(localhost)); got.Revoked != 1 || got.TerminalsClosed != 0 || got.Origin != localhost {
		t.Fatalf("origin revocation = %+v", got)
	}
	if status := h.bearerStatus(named.Token); status != http.StatusUnauthorized {
		t.Fatalf("revoked localhost session = %d", status)
	}
	if status := h.bearerStatus(numeric); status != http.StatusOK {
		t.Fatalf("other origin's session = %d", status)
	}

	// Every session; the open terminal closes at once.
	if got := h.revokeSessions(""); got.Revoked != 1 || got.TerminalsClosed != 1 || got.Origin != "" {
		t.Fatalf("revocation = %+v", got)
	}
	if code, _ := closeStatus(t, sessionTerminal); code != CloseUnauthenticated {
		t.Fatalf("revoked session's terminal closed %d, want 4401", code)
	}
	if status := h.bearerStatus(numeric); status != http.StatusUnauthorized {
		t.Fatalf("revoked session = %d", status)
	}
	h.expectBrowserClose(t, "ticket from a revoked session", "?ticket="+ticket.Ticket, http.Header{"Origin": {h.ownOrigin()}}, CloseUnauthenticated)
	h.expectBrowserClose(t, "revoked session on the upgrade", "", http.Header{"Authorization": {"Bearer " + numeric}}, CloseUnauthenticated)

	// A paired origin is not a browser session.
	if status := h.bearerStatus(appToken); status != http.StatusOK {
		t.Fatalf("paired origin after session revocation = %d", status)
	}
	writeText(t, appTerminal, `{"after":true}`)
	if got := readText(t, appTerminal); got != `{"after":true}` {
		t.Fatalf("paired origin terminal echo = %q", got)
	}

	// Nothing left is fine; the route is Local-only and validates origin.
	if got := h.revokeSessions(""); got.Revoked != 0 {
		t.Fatalf("empty revocation = %+v", got)
	}
	response, data = h.localDo(req{method: http.MethodDelete, path: "/api/v0/pairing/sessions?origin=ftp://nope"})
	expect(t, response, data, http.StatusBadRequest, CodeInvalidRequest)
	response, data = h.localDo(req{method: http.MethodDelete, path: "/api/v0/pairing/sessions?who=all"})
	expect(t, response, data, http.StatusBadRequest, CodeInvalidRequest)
	response, data = h.browserDo(req{method: http.MethodDelete, path: "/api/v0/pairing/sessions", header: mutationHeaders(h.ownOrigin(), map[string]string{"Authorization": "Bearer " + appToken})})
	expect(t, response, data, http.StatusForbidden, CodeLocalOnly)
}

// A revocation that lands between a terminal's authorization and its
// registration must still close it.
func TestRevokedSessionCannotRegisterATerminal(t *testing.T) {
	h := newHarness(t)
	session := h.pairBrowser()
	_, client, ok := h.s.auth.lookupSession(session)
	if !ok || !h.s.auth.sessionClientLive(client) {
		t.Fatal("fresh session is not live")
	}
	h.s.auth.revokeSessions("")
	if h.s.auth.sessionClientLive(client) {
		t.Fatal("revoked session still live")
	}
	if !h.s.auth.sessionClientLive("origin:http://app.example") || !h.s.auth.sessionClientLive("local") {
		t.Fatal("non-session clients must always be live")
	}
}

// Authorization can finish before revocation while the request body has not
// arrived yet. Ticket issuance must recheck that credential atomically.
func TestRevocationWinsOverAnAuthorizedTicketRequest(t *testing.T) {
	h := newHarness(t)
	token := h.pairBrowser()
	c, result := h.s.resolveBearer(token, h.ownOrigin())
	if result != bearerOK {
		t.Fatal("session authorization failed")
	}
	c.listener = ListenerBrowser
	h.revokeSessions("")
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v0/ws-tickets", strings.NewReader("{}"))
	h.s.handleTicket(w, r, c)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("ticket issued after revocation: %d %s", w.Code, w.Body.String())
	}
	h.s.auth.mu.Lock()
	defer h.s.auth.mu.Unlock()
	if len(h.s.auth.tickets) != 0 {
		t.Fatal("revoked session retained an unused ticket")
	}
}
