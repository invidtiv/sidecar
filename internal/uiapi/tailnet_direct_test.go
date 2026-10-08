package uiapi

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/marcus/sidecar/internal/apiservice"
)

const directTestPort = 7861

var (
	directNodeV4 = netip.MustParseAddr("100.64.0.10")
	directNodeV6 = netip.MustParseAddr("fd7a:115c:a1e0::10")
	loopbackPeer = netip.MustParseAddr("127.0.0.1")
)

// fakeTailnet stands in for Tailscale: the node, whois, certificates, serve
// routes and binding. Every tailnet address binds a fresh loopback port, so
// test clients connect from 127.0.0.1, which whois maps to peer.
type fakeTailnet struct {
	t     *testing.T
	clock func() time.Time

	mu         sync.Mutex
	node       TailnetNode
	nodeErr    error
	peers      map[netip.Addr]TailnetPeer
	whoisErr   error
	whoisCalls int
	serveHeld  bool
	certPEM    []byte
	keyPEM     []byte
	certErr    error
	certCalls  int
	listens    []string
	listenErr  error
}

func newFakeTailnet(t *testing.T, clock func() time.Time) *fakeTailnet {
	f := &fakeTailnet{t: t, clock: clock,
		node:  TailnetNode{Host: testTailnetHost, OwnerLogin: testTailnetLogin, Addresses: []netip.Addr{directNodeV4, directNodeV6}},
		peers: map[netip.Addr]TailnetPeer{loopbackPeer: {Login: testTailnetLogin, Device: "laptop"}}}
	f.certPEM, f.keyPEM = testCertificate(t, testTailnetHost, clock().Add(60*24*time.Hour))
	return f
}

func (f *fakeTailnet) adapters() TailnetAdapters {
	return TailnetAdapters{
		Node: func(context.Context) (TailnetNode, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			node := f.node
			node.Addresses = append([]netip.Addr(nil), f.node.Addresses...)
			return node, f.nodeErr
		},
		Whois: func(_ context.Context, addr netip.Addr) (TailnetPeer, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.whoisCalls++
			if f.whoisErr != nil {
				return TailnetPeer{}, f.whoisErr
			}
			peer, ok := f.peers[addr]
			if !ok {
				return TailnetPeer{}, errors.New("peer not found")
			}
			return peer, nil
		},
		Cert: func(_ context.Context, host string) ([]byte, []byte, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.certCalls++
			if f.certErr != nil {
				return nil, nil, f.certErr
			}
			return f.certPEM, f.keyPEM, nil
		},
		ServeHolds: func(context.Context, int) (bool, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.serveHeld, nil
		},
		Listen: func(network, address string) (net.Listener, error) {
			f.mu.Lock()
			f.listens = append(f.listens, address)
			err := f.listenErr
			f.mu.Unlock()
			if err != nil {
				return nil, &net.OpError{Op: "listen", Net: network, Err: err}
			}
			return net.Listen("tcp", "127.0.0.1:0")
		},
	}
}

func (f *fakeTailnet) set(fn func(f *fakeTailnet)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *fakeTailnet) count(fn func(f *fakeTailnet) int) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fn(f)
}

func testCertificate(t *testing.T, host string, notAfter time.Time) ([]byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: host}, DNSNames: []string{host},
		NotBefore: notAfter.Add(-90 * 24 * time.Hour), NotAfter: notAfter, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

type directHarness struct {
	*harness
	fake *fakeTailnet
}

func newDirectHarness(t *testing.T, setup ...func(*fakeTailnet)) *directHarness {
	t.Helper()
	var fake *fakeTailnet
	h := newHarness(t, func(o *Options) {
		fake = newFakeTailnet(t, o.Now)
		for _, fn := range setup {
			fn(fake)
		}
		o.Tailnet = &TailnetOptions{Mode: TailnetModeDirect, Adapters: fake.adapters(), pollInterval: 20 * time.Millisecond, minBackoff: 10 * time.Millisecond}
	})
	return &directHarness{harness: h, fake: fake}
}

