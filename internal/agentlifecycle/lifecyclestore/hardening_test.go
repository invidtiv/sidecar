package lifecyclestore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/agentlifecycle"
)

// Phase E step 2: the operational properties this log is held to when it is
// being written by short-lived hook processes and read by a polling TUI at the
// same time, on a machine that sometimes loses power, runs a wandering clock,
// or produces a burst of events faster than anything reads them.
//
// Ordering, run rotation, and recycled-pane rules are covered by
// store_test.go and assign_test.go and are deliberately not repeated here.

// paneAt builds a report for a named pane, so a test can spread records across
// several keys without every case restating the whole record.
func paneAt(pane string, seq uint64, state agentactivity.State) agentlifecycle.Report {
	r := report(seq, state)
	r.ID = fmt.Sprintf("rep-%s-%d", strings.TrimPrefix(pane, "%"), seq)
	r.Identity.PaneID = pane
	return r
}

// TestATruncatedFinalLineDoesNotSwallowTheNextReport is the partial-write case,
// which is a different failure from a malformed line and has a worse
// consequence.
//
// A machine that loses power mid-append leaves a final line with no terminating
// newline. An appender that does not notice writes its record straight onto
// that fragment, so one unparseable line now holds two records: the crash costs
// the report it interrupted *and* the next healthy one, and Append returns
// success for the record it just destroyed. Every later report is then measured
// against a fold that has silently rewound.
//
// Against the unrepaired implementation this test observes exactly that: the
// pane's latest report goes backwards to sequence 1.
func TestATruncatedFinalLineDoesNotSwallowTheNextReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)

	s, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	s.SetClock(func() time.Time { return testNow })
	mustAppend(t, s, report(1, agentactivity.StateWorking))
	mustAppend(t, s, report(2, agentactivity.StateBlocked))

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Cut the tail off the last record, newline included. This is what a
	// half-flushed write leaves on disk, not what a hand-edit leaves.
	if err := os.WriteFile(path, data[:len(data)-30], 0o644); err != nil {
		t.Fatal(err)
	}

	next, err := OpenPath(path)
	if err != nil {
		t.Fatalf("a truncated log must still open: %v", err)
	}
	next.SetClock(func() time.Time { return testNow })
	if _, err := next.Append(report(3, agentactivity.StateIdle)); err != nil {
		t.Fatalf("append after truncation: %v", err)
	}

	// The record written after the damage must be readable by a process that
	// knows nothing about any of this.
	reopened, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	reopened.SetClock(func() time.Time { return testNow })
	got, ok := reopened.Latest(paneOf(report(3, agentactivity.StateIdle)))
	if !ok {
		t.Fatal("the log lost every record to one truncated line")
	}
	if got.Sequence != 3 || got.State != agentactivity.StateIdle {
		t.Fatalf("the report written after the truncation was swallowed: latest = seq %d %q",
			got.Sequence, got.State)
	}

	// And the damage must not spread: the log ends properly framed again, so
	// the report after that one is ordinary.
	if _, err := reopened.Append(report(4, agentactivity.StateWorking)); err != nil {
		t.Fatalf("append after the repair: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if unterminated, err := endsWithoutNewline(f); err != nil || unterminated {
		t.Fatalf("log still ends mid-line: unterminated=%v err=%v", unterminated, err)
	}
}

