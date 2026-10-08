package uiapi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Direct mode: the Tailnet listener binds the node's own tailnet addresses,
// terminates TLS with the node's `tailscale cert` certificate, and learns who
// is calling from the WireGuard source address of each connection. No header
// carries identity in this mode, so neither a web page nor a LAN device can
// claim a login: the address a packet arrived from over the tailnet is the
// only thing whois answers for.

// Direct-mode states reported in status.
const (
	TailnetStateStarting   = "starting"
	TailnetStateWaiting    = "waiting_for_tailscale"
	TailnetStatePortHeld   = "port_held"
	TailnetStateCertFailed = "certificate_unavailable"
	TailnetStateBindFailed = "bind_failed"
	TailnetStateListening  = "listening"
	TailnetStateStopped    = "stopped"
)

const (
	tailnetPollInterval     = 30 * time.Second
	tailnetMinBackoff       = 2 * time.Second
	tailnetMaxBackoff       = time.Minute
	tailnetCertInterval     = 12 * time.Hour
	tailnetWhoisTTL         = 30 * time.Second
	tailnetWhoisNegativeTTL = 5 * time.Second
	tailnetWhoisTimeout     = 5 * time.Second
	tailnetWhoisConcurrency = 4
	tailnetWhoisMaxEntries  = 512
	tailnetNodeTimeout      = 10 * time.Second
	tailnetCertTimeout      = 2 * time.Minute
	tailnetFirstAttemptWait = 20 * time.Second
)

// TailnetStatus is the Tailnet listener's state in GET /api/v0/status.
type TailnetStatus struct {
	Mode TailnetMode `json:"mode"`
	// State is listening once the listener serves; direct mode also reports
	// starting, waiting_for_tailscale, port_held, certificate_unavailable,
	// bind_failed and stopped.
	State string `json:"state"`
	// Message says what is wrong and what to do while State is not listening.
	Message string `json:"message,omitempty"`
	Host    string `json:"host,omitempty"`
	// Origin is the listener's own origin.
	Origin string `json:"origin,omitempty"`
	// Port is the tailnet HTTPS port direct mode binds.
	Port int `json:"port,omitempty"`
	// Addresses are the tailnet addresses direct mode is bound to.
	Addresses []string `json:"addresses,omitempty"`
	Logins    []string `json:"logins,omitempty"`
	// CertificateExpiresAt is the served certificate's expiry (direct mode).
	CertificateExpiresAt *time.Time `json:"certificate_expires_at,omitempty"`
	// Since is when State last changed.
	Since time.Time `json:"since"`
	// NextAttemptAt is when direct mode next retries, while not listening.
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
}

// tailnetTrust is one immutable snapshot of what the Tailnet listener trusts.
// Serve mode sets it once; direct mode replaces it when the node changes.
type tailnetTrust struct {
	hosts     map[string]bool
	origins   map[string]bool
	logins    map[string]bool
	publicURL string
	host      string
	// own holds the node's own tailnet addresses (direct mode only).
	own map[netip.Addr]bool
}

func (s *Server) tailnetTrustSnapshot() *tailnetTrust {
	if trust := s.tailnet.Load(); trust != nil {
		return trust
	}
	return &tailnetTrust{}
}

type tailnetPeerKey struct{}
type tailnetConnKey struct{}

func tailnetPeerFrom(ctx context.Context) (TailnetPeer, bool) {
	peer, ok := ctx.Value(tailnetPeerKey{}).(TailnetPeer)
	return peer, ok
}

type tailnetDirect struct {
	s        *Server
	logins   []string // configured; empty means the node owner
	port     int
	adapters TailnetAdapters
	tlsDir   string
	whois    *whoisCache
	http     *http.Server
	handler  *listenerHandler

	pollInterval time.Duration
	minBackoff   time.Duration
	certInterval time.Duration

	cert      tlsCertHolder
	certCheck time.Time
	certHost  string

	mu         sync.Mutex
	status     TailnetStatus
	node       TailnetNode
	listeners  []net.Listener
	bound      []ListenerInfo
	generation int
	backoff    time.Duration

	kick  chan struct{}
	done  chan struct{}
	first chan struct{}
}

type tlsCertHolder struct {
	mu   sync.RWMutex
	cert *tls.Certificate
	leaf *x509.Certificate
}