// addr is the loopback address the first tailnet address was mapped onto.
func (h *directHarness) addr() string {
	h.t.Helper()
	for _, listener := range h.s.status().Listeners {
		if listener.Name == ListenerTailnet {
			return listener.Address
		}
	}
	h.t.Fatalf("no tailnet listener in %+v", h.s.TailnetStatus())
	return ""
}

func (h *directHarness) client() *http.Client {
	pool := x509.NewCertPool()
	h.fake.mu.Lock()
	pool.AppendCertsFromPEM(h.fake.certPEM)
	h.fake.mu.Unlock()
	address := h.addr()
	return &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: testTailnetHost},
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", address)
		}}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (h *directHarness) directDo(r req) (*http.Response, []byte) {
	return h.do(h.client(), fmt.Sprintf("https://%s:%d", testTailnetHost, directTestPort), r)
}

func (h *directHarness) dial(t *testing.T, header http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	return websocket.Dial(context.Background(), fmt.Sprintf("wss://%s:%d%s", testTailnetHost, directTestPort, terminalPath),
		&websocket.DialOptions{HTTPClient: h.client(), HTTPHeader: header})
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func directOrigin() string { return fmt.Sprintf("https://%s:%d", testTailnetHost, directTestPort) }

func TestDirectTailnetAdmitsAnAllowedLogin(t *testing.T) {
	h := newDirectHarness(t)
	status := h.s.TailnetStatus()
	if status.Mode != TailnetModeDirect || status.State != TailnetStateListening || status.Origin != directOrigin() ||
		strings.Join(status.Logins, ",") != testTailnetLogin || len(status.Addresses) != 2 || status.CertificateExpiresAt == nil {
		t.Fatalf("status = %+v", status)
	}
	// Both tailnet addresses were bound on the configured port.
	listens := strings.Join(h.fake.listens, " ")
	if listens != "100.64.0.10:7861 [fd7a:115c:a1e0::10]:7861" {
		t.Fatalf("listens = %q", listens)
	}
	response, data := h.directDo(req{path: "/api/v0/sessions"})
	expect(t, response, data, http.StatusOK, "")
	response, data = h.directDo(req{path: "/"})
	expect(t, response, data, http.StatusOK, "")
	if response.Header.Get("X-Frame-Options") != "SAMEORIGIN" {
		t.Fatalf("static UI is frameable: %v", response.Header)
	}
	// The connection's whois identity is the client's login.
	conn, _, err := h.dial(t, http.Header{"Origin": {directOrigin()}})
	if err != nil {
		t.Fatal(err)
	}
	writeText(t, conn, `{"direct":true}`)
	if got := readText(t, conn); got != `{"direct":true}` {
		t.Fatalf("echo = %q", got)
	}
	clients := h.s.status().Clients
	if len(clients) != 1 || clients[0].Auth != "tailnet" || clients[0].Login != testTailnetLogin || clients[0].Listener != ListenerTailnet {
		t.Fatalf("clients = %+v", clients)
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")
	// The status route on the Local socket carries the same report.
	response, data = h.localDo(req{path: "/api/v0/status"})
	expect(t, response, data, http.StatusOK, "")
	var decoded Status
	if err := json.Unmarshal(data, &decoded); err != nil || decoded.Tailnet == nil || decoded.Tailnet.State != TailnetStateListening {
		t.Fatalf("status route = %s (%v)", data, err)
	}
}

func TestDirectTailnetRefusesUnidentifiedPeers(t *testing.T) {
	for name, tc := range map[string]struct {
		setup  func(f *fakeTailnet)
		status int
		code   string
	}{
		"login not allowed": {func(f *fakeTailnet) {
			f.peers[loopbackPeer] = TailnetPeer{Login: "intruder@example.com", Device: "theirs"}
		}, http.StatusForbidden, CodeLoginRefused},
		"tagged device": {func(f *fakeTailnet) {
			f.peers[loopbackPeer] = TailnetPeer{Login: testTailnetLogin, Device: "ci", Tags: []string{"tag:ci"}}
		}, http.StatusForbidden, CodeLoginRefused},
		"whois fails":     {func(f *fakeTailnet) { f.whoisErr = errors.New("tailscaled unavailable") }, http.StatusUnauthorized, CodeUnauthenticated},
		"unknown address": {func(f *fakeTailnet) { delete(f.peers, loopbackPeer) }, http.StatusUnauthorized, CodeUnauthenticated},
		"own address": {func(f *fakeTailnet) {
			f.node.Addresses = []netip.Addr{loopbackPeer}
		}, http.StatusForbidden, CodeLoginRefused},
	} {
		t.Run(name, func(t *testing.T) {
			h := newDirectHarness(t, tc.setup)
			for _, path := range []string{"/api/v0/sessions", "/", "/api/v0/hello", "/pair", "/api/v0/pairing/exchange"} {
				response, data := h.directDo(req{path: path, header: map[string]string{"Origin": directOrigin(), tailscaleLoginHead: testTailnetLogin}})
				expect(t, response, data, tc.status, tc.code)
			}
			if _, response, err := h.dial(t, http.Header{"Origin": {directOrigin()}, tailscaleLoginHead: {testTailnetLogin}}); err == nil || response == nil || response.StatusCode != tc.status {
				t.Fatalf("terminal upgrade was not refused: %v %v", response, err)
			}
			if name == "own address" && h.fake.count(func(f *fakeTailnet) int { return f.whoisCalls }) != 0 {
				t.Fatal("whois ran for the node's own address")
			}
		})
	}
}

// Direct mode's identity is the connection's, so the header tailscale serve
// would add is meaningless here: it neither grants nor changes a login.
func TestDirectTailnetIgnoresTheLoginHeader(t *testing.T) {
	h := newDirectHarness(t, func(f *fakeTailnet) { f.peers[loopbackPeer] = TailnetPeer{Login: "intruder@example.com"} })
	response, data := h.directDo(req{path: "/api/v0/sessions", header: map[string]string{tailscaleLoginHead: testTailnetLogin}})
	expect(t, response, data, http.StatusForbidden, CodeLoginRefused)

	h = newDirectHarness(t)
	response, data = h.directDo(req{path: "/api/v0/sessions", header: map[string]string{tailscaleLoginHead: "intruder@example.com"}})
	expect(t, response, data, http.StatusOK, "")
	conn, _, err := h.dial(t, http.Header{"Origin": {directOrigin()}, tailscaleLoginHead: {"someone-else@example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	writeText(t, conn, `{"x":1}`)
	_ = readText(t, conn)
	if clients := h.s.status().Clients; len(clients) != 1 || clients[0].Login != testTailnetLogin {
		t.Fatalf("clients = %+v", clients)
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")
}

func TestDirectTailnetKeepsTheTailnetGuards(t *testing.T) {
	h := newDirectHarness(t)
	own := directOrigin()
	// Host guard: only <magicdns>:<port>.
	for _, host := range []string{testTailnetHost, testTailnetHost + ":443", "evil.example:7861", "100.64.0.10:7861"} {
		response, data := h.directDo(req{path: "/api/v0/sessions", host: host})
		expect(t, response, data, http.StatusMisdirectedRequest, CodeHostRefused)
	}
	// Origin guard, including a GET naming a foreign origin and the serve-mode
	// http:// origin, which direct mode never serves.
	for _, origin := range []string{"https://evil.example", "http://" + testTailnetHost, "https://" + testTailnetHost} {
		response, data := h.directDo(req{path: "/api/v0/sessions", header: map[string]string{"Origin": origin}})
		expect(t, response, data, http.StatusForbidden, CodeOriginRefused)
	}
	// Mutations need an Origin: the identity is ambient.
	response, data := h.directDo(req{method: http.MethodPost, path: "/api/v0/ws-tickets", body: "{}", header: mutationHeaders("", nil)})
	expect(t, response, data, http.StatusForbidden, CodeOriginRefused)
	response, data = h.directDo(req{method: http.MethodPost, path: "/api/v0/ws-tickets", body: "{}", header: map[string]string{"Origin": own, "Content-Type": "text/plain"}})
	expect(t, response, data, http.StatusForbidden, CodeMutationRefused)
	response, data = h.directDo(req{method: http.MethodPost, path: "/api/v0/ws-tickets", body: "{}", header: mutationHeaders(own, nil)})
	expect(t, response, data, http.StatusOK, "")
	// Upgrades need an allowed Origin.
	for _, header := range []http.Header{{}, {"Origin": {"https://evil.example"}}} {
		conn, _, err := h.dial(t, header)
		if err != nil {
			t.Fatal(err)
		}
		if code, _ := closeStatus(t, conn); code != CloseOriginRefused {
			t.Fatalf("upgrade with %v: close %d", header, code)
		}
	}
	// Local-only routes stay local.
	response, data = h.directDo(req{method: http.MethodPost, path: "/api/v0/pairing/codes", body: "{}", header: mutationHeaders(own, nil)})
	expect(t, response, data, http.StatusForbidden, CodeLocalOnly)
	// A paired origin still needs its own bearer.
	const app = "http://localhost:5173"
	token := h.pairOrigin(app)
	response, data = h.directDo(req{path: "/api/v0/sessions", header: map[string]string{"Origin": app}})
	expect(t, response, data, http.StatusUnauthorized, CodeUnauthenticated)
	response, data = h.directDo(req{path: "/api/v0/sessions", header: map[string]string{"Origin": app, "Authorization": "Bearer " + token}})
	expect(t, response, data, http.StatusOK, "")
}

func TestDirectTailnetPerLoginTerminalLimit(t *testing.T) {
	h := newDirectHarness(t)
	var conns []*websocket.Conn
	for i := 0; i < maxTerminalsPerClient; i++ {
		conn, _, err := h.dial(t, http.Header{"Origin": {directOrigin()}})
		if err != nil {
			t.Fatal(err)
		}
		writeText(t, conn, `{"n":1}`)
		_ = readText(t, conn)
		conns = append(conns, conn)
	}
	extra, _, err := h.dial(t, http.Header{"Origin": {directOrigin()}})
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := closeStatus(t, extra); code != CloseTooManyTerminals {
		t.Fatalf("terminal over the per-login limit: close %d", code)
	}
	for _, conn := range conns {
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}
}

func TestWhoisCacheExpiresAndCoalesces(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)}
	var mu sync.Mutex
	calls := 0
	release := make(chan struct{})
	fail := false
	cache := newWhoisCache(func(context.Context, netip.Addr) (TailnetPeer, error) {
		<-release
		mu.Lock()
		defer mu.Unlock()
		calls++
		if fail {
			return TailnetPeer{}, errors.New("down")
		}
		return TailnetPeer{Login: testTailnetLogin}, nil
	}, clock.Now)
	addr := netip.MustParseAddr("100.64.0.20")
	// A burst of connections from one address costs one lookup.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if peer, err := cache.get(context.Background(), addr); err != nil || peer.Login != testTailnetLogin {
				t.Errorf("get = %+v, %v", peer, err)
			}
		}()
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	count := func() int { mu.Lock(); defer mu.Unlock(); return calls }
	if count() != 1 {
		t.Fatalf("burst made %d lookups", count())
	}
	clock.Advance(tailnetWhoisTTL - time.Second)
	_, _ = cache.get(context.Background(), addr)
	if count() != 1 {
		t.Fatal("a fresh answer was looked up again")
	}
	// After the TTL the address is asked about again; the answer may differ.
	clock.Advance(2 * time.Second)
	mu.Lock()
	fail = true
	mu.Unlock()
	if _, err := cache.get(context.Background(), addr); err == nil || count() != 2 {
		t.Fatalf("expired entry: err %v, %d lookups", err, count())
	}
	// Failures are remembered briefly, then retried.
	_, _ = cache.get(context.Background(), addr)
	if count() != 2 {
		t.Fatal("a failure was not cached")
	}
	clock.Advance(tailnetWhoisNegativeTTL + time.Second)
	mu.Lock()
	fail = false
	mu.Unlock()
	if peer, err := cache.get(context.Background(), addr); err != nil || peer.Login != testTailnetLogin || count() != 3 {
		t.Fatalf("after negative TTL: %+v %v, %d lookups", peer, err, count())
	}
}

func TestDirectTailnetCachesWhoisPerAddress(t *testing.T) {
	h := newDirectHarness(t)
	for i := 0; i < 5; i++ {
		response, data := h.directDo(req{path: "/api/v0/hello"})
		expect(t, response, data, http.StatusOK, "")
	}
	if calls := h.fake.count(func(f *fakeTailnet) int { return f.whoisCalls }); calls != 1 {
		t.Fatalf("whois ran %d times for one address", calls)
	}
	// Once the answer expires the device is identified again, and a change
	// (here: the device was tagged) takes effect.
	h.fake.set(func(f *fakeTailnet) {
		f.peers[loopbackPeer] = TailnetPeer{Login: testTailnetLogin, Tags: []string{"tag:server"}}
	})
	h.clock.Advance(tailnetWhoisTTL + time.Second)
	response, data := h.directDo(req{path: "/api/v0/hello"})
	expect(t, response, data, http.StatusForbidden, CodeLoginRefused)
}

func TestDirectTailnetRebindsWhenTheAddressChanges(t *testing.T) {
	h := newDirectHarness(t)
	before := h.addr()
	h.fake.set(func(f *fakeTailnet) { f.node.Addresses = []netip.Addr{netip.MustParseAddr("100.64.0.11")} })
	waitFor(t, "rebind", func() bool {
		status := h.s.TailnetStatus()
		return status.State == TailnetStateListening && len(status.Addresses) == 1 && status.Addresses[0] != before
	})
	if !strings.Contains(strings.Join(h.fake.listens, " "), "100.64.0.11:7861") {
		t.Fatalf("listens = %v", h.fake.listens)
	}
	if conn, err := net.DialTimeout("tcp", before, time.Second); err == nil {
		_ = conn.Close()
		t.Fatal("the old address is still bound")
	}
	response, data := h.directDo(req{path: "/api/v0/hello"})
	expect(t, response, data, http.StatusOK, "")
	// The new address is now one of the node's own.
	if !h.s.tailnetTrustSnapshot().own[netip.MustParseAddr("100.64.0.11")] || h.s.tailnetTrustSnapshot().own[directNodeV4] {
		t.Fatal("own addresses did not follow the node")
	}
}

func TestDirectTailnetSurvivesTailscaleStopping(t *testing.T) {
	h := newDirectHarness(t)
	before := h.addr()
	h.fake.set(func(f *fakeTailnet) { f.nodeErr = errors.New("Tailscale is Stopped") })
	waitFor(t, "waiting state", func() bool { return h.s.TailnetStatus().State == TailnetStateWaiting })
	status := h.s.TailnetStatus()
	if len(status.Addresses) != 0 || !strings.Contains(status.Message, "Tailscale is Stopped") || status.NextAttemptAt == nil {
		t.Fatalf("status = %+v", status)
	}
	if conn, err := net.DialTimeout("tcp", before, time.Second); err == nil {
		_ = conn.Close()
		t.Fatal("still bound while Tailscale is down")
	}
	// The other listeners are untouched.
	response, data := h.localDo(req{path: "/api/v0/hello"})
	expect(t, response, data, http.StatusOK, "")
	h.fake.set(func(f *fakeTailnet) { f.nodeErr = nil })
	waitFor(t, "listening again", func() bool { return h.s.TailnetStatus().State == TailnetStateListening })
	response, data = h.directDo(req{path: "/api/v0/hello"})
	expect(t, response, data, http.StatusOK, "")
}

func TestDirectTailnetStartsWhileTailscaleIsDown(t *testing.T) {
	h := newDirectHarness(t, func(f *fakeTailnet) { f.nodeErr = errors.New("the tailscale CLI is not on PATH") })
	if state := h.s.TailnetStatus().State; state != TailnetStateWaiting {
		t.Fatalf("state = %s", state)
	}
	response, data := h.browserDo(req{path: "/api/v0/hello"})
	expect(t, response, data, http.StatusUnauthorized, CodeUnauthenticated)
	h.fake.set(func(f *fakeTailnet) { f.nodeErr = nil })
	waitFor(t, "listening", func() bool { return h.s.TailnetStatus().State == TailnetStateListening })
}

func TestDirectTailnetRefusesAPortHeldByServe(t *testing.T) {
	h := newDirectHarness(t, func(f *fakeTailnet) { f.serveHeld = true })
	status := h.s.TailnetStatus()
	if status.State != TailnetStatePortHeld || !strings.Contains(status.Message, "tailscale serve --https=7861 off") || len(status.Addresses) != 0 {
		t.Fatalf("status = %+v", status)
	}
	if len(h.fake.listens) != 0 {
		t.Fatalf("bound while serve holds the port: %v", h.fake.listens)
	}
	// Removing the route is enough; no restart.
	h.fake.set(func(f *fakeTailnet) { f.serveHeld = false })
	waitFor(t, "listening", func() bool { return h.s.TailnetStatus().State == TailnetStateListening })
}

func TestDirectTailnetNamesTheServeCommandWhenThePortIsInUse(t *testing.T) {
	h := newDirectHarness(t, func(f *fakeTailnet) { f.listenErr = syscall.EADDRINUSE })
	status := h.s.TailnetStatus()
	if status.State != TailnetStateBindFailed || !strings.Contains(status.Message, "tailscale serve --https=7861 off") {
		t.Fatalf("status = %+v", status)
	}
}

func TestDirectTailnetCertificateIsPrivateAndReloads(t *testing.T) {
	h := newDirectHarness(t)
	dir := filepath.Join(Dir(h.state), "tls", "tailnet")
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("tls dir: %v %v", info, err)
	}
	for _, name := range []string{testTailnetHost + ".crt", testTailnetHost + ".key"} {
		if info, err := os.Stat(filepath.Join(dir, name)); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", name, info, err)
		}
	}
	served := func() *x509.Certificate {
		conn, err := tls.Dial("tcp", h.addr(), &tls.Config{ServerName: testTailnetHost, InsecureSkipVerify: true}) //nolint:gosec // reading the served certificate
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close() }()
		return conn.ConnectionState().PeerCertificates[0]
	}
	first := served()
	// Tailscale renews its copy; the next check picks it up without a restart.
	renewedCert, renewedKey := testCertificate(t, testTailnetHost, h.clock.Now().Add(90*24*time.Hour))
	h.fake.set(func(f *fakeTailnet) { f.certPEM, f.keyPEM = renewedCert, renewedKey })
	h.clock.Advance(tailnetCertInterval + time.Minute)
	waitFor(t, "renewed certificate", func() bool { return served().SerialNumber.Cmp(first.SerialNumber) != 0 })
	if stored, _ := os.ReadFile(filepath.Join(dir, testTailnetHost+".crt")); string(stored) != string(renewedCert) {
		t.Fatal("the renewed certificate was not stored")
	}
	// A failed refresh keeps serving a certificate that is still valid.
	h.fake.set(func(f *fakeTailnet) { f.certErr = errors.New("acme down") })
	h.clock.Advance(tailnetCertInterval + time.Minute)
	waitFor(t, "a refresh attempt", func() bool { return h.fake.count(func(f *fakeTailnet) int { return f.certCalls }) >= 3 })
	if state := h.s.TailnetStatus().State; state != TailnetStateListening {
		t.Fatalf("state after failed refresh = %s", state)
	}
}

