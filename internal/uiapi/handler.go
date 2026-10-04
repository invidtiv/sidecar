package uiapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/mobile"
	"github.com/marcus/sidecar/internal/mobileproto"
)

const (
	terminalPath        = "/api/v0/terminal"
	maxBodyBytes        = 64 << 10
	tailscaleLoginHead  = "Tailscale-User-Login"
	mutationHeader      = "X-Sidecar-Request"
	corsAllowedHeaders  = "Authorization, Content-Type, X-Sidecar-Request, If-Match, If-None-Match"
	corsAllowedMethods  = "GET, HEAD, POST, PUT, DELETE"
	corsPreflightMaxAge = "600"
)

type routeFunc func(w http.ResponseWriter, r *http.Request, c caller)

type route struct {
	methods   map[string]routeFunc
	listeners []Listener // empty means every listener
	public    bool       // served without authentication
}

func (rt *route) allows(kind Listener) bool {
	if len(rt.listeners) == 0 {
		return true
	}
	for _, allowed := range rt.listeners {
		if allowed == kind {
			return true
		}
	}
	return false
}

func (rt *route) localOnly() bool {
	return len(rt.listeners) == 1 && rt.listeners[0] == ListenerLocal
}

func (s *Server) routeTable() map[string]*route {
	local := []Listener{ListenerLocal}
	remote := []Listener{ListenerBrowser, ListenerTailnet}
	return map[string]*route{
		contentRoute:               {methods: map[string]routeFunc{http.MethodGet: s.handleContent}},
		treeRoute:                  {methods: map[string]routeFunc{http.MethodGet: s.handleTree}},
		layoutRoute:                {methods: map[string]routeFunc{http.MethodGet: s.handleLayout, http.MethodPut: s.handleLayout}},
		"/api/v0/hello":            {methods: map[string]routeFunc{http.MethodGet: s.handleHello}},
		"/api/v0/sessions":         {methods: map[string]routeFunc{http.MethodGet: s.handleSessions}},
		"/api/v0/status":           {methods: map[string]routeFunc{http.MethodGet: s.handleStatus}},
		"/api/v0/ws-tickets":       {methods: map[string]routeFunc{http.MethodPost: s.handleTicket}, listeners: remote},
		"/api/v0/pairing/codes":    {methods: map[string]routeFunc{http.MethodPost: s.handlePairingCode}, listeners: local},
		"/api/v0/pairing/sessions": {methods: map[string]routeFunc{http.MethodDelete: s.handleRevokeSessions}, listeners: local},
		"/api/v0/origins": {methods: map[string]routeFunc{http.MethodGet: s.handleListOrigins, http.MethodPost: s.handlePairOrigin,
			http.MethodDelete: s.handleRevokeOrigin}, listeners: local},
		"/api/v0/pairing/session-proof":        {methods: map[string]routeFunc{http.MethodPost: s.handleSessionProofChallenge}, listeners: []Listener{ListenerBrowser}, public: true},
		"/api/v0/pairing/session-proof/verify": {methods: map[string]routeFunc{http.MethodPost: s.handleSessionProofVerify}, listeners: []Listener{ListenerBrowser}, public: true},
		"/api/v0/pairing/exchange":             {methods: map[string]routeFunc{http.MethodPost: s.handlePairingExchange}, listeners: []Listener{ListenerBrowser}, public: true},
		"/pair":                                {methods: map[string]routeFunc{http.MethodGet: s.handlePair}, listeners: []Listener{ListenerBrowser}, public: true},
	}
}

type listenerHandler struct {
	s      *Server
	kind   Listener
	routes map[string]*route
}

