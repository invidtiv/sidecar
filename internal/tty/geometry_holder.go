package tty

import (
	"strings"
	"sync"
	"unicode"
)

type geometryHolder struct{ owner, kind, label string }

// Cached with the lease's existing one-second observation, never from View.
var geometryHolders sync.Map

func holderFromMetadata(token, metadataOwner, kind, label string) (string, string) {
	owner := leaseOwner(token)
	if token == "" {
		return "", ""
	}
	if metadataOwner == owner && kind != "" && len(label) <= 128 && strings.IndexFunc(label, unicode.IsControl) < 0 {
		return kind, label
	}
	if strings.Contains(owner, "-mobile-") {
		return "unknown", "Another viewer"
	}
	if host, _, ok := splitInstanceID(owner); ok {
		return "tui", "TUI on " + host
	}
	return "unknown", "Another viewer"
}

// GeometryHolderHint is presentation-only, with no subprocess or authority.
// The ordinary geometry poll supplies the cached observation for both TUIs.
func GeometryHolderHint(target string) string {
	defaultLeaseKeeper.mu.Lock()
	session := defaultLeaseKeeper.targets[target]
	defaultLeaseKeeper.mu.Unlock()
	if session == "" {
		session = target
	}
	value, ok := geometryHolders.Load(session)
	if !ok {
		return ""
	}
	holder := value.(geometryHolder)
	if holder.owner == "" || holder.owner == InstanceID() {
		return ""
	}
	return "sized for " + holder.label
}
