package tty

import (
	"sync"
	"testing"
	"time"
)

// fakeLeaseStore is a tmux server's @sidecar-owner option for one session,
// shared by every keeper in a test the way a real tmux server is shared by two
// machines.
type fakeLeaseStore struct {
	mu      sync.Mutex
	token   string
	unknown bool // read() fails, as it does when tmux is unreachable
	sets    int
	reads   int
	clears  int

	// frozen models the window in which two machines have both read the option
	// but neither's write is visible to the other yet.
	frozen   bool
	snapshot string
}

func (s *fakeLeaseStore) freeze() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.frozen = true
	s.snapshot = s.token
}

func (s *fakeLeaseStore) thaw() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.frozen = false
}

func (s *fakeLeaseStore) read(string) (string, string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unknown {
		return "", "", false
	}
	s.reads++
	if s.frozen {
		return "sess", s.snapshot, true
	}
	return "sess", s.token, true
}

func (s *fakeLeaseStore) set(_, token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.token = token
	s.sets++
}

func (s *fakeLeaseStore) clear(string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.token = ""
	s.clears++
}

func (s *fakeLeaseStore) current() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.token
}

// newTestKeeper builds a keeper whose every allow() counts as a tick, so tests
// drive tick history directly instead of sleeping.
func newTestKeeper(store leaseStore, id string, policy LeasePolicy) *leaseKeeper {
	k := newLeaseKeeper(store, policy, 0)
	k.selfID = id
	return k
}

func TestDecideGeometryLease(t *testing.T) {
	policy := LeasePolicy{StaleTicks: 3, StaleAfter: 10 * time.Second, RefreshTicks: 2, PreemptIdle: 5 * time.Second}
	tests := []struct {
		name       string
		obs        LeaseObservation
		wantResize bool
		wantWrite  bool
		wantReason string
	}{
		{
			name:       "unfocused never asserts geometry",
			obs:        LeaseObservation{SelfID: "a", Token: "a:1", Focused: false},
			wantReason: "unfocused",
		},
		{
			name:       "unfocused with a stale foreign lease still declines",
			obs:        LeaseObservation{SelfID: "a", Token: "b:1", UnchangedTicks: 99},
			wantReason: "unfocused",
		},
		{
			name:       "unowned lease is claimed",
			obs:        LeaseObservation{SelfID: "a", Focused: true},
			wantResize: true, wantWrite: true, wantReason: "unowned",
		},
		{
			name:       "owner resizes without rewriting every tick",
			obs:        LeaseObservation{SelfID: "a", Token: "a:7", Focused: true, TicksSinceWrite: 1},
			wantResize: true, wantReason: "owner",
		},
		{
			name:       "owner refreshes on cadence",
			obs:        LeaseObservation{SelfID: "a", Token: "a:7", Focused: true, TicksSinceWrite: 2},
			wantResize: true, wantWrite: true, wantReason: "owner",
		},
		{
			name:       "fresh foreign lease is respected",
			obs:        LeaseObservation{SelfID: "a", Token: "b:4", Focused: true, UnchangedTicks: 2},
			wantReason: "held",
		},
		{
			name:       "a lease from a dead local instance is claimed at once",
			obs:        LeaseObservation{SelfID: "a", Token: "b:4", Focused: true, OwnerDefunct: true},
			wantResize: true, wantWrite: true, wantReason: "defunct",
		},
		{
			name:       "stale foreign lease is claimable",
			obs:        LeaseObservation{SelfID: "a", Token: "b:4", Focused: true, UnchangedTicks: 3},
			wantResize: true, wantWrite: true, wantReason: "stale",
		},
		{
			name: "an owner the user left behind is preempted by recent input here",
			obs: LeaseObservation{SelfID: "a", Token: "b:4:60", Focused: true, UnchangedTicks: 1,
				SelfIdle: 0, OwnerIdle: 60 * time.Second, OwnerIdleKnown: true},
			wantResize: true, wantWrite: true, wantReason: "preempt",
		},
		{
			name: "an instance nobody is using never preempts",
			obs: LeaseObservation{SelfID: "a", Token: "b:4:60", Focused: true, UnchangedTicks: 1,
				SelfIdle: 45 * time.Second, OwnerIdle: 60 * time.Second, OwnerIdleKnown: true},
			wantReason: "held",
		},
		{
			name: "an owner still in use keeps the lease",
			obs: LeaseObservation{SelfID: "a", Token: "b:4:1", Focused: true, UnchangedTicks: 1,
				SelfIdle: 0, OwnerIdle: time.Second, OwnerIdleKnown: true},
			wantReason: "held",
		},
		{
			name: "a token with no idle evidence is never preempted",
			obs: LeaseObservation{SelfID: "a", Token: "b:4", Focused: true, UnchangedTicks: 1,
				SelfIdle: 0, OwnerIdle: time.Hour},
			wantReason: "held",
		},
		{
			name: "a token unchanged for long enough is stale on few ticks",
			obs: LeaseObservation{SelfID: "a", Token: "b:4", Focused: true,
				UnchangedTicks: 1, UnchangedFor: 30 * time.Second},
			wantResize: true, wantWrite: true, wantReason: "stale",
		},
		{
			name: "a token first seen this tick is never stale on elapsed time alone",
			obs: LeaseObservation{SelfID: "a", Token: "b:4", Focused: true,
				UnchangedTicks: 0, UnchangedFor: time.Hour},
			wantReason: "held",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DecideGeometryLease(tt.obs, policy)
			if got.Resize != tt.wantResize || got.Write != tt.wantWrite || got.Reason != tt.wantReason {
				t.Errorf("DecideGeometryLease() = %+v, want resize=%v write=%v reason=%q",
					got, tt.wantResize, tt.wantWrite, tt.wantReason)
			}
		})
	}
}

