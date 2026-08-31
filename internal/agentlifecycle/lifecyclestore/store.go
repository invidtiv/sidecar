// Package lifecyclestore persists agent lifecycle reports.
//
// It is deliberately a subpackage rather than part of [agentlifecycle] itself.
// That package's contract is that it is pure — no filesystem, no tmux, no CLI —
// which is what lets its resolver and schemas be table-tested as plain values.
// The store is the one piece of the lifecycle core that must touch a disk, so
// it lives one level down, next to the harness, and depends on the pure package
// rather than the other way round.
//
// Two implementations satisfy [Store]: [Memory], for tests and for callers that
// want lifecycle semantics without a file, and [JSONL], the host-local
// append-only log that hook processes, the TUI, and the CLI all share. They are
// held to the same behavior by one contract test, because a memory store that
// quietly accepts what the real one rejects is worse than no memory store.
//
// # What is stored
//
// Bounded lifecycle facts and opaque identity only: lanes, outcomes, reason
// codes from a frozen allowlist, sequences, timestamps, and salted digests.
// Never prompt text, response text, tool arguments or results, environment
// values, credentials, or provider paths. [agentlifecycle.Validate] and
// [agentlifecycle.SanitizeDetail] are applied to every record before it is
// written, so this is enforced at the seam rather than trusted to callers.
//
// # Ordering and runs
//
// Sequences strictly increase within a [agentlifecycle.Key] — server
// incarnation, pane, source, run. Everything is namespaced by tmux server
// incarnation so that a recycled %pane identifier after a server restart cannot
// inherit the previous occupant's authority. A report for a run this pane has
// already moved on from is rejected rather than stored, which is what stops an
// old run replaying into authority after a restart.
package lifecyclestore

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/marcus/sidecar/internal/agentlifecycle"
)

// Bounds on retained history. Compaction enforces these; nothing else does, so
// a store that is never compacted grows until it is.
const (
	// HistoryPerKey is how many records survive compaction for one
	// (server, pane, source, run). The latest is what arbitration reads; the
	// rest are diagnostic, which is why the number is small.
	HistoryPerKey = 8

	// MaxRunsPerPane bounds how many distinct runs a pane retains. A long-lived
	// pane can host many agent runs over a day and the old ones stop being
	// interesting as soon as the next one starts.
	MaxRunsPerPane = 4

	// MaxRecords is the hard ceiling after per-key and per-run trimming. It
	// exists because the other two bounds are per-pane, and a machine with many
	// panes could still accumulate without a global limit.
	MaxRecords = 5000
)

// Errors a caller is expected to branch on. Everything else is I/O.
var (
	// ErrStaleSequence means the sequence did not advance within its key: the
	// report is out of order, or reuses a sequence for a different fact.
	ErrStaleSequence = errors.New("lifecyclestore: stale sequence")

	// ErrPriorRun means the report belongs to a run this pane has already moved
	// past. It is refused rather than stored so it can never be replayed into
	// authority.
	ErrPriorRun = errors.New("lifecyclestore: report from a prior run")
)

// PaneKey is the arbitration scope: one pane on one tmux server incarnation.
//
// It is intentionally coarser than [agentlifecycle.Key], which also names the
// source and run. Latest has to be able to return a report whose source or run
// does *not* match the live one — otherwise the resolver's source-mismatch and
// run-mismatch fallback reasons would be unreachable, and a pane whose agent
// was replaced would silently look like a pane with no integration.
type PaneKey struct {
	ServerIncarnation string
	PaneID            string
}

// PaneKeyFor returns the pane scope a report belongs to.
func PaneKeyFor(r agentlifecycle.Report) PaneKey {
	return PaneKey{ServerIncarnation: r.Identity.ServerIncarnation, PaneID: r.Identity.PaneID}
}

