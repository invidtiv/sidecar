package tty

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Geometry ownership across sidecar instances (td-ee222a).
//
// Two sidecar instances on two machines can be attached to one tmux server. Each
// asserts its own pane geometry unconditionally, the last resize-window wins, and
// each re-renders in reaction to the other — a continuous ping-pong.
//
// Arbitration lives here because tmux cannot do it for us: `window-size latest`
// would require dropping `-f ignore-size` from the control attach (td-bcd2d4) and
// tracks the most recent *client*, which for sidecar means subscription churn
// rather than user input. Neither `-f ignore-size` nor `window-size manual`
// (td-816540) changes.
//
// The lease is a tmux user option on the session — the tmux server is the only
// thing both machines already share, so no new file, lock, or socket is needed:
//
//	tmux set-option -t <session> @sidecar-owner "<instance-id>:<counter>"
//
// The focused instance claims the lease and refreshes it; an unfocused instance
// never asserts geometry and releases what it holds. A non-owner facing a fresh
// lease declines to resize and renders the owner's geometry through the pane-fit
// path (td-73fa86).
//
// Staleness is counted in the reader's *own* local ticks with the token treated
// as opaque. Wall-clock timestamps are never compared between machines: their
// clocks skew, and an opaque changing token sidesteps that entirely.

const leaseOptionName = "@sidecar-owner"

// LeasePolicy is the tick budget arbitration runs on. Ticks are the reader's own
// local ticks; they carry no cross-machine meaning.
type LeasePolicy struct {
	// StaleTicks is how many consecutive ticks a foreign token may stay
	// unchanged before the lease is considered abandoned and claimable.
	StaleTicks int
	// RefreshTicks is how many ticks the owner lets pass before writing a new
	// token, so readers elsewhere keep seeing it change.
	RefreshTicks int
}

// DefaultLeasePolicy leaves an owner roughly two refreshes of slack before
// anyone else treats its lease as abandoned.
var DefaultLeasePolicy = LeasePolicy{StaleTicks: 5, RefreshTicks: 2}

// LeaseObservation is everything arbitration needs: what this instance is, what
// the shared option currently says, and how long the reader has watched it.
type LeaseObservation struct {
	// SelfID identifies this sidecar instance. A token belongs to us when it
	// carries this ID.
	SelfID string
	// Token is the raw @sidecar-owner value, empty when the option is unset.
	Token string
	// Focused reports whether the user is looking at this instance.
	Focused bool
	// UnchangedTicks is how many of the reader's ticks Token has been identical
	// for. Zero on the tick it changed.
	UnchangedTicks int
	// TicksSinceWrite is how many ticks since this instance last wrote a token.
	TicksSinceWrite int
}

// LeaseDecision is the verdict for one tick.
type LeaseDecision struct {
	// Resize reports whether this instance may assert pane geometry.
	Resize bool
	// Write reports whether it should stamp a fresh token — a claim when the
	// lease was free or stale, a refresh when it already held it.
	Write bool
	// Reason names the rule that fired, for tests and logs.
	Reason string
}

// DecideGeometryLease is the whole arbitration rule, state-free so a headless
// caller can adopt it unchanged.
func DecideGeometryLease(obs LeaseObservation, policy LeasePolicy) LeaseDecision {
	// An unfocused instance never asserts geometry, whatever the lease says.
	if !obs.Focused {
		return LeaseDecision{Reason: "unfocused"}
	}
	switch {
	case obs.Token == "":
		return LeaseDecision{Resize: true, Write: true, Reason: "unowned"}
	case leaseOwner(obs.Token) == obs.SelfID:
		// Refreshing on a cadence, not every tick, keeps tmux writes rare while
		// still giving readers a token that visibly changes.
		return LeaseDecision{Resize: true, Write: obs.TicksSinceWrite >= policy.RefreshTicks, Reason: "owner"}
	case policy.StaleTicks > 0 && obs.UnchangedTicks >= policy.StaleTicks:
		// Two instances can claim at once; the loser simply sees a foreign fresh
		// token on its next tick and backs off, costing one extra resize.
		return LeaseDecision{Resize: true, Write: true, Reason: "stale"}
	default:
		return LeaseDecision{Reason: "held"}
	}
}

// leaseOwner extracts the instance ID from a "<instance-id>:<counter>" token.
func leaseOwner(token string) string {
	if i := strings.LastIndex(token, ":"); i >= 0 {
		return token[:i]
	}
	return token
}

// leaseStore is the seam between arbitration and tmux, so the keeper is testable
// without a tmux server.
type leaseStore interface {
	// read resolves a resize target (pane ID, session name, or session:win.pane)
	// to the session holding the lease and returns the lease token, empty when
	// unset. ok is false when the target cannot be resolved.
	read(target string) (session, token string, ok bool)
	set(session, token string)
	clear(session string)
}

type tmuxLeaseStore struct{}

// read asks for the session and the option in one invocation: the lease is
// consulted on every tick of every live pane, so a second process per check is
// worth avoiding. tmux expands user options in formats, so #{@sidecar-owner}
// needs no separate show-options.
func (tmuxLeaseStore) read(target string) (string, string, bool) {
	if target == "" {
		return "", "", false
	}
	out, err := exec.Command("tmux", "display-message", "-t", target, "-p",
		"#{session_name}\t#{"+leaseOptionName+"}").Output()
	if err != nil {
		return "", "", false
	}
	session, token, found := strings.Cut(strings.TrimRight(string(out), "\r\n"), "\t")
	if !found || session == "" {
		return "", "", false
	}
	return session, strings.TrimSpace(token), true
}

