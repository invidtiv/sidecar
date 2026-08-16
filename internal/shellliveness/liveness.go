// Package shellliveness decides, from tmux evidence alone, whether a Sidecar
// shell's tmux session is gone.
//
// Both Workspaces surfaces — the project plugin and the global browser — must
// reach the same answer, so the rule lives here rather than in either of them.
// Each surface already watches tmux for its own reasons (the plugin polls the
// pane it is rendering, the global browser takes one `list-panes -a` per
// refresh cycle); this package turns what they already saw into a verdict and
// confirms it with one bounded probe. Nothing here runs on a timer, and nothing
// here belongs on the startup path.
//
// The asymmetry is deliberate. Leaving a dead row on screen for another poll is
// a cosmetic defect; deleting a live user's shell entry because tmux hiccuped
// is data loss. So every ambiguous signal — a missing binary, a socket we could
// not reach, a server that is not running — resolves to Unknown, and Unknown
// never closes anything.
package shellliveness

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Verdict is what the evidence supports, not what is true.
type Verdict int

const (
	// Unknown means the evidence cannot distinguish a dead session from an
	// unreachable tmux. It is the safe default and must never close a shell.
	Unknown Verdict = iota
	// Alive means tmux positively listed the session.
	Alive
	// Gone means tmux answered, and its answer did not include the session.
	Gone
)

func (v Verdict) String() string {
	switch v {
	case Alive:
		return "alive"
	case Gone:
		return "gone"
	default:
		return "unknown"
	}
}

// goneMarkers are the tmux messages that name one missing session or pane.
// They are suspicion, not proof: the probe decides.
var goneMarkers = []string{
	"can't find session",
	"can't find pane",
	"can't find window",
	"no such session",
	"session not found",
	"pane not found",
	// What tmux actually says when the target is a pane id (%7) that no longer
	// exists and the caller has no current client to fall back to. It is the
	// message the embedded terminal gets for every capture after a shell exits,
	// and missing it is why the mode never ended. tmux also says it for an empty
	// target, so every caller rejects those before capturing.
	"no current target",
}

// unknownMarkers are the tmux messages that say nothing about one session.
// "no server running" is the important one: it is also what a server restart
// looks like from the outside, and it would otherwise condemn every shell at
// once.
var unknownMarkers = []string{
	"no server running",
	"no sessions",
	"error connecting to",
	"lost server",
	"server exited",
}

