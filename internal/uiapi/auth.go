package uiapi

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

const (
	pairingCodeTTL       = 60 * time.Second
	ticketTTL            = 30 * time.Second
	maxOutstandingCodes  = 64
	maxOutstandingTicket = 256
	maxSessions          = 1024
)

// grant is what a ticket carries from its issuing request to the WebSocket
// upgrade that redeems it.
type grant struct {
	listener Listener
	auth     string
	origin   string
	login    string
	expires  time.Time
}

type pairingCode struct {
	next    string
	expires time.Time
}

// authStore holds the in-memory credentials: single-use pairing codes, cookie
// sessions and WebSocket tickets. Nothing here survives a restart (v0).
type authStore struct {
	mu       sync.Mutex
	now      func() time.Time
	codes    map[string]pairingCode
	sessions map[string]time.Time // token hash -> created
	tickets  map[string]grant     // ticket hash -> grant
}

func newAuthStore(now func() time.Time) *authStore {
	return &authStore{now: now, codes: map[string]pairingCode{}, sessions: map[string]time.Time{}, tickets: map[string]grant{}}
}

func (a *authStore) issueCode(next string) (string, time.Time, error) {
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
	a.codes[hashToken(code)] = pairingCode{next: next, expires: expires}
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
	entry, ok := a.codes[key]
	delete(a.codes, key)
	return ok && a.now().Before(entry.expires)
}

func (a *authStore) newSession() (string, error) {
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
		for key, created := range a.sessions {
			if oldestKey == "" || created.Before(oldest) {
				oldestKey, oldest = key, created
			}
		}
		delete(a.sessions, oldestKey)
	}
	a.sessions[hashToken(token)] = a.now()
	return token, nil
}

func (a *authStore) validSession(token string) bool {
	if token == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.sessions[hashToken(token)]
	return ok
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
	if len(a.tickets) >= maxOutstandingTicket {
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
	for key, code := range a.codes {
		if !now.Before(code.expires) {
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
