// Package uiapi serves Sidecar's UI API v0 (docs/reference/ui-api.md).
//
// It owns transport, trust and the HTTP/WebSocket mapping only. What a
// terminal stream means and what a Sessions row says belong to the packages
// behind Backend, which `sidecar mobile serve --stdio` and `sidecar mobile
// sessions --json` use unchanged; this package never decides either.
package uiapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/marcus/sidecar/internal/apiservice"
	"github.com/marcus/sidecar/internal/filefind"
	"github.com/marcus/sidecar/internal/mobileproto"
)

const (
	// APIVersion is the wire contract version in every route path.
	APIVersion = 0
	// DefaultPort is the Browser listener's default loopback port.
	DefaultPort = 7861
)

// Backend is the library seam the API maps onto. One Backend lives for the
// whole server process; ServeTerminal is called once per terminal connection
// and owns that stream until input reaches EOF or ctx ends.
type Backend interface {
	Sessions(ctx context.Context, query mobileproto.CatalogQuery) (mobileproto.CatalogSnapshot, error)
	ServeTerminal(ctx context.Context, input io.Reader, output io.Writer) error
}

// Listener names one of the three trust surfaces.
type Listener string

const (
	ListenerLocal   Listener = "local"
	ListenerBrowser Listener = "browser"
	ListenerTailnet Listener = "tailnet"
)

// TailnetOptions configures the Tailnet listener.
type TailnetOptions struct {
	// Host is the node's MagicDNS name without the trailing dot.
	Host string
	// Logins are the Tailscale logins trusted on this listener.
	Logins []string
	// HTTPSPort is the public HTTPS port. In serve mode it is the Tailscale
	// Serve port, not a local bind, and zero uses the standard-port Host and
	// Origin guards. In direct mode it is the port bound on the node's tailnet
	// addresses, and zero means DefaultPort.
	HTTPSPort int
	// Port, when non-zero, serves the serve-mode listener on a dedicated
	// loopback TCP port instead of the Unix socket, for a tailscaled that
	// cannot open a 0600 user socket. Direct mode refuses it.
	Port int
	// Mode selects direct or serve. The CLI resolves api.tailnetMode, whose
	// default is direct; the zero value here is serve, so a caller that names
	// no mode never starts reaching Tailscale on its own.
	Mode TailnetMode
	// Adapters are direct mode's seams over Tailscale; nil fields use the CLI.
	// In direct mode Host is discovered, and Logins may be empty to mean the
	// node owner.
	Adapters TailnetAdapters

	pollInterval    time.Duration // direct-mode tests shorten these
	minBackoff      time.Duration
	recheckInterval time.Duration
	peerPrefixes    []netip.Prefix // tests admit loopback peers
}

// Options configures Start.
type Options struct {
	StateDir string
	// Inherited supplies already-acquired manager listeners. Start owns them on
	// success; the caller closes them on failure. Nil discovers manager sockets.
	Inherited []apiservice.ActivatedListener
	// Port is the Browser listener's loopback port; 0 picks a free one.
	Port               int
	BrowserProxyOrigin string
	UIDir              string
	Tailnet            *TailnetOptions
	Backend            Backend
	Content            ContentBackend
	Version            string
	FixtureStatus      *Status
	Now                func() time.Time
	Logf               func(format string, args ...any)
	// KeepaliveInterval and KeepaliveTimeout govern terminal WebSocket pings;
	// zero means 30s and 15s.
	KeepaliveInterval time.Duration
	KeepaliveTimeout  time.Duration
	// RequestReadTimeout bounds how long a non-stream request may take to
	// deliver its body after its headers. Zero means 10 seconds.
	RequestReadTimeout time.Duration
	// KeepaliveStallTimeout bounds how long a blocked inbound pump may excuse
	// missing pongs. Zero means one minute.
	KeepaliveStallTimeout time.Duration
}

