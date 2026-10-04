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
	"strconv"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/mobile"
	"github.com/marcus/sidecar/internal/mobileproto"
)

const (
	terminalPath        = "/api/v0/terminal"
	maxBodyBytes        = 64 << 10
	cookieMaxAge        = 400 * 24 * 60 * 60
	tailscaleLoginHead  = "Tailscale-User-Login"
	mutationHeader      = "X-Sidecar-Request"
	corsAllowedHeaders  = "Authorization, Content-Type, X-Sidecar-Request"
	corsAllowedMethods  = "GET, POST, DELETE"
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
		"/api/v0/hello":         {methods: map[string]routeFunc{http.MethodGet: s.handleHello}},
		"/api/v0/sessions":      {methods: map[string]routeFunc{http.MethodGet: s.handleSessions}},
		"/api/v0/status":        {methods: map[string]routeFunc{http.MethodGet: s.handleStatus}},
		"/api/v0/ws-tickets":    {methods: map[string]routeFunc{http.MethodPost: s.handleTicket}, listeners: remote},
		"/api/v0/pairing/codes": {methods: map[string]routeFunc{http.MethodPost: s.handlePairingCode}, listeners: local},
		"/api/v0/origins": {methods: map[string]routeFunc{http.MethodGet: s.handleListOrigins, http.MethodPost: s.handlePairOrigin,
			http.MethodDelete: s.handleRevokeOrigin}, listeners: local},
		"/pair": {methods: map[string]routeFunc{http.MethodGet: s.handlePair}, listeners: []Listener{ListenerBrowser}, public: true},
	}
}

type listenerHandler struct {
	s      *Server
	kind   Listener
	routes map[string]*route
}

func (h *listenerHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.kind == ListenerLocal {
		if r.URL.Path == terminalPath {
			h.serveTerminal(w, r)
			return
		}
		h.dispatch(w, r, caller{listener: ListenerLocal, auth: "local"})
		return
	}
	if !h.hostAllowed(r.Host) {
		writeError(w, http.StatusMisdirectedRequest, CodeHostRefused, fmt.Sprintf("This server does not answer to host %q; use %s.", r.Host, h.canonicalBase()))
		return
	}
	if r.URL.Path == terminalPath {
		h.serveTerminal(w, r)
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
		if origin == "" {
			writeError(w, http.StatusForbidden, CodeOriginRefused, "Requests that change state must carry an allowed Origin header.")
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
	fn(w, r, c)
}

// authenticate establishes trust for an HTTP request, writing the refusal
// itself when there is none.
func (h *listenerHandler) authenticate(w http.ResponseWriter, r *http.Request, c caller) (caller, bool) {
	switch h.kind {
	case ListenerLocal:
		c.auth = "local"
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
		c.auth, c.login = "tailnet", login
		return c, true
	}
	if token, present := bearerToken(r); present {
		record, ok := h.s.origins.lookupToken(token)
		if !ok {
			writeError(w, http.StatusUnauthorized, CodeUnauthenticated, "This bearer token is not paired; pair the origin again with `sidecar api pair --origin URL`.")
			return c, false
		}
		if c.origin != "" && c.origin != record.Origin {
			writeError(w, http.StatusForbidden, CodeOriginRefused, "This bearer token was issued to another origin.")
			return c, false
		}
		c.auth, c.origin = "bearer", record.Origin
		return c, true
	}
	// Cookies are for the same-origin UI only. A paired origin on the same
	// site would also carry it, so it authenticates with its token instead.
	if c.origin == "" || h.ownOrigin(c.origin) {
		if cookie, err := r.Cookie(h.cookieName()); err == nil && h.s.auth.validSession(cookie.Value) {
			c.auth = "cookie"
			return c, true
		}
	}
	writeError(w, http.StatusUnauthorized, CodeUnauthenticated, "Pair this browser with `sidecar api open`, or send a paired origin's bearer token.")
	return c, false
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

func (h *listenerHandler) cookieName() string { return h.s.cookieName() }

func (s *Server) cookieName() string { return "sidecar_session_" + strconv.Itoa(s.browserPort) }

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
	var body struct{}
	if !decodeBody(w, r, &body) {
		return
	}
	ticket, expires, err := s.auth.issueTicket(grant{listener: c.listener, auth: c.auth, origin: c.origin, login: c.login})
	if err != nil {
		writeError(w, http.StatusTooManyRequests, CodeTooMany, "Too many unredeemed tickets; open the WebSocket with one you already have, or wait 30 seconds.")
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
	var body struct {
		Next string `json:"next"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	next, err := sameOriginPath(body.Next)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
		return
	}
	code, expires, err := s.auth.issueCode(next)
	if err != nil {
		writeError(w, http.StatusTooManyRequests, CodeTooMany, "Too many unused pairing codes; use one or wait a minute.")
		return
	}
	link := s.BrowserURL() + "/pair?" + url.Values{"code": {code}, "next": {next}}.Encode()
	writeJSON(w, http.StatusOK, PairingCode{Code: code, URL: link, ExpiresAt: expires.UTC()})
}

func (s *Server) handlePair(w http.ResponseWriter, r *http.Request, _ caller) {
	query := r.URL.Query()
	next, err := sameOriginPath(query.Get("next"))
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
		return
	}
	if !s.auth.redeemCode(query.Get("code")) {
		writeError(w, http.StatusUnauthorized, CodePairingInvalid, "This pairing link expired or was already used; run `sidecar api open` again.")
		return
	}
	token, err := s.auth.newSession()
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeBackend, "Could not create a session; try `sidecar api open` again.")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: s.cookieName(), Value: token, Path: "/", MaxAge: cookieMaxAge, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, next, http.StatusSeeOther)
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

func (s *Server) handlePairOrigin(w http.ResponseWriter, r *http.Request, _ caller) {
	var body struct {
		Origin string   `json:"origin"`
		Scopes []string `json:"scopes"`
	}
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
	token, record, err := s.origins.pair(origin, scopes, s.opts.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeBackend, fmt.Sprintf("Could not save the paired origin: %v.", err))
		return
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
	revoked, err := s.origins.revoke(origin)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeBackend, fmt.Sprintf("Could not save the paired origins: %v.", err))
		return
	}
	if !revoked {
		writeError(w, http.StatusNotFound, CodeOriginNotFound, fmt.Sprintf("%s is not paired; list registrations with `sidecar api pair --list`.", origin))
		return
	}
	writeJSON(w, http.StatusOK, OriginRevocation{Origin: origin, Revoked: true})
}
