package agentintegration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/marcus/sidecar/internal/agentlifecycle"
	"github.com/marcus/sidecar/internal/agentlifecycle/lifecyclestore"
	"github.com/marcus/sidecar/internal/agentresolve"
)

// StoreSource supplies lifecycle evidence to the shared resolver by reading the
// host-local JSONL log.
//
// It is read-only by construction: it uses [lifecyclestore.ReadAll], which never
// locks, compacts, repairs, or creates anything. A polling surface must not be
// able to rewrite the log that hook processes are appending to, and it must not
// contend with them for a lock either.
//
// # Why the fold is cached behind a stat
//
// Every surface calls this on its own polling cadence, for every pane. Reading
// and parsing the whole log each time would put a file read per pane per poll
// on a path that runs continuously. Instead the fold is reused until the file's
// size or modification time changes, which costs one stat. That is the same
// no-change gate livewatch uses, for the same reason.
type StoreSource struct {
	path string

	// resolvePane maps a tmux session name to its pane id. Injectable so tests
	// do not need a tmux server.
	resolvePane func(session string) string
	// processAlive reports whether a provider generation is still running.
	processAlive func(generation string) bool
	// providerVersion reports the installed version of a provider CLI.
	providerVersion func(provider string) string

	mu       sync.Mutex
	folded   []agentlifecycle.Report
	statSize int64
	statMod  time.Time
	loaded   bool
	failed   bool

	panes    map[string]string
	versions map[string]string
}

// NewStoreSource returns a source reading the lifecycle log in stateDir.
func NewStoreSource(stateDir string) *StoreSource {
	return &StoreSource{
		path:            filepath.Join(stateDir, lifecyclestore.FileName),
		resolvePane:     tmuxPaneForSession,
		processAlive:    generationAlive,
		providerVersion: detectProviderVersion,
		panes:           map[string]string{},
		versions:        map[string]string{},
	}
}

// Evidence implements [agentresolve.Source].
func (s *StoreSource) Evidence(ref agentresolve.PaneRef) (agentresolve.Evidence, bool) {
	if s == nil {
		return agentresolve.Evidence{}, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.refresh(); err != nil {
		// An unreadable store withdraws hook authority and says so, rather than
		// guessing. The resolver turns this into an explicit fallback reason so
		// the pane is diagnosable instead of merely screen-driven.
		return agentresolve.Evidence{StoreUnavailable: true}, true
	}
	if len(s.folded) == 0 {
		// No records at all is the overwhelmingly common case and it must be
		// indistinguishable from having no source: reporting it as evidence
		// would make every pane on the machine claim a removed integration.
		return agentresolve.Evidence{}, false
	}

	paneID := s.paneID(ref)
	if paneID == "" {
		return agentresolve.Evidence{}, false
	}

	var latest *agentlifecycle.Report
	for i := range s.folded {
		if s.folded[i].Identity.PaneID == paneID {
			latest = &s.folded[i]
		}
	}
	if latest == nil {
		return agentresolve.Evidence{}, false
	}

	capability, known := agentlifecycle.CapabilityForSource(latest.Source)
	status := agentlifecycle.StatusCurrent
	switch {
	case !known:
		status = agentlifecycle.StatusUnsupported
	case latest.SourceVersion != "" && latest.SourceVersion != capability.AssetVersion:
		// The record was written by a different asset version than this build
		// ships. That is exactly what "outdated" means, and the capability's own
		// rules decide what authority, if any, survives it.
		status = agentlifecycle.StatusOutdated
	}

	// The live identity is the report's own, with the fields the resolver
	// re-checks taken from the record. This source cannot independently observe
	// the run -- it has no pane context of its own -- so the identity checks
	// that matter here are liveness and the ones the store already enforced on
	// the way in: a prior run's report is never stored, and a record whose
	// process is gone is caught below.
	live := latest.Identity

	return agentresolve.Evidence{
		Live:                  live,
		ProcessAlive:          s.processAlive(latest.Identity.ProcessGeneration),
		Capability:            capability,
		Status:                status,
		ProviderInTestedRange: s.inTestedRange(capability),
		Latest:                latest,
	}, true
}

// refresh reloads the fold when the file has changed.
func (s *StoreSource) refresh() error {
	info, err := os.Stat(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			// A missing log is not a failure. Nothing has ever reported on this
			// machine, which is the normal state before any integration is
			// installed.
			s.folded, s.loaded, s.failed = nil, true, false
			return nil
		}
		s.failed = true
		return err
	}
	if s.loaded && !s.failed && info.Size() == s.statSize && info.ModTime().Equal(s.statMod) {
		return nil
	}
	records, err := lifecyclestore.ReadAll(s.path)
	if err != nil {
		s.failed = true
		return err
	}
	s.folded, s.statSize, s.statMod, s.loaded, s.failed = records, info.Size(), info.ModTime(), true, false
	return nil
}