// Store is the narrow persistence interface the rest of Sidecar depends on.
//
// Keeping it this small is what makes the JSONL default replaceable: nothing
// here mentions a file, a path, a lock, or a line. A future socket or database
// transport implements these five methods and no caller changes.
type Store interface {
	// Append validates and stores one report, returning how it was accepted.
	Append(r agentlifecycle.Report) (agentlifecycle.Acceptance, error)

	// AppendNext stores one report at the next free sequence within its key,
	// assigned under the same exclusive lock the append itself takes, and
	// returns the report as stored.
	//
	// This exists because "the reporter knows its own sequence" is a property of
	// exactly one shape of integration. OpenCode's plugin is a single long-lived
	// process that can hold a counter in memory. Codex and Claude Code run each
	// hook as an independent short-lived process with no shared state, so every
	// one of them would have to guess, and guesses collide: the store enforces a
	// strictly increasing sequence per run and correctly rejects the loser,
	// which silently drops a report.
	//
	// Assigning here is the only place it can be done correctly, because it is
	// the only place that already holds the lock that makes read-then-write
	// atomic. A caller that computed "latest + 1" first and appended second
	// would have a race between the two.
	AppendNext(r agentlifecycle.Report) (agentlifecycle.Report, agentlifecycle.Acceptance, error)

	// Latest returns the newest retained report for a pane, of any kind, from
	// the pane's current run.
	Latest(k PaneKey) (agentlifecycle.Report, bool)

	// Release records an explicit surrender of authority. r.Kind must be
	// [agentlifecycle.KindRelease].
	Release(r agentlifecycle.Report) (agentlifecycle.Acceptance, error)

	// List returns the retained records for a pane in append order.
	List(k PaneKey) []agentlifecycle.Report

	// Compact trims the store to its retention bounds.
	Compact() error
}

// index holds the fold shared by both implementations.
//
// Both stores answer from this; the JSONL store additionally persists. Putting
// the decision logic here rather than in each implementation is what makes the
// contract test meaningful — the two cannot disagree about ordering, run
// rotation, or duplicate detection, because there is only one copy of it.
type index struct {
	// records is every retained record in append order. It is the source of
	// truth for List and for compaction; the maps below are derived.
	records []agentlifecycle.Report

	// highSeq is the greatest accepted sequence per key.
	highSeq map[agentlifecycle.Key]uint64
	// latestForKey lets a duplicate be compared against the record it
	// duplicates, so a replay is idempotent but a sequence reused for a
	// different fact is not.
	latestForKey map[agentlifecycle.Key]agentlifecycle.Report

	// currentRun is the run a pane is on now. seenRuns is every run the pane
	// has ever been on, which is how a late report from an earlier run is told
	// apart from the first report of a new one.
	currentRun map[PaneKey]string
	seenRuns   map[PaneKey]map[string]bool

	// latest is the newest record for each pane's current run.
	latest map[PaneKey]agentlifecycle.Report
}

func newIndex() *index {
	return &index{
		highSeq:      map[agentlifecycle.Key]uint64{},
		latestForKey: map[agentlifecycle.Key]agentlifecycle.Report{},
		currentRun:   map[PaneKey]string{},
		seenRuns:     map[PaneKey]map[string]bool{},
		latest:       map[PaneKey]agentlifecycle.Report{},
	}
}

// admit decides whether a report may be stored, without storing it.
//
// It returns the acceptance to report on success. A duplicate is accepted with
// [agentlifecycle.AcceptedDuplicate] and must not be appended by the caller.
func (ix *index) admit(r agentlifecycle.Report) (agentlifecycle.Acceptance, bool, error) {
	key := r.Key()
	pane := PaneKeyFor(r)

	// Run rotation first. A report from a run the pane has already left is
	// refused outright: storing it would leave a record that a later fold, or a
	// reader with a different retention window, could mistake for current.
	if cur, ok := ix.currentRun[pane]; ok && cur != r.Identity.RunID {
		if ix.seenRuns[pane][r.Identity.RunID] {
			return "", false, fmt.Errorf("%w: pane %s is on run %s, report claims %s",
				ErrPriorRun, pane.PaneID, cur, r.Identity.RunID)
		}
		// An unseen run is a genuinely new run, which reanchors sequencing.
	}

	if high, ok := ix.highSeq[key]; ok {
		switch {
		case r.Sequence < high:
			return "", false, fmt.Errorf("%w: sequence %d is behind %d for source %s",
				ErrStaleSequence, r.Sequence, high, r.Source)
		case r.Sequence == high:
			// Replay is idempotent, but only for a genuinely identical record.
			// A source that reuses a sequence to assert something different is
			// not replaying, it is contradicting itself, and accepting the
			// second one would make ordering meaningless.
			if prev, ok := ix.latestForKey[key]; ok && sameFact(prev, r) {
				return agentlifecycle.AcceptedDuplicate, false, nil
			}
			return "", false, fmt.Errorf("%w: sequence %d reused with different content for source %s",
				ErrStaleSequence, r.Sequence, r.Source)
		}
	}
	return agentlifecycle.AcceptedAuthoritative, true, nil
}