func (h *listenerHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.kind == ListenerLocal {
		if r.URL.Path == terminalPath || r.URL.Path == eventsPath {
			h.serveStream(w, r)
			return
		}
		h.dispatch(w, r, caller{listener: ListenerLocal, auth: "local"})
		return
	}
	if !h.hostAllowed(r.Host) {
		writeError(w, http.StatusMisdirectedRequest, CodeHostRefused, fmt.Sprintf("This server does not answer to host %q; use %s.", r.Host, h.canonicalBase()))
		return
	}
	if r.URL.Path == terminalPath || r.URL.Path == eventsPath {
		h.serveStream(w, r)
		return
	}
	origin := r.Header.Get("Origin")
	own := h.ownOrigin(origin)
	paired := origin != "" && !own && h.s.origins.has(origin)
	if origin != "" && !own && !paired {
		writeError(w, http.StatusForbidden, CodeOriginRefused, fmt.Sprintf("Origin %q is not paired; pair it with `sidecar api pair --origin %s`.", origin, origin))
		return
	}
	if paired {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Add("Vary", "Origin")
		w.Header().Set("Access-Control-Expose-Headers", "ETag")
	}
	if r.Method == http.MethodOptions {
		if !paired {
			writeError(w, http.StatusForbidden, CodeOriginRefused, "Only paired origins may make cross-origin requests; pair one with `sidecar api pair --origin URL`.")
			return
		}
		w.Header().Set("Access-Control-Allow-Methods", corsAllowedMethods)
		w.Header().Set("Access-Control-Allow-Headers", corsAllowedHeaders)
		w.Header().Set("Access-Control-Max-Age", corsPreflightMaxAge)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		if origin == "" && !h.bearerWithoutOrigin(r) {
			writeError(w, http.StatusForbidden, CodeOriginRefused, "Requests that change state must carry an allowed Origin header, or a bearer token.")
			return
		}
		if !isJSONContentType(r.Header.Get("Content-Type")) || r.Header.Get(mutationHeader) != "1" {
			writeError(w, http.StatusForbidden, CodeMutationRefused, "Send state-changing requests as Content-Type: application/json with the header X-Sidecar-Request: 1.")
			return
		}
	}
	h.dispatch(w, r, caller{listener: h.kind, origin: origin})
}

func (h *listenerHandler) dispatch(w http.ResponseWriter, r *http.Request, c caller) {
	rt := h.routes[r.URL.Path]
	if template, _ := projectContentRoute(r.URL.Path); template != "" {
		rt = h.routes[template]
	}
	if rt == nil {
		if strings.HasPrefix(r.URL.Path, "/api/") || h.kind == ListenerLocal {
			writeError(w, http.StatusNotFound, CodeNotFound, fmt.Sprintf("There is no route %s; see docs/reference/ui-api.md for the v0 routes.", r.URL.Path))
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			writeError(w, http.StatusMethodNotAllowed, CodeMethod, "Static UI files are served only for GET and HEAD.")
			return
		}
		if h.kind == ListenerTailnet {
			if _, ok := h.authenticate(w, r, c); !ok {
				return
			}
		}
		// The UI may only be framed by itself. On the Tailnet listener the
		// credential is ambient, so a page on another site could otherwise
		// frame a live terminal and steer the user's clicks and keys into it.
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'self'")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		h.s.static.ServeHTTP(w, r)
		return
	}
	if !rt.allows(h.kind) {
		if rt.localOnly() {
			writeError(w, http.StatusForbidden, CodeLocalOnly, fmt.Sprintf("%s is served only on the local Unix socket; use the sidecar CLI on this machine.", r.URL.Path))
			return
		}
		writeError(w, http.StatusForbidden, CodeNotServedHere, fmt.Sprintf("%s is not served on the %s listener.", r.URL.Path, h.kind))
		return
	}
	method := r.Method
	if method == http.MethodHead {
		method = http.MethodGet
	}
	fn := rt.methods[method]
	if fn == nil {
		allowed := make([]string, 0, len(rt.methods))
		for name := range rt.methods {
			allowed = append(allowed, name)
		}
		sort.Strings(allowed)
		w.Header().Set("Allow", strings.Join(allowed, ", "))
		writeError(w, http.StatusMethodNotAllowed, CodeMethod, fmt.Sprintf("%s accepts %s.", r.URL.Path, strings.Join(allowed, ", ")))
		return
	}
	if !rt.public {
		var ok bool
		if c, ok = h.authenticate(w, r, c); !ok {
			return
		}
	}
	if !rt.public && r.URL.Path != "/api/v0/hello" && r.URL.Path != "/api/v0/ws-tickets" {
		if template, _ := projectContentRoute(r.URL.Path); template == "" && !h.s.requireScope(w, c, ScopeFull) {
			return
		}
	}
	fn(w, r, c)
}

