// Package uiapi serves Sidecar's UI API v0 (docs/reference/ui-api.md).
//
// It owns transport, trust and the HTTP/WebSocket mapping only. What a
// terminal stream means and what a Sessions row says belong to the packages
// behind Backend, which `sidecar mobile serve --stdio` and `sidecar mobile
// sessions --json` use unchanged; this package never decides either.
package uiapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

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
	// Port, when non-zero, serves the Tailnet listener on a dedicated loopback
	// TCP port instead of the Unix socket, for a tailscaled that cannot open a
	// 0600 user socket.
	Port int
}

// Options configures Start.
type Options struct {
	StateDir string
	// Port is the Browser listener's loopback port; 0 picks a free one.
	Port          int
	UIDir         string
	Tailnet       *TailnetOptions
	Backend       Backend
	Version       string
	FixtureStatus *Status
	Now           func() time.Time
	Logf          func(format string, args ...any)
	// KeepaliveInterval and KeepaliveTimeout govern terminal WebSocket pings;
	// zero means 30s and 15s.
	KeepaliveInterval time.Duration
	KeepaliveTimeout  time.Duration
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
	tailnetHosts   map[string]bool
	tailnetOrigins map[string]bool
	tailnetLogins  map[string]bool

	listeners []ListenerInfo
	servers   []*http.Server
	bound     []boundListener
	failed    chan error

	ctx      context.Context
	cancel   context.CancelFunc
	streamMu sync.Mutex
	closing  bool
	streams  sync.WaitGroup
	endpoint Endpoint
	shutdown sync.Once
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
	if s.static, err = newStaticHandler(opts.UIDir); err != nil {
		return nil, err
	}

	localPath := filepath.Join(dir, localSocketName)
	local, err := listenPrivateUnix(localPath)
	if err != nil {
		return nil, err
	}
	s.addListener(ListenerLocal, local, ListenerInfo{Name: ListenerLocal, Network: "unix", Address: localPath})

	browser, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(opts.Port)))
	if err != nil {
		return nil, fmt.Errorf("listen on 127.0.0.1:%d: %w; choose another with --port", opts.Port, err)
	}
	s.browserPort = browser.Addr().(*net.TCPAddr).Port
	port := strconv.Itoa(s.browserPort)
	s.browserHosts = map[string]bool{"127.0.0.1:" + port: true, "localhost:" + port: true}
	s.browserOrigins = map[string]bool{"http://127.0.0.1:" + port: true, "http://localhost:" + port: true}
	s.addListener(ListenerBrowser, browser, ListenerInfo{Name: ListenerBrowser, Network: "tcp", Address: browser.Addr().String()})

	s.endpoint = Endpoint{PID: os.Getpid(), Version: opts.Version, APIVersion: APIVersion, APIInstance: s.instance,
		StartedAt: s.startedAt, UnixSocket: localPath, TCP: browser.Addr().String()}

	if opts.Tailnet != nil {
		if err := s.listenTailnet(*opts.Tailnet); err != nil {
			return nil, err
		}
	}
	if err := writeEndpoint(dir, s.endpoint); err != nil {
		return nil, err
	}
	ok = true
	for index := range s.servers {
		s.serve(index)
	}
	return s, nil
}

func (s *Server) listenTailnet(opts TailnetOptions) error {
	// Host comparison lowercases the request's Host; DNS names are
	// case-insensitive, so the allowlist is kept in the same form.
	opts.Host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(opts.Host), "."))
	if opts.Host == "" {
		return errors.New("ui api: the tailnet listener needs the node's MagicDNS name")
	}
	if len(opts.Logins) == 0 {
		return errors.New("ui api: the tailnet listener needs at least one allowed login; set api.tailnetLogins")
	}
	s.tailnetHosts = map[string]bool{opts.Host: true}
	s.tailnetOrigins = map[string]bool{"https://" + opts.Host: true, "http://" + opts.Host: true}
	for _, port := range []string{"443", "80"} {
		s.tailnetHosts[opts.Host+":"+port] = true
	}
	s.tailnetLogins = map[string]bool{}
	for _, login := range opts.Logins {
		s.tailnetLogins[login] = true
	}
	if opts.Port > 0 {
		listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(opts.Port)))
		if err != nil {
			return fmt.Errorf("listen on tailnet fallback port 127.0.0.1:%d: %w", opts.Port, err)
		}
		s.addListener(ListenerTailnet, listener, ListenerInfo{Name: ListenerTailnet, Network: "tcp", Address: listener.Addr().String(), Host: opts.Host})
		s.endpoint.TailnetTCP = listener.Addr().String()
		return nil
	}
	path := filepath.Join(s.dir, tailnetSockName)
	listener, err := listenPrivateUnix(path)
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
		for _, server := range s.servers {
			if err := server.Shutdown(ctx); err != nil && result == nil {
				result = err
			}
		}
		done := make(chan struct{})
		go func() { s.streams.Wait(); close(done) }()
		select {
		case <-done:
		case <-ctx.Done():
			if result == nil {
				result = ctx.Err()
			}
		}
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
		Capabilities: []string{"sessions", "status", "terminal", "ws_tickets"},
		Terminal:     TerminalProtocol{Protocol: "mobile", Version: mobileproto.Version}}
}

// Status is GET /api/v0/status and `sidecar api status --json`.
type Status struct {
	APIVersion    int            `json:"api_version"`
	APIInstance   string         `json:"api_instance"`
	ServerVersion string         `json:"server_version"`
	PID           int            `json:"pid"`
	StartedAt     time.Time      `json:"started_at"`
	Listeners     []ListenerInfo `json:"listeners"`
	Clients       []ClientInfo   `json:"clients"`
	Terminals     []TerminalInfo `json:"terminals"`
}

func (s *Server) status() Status {
	clients, terminals := s.clients.snapshot()
	if s.opts.FixtureStatus != nil {
		status := *s.opts.FixtureStatus
		status.Listeners = append([]ListenerInfo(nil), s.listeners...)
		status.Clients, status.Terminals = clients, terminals
		return status
	}
	return Status{APIVersion: APIVersion, APIInstance: s.instance, ServerVersion: s.opts.Version, PID: os.Getpid(),
		StartedAt: s.startedAt, Listeners: append([]ListenerInfo(nil), s.listeners...), Clients: clients, Terminals: terminals}
}
