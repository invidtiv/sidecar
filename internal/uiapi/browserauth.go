package uiapi

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"
)

const maxBrowserBearers = 4096

// BrowserPublicKey is the public portion of an ECDSA P-256 JWK. The wire
// decoder rejects a private d field; key_ops/ext from WebCrypto are advisory.
type BrowserPublicKey struct {
	Kty    string   `json:"kty"`
	Crv    string   `json:"crv"`
	X      string   `json:"x"`
	Y      string   `json:"y"`
	KeyOps []string `json:"key_ops,omitempty"`
	Ext    *bool    `json:"ext,omitempty"`
}

func (k BrowserPublicKey) ecdsaKey() (*ecdsa.PublicKey, error) {
	if k.Kty != "EC" || k.Crv != "P-256" {
		return nil, errors.New("public_key must be an EC P-256 public JWK")
	}
	x, xe := base64.RawURLEncoding.DecodeString(k.X)
	y, ye := base64.RawURLEncoding.DecodeString(k.Y)
	if xe != nil || ye != nil || len(x) != 32 || len(y) != 32 || base64.RawURLEncoding.EncodeToString(x) != k.X || base64.RawURLEncoding.EncodeToString(y) != k.Y {
		return nil, errors.New("public_key requires canonical 32-byte base64url x and y")
	}
	key, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), append(append([]byte{4}, x...), y...))
	if err != nil {
		return nil, errors.New("public_key is not a P-256 point")
	}
	return key, nil
}

func browserRegistrationID(origin string, key BrowserPublicKey) string {
	return hashToken(origin + "\n" + key.X + "\n" + key.Y)
}

func BrowserProofMessage(origin, id, nonce string, timestamp int64) string {
	return "sidecar-browser-session-v1\n" + origin + "\n" + id + "\n" + nonce + "\n" + strconv.FormatInt(timestamp, 10)
}

type browserBearer struct {
	registration string
	expires      time.Time
}
type browserProof struct {
	registration, origin string
	timestamp            int64
	expires              time.Time
}

func (a *authStore) registerBrowser(origin string, key BrowserPublicKey) (string, string, time.Time, error) {
	if _, err := key.ecdsaKey(); err != nil {
		return "", "", time.Time{}, err
	}
	// Store only the actual public coordinates, not browser metadata.
	key.KeyOps = nil
	key.Ext = nil
	id := browserRegistrationID(origin, key)
	a.mu.Lock()
	defer a.mu.Unlock()
	err := a.withSessionsLocked(func(records map[string]session) bool {
		now := a.now().UTC()
		for id, s := range records {
			if !now.Before(s.ExpiresAt) {
				delete(records, id)
			}
		}
		if _, exists := records[id]; !exists && len(records) >= maxSessions {
			var oldestID string
			var oldest time.Time
			for id, s := range records {
				if oldestID == "" || s.CreatedAt.Before(oldest) || (s.CreatedAt.Equal(oldest) && id < oldestID) {
					oldestID, oldest = id, s.CreatedAt
				}
			}
			delete(records, oldestID)
		}
		s, exists := records[id]
		if !exists {
			s = session{Origin: origin, PublicKey: key, CreatedAt: now, LastUsedAt: now}
		}
		s.ExpiresAt = sessionExpiry(s)
		records[id] = s
		return true
	})
	if err != nil {
		return "", "", time.Time{}, err
	}
	token, expires, err := a.issueBrowserBearerLocked(id)
	return id, token, expires, err
}

func (a *authStore) issueBrowserBearerLocked(id string) (string, time.Time, error) {
	now := a.now()
	for hash, b := range a.bearers {
		if !now.Before(b.expires) {
			delete(a.bearers, hash)
		}
	}
	if len(a.bearers) >= maxBrowserBearers {
		return "", time.Time{}, errTooManyOutstanding
	}
	token, err := randomToken(32)
	if err != nil {
		return "", time.Time{}, err
	}
	expires := now.Add(browserBearerTTL).UTC()
	if cap := a.sessions[id].ExpiresAt; cap.Before(expires) {
		expires = cap
	}
	a.bearers[hashToken(token)] = browserBearer{id, expires}
	return token, expires, nil
}

