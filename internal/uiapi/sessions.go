package uiapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	sessionsFileName     = "sessions.json"
	sessionIdleTTL       = 30 * 24 * time.Hour
	sessionAbsoluteTTL   = 180 * 24 * time.Hour
	sessionTouchInterval = time.Minute
	browserBearerTTL     = 15 * time.Minute
	// sessionsVersion is the store format written. Version 1 lacked the
	// device fields and is migrated in place on first open.
	sessionsVersion = 2
)

// session is a durable public-key registration, never a bearer credential.
// Label, ApprovedVia and ApprovedAt describe the device for the device list:
// the label is what the browser claimed when it asked for access, and
// ApprovedVia names the surface that let it in (see approvedVia).
// ApprovedDevice is the tailnet device an allowed login approved from, set
// only with a tailnet: ApprovedVia on the direct listener. It is omitted when
// empty, so a version-2 store without it reads unchanged.
type session struct {
	PublicKey      BrowserPublicKey `json:"public_key"`
	Origin         string           `json:"origin"`
	Label          string           `json:"label"`
	ApprovedVia    string           `json:"approved_via"`
	ApprovedDevice string           `json:"approved_device,omitempty"`
	ApprovedAt     time.Time        `json:"approved_at"`
	CreatedAt      time.Time        `json:"created_at"`
	LastUsedAt     time.Time        `json:"last_used_at"`
	ExpiresAt      time.Time        `json:"expires_at"`
}

type sessionsFile struct {
	Version       int                `json:"version"`
	Registrations map[string]session `json:"registrations"`
}

var errLegacySessions = errors.New("legacy browser bearer store")

func sessionExpiry(s session) time.Time {
	expires := s.LastUsedAt.Add(sessionIdleTTL)
	if cap := s.CreatedAt.Add(sessionAbsoluteTTL); cap.Before(expires) {
		return cap
	}
	return expires
}