// TestAReaderNeverSeesATornLogWhileWritersAppend covers the half of the
// concurrency contract the existing writer tests do not: Sidecar's polling
// surfaces read this file continuously through the lock-free ReadAll path while
// hook processes append to it.
//
// The property is that a reader either sees a record or does not — never half
// of one, and never a fold that goes backwards. A writer that built its line in
// several writes, or that appended without O_APPEND, would fail this.
func TestAReaderNeverSeesATornLogWhileWritersAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)

	// Per key, so that retention keeps every record and a missing one at the
	// end is unambiguously a lost write rather than a trim.
	const writers = 6
	const each = HistoryPerKey

	var wg sync.WaitGroup
	stop := make(chan struct{})

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			// A handle per goroutine, as separate hook processes have.
			s, err := OpenPath(path)
			if err != nil {
				return
			}
			s.SetClock(func() time.Time { return testNow })
			pane := fmt.Sprintf("%%%d", w)
			for i := 0; i < each; i++ {
				r := paneAt(pane, 0, agentactivity.StateWorking)
				if _, _, err := s.AppendNext(r); err != nil {
					return
				}
			}
		}(w)
	}

	var reads int64
	var readWG sync.WaitGroup
	for r := 0; r < 3; r++ {
		readWG.Add(1)
		go func() {
			defer readWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				got, err := ReadAll(path)
				if err != nil {
					t.Errorf("a reader failed while writers appended: %v", err)
					return
				}
				atomic.AddInt64(&reads, 1)
				for _, rec := range got {
					// Every record a reader sees must be complete and valid.
					// A torn line would not unmarshal at all, and a line built
					// from two half-writes would unmarshal into something that
					// fails this.
					if err := agentlifecycle.Validate(rec, testNow); err != nil {
						t.Errorf("a reader saw a record that is not a valid report: %v", err)
						return
					}
				}
			}
		}()
	}

	wg.Wait()
	close(stop)
	readWG.Wait()

	if atomic.LoadInt64(&reads) == 0 {
		t.Fatal("no read completed, so this proved nothing")
	}
	final, err := ReadAll(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(final) != writers*each {
		t.Fatalf("log holds %d records, want %d", len(final), writers*each)
	}
}

// TestAContendedLockFailsWithinItsTimeoutRatherThanHanging pins the one
// property that makes the lock safe to take from a provider hook: it gives up.
//
// A hook runs in the agent's critical path and the plan's rule is that a
// reporting failure must never delay the provider. An unbounded wait would
// break that far more visibly than a dropped report does, so the bound is the
// behavior, not an implementation detail.
func TestAContendedLockFailsWithinItsTimeoutRatherThanHanging(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)

	s, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	s.SetClock(func() time.Time { return testNow })
	mustAppend(t, s, report(1, agentactivity.StateWorking))
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// Hold the lock the way another process would. flock is per open file
	// description, so a second descriptor genuinely contends even in-process.
	held, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	release := func() {
		_ = syscall.Flock(int(held.Fd()), syscall.LOCK_UN)
		_ = held.Close()
	}
	defer release()

	other, err := OpenPath(path)
	if err != nil {
		// Open itself takes the lock, so contention may surface here. Either
		// way the requirement is the same: it returned rather than hung.
		if !strings.Contains(err.Error(), "timeout") {
			t.Fatalf("open under contention failed for the wrong reason: %v", err)
		}
		return
	}
	other.SetClock(func() time.Time { return testNow })

	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := other.Append(report(2, agentactivity.StateBlocked))
		done <- err
	}()

	select {
	case err := <-done:
		elapsed := time.Since(start)
		if err == nil {
			t.Fatal("an append succeeded while another writer held the lock")
		}
		if !strings.Contains(err.Error(), "timeout") {
			t.Fatalf("err = %v, want a lock timeout", err)
		}
		if elapsed > 4*lockTimeout {
			t.Fatalf("the append waited %v for a %v timeout", elapsed, lockTimeout)
		}
	case <-time.After(10 * lockTimeout):
		t.Fatalf("an append blocked indefinitely on a held lock; the %v bound did not apply", lockTimeout)
	}

	// A refused append must leave the log exactly as it found it. A writer that
	// opened the file before waiting for the lock would fail this.
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("a timed-out append modified the log:\nbefore %q\nafter  %q", before, after)
	}
}