// Server is one running API process.
type Server struct {
	opts      Options
	dir       string
	instance  string
	startedAt time.Time
	lock      *os.File
	auth      *authStore
	origins   *originStore
	clients   *clientRegistry
	static    http.Handler
	// credentialMu orders credential changes with ticket issuance and stream
	// registration. Authorization alone is a snapshot, not admission authority.
	credentialMu sync.Mutex

	browserPort    int
	browserHosts   map[string]bool
	browserOrigins map[string]bool
	// tailnet is what the Tailnet listener trusts; direct mode replaces it
	// when the node's name or addresses change.
	tailnet atomic.Pointer[tailnetTrust]
	direct  *tailnetDirect

	listeners []ListenerInfo
	servers   []*http.Server
	bound     []boundListener
	failed    chan error

	ctx           context.Context
	cancel        context.CancelFunc
	streamMu      sync.Mutex
	closing       bool
	streams       sync.WaitGroup
	endpoint      Endpoint
	shutdown      sync.Once
	eventOnce     sync.Once
	eventErr      error
	catalogEvents eventSignals
	holderCache   legacyHolderCache
	viewer        viewerRelay
	viewerErr     error
	// contentWatches bounds live content registrations per credential.
	contentWatches     watchBudget
	contentRequests    contentRequestBudget
	fileIndex          filefind.Index
	fileSearchRequests contentRequestBudget
	// accessSignals carries new access requests to approvers' events
	// streams. accessMu orders the access notification's post and
	// withdrawal; accessNoteID is the live one, if any.
	accessSignals     accessEvents
	accessMu          sync.Mutex
	accessNoteID      string
	accessNoteChecked time.Time
	// sweepTimer fires at sweepAt, the earliest pending request's expiry.
	sweepMu    sync.Mutex
	sweepTimer *time.Timer
	sweepAt    time.Time
}

// ListenerInfo describes one bound listener in status.
type ListenerInfo struct {
	Name    Listener `json:"name"`
	Network string   `json:"network"`
	Address string   `json:"address"`
	Host    string   `json:"host,omitempty"`
}

// maxUnixSocketPath is the conservative sun_path bound (macOS allows 104
// bytes including the terminator; Linux 108).
const maxUnixSocketPath = 103