// SuspectsDeath reports whether a failed tmux interaction is worth probing.
// It is intentionally cheap and text-based: the caller already has the error in
// hand, and a probe only follows a positive answer here.
func SuspectsDeath(message string) bool {
	message = strings.ToLower(message)
	for _, marker := range unknownMarkers {
		if strings.Contains(message, marker) {
			return false
		}
	}
	for _, marker := range goneMarkers {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

// SuspectsDeathErr is SuspectsDeath over an error value.
//
// tmux's actual message is almost never in the error string: a failed
// exec.Cmd.Output() wraps to "exit status 1" and hides "can't find session" in
// ExitError.Stderr. Callers that only matched err.Error() were therefore
// matching nothing at all, which is why an exited shell used to linger.
func SuspectsDeathErr(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
		message += " " + string(exitErr.Stderr)
	}
	return SuspectsDeath(message)
}

// ProbeFunc answers "is this tmux session running?" for one session name.
type ProbeFunc func(session string) Verdict

// ProbeSession asks tmux for its full session list and compares names exactly.
//
// `has-session -t name` is the obvious call and the wrong one: tmux target
// resolution falls back to prefix and pattern matching, so it answers about a
// session the caller did not name. A listing compared with == cannot do that.
// A listing also fails honestly — if tmux cannot answer at all, including when
// no server is running, the verdict is Unknown and no shell is closed.
func ProbeSession(session string) Verdict {
	if strings.TrimSpace(session) == "" {
		return Unknown
	}
	// Every other tmux call in this codebase is bounded, and this one must be
	// too. A wedged server that never answers would otherwise hang the caller's
	// command forever: in the project plugin that command is the shell's whole
	// poll chain, so the row would freeze; in the global browser the throttle
	// would keep launching replacements that never exit. A deadline reached is
	// simply no evidence, which is already the safe answer.
	ctx, cancel := context.WithTimeout(context.Background(), ProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tmux", "list-sessions", "-F", "#{session_name}")
	// Cancelling kills tmux, but Output() waits on the pipes, and a grandchild
	// that inherited them keeps them open after its parent dies. Without a wait
	// delay the deadline is advisory and the call can still hang forever.
	cmd.WaitDelay = ProbeTimeout
	output, err := cmd.Output()
	if err != nil {
		return Unknown
	}
	for _, line := range strings.Split(string(output), "\n") {
		if strings.TrimSpace(line) == session {
			return Alive
		}
	}
	return Gone
}

// ProbeTimeout bounds one probe. It is generous relative to a `list-sessions`
// against a healthy server and short enough that a wedged one cannot pin a poll
// chain for long.
const ProbeTimeout = 2 * time.Second

// DefaultProbeInterval throttles repeat probes for one session. The first
// probe after a suspicion is immediate — that is the one that closes a shell
// the user just exited. The interval only governs the stuck case, where tmux
// keeps answering Unknown: a shell that keeps failing must not spawn a tmux
// process per poll, because on a machine running an endpoint security agent
// every spawn is expensive.
const DefaultProbeInterval = 15 * time.Second

// DefaultConfirmations is how many consecutive Gone probes close a shell.
//
// One is correct here because a probe is never the only evidence: the caller
// has already observed the session missing from a listing or failing to
// capture, and the probe is the independent second opinion taken fresh from
// tmux. Two positives from two different observations beats two positives from
// the same one.
const DefaultConfirmations = 1

type entry struct {
	seenAlive   bool
	lastProbe   time.Time
	gone        int
	incarnation uint64
}

// Tracker remembers, per tmux session, what a surface has observed. It holds no
// tmux handles and starts no goroutines, so constructing one costs nothing and
// it is safe to build during plugin construction.
type Tracker struct {
	mu    sync.Mutex
	state map[string]*entry

	// Confirmations and ProbeInterval default to the constants above when zero.
	Confirmations int
	ProbeInterval time.Duration
}

// NewTracker returns an empty tracker with default policy.
func NewTracker() *Tracker { return &Tracker{} }

func (t *Tracker) get(name string) *entry {
	if t.state == nil {
		t.state = make(map[string]*entry)
	}
	e, ok := t.state[name]
	if !ok {
		e = &entry{}
		t.state[name] = e
	}
	return e
}

// Observe records positive evidence that the session is running — a listing
// that included it, a capture that succeeded, or a creation that just made it.
// It clears any accumulated suspicion: a session that answered is not dying.
// A surface must call it before a probe of that session can ever close a shell.
func (t *Tracker) Observe(name string) {
	if name == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.get(name)
	e.seenAlive = true
	e.gone = 0
	// Every sighting starts a new incarnation. tmux names are reused — an
	// offline row recreated with Enter comes back under exactly its old name —
	// so a verdict taken before this sighting is about a session that no longer
	// exists in the sense that matters, and Confirm must refuse it.
	e.incarnation++
}

// Incarnation identifies the current life of a tmux name. A caller reads it
// when it starts confirming a death and hands it back to Confirm, which is what
// makes a verdict that was overtaken by a resurrection unusable rather than
// merely late (td-6a4100).
func (t *Tracker) Incarnation(name string) uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	if e, ok := t.state[name]; ok {
		return e.incarnation
	}
	return 0
}

// SeenAlive reports whether this tracker ever observed the session running.
//
// It is the gate that separates "a shell died while Sidecar watched" from "a
// manifest entry that was already cold when Sidecar started". The second is
// what survives a reboot and what the offline-shell recreate path exists for,
// and auto-close must not eat it.
func (t *Tracker) SeenAlive(name string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.state[name]
	return ok && e.seenAlive
}

// ShouldProbe reports whether a probe for this session is due: the tracker must
// have seen the session alive, and no probe may have run inside the throttle
// window. It records the attempt.
//
// The liveness half of that gate is the whole safety property, so it lives here
// rather than in each surface. The project plugin used to substitute "this row
// has an Agent", which reads as liveness but is not: the nested sibling
// projection synthesises an Agent for rows whose tmux server this instance
// cannot even see. One gate, one meaning, both surfaces.
func (t *Tracker) ShouldProbe(name string, now time.Time) bool {
	if name == "" || !t.SeenAlive(name) {
		return false
	}
	interval := t.ProbeInterval
	if interval <= 0 {
		interval = DefaultProbeInterval
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.get(name)
	if !e.lastProbe.IsZero() && now.Sub(e.lastProbe) < interval {
		return false
	}
	e.lastProbe = now
	return true
}

// Confirm folds one probe verdict in and reports whether the shell should now
// be closed. Alive and Unknown both clear the count, so a session that flickers
// out of reach is never condemned by accumulation.
func (t *Tracker) Confirm(name string, verdict Verdict, incarnation uint64) bool {
	if name == "" {
		return false
	}
	required := t.Confirmations
	if required <= 0 {
		required = DefaultConfirmations
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.get(name)
	if verdict != Gone {
		e.gone = 0
		if verdict == Alive {
			e.seenAlive = true
		}
		return false
	}
	if !e.seenAlive {
		// Nothing this tracker watched ever ran under that name. Refuse to act
		// on a probe alone.
		return false
	}
	if incarnation != e.incarnation {
		// The name has been seen alive since this verdict was taken, so the
		// verdict is about a previous life. Closing on it would delete a shell
		// that is running right now.
		e.gone = 0
		return false
	}
	e.gone++
	if e.gone < required {
		return false
	}
	delete(t.state, name)
	return true
}

// Forget drops all state for a session, for use when a shell is closed by any
// other route so a later reuse of the name starts clean.
func (t *Tracker) Forget(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.state, name)
}
