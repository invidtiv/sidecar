package uiapi

import (
	"crypto/rand"
	"encoding/base64"
	"strings"
	"sync"
	"time"
)

const (
	pairingCodeTTL      = 60 * time.Second
	ticketTTL           = 30 * time.Second
	maxOutstandingCodes = 64
	// maxOutstandingTickets bounds every unredeemed ticket; the per-client
	// bound keeps one paired origin or session from taking all of them.
	maxOutstandingTickets = 256
	maxTicketsPerClient   = 16
	maxSessions           = 1024
)

// grant is what a ticket carries from its issuing request to the WebSocket
// upgrade that redeems it.
type grant struct {
	listener   Listener
	auth       string
	origin     string
	login      string
	client     string
	credential string // paired-origin token hash at authorization time
	expires    time.Time
}

// session is a browser session minted by a pairing exchange. Its token is a
// bearer credential bound to the origin that exchanged the code.
type session struct {
	origin  string
	created time.Time
}

// authStore holds the in-memory credentials: single-use pairing codes, browser
// sessions and WebSocket tickets. Nothing here survives a restart (v0).
type authStore struct {
	mu       sync.Mutex
	now      func() time.Time
	codes    map[string]time.Time // code hash -> expiry
	sessions map[string]session   // token hash -> session
	tickets  map[string]grant     // ticket hash -> grant
}

func newAuthStore(now func() time.Time) *authStore {
	return &authStore{now: now, codes: map[string]time.Time{}, sessions: map[string]session{}, tickets: map[string]grant{}}
}

func (a *authStore) issueCode() (string, time.Time, error) {
	code, err := randomToken(16)
	if err != nil {
		return "", time.Time{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	a.pruneLocked(now)
	if len(a.codes) >= maxOutstandingCodes {
		return "", time.Time{}, errTooManyOutstanding
	}
	expires := now.Add(pairingCodeTTL)
	a.codes[hashToken(code)] = expires
	return code, expires, nil
}

// redeemCode consumes code. It is single use whether or not it had expired.
func (a *authStore) redeemCode(code string) bool {
	if code == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	key := hashToken(code)
	expires, ok := a.codes[key]
	delete(a.codes, key)
	return ok && a.now().Before(expires)
}

// newSession mints a session token bound to origin.
func (a *authStore) newSession(origin string) (string, error) {
	token, err := randomToken(32)
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.sessions) >= maxSessions {
		// Evict the oldest: a browser that paired a thousand sessions ago
		// re-pairs with `sidecar api open`.
		var oldestKey string
		var oldest time.Time
		for key, s := range a.sessions {
			if oldestKey == "" || s.created.Before(oldest) {
				oldestKey, oldest = key, s.created
			}
		}
		delete(a.sessions, oldestKey)
	}
	a.sessions[hashToken(token)] = session{origin: origin, created: a.now()}
	return token, nil
}

// lookupSession returns the origin a session token is bound to, and the key
// that identifies this session as one client.
func (a *authStore) lookupSession(token string) (origin, client string, ok bool) {
	if token == "" {
		return "", "", false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	key := hashToken(token)
	s, ok := a.sessions[key]
	if !ok {
		return "", "", false
	}
	return s.origin, sessionClient(key), true
}

// sessionClient is the client key a session's requests, tickets and
// terminals are counted under.
func sessionClient(key string) string { return "session:" + key[:16] }

// revokeSessions drops every browser session, or only those bound to origin
// when it is not empty, together with the tickets they issued. It returns the
// client keys it revoked, so their open terminals can be closed.
func (a *authStore) revokeSessions(origin string) map[string]bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	revoked := map[string]bool{}
	for key, s := range a.sessions {
		if origin == "" || s.origin == origin {
			delete(a.sessions, key)
			revoked[sessionClient(key)] = true
		}
	}
	for key, g := range a.tickets {
		if revoked[g.client] {
			delete(a.tickets, key)
		}
	}
	return revoked
}

func (a *authStore) revokeTickets(keys map[string]bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for key, g := range a.tickets {
		if keys[g.client] {
			delete(a.tickets, key)
		}
	}
}

// sessionClientLive reports whether client, a key from sessionClient, still
// names a session. Keys of other kinds are not sessions and always are.
func (a *authStore) sessionClientLive(client string) bool {
	if !strings.HasPrefix(client, "session:") {
		return true
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for key := range a.sessions {
		if sessionClient(key) == client {
			return true
		}
	}
	return false
}

func (a *authStore) issueTicket(g grant) (string, time.Time, error) {
	ticket, err := randomToken(24)
	if err != nil {
		return "", time.Time{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	a.pruneLocked(now)
	if len(a.tickets) >= maxOutstandingTickets {
		return "", time.Time{}, errTooManyOutstanding
	}
	held := 0
	for _, existing := range a.tickets {
		if existing.client == g.client {
			held++
		}
	}
	if held >= maxTicketsPerClient {
		return "", time.Time{}, errTooManyOutstanding
	}
	g.expires = now.Add(ticketTTL)
	a.tickets[hashToken(ticket)] = g
	return ticket, g.expires, nil
}

// redeemTicket consumes ticket. It is single use whether or not it had
// expired.
func (a *authStore) redeemTicket(ticket string) (grant, bool) {
	if ticket == "" {
		return grant{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	key := hashToken(ticket)
	g, ok := a.tickets[key]
	delete(a.tickets, key)
	if !ok || !a.now().Before(g.expires) {
		return grant{}, false
	}
	return g, true
}

func (a *authStore) pruneLocked(now time.Time) {
	for key, expires := range a.codes {
		if !now.Before(expires) {
			delete(a.codes, key)
		}
	}
	for key, g := range a.tickets {
		if !now.Before(g.expires) {
			delete(a.tickets, key)
		}
	}
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