func (h *tlsCertHolder) get() (*tls.Certificate, *x509.Certificate) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.cert, h.leaf
}

func (h *tlsCertHolder) set(cert *tls.Certificate, leaf *x509.Certificate) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cert, h.leaf = cert, leaf
}

func (s *Server) prepareTailnetDirect(opts TailnetOptions) error {
	if opts.Port != 0 {
		return errors.New("ui api: --tailnet-port serves the serve-mode socket fallback; direct mode binds the tailnet address itself")
	}
	port := opts.HTTPSPort
	if port == 0 {
		port = DefaultPort
	}
	if port < 1 || port > 65535 {
		return errors.New("api.tailnetHTTPSPort must be a number from 1 to 65535")
	}
	d := &tailnetDirect{s: s, port: port, adapters: opts.Adapters.withDefaults(), tlsDir: filepath.Join(s.dir, "tls", "tailnet"),
		pollInterval: opts.pollInterval, minBackoff: opts.minBackoff, certInterval: tailnetCertInterval,
		kick: make(chan struct{}, 1), done: make(chan struct{}), first: make(chan struct{})}
	for _, login := range opts.Logins {
		if login = strings.TrimSpace(login); login != "" {
			d.logins = append(d.logins, login)
		}
	}
	if d.pollInterval <= 0 {
		d.pollInterval = tailnetPollInterval
	}
	if d.minBackoff <= 0 {
		d.minBackoff = tailnetMinBackoff
	}
	d.whois = newWhoisCache(d.adapters.Whois, s.opts.Now)
	d.status = TailnetStatus{Mode: TailnetModeDirect, State: TailnetStateStarting, Port: port, Logins: append([]string(nil), d.logins...), Since: s.opts.Now().UTC()}
	d.handler = &listenerHandler{s: s, kind: ListenerTailnet, routes: s.routeTable(), direct: d}
	d.http = &http.Server{Handler: d.handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute,
		BaseContext: func(net.Listener) context.Context { return s.ctx },
		ConnContext: func(ctx context.Context, conn net.Conn) context.Context {
			return context.WithValue(ctx, tailnetConnKey{}, conn.RemoteAddr())
		},
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: d.getCertificate},
		ErrorLog:  log.New(logfWriter(func(line string) { s.opts.Logf("tailnet listener: %s", line) }), "", 0)}
	s.direct = d
	return nil
}

type logfWriter func(string)

func (w logfWriter) Write(p []byte) (int, error) {
	w(strings.TrimSpace(string(p)))
	return len(p), nil
}

func (d *tailnetDirect) start() {
	go d.run()
	timer := time.NewTimer(tailnetFirstAttemptWait)
	defer timer.Stop()
	select {
	case <-d.first:
	case <-timer.C:
	}
}

// stop waits for the supervisor, which closes its listeners on the way out,
// then shuts the HTTP server down. The server context is already cancelled.
func (d *tailnetDirect) stop(ctx context.Context) error {
	select {
	case <-d.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	return d.http.Shutdown(ctx)
}

func (d *tailnetDirect) run() {
	defer close(d.done)
	ctx := d.s.ctx
	firstDone := false
	for {
		wait := d.attempt(ctx)
		if !firstDone {
			close(d.first)
			firstDone = true
		}
		if wait > 0 && d.snapshotState() != TailnetStateListening {
			next := d.s.opts.Now().Add(wait).UTC()
			d.mu.Lock()
			d.status.NextAttemptAt = &next
			d.mu.Unlock()
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			d.closeListeners()
			d.setState(TailnetStateStopped, "The API server is shutting down.")
			return
		case <-d.kick:
		case <-timer.C:
		}
		timer.Stop()
	}
}

func (d *tailnetDirect) snapshotState() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.status.State
}

// fail records a non-listening state and returns the next backoff.
func (d *tailnetDirect) fail(state, message string) time.Duration {
	d.setState(state, message)
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.backoff < d.minBackoff {
		d.backoff = d.minBackoff
	} else if d.backoff *= 2; d.backoff > tailnetMaxBackoff {
		d.backoff = tailnetMaxBackoff
	}
	return d.backoff
}