func TestLeaseOwner(t *testing.T) {
	tests := []struct{ token, want string }{
		{"mac-mini-4821:17", "mac-mini-4821"},
		{"host:with:colons:3", "host:with:colons"},
		{"bare", "bare"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := leaseOwner(tt.token); got != tt.want {
			t.Errorf("leaseOwner(%q) = %q, want %q", tt.token, got, tt.want)
		}
	}
}

func TestLeaseKeeperOwnerResizes(t *testing.T) {
	store := &fakeLeaseStore{}
	k := newTestKeeper(store, "owner", DefaultLeasePolicy)

	if !k.allow("%1") {
		t.Fatal("first resize on an unowned session was declined")
	}
	if got := store.current(); got != "owner:1:0" {
		t.Fatalf("lease = %q, want owner:1:0", got)
	}
	for i := range 10 {
		if !k.allow("%1") {
			t.Fatalf("owner declined its own resize on tick %d", i)
		}
	}
	if leaseOwner(store.current()) != "owner" {
		t.Fatalf("lease drifted away from the owner: %q", store.current())
	}
}

func TestLeaseKeeperNonOwnerDeclinesFreshLease(t *testing.T) {
	store := &fakeLeaseStore{}
	other := newTestKeeper(store, "other", DefaultLeasePolicy)
	k := newTestKeeper(store, "self", DefaultLeasePolicy)

	other.allow("%1")
	for i := range 20 {
		// The owner keeps refreshing, so the token never sits still long enough
		// to look abandoned.
		other.allow("%1")
		if k.allow("%1") {
			t.Fatalf("non-owner asserted geometry on tick %d against a fresh lease", i)
		}
	}
	if leaseOwner(store.current()) != "other" {
		t.Fatalf("lease = %q, want it still held by other", store.current())
	}
}

func TestLeaseKeeperClaimsStaleLease(t *testing.T) {
	store := &fakeLeaseStore{}
	policy := LeasePolicy{StaleTicks: 3, RefreshTicks: 2}
	store.set("sess", "gone:1")
	k := newTestKeeper(store, "self", policy)

	// The token is only counted as unchanged from the second observation on, so
	// StaleTicks ticks of silence take StaleTicks+1 reads.
	for i := range policy.StaleTicks {
		if k.allow("%1") {
			t.Fatalf("claimed a lease after only %d unchanged ticks, want %d", i, policy.StaleTicks)
		}
	}
	if !k.allow("%1") {
		t.Fatal("stale lease was not claimed")
	}
	if leaseOwner(store.current()) != "self" {
		t.Fatalf("lease = %q, want it claimed by self", store.current())
	}
}

func TestLeaseKeeperSimultaneousClaimSettles(t *testing.T) {
	store := &fakeLeaseStore{}
	policy := LeasePolicy{StaleTicks: 2, RefreshTicks: 2}
	store.set("sess", "gone:1")
	a := newTestKeeper(store, "a", policy)
	b := newTestKeeper(store, "b", policy)

	// Both instances watch the same abandoned lease. On the tick it goes stale
	// neither has seen the other's write yet, so both claim.
	var claimedA, claimedB bool
	for i := range policy.StaleTicks + 1 {
		if i == policy.StaleTicks {
			store.freeze()
		}
		claimedA = a.allow("%1")
		claimedB = b.allow("%1")
	}
	store.thaw()
	if !claimedA || !claimedB {
		t.Fatalf("expected both to claim the abandoned lease, got a=%v b=%v", claimedA, claimedB)
	}
	if leaseOwner(store.current()) != "b" {
		t.Fatalf("lease = %q, want the last writer to hold it", store.current())
	}

	// The loser backs off on its next tick and stays backed off: one extra
	// resize, then no sustained flapping.
	for i := range 10 {
		if a.allow("%1") {
			t.Fatalf("loser re-asserted geometry on tick %d", i)
		}
		if !b.allow("%1") {
			t.Fatalf("winner lost its own lease on tick %d", i)
		}
	}
}

func TestLeaseKeeperFocusLossReleasesOwnership(t *testing.T) {
	store := &fakeLeaseStore{}
	k := newTestKeeper(store, "self", DefaultLeasePolicy)
	other := newTestKeeper(store, "other", DefaultLeasePolicy)

	if !k.allow("%1") {
		t.Fatal("failed to take an unowned lease")
	}
	k.setFocused(false)

	if store.current() != "" {
		t.Fatalf("lease = %q, want it released on focus loss", store.current())
	}
	if k.allow("%1") {
		t.Fatal("unfocused instance asserted geometry")
	}
	if store.current() != "" {
		t.Fatalf("unfocused instance wrote a lease: %q", store.current())
	}
	// The machine the user moved to takes over immediately, without waiting the
	// staleness budget out.
	if !other.allow("%1") {
		t.Fatal("released lease was not immediately claimable")
	}
	if leaseOwner(store.current()) != "other" {
		t.Fatalf("lease = %q, want other", store.current())
	}

	// Regaining focus makes this instance a candidate again, but only once the
	// other side's lease goes stale.
	k.setFocused(true)
	if k.allow("%1") {
		t.Fatal("refocused instance stole a fresh foreign lease")
	}
}

func TestLeaseKeeperTicksAreRateLimited(t *testing.T) {
	store := &fakeLeaseStore{}
	k := newLeaseKeeper(store, DefaultLeasePolicy, time.Second)
	k.selfID = "self"
	now := time.Now()
	k.now = func() time.Time { return now }

	if !k.allow("%1") {
		t.Fatal("first resize declined")
	}
	reads, writes := store.reads, store.sets
	for range 50 {
		if !k.allow("%1") {
			t.Fatal("cached verdict flipped between ticks")
		}
	}
	if store.reads != reads || store.sets != writes {
		t.Fatalf("hit tmux inside one tick: reads %d->%d, writes %d->%d",
			reads, store.reads, writes, store.sets)
	}

	// Crossing the interval advances a tick, and the owner refreshes on cadence.
	for range DefaultLeasePolicy.RefreshTicks {
		now = now.Add(2 * time.Second)
		k.allow("%1")
	}
	if store.sets <= writes {
		t.Fatal("owner never refreshed its lease across ticks")
	}
}

func TestSplitInstanceID(t *testing.T) {
	tests := []struct {
		id     string
		host   string
		pid    int
		wantOK bool
	}{
		{"mac-mini-4821", "mac-mini", 4821, true},
		{"sidecar-1", "sidecar", 1, true},
		{"nopid", "", 0, false},
		{"-4821", "", 0, false},
		{"host-notanumber", "", 0, false},
		{"", "", 0, false},
	}
	for _, tt := range tests {
		host, pid, ok := splitInstanceID(tt.id)
		if ok != tt.wantOK || host != tt.host || pid != tt.pid {
			t.Errorf("splitInstanceID(%q) = (%q, %d, %v), want (%q, %d, %v)",
				tt.id, host, pid, ok, tt.host, tt.pid, tt.wantOK)
		}
	}
}

// Nothing clears @sidecar-owner when sidecar quits or crashes, and a restart
// draws a new PID — so the very next run used to meet its own leftover lease,
// read it as foreign, and decline every resize until the staleness budget
// elapsed. The one-shot resize before an attach never gets that many ticks.
func TestLeaseKeeperReclaimsLeaseFromItsOwnDeadPredecessor(t *testing.T) {
	store := &fakeLeaseStore{}
	store.set("sess", "mac-9012:37")
	k := newTestKeeper(store, "mac-9977", DefaultLeasePolicy)
	k.selfHost = "mac"
	k.alive = func(int) bool { return false }

	if !k.allow("%1") {
		t.Fatal("a restarted instance declined to resize against its own leftover lease")
	}
	if leaseOwner(store.current()) != "mac-9977" {
		t.Fatalf("lease = %q, want it reclaimed by the new instance", store.current())
	}
}

func TestLeaseKeeperRespectsLiveInstances(t *testing.T) {
	tests := []struct {
		name  string
		token string
		host  string
		alive bool
	}{
		// A second sidecar on this machine is a real peer, not a corpse.
		{name: "live local pid", token: "mac-9012:37", host: "mac", alive: true},
		// Another machine's PID means nothing here; it must never be probed.
		{name: "remote host", token: "laptop-9012:37", host: "mac", alive: false},
		// A token we cannot take apart tells us nothing either.
		{name: "opaque owner", token: "someone:37", host: "mac", alive: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeLeaseStore{}
			store.set("sess", tt.token)
			k := newTestKeeper(store, "mac-9977", DefaultLeasePolicy)
			k.selfHost = tt.host
			probed := false
			k.alive = func(int) bool {
				probed = true
				return tt.alive
			}

			for i := range DefaultLeasePolicy.StaleTicks {
				if k.allow("%1") {
					t.Fatalf("stole a live instance's lease on tick %d", i)
				}
			}
			if tt.name == "remote host" && probed {
				t.Fatal("probed a PID on another machine")
			}
			if store.current() != tt.token {
				t.Fatalf("lease = %q, want it untouched at %q", store.current(), tt.token)
			}
		})
	}
}