// Start binds every listener, writes endpoint.json and begins serving. It
// refuses while another server holds the same state tree.
func Start(opts Options) (*Server, error) {
	if opts.Backend == nil {
		return nil, errors.New("ui api: a backend is required")
	}
	if opts.StateDir == "" {
		return nil, errors.New("ui api: a state directory is required")
	}
	if opts.Port < 0 || opts.Port > 65535 {
		return nil, fmt.Errorf("ui api: port %d is out of range", opts.Port)
	}
	if opts.BrowserProxyOrigin != "" {
		origin, err := NormalizeOrigin(opts.BrowserProxyOrigin)
		if err != nil || !strings.HasPrefix(origin, "https://") {
			return nil, errors.New("api.browserProxyOrigin must be one HTTPS origin, with no path, query, fragment or credentials")
		}
		opts.BrowserProxyOrigin = origin
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logf == nil {
		opts.Logf = func(string, ...any) {}
	}
	dir := Dir(opts.StateDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	lock, err := acquireLock(dir)
	if err != nil {
		return nil, err
	}
	instance, err := randomToken(12)
	if err != nil {
		releaseLock(lock)
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{opts: opts, dir: dir, instance: "api_" + instance, startedAt: opts.Now().UTC(), lock: lock,
		auth: newAuthStore(opts.Now), clients: newClientRegistry(opts.Now), failed: make(chan error, 4), ctx: ctx, cancel: cancel}
	ok := false
	defer func() {
		if !ok {
			s.closeListeners()
			s.closeStatic()
			cancel()
			releaseLock(lock)
		}
	}()
	if s.origins, err = loadOriginStore(filepath.Join(dir, originsFileName)); err != nil {
		return nil, err
	}
	s.auth.sessionPath = filepath.Join(dir, sessionsFileName)
	s.auth.logf = opts.Logf
	if err := s.auth.withSessionsLocked(func(map[string]session) bool { return false }); err != nil {
		return nil, err
	}
	if s.static, err = newStaticHandler(opts.UIDir); err != nil {
		return nil, err
	}

	activated := opts.Inherited
	if activated == nil {
		activated, err = apiservice.Activate()
		if err != nil {
			return nil, err
		}
	}
	inherited, err := validateActivated(opts, activated)
	if err != nil {
		apiservice.CloseActivated(activated)
		return nil, err
	}
	defer func() {
		if !ok {
			apiservice.CloseActivated(activated)
		}
	}()
	if os.Getenv(apiservice.ActivationRequired) == "1" && inherited[ListenerBrowser] == nil {
		return nil, errors.New("ui api: service requires a manager-held Browser listener; run `sidecar api service install` (Linux Homebrew services do not support socket activation)")
	}
	localPath := filepath.Join(dir, localSocketName)
	local, err := acquireListener(inherited[ListenerLocal], "unix", localPath)
	if err != nil {
		return nil, err
	}
	s.addListener(ListenerLocal, local, ListenerInfo{Name: ListenerLocal, Network: "unix", Address: localPath})

	browser, err := acquireListener(inherited[ListenerBrowser], "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(opts.Port)))
	if err != nil {
		return nil, fmt.Errorf("listen on 127.0.0.1:%d: %w; choose another with --port", opts.Port, err)
	}
	s.browserPort = browser.Addr().(*net.TCPAddr).Port
	port := strconv.Itoa(s.browserPort)
	s.browserHosts = map[string]bool{"127.0.0.1:" + port: true, "localhost:" + port: true}
	s.browserOrigins = map[string]bool{"http://127.0.0.1:" + port: true, "http://localhost:" + port: true}
	if opts.BrowserProxyOrigin != "" {
		s.browserHosts[strings.TrimPrefix(opts.BrowserProxyOrigin, "https://")] = true
		s.browserOrigins[opts.BrowserProxyOrigin] = true
	}
	s.addListener(ListenerBrowser, browser, ListenerInfo{Name: ListenerBrowser, Network: "tcp", Address: browser.Addr().String()})

	s.endpoint = Endpoint{PID: os.Getpid(), Version: opts.Version, APIVersion: APIVersion, APIInstance: s.instance,
		StartedAt: s.startedAt, UnixSocket: localPath, TCP: browser.Addr().String(), BrowserProxyOrigin: opts.BrowserProxyOrigin}

	if opts.Tailnet != nil && opts.Tailnet.Mode == TailnetModeDirect {
		if err := s.prepareTailnetDirect(*opts.Tailnet); err != nil {
			return nil, err
		}
	} else if opts.Tailnet != nil {
		if err := s.listenTailnet(*opts.Tailnet, inherited[ListenerTailnet]); err != nil {
			return nil, err
		}
	}
	if err := writeEndpoint(dir, s.endpoint); err != nil {
		return nil, err
	}
	s.withdrawStaleAccessNotifications()
	ok = true
	for index := range s.servers {
		s.serve(index)
	}
	if s.direct != nil {
		// Bound by Sidecar itself, never inherited, and retried in the
		// background: Tailscale being down never stops the other listeners.
		s.direct.start()
	}
	return s, nil
}

func (s *Server) listenTailnet(opts TailnetOptions, inherited net.Listener) error {
	// Host comparison lowercases the request's Host; DNS names are
	// case-insensitive, so the allowlist is kept in the same form.
	opts.Host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(opts.Host), "."))
	if opts.Host == "" {
		return errors.New("ui api: the tailnet listener needs the node's MagicDNS name")
	}
	if len(opts.Logins) == 0 {
		return errors.New("ui api: the tailnet listener needs at least one allowed login; set api.tailnetLogins")
	}
	publicURL, err := opts.HTTPSURL()
	if err != nil {
		return err
	}
	trust := &tailnetTrust{hosts: map[string]bool{opts.Host: true}, origins: map[string]bool{"https://" + opts.Host: true, "http://" + opts.Host: true},
		logins: map[string]bool{}, publicURL: publicURL, host: opts.Host}
	for _, port := range []string{"443", "80"} {
		trust.hosts[opts.Host+":"+port] = true
	}
	if opts.HTTPSPort != 0 && opts.HTTPSPort != 443 {
		// A dedicated port is a separate origin: do not trust a page served
		// by another application on the node's standard HTTP/HTTPS ports.
		trust.hosts = map[string]bool{strings.TrimPrefix(publicURL, "https://"): true}
		trust.origins = map[string]bool{publicURL: true}
	}
	for _, login := range opts.Logins {
		trust.logins[strings.ToLower(login)] = true
	}
	s.tailnet.Store(trust)
	if opts.Port > 0 {
		listener, err := acquireListener(inherited, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(opts.Port)))
		if err != nil {
			return fmt.Errorf("listen on tailnet fallback port 127.0.0.1:%d: %w", opts.Port, err)
		}
		s.addListener(ListenerTailnet, listener, ListenerInfo{Name: ListenerTailnet, Network: "tcp", Address: listener.Addr().String(), Host: opts.Host})
		s.endpoint.TailnetTCP = listener.Addr().String()
		return nil
	}
	path := filepath.Join(s.dir, tailnetSockName)
	listener, err := acquireListener(inherited, "unix", path)
	if err != nil {
		return err
	}
	s.addListener(ListenerTailnet, listener, ListenerInfo{Name: ListenerTailnet, Network: "unix", Address: path, Host: opts.Host})
	s.endpoint.TailnetSocket = path
	return nil
}

func listenPrivateUnix(path string) (net.Listener, error) {
	if len(path) > maxUnixSocketPath {
		return nil, fmt.Errorf("unix socket path %s is %d bytes, over the %d-byte limit; use a shorter XDG_STATE_HOME", path, len(path), maxUnixSocketPath)
	}
	// We hold the single-instance lock, so any file here is a dead server's.
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		return nil, err
	}
	return listener, nil
}

