package tty

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
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
//	tmux set-option -t <session> @sidecar-owner "<instance-id>:<counter>:<idle-seconds>"
//
// The focused instance claims the lease and refreshes it; an unfocused instance
// never asserts geometry and releases what it holds. A non-owner facing a fresh
// lease declines to resize and renders the owner's geometry through the pane-fit
// path (td-73fa86).
//
// Focus alone cannot decide it. tea.FocusMsg/BlurMsg report focus in *that*
// machine's own window server, so walking from one Mac to another — or letting
// the first one's display sleep — produces no blur on the machine left behind:
// both instances sit at focused, and the abandoned one keeps refreshing forever.
// The tie-break is therefore how long each side has gone without user input,
// which the token carries as a duration the writer measured on its own clock.
// Durations survive clock skew where timestamps do not.
//
// Staleness is counted in the reader's *own* local ticks with the token treated
// as opaque, plus a wall-clock floor: a session nobody polls (the terminal
// panel, whose only ticks are resize events) would otherwise need one toggle per
// tick of the budget before it could reclaim a dead machine's lease.
//
// An unambiguous local action — attaching, entering interactive mode — claims
// outright. Pressing a key on this machine is better evidence of where the user
// is than any lease.

const leaseOptionName = "@sidecar-owner"

// LeasePolicy is the budget arbitration runs on. Ticks are the reader's own
// local ticks; they carry no cross-machine meaning. Durations are elapsed
// measurements, never timestamps, so they do carry across machines.
type LeasePolicy struct {
	// StaleTicks is how many consecutive ticks a foreign token may stay
	// unchanged before the lease is considered abandoned and claimable.
	StaleTicks int
	// StaleAfter is the wall-clock floor on the same judgement, for sessions
	// whose ticks are sporadic. Zero disables it.
	StaleAfter time.Duration
	// RefreshTicks is how many ticks the owner lets pass before writing a new
	// token, so readers elsewhere keep seeing it change.
	RefreshTicks int
	// PreemptIdle is how much longer than us the current owner must have gone
	// without user input before we take the lease off it. It doubles as the
	// window in which our own last input counts as "the user is here". Zero
	// disables idle preemption.
	PreemptIdle time.Duration
}

// DefaultLeasePolicy leaves an owner roughly two refreshes of slack before
// anyone else treats its lease as abandoned, and hands geometry to whichever
// machine the user is actually typing on within a few seconds.
var DefaultLeasePolicy = LeasePolicy{
	StaleTicks:   5,
	StaleAfter:   10 * time.Second,
	RefreshTicks: 2,
	PreemptIdle:  5 * time.Second,
}

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
	// UnchangedFor is how long Token has been identical in the reader's own
	// elapsed time.
	UnchangedFor time.Duration
	// TicksSinceWrite is how many ticks since this instance last wrote a token.
	TicksSinceWrite int
	// SelfIdle is how long since the user last gave this instance input.
	SelfIdle time.Duration
	// OwnerIdle is what the current token says its writer's idle time was when
	// it was written, valid only when OwnerIdleKnown.
	OwnerIdle      time.Duration
	OwnerIdleKnown bool
	// OwnerDefunct reports that Token was written by an instance the reader can
	// prove is gone — in practice a previous sidecar on this same machine whose
	// PID no longer exists. Only ever provable locally; a token from another
	// machine is never defunct as far as this reader is concerned.
	OwnerDefunct bool
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
	case obs.OwnerDefunct:
		// Our own previous incarnation: nothing clears the option on a crash, and
		// waiting the staleness budget out would deny the very first resize after
		// every restart — including the one-shot resize before an attach.
		return LeaseDecision{Resize: true, Write: true, Reason: "defunct"}
	case idlePreempts(obs, policy):
		// The user is typing here and has not touched the owner for a while: the
		// machine they walked away from never blurs, so this is the only signal
		// that separates two instances that both believe they are focused.
		return LeaseDecision{Resize: true, Write: true, Reason: "preempt"}
	case leaseStale(obs, policy):
		// Two instances can claim at once; the loser simply sees a foreign fresh
		// token on its next tick and backs off, costing one extra resize.
		return LeaseDecision{Resize: true, Write: true, Reason: "stale"}
	default:
		return LeaseDecision{Reason: "held"}
	}
}

// idlePreempts reports whether recent input here outranks the current owner's.
// Both halves matter: an instance nobody is using must never take a lease off
// anybody, however idle that owner looks.
func idlePreempts(obs LeaseObservation, policy LeasePolicy) bool {
	if policy.PreemptIdle <= 0 || !obs.OwnerIdleKnown {
		return false
	}
	return obs.SelfIdle <= policy.PreemptIdle && obs.OwnerIdle-obs.SelfIdle >= policy.PreemptIdle
}

// leaseStale reports whether a foreign token has sat still long enough to count
// as abandoned. The wall-clock arm still needs one prior observation: a token
// first seen this tick may have been written a millisecond ago.
func leaseStale(obs LeaseObservation, policy LeasePolicy) bool {
	if policy.StaleTicks > 0 && obs.UnchangedTicks >= policy.StaleTicks {
		return true
	}
	return policy.StaleAfter > 0 && obs.UnchangedTicks >= 1 && obs.UnchangedFor >= policy.StaleAfter
}

