package agentintegration

import (
	"context"
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
	"github.com/marcus/sidecar/internal/procgroup"
	"github.com/marcus/sidecar/internal/resource"
	"github.com/marcus/sidecar/internal/tmuxserver"
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
	// namespace is the tmux socket this source resolves panes and the server
	// incarnation through. Empty means whichever server this process is already
	// talking to, which is what the polling surfaces want.
	namespace string

	// resolvePane maps a tmux session name to its pane id. Injectable so tests
	// do not need a tmux server.
	resolvePane func(session string) string
	// processAlive reports whether a provider generation is still running.
	processAlive func(generation string) bool
	// providerVersion reports the installed version of a provider CLI.
	providerVersion func(provider string) string
	// serverID reports the live tmux server incarnation, in the same form the
	// managed shell publishes and hooks record.
	serverID func() string
	// host reports this machine's name.
	host func() string

	mu       sync.Mutex
	folded   []agentlifecycle.Report
	statSize int64
	statMod  time.Time
	loaded   bool
	failed   bool

	panes        map[string]string
	versions     map[string]string
	cachedServer string
	cachedHost   string
}

// NewStoreSource returns a source reading the lifecycle log in stateDir,
// resolving tmux through whichever server this process is already talking to.
func NewStoreSource(stateDir string) *StoreSource {
	return NewStoreSourceOn(stateDir, "")
}

// NewStoreSourceOn is [NewStoreSource] bound to a specific tmux socket.
//
// The distinction is not theoretical. A caller asking about a pane it is not
// running in — `sidecar agent explain --shell TARGET` — knows which namespace
// that shell belongs to, and a bare `tmux` invocation would instead answer from
// $TMUX or the default socket. On a machine with more than one tmux server that
// silently reads the wrong server: the pane lookup returns a pane id from
// somewhere else, the server incarnation is somebody else's, and the pane being
// asked about resolves to screen fallback with no indication that the question
// was answered about the wrong machine's worth of state. That was found by
// running the Phase C gate from inside a tmux session, which is where anyone
// using Sidecar runs everything.
func NewStoreSourceOn(stateDir, namespace string) *StoreSource {
	return &StoreSource{
		path:            filepath.Join(stateDir, lifecyclestore.FileName),
		namespace:       namespace,
		resolvePane:     func(session string) string { return tmuxPaneForSession(namespace, session) },
		processAlive:    generationAlive,
		providerVersion: detectProviderVersion,
		serverID:        func() string { return liveServerIncarnation(namespace) },
		host:            hostname,
		panes:           map[string]string{},
		versions:        map[string]string{},
	}
}

// serverIncarnation returns the live tmux server identity, resolved once.
//
// Caching is safe within a process because a tmux server restart replaces the
// panes this source is asked about too: the pane lookup fails, or resolves to a
// pane whose stored records carry the old incarnation and are therefore not
// matched. Either way the answer is fallback rather than a wrong lane.
func (s *StoreSource) serverIncarnation() string {
	if s.cachedServer == "" {
		s.cachedServer = s.serverID()
	}
	return s.cachedServer
}

func (s *StoreSource) hostname() string {
	if s.cachedHost == "" {
		s.cachedHost = s.host()
	}
	return s.cachedHost
}

// liveServerIncarnation reports the current tmux server in the same form the
// managed shell publishes, so a stored record and a live observation are
// directly comparable.
func liveServerIncarnation(namespace string) string {
	out, err := exec.Command("tmux", tmuxArgs(namespace, "display-message", "-p", "#{pid}")...).Output()
	if err != nil {
		return ""
	}
	pid, ok := tmuxserver.ParsePID(strings.TrimSpace(string(out)))
	if !ok {
		return ""
	}
	return "pid=" + strconv.Itoa(pid)
}

// tmuxArgs prefixes an explicit socket when one is known.
//
// Without it every tmux call here answers from $TMUX or the default socket,
// which is the right server only by coincidence when the caller is asking about
// a pane somewhere else.
func tmuxArgs(namespace string, args ...string) []string {
	if namespace == "" {
		return args
	}
	return append([]string{"-S", namespace}, args...)
}

func hostname() string {
	name, err := os.Hostname()
	if err != nil {
		return ""
	}
	return name
}

