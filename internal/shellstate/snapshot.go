package shellstate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/marcus/sidecar/internal/config"
)

// ErrNoManifestPath refuses a pathless handle before it can create a relative
// .lock or .tmp file next to the caller's working directory.
var ErrNoManifestPath = errors.New("shell manifest has no path")

func checkManifestPath(path string) error {
	if strings.TrimSpace(path) == "" {
		return ErrNoManifestPath
	}
	if err := config.AssertIsolatedPath(path); err != nil {
		return &Error{Kind: KindState, Msg: "refusing shell manifest path", Err: err}
	}
	return nil
}

// SnapshotAtPath reads one coherent manifest using the same advisory lock as
// writers. Missing files return the current empty shape; other read failures
// remain errors so each caller can apply its own recovery policy.
func SnapshotAtPath(path string) (Snapshot, error) {
	if err := checkManifestPath(path); err != nil {
		return Snapshot{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return Snapshot{}, &Error{Kind: KindState, Msg: "create shell manifest directory", Err: err}
	}
	lock, err := acquireLockKind(path, syscall.LOCK_SH)
	if err != nil {
		return Snapshot{}, &Error{Kind: KindState, Msg: "lock shell manifest", Err: err}
	}
	defer releaseLock(lock)
	m, err := readManifest(path)
	if os.IsNotExist(err) {
		return Snapshot{Version: CurrentVersion, Shells: []Definition{}}, nil
	}
	if err != nil {
		return Snapshot{}, &Error{Kind: KindState, Msg: "read shell manifest", Err: err}
	}
	return m, nil
}

// EditAtPath is the shared persistence boundary for a single snapshot edit.
// It rereads under the exclusive lock, checks the schema, expires tombstones,
// and atomically writes only when apply reports a change. The returned snapshot
// is the exact state considered by the edit, including on a successful no-op.
// identityRemoval permits a deliberate live-count reduction to be observed as
// such; it is true only for forgetting or reaping an exact shell record.
//
// A nil fallback fails closed on unreadable state. The workspace compatibility
// handle supplies its previous snapshot to retain its historical recovery of a
// missing or corrupt manifest. An explicit newer schema always refuses writes.
// The callback must not call another manifest operation while holding this lock.
func EditAtPath(path string, fallback *Snapshot, identityRemoval bool, apply func(*Snapshot) (bool, error)) (Snapshot, bool, error) {
	return editAtPath(path, fallback, identityRemoval, false, apply)
}

// requireExisting retains the rename contract: disappearance of an observed
// manifest is a storage failure, not an empty project to create or adopt.
func editAtPath(path string, fallback *Snapshot, identityRemoval, requireExisting bool, apply func(*Snapshot) (bool, error)) (Snapshot, bool, error) {
	if err := checkManifestPath(path); err != nil {
		return Snapshot{}, false, err
	}
	if !requireExisting {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return Snapshot{}, false, &Error{Kind: KindState, Msg: "create shell manifest directory", Err: err}
		}
	}
	lock, err := acquireLock(path)
	if err != nil {
		return Snapshot{}, false, &Error{Kind: KindState, Msg: "lock shell manifest", Err: err}
	}
	defer releaseLock(lock)
	m, readErr := readManifest(path)
	if readErr != nil {
		if err := CheckWritableVersion(m.Version); err != nil {
			return Snapshot{}, false, err
		}
		switch {
		case fallback != nil:
			m = *fallback
			// The fallback belongs to this build; unreadable bytes cannot carry
			// an authoritative schema version.
			m.Version = CurrentVersion
		case os.IsNotExist(readErr) && !requireExisting:
			m = Snapshot{Version: CurrentVersion}
		default:
			return Snapshot{}, false, &Error{Kind: KindState, Msg: "read shell manifest", Err: readErr}
		}
	}
	if err := CheckWritableVersion(m.Version); err != nil {
		return Snapshot{}, false, err
	}
	m.Tombstones = expireTombstonesNow(m.Tombstones)
	before := len(m.Shells)
	changed, err := apply(&m)
	if err != nil {
		return Snapshot{}, false, err
	}
	if !changed {
		return m, false, nil
	}
	ObserveLiveCountWrite(path, before, len(m.Shells), identityRemoval)
	m.Version = CurrentVersion
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return Snapshot{}, false, &Error{Kind: KindState, Msg: "encode shell manifest", Err: err}
	}
	if err := os.WriteFile(path+".tmp", data, 0644); err != nil {
		return Snapshot{}, false, &Error{Kind: KindState, Msg: "write shell manifest", Err: err}
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		return Snapshot{}, false, &Error{Kind: KindState, Msg: "replace shell manifest", Err: err}
	}
	return m, true, nil
}