type boundListener struct {
	net.Listener
	kind Listener
}

func (s *Server) addListener(kind Listener, listener net.Listener, info ListenerInfo) {
	handler := &listenerHandler{s: s, kind: kind, routes: s.routeTable()}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute,
		BaseContext: func(net.Listener) context.Context { return s.ctx }}
	s.servers = append(s.servers, server)
	s.listeners = append(s.listeners, info)
	s.bound = append(s.bound, boundListener{Listener: listener, kind: kind})
}

func (s *Server) serve(index int) {
	server, listener := s.servers[index], s.bound[index]
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case s.failed <- fmt.Errorf("%s listener: %w", listener.kind, err):
			default:
			}
		}
	}()
}

func (s *Server) closeStatic() {
	if closer, ok := s.static.(io.Closer); ok {
		_ = closer.Close()
	}
}

func (s *Server) closeListeners() {
	for _, listener := range s.bound {
		_ = listener.Close()
	}
}

func (s *Server) requestReadTimeout() time.Duration {
	if s.opts.RequestReadTimeout > 0 {
		return s.opts.RequestReadTimeout
	}
	return defaultRequestReadTimeout
}

const defaultRequestReadTimeout = 10 * time.Second

// readBodyPromptly reads a non-stream request's body before dispatch, under a
// read deadline, then clears the deadline. A request is authorized when its
// headers arrive, so its body must follow promptly: without a bound a client
// could hold an authorized request open indefinitely. The deadline must not
// outlive the read: net/http's background read would hit it and cancel the
// request context of a handler that legitimately runs longer. The body is
// read up to one byte past maxBodyBytes so handlers keep enforcing the cap.
// It reports false when it has answered the request itself.
func (s *Server) readBodyPromptly(w http.ResponseWriter, r *http.Request) bool {
	if r.Body == nil || r.Body == http.NoBody {
		return true
	}
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Now().Add(s.requestReadTimeout()))
	data, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	if err != nil {
		// Leave the expired deadline in place: net/http drains an unread
		// body after the handler, and that drain must fail at once rather
		// than wait on a stalled client.
		w.Header().Set("Connection", "close")
		writeError(w, http.StatusRequestTimeout, CodeRequestTimeout, fmt.Sprintf("The request body did not arrive within %s; send it with the headers.", s.requestReadTimeout()))
		return false
	}
	if len(data) > maxBodyBytes {
		// Over the cap: the handler refuses it at once. The rest of the
		// body is never read, so keep the deadline and close afterwards.
		w.Header().Set("Connection", "close")
	} else {
		// The whole body is in hand; nothing more is read from this request.
		_ = controller.SetReadDeadline(time.Time{})
	}
	r.Body = io.NopCloser(bytes.NewReader(data))
	return true
}

// Endpoint is what this server recorded in endpoint.json.
func (s *Server) Endpoint() Endpoint { return s.endpoint }

// Failed delivers a listener that stopped serving unexpectedly.
func (s *Server) Failed() <-chan error { return s.failed }

// BrowserURL is the Browser listener's base URL.
func (s *Server) BrowserURL() string { return "http://127.0.0.1:" + strconv.Itoa(s.browserPort) }

