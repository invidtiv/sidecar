package uiapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// ScopeFull is the only v0 scope. Narrower scopes arrive with the routes they
// protect.
const ScopeFull = "full"

// OriginRecord is one paired origin as persisted. The bearer token itself is
// never stored, only its SHA-256.
type OriginRecord struct {
	Origin      string    `json:"origin"`
	TokenSHA256 string    `json:"token_sha256"`
	Scopes      []string  `json:"scopes"`
	CreatedAt   time.Time `json:"created_at"`
}

// PairedOrigin is a registration as listed: never the token or its hash.
type PairedOrigin struct {
	Origin    string    `json:"origin"`
	Scopes    []string  `json:"scopes"`
	CreatedAt time.Time `json:"created_at"`
}

type originsFile struct {
	Origins []OriginRecord `json:"origins"`
}

// originStore is the paired-origin registry, persisted as a 0600 JSON file.
// The server is its only writer; the CLI changes it through the Local socket.
type originStore struct {
	path    string
	mu      sync.RWMutex
	records []OriginRecord
}

func loadOriginStore(path string) (*originStore, error) {
	store := &originStore{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	// A hand-copied file can arrive with broader permissions; the hashes are
	// still credentials-adjacent, so tighten rather than refuse.
	if info, statErr := os.Stat(path); statErr == nil && info.Mode().Perm() != 0o600 {
		if err := os.Chmod(path, 0o600); err != nil {
			return nil, err
		}
	}
	var file originsFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	store.records = file.Origins
	return store, nil
}

func (s *originStore) save(records []OriginRecord) error {
	data, err := json.MarshalIndent(originsFile{Origins: records}, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFile(s.path, append(data, '\n'))
}

// pair registers or re-registers origin, rotating its token.
func (s *originStore) pair(origin string, scopes []string, now time.Time) (string, OriginRecord, error) {
	token, err := randomToken(32)
	if err != nil {
		return "", OriginRecord{}, err
	}
	record := OriginRecord{Origin: origin, TokenSHA256: hashToken(token), Scopes: scopes, CreatedAt: now.UTC()}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make([]OriginRecord, 0, len(s.records)+1)
	for _, existing := range s.records {
		if existing.Origin != origin {
			next = append(next, existing)
		}
	}
	next = append(next, record)
	sort.Slice(next, func(i, j int) bool { return next[i].Origin < next[j].Origin })
	if err := s.save(next); err != nil {
		return "", OriginRecord{}, err
	}
	s.records = next
	return token, record, nil
}

func (s *originStore) revoke(origin string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make([]OriginRecord, 0, len(s.records))
	found := false
	for _, existing := range s.records {
		if existing.Origin == origin {
			found = true
			continue
		}
		next = append(next, existing)
	}
	if !found {
		return false, nil
	}
	if err := s.save(next); err != nil {
		return false, err
	}
	s.records = next
	return true, nil
}

func (s *originStore) list() []PairedOrigin {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]PairedOrigin, 0, len(s.records))
	for _, record := range s.records {
		out = append(out, PairedOrigin{Origin: record.Origin, Scopes: append([]string(nil), record.Scopes...), CreatedAt: record.CreatedAt})
	}
	return out
}

func (s *originStore) has(origin string) bool {
	if origin == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, record := range s.records {
		if record.Origin == origin {
			return true
		}
	}
	return false
}

// lookupToken returns the origin a bearer token was issued to.
func (s *originStore) lookupToken(token string) (OriginRecord, bool) {
	if token == "" {
		return OriginRecord{}, false
	}
	want := []byte(hashToken(token))
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, record := range s.records {
		if subtle.ConstantTimeCompare(want, []byte(record.TokenSHA256)) == 1 {
			return record, true
		}
	}
	return OriginRecord{}, false
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// NormalizeOrigin returns the serialized origin a browser would send for raw,
// or an error that says what is wrong with it.
func NormalizeOrigin(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || raw == "" {
		return "", fmt.Errorf("origin %q is not a URL; pass scheme://host[:port]", raw)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("origin %q must use http or https", raw)
	}
	if parsed.User != nil || parsed.Host == "" || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return "", fmt.Errorf("origin %q must be only scheme://host[:port], with no path, query or credentials", raw)
	}
	host := strings.ToLower(parsed.Hostname())
	port := parsed.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		return scheme + "://" + host + ":" + port, nil
	}
	return scheme + "://" + host, nil
}

func normalizeScopes(scopes []string) ([]string, error) {
	if len(scopes) == 0 {
		return []string{ScopeFull}, nil
	}
	for _, scope := range scopes {
		if scope != ScopeFull {
			return nil, fmt.Errorf("scope %q is not available in v0; the only scope is %q", scope, ScopeFull)
		}
	}
	return []string{ScopeFull}, nil
}