// leaseToken is the parsed form of an @sidecar-owner value:
//
//	<instance-id>:<counter>:<idle-seconds>
//
// The idle field is a duration its writer measured against its own clock, so it
// is comparable on any machine; a wall-clock timestamp would not be. Tokens
// without it (an older sidecar) simply carry no idle evidence.
type leaseToken struct {
	owner     string
	idle      time.Duration
	idleKnown bool
}

func parseLeaseToken(token string) leaseToken {
	fields := strings.Split(token, ":")
	if len(fields) >= 3 {
		if _, err := strconv.Atoi(fields[len(fields)-2]); err == nil {
			if secs, err := strconv.Atoi(fields[len(fields)-1]); err == nil && secs >= 0 {
				return leaseToken{
					owner:     strings.Join(fields[:len(fields)-2], ":"),
					idle:      time.Duration(secs) * time.Second,
					idleKnown: true,
				}
			}
		}
	}
	if i := strings.LastIndex(token, ":"); i >= 0 {
		return leaseToken{owner: token[:i]}
	}
	return leaseToken{owner: token}
}

// leaseOwner extracts the instance ID from a token.
func leaseOwner(token string) string {
	return parseLeaseToken(token).owner
}

// splitInstanceID takes an instance ID back apart into the host and PID
// instanceID built it from. The PID never contains "-", so the last one splits
// hostnames that do.
func splitInstanceID(id string) (host string, pid int, ok bool) {
	i := strings.LastIndex(id, "-")
	if i <= 0 {
		return "", 0, false
	}
	pid, err := strconv.Atoi(id[i+1:])
	if err != nil || pid <= 0 {
		return "", 0, false
	}
	return id[:i], pid, true
}

// processAlive reports whether a PID on this machine still exists. Signal 0
// performs the permission and existence checks without delivering anything;
// EPERM means the process is there but owned by somebody else.
func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
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
	unchangedSince  time.Time
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
	selfHost string
	alive    func(pid int) bool

	focused   bool
	lastInput time.Time
	counter   uint64
	states    map[string]*leaseState
	targets   map[string]string
}

func newLeaseKeeper(store leaseStore, policy LeasePolicy, interval time.Duration) *leaseKeeper {
	host, pid := hostAndPID()
	now := time.Now()
	return &leaseKeeper{
		store:    store,
		policy:   policy,
		interval: interval,
		now:      time.Now,
		selfID:   fmt.Sprintf("%s-%d", host, pid),
		selfHost: host,
		alive:    processAlive,
		// Focused by default so a single instance behaves exactly as it did
		// before the lease existed, including before any focus event arrives.
		focused: true,
		// Launching sidecar is itself a user action: a fresh instance counts as
		// active so it can take geometry from a machine left behind, without
		// waiting for the first keystroke.
		lastInput: now,
		states:    make(map[string]*leaseState),
		targets:   make(map[string]string),
	}
}

// noteInput records that the user just gave this instance keyboard or mouse
// input. It is the evidence idle preemption runs on, and it is deliberately
// cheap: input events arrive at typing and mouse-motion rates.
func (k *leaseKeeper) noteInput() {
	k.mu.Lock()
	k.lastInput = k.now()
	k.mu.Unlock()
}

// idleLocked is how long since the user last touched this instance.
func (k *leaseKeeper) idleLocked(now time.Time) time.Duration {
	if k.lastInput.IsZero() {
		return 0
	}
	if d := now.Sub(k.lastInput); d > 0 {
		return d
	}
	return 0
}

// tokenLocked mints this instance's next token, stamping the idle time readers
// on other machines arbitrate against.
func (k *leaseKeeper) tokenLocked(now time.Time) string {
	k.counter++
	return fmt.Sprintf("%s:%d:%d", k.selfID, k.counter, int64(k.idleLocked(now).Seconds()))
}

// hostAndPID names this instance. The PID is part of the identity so two
// sidecars on one machine do not mistake each other for themselves; recognising
// a dead PID (see defunct) is what keeps that from punishing a restart.
func hostAndPID() (string, int) {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "sidecar"
	}
	return host, os.Getpid()
}

// defunct reports whether a foreign token was left behind by a sidecar on this
// machine that has since exited. Nothing clears the option on a crash, and a
// restart draws a new PID, so without this every restart would meet its own
// leftover lease and decline to resize until the staleness budget elapsed.
func (k *leaseKeeper) defunct(token string) bool {
	host, pid, ok := splitInstanceID(leaseOwner(token))
	if !ok || host != k.selfHost {
		return false
	}
	return !k.alive(pid)
}