// nextSequence is the sequence a report should take to be the next one in its
// key. It is one past the highest accepted, or 1 when the key is new.
//
// It must only ever be called while holding whatever lock guards the fold, and
// with the fold freshly reloaded, or it answers about a world that has already
// moved on.
func (ix *index) nextSequence(r agentlifecycle.Report) uint64 {
	pane := PaneKeyFor(r)
	// A new run reanchors sequencing, so the previous run's high-water mark is
	// not this run's floor. Without this, a relaunched agent in the same pane
	// would start numbering above the dead run's last report, which is harmless
	// but makes the sequence stop meaning "how far into this run".
	if cur, ok := ix.currentRun[pane]; ok && cur != r.Identity.RunID {
		if !ix.seenRuns[pane][r.Identity.RunID] {
			return 1
		}
	}
	if high, ok := ix.highSeq[r.Key()]; ok {
		return high + 1
	}
	return 1
}

// commit records an admitted report in the fold.
func (ix *index) commit(r agentlifecycle.Report) {
	key := r.Key()
	pane := PaneKeyFor(r)

	ix.records = append(ix.records, r)
	ix.highSeq[key] = r.Sequence
	ix.latestForKey[key] = r

	if ix.seenRuns[pane] == nil {
		ix.seenRuns[pane] = map[string]bool{}
	}
	ix.seenRuns[pane][r.Identity.RunID] = true
	ix.currentRun[pane] = r.Identity.RunID
	ix.latest[pane] = r
}

// rebuild refolds the maps from records, which is what a load or a compaction
// ends with.
func (ix *index) rebuild(records []agentlifecycle.Report) {
	ix.records = nil
	ix.highSeq = map[agentlifecycle.Key]uint64{}
	ix.latestForKey = map[agentlifecycle.Key]agentlifecycle.Report{}
	ix.currentRun = map[PaneKey]string{}
	ix.seenRuns = map[PaneKey]map[string]bool{}
	ix.latest = map[PaneKey]agentlifecycle.Report{}
	for _, r := range records {
		ix.commit(r)
	}
}

func (ix *index) latestFor(k PaneKey) (agentlifecycle.Report, bool) {
	r, ok := ix.latest[k]
	return r, ok
}

func (ix *index) listFor(k PaneKey) []agentlifecycle.Report {
	var out []agentlifecycle.Report
	for _, r := range ix.records {
		if PaneKeyFor(r) == k {
			out = append(out, r)
		}
	}
	return out
}

// sameFact reports whether two records at the same sequence assert the same
// thing. Report.ID and Detail are excluded: a hook that retries generates a new
// ID for the same event, and treating that as a contradiction would turn an
// ordinary retry into a hard error.
func sameFact(a, b agentlifecycle.Report) bool {
	return a.Kind == b.Kind &&
		a.State == b.State &&
		a.Outcome == b.Outcome &&
		a.Reason == b.Reason &&
		a.Identity == b.Identity &&
		a.Source == b.Source &&
		a.SourceVersion == b.SourceVersion
}