func (s *StoreSource) paneID(ref agentresolve.PaneRef) string {
	if ref.PaneID != "" {
		return ref.PaneID
	}
	if ref.Session == "" {
		return ""
	}
	if id, ok := s.panes[ref.Session]; ok && id != "" {
		return id
	}
	id := s.resolvePane(ref.Session)
	if id != "" {
		// Cached because the mapping only changes when a pane is split or
		// closed, and a tmux call per pane per poll is exactly the cost this
		// source exists to avoid.
		s.panes[ref.Session] = id
	}
	return id
}

func (s *StoreSource) inTestedRange(c agentlifecycle.Capability) bool {
	if c.TestedProviderRange == "" {
		return false
	}
	version, ok := s.versions[c.Provider]
	if !ok {
		version = s.providerVersion(c.Provider)
		s.versions[c.Provider] = version
	}
	return versionInRange(version, c.TestedProviderRange)
}

// versionInRange reports whether version falls inside a recorded tested range.
//
// The range is either a single version ("1.18.23") or an inclusive span
// ("1.18.23 - 1.18.25"). An unparseable version or range is not in range, which
// demotes the source to advisory: refusing to guess is the whole point of
// version-gating authority, so the ambiguous answer has to be the cautious one.
func versionInRange(version, spec string) bool {
	version = strings.TrimSpace(version)
	if version == "" {
		return false
	}
	lo, hi, found := strings.Cut(spec, "-")
	lo = strings.TrimSpace(lo)
	if !found {
		return compareVersions(version, lo) == 0
	}
	hi = strings.TrimSpace(hi)
	return compareVersions(version, lo) >= 0 && compareVersions(version, hi) <= 0
}

// compareVersions compares dotted numeric versions. Non-numeric components
// compare as zero, which is deliberate: a prerelease suffix should not make a
// version sort above the release it precedes.
func compareVersions(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var x, y int
		if i < len(as) {
			x = leadingInt(as[i])
		}
		if i < len(bs) {
			y = leadingInt(bs[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func leadingInt(s string) int {
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0
	}
	n, err := strconv.Atoi(s[:end])
	if err != nil {
		return 0
	}
	return n
}

// generationAlive reports whether the process behind a generation string is
// still running.
//
// This is what stops a crashed provider from holding a lane until its freshness
// window expires. Signal 0 checks existence without delivering anything.
func generationAlive(generation string) bool {
	pid := generationPID(generation)
	if pid <= 0 {
		// An unparseable generation cannot be shown to be dead, and treating it
		// as dead would silently disable a working integration. The resolver's
		// other identity checks still apply.
		return true
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// generationPID extracts the pid from a "pid=123,start=..." generation string.
func generationPID(generation string) int {
	for _, part := range strings.Split(generation, ",") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(part), "pid="); ok {
			n, err := strconv.Atoi(strings.TrimSpace(rest))
			if err != nil {
				return 0
			}
			return n
		}
	}
	return 0
}

func tmuxPaneForSession(session string) string {
	out, err := exec.Command("tmux", "list-panes", "-t", session, "-F", "#{pane_id}").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

func detectProviderVersion(provider string) string {
	if provider == "" {
		return ""
	}
	out, err := exec.Command(provider, "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0])
}

var _ agentresolve.Source = (*StoreSource)(nil)