func TestLeaseKeeperReleaseHandsBackOwnership(t *testing.T) {
	store := &fakeLeaseStore{}
	k := newTestKeeper(store, "self", DefaultLeasePolicy)
	other := newTestKeeper(store, "other", DefaultLeasePolicy)

	if !k.allow("%1") {
		t.Fatal("failed to take an unowned lease")
	}
	k.release()
	if store.current() != "" {
		t.Fatalf("lease = %q, want it released on exit", store.current())
	}
	// A successor finds the option unset instead of pointing at a dead process.
	if !other.allow("%1") {
		t.Fatal("released lease was not immediately claimable")
	}
}

// A settled owner stops calling ResizeTmuxPane, so its lease is only kept alive
// by the geometry loop ticking it anyway. Without that tick the owner goes
// stale, the peer claims, and ownership ping-pongs on the staleness period.
func TestLeaseKeeperSettledOwnerKeepsLeaseByTicking(t *testing.T) {
	store := &fakeLeaseStore{}
	owner := newTestKeeper(store, "owner", DefaultLeasePolicy)
	peer := newTestKeeper(store, "peer", DefaultLeasePolicy)

	if !owner.allow("%1") {
		t.Fatal("failed to take an unowned lease")
	}
	for i := range 20 {
		// The owner's pane already matches, so nothing is resized — this is the
		// bare touch the geometry loop performs on every poll.
		owner.allow("%1")
		if peer.allow("%1") {
			t.Fatalf("peer claimed a settled owner's lease on tick %d", i)
		}
	}
	if leaseOwner(store.current()) != "owner" {
		t.Fatalf("lease = %q, want it still held by the settled owner", store.current())
	}
}