// Shutdown closes every terminal stream with 4409, stops the listeners and
// removes endpoint.json. Leases are released by the streams themselves, exactly
// as on stdin EOF.
func (s *Server) Shutdown(ctx context.Context) error {
	var result error
	s.shutdown.Do(func() {
		s.streamMu.Lock()
		s.closing = true
		s.streamMu.Unlock()
		s.cancel()
		// Close even listeners whose Serve goroutine has not yet registered
		// with net/http. Do so before releasing the single-instance lock:
		// UnixListener.Close unlinks its path, which may soon name a successor.
		s.closeListeners()
		if s.direct != nil {
			if err := s.direct.stop(ctx); err != nil && result == nil {
				result = err
			}
		}
		for _, server := range s.servers {
			if err := server.Shutdown(ctx); err != nil && result == nil {
				result = err
			}
		}
		done := make(chan struct{})
		go func() { s.streams.Wait(); s.viewer.workers.Wait(); close(done) }()
		select {
		case <-done:
		case <-ctx.Done():
			if result == nil {
				result = ctx.Err()
			}
		}
		// Pending access requests die with this process; so does their
		// notification.
		s.stopAccessSweep()
		s.withdrawAccessNotification(true)
		removeEndpoint(s.dir, s.instance)
		releaseLock(s.lock)
		s.closeStatic()
	})
	return result
}

// beginStream registers a terminal stream unless shutdown has begun.
func (s *Server) beginStream() bool {
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	if s.closing {
		return false
	}
	s.streams.Add(1)
	return true
}

func (s *Server) hello() Hello {
	return Hello{APIVersion: APIVersion, APIInstance: s.instance, ServerVersion: s.opts.Version,
		Capabilities: []string{"sessions", "status", "terminal", "terminal_ended", "ws_tickets", "events", "projects", "workspace", "workspace_operations", "content", "layouts", "uiRequestRelayV1", "notifications", "file_search", "notifications_batch", "access_requests"},
		Terminal:     TerminalProtocol{Protocol: "mobile", Version: mobileproto.Version}}
}

// Status is GET /api/v0/status and `sidecar api status --json`.
type Status struct {
	APIVersion    int            `json:"api_version"`
	APIInstance   string         `json:"api_instance"`
	ServerVersion string         `json:"server_version"`
	UIDir         *string        `json:"ui_dir,omitempty"`
	UIConfigured  bool           `json:"ui_configured"`
	PID           int            `json:"pid"`
	StartedAt     time.Time      `json:"started_at"`
	Listeners     []ListenerInfo `json:"listeners"`
	// Tailnet reports the Tailnet listener when it is enabled.
	Tailnet   *TailnetStatus `json:"tailnet,omitempty"`
	Clients   []ClientInfo   `json:"clients"`
	Terminals []TerminalInfo `json:"terminals"`
}

func (s *Server) status() Status {
	clients, terminals := s.clients.snapshot()
	if s.opts.FixtureStatus != nil {
		status := *s.opts.FixtureStatus
		status.Listeners = s.listenerSnapshot()
		status.Tailnet = s.TailnetStatus()
		status.Clients, status.Terminals = clients, terminals
		status.UIDir = &s.opts.UIDir
		status.UIConfigured = s.opts.UIDir != ""
		return status
	}
	return Status{APIVersion: APIVersion, APIInstance: s.instance, ServerVersion: s.opts.Version, PID: os.Getpid(),
		StartedAt: s.startedAt, UIDir: &s.opts.UIDir, UIConfigured: s.opts.UIDir != "", Listeners: s.listenerSnapshot(), Tailnet: s.TailnetStatus(), Clients: clients, Terminals: terminals}
}

func (s *Server) listenerSnapshot() []ListenerInfo {
	listeners := append([]ListenerInfo(nil), s.listeners...)
	if s.direct != nil {
		listeners = append(listeners, s.direct.listenerInfos()...)
	}
	return listeners
}

// TailnetStatus reports the Tailnet listener, or nil when it is not enabled.
func (s *Server) TailnetStatus() *TailnetStatus {
	if s.direct != nil {
		status := s.direct.statusSnapshot()
		return &status
	}
	if s.opts.Tailnet == nil {
		return nil
	}
	trust := s.tailnetTrustSnapshot()
	status := TailnetStatus{Mode: TailnetModeServe, State: TailnetStateListening, Host: trust.host, Origin: trust.publicURL, Since: s.startedAt}
	for login := range trust.logins {
		status.Logins = append(status.Logins, login)
	}
	sort.Strings(status.Logins)
	return &status
}
