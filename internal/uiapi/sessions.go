package uiapi

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
	"time"
)

const (
	sessionsFileName   = "sessions.json"
	sessionIdleTTL     = 30 * 24 * time.Hour
	sessionAbsoluteTTL = 180 * 24 * time.Hour
)

// Session persistence is separate from short-lived codes and tickets. The
// token stays stable until expiry/revocation, so pairing another tab does not
// rotate a credential out from under an already-open tab.
type sessionsFile struct {
	Sessions map[string]session `json:"sessions"`
}

func sessionExpiry(s session) time.Time {
	expires := s.LastUsedAt.Add(sessionIdleTTL)
	if cap := s.CreatedAt.Add(sessionAbsoluteTTL); cap.Before(expires) {
		return cap
	}
	return expires
}

// withSessionsLocked reloads before every operation. The separate lock inode
// survives atomic replacement, and prevents stale snapshots (including a
// concurrent local CLI caller) from resurrecting revoked tokens. a.mu must
// be held by the caller. Nothing is published in memory until a save succeeds.
func (a *authStore) withSessionsLocked(change func(map[string]session) bool) error {
	if a.sessionPath == "" { // ephemeral store used by focused auth tests
		change(a.sessions)
		return nil
	}
	lock, err := os.OpenFile(a.sessionPath+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	if err := lock.Chmod(0o600); err != nil {
		return err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	next, err := readSessions(a.sessionPath)
	if err != nil {
		return err
	}
	if change(next) {
		data, err := json.MarshalIndent(sessionsFile{Sessions: next}, "", "  ")
		if err != nil {
			return err
		}
		if err := writePrivateFile(a.sessionPath, append(data, '\n')); err != nil {
			return err
		}
	}
	a.sessions = next
	return nil
}

func readSessions(path string) (map[string]session, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]session{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	if err := file.Chmod(0o600); err != nil {
		return nil, err
	}
	// Bound reads even for a corrupt or hand-edited file.
	data, err := io.ReadAll(io.LimitReader(file, 1<<20+1))
	if err != nil {
		return nil, err
	}
	corrupt := func(reason any) (map[string]session, error) {
		return nil, fmt.Errorf("browser session store %s is corrupt (%v); access is refused; move this file aside and pair browsers again with `sidecar api open`", path, reason)
	}
	if len(data) > 1<<20 {
		return corrupt("file exceeds 1 MiB")
	}
	var stored sessionsFile
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&stored); err != nil {
		return corrupt(err)
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return corrupt("trailing JSON")
	}
	if stored.Sessions == nil || len(stored.Sessions) > maxSessions {
		return corrupt("missing or oversized sessions map")
	}
	for hash, s := range stored.Sessions {
		decoded, err := hex.DecodeString(hash)
		origin, originErr := NormalizeOrigin(s.Origin)
		if err != nil || len(decoded) != 32 || hash != hashTokenLower(hash) || originErr != nil || origin != s.Origin || s.CreatedAt.IsZero() || s.LastUsedAt.Before(s.CreatedAt) || !s.ExpiresAt.Equal(sessionExpiry(s)) {
			return corrupt("invalid hash, origin or session timestamps")
		}
	}
	return stored.Sessions, nil
}

func hashTokenLower(hash string) string {
	// Re-encoding validates the canonical lowercase SHA-256 representation.
	decoded, _ := hex.DecodeString(hash)
	return hex.EncodeToString(decoded)
}
