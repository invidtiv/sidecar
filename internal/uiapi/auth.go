package uiapi

import (
	"crypto/rand"
	"encoding/base64"
	"os"
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
	credential string // bearer token hash at authorization time
	expires    time.Time
}

// authStore keeps codes, tickets and short-lived bearers in memory. Only
// browser public-key registrations survive a restart.
type authStore struct {
	sessionPath string
	sessionInfo os.FileInfo
	storeErr    error
	bearers     map[string]browserBearer
	proofs      map[string]browserProof
	logf        func(string, ...any)
	mu          sync.Mutex
	now         func() time.Time
	codes       map[string]time.Time // code hash -> expiry
	sessions    map[string]session   // public registration ID -> registration
	tickets     map[string]grant     // ticket hash -> grant
}

func newAuthStore(now func() time.Time) *authStore {
	return &authStore{now: now, codes: map[string]time.Time{}, sessions: map[string]session{}, tickets: map[string]grant{}, bearers: map[string]browserBearer{}, proofs: map[string]browserProof{}}
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

// sessionClient is the client key a session's requests, tickets and
// terminals are counted under.
func sessionClient(key string) string { return "session:" + key }

// revokeSessions drops every browser session, or only those bound to origin
// when it is not empty, together with the tickets they issued. It returns the
// client keys it revoked, so their open terminals can be closed.
func (a *authStore) revokeSessions(origin string) (map[string]bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	revoked := map[string]bool{}
	if err := a.withSessionsLocked(func(sessions map[string]session) bool {
		for key, s := range sessions {
			if origin == "" || s.Origin == origin {
				delete(sessions, key)
				revoked[sessionClient(key)] = true
			}
		}
		return true
	}); err != nil {
		return nil, err
	}
	for hash, bearer := range a.bearers {
		if origin == "" || bearer.origin == origin {
			delete(a.bearers, hash)
		}
	}
	for hash, proof := range a.proofs {
		if origin == "" || proof.origin == origin {
			delete(a.proofs, hash)
		}
	}
	for key, g := range a.tickets {
		if strings.HasPrefix(g.client, "session:") && (origin == "" || g.origin == origin) {
			delete(a.tickets, key)
		}
	}
	return revoked, nil
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