// retain applies the retention bounds and returns the records that survive, in
// append order.
//
// The order of trimming matters. Per-key history is applied first so that every
// run keeps a usable tail, then the oldest runs are dropped whole, then the
// global ceiling truncates from the front. Doing the global cut first would let
// one noisy pane evict every other pane's latest report, which is the one
// record that must never be lost.
func retain(records []agentlifecycle.Report) []agentlifecycle.Report {
	if len(records) == 0 {
		return nil
	}

	keep := make(map[int]bool, len(records))

	// 1. Last HistoryPerKey records per (server, pane, source, run).
	perKey := map[agentlifecycle.Key][]int{}
	for i, r := range records {
		k := r.Key()
		perKey[k] = append(perKey[k], i)
	}
	for _, idxs := range perKey {
		start := 0
		if len(idxs) > HistoryPerKey {
			start = len(idxs) - HistoryPerKey
		}
		for _, i := range idxs[start:] {
			keep[i] = true
		}
	}

	// 2. Only the MaxRunsPerPane most recently active runs per pane.
	lastSeen := map[PaneKey]map[string]int{}
	for i, r := range records {
		p := PaneKeyFor(r)
		if lastSeen[p] == nil {
			lastSeen[p] = map[string]int{}
		}
		lastSeen[p][r.Identity.RunID] = i
	}
	liveRuns := map[PaneKey]map[string]bool{}
	for p, runs := range lastSeen {
		type runAt struct {
			run string
			at  int
		}
		var all []runAt
		for run, at := range runs {
			all = append(all, runAt{run, at})
		}
		sort.Slice(all, func(a, b int) bool { return all[a].at > all[b].at })
		if len(all) > MaxRunsPerPane {
			all = all[:MaxRunsPerPane]
		}
		liveRuns[p] = map[string]bool{}
		for _, ra := range all {
			liveRuns[p][ra.run] = true
		}
	}

	var out []agentlifecycle.Report
	for i, r := range records {
		if !keep[i] {
			continue
		}
		if !liveRuns[PaneKeyFor(r)][r.Identity.RunID] {
			continue
		}
		out = append(out, r)
	}

	// 3. Global ceiling, oldest first.
	if len(out) > MaxRecords {
		out = out[len(out)-MaxRecords:]
	}
	return out
}

// prepare sanitizes and validates a report, returning the record to store.
//
// Sanitizing before validating is deliberate: Detail is the one field a caller
// is allowed to hand over raw, and Validate then refuses anything that is still
// not clean. That ordering means a caller cannot accidentally persist an escape
// sequence, and also cannot deliberately bypass the bound by pre-truncating.
func prepare(r agentlifecycle.Report, now time.Time) (agentlifecycle.Report, error) {
	r.Detail = agentlifecycle.SanitizeDetail(r.Detail)
	if err := agentlifecycle.Validate(r, now); err != nil {
		return agentlifecycle.Report{}, err
	}
	return r, nil
}

// Memory is an in-process [Store].
//
// It is not merely a test double: it is the store a caller uses when it wants
// lifecycle sequencing and run rotation without a file — and it is what proves
// those rules live in the shared fold rather than in the JSONL plumbing.
type Memory struct {
	ix  *index
	now func() time.Time
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory { return &Memory{ix: newIndex(), now: time.Now} }

// SetClock overrides the clock used for skew validation. Tests use it; nothing
// else should.
func (m *Memory) SetClock(now func() time.Time) { m.now = now }

func (m *Memory) Append(r agentlifecycle.Report) (agentlifecycle.Acceptance, error) {
	rec, err := prepare(r, m.now())
	if err != nil {
		return "", err
	}
	acc, store, err := m.ix.admit(rec)
	if err != nil {
		return "", err
	}
	if store {
		m.ix.commit(rec)
	}
	return acc, nil
}

func (m *Memory) AppendNext(r agentlifecycle.Report) (agentlifecycle.Report, agentlifecycle.Acceptance, error) {
	rec, err := prepare(r, m.now())
	if err != nil {
		return agentlifecycle.Report{}, "", err
	}
	rec.Sequence = m.ix.nextSequence(rec)
	acc, store, err := m.ix.admit(rec)
	if err != nil {
		return agentlifecycle.Report{}, "", err
	}
	if store {
		m.ix.commit(rec)
	}
	return rec, acc, nil
}

func (m *Memory) Release(r agentlifecycle.Report) (agentlifecycle.Acceptance, error) {
	if r.Kind != agentlifecycle.KindRelease {
		return "", fmt.Errorf("%w: release requires kind %q, got %q",
			agentlifecycle.ErrValidation, agentlifecycle.KindRelease, r.Kind)
	}
	return m.Append(r)
}

func (m *Memory) Latest(k PaneKey) (agentlifecycle.Report, bool) { return m.ix.latestFor(k) }

func (m *Memory) List(k PaneKey) []agentlifecycle.Report { return m.ix.listFor(k) }

func (m *Memory) Compact() error {
	m.ix.rebuild(retain(m.ix.records))
	return nil
}