func TestDirectTailnetWithoutACertificateWaits(t *testing.T) {
	h := newDirectHarness(t, func(f *fakeTailnet) { f.certErr = errors.New("HTTPS certificates are disabled") })
	status := h.s.TailnetStatus()
	if status.State != TailnetStateCertFailed || !strings.Contains(status.Message, "HTTPS certificates are disabled") || len(h.fake.listens) != 0 {
		t.Fatalf("status = %+v listens %v", status, h.fake.listens)
	}
}

func TestDirectTailnetUsesAStoredCertificateWhenTailscaleCannotIssue(t *testing.T) {
	state := shortTempDir(t)
	fake := newFakeTailnet(t, time.Now)
	dir := filepath.Join(Dir(state), "tls", "tailnet")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, testTailnetHost+".crt"), fake.certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, testTailnetHost+".key"), fake.keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	fake.certErr = errors.New("offline")
	s, err := Start(Options{StateDir: state, Backend: newFakeBackend(), Inherited: []apiservice.ActivatedListener{},
		Tailnet: &TailnetOptions{Mode: TailnetModeDirect, Adapters: fake.adapters(), pollInterval: 20 * time.Millisecond, minBackoff: 10 * time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Shutdown(context.Background()) }()
	if state := s.TailnetStatus().State; state != TailnetStateListening {
		t.Fatalf("state = %s (%s)", state, s.TailnetStatus().Message)
	}
}

func TestDirectTailnetShutdownClosesItsListeners(t *testing.T) {
	h := newDirectHarness(t)
	address := h.addr()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if conn, err := net.DialTimeout("tcp", address, time.Second); err == nil {
		_ = conn.Close()
		t.Fatal("tailnet listener survived shutdown")
	}
}

func TestDirectTailnetRefusesTheServePortFallback(t *testing.T) {
	_, err := Start(Options{StateDir: shortTempDir(t), Backend: newFakeBackend(), Inherited: []apiservice.ActivatedListener{},
		Tailnet: &TailnetOptions{Mode: TailnetModeDirect, Port: 7862, Adapters: newFakeTailnet(t, time.Now).adapters()}})
	if err == nil || !strings.Contains(err.Error(), "--tailnet-port") {
		t.Fatalf("err = %v", err)
	}
}

// The service manager hands over Browser and Local; direct mode binds the
// tailnet itself, so those descriptors validate exactly as before and a
// Tailnet descriptor is refused.
func TestActivatedListenersInDirectMode(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "sc-direct-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(root) }()
	if err := os.MkdirAll(Dir(root), 0o700); err != nil {
		t.Fatal(err)
	}
	opts := Options{StateDir: root, Port: 0, Tailnet: &TailnetOptions{Mode: TailnetModeDirect, HTTPSPort: 7861}}
	browser, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = browser.Close() }()
	localPath := filepath.Join(Dir(root), localSocketName)
	local, err := listenPrivateUnix(localPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = local.Close() }()
	inherited, err := validateActivated(opts, []apiservice.ActivatedListener{{Name: "browser", Listener: browser}, {Name: "local", Listener: local}})
	if err != nil || inherited[ListenerBrowser] == nil || inherited[ListenerLocal] == nil || inherited[ListenerTailnet] != nil {
		t.Fatalf("inherited = %v, %v", inherited, err)
	}
	tailnetPath := filepath.Join(Dir(root), tailnetSockName)
	tailnet, err := listenPrivateUnix(tailnetPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tailnet.Close() }()
	if _, err := validateActivated(opts, []apiservice.ActivatedListener{{Name: "tailnet", Listener: tailnet}}); err == nil {
		t.Fatal("direct mode accepted a manager Tailnet socket")
	}
	if _, err := validateActivated(opts, []apiservice.ActivatedListener{{Listener: tailnet}}); err == nil {
		t.Fatal("direct mode accepted an unnamed Tailnet socket")
	}
}

