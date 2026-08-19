package notify

import (
	"sync"
	"time"

	"github.com/marcus/sidecar/internal/config"
)

// How long a toast stays on screen is a *setting*, not a constant. The registry
// holds the defaults; this file is the one place that answers "how long does a
// notification from this source toast for", so the store, the CLI, and any
// later headless caller all get the user's answer rather than the built-in one.

var (
	expiryMu        sync.RWMutex
	expiryOverrides map[SourceID]time.Duration
)

// SetSourceExpiries replaces the per-source expiry overrides. A source absent
// from the map keeps its registry default; a zero duration means sticky.
//
// It is package state deliberately: Normalize is called from the store, from
// the CLI, and from the app, and threading a config through all three to answer
// one duration would put configuration in the model. Callers set it once after
// loading config (see ApplyConfig).
func SetSourceExpiries(overrides map[SourceID]time.Duration) {
	expiryMu.Lock()
	defer expiryMu.Unlock()
	if len(overrides) == 0 {
		expiryOverrides = nil
		return
	}
	next := make(map[SourceID]time.Duration, len(overrides))
	for id, d := range overrides {
		if d < 0 {
			continue
		}
		next[id] = d
	}
	expiryOverrides = next
}

// ExpiryFor is how long a toast from this source stays on screen: the
// configured override if there is one, otherwise the registry default. Zero
// means the source is sticky.
func ExpiryFor(id SourceID) time.Duration {
	expiryMu.RLock()
	override, ok := expiryOverrides[id]
	expiryMu.RUnlock()
	if ok {
		return override
	}
	return SourceOf(id).DefaultExpiry
}

// ApplyConfig binds the `notifications` config section to this package. It is
// the seam between configuration and the model: nothing else in internal/notify
// reads config, and nothing outside it has to know how an expiry is stored.
func ApplyConfig(cfg config.NotificationsConfig) {
	expiries := cfg.SourceExpiries()
	if len(expiries) == 0 {
		SetSourceExpiries(nil)
		return
	}
	out := make(map[SourceID]time.Duration, len(expiries))
	for id, d := range expiries {
		out[SourceID(id)] = d
	}
	SetSourceExpiries(out)
}