// authenticate establishes trust for an HTTP request, writing the refusal
// itself when there is none.
func (h *listenerHandler) authenticate(w http.ResponseWriter, r *http.Request, c caller) (caller, bool) {
	switch h.kind {
	case ListenerLocal:
		c.auth, c.client = "local", "local"
		return c, true
	case ListenerTailnet:
		login, code, message := h.tailnetLogin(r)
		if code != "" {
			status := http.StatusUnauthorized
			if code == CodeLoginRefused {
				status = http.StatusForbidden
			}
			writeError(w, status, code, message)
			return c, false
		}
		if h.pairedOnTailnet(c.origin) {
			// The login header is ambient: tailscale serve adds it to every
			// request from the owner's devices, whatever page sent it. It
			// vouches for the device, not for code served from a paired
			// origin, so that code still presents the origin's own token.
			token, _ := bearerToken(r)
			resolved, result := h.s.resolveBearer(token, c.origin)
			if result != bearerOK || resolved.auth != "bearer" {
				writeError(w, http.StatusUnauthorized, CodeUnauthenticated, "A paired origin must send its own bearer token on the tailnet listener too; pair it with `sidecar api pair --origin URL`.")
				return c, false
			}
			resolved.listener, resolved.login = c.listener, login
			return resolved, true
		}
		c.auth, c.login, c.client = "tailnet", login, "tailnet:"+login
		return c, true
	}
	token, present := bearerToken(r)
	if !present {
		writeError(w, http.StatusUnauthorized, CodeUnauthenticated, "Send Authorization: Bearer with this browser's session token (pair it with `sidecar api open`) or a paired origin's token.")
		return c, false
	}
	resolved, result := h.s.resolveBearer(token, c.origin)
	switch result {
	case bearerWrongOrigin:
		writeError(w, http.StatusForbidden, CodeOriginRefused, "This bearer token was issued to another origin.")
		return c, false
	case bearerUnknown:
		writeError(w, http.StatusUnauthorized, CodeUnauthenticated, "This bearer token is not valid; pair again with `sidecar api open` or `sidecar api pair --origin URL`.")
		return c, false
	}
	resolved.listener = c.listener
	return resolved, true
}

type bearerResult int

const (
	bearerOK bearerResult = iota
	bearerUnknown
	bearerWrongOrigin
)

// resolveBearer accepts a paired origin's token or a browser session token.
// Both are bound to one origin: a request that names any other Origin is
// refused, so a token copied to another page is useless there.
func (s *Server) resolveBearer(token, origin string) (caller, bearerResult) {
	if record, ok := s.origins.lookupToken(token); ok {
		if origin != "" && origin != record.Origin {
			return caller{}, bearerWrongOrigin
		}
		return caller{auth: "bearer", origin: record.Origin, client: "origin:" + record.Origin, credential: record.TokenSHA256}, bearerOK
	}
	if bound, client, ok := s.auth.lookupSession(token); ok {
		if origin != "" && origin != bound {
			return caller{}, bearerWrongOrigin
		}
		return caller{auth: "session", origin: bound, client: client, credential: hashToken(token)}, bearerOK
	}
	return caller{}, bearerUnknown
}

// callerLive is checked under credentialMu at ticket/terminal admission.
func (s *Server) callerLive(c caller) bool {
	if strings.HasPrefix(c.client, "origin:") {
		return s.origins.credentialLive(strings.TrimPrefix(c.client, "origin:"), c.credential)
	}
	return s.auth.browserCredentialLive(c.client, c.credential)
}

func (h *listenerHandler) tailnetLogin(r *http.Request) (login, code, message string) {
	login = strings.TrimSpace(r.Header.Get(tailscaleLoginHead))
	if login == "" {
		return "", CodeUnauthenticated, "Reach this listener through `tailscale serve`, which identifies your tailnet login."
	}
	if !h.s.tailnetLogins[login] {
		return "", CodeLoginRefused, fmt.Sprintf("Tailnet login %q is not allowed; add it to api.tailnetLogins in the Sidecar config.", login)
	}
	return login, "", ""
}

// bearerWithoutOrigin reports whether a request with no Origin may proceed
// on the strength of an Authorization: Bearer header. Origin guards protect
// ambient credentials, and on the Browser listener there are none: a bearer
// token has to be presented deliberately, which a cross-site page cannot do
// without a preflight. Node, curl and native clients send no Origin. The token
// itself is still validated, and an Origin that is present must match it. On
// the Tailnet listener the login header is ambient, so Origin stays required.
// Only the Bearer scheme with a non-empty token qualifies: Basic and other
// schemes can be attached by a browser on its own, so they are not deliberate.
func (h *listenerHandler) bearerWithoutOrigin(r *http.Request) bool {
	if h.kind != ListenerBrowser {
		return false
	}
	token, _ := bearerToken(r)
	return token != ""
}