func (d *tailnetDirect) setState(state, message string) {
	d.mu.Lock()
	changed := d.status.State != state || d.status.Message != message
	if changed {
		d.status.State, d.status.Message, d.status.Since = state, message, d.s.opts.Now().UTC()
	}
	if state == TailnetStateListening {
		d.status.NextAttemptAt = nil
	}
	d.mu.Unlock()
	if changed {
		if message != "" {
			d.s.opts.Logf("tailnet listener: %s: %s", state, message)
		} else {
			d.s.opts.Logf("tailnet listener: %s", state)
		}
	}
}

// attempt brings the listener to match the node and returns how long to wait
// before checking again.
func (d *tailnetDirect) attempt(ctx context.Context) time.Duration {
	nodeCtx, cancel := context.WithTimeout(ctx, tailnetNodeTimeout)
	node, err := d.adapters.Node(nodeCtx)
	cancel()
	if ctx.Err() != nil {
		return 0
	}
	if err != nil {
		d.closeListeners()
		return d.fail(TailnetStateWaiting, fmt.Sprintf("Tailscale is not reachable (%v); retrying. Start or log in to Tailscale.", err))
	}
	logins := d.logins
	if len(logins) == 0 && node.OwnerLogin != "" {
		logins = []string{node.OwnerLogin}
	}
	if len(logins) == 0 {
		d.closeListeners()
		return d.fail(TailnetStateWaiting, "Tailscale reports no owner login for this node; set api.tailnetLogins in the Sidecar config.")
	}

	d.mu.Lock()
	changed := d.node.Host != node.Host || !sameAddrs(d.node.Addresses, node.Addresses)
	d.mu.Unlock()
	if changed {
		// A new name or address: what is bound no longer reaches this node.
		d.closeListeners()
	}
	publicURL := d.publish(node, logins)

	if wait, ok := d.ensureCert(ctx, node.Host); !ok {
		d.closeListeners()
		return wait
	}

	holdsCtx, cancel := context.WithTimeout(ctx, tailnetNodeTimeout)
	held, holdsErr := d.adapters.ServeHolds(holdsCtx, d.port)
	cancel()
	if holdsErr == nil && held {
		d.closeListeners()
		d.setState(TailnetStatePortHeld, fmt.Sprintf("A tailscale serve route holds port %d, and Tailscale answers it before Sidecar can. Remove it with `tailscale serve --https=%d off`, or choose another api.tailnetHTTPSPort. Retrying.", d.port, d.port))
		return d.pollInterval
	}
	if err := d.bind(node); err != nil {
		var message string
		if errors.Is(err, syscall.EADDRINUSE) {
			message = fmt.Sprintf("Port %d on this node's tailnet address is in use by another process (%v). If it is a tailscale serve route, remove it with `tailscale serve --https=%d off`; otherwise choose another api.tailnetHTTPSPort. Retrying.", d.port, err, d.port)
		} else {
			message = fmt.Sprintf("Could not bind the tailnet address (%v); retrying.", err)
		}
		return d.fail(TailnetStateBindFailed, message)
	}
	d.mu.Lock()
	d.backoff = 0
	d.status.Host, d.status.Origin, d.status.Logins = node.Host, publicURL, append([]string(nil), logins...)
	d.mu.Unlock()
	d.setState(TailnetStateListening, "")
	wait := d.pollInterval
	if untilCert := d.certCheck.Add(d.certInterval).Sub(d.s.opts.Now()); untilCert > 0 && untilCert < wait {
		wait = untilCert
	}
	return wait
}

// publish replaces the trust snapshot for node and returns its own origin.
func (d *tailnetDirect) publish(node TailnetNode, logins []string) string {
	host := node.Host
	hostPort := host + ":" + strconv.Itoa(d.port)
	publicURL := "https://" + hostPort
	trust := &tailnetTrust{host: host, hosts: map[string]bool{hostPort: true}, origins: map[string]bool{publicURL: true}, logins: map[string]bool{}, own: map[netip.Addr]bool{}}
	if d.port == 443 {
		publicURL = "https://" + host
		trust.hosts[host] = true
		trust.origins = map[string]bool{publicURL: true}
	}
	trust.publicURL = publicURL
	for _, login := range logins {
		trust.logins[login] = true
	}
	for _, addr := range node.Addresses {
		trust.own[addr.Unmap()] = true
	}
	d.s.tailnet.Store(trust)
	d.mu.Lock()
	d.node = node
	d.status.Host, d.status.Origin = host, publicURL
	d.mu.Unlock()
	return publicURL
}