func TestParseTailscaleNode(t *testing.T) {
	node, err := ParseTailscaleNode([]byte(`{"BackendState":"Running","Self":{"DNSName":"Aerie.tail.ts.net.","UserID":7,"TailscaleIPs":["100.89.245.23","fd7a:115c:a1e0::6139:f517"]},"User":{"7":{"LoginName":"marcus@example.com"}}}`))
	if err != nil || node.Host != "aerie.tail.ts.net" || node.OwnerLogin != "marcus@example.com" || len(node.Addresses) != 2 || node.Addresses[0] != netip.MustParseAddr("100.89.245.23") {
		t.Fatalf("node = %+v, %v", node, err)
	}
	for _, data := range []string{
		`{"BackendState":"Stopped","Self":{"DNSName":"a.ts.net.","TailscaleIPs":["100.64.0.1"]}}`,
		`{"BackendState":"Running","Self":{"DNSName":"a.ts.net.","TailscaleIPs":[]}}`,
		`{"BackendState":"Running","Self":{"DNSName":""}}`,
		`not json`,
	} {
		if _, err := ParseTailscaleNode([]byte(data)); err == nil {
			t.Fatalf("accepted %s", data)
		}
	}
}

func TestParseTailscaleWhois(t *testing.T) {
	peer, err := ParseTailscaleWhois([]byte(`{"Node":{"Name":"marcusbook-pro.tail.ts.net.","ComputedName":"marcusbook-pro","Tags":null},"UserProfile":{"LoginName":"marcus@example.com"}}`))
	if err != nil || peer.Login != "marcus@example.com" || peer.Device != "marcusbook-pro" || len(peer.Tags) != 0 {
		t.Fatalf("peer = %+v, %v", peer, err)
	}
	peer, err = ParseTailscaleWhois([]byte(`{"Node":{"Name":"ci.tail.ts.net.","Tags":["tag:ci"]},"UserProfile":{"LoginName":"tagged-devices"}}`))
	if err != nil || peer.Device != "ci" || len(peer.Tags) != 1 {
		t.Fatalf("tagged = %+v, %v", peer, err)
	}
	for _, data := range []string{``, `{"Node":{}}`, `{"Node":{},"UserProfile":{"LoginName":""}}`} {
		if _, err := ParseTailscaleWhois([]byte(data)); err == nil {
			t.Fatalf("accepted %q", data)
		}
	}
}

func TestParseServeHoldsAndSplitPEM(t *testing.T) {
	status := []byte(`{"TCP":{"443":{"HTTPS":true},"7861":{"HTTPS":true}},"Web":{}}`)
	if held, err := ParseServeHolds(status, 7861); err != nil || !held {
		t.Fatalf("7861: %v %v", held, err)
	}
	if held, err := ParseServeHolds(status, 7867); err != nil || held {
		t.Fatalf("7867: %v %v", held, err)
	}
	if held, err := ParseServeHolds(nil, 7861); err != nil || held {
		t.Fatalf("empty: %v %v", held, err)
	}
	certPEM, keyPEM := testCertificate(t, testTailnetHost, time.Now().Add(time.Hour))
	gotCert, gotKey, err := splitPEM(append(append([]byte(nil), certPEM...), keyPEM...))
	if err != nil || string(gotCert) != string(certPEM) || string(gotKey) != string(keyPEM) {
		t.Fatalf("splitPEM: %v", err)
	}
	if _, _, err := splitPEM(certPEM); err == nil {
		t.Fatal("accepted a certificate without a key")
	}
}