// Unknown tokens exit before filesystem access. Known tokens consult the
// cached public-key index and only touch last_used_at once per minute.
func (a *authStore) lookupSession(token string) (origin, client string, ok bool) {
	if token == "" {
		return "", "", false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	b, found := a.bearers[hashToken(token)]
	if !found || !a.now().Before(b.expires) {
		return "", "", false
	}
	if err := a.reloadSessionsLocked(); err != nil {
		a.logStoreError(err)
		return "", "", false
	}
	s, found := a.sessions[b.registration]
	if !found || !a.now().Before(s.ExpiresAt) {
		return "", "", false
	}
	if err := a.touchBrowserLocked(b.registration); err != nil {
		a.logStoreError(err)
		return "", "", false
	}
	return s.Origin, sessionClient(b.registration), true
}

func (a *authStore) touchBrowserLocked(id string) error {
	s, ok := a.sessions[id]
	if !ok || !a.now().Before(s.ExpiresAt) {
		return errors.New("browser registration expired or revoked")
	}
	if a.now().Sub(s.LastUsedAt) < sessionTouchInterval {
		return nil
	}
	live := false
	err := a.withSessionsLocked(func(records map[string]session) bool {
		s, ok := records[id]
		now := a.now().UTC()
		if !ok || !now.Before(s.ExpiresAt) {
			return false
		}
		live = true
		if now.Sub(s.LastUsedAt) < sessionTouchInterval {
			return false
		}
		s.LastUsedAt = now
		s.ExpiresAt = sessionExpiry(s)
		records[id] = s
		return true
	})
	if err != nil {
		return err
	}
	if !live {
		return errors.New("browser registration expired or revoked")
	}
	return nil
}

func (a *authStore) logStoreError(err error) {
	if a.logf != nil {
		a.logf("browser authentication refused: %v", err)
	}
}

func (a *authStore) browserCredentialLive(client, credential string) bool {
	if !strings.HasPrefix(client, "session:") {
		return true
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	b, ok := a.bearers[credential]
	if !ok || sessionClient(b.registration) != client || !a.now().Before(b.expires) {
		return false
	}
	if err := a.reloadSessionsLocked(); err != nil {
		a.logStoreError(err)
		return false
	}
	s, ok := a.sessions[b.registration]
	return ok && a.now().Before(s.ExpiresAt)
}

func (a *authStore) issueBrowserProof(origin, id string) (SessionProofChallenge, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.reloadSessionsLocked(); err != nil {
		return SessionProofChallenge{}, err
	}
	s, ok := a.sessions[id]
	if !ok || s.Origin != origin || !a.now().Before(s.ExpiresAt) {
		return SessionProofChallenge{}, errBrowserProofInvalid
	}
	now := a.now()
	held := 0
	for key, p := range a.proofs {
		if !now.Before(p.expires) {
			delete(a.proofs, key)
		} else if p.registration == id {
			held++
		}
	}
	if len(a.proofs) >= maxOutstandingCodes || held >= maxTicketsPerClient {
		return SessionProofChallenge{}, errTooManyOutstanding
	}
	nonce, err := randomToken(24)
	if err != nil {
		return SessionProofChallenge{}, err
	}
	proof := browserProof{id, origin, now.UnixMilli(), now.Add(pairingCodeTTL).UTC()}
	a.proofs[hashToken(nonce)] = proof
	return SessionProofChallenge{Nonce: nonce, Timestamp: proof.timestamp, ExpiresAt: proof.expires}, nil
}

var errBrowserProofInvalid = errors.New("browser key proof is invalid, expired or revoked; pair again with `sidecar api open`")

func (a *authStore) verifyBrowserProof(origin string, req SessionProofRequest) (SessionToken, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	proof, ok := a.proofs[hashToken(req.Nonce)]
	delete(a.proofs, hashToken(req.Nonce)) // attempts are single-use, including refusals
	if !ok || proof.origin != origin || proof.registration != req.RegistrationID || proof.timestamp != req.Timestamp || !a.now().Before(proof.expires) {
		return SessionToken{}, errBrowserProofInvalid
	}
	if err := a.reloadSessionsLocked(); err != nil {
		return SessionToken{}, err
	}
	s, ok := a.sessions[proof.registration]
	if !ok || s.Origin != origin || !a.now().Before(s.ExpiresAt) {
		return SessionToken{}, errBrowserProofInvalid
	}
	key, err := s.PublicKey.ecdsaKey()
	if err != nil {
		return SessionToken{}, errBrowserProofInvalid
	}
	sig, err := base64.RawURLEncoding.DecodeString(req.Signature)
	if err != nil || len(sig) != 64 {
		return SessionToken{}, errBrowserProofInvalid
	}
	digest := sha256.Sum256([]byte(BrowserProofMessage(origin, req.RegistrationID, req.Nonce, req.Timestamp)))
	if !ecdsa.Verify(key, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		return SessionToken{}, errBrowserProofInvalid
	}
	if err := a.touchBrowserLocked(proof.registration); err != nil {
		return SessionToken{}, fmt.Errorf("renew browser registration: %w", err)
	}
	token, expires, err := a.issueBrowserBearerLocked(proof.registration)
	return SessionToken{Token: token, ExpiresAt: expires}, err
}