// newClockedKeeper builds a keeper on a fake clock so tests can drive elapsed
// time — ticks, staleness, and idleness — without sleeping.
func newClockedKeeper(store leaseStore, id string, policy LeasePolicy, now *time.Time) *leaseKeeper {
	k := newLeaseKeeper(store, policy, time.Second)
	k.selfID = id
	k.now = func() time.Time { return *now }
	k.lastInput = *now
	return k
}

// Focus is reported by each machine's own window server, so walking away from
// one Mac never blurs it: both instances stay focused, the abandoned one keeps
// refreshing, and the machine the user is actually at could never take over.
// Recent input, compared as a duration, is what breaks the tie.
func TestLeaseKeeperPreemptsAnOwnerTheUserWalkedAwayFrom(t *testing.T) {
	store := &fakeLeaseStore{}
	now := time.Now()
	left := newClockedKeeper(store, "left", DefaultLeasePolicy, &now)
	here := newClockedKeeper(store, "here", DefaultLeasePolicy, &now)

	// The machine the user started on owns the lease and keeps polling the pane.
	if !left.allow("%1") {
		t.Fatal("first instance failed to take an unowned lease")
	}
	tick := func() {
		now = now.Add(time.Second)
		left.allow("%1")
	}
	for range 3 {
		tick()
	}

	// The user walks to the other machine and types. Neither instance ever loses
	// focus, and the one left behind never stops refreshing.
	claimed := false
	for range 10 {
		now = now.Add(time.Second)
		here.noteInput()
		left.allow("%1")
		if here.allow("%1") {
			claimed = true
			break
		}
	}
	if !claimed {
		t.Fatal("the machine the user is typing on never took geometry from the one they left")
	}
	if leaseOwner(store.current()) != "here" {
		t.Fatalf("lease = %q, want it held by the machine with recent input", store.current())
	}

	// And it stays put: the abandoned machine must not take it back.
	for i := range 10 {
		now = now.Add(time.Second)
		here.noteInput()
		if left.allow("%1") {
			t.Fatalf("the abandoned machine reclaimed geometry on tick %d", i)
		}
		if !here.allow("%1") {
			t.Fatalf("the active machine lost its own lease on tick %d", i)
		}
	}
}