func sameAddrs(a, b []netip.Addr) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (d *tailnetDirect) bind(node TailnetNode) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.listeners) > 0 {
		return nil
	}
	d.generation++
	generation := d.generation
	var listeners []net.Listener
	var bound []ListenerInfo
	var v6Err error
	for _, addr := range node.Addresses {
		address := net.JoinHostPort(addr.String(), strconv.Itoa(d.port))
		listener, err := d.adapters.Listen("tcp", address)
		if err != nil {
			if addr.Is6() && !errors.Is(err, syscall.EADDRINUSE) {
				// IPv4 is the address every client uses; a missing IPv6
				// stack is not a reason to stay offline.
				v6Err = err
				continue
			}
			for _, opened := range listeners {
				_ = opened.Close()
			}
			return err
		}
		listeners = append(listeners, listener)
		bound = append(bound, ListenerInfo{Name: ListenerTailnet, Network: "tcp", Address: listener.Addr().String(), Host: node.Host})
	}
	if len(listeners) == 0 {
		if v6Err != nil {
			return v6Err
		}
		return errors.New("no tailnet address to bind")
	}
	if v6Err != nil {
		d.s.opts.Logf("tailnet listener: serving IPv4 only: %v", v6Err)
	}
	d.listeners, d.bound = listeners, bound
	d.status.Addresses = d.status.Addresses[:0]
	for _, info := range bound {
		d.status.Addresses = append(d.status.Addresses, info.Address)
	}
	for _, listener := range listeners {
		go func(listener net.Listener) {
			err := d.http.ServeTLS(listener, "", "")
			if err == nil || errors.Is(err, http.ErrServerClosed) || d.s.ctx.Err() != nil {
				return
			}
			d.mu.Lock()
			current := d.generation == generation
			d.mu.Unlock()
			if current {
				// The address went away under us (Tailscale stopped, say):
				// re-check now rather than at the next poll.
				d.s.opts.Logf("tailnet listener: %v; rebinding", err)
				d.closeListeners()
				select {
				case d.kick <- struct{}{}:
				default:
				}
			}
		}(listener)
	}
	return nil
}

func (d *tailnetDirect) closeListeners() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.listeners) == 0 {
		return
	}
	d.generation++
	for _, listener := range d.listeners {
		_ = listener.Close()
	}
	d.listeners, d.bound, d.status.Addresses = nil, nil, nil
}

func (d *tailnetDirect) listenerInfos() []ListenerInfo {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]ListenerInfo(nil), d.bound...)
}

func (d *tailnetDirect) statusSnapshot() TailnetStatus {
	d.mu.Lock()
	status := d.status
	status.Addresses = append([]string(nil), d.status.Addresses...)
	status.Logins = append([]string(nil), d.status.Logins...)
	d.mu.Unlock()
	if _, leaf := d.cert.get(); leaf != nil {
		expires := leaf.NotAfter.UTC()
		status.CertificateExpiresAt = &expires
	}
	return status
}

func (d *tailnetDirect) getCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	cert, _ := d.cert.get()
	if cert == nil {
		return nil, errors.New("no tailnet certificate yet")
	}
	return cert, nil
}