// TestReadAllIsNeverBlockedByAWriterHoldingTheLock is why the lock is a sidecar
// file rather than the log itself. The polling surfaces read through ReadAll on
// every refresh; if that contended with hook writers, a busy agent would stall
// the surface watching it, and `cat` on the log would block too.
func TestReadAllIsNeverBlockedByAWriterHoldingTheLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)

	s, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	s.SetClock(func() time.Time { return testNow })
	mustAppend(t, s, report(1, agentactivity.StateWorking))

	held, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = syscall.Flock(int(held.Fd()), syscall.LOCK_UN)
		_ = held.Close()
	}()

	done := make(chan int, 1)
	go func() {
		got, err := ReadAll(path)
		if err != nil {
			done <- -1
			return
		}
		done <- len(got)
	}()

	select {
	case n := <-done:
		if n != 1 {
			t.Fatalf("ReadAll returned %d records under a held lock", n)
		}
	case <-time.After(lockTimeout / 4):
		t.Fatal("ReadAll waited on the writer's lock; inspection is supposed to be lock-free")
	}
}

// TestAReaderDuringCompactionAlwaysSeesACompleteLog is the property the
// temp-file-and-rename exists for. Compaction replaces the whole file, and a
// reader that caught it mid-rewrite would see a truncated log and conclude the
// pane has no lifecycle evidence — silently returning a healthy agent to screen
// fallback for as long as the rewrite took.
func TestAReaderDuringCompactionAlwaysSeesACompleteLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)

	s, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	s.SetClock(func() time.Time { return testNow })

	// Spread across panes so every record survives retention: compaction here
	// is a pure rewrite, which makes "fewer records than before" unambiguously
	// a torn read rather than a trim.
	const panes, each = 5, 4
	for p := 0; p < panes; p++ {
		for i := uint64(1); i <= each; i++ {
			mustAppend(t, s, paneAt(fmt.Sprintf("%%%d", p), i, agentactivity.StateWorking))
		}
	}
	const want = panes * each
	if n := len(mustReadAll(t, path)); n != want {
		t.Fatalf("setup wrote %d records, want %d", n, want)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 40; i++ {
			if err := s.Compact(); err != nil {
				t.Errorf("compact: %v", err)
				return
			}
		}
		close(stop)
	}()

	var reads int
	for {
		select {
		case <-stop:
			wg.Wait()
			if reads == 0 {
				t.Fatal("no read completed during compaction, so this proved nothing")
			}
			return
		default:
		}
		got := mustReadAll(t, path)
		reads++
		if len(got) != want {
			t.Fatalf("a reader saw %d records mid-compaction, want %d: the rewrite was not atomic",
				len(got), want)
		}
	}
}

func mustReadAll(t *testing.T, path string) []agentlifecycle.Report {
	t.Helper()
	got, err := ReadAll(path)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	return got
}

// TestABurstOfHookProcessesKeepsTheLogBounded is the event-burst case. A
// provider that fires a hook per tool call can produce hundreds of reports in a
// turn, each from its own short-lived process, and nothing outside this store
// bounds the file.
//
// The bound comes from Open's compaction hysteresis, so the burst has to be
// driven through fresh handles to exercise it — which is also exactly what a
// per-event hook provider does.
func TestABurstOfHookProcessesKeepsTheLogBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)

	const burst = 300
	for i := 0; i < burst; i++ {
		s, err := OpenPath(path)
		if err != nil {
			t.Fatalf("burst report %d: %v", i, err)
		}
		s.SetClock(func() time.Time { return testNow })
		r := report(0, agentactivity.StateWorking)
		r.ID = fmt.Sprintf("burst-%d", i)
		got, _, err := s.AppendNext(r)
		if err != nil {
			t.Fatalf("burst report %d: %v", i, err)
		}
		if got.Sequence != uint64(i+1) {
			t.Fatalf("burst report %d took sequence %d", i, got.Sequence)
		}
	}

	// One key, so retention keeps HistoryPerKey records and the hysteresis
	// tolerates roughly twice that before rewriting again.
	lines := len(mustReadAll(t, path))
	if ceiling := HistoryPerKey*2 + 8; lines > ceiling {
		t.Fatalf("a %d-report burst left %d records on disk, above the %d the bounds allow",
			burst, lines, ceiling)
	}

	// The burst must not have cost the one record that matters.
	final, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	final.SetClock(func() time.Time { return testNow })
	got, ok := final.Latest(paneOf(report(1, agentactivity.StateWorking)))
	if !ok || got.Sequence != burst {
		t.Fatalf("latest after the burst = seq %d ok=%v, want %d", got.Sequence, ok, burst)
	}
	// And ordering must still be anchored where the burst left it, so a
	// straggler cannot rewind the run.
	if _, err := final.Append(report(uint64(burst/2), agentactivity.StateIdle)); !errors.Is(err, ErrStaleSequence) {
		t.Fatalf("err = %v, want ErrStaleSequence after a compacting burst", err)
	}
}

