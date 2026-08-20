package config

import (
	"log/slog"
	"strings"
	"time"
)

// NotificationsConfig is the app-level `notifications` section.
//
// Phase 1.5 populates only the per-source toast expiry: the defaults live in
// internal/notify's source registry, and this is how a user lengthens or
// shortens one without a rebuild. There is deliberately no configui page yet —
// Phase 4 renders the full per-source table (toast / centre / bell / expiry)
// over this same struct — but the values are user-editable in config.json from
// day one, which is the point.
//
// Example:
//
//	"notifications": {
//	  "sources": {
//	    "agent":   { "expiry": "20s" },
//	    "session": { "expiry": "sticky" }
//	  }
//	}
type NotificationsConfig struct {
	// Sources is keyed by notification source id (`agent`, `waiting`,
	// `session`, `tasks`, `td`, `system`). An unknown key is kept rather than
	// dropped: internal/notify decides what a source id means, and a config
	// written by a newer build must survive a round trip through an older one.
	Sources map[string]NotificationSourceConfig `json:"sources,omitempty"`
}

// NotificationSourceConfig is the per-source overrides.
type NotificationSourceConfig struct {
	// Expiry is how long a toast from this source stays on screen: any Go
	// duration string ("12s", "1m30s"), or "sticky" / "0" for a toast with no
	// countdown that waits for the user.
	Expiry string `json:"expiry,omitempty"`
}

// StickyExpiry is the sentinel a zero duration carries: a source whose toasts
// have no countdown. It is a named constant so callers do not have to know
// that "sticky" and 0 are the same statement.
const StickyExpiry time.Duration = 0

// SourceExpiries resolves the configured expiries into durations, keyed by
// source id. Anything unparseable is skipped with a warning rather than
// failing the load: one bad duration in a config file must not cost the user
// their notifications, let alone their startup.
func (c NotificationsConfig) SourceExpiries() map[string]time.Duration {
	if len(c.Sources) == 0 {
		return nil
	}
	out := make(map[string]time.Duration, len(c.Sources))
	for id, src := range c.Sources {
		raw := strings.TrimSpace(src.Expiry)
		if raw == "" {
			continue
		}
		if strings.EqualFold(raw, "sticky") || strings.EqualFold(raw, "never") {
			out[id] = StickyExpiry
			continue
		}
		d, err := time.ParseDuration(raw)
		if err != nil || d < 0 {
			slog.Warn("notifications: ignoring unreadable expiry", "source", id, "expiry", raw)
			continue
		}
		out[id] = d
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