// pairedOnTailnet reports a request on the Tailnet listener whose Origin is a
// paired origin rather than the listener's own. The tailnet login authorizes
// only the listener's own origins; a paired origin authenticates with its own
// credential, so revoking or rotating it reaches its tailnet streams too.
func (h *listenerHandler) pairedOnTailnet(origin string) bool {
	return h.kind == ListenerTailnet && origin != "" && !h.ownOrigin(origin) && h.s.origins.has(origin)
}

func (h *listenerHandler) hostAllowed(host string) bool {
	switch h.kind {
	case ListenerBrowser:
		return h.s.browserHosts[host]
	case ListenerTailnet:
		return h.s.tailnetHosts[strings.ToLower(host)]
	}
	return true
}

func (h *listenerHandler) ownOrigin(origin string) bool {
	switch h.kind {
	case ListenerBrowser:
		return h.s.browserOrigins[origin]
	case ListenerTailnet:
		return h.s.tailnetOrigins[origin]
	}
	return false
}

func (h *listenerHandler) originAllowed(origin string) bool {
	return origin != "" && (h.ownOrigin(origin) || h.s.origins.has(origin))
}

func (h *listenerHandler) canonicalBase() string {
	if h.kind == ListenerTailnet && h.s.opts.Tailnet != nil {
		return "https://" + h.s.opts.Tailnet.Host
	}
	return h.s.BrowserURL()
}

func bearerToken(r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return "", false
	}
	scheme, token, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", true
	}
	return strings.TrimSpace(token), true
}

func isJSONContentType(value string) bool {
	media, _, err := mime.ParseMediaType(value)
	return err == nil && media == "application/json"
}

// decodeBody reads an optional bounded JSON object. An empty body is an empty
// object.
func decodeBody(w http.ResponseWriter, r *http.Request, into any) bool {
	reader := http.MaxBytesReader(w, r.Body, maxBodyBytes)
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, fmt.Sprintf("The request body is not the expected JSON object: %v.", err))
		return false
	}
	return true
}

