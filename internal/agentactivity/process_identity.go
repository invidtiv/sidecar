package agentactivity

import (
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const foregroundIdentityTTL = 2 * time.Second

type foregroundIdentityEntry struct {
	group      int
	identity   string
	resolvedAt time.Time
}

var foregroundIdentities = struct {
	sync.Mutex
	entries map[int]foregroundIdentityEntry
}{entries: make(map[int]foregroundIdentityEntry)}

// ResolveForegroundAgent identifies a pane's foreground job without shelling
// out. The platform adapter supplies process-group argv[0] values; this shared
// layer resolves symlinks and maps only exact known executable names. Results
// are briefly cached by pane PID and foreground group so active-agent polling
// does not scan the process table on every frame, while a new foreground job
// invalidates the cache immediately.
func ResolveForegroundAgent(panePID int) string {
	if panePID <= 0 {
		return ""
	}
	group := platformForegroundProcessGroup(panePID)
	if group <= 0 {
		return ""
	}
	now := time.Now()
	foregroundIdentities.Lock()
	entry, ok := foregroundIdentities.entries[panePID]
	foregroundIdentities.Unlock()
	if ok && entry.group == group && now.Sub(entry.resolvedAt) < foregroundIdentityTTL {
		return entry.identity
	}

	identity := ""
	for _, argv0 := range platformForegroundArgv0s(group) {
		if candidate := identifyArgv0(argv0); candidate != "" && candidate != "shell" {
			identity = candidate
			break
		}
	}

	foregroundIdentities.Lock()
	if len(foregroundIdentities.entries) > 256 {
		for pid, cached := range foregroundIdentities.entries {
			if now.Sub(cached.resolvedAt) >= foregroundIdentityTTL {
				delete(foregroundIdentities.entries, pid)
			}
		}
	}
	foregroundIdentities.entries[panePID] = foregroundIdentityEntry{group: group, identity: identity, resolvedAt: now}
	foregroundIdentities.Unlock()
	return identity
}

func identifyArgv0(argv0 string) string {
	argv0 = strings.TrimSpace(argv0)
	if argv0 == "" {
		return ""
	}
	resolved := argv0
	if target, err := filepath.EvalSymlinks(argv0); err == nil {
		resolved = target
	}
	name := strings.TrimPrefix(filepath.Base(resolved), "-")
	return identifyProcessName(name)
}

// HasProcessIdentity reports whether this platform can disambiguate a pane's
// foreground job by argv[0].
//
// It exists so the answer is read from the build rather than restated as a
// GOOS list somewhere else — the host protocol's hello carries this bit, and a
// second copy of "which platforms are implemented" would be wrong the moment a
// third one is added.
func HasProcessIdentity() bool { return processIdentitySupported }
