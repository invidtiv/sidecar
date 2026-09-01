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

// foregroundProcess is the small, platform-neutral slice of process-table
// state needed to decide which members still belong to a foreground job.
//
// A daemon may double-fork, be adopted by init, and retain its old process
// group. Git's fsmonitor daemon does exactly that on macOS. Such a process no
// longer belongs to the pane shell even though a group-only scan finds it.
type foregroundProcess struct {
	PID       int
	ParentPID int
	Argv0     string
}

func foregroundProcessArgv0s(group int, processes []foregroundProcess) []string {
	var leader string
	var members []string
	for _, process := range processes {
		argv0 := strings.TrimSpace(process.Argv0)
		if argv0 == "" {
			continue
		}
		// The group leader is authoritative even if its parent later exits.
		// Other init-adopted members are detached daemons, not jobs still
		// owned by the interactive shell.
		if process.PID != group && process.ParentPID == 1 {
			continue
		}
		if process.PID == group {
			leader = argv0
		} else {
			members = append(members, argv0)
		}
	}
	if leader != "" {
		return append([]string{leader}, members...)
	}
	return members
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
	identity := ResolveForegroundProcess(panePID)
	if identity == "shell" {
		return ""
	}
	return identity
}

// ResolveForegroundProcess identifies the known program in the pane's actual
// foreground process group, including "shell" when the group belongs to an
// interactive shell. Unlike pane_current_command, this is process ownership
// evidence and is therefore safe for agent-control's shell-ready gate.
func ResolveForegroundProcess(panePID int) string {
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
	shell := false

scan:
	for _, argv0 := range platformForegroundArgv0s(group) {
		switch candidate := identifyArgv0(argv0); candidate {
		case "shell":
			shell = true
		case "":
		default:
			identity = candidate
			break scan
		}
	}
	if identity == "" && shell {
		identity = "shell"
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

// ForegroundShellReady is the strict launch gate for a managed tmux pane. It
// accepts only the pane shell's own process group with exactly one member, and
// that member must resolve to a known interactive shell. Unknown helpers are
// busy, not ignorable.
func ForegroundShellReady(panePID int, currentCommand string) bool {
	if panePID <= 0 || platformForegroundProcessGroup(panePID) != panePID {
		return false
	}
	argv0s := platformForegroundArgv0s(panePID)
	if len(argv0s) != 1 || identifyArgv0(argv0s[0]) != "shell" {
		return false
	}
	return identifyProcessName(strings.ToLower(strings.TrimSpace(currentCommand))) == "shell"
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