func (s *Server) handleHello(w http.ResponseWriter, _ *http.Request, _ caller) {
	writeJSON(w, http.StatusOK, s.hello())
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request, _ caller) {
	writeJSON(w, http.StatusOK, s.status())
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request, _ caller) {
	query, err := ParseCatalogQuery(r.URL.Query())
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
		return
	}
	snapshot, err := s.opts.Backend.Sessions(r.Context(), query)
	if err != nil {
		var refusal *mobile.ResolveError
		if errors.As(err, &refusal) {
			status := http.StatusServiceUnavailable
			if refusal.Code == mobileproto.ErrorInvalidRequest {
				status = http.StatusBadRequest
			}
			writeError(w, status, refusal.Code, refusal.Message)
			return
		}
		writeError(w, http.StatusServiceUnavailable, CodeBackend, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

// ParseCatalogQuery maps the sessions route's query parameters onto the
// protocol's catalog_query, refusing anything it does not name.
func ParseCatalogQuery(values url.Values) (mobileproto.CatalogQuery, error) {
	query := mobileproto.CatalogQuery{}
	for key, list := range values {
		switch key {
		case "sort", "search", "show_idle_sessions":
			if len(list) != 1 {
				return query, fmt.Errorf("query parameter %s takes one value", key)
			}
		case "host", "provider", "state":
		default:
			return query, fmt.Errorf("unknown query parameter %q; use sort, search, host, provider, state or show_idle_sessions", key)
		}
	}
	query.Sort = values.Get("sort")
	query.Search = values.Get("search")
	query.Hosts = values["host"]
	query.Providers = values["provider"]
	query.States = values["state"]
	if values.Has("show_idle_sessions") {
		var show bool
		switch strings.ToLower(strings.TrimSpace(values.Get("show_idle_sessions"))) {
		case "true":
			show = true
		case "false":
		default:
			return query, errors.New("show_idle_sessions must be true or false")
		}
		query.ShowIdleSessions = &show
	}
	return query, nil
}

// TicketResponse is POST /api/v0/ws-tickets.
type TicketResponse struct {
	Ticket    string    `json:"ticket"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (s *Server) handleTicket(w http.ResponseWriter, r *http.Request, c caller) {
	var body TicketRequest
	if !decodeBody(w, r, &body) {
		return
	}
	s.credentialMu.Lock()
	defer s.credentialMu.Unlock()
	if !s.callerLive(c) {
		writeError(w, http.StatusUnauthorized, CodeUnauthenticated, revokedSessionReason)
		return
	}
	ticket, expires, err := s.auth.issueTicket(grant{listener: c.listener, auth: c.auth, origin: c.origin, login: c.login, client: c.client, credential: c.credential})
	if err != nil {
		writeError(w, http.StatusTooManyRequests, CodeTooMany, fmt.Sprintf("Too many unredeemed tickets (at most %d per client); open the WebSocket with one you already have, or wait 30 seconds.", maxTicketsPerClient))
		return
	}
	writeJSON(w, http.StatusOK, TicketResponse{Ticket: ticket, ExpiresAt: expires.UTC()})
}

// PairingCode is POST /api/v0/pairing/codes.
type PairingCode struct {
	Code      string    `json:"code"`
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (s *Server) handlePairingCode(w http.ResponseWriter, r *http.Request, _ caller) {
	var body PairingCodeRequest
	if !decodeBody(w, r, &body) {
		return
	}
	next, err := sameOriginPath(body.Next)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
		return
	}
	code, expires, err := s.auth.issueCode()
	if err != nil {
		writeError(w, http.StatusTooManyRequests, CodeTooMany, "Too many unused pairing codes; use one or wait a minute.")
		return
	}
	// The code rides in the fragment, which a browser never sends: it reaches
	// the server only in the pairing page's POST body, never a request line.
	link := s.BrowserURL() + "/pair#" + url.Values{"code": {code}, "next": {next}}.Encode()
	writeJSON(w, http.StatusOK, PairingCode{Code: code, URL: link, ExpiresAt: expires.UTC()})
}

// PairingExchange is POST /api/v0/pairing/exchange: a browser session token
// for the same-origin UI, and the validated path to land on.
type PairingExchange struct {
	RegistrationID string    `json:"registration_id"`
	ExpiresAt      time.Time `json:"expires_at"`
	Token          string    `json:"token"`
	Next           string    `json:"next"`
}

// handlePairingExchange registers a browser public key using a pairing code. It needs no
// credential (the code is one), but it is a mutation like any other, and only
// the listener's own origin may make it: a paired origin has its own token.
func (s *Server) handlePairingExchange(w http.ResponseWriter, r *http.Request, c caller) {
	if !s.browserOrigins[c.origin] {
		writeError(w, http.StatusForbidden, CodeOriginRefused, "Only this server's own pairing page may exchange a pairing code; open the link from `sidecar api open`.")
		return
	}
	var body PairingExchangeRequest
	if !decodeBody(w, r, &body) {
		return
	}
	next, err := sameOriginPath(body.Next)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
		return
	}
	if _, err := body.PublicKey.ecdsaKey(); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
		return
	}
	s.credentialMu.Lock()
	defer s.credentialMu.Unlock()
	if !s.auth.redeemCode(body.Code) {
		writeError(w, http.StatusUnauthorized, CodePairingInvalid, "This pairing link expired or was already used; run `sidecar api open` again.")
		return
	}
	id, token, expires, err := s.auth.registerBrowser(c.origin, body.PublicKey)
	if err != nil {
		if errors.Is(err, errTooManyOutstanding) {
			writeBrowserProofError(w, err)
		} else {
			s.auth.logStoreError(err)
			writeError(w, http.StatusServiceUnavailable, CodeBackend, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, PairingExchange{RegistrationID: id, Token: token, ExpiresAt: expires, Next: next})
}

// handlePair serves the pairing page. It reads nothing from the request and
// consumes nothing: the page's own script exchanges the code from the
// fragment.
func (s *Server) handlePair(w http.ResponseWriter, r *http.Request, _ caller) {
	servePairPage(w, r)
}

// sameOriginPath accepts only an absolute path on this origin, so a pairing
// link can never redirect a freshly authenticated browser somewhere else.
func sameOriginPath(next string) (string, error) {
	if next == "" {
		return "/", nil
	}
	refuse := fmt.Errorf("next must be a path on this server, such as /; %q is not", next)
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.ContainsRune(next, '\\') {
		return "", refuse
	}
	for _, r := range next {
		if r < 0x20 || r == 0x7f {
			return "", refuse
		}
	}
	parsed, err := url.Parse(next)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || parsed.User != nil || parsed.Opaque != "" {
		return "", refuse
	}
	return next, nil
}

// OriginRegistration is POST /api/v0/origins. The token appears only here.
type OriginRegistration struct {
	Origin string   `json:"origin"`
	Token  string   `json:"token"`
	Scopes []string `json:"scopes"`
}

// OriginList is GET /api/v0/origins.
type OriginList struct {
	Origins []PairedOrigin `json:"origins"`
}

// OriginRevocation is DELETE /api/v0/origins.
type OriginRevocation struct {
	Origin  string `json:"origin"`
	Revoked bool   `json:"revoked"`
}

// SessionRevocation is DELETE /api/v0/pairing/sessions.
type SessionRevocation struct {
	Origin          string `json:"origin,omitempty"`
	Revoked         int    `json:"revoked"`
	TerminalsClosed int    `json:"terminals_closed"`
}

// handleRevokeSessions drops browser sessions, all of them or one origin's,
// without a restart. Their tokens get 401 on the next request, their unused
// tickets stop working, and their open terminals close with 4401.
func (s *Server) handleRevokeSessions(w http.ResponseWriter, r *http.Request, _ caller) {
	var origin string
	if r.URL.Query().Has("origin") {
		normalized, err := NormalizeOrigin(r.URL.Query().Get("origin"))
		if err != nil {
			writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
			return
		}
		origin = normalized
	}
	for key := range r.URL.Query() {
		if key != "origin" {
			writeError(w, http.StatusBadRequest, CodeInvalidRequest, fmt.Sprintf("unknown query parameter %q; only origin narrows a session revocation", key))
			return
		}
	}
	s.credentialMu.Lock()
	defer s.credentialMu.Unlock()
	revoked, err := s.auth.revokeSessions(origin)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeBackend, fmt.Sprintf("Could not save browser session revocation: %v.", err))
		return
	}
	closed := s.clients.revoke(s.clients.sessionKeys(origin))
	writeJSON(w, http.StatusOK, SessionRevocation{Origin: origin, Revoked: len(revoked), TerminalsClosed: closed})
}

func (s *Server) handlePairOrigin(w http.ResponseWriter, r *http.Request, _ caller) {
	var body OriginRequest
	if !decodeBody(w, r, &body) {
		return
	}
	origin, err := NormalizeOrigin(body.Origin)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
		return
	}
	scopes, err := normalizeScopes(body.Scopes)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
		return
	}
	s.credentialMu.Lock()
	defer s.credentialMu.Unlock()
	rotating := s.origins.has(origin)
	token, record, err := s.origins.pair(origin, scopes, s.opts.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeBackend, fmt.Sprintf("Could not save the paired origin: %v.", err))
		return
	}
	if rotating {
		// Rotation replaces a token the owner may believe leaked. Every stream
		// and ticket that exists now was authorized with the old token (the new
		// one has not been returned yet), so end them as revocation does.
		keys := map[string]bool{"origin:" + origin: true}
		s.auth.revokeTickets(keys)
		s.clients.revoke(keys)
	}
	writeJSON(w, http.StatusOK, OriginRegistration{Origin: record.Origin, Token: token, Scopes: record.Scopes})
}

func (s *Server) handleListOrigins(w http.ResponseWriter, _ *http.Request, _ caller) {
	writeJSON(w, http.StatusOK, OriginList{Origins: s.origins.list()})
}

func (s *Server) handleRevokeOrigin(w http.ResponseWriter, r *http.Request, _ caller) {
	origin, err := NormalizeOrigin(r.URL.Query().Get("origin"))
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
		return
	}
	s.credentialMu.Lock()
	defer s.credentialMu.Unlock()
	if !s.origins.has(origin) {
		writeError(w, http.StatusNotFound, CodeOriginNotFound, fmt.Sprintf("%s is not paired; list registrations with `sidecar api pair --list`.", origin))
		return
	}
	// Purge matching browser credentials too, before acknowledging revocation.
	if _, err := s.auth.revokeSessions(origin); err != nil {
		writeError(w, http.StatusInternalServerError, CodeBackend, fmt.Sprintf("Could not save browser session revocation: %v.", err))
		return
	}
	// Even if saving the origin registry fails, streams belonging to browser
	// credentials already purged above must close.
	s.clients.revoke(s.clients.sessionKeys(origin))
	revoked, err := s.origins.revoke(origin)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeBackend, fmt.Sprintf("Could not save the paired origins: %v.", err))
		return
	}
	if !revoked {
		writeError(w, http.StatusNotFound, CodeOriginNotFound, fmt.Sprintf("%s is not paired; list registrations with `sidecar api pair --list`.", origin))
		return
	}
	keys := s.clients.sessionKeys(origin)
	keys["origin:"+origin] = true
	s.auth.revokeTickets(keys)
	s.clients.revoke(keys)
	writeJSON(w, http.StatusOK, OriginRevocation{Origin: origin, Revoked: true})
}
