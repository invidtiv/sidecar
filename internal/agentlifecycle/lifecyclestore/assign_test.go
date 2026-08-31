package lifecyclestore

import (
	"fmt"

	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/agentlifecycle"
)

// AppendNext exists so a provider that runs each hook as its own process can
// report at all. These pin the properties that makes true.

// TestAssignedSequencesAreDenseAndOrdered is the basic contract, run against
// both implementations so the memory store cannot quietly disagree.
func TestAssignedSequencesAreDenseAndOrdered(t *testing.T) {
	for name, s := range stores(t) {
		t.Run(name, func(t *testing.T) {
			for want := uint64(1); want <= 5; want++ {
				r := report(0, agentactivity.StateWorking)
				r.ID = fmt.Sprintf("rep-%d", want)
				got, acc, err := s.AppendNext(r)
				if err != nil {
					t.Fatalf("append %d: %v", want, err)
				}
				if acc != agentlifecycle.AcceptedAuthoritative {
					t.Fatalf("append %d accepted as %s", want, acc)
				}
				if got.Sequence != want {
					t.Fatalf("assigned sequence %d, want %d", got.Sequence, want)
				}
			}
		})
	}
}

// TestConcurrentReportersNeverCollideOnASequence is the whole reason this
// exists. Codex and Claude Code run each hook as an independent short-lived
// process; if each one chose its own number they would collide, and the store
// would correctly reject the loser -- which means silently dropping a lifecycle
// report. The assignment therefore has to happen inside the lock that makes
// read-then-write atomic, and this is the test that would fail if it ever moved
// back outside it.
//
// The two writers here share one JSONL file through separate store handles,
// which is the same situation two hook processes are in.
func TestConcurrentReportersNeverCollideOnASequence(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)

	const writers = 4
	const each = 6

	var wg sync.WaitGroup
	errs := make(chan error, writers*each)
	seqs := make(chan uint64, writers*each)

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			// A separate handle per goroutine: sharing one would be testing the
			// in-process mutex rather than the file lock two processes rely on.
			s, err := OpenPath(path)
			if err != nil {
				errs <- err
				return
			}
			s.SetClock(func() time.Time { return testNow })
			for i := 0; i < each; i++ {
				r := report(0, agentactivity.StateWorking)
				r.ID = fmt.Sprintf("rep-%d-%d", w, i)
				got, _, err := s.AppendNext(r)
				if err != nil {
					errs <- err
					return
				}
				seqs <- got.Sequence
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	close(seqs)

	for err := range errs {
		t.Fatalf("a concurrent reporter failed, which is the drop this prevents: %v", err)
	}

	seen := map[uint64]bool{}
	for s := range seqs {
		if seen[s] {
			t.Fatalf("sequence %d was handed out twice; one of those reports would have been rejected", s)
		}
		seen[s] = true
	}
	if len(seen) != writers*each {
		t.Fatalf("got %d distinct sequences, want %d", len(seen), writers*each)
	}
	// Dense from 1: nothing was skipped either, so the sequence still means
	// "how far into this run" rather than merely "bigger than last time".
	for want := uint64(1); want <= writers*each; want++ {
		if !seen[want] {
			t.Fatalf("sequence %d was never assigned", want)
		}
	}
}

// TestANewRunReanchorsAssignedSequencing keeps the assigned number meaningful.
// A relaunched agent in the same pane is a new run, and numbering it above the
// dead run's high-water mark would be harmless but would stop the sequence
// meaning anything about this run.
func TestANewRunReanchorsAssignedSequencing(t *testing.T) {
	for name, s := range stores(t) {
		t.Run(name, func(t *testing.T) {
			for i := 0; i < 3; i++ {
				r := report(0, agentactivity.StateWorking)
				r.ID = fmt.Sprintf("first-%d", i)
				if _, _, err := s.AppendNext(r); err != nil {
					t.Fatal(err)
				}
			}
			next := report(0, agentactivity.StateWorking)
			next.ID = "second-run"
			next.Identity.RunID = "run-2"
			next.Identity.ProcessGeneration = "gen-2"
			got, _, err := s.AppendNext(next)
			if err != nil {
				t.Fatal(err)
			}
			if got.Sequence != 1 {
				t.Fatalf("a new run started at sequence %d, want 1", got.Sequence)
			}
		})
	}
}

// TestAssignmentStillRefusesAReportFromARunThePaneHasLeft proves assignment did
// not become a way around admission. A late hook from a dead run must not be
// handed a fresh sequence and let in through the side door.
func TestAssignmentStillRefusesAReportFromARunThePaneHasLeft(t *testing.T) {
	for name, s := range stores(t) {
		t.Run(name, func(t *testing.T) {
			first := report(0, agentactivity.StateWorking)
			if _, _, err := s.AppendNext(first); err != nil {
				t.Fatal(err)
			}
			second := report(0, agentactivity.StateWorking)
			second.ID = "run-2"
			second.Identity.RunID = "run-2"
			second.Identity.ProcessGeneration = "gen-2"
			if _, _, err := s.AppendNext(second); err != nil {
				t.Fatal(err)
			}
			// Now the pane is on run-2. A straggler from run-1 arrives.
			late := report(0, agentactivity.StateIdle)
			late.ID = "late"
			if _, _, err := s.AppendNext(late); err == nil {
				t.Fatal("a report from a run the pane has left was accepted because its sequence was assigned")
			}
		})
	}
}