// ensureCert keeps a valid certificate for host loaded, asking the adapter
// again every certInterval. Tailscale renews its own copy before expiry, so
// asking returns the renewed one, which replaces the served certificate
// without a restart. A failed refresh keeps a certificate that is still valid.
func (d *tailnetDirect) ensureCert(ctx context.Context, host string) (time.Duration, bool) {
	now := d.s.opts.Now()
	_, leaf := d.cert.get()
	valid := leaf != nil && d.certHost == host && now.Before(leaf.NotAfter)
	if valid && now.Before(d.certCheck.Add(d.certInterval)) {
		return 0, true
	}
	if !valid && d.certHost != host {
		d.loadStoredCert(host)
		_, leaf = d.cert.get()
		valid = leaf != nil && d.certHost == host && now.Before(leaf.NotAfter)
	}
	certCtx, cancel := context.WithTimeout(ctx, tailnetCertTimeout)
	certPEM, keyPEM, err := d.adapters.Cert(certCtx, host)
	cancel()
	if err == nil {
		var cert *tls.Certificate
		var parsed *x509.Certificate
		if cert, parsed, err = parseTailnetCert(certPEM, keyPEM, host, now); err == nil {
			if storeErr := d.storeCert(host, certPEM, keyPEM); storeErr != nil {
				d.s.opts.Logf("tailnet listener: could not save the certificate: %v", storeErr)
			}
			d.cert.set(cert, parsed)
			d.certHost, d.certCheck = host, now
			return 0, true
		}
	}
	if valid {
		d.s.opts.Logf("tailnet listener: certificate refresh failed (%v); still serving the current one until %s", err, leaf.NotAfter.UTC().Format(time.RFC3339))
		// Ask again after the longest backoff, not a whole interval later.
		d.certCheck = now.Add(tailnetMaxBackoff - d.certInterval)
		return 0, true
	}
	return d.fail(TailnetStateCertFailed, fmt.Sprintf("Could not get a certificate for %s with `tailscale cert` (%v). Enable HTTPS certificates for the tailnet in the Tailscale admin console; retrying.", host, err)), false
}

func parseTailnetCert(certPEM, keyPEM []byte, host string, now time.Time) (*tls.Certificate, *x509.Certificate, error) {
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, nil, fmt.Errorf("certificate and key do not form a pair: %w", err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, nil, err
	}
	if err := leaf.VerifyHostname(host); err != nil {
		return nil, nil, err
	}
	if !now.Before(leaf.NotAfter) {
		return nil, nil, fmt.Errorf("certificate expired at %s", leaf.NotAfter.UTC().Format(time.RFC3339))
	}
	cert.Leaf = leaf
	return &cert, leaf, nil
}

func (d *tailnetDirect) certPaths(host string) (string, string) {
	return filepath.Join(d.tlsDir, host+".crt"), filepath.Join(d.tlsDir, host+".key")
}

// storeCert keeps the pair at mode 0600 in a 0700 directory, so a restart
// while Tailscale is unreachable can still serve TLS.
func (d *tailnetDirect) storeCert(host string, certPEM, keyPEM []byte) error {
	if err := os.MkdirAll(d.tlsDir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(d.tlsDir, 0o700); err != nil {
		return err
	}
	certPath, keyPath := d.certPaths(host)
	if err := writePrivateFile(keyPath, keyPEM); err != nil {
		return err
	}
	return writePrivateFile(certPath, certPEM)
}

func (d *tailnetDirect) loadStoredCert(host string) {
	certPath, keyPath := d.certPaths(host)
	info, err := os.Lstat(keyPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return
	}
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return
	}
	cert, leaf, err := parseTailnetCert(certPEM, keyPEM, host, d.s.opts.Now())
	if err != nil {
		return
	}
	d.cert.set(cert, leaf)
	d.certHost = host
}

// admit establishes the connection's tailnet identity before anything else
// is read from the request. It refuses the node's own addresses (local
// processes use the Local socket or the Browser listener), any address
// Tailscale cannot identify, tagged devices, and logins that are not allowed.
func (d *tailnetDirect) admit(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	addr, ok := connPeerAddr(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, CodeUnauthenticated, "This connection has no tailnet source address, so it cannot be identified.")
		return r, false
	}
	trust := d.s.tailnetTrustSnapshot()
	if trust.own[addr] {
		writeError(w, http.StatusForbidden, CodeLoginRefused, "Connections from this machine's own tailnet address are refused. On this machine use the Local socket (the sidecar CLI) or the Browser listener (`sidecar api open`).")
		return r, false
	}
	peer, err := d.whois.get(r.Context(), addr)
	if err != nil {
		writeError(w, http.StatusUnauthorized, CodeUnauthenticated, fmt.Sprintf("Tailscale could not identify the device at %s (%v); this listener admits only identified tailnet devices.", addr, err))
		return r, false
	}
	if len(peer.Tags) > 0 {
		writeError(w, http.StatusForbidden, CodeLoginRefused, fmt.Sprintf("Device %s is tagged (%s) and belongs to no person; only an allowed login's untagged devices may use this listener.", peer.Device, strings.Join(peer.Tags, ", ")))
		return r, false
	}
	if !trust.logins[peer.Login] {
		writeError(w, http.StatusForbidden, CodeLoginRefused, fmt.Sprintf("Tailnet login %q (device %s) is not allowed; add it to api.tailnetLogins in the Sidecar config.", peer.Login, peer.Device))
		return r, false
	}
	return r.WithContext(context.WithValue(r.Context(), tailnetPeerKey{}, peer)), true
}

