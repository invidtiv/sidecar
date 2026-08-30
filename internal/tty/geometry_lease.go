package tty

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
// A tick is a poll, not a timer, and poll intervals here range from 200ms to 20s
// (internal/plugins/workspace/agent.go). Both halves of the budget are therefore
// bounded in elapsed time as well as in ticks: the owner refreshes at latest
// RefreshAfter, so its refresh period never scales past one poll interval, and
// the staleness floor sits well above the slowest poll so a correctly refreshing
// owner can never look abandoned to a peer polling just as slowly.
//
// An unambiguous local action — attaching, entering interactive mode — claims
// outright. Pressing a key on this machine is better evidence of where the user
// is than any lease. Actions that suspend the TUI for their whole duration
// (attaching) also hold the lease from a goroutine, since no tick accrues while
// the event loop is blocked; that goroutine supplies the missing ticks and
// nothing more, so an attach nobody is sitting at can still be preempted.
//
// While an attach is up, the user's keystrokes go to tmux rather than to
// sidecar, so this instance's own idle time is blind and only ever grows. The
// hold therefore reads back tmux's activity marker for the client on this
// machine's own terminal and counts a change in it as input here. That marker is
// opaque and only ever compared against the previous one this instance read — it
// is never measured against a local clock — so the no-cross-machine-timestamps
// rule stands.

const leaseOptionName = "@sidecar-owner"

// LeasePolicy is the budget arbitration runs on. Ticks are the reader's own
// local ticks; they carry no cross-machine meaning. Durations are elapsed
// measurements, never timestamps, so they do carry across machines.
type LeasePolicy struct {
	// StaleTicks is how many consecutive ticks a foreign token may stay
	// unchanged before the lease is considered abandoned and claimable.
	StaleTicks int
	// StaleAfter is the wall-clock floor on the same judgement, for sessions
	// whose ticks are sporadic. Zero disables it. It must stay comfortably above
	// RefreshAfter *and* above the slowest poll interval any caller ticks on,
	// since an owner can only refresh on a tick of its own.
	StaleAfter time.Duration
	// RefreshTicks is how many ticks the owner lets pass before writing a new
	// token, so readers elsewhere keep seeing it change.
	RefreshTicks int
	// RefreshAfter bounds the same cadence in elapsed time. A tick is a poll,
	// and polls run as slowly as 20s, so a tick count alone lets the owner's
	// refresh period stretch to a multiple of an arbitrary poll interval — past
	// the point where peers polling just as slowly call it abandoned. Zero
	// disables the elapsed-time arm.
	RefreshAfter time.Duration
	// PreemptIdle is how much longer than us the current owner must have gone
	// without user input before we take the lease off it. It doubles as the
	// window in which our own last input counts as "the user is here". Zero
	// disables idle preemption.
	PreemptIdle time.Duration
}

// DefaultLeasePolicy leaves an owner at least three refreshes of slack at the
// slowest poll interval in the app (20s) before anyone else treats its lease as
// abandoned, and hands geometry to whichever machine the user is actually typing
// on within a few seconds.
var DefaultLeasePolicy = LeasePolicy{
	StaleTicks:   5,
	StaleAfter:   60 * time.Second,
	RefreshTicks: 2,
	RefreshAfter: 5 * time.Second,
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
	// SinceWrite is how long since this instance last wrote a token, measured on
	// its own clock. Zero when it never has.
	SinceWrite time.Duration
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
		return LeaseDecision{Resize: true, Write: ownerRefreshes(obs, policy), Reason: "owner"}
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

// ownerRefreshes reports whether the holder of a lease should stamp a new token.
// The elapsed arm is what keeps the refresh period from scaling with the poll
// interval: on a 20s poll a two-tick cadence would only rewrite every 40s, which
// any peer polling at the same rate would read as an abandoned lease.
func ownerRefreshes(obs LeaseObservation, policy LeasePolicy) bool {
	if policy.RefreshTicks > 0 && obs.TicksSinceWrite >= policy.RefreshTicks {
		return true
	}
	return policy.RefreshAfter > 0 && obs.SinceWrite >= policy.RefreshAfter
}

// idlePreempts reports whether recent input here outranks the current owner's.
// Both halves matter: an instance nobody is using must never take a lease off
// anybody, however idle that owner looks.
//
// A live peer on a sidecar predating the idle field writes two-field tokens and
// so offers no evidence to compare: it can never be preempted, and while it
// keeps refreshing it is never stale either, so ownership stays with it until
// this machine does something explicit (attach, interactive mode). That is a
// mixed-version condition only, and the explicit-claim path is the escape.
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
	// inputMark returns an opaque marker of user input into session from this
	// machine's own terminal. It carries no meaning beyond changing when that
	// input happens; callers compare it only against the previous marker they
	// read themselves.
	inputMark(session string) string
}