// TestTheStoresClockGovernsSkewAndNeverOrdering covers the two clock hazards
// separately, because they have opposite correct answers.
//
// A timestamp far from the receiver's clock is refused — that is the bound
// against a replayed record sitting in the future and reading as fresh forever.
// A timestamp that merely moved backwards *within* the bound is not refused and
// must not reorder anything: ordering is by sequence, and a receiver whose clock
// stepped back must not lose the reports written across the step.
func TestTheStoresClockGovernsSkewAndNeverOrdering(t *testing.T) {
	t.Run("a report beyond the skew bound is refused by the store's own clock", func(t *testing.T) {
		for name, s := range stores(t) {
			t.Run(name, func(t *testing.T) {
				future := report(1, agentactivity.StateWorking)
				future.ObservedAt = testNow.Add(agentlifecycle.MaxClockSkew + time.Minute)
				if _, err := s.Append(future); !errors.Is(err, agentlifecycle.ErrValidation) {
					t.Fatalf("future report: err = %v, want ErrValidation", err)
				}
				past := report(1, agentactivity.StateWorking)
				past.ObservedAt = testNow.Add(-agentlifecycle.MaxClockSkew - time.Minute)
				if _, err := s.Append(past); !errors.Is(err, agentlifecycle.ErrValidation) {
					t.Fatalf("backdated report: err = %v, want ErrValidation", err)
				}
				if _, ok := s.Latest(paneOf(future)); ok {
					t.Fatal("a skewed record reached the store")
				}
			})
		}
	})

	t.Run("a clock that steps backwards within the bound loses nothing", func(t *testing.T) {
		for name, s := range stores(t) {
			t.Run(name, func(t *testing.T) {
				first := report(1, agentactivity.StateWorking)
				mustAppend(t, s, first)

				// The receiver's clock steps back; the next hook stamps an
				// earlier time than the record already stored.
				second := report(2, agentactivity.StateBlocked)
				second.ObservedAt = testNow.Add(-2 * time.Minute)
				mustAppend(t, s, second)

				third := report(3, agentactivity.StateIdle)
				mustAppend(t, s, third)

				got, ok := s.Latest(paneOf(third))
				if !ok || got.Sequence != 3 {
					t.Fatalf("latest = seq %d ok=%v, want 3: ordering followed the clock, not the sequence",
						got.Sequence, ok)
				}
				list := s.List(paneOf(third))
				if len(list) != 3 {
					t.Fatalf("a backwards clock cost %d of 3 records", 3-len(list))
				}
				for i, r := range list {
					if r.Sequence != uint64(i+1) {
						t.Fatalf("record %d is sequence %d; the fold is not in append order", i, r.Sequence)
					}
				}
			})
		}
	})

	t.Run("the skew bound is smaller than the shortest freshness window", func(t *testing.T) {
		// This is what stops a report from the future being fresh for longer
		// than a report from the present. Freshness clamps a negative age to
		// zero, so a future-stamped record's extra life is exactly its skew;
		// the bound has to stay well under the window it would extend.
		if window := agentlifecycle.DefaultFreshnessPolicy().Working; agentlifecycle.MaxClockSkew >= window {
			t.Fatalf("MaxClockSkew %v is not smaller than the working freshness window %v, so a future-stamped report can outlive a present one",
				agentlifecycle.MaxClockSkew, window)
		}
	})
}