// allow reports whether this instance may assert geometry on target, claiming or
// refreshing the lease as the decision requires.
//
// A target whose session cannot be resolved is allowed: without a shared tmux
// server there is nobody to arbitrate with, and geometry must keep working.
//
// Ticking here rather than on a timer means the lease is refreshed by the work it
// guards. What refreshes it is the geometry loop, not the resize: a caller whose
// pane already matches skips ResizeTmuxPane and calls TouchGeometryLease instead,
// so a settled owner keeps its lease. An instance that stops polling a pane
// altogether lets that lease lapse, which is the right outcome — it is no longer
// driving that geometry.
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
		state.unchangedSince = now
	}
	state.token = token
	state.ticksSinceWrite++

	parsed := parseLeaseToken(token)
	foreign := token != "" && parsed.owner != k.selfID
	decision := DecideGeometryLease(LeaseObservation{
		SelfID:          k.selfID,
		Token:           token,
		Focused:         k.focused,
		UnchangedTicks:  state.unchangedTicks,
		UnchangedFor:    now.Sub(state.unchangedSince),
		TicksSinceWrite: state.ticksSinceWrite,
		SelfIdle:        k.idleLocked(now),
		OwnerIdle:       parsed.idle,
		OwnerIdleKnown:  foreign && parsed.idleKnown,
		// Only worth the liveness check when it could change the verdict.
		OwnerDefunct: k.focused && foreign && k.defunct(token),
	}, k.policy)

	if decision.Write {
		k.writeLocked(session, state, now)
	}
	state.owned = decision.Resize
	state.lastResize = decision.Resize
	return decision.Resize
}

// writeLocked stamps a fresh token and resets the history it invalidates.
func (k *leaseKeeper) writeLocked(session string, state *leaseState, now time.Time) {
	fresh := k.tokenLocked(now)
	k.store.set(session, fresh)
	state.token = fresh
	state.unchangedTicks = 0
	state.unchangedSince = now
	state.ticksSinceWrite = 0
}

// claim takes the lease for target outright, whatever anyone else holds.
//
// Arbitration is evidence about where the user is, and an explicit local action
// — attaching, entering interactive mode — is the strongest evidence there is.
// Without this, a machine facing a fresh foreign lease would attach at the other
// machine's preview geometry and stay letterboxed for the whole session: while
// attached, sidecar's TUI is suspended, so no tick accrues and nothing retries.
func (k *leaseKeeper) claim(target string) {
	k.mu.Lock()
	defer k.mu.Unlock()

	now := k.now()
	k.lastInput = now
	session, _, ok := k.store.read(target)
	if !ok {
		return
	}
	k.targets[target] = session
	state := k.states[session]
	if state == nil {
		state = &leaseState{}
		k.states[session] = state
	}
	k.writeLocked(session, state, now)
	state.lastTick = now
	state.owned = true
	state.lastResize = true
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
	if focused {
		// Focus is only ever gained by a deliberate act — clicking or tabbing
		// into this terminal — so it counts as the user being here, even though
		// its absence proves nothing on the machine they walked away from.
		k.lastInput = k.now()
	}
	// Tick history is meaningless across a focus change: it was accumulated
	// while this instance was not a candidate.
	release := k.dropStatesLocked(!focused)
	k.mu.Unlock()

	for _, session := range release {
		k.store.clear(session)
	}
}

// release hands back every lease this instance holds. Called on a clean exit so
// the next sidecar — here or on another machine — finds the option unset rather
// than pointing at a process that no longer exists.
func (k *leaseKeeper) release() {
	k.mu.Lock()
	sessions := k.dropStatesLocked(true)
	k.mu.Unlock()

	for _, session := range sessions {
		k.store.clear(session)
	}
}

// dropStatesLocked forgets all tick history and, when releasing, reports the
// sessions whose leases this instance was holding.
func (k *leaseKeeper) dropStatesLocked(releasing bool) []string {
	var owned []string
	if releasing {
		for session, state := range k.states {
			if state.owned {
				owned = append(owned, session)
			}
		}
	}
	k.states = make(map[string]*leaseState)
	k.targets = make(map[string]string)
	return owned
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

// NoteUserInput records keyboard or mouse input on this instance. Focus reports
// only what one machine's window server believes, and the machine the user
// walked away from never hears about it — so recent input, compared as a
// duration between machines, is what actually decides who owns geometry.
func NoteUserInput() {
	defaultLeaseKeeper.noteInput()
}

// ClaimGeometryLease takes ownership of target's session for this instance,
// overriding whatever lease it finds. Reserved for unambiguous local actions —
// attaching to a session, entering interactive mode — where the user has just
// proved which machine they are sitting at.
func ClaimGeometryLease(target string) {
	if target == "" {
		return
	}
	defaultLeaseKeeper.claim(target)
}

// TouchGeometryLease advances the lease for target without asserting geometry.
//
// The lease is refreshed by the work it guards, and callers skip ResizeTmuxPane
// once a pane already matches — so an owner that has settled on the right size
// stops refreshing and eventually looks abandoned to everyone else. Every
// geometry loop therefore ticks the lease on the passes where it decides not to
// resize; polling the pane at all is what makes this instance the one driving
// its geometry, not the resize call that occasionally follows.
func TouchGeometryLease(target string) {
	defaultLeaseKeeper.allow(target)
}

// ReleaseGeometryLeases gives up every lease this instance holds. Call it on the
// way out of the process.
func ReleaseGeometryLeases() {
	defaultLeaseKeeper.release()
}