// An idle instance must never take geometry off anybody: with no user at either
// machine, whoever holds the lease keeps it.
func TestLeaseKeeperIdleInstanceNeverPreempts(t *testing.T) {
	store := &fakeLeaseStore{}
	now := time.Now()
	owner := newClockedKeeper(store, "owner", DefaultLeasePolicy, &now)
	peer := newClockedKeeper(store, "peer", DefaultLeasePolicy, &now)

	if !owner.allow("%1") {
		t.Fatal("failed to take an unowned lease")
	}
	// Nobody touches either machine for minutes; both keep polling.
	for i := range 60 {
		now = now.Add(time.Second)
		owner.allow("%1")
		if peer.allow("%1") {
			t.Fatalf("an unused instance preempted a live owner on tick %d", i)
		}
	}
}

// The terminal panel session's only lease events used to be resize commands, so
// reclaiming a dead machine's lease cost one panel toggle per tick of the
// staleness budget — unbounded wall-clock time. Elapsed time settles it instead.
func TestLeaseKeeperClaimsALeaseNobodyRefreshesOnElapsedTime(t *testing.T) {
	store := &fakeLeaseStore{}
	store.set("sess", "dead-mac-4821:9:0")
	now := time.Now()
	k := newClockedKeeper(store, "here", DefaultLeasePolicy, &now)

	// First sight of the token proves nothing: it could have been written a
	// moment ago, so this instance defers.
	if k.allow("%1") {
		t.Fatal("claimed a foreign lease on first sight")
	}
	// One later event — a second panel toggle, minutes on — is enough, because a
	// live owner would have refreshed within a couple of seconds.
	now = now.Add(2 * time.Minute)
	if !k.allow("%1") {
		t.Fatal("a lease nobody has refreshed for two minutes was still declined")
	}
	if leaseOwner(store.current()) != "here" {
		t.Fatalf("lease = %q, want it claimed here", store.current())
	}
}