// Writers reload while holding an independent lock inode, then atomically
// replace the store. The CLI uses this same store through the Local socket.
// a.mu is held, except during startup before the auth store is published.
func (a *authStore) withSessionsLocked(change func(map[string]session) bool) error {
	if a.sessionPath == "" {
		change(a.sessions)
		return nil
	}
	lock, err := os.OpenFile(a.sessionPath+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	if err := lock.Chmod(0600); err != nil {
		return err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	next, info, err := readSessionSnapshot(a.sessionPath)
	legacy := errors.Is(err, errLegacySessions)
	migrate := errors.Is(err, errSessionsNeedMigration)
	if legacy {
		next = map[string]session{}
		err = nil
	}
	if migrate {
		err = nil
	}
	if err != nil {
		a.storeErr = err
		return err
	}
	changed := change(next)
	if changed || legacy || migrate {
		data, err := json.MarshalIndent(sessionsFile{Version: sessionsVersion, Registrations: next}, "", "  ")
		if err != nil {
			return err
		}
		if err := writePrivateFile(a.sessionPath, append(data, '\n')); err != nil {
			return err
		}
		info, err = os.Stat(a.sessionPath)
		if err != nil {
			return err
		}
	}
	a.sessions, a.sessionInfo, a.storeErr = next, info, nil
	if legacy && a.logf != nil {
		a.logf("discarded legacy persisted browser bearer hashes; pair browsers again with `sidecar api open`")
	}
	return nil
}

// Requests use the cached registration index. A changed inode, mtime or size
// reloads the file without acquiring the writer lock: replacement is atomic.
func (a *authStore) reloadSessionsLocked() error {
	if a.sessionPath == "" {
		return nil
	}
	info, err := os.Stat(a.sessionPath)
	if errors.Is(err, os.ErrNotExist) {
		a.sessions = map[string]session{}
		a.sessionInfo = nil
		a.storeErr = nil
		return nil
	}
	if err != nil {
		a.storeErr = err
		return err
	}
	if a.storeErr == nil && a.sessionInfo != nil && os.SameFile(info, a.sessionInfo) && info.ModTime().Equal(a.sessionInfo.ModTime()) && info.Size() == a.sessionInfo.Size() && info.Mode() == a.sessionInfo.Mode() {
		return nil
	}
	next, info, err := readSessionSnapshot(a.sessionPath)
	if errors.Is(err, errSessionsNeedMigration) {
		// An older writer replaced the store. Its records are valid; the
		// next write upgrades the file.
		err = nil
	}
	if err != nil {
		a.storeErr = err
		return err
	}
	a.sessions, a.sessionInfo, a.storeErr = next, info, nil
	return nil
}

// errSessionsNeedMigration accompanies a valid version-1 snapshot whose
// records were upgraded in memory and should be written back.
var errSessionsNeedMigration = errors.New("browser public-key store needs migration")

func readSessions(path string) (map[string]session, error) {
	records, _, err := readSessionSnapshot(path)
	if errors.Is(err, errSessionsNeedMigration) {
		err = nil
	}
	return records, err
}

func readSessionSnapshot(path string) (map[string]session, os.FileInfo, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]session{}, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = file.Close() }()
	if err := file.Chmod(0600); err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	data, err := io.ReadAll(io.LimitReader(file, 1<<20+1))
	if err != nil {
		return nil, nil, err
	}
	corrupt := func(reason any) (map[string]session, os.FileInfo, error) {
		return nil, nil, fmt.Errorf("browser public-key store %s is corrupt (%v); access is refused; move this file aside, restart and pair browsers again with `sidecar api open`", path, reason)
	}
	if len(data) > 1<<20 {
		return corrupt("file exceeds 1 MiB")
	}
	var legacy map[string]json.RawMessage
	if json.Unmarshal(data, &legacy) == nil && legacy["sessions"] != nil && legacy["version"] == nil {
		return nil, info, errLegacySessions
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
	if (stored.Version != 1 && stored.Version != sessionsVersion) || stored.Registrations == nil || len(stored.Registrations) > maxSessions {
		return corrupt("unsupported version or missing/oversized registration map")
	}
	for id, s := range stored.Registrations {
		if stored.Version == 1 {
			if s.Label != "" || s.ApprovedVia != "" || !s.ApprovedAt.IsZero() {
				return corrupt("version 1 registrations carry no device fields")
			}
			s = migrateSessionV1(s)
			stored.Registrations[id] = s
		}
		origin, err := NormalizeOrigin(s.Origin)
		_, keyErr := s.PublicKey.ecdsaKey()
		if err != nil || origin != s.Origin || keyErr != nil || id != browserRegistrationID(s.Origin, s.PublicKey) || s.CreatedAt.IsZero() || s.LastUsedAt.Before(s.CreatedAt) || !s.ExpiresAt.Equal(sessionExpiry(s)) {
			return corrupt("invalid public key, origin or registration timestamps")
		}
		if !validApprovedVia(s.ApprovedVia) || s.ApprovedAt.IsZero() || !storedLabelValid(s.Label) || !storedLabelValid(s.ApprovedDevice) ||
			(s.ApprovedDevice != "" && !strings.HasPrefix(s.ApprovedVia, approvedViaTailnetPrefix)) {
			return corrupt("invalid device label or approval record")
		}
		// Re-clean rather than compare: a later, stricter sanitizer must
		// tidy old labels, not refuse the whole store.
		s.Label = cleanDeviceLabel(s.Label)
		s.ApprovedDevice = cleanDeviceLabel(s.ApprovedDevice)
		stored.Registrations[id] = s
	}
	if stored.Version == 1 {
		return stored.Registrations, info, errSessionsNeedMigration
	}
	return stored.Registrations, info, nil
}

// storedLabelValid is the stable invariant a stored label must hold: valid
// UTF-8 with no control or format characters, within the length cap.
func storedLabelValid(label string) bool {
	if !utf8.ValidString(label) || utf8.RuneCountInString(label) > maxDeviceLabelRunes {
		return false
	}
	for _, r := range label {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return false
		}
	}
	return true
}

// migrateSessionV1 fills the device fields a version-1 registration lacks.
// Every version-1 registration came from a pairing link.
func migrateSessionV1(s session) session {
	s.ApprovedVia = approvedViaLink
	s.ApprovedAt = s.CreatedAt
	return s
}
