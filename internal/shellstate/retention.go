package shellstate

import (
	"sync"
	"time"

	"github.com/marcus/sidecar/internal/config"
)

// Tombstone retention is deliberately not a constant in a writer. A forgotten
// record is the only copy of a display name, agent type and skip-perms flag
// that exists anywhere, so how long it stays restorable is a user's decision,
// not a number one writer happens to hold — see
// docs/plans/implemented/shell-record-durability.md part D.
var (
	retentionMu     sync.Mutex
	retentionWindow *time.Duration
)

// TombstoneRetention is how long a forgotten shell record stays restorable
// before the next write drops it, from `shells.tombstoneRetention` in
// config.json. Zero means forgotten records are never expired.
//
// The config file is read once per process and cached: every manifest write
// asks this question, and one of those writes is on the startup path, where
// AGENTS.md §Startup Latency counts file opens.
func TombstoneRetention() time.Duration {
	retentionMu.Lock()
	defer retentionMu.Unlock()
	if retentionWindow != nil {
		return *retentionWindow
	}
	window := config.DefaultTombstoneRetention
	if cfg, err := config.Load(); err == nil {
		window = cfg.Shells.TombstoneRetentionWindow()
	}
	retentionWindow = &window
	return window
}

// SetTombstoneRetention overrides the resolved window for this process. It is
// for tests and for a caller that has already loaded config and wants to skip
// the lazy read; it does not write config.json.
func SetTombstoneRetention(window time.Duration) {
	retentionMu.Lock()
	defer retentionMu.Unlock()
	if window < 0 {
		window = 0
	}
	retentionWindow = &window
}

// ResetTombstoneRetention drops the cached window so the next caller re-reads
// config.json.
func ResetTombstoneRetention() {
	retentionMu.Lock()
	defer retentionMu.Unlock()
	retentionWindow = nil
}

// ExpireTombstones drops forgotten records deleted longer ago than the
// retention window. A zero or negative window keeps everything, and a
// tombstone with no deletedAt (written before the field existed, or by hand) is
// kept rather than expired immediately — the failure mode of guessing is
// deleting a record nobody asked to delete.
//
// Expiry is applied at every writer boundary rather than by a sweeper: a
// manifest only grows when something writes it, so sweeping on write is what
// bounds it, and no background job has to know where the file lives.
//
// Letting a tombstone go also makes a still-running session of that name
// adoptable again (EnsureShells and mergeShellState both skip forgotten
// names). That is intended: after the window, Sidecar no longer remembers the
// forget, and a session that is genuinely running should come back as a row
// rather than stay invisible forever.
func ExpireTombstones(tombs []Tombstone, now time.Time, window time.Duration) []Tombstone {
	if len(tombs) == 0 || window <= 0 {
		return tombs
	}
	cutoff := now.Add(-window)
	expired := 0
	for _, stone := range tombs {
		if !stone.DeletedAt.IsZero() && stone.DeletedAt.Before(cutoff) {
			expired++
		}
	}
	// Filtering in place would edit the caller's backing array even when the
	// caller only asked a question, so nothing is copied until something is
	// actually dropped.
	if expired == 0 {
		return tombs
	}
	if expired == len(tombs) {
		return nil
	}
	kept := make([]Tombstone, 0, len(tombs)-expired)
	for _, stone := range tombs {
		if !stone.DeletedAt.IsZero() && stone.DeletedAt.Before(cutoff) {
			continue
		}
		kept = append(kept, stone)
	}
	return kept
}

// expireTombstonesNow applies the configured window as of now.
func expireTombstonesNow(tombs []Tombstone) []Tombstone {
	return ExpireTombstones(tombs, time.Now().UTC(), TombstoneRetention())
}