func (tmuxLeaseStore) set(session, token string) {
	_ = exec.Command("tmux", "set-option", "-t", session, leaseOptionName, token).Run()
}

func (tmuxLeaseStore) clear(session string) {
	_ = exec.Command("tmux", "set-option", "-u", "-t", session, leaseOptionName).Run()
}

// leaseState is one session's tick history as this instance has observed it.
type leaseState struct {
	token           string
	unchangedTicks  int
	ticksSinceWrite int
	lastTick        time.Time
	lastResize      bool
	owned           bool
}

// leaseKeeper holds the per-session tick counters that DecideGeometryLease is
// fed from. One process-wide instance backs ResizeTmuxPane; tests build their
// own against a fake store.
type leaseKeeper struct {
	mu       sync.Mutex
	store    leaseStore
	policy   LeasePolicy
	interval time.Duration
	now      func() time.Time
	selfID   string

	focused bool
	counter uint64
	states  map[string]*leaseState
	targets map[string]string
}

func newLeaseKeeper(store leaseStore, policy LeasePolicy, interval time.Duration) *leaseKeeper {
	return &leaseKeeper{
		store:    store,
		policy:   policy,
		interval: interval,
		now:      time.Now,
		selfID:   instanceID(),
		// Focused by default so a single instance behaves exactly as it did
		// before the lease existed, including before any focus event arrives.
		focused: true,
		states:  make(map[string]*leaseState),
		targets: make(map[string]string),
	}
}

func instanceID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "sidecar"
	}
	return fmt.Sprintf("%s-%d", host, os.Getpid())
}

// allow reports whether this instance may assert geometry on target, claiming or
// refreshing the lease as the decision requires.
//
// A target whose session cannot be resolved is allowed: without a shared tmux
// server there is nobody to arbitrate with, and geometry must keep working.
//
// Ticking here rather than on a timer means the lease is refreshed by the work it
// guards. A live pane attempts a resize on every poll, so an active owner refreshes
// continuously; an owner with nothing to assert lets its lease lapse, which is the
// right outcome — it is not currently driving geometry.
func (k *leaseKeeper) allow(target string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()

	// Between ticks the previous verdict stands. Resizes are attempted on every
	// poll of a live pane; one tmux read per tick keeps that affordable and makes
	// "the reader's own local ticks" a fixed cadence rather than a poll count.
	now := k.now()
	if session, known := k.targets[target]; known {
		if state := k.states[session]; state != nil && now.Sub(state.lastTick) < k.interval {
			return state.lastResize
		}
	}

	session, token, ok := k.store.read(target)
	if !ok {
		return true
	}
	k.targets[target] = session

	state := k.states[session]
	if state == nil {
		state = &leaseState{}
		k.states[session] = state
	}
	// A second target in the same session can resolve inside an existing tick.
	if !state.lastTick.IsZero() && now.Sub(state.lastTick) < k.interval {
		return state.lastResize
	}
	state.lastTick = now

	if token == state.token {
		state.unchangedTicks++
	} else {
		state.unchangedTicks = 0
	}
	state.token = token
	state.ticksSinceWrite++

	decision := DecideGeometryLease(LeaseObservation{
		SelfID:          k.selfID,
		Token:           token,
		Focused:         k.focused,
		UnchangedTicks:  state.unchangedTicks,
		TicksSinceWrite: state.ticksSinceWrite,
	}, k.policy)

	if decision.Write {
		k.counter++
		fresh := fmt.Sprintf("%s:%d", k.selfID, k.counter)
		k.store.set(session, fresh)
		state.token = fresh
		state.unchangedTicks = 0
		state.ticksSinceWrite = 0
	}
	state.owned = decision.Resize
	state.lastResize = decision.Resize
	return decision.Resize
}

// setFocused records application focus. Losing focus releases every lease this
// instance holds, so the machine the user moved to can claim immediately instead
// of waiting the staleness budget out.
func (k *leaseKeeper) setFocused(focused bool) {
	k.mu.Lock()
	if k.focused == focused {
		k.mu.Unlock()
		return
	}
	k.focused = focused
	var release []string
	for session, state := range k.states {
		if !focused && state.owned {
			release = append(release, session)
		}
	}
	// Tick history is meaningless across a focus change: it was accumulated
	// while this instance was not a candidate.
	k.states = make(map[string]*leaseState)
	k.mu.Unlock()

	for _, session := range release {
		k.store.clear(session)
	}
}

// defaultLeaseKeeper is the arbitration ResizeTmuxPane consults. The tick
// interval is local only — it spaces this instance's own reads and never gets
// compared against another machine's clock.
var defaultLeaseKeeper = newLeaseKeeper(tmuxLeaseStore{}, DefaultLeasePolicy, time.Second)

// SetAppFocused tells geometry arbitration whether the user is looking at this
// sidecar instance. ControlManager.SetAppFocused forwards here, so every resize
// path — including the filebrowser and notes inline editors, which have no focus
// plumbing of their own — observes the same bit.
func SetAppFocused(focused bool) {
	defaultLeaseKeeper.setFocused(focused)
}
