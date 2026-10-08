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
	// The rest is direct mode only. own holds the node's own tailnet
	// addresses; ownerID pins the default owner by user ID (zero when
	// api.tailnetLogins names the logins); selfID is the node's stable ID;
	// suffix is the tailnet's MagicDNS suffix.
	own     map[netip.Addr]bool
	ownerID int64
	selfID  string
	suffix  string
}

func (s *Server) tailnetTrustSnapshot() *tailnetTrust {
	if trust := s.tailnet.Load(); trust != nil {
		return trust
	}
	return &tailnetTrust{}
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
	logs     *tailnetLogs
	workers  sync.WaitGroup

	pollInterval    time.Duration
	minBackoff      time.Duration
	certInterval    time.Duration
	recheckInterval time.Duration
	peerPrefixes    []netip.Prefix

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
	holdsErr   string // last serve-status error, logged once

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
		recheckInterval: opts.recheckInterval, peerPrefixes: tailnetSourcePrefixes, logs: newTailnetLogs(s.opts.Logf),
		kick: make(chan struct{}, 1), done: make(chan struct{}), first: make(chan struct{})}
	if opts.peerPrefixes != nil {
		d.peerPrefixes = opts.peerPrefixes
	}
	if d.recheckInterval <= 0 {
		d.recheckInterval = tailnetRecheckInterval
	}
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
	d.whois = newWhoisCache(s.ctx, d.adapters.Whois, s.opts.Now)
	d.status = TailnetStatus{Mode: TailnetModeDirect, State: TailnetStateStarting, Port: port, Logins: append([]string(nil), d.logins...), Since: s.opts.Now().UTC()}
	d.handler = &listenerHandler{s: s, kind: ListenerTailnet, routes: s.routeTable(), direct: d}
	d.http = &http.Server{Handler: d.handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute,
		BaseContext: func(net.Listener) context.Context { return s.ctx },
		ConnContext: func(ctx context.Context, conn net.Conn) context.Context {
			return context.WithValue(ctx, tailnetConnKey{}, conn.RemoteAddr())
		},
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: d.getCertificate},
		ErrorLog:  log.New(d.logs, "", 0)}
	s.direct = d
	return nil
}

func (d *tailnetDirect) start() {
	d.workers.Add(1)
	go d.supervise()
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
	d.workers.Wait()
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
	logins, ownerID := d.logins, int64(0)
	if len(logins) == 0 && len(node.Tags) > 0 {
		// A tagged node belongs to no person, so there is no owner to trust.
		d.closeListeners()
		return d.fail(TailnetStateWaiting, fmt.Sprintf("This node is tagged (%s), so it has no owner to default to; set api.tailnetLogins in the Sidecar config.", strings.Join(node.Tags, ", ")))
	}
	if len(logins) == 0 && node.OwnerLogin != "" && node.OwnerID != 0 && !strings.EqualFold(node.OwnerLogin, taggedDevicesLogin) {
		logins, ownerID = []string{node.OwnerLogin}, node.OwnerID
	}
	if len(logins) == 0 {
		d.closeListeners()
		return d.fail(TailnetStateWaiting, "Tailscale reports no owner login for this node; set api.tailnetLogins in the Sidecar config.")
	}
	if !validMagicDNSName(node.Host) {
		// The name becomes a certificate file name and the Host guard.
		d.closeListeners()
		return d.fail(TailnetStateWaiting, fmt.Sprintf("Tailscale reports %q as this node's name, which is not a DNS name; retrying.", node.Host))
	}

	d.mu.Lock()
	changed := d.node.Host != node.Host || !sameAddrs(d.node.Addresses, node.Addresses)
	d.mu.Unlock()
	if changed {
		// A new name or address: what is bound no longer reaches this node.
		d.closeListeners()
	}
	publicURL := d.publish(node, logins, ownerID)

	if wait, ok := d.ensureCert(ctx, node.Host); !ok {
		d.closeListeners()
		return wait
	}

	holdsCtx, cancel := context.WithTimeout(ctx, tailnetNodeTimeout)
	held, holdsErr := d.adapters.ServeHolds(holdsCtx, d.port)
	cancel()
	if holdsErr != nil && holdsErr.Error() != d.holdsErr {
		// Not knowing is no reason to stay offline: a Serve route only
		// shadows the port; it never reaches this listener.
		d.s.opts.Logf("tailnet listener: could not read tailscale serve routes (%v); binding anyway", holdsErr)
	}
	d.holdsErr = ""
	if holdsErr != nil {
		d.holdsErr = holdsErr.Error()
	}
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
func (d *tailnetDirect) publish(node TailnetNode, logins []string, ownerID int64) string {
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
	trust.ownerID, trust.selfID, trust.suffix = ownerID, node.StableID, node.Suffix
	if trust.suffix == "" {
		_, trust.suffix, _ = strings.Cut(host, ".")
	}
	for _, login := range logins {
		trust.logins[strings.ToLower(login)] = true
	}
	for _, addr := range node.Addresses {
		trust.own[addr.Unmap()] = true
	}
	previous := d.s.tailnet.Swap(trust)
	if previous != nil && (previous.ownerID != trust.ownerID || !sameLogins(previous.logins, trust.logins)) {
		// Who may connect changed: streams admitted under the old rule close
		// and reconnect under the new one.
		d.s.opts.Logf("tailnet listener: allowed logins changed; closing open tailnet streams")
		for _, client := range d.s.clients.tailnetStreams() {
			client.revoke()
		}
	}
	d.mu.Lock()
	d.node = node
	d.status.Host, d.status.Origin = host, publicURL
	d.mu.Unlock()
	return publicURL
}

func sameLogins(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for login := range a {
		if !b[login] {
			return false
		}
	}
	return true
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

// certPath is one file holding the chain and its key, so the pair is
// replaced atomically and can never be read half old, half new.
func (d *tailnetDirect) certPath(host string) string {
	return filepath.Join(d.tlsDir, host+".pem")
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
	return writePrivateFile(d.certPath(host), append(append([]byte(nil), certPEM...), keyPEM...))
}

func (d *tailnetDirect) loadStoredCert(host string) {
	path := d.certPath(host)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	certPEM, keyPEM, err := splitPEM(data)
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

// validMagicDNSName accepts lowercase DNS labels joined by dots.
func validMagicDNSName(host string) bool {
	if host == "" || len(host) > 253 || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") || strings.Contains(host, "..") {
		return false
	}
	for _, r := range host {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '.' {
			return false
		}
	}
	return true
}