// Attaching (and entering interactive mode) is unambiguous proof of which
// machine the user is at. Nothing retries the one-shot resize that precedes an
// attach — the TUI is suspended for its whole duration — so a fresh foreign
// lease must not be able to block it.
func TestLeaseKeeperExplicitClaimBeatsAFreshForeignLease(t *testing.T) {
	store := &fakeLeaseStore{}
	other := newTestKeeper(store, "other", DefaultLeasePolicy)
	k := newTestKeeper(store, "self", DefaultLeasePolicy)

	other.allow("%1")
	if k.allow("%1") {
		t.Fatal("non-owner asserted geometry against a fresh lease")
	}

	k.claim("%1")
	if leaseOwner(store.current()) != "self" {
		t.Fatalf("lease = %q, want it claimed by the attaching instance", store.current())
	}
	if !k.allow("%1") {
		t.Fatal("resize was declined immediately after an explicit claim")
	}
}

func TestParseLeaseToken(t *testing.T) {
	tests := []struct {
		token     string
		owner     string
		idle      time.Duration
		idleKnown bool
	}{
		{token: "mac-mini-4821:17:42", owner: "mac-mini-4821", idle: 42 * time.Second, idleKnown: true},
		{token: "mac-mini-4821:17:0", owner: "mac-mini-4821", idleKnown: true},
		// A token from an older sidecar carries no idle evidence.
		{token: "mac-mini-4821:17", owner: "mac-mini-4821"},
		{token: "host:with:colons:3", owner: "host:with:colons"},
		{token: "bare", owner: "bare"},
		{token: "", owner: ""},
	}
	for _, tt := range tests {
		got := parseLeaseToken(tt.token)
		if got.owner != tt.owner || got.idle != tt.idle || got.idleKnown != tt.idleKnown {
			t.Errorf("parseLeaseToken(%q) = %+v, want owner=%q idle=%v known=%v",
				tt.token, got, tt.owner, tt.idle, tt.idleKnown)
		}
	}
}

func TestLeaseKeeperAllowsWhenSessionUnknown(t *testing.T) {
	// No resolvable session means no shared tmux server to arbitrate with;
	// geometry must keep working exactly as it did before the lease existed.
	store := &fakeLeaseStore{unknown: true}
	k := newTestKeeper(store, "self", DefaultLeasePolicy)
	if !k.allow("%1") {
		t.Fatal("declined a resize on an unresolvable target")
	}
}