// PaneIdentity reports the live host, tmux server incarnation, and first pane
// of a managed session, in exactly the form the report command records them.
//
// It exists for `sidecar agent explain --shell TARGET`, which asks about a pane
// it is not running in and therefore cannot derive identity from its own
// environment. Everything here is observed now, from live tmux — nothing is
// read out of a stored record, because a stored claim checked against itself is
// not a check.
//
// A pane that cannot be resolved yields an identity with an empty PaneID, which
// every caller reads as "no lifecycle evidence applies".
func PaneIdentity(namespace, session string) agentlifecycle.Identity {
	return agentlifecycle.Identity{
		Host:              hostname(),
		ServerIncarnation: liveServerIncarnation(namespace),
		PaneID:            tmuxPaneForSession(namespace, session),
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

	// The live server incarnation, resolved once and cached. Records are
	// namespaced by it, so a record from a previous tmux server must not be
	// found at all when looking up a pane on the current one.
	//
	// This is the guard that makes PID namespacing actually implement the
	// plan's recycled-pane rule. Matching on pane id alone is not enough: tmux
	// hands out %N from a per-server counter, so after a server restart the very
	// first pane is %0 again, and with the blocked and idle freshness windows
	// measured in hours a dead run's lane would be inherited by whatever
	// occupies that id next.
	server := s.serverIncarnation()
	if server == "" {
		// Without a live server identity nothing can be matched safely. Screen
		// detection is the honest answer.
		return agentresolve.Evidence{}, false
	}

	var latest *agentlifecycle.Report
	for i := range s.folded {
		id := s.folded[i].Identity
		if id.PaneID == paneID && id.ServerIncarnation == server {
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

	// The live identity is built from what is true NOW, never copied from the
	// record.
	//
	// Copying it -- which an earlier version did -- makes every identity check
	// in the resolver tautological: host, server, pane, run, and generation all
	// compare a value against itself and can never disagree, so
	// ReasonServerIncarnationNew and ReasonProcessGenChanged become unreachable
	// and the record is trusted purely because it exists. The whole point of
	// arbitration is that a stored claim is checked against the world.
	live := agentlifecycle.Identity{
		Host:              s.hostname(),
		ServerIncarnation: server,
		PaneID:            paneID,
		Provider:          latest.Identity.Provider,
		RunID:             latest.Identity.RunID,
		ProcessGeneration: latest.Identity.ProcessGeneration,
	}
	// Run and generation are the two fields this source genuinely cannot
	// observe independently -- it has no pane context of its own and the run is
	// Sidecar-assigned -- so they are carried across and defended by liveness
	// instead. generationAlive checks the full generation string, start time
	// included, so a recycled PID does not read as the same process.
	if !s.processAlive(latest.Identity.ProcessGeneration) {
		live.ProcessGeneration = "exited"
	}

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

// generationAlive reports whether the exact process behind a generation string
// is still running.
//
// "Exact" is the whole point. A generation is recorded as
// "pid=123,start=<process start time>", and the start component exists solely to
// disambiguate PID reuse. Checking only the pid — which an earlier version did —
// throws that away: a long-lived pane can outlive enough process churn for the
// number to come back around, and the new occupant would then keep a dead run's
// lane alive for the whole of an eight-hour blocked freshness window.
//
// So the pid must exist *and*, when a start time was recorded, still report the
// same one. Signal 0 checks existence without delivering anything.
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
	if proc.Signal(syscall.Signal(0)) != nil {
		return false
	}
	want := generationStart(generation)
	if want == "" {
		// Nothing recorded to compare against. The pid exists, which is all
		// that can honestly be said.
		return true
	}
	got := processStartToken(pid)
	if got == "" {
		// The start time cannot be read now. Refusing here would disable a
		// working integration on any system where ps is unavailable, so the
		// weaker liveness answer stands.
		return true
	}
	return got == want
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

// generationStart extracts the start token from a generation string.
func generationStart(generation string) string {
	for _, part := range strings.Split(generation, ",") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(part), "start="); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// processStartToken reads a process's start time in the same collapsed form
// lifecycleenv records it in, so the two are directly comparable.
func processStartToken(pid int) string {
	out, err := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.Join(strings.Fields(string(out)), "-")
}

func tmuxPaneForSession(namespace, session string) string {
	out, err := exec.Command("tmux", tmuxArgs(namespace, "list-panes", "-t", session, "-F", "#{pane_id}")...).Output()
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

// providerVersionTimeout bounds one `<provider> --version` call.
//
// Three seconds is far longer than any of these take -- every provider surveyed
// answers in under one -- and the value is not a performance tuning knob. It is
// the difference between "this provider did not tell us its version", which is
// a blank field, and Sidecar hanging.
const providerVersionTimeout = 3 * time.Second

// detectProviderVersion asks a provider CLI for its version, and gives up.
//
// The timeout is load-bearing rather than defensive hygiene. These are
// third-party binaries chosen by name from a catalog, invoked by a surface that
// asks *every* known provider at once, and one of them not returning is not
// hypothetical: `cursor --version` does not exit, so listing integrations hung
// indefinitely as soon as the catalog gained an entry named `cursor`. A version
// string is decoration on a status line; nothing decides anything on its
// absence, so the only correct response to a provider that will not answer is
// to stop waiting for it.
//
// Stdin is explicitly closed, because a binary that decides to prompt is the
// other way this blocks, and WaitDelay ensures a child that ignores the kill
// signal cannot hold the pipe open past the deadline either.
func detectProviderVersion(provider string) string {
	if provider == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), providerVersionTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, provider, "--version")
	cmd.Stdin = nil
	cmd.WaitDelay = time.Second
	// The timeout kills the child, but a provider CLI is often a wrapper that
	// forks a runtime, and killing only the wrapper leaves the grandchild alive
	// still holding the stdout pipe. Own a group and kill the group: this probe
	// exists because `cursor --version` never exits, and half-killing it would
	// have traded an infinite hang for a leak.
	procgroup.Set(cmd)
	cmd.Cancel = func() error {
		procgroup.Kill(cmd)
		return nil
	}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	first := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	// This is the output of a third-party binary on the user's PATH, and it goes
	// straight into the TUI. Unbounded and unsanitized it could carry ANSI, an
	// OSC hyperlink, or a kilobyte of text into a table cell.
	return resource.SanitizeLine(first, resource.MaxProviderVersionChars)
}

var _ agentresolve.Source = (*StoreSource)(nil)