// leaseClearWaiter is implemented by stores whose clear is asynchronous. A
// lifecycle spanning multiple transports waits for this completion before a
// replacement claimant may proceed; ordinary local stores clear synchronously
// and need no second interface.
type leaseClearWaiter interface {
	clearAndWait(session string) <-chan struct{}
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

// inputMark reports the activity marker of the tmux clients attached to session
// from this machine's own terminal. tmux advances a client's activity on key
// input, so a change in the marker means the user typed into tmux here — the one
// thing an attached sidecar cannot see for itself, since its own event loop is
// suspended and the keystrokes never reach it.
//
// Clients are matched on tty because that is what makes the answer local: an
// attach inherits this process's terminal, while the other machine's clients —
// and every control client on either — sit on some other tty. Without a tty we
// simply have no evidence, which leaves arbitration exactly where it was.
//
// The marker itself is a tmux clock reading, and it is deliberately treated as
// opaque: it is only ever compared with the previous marker this instance read,
// never against local time.
func (tmuxLeaseStore) inputMark(session string) string {
	tty := ownTTY()
	if session == "" || tty == "" {
		return ""
	}
	out, err := exec.Command("tmux", "list-clients", "-t", session, "-F",
		"#{client_tty}\t#{client_activity}").Output()
	if err != nil {
		return ""
	}
	var marks []string
	for _, line := range strings.Split(strings.TrimRight(string(out), "\r\n"), "\n") {
		if path, activity, found := strings.Cut(line, "\t"); found && path == tty {
			marks = append(marks, activity)
		}
	}
	return strings.Join(marks, ",")
}

// ownTTY names the terminal this process was started on, resolved once. An
// attach runs as a child sharing this terminal, so this is the name tmux reports
// back for the client the user is typing into.
var ownTTY = sync.OnceValue(func() string { return ttyPathOf(os.Stdin) })

// ttyPathOf walks /dev for the device file behind an open terminal, since the
// standard library exposes no ttyname. Empty when the file is not a terminal or
// no device matches — both of which simply mean no input evidence is available.
func ttyPathOf(f *os.File) string {
	info, err := f.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return ""
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	// /dev/pts first for Linux; on macOS the ttys live directly in /dev. "tty"
	// itself is the controlling-terminal alias, never a name tmux reports.
	for _, dir := range []string{"/dev/pts", "/dev"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if dir == "/dev" && (entry.Name() == "tty" || !strings.HasPrefix(entry.Name(), "tty")) {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			cand, err := os.Stat(path)
			if err != nil || cand.Mode()&os.ModeCharDevice == 0 {
				continue
			}
			if cst, ok := cand.Sys().(*syscall.Stat_t); ok && cst.Rdev == st.Rdev {
				return path
			}
		}
	}
	return ""
}

// leaseState is one session's tick history as this instance has observed it.
type leaseState struct {
	token           string
	unchangedTicks  int
	unchangedSince  time.Time
	ticksSinceWrite int
	lastWrite       time.Time
	lastTick        time.Time
	lastResize      bool
	owned           bool
}

// leaseHold is one target's background refresher: the func that stops it, plus
// the last tmux input marker the refresher saw, which is what tells a suspended
// instance that the user is still typing into the session it is attached to.
type leaseHold struct {
	stop      func()
	mark      string
	onAllowed func()
	allowed   bool
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

	// holdEvery is how often a held lease is refreshed from its goroutine, and
	// newTicker is the seam tests drive that goroutine through.
	holdEvery time.Duration
	newTicker func(time.Duration) (<-chan time.Time, func())

	focused   bool
	lastInput time.Time
	counter   uint64
	states    map[string]*leaseState
	targets   map[string]string
	// holds maps a target to the goroutine refreshing its lease across a TUI
	// suspension, and doubles as the flag that stamps its tokens as attended.
	holds map[string]*leaseHold
}

func newLeaseKeeper(store leaseStore, policy LeasePolicy, interval time.Duration) *leaseKeeper {
	host, pid := hostAndPID()
	now := time.Now()
	return &leaseKeeper{
		store:     store,
		policy:    policy,
		interval:  interval,
		now:       time.Now,
		selfID:    fmt.Sprintf("%s-%d", host, pid),
		selfHost:  host,
		alive:     processAlive,
		holdEvery: time.Second,
		newTicker: realTicker,
		holds:     make(map[string]*leaseHold),
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

// realTicker is the production ticker behind held leases.
func realTicker(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(d)
	return t.C, t.Stop
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
//
// Idle is always measured from the last input this instance saw, holds included.
// Stamping zero for the duration of an attach looks tempting — the user is at
// this machine, even though the keystrokes go to tmux rather than to sidecar —
// but nothing in the arbitration can observe when that stops being true, so a
// machine left attached and walked away from would defend geometry against the
// machine the user moved to for as long as the attach lasted. A hold keeps the
// token changing instead, which is enough to hold off any peer nobody is using,
// since only recent input on the peer's own side can preempt — and it harvests
// the input the user gives tmux (refreshHold), so the number stays honest in
// both directions rather than only ever climbing.
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
	allowed, _ := k.tickLocked(target, now, true)
	return allowed
}

// tickLocked is one round of arbitration for target: read the shared option,
// advance this instance's own history, apply the verdict.
//
// rateLimit skips the round when this session already ticked inside the current
// interval, which is what keeps a per-poll caller to one tmux read per tick. A
// hold's refresher passes false: while the event loop is suspended its ticks are
// the only cadence there is, so they must never be thrown away.
func (k *leaseKeeper) tickLocked(target string, now time.Time, rateLimit bool) (allowed, acquired bool) {
	session, token, ok := k.store.read(target)
	if !ok {
		return true, false
	}
	k.targets[target] = session

	state := k.states[session]
	if state == nil {
		state = &leaseState{}
		k.states[session] = state
	}
	// A second target in the same session can resolve inside an existing tick.
	if rateLimit && !state.lastTick.IsZero() && now.Sub(state.lastTick) < k.interval {
		return state.lastResize, false
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
		SinceWrite:      sinceWrite(state.lastWrite, now),
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
	acquired = decision.Resize && leaseOwner(token) != k.selfID
	return decision.Resize, acquired
}

// sinceWrite is how long ago this instance last stamped a token, zero when it
// never has — a case where the elapsed refresh arm has nothing to say anyway,
// since a lease we have never written is not ours.
func sinceWrite(lastWrite, now time.Time) time.Duration {
	if lastWrite.IsZero() {
		return 0
	}
	if d := now.Sub(lastWrite); d > 0 {
		return d
	}
	return 0
}

// writeLocked stamps a fresh token and resets the history it invalidates.
func (k *leaseKeeper) writeLocked(session string, state *leaseState, now time.Time) {
	fresh := k.tokenLocked(now)
	k.store.set(session, fresh)
	state.token = fresh
	state.unchangedTicks = 0
	state.unchangedSince = now
	state.ticksSinceWrite = 0
	state.lastWrite = now
}

// claim takes the lease for target outright, whatever anyone else holds.
//
// Arbitration is evidence about where the user is, and an explicit local action
// — attaching, entering interactive mode — is the strongest evidence there is.
// Without this, a machine facing a fresh foreign lease would attach at the other
// machine's preview geometry and stay letterboxed. A claim only makes the next
// resize land, though; keeping it is hold's job.
//
// The claim is still gated on focus: these callers are asynchronous commands, so
// one can land after the user has already moved to the other machine, and an
// unfocused instance asserts nothing whatever it was asked to do.
func (k *leaseKeeper) claim(target string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.claimLocked(target)
}

// claimLocked writes this instance onto the lease, reporting whether it did.
func (k *leaseKeeper) claimLocked(target string) bool {
	if !k.focused {
		return false
	}
	now := k.now()
	k.lastInput = now
	session, _, ok := k.store.read(target)
	if !ok {
		return false
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
	return true
}

// hold claims target and then keeps its lease refreshed from a goroutine until
// releaseHold.
//
// Attaching runs through tea.ExecProcess, which blocks the event loop for the
// whole attach: no Update, no poll, no tick. A claim alone would only buy the
// staleness budget, after which a peer merely polling the same session — even an
// unattended one, since the stale rule has no idle guard — would resize the pane
// the user is sitting in, with nothing on this side able to answer until detach.
// The refresher runs outside that loop, so the lease keeps changing.
//
// What it supplies is ticks, not authority: each one is an ordinary arbitration
// round, so an attach the user has walked away from still loses to the machine
// they are typing on rather than defending geometry until they come back.
//
// It cannot strand the lease: the goroutine lives and dies with this process, so
// a crash mid-attach stops the token changing and peers reclaim it normally.
func (k *leaseKeeper) hold(target string) {
	k.holdWithAction(target, nil)
}

// holdWithAction is hold plus a geometry assertion to run after each allowed
// periodic tick. Embedded local terminals use it to restore their own viewport
// when fresh human input preempts a remote viewer while the pane is settled.
func (k *leaseKeeper) holdWithAction(target string, onAllowed func()) {
	k.mu.Lock()
	if !k.claimLocked(target) {
		k.mu.Unlock()
		return
	}
	if hold := k.holds[target]; hold != nil {
		hold.onAllowed = onAllowed
		k.mu.Unlock()
		return
	}
	ticks, stopTicker := k.newTicker(k.holdEvery)
	done := make(chan struct{})
	finished := make(chan struct{})
	k.holds[target] = &leaseHold{
		stop: func() {
			stopTicker()
			close(done)
			<-finished
		},
		// Primed so the refresher measures input from the attach onwards. The
		// claim above has already stamped this moment as input.
		mark:      k.store.inputMark(k.targets[target]),
		onAllowed: onAllowed,
		allowed:   true,
	}
	k.mu.Unlock()

	go func() {
		defer close(finished)
		for {
			select {
			case <-done:
				return
			case <-ticks:
				k.refreshHold(target)
			}
		}
	}()
}

// refreshHold takes one arbitration tick on a held target's behalf, standing in
// for the geometry loop poll the suspended event loop cannot make, and first
// harvests whatever input the user has given tmux since the last tick.
//
// It is a tick source, not an override. Refreshing unconditionally would defend
// the lease against every peer, including the one the user actually walked over
// to; going through the ordinary verdict keeps the attach safe from peers nobody
// is using — they have no recent input to preempt with, and the token they read
// keeps changing — while still yielding to a machine that is being typed on.
//
// The harvested input is what makes that yielding survivable. Once a peer takes
// the lease, an attached instance can only get it back by preempting, and
// preemption demands recent input here; its own idle clock only grows during an
// attach, because the keystrokes go to tmux. Without this the peer would keep
// geometry for the rest of the attach however long the user sat typing at this
// machine — staleness cannot save it either, since a polling peer refreshes its
// own token forever. Reading the tty's client activity back out of tmux turns
// those keystrokes into the evidence arbitration already knows how to use.
func (k *leaseKeeper) refreshHold(target string) {
	k.mu.Lock()

	hold := k.holds[target]
	if hold == nil {
		k.mu.Unlock()
		return
	}
	now := k.now()
	if session, known := k.targets[target]; known {
		// An empty mark is absence of evidence, not evidence of input: it is
		// what a failed tmux read returns, and equally what a machine with no
		// resolvable tty returns on every tick. Treating it as a change would
		// stamp input on an attach nobody is sitting at — one unlucky exec and
		// this machine defends geometry the user has walked away from for a
		// further PreemptIdle. Only a real marker that differs is input here.
		if mark := k.store.inputMark(session); mark != "" && mark != hold.mark {
			hold.mark = mark
			k.lastInput = now
		}
	}
	allowed, acquired := k.tickLocked(target, now, false)
	onAllowed := hold.onAllowed
	transitioned := !hold.allowed && allowed
	hold.allowed = allowed
	k.mu.Unlock()
	if (acquired || transitioned) && onAllowed != nil {
		onAllowed()
	}
}

// releaseHold ends the background refresh for target. The lease itself stays put
// — the instance that just detached is still the one the user is at — and goes
// back to being refreshed by the geometry loop's ticks.
func (k *leaseKeeper) releaseHold(target string) {
	k.mu.Lock()
	hold := k.holds[target]
	delete(k.holds, target)
	k.mu.Unlock()

	if hold != nil {
		hold.stop()
	}
}

// releaseInteractive ends an embedded terminal's hold and clears the session
// only when this keeper still owns it. Unlike an attach detach, leaving
// interactive mode is an explicit handoff: a remote viewer should not wait for
// the stale budget before taking geometry. The ownership read also prevents a
// local exit from clearing a peer that already preempted it.
func (k *leaseKeeper) releaseInteractive(target string) {
	k.mu.Lock()
	hold := k.holds[target]
	delete(k.holds, target)
	session := k.targets[target]
	owned := session != "" && k.states[session] != nil && k.states[session].owned
	delete(k.targets, target)
	if session != "" {
		delete(k.states, session)
	}
	k.mu.Unlock()
	if hold != nil {
		hold.stop()
	}
	if !owned {
		return
	}
	resolved, token, ok := k.store.read(target)
	if ok && resolved == session && leaseOwner(token) == k.selfID {
		k.clearAndWait(session)
	}
}

// dropHoldsLocked stops every background refresher, for a process on its way out.
func (k *leaseKeeper) dropHoldsLocked() []func() {
	stops := make([]func(), 0, len(k.holds))
	for target, hold := range k.holds {
		stops = append(stops, hold.stop)
		delete(k.holds, target)
	}
	return stops
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
		k.clearAndWait(session)
	}
}

// release hands back every lease this instance holds. Called on a clean exit so
// the next sidecar — here or on another machine — finds the option unset rather
// than pointing at a process that no longer exists.
func (k *leaseKeeper) release() {
	k.mu.Lock()
	stops := k.dropHoldsLocked()
	sessions := k.dropStatesLocked(true)
	k.mu.Unlock()

	for _, stop := range stops {
		stop()
	}
	for _, session := range sessions {
		k.clearAndWait(session)
	}
}

func (k *leaseKeeper) clearAndWait(session string) {
	if waiter, ok := k.store.(leaseClearWaiter); ok {
		if done := waiter.clearAndWait(session); done != nil {
			<-done
		}
		return
	}
	k.store.clear(session)
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

// HoldGeometryLease claims target and keeps its lease refreshed from outside the
// event loop, for actions that suspend the TUI for their whole duration —
// attaching to a session. Pair every call with ReleaseGeometryHold on the way
// back; a crash in between is safe, since the refresher dies with the process and
// the lease then goes stale like any other.
func HoldGeometryLease(target string) {
	if target == "" {
		return
	}
	defaultLeaseKeeper.hold(target)
}

// ReleaseGeometryHold ends the background refresh started by HoldGeometryLease.
// The lease stays with this instance; it just goes back to being refreshed by the
// geometry loop.
func ReleaseGeometryHold(target string) {
	if target == "" {
		return
	}
	defaultLeaseKeeper.releaseHold(target)
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