func connPeerAddr(r *http.Request) (netip.Addr, bool) {
	var raw string
	if addr, ok := r.Context().Value(tailnetConnKey{}).(net.Addr); ok && addr != nil {
		raw = addr.String()
	} else {
		return netip.Addr{}, false
	}
	addrPort, err := netip.ParseAddrPort(raw)
	if err != nil {
		return netip.Addr{}, false
	}
	return addrPort.Addr().Unmap(), true
}

// whoisCache answers whois by address for a short time, with one lookup in
// flight per address and a bound on concurrent lookups, so a burst of
// connections costs one subprocess. Failures are cached briefly so a peer
// that is refused cannot turn reconnects into a subprocess storm.
type whoisCache struct {
	lookup  TailnetWhoisFunc
	now     func() time.Time
	ttl     time.Duration
	failTTL time.Duration
	sem     chan struct{}

	mu      sync.Mutex
	entries map[netip.Addr]*whoisEntry
}

type whoisEntry struct {
	ready   chan struct{}
	peer    TailnetPeer
	err     error
	expires time.Time
}

func newWhoisCache(lookup TailnetWhoisFunc, now func() time.Time) *whoisCache {
	return &whoisCache{lookup: lookup, now: now, ttl: tailnetWhoisTTL, failTTL: tailnetWhoisNegativeTTL,
		sem: make(chan struct{}, tailnetWhoisConcurrency), entries: map[netip.Addr]*whoisEntry{}}
}

func (c *whoisCache) get(ctx context.Context, addr netip.Addr) (TailnetPeer, error) {
	c.mu.Lock()
	if entry := c.entries[addr]; entry != nil {
		select {
		case <-entry.ready:
			if c.now().Before(entry.expires) {
				c.mu.Unlock()
				return entry.peer, entry.err
			}
		default:
			c.mu.Unlock()
			select {
			case <-entry.ready:
				return entry.peer, entry.err
			case <-ctx.Done():
				return TailnetPeer{}, ctx.Err()
			}
		}
	}
	entry := &whoisEntry{ready: make(chan struct{})}
	c.sweepLocked()
	c.entries[addr] = entry
	c.mu.Unlock()

	peer, err := c.resolve(ctx, addr)
	ttl := c.ttl
	if err != nil {
		ttl = c.failTTL
		if ctx.Err() != nil {
			ttl = 0
		}
	}
	entry.peer, entry.err, entry.expires = peer, err, c.now().Add(ttl)
	close(entry.ready)
	return peer, err
}

func (c *whoisCache) resolve(ctx context.Context, addr netip.Addr) (TailnetPeer, error) {
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return TailnetPeer{}, ctx.Err()
	}
	defer func() { <-c.sem }()
	lookupCtx, cancel := context.WithTimeout(ctx, tailnetWhoisTimeout)
	defer cancel()
	peer, err := c.lookup(lookupCtx, addr)
	if err == nil && strings.TrimSpace(peer.Login) == "" {
		err = errors.New("whois named no login")
	}
	if err != nil {
		return TailnetPeer{}, err
	}
	peer.Tags = append([]string(nil), peer.Tags...)
	sort.Strings(peer.Tags)
	return peer, nil
}

func (c *whoisCache) sweepLocked() {
	if len(c.entries) < tailnetWhoisMaxEntries {
		return
	}
	now := c.now()
	for addr, entry := range c.entries {
		select {
		case <-entry.ready:
			if !now.Before(entry.expires) {
				delete(c.entries, addr)
			}
		default:
		}
	}
	if len(c.entries) < tailnetWhoisMaxEntries {
		return
	}
	for addr, entry := range c.entries {
		select {
		case <-entry.ready:
			delete(c.entries, addr)
		default:
		}
	}
}
