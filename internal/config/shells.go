package config

import (
	"log/slog"
	"strconv"
	"strings"
	"time"
)

// ShellsConfig is the app-level `shells` section: policy for the shell records
// Sidecar owns in each project's shells.json.
//
// It is app-level rather than a workspace-plugin setting because the records
// are written by every surface — the workspace plugin, the global Sessions
// browser, and `sidecar shell forget|restore` in a process with no plugins at
// all — and a retention window that only one of them honoured would not be a
// retention window.
//
// Example:
//
//	"shells": {
//	  "tombstoneRetention": "30d"
//	}
type ShellsConfig struct {
	// TombstoneRetention is how long a forgotten shell record stays restorable
	// before a later write drops it: a Go duration string ("336h", "90m"), a
	// whole number of days ("14d"), or "forever" / "never" to keep forgotten
	// records indefinitely.
	//
	// Empty or unreadable means DefaultTombstoneRetention. Expiring a tombstone
	// also lets a still-running session of that name be adopted again, which is
	// the point: after the window, Sidecar has no memory of the forget.
	TombstoneRetention string `json:"tombstoneRetention,omitempty"`
}

// DefaultTombstoneRetention is how long a forgotten shell record is kept when
// the config says nothing. Two weeks is long enough to cover "I deleted that
// yesterday and want it back" plus a holiday, and short enough that a
// long-lived project's shells.json does not grow one record per shell ever
// forgotten.
const DefaultTombstoneRetention = 14 * 24 * time.Hour

// KeepTombstonesForever is the sentinel a zero window carries: forgotten
// records are never expired. It is named so callers do not have to know that
// "forever" and 0 are the same statement.
const KeepTombstonesForever time.Duration = 0

// TombstoneRetentionWindow resolves the configured retention into a duration.
// An unreadable value falls back to the default with a warning rather than
// failing the load — one bad duration must not cost the user their startup,
// and least of all their shell records.
func (c ShellsConfig) TombstoneRetentionWindow() time.Duration {
	raw := strings.TrimSpace(c.TombstoneRetention)
	if raw == "" {
		return DefaultTombstoneRetention
	}
	switch strings.ToLower(raw) {
	case "forever", "never", "off":
		return KeepTombstonesForever
	}
	if d, ok := parseDays(raw); ok {
		return d
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		slog.Warn("shells: ignoring unreadable tombstoneRetention", "value", raw, "using", DefaultTombstoneRetention)
		return DefaultTombstoneRetention
	}
	if d == 0 {
		return KeepTombstonesForever
	}
	return d
}

// parseDays reads the "14d" form. time.ParseDuration stops at hours, and a
// retention window is naturally expressed in days, so "336h" should not be the
// only way to say two weeks.
func parseDays(raw string) (time.Duration, bool) {
	if !strings.HasSuffix(raw, "d") {
		return 0, false
	}
	n, err := strconv.ParseFloat(strings.TrimSuffix(raw, "d"), 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return time.Duration(n * float64(24*time.Hour)), true
}
