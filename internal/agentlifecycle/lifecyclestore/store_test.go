package lifecyclestore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/agentlifecycle"
)

var testNow = time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)

// report builds a valid state report. Tests mutate the result rather than
// passing a dozen arguments, so each case names only what it is about.
func report(seq uint64, state agentactivity.State) agentlifecycle.Report {
	return agentlifecycle.Report{
		SchemaVersion: agentlifecycle.SchemaVersion,
		ID:            fmt.Sprintf("rep-%d", seq),
		Kind:          agentlifecycle.KindState,
		Identity: agentlifecycle.Identity{
			Host:              "host-a",
			ServerIncarnation: "inc-1",
			PaneID:            "%7",
			Provider:          "opencode",
			RunID:             "run-1",
			ProcessGeneration: "gen-1",
		},
		Source:        "sidecar.opencode.plugin",
		SourceVersion: "1",
		Sequence:      seq,
		State:         state,
		ObservedAt:    testNow,
		Reason:        agentlifecycle.ReasonTurnStart,
	}
}

func paneOf(r agentlifecycle.Report) PaneKey { return PaneKeyFor(r) }

// stores returns both implementations so every contract case runs against each.
// A memory store that accepts what the JSONL store rejects would make every
// test written against it a lie.
func stores(t *testing.T) map[string]Store {
	t.Helper()
	mem := NewMemory()
	mem.SetClock(func() time.Time { return testNow })

	js, err := OpenPath(filepath.Join(t.TempDir(), FileName))
	if err != nil {
		t.Fatal(err)
	}
	js.SetClock(func() time.Time { return testNow })

	return map[string]Store{"memory": mem, "jsonl": js}
}

func TestStoreContract(t *testing.T) {
	t.Run("append then latest", func(t *testing.T) {
		for name, s := range stores(t) {
			t.Run(name, func(t *testing.T) {
				r := report(1, agentactivity.StateWorking)
				acc, err := s.Append(r)
				if err != nil {
					t.Fatal(err)
				}
				if acc != agentlifecycle.AcceptedAuthoritative {
					t.Fatalf("acceptance = %q", acc)
				}
				got, ok := s.Latest(paneOf(r))
				if !ok || got.Sequence != 1 || got.State != agentactivity.StateWorking {
					t.Fatalf("latest = %+v ok=%v", got, ok)
				}
			})
		}
	})

	t.Run("sequence must advance", func(t *testing.T) {
		for name, s := range stores(t) {
			t.Run(name, func(t *testing.T) {
				mustAppend(t, s, report(5, agentactivity.StateWorking))
				_, err := s.Append(report(4, agentactivity.StateIdle))
				if !errors.Is(err, ErrStaleSequence) {
					t.Fatalf("err = %v, want ErrStaleSequence", err)
				}
				// The rejected record must not have displaced the good one.
				got, _ := s.Latest(paneOf(report(5, agentactivity.StateWorking)))
				if got.Sequence != 5 || got.State != agentactivity.StateWorking {
					t.Fatalf("stale report changed latest: %+v", got)
				}
			})
		}
	})

	t.Run("replay of the same fact is idempotent", func(t *testing.T) {
		for name, s := range stores(t) {
			t.Run(name, func(t *testing.T) {
				r := report(3, agentactivity.StateBlocked)
				mustAppend(t, s, r)

				// A retrying hook regenerates the ID; that must not read as a
				// different fact.
				again := r
				again.ID = "rep-3-retry"
				acc, err := s.Append(again)
				if err != nil {
					t.Fatal(err)
				}
				if acc != agentlifecycle.AcceptedDuplicate {
					t.Fatalf("acceptance = %q, want duplicate", acc)
				}
				if n := len(s.List(paneOf(r))); n != 1 {
					t.Fatalf("duplicate was stored: %d records", n)
				}
			})
		}
	})

	t.Run("a reused sequence asserting something else is refused", func(t *testing.T) {
		for name, s := range stores(t) {
			t.Run(name, func(t *testing.T) {
				mustAppend(t, s, report(3, agentactivity.StateBlocked))
				_, err := s.Append(report(3, agentactivity.StateIdle))
				if !errors.Is(err, ErrStaleSequence) {
					t.Fatalf("err = %v, want ErrStaleSequence", err)
				}
			})
		}
	})

	t.Run("a new run reanchors the sequence", func(t *testing.T) {
		for name, s := range stores(t) {
			t.Run(name, func(t *testing.T) {
				mustAppend(t, s, report(100, agentactivity.StateWorking))

				next := report(1, agentactivity.StateWorking)
				next.Identity.RunID = "run-2"
				next.Identity.ProcessGeneration = "gen-2"
				// Sequence 1 is far below run-1's 100 and must still be accepted:
				// sequencing is per run, not per pane.
				mustAppend(t, s, next)

				got, _ := s.Latest(paneOf(next))
				if got.Identity.RunID != "run-2" {
					t.Fatalf("latest run = %q, want run-2", got.Identity.RunID)
				}
			})
		}
	})

	t.Run("a prior run cannot replay after the pane moves on", func(t *testing.T) {
		for name, s := range stores(t) {
			t.Run(name, func(t *testing.T) {
				mustAppend(t, s, report(1, agentactivity.StateWorking))

				second := report(1, agentactivity.StateWorking)
				second.Identity.RunID = "run-2"
				second.Identity.ProcessGeneration = "gen-2"
				mustAppend(t, s, second)

				// run-1 speaks again, late. This is the restart-replay case: if
				// it were stored, a fold would see it as the newest word on the
				// pane and hand a dead run authority.
				late := report(2, agentactivity.StateIdle)
				_, err := s.Append(late)
				if !errors.Is(err, ErrPriorRun) {
					t.Fatalf("err = %v, want ErrPriorRun", err)
				}
				got, _ := s.Latest(paneOf(late))
				if got.Identity.RunID != "run-2" {
					t.Fatalf("late prior-run report took authority: %+v", got)
				}
			})
		}
	})

	t.Run("release is recorded and must carry its kind", func(t *testing.T) {
		for name, s := range stores(t) {
			t.Run(name, func(t *testing.T) {
				mustAppend(t, s, report(1, agentactivity.StateWorking))

				rel := report(2, "")
				rel.Kind = agentlifecycle.KindRelease
				rel.Reason = agentlifecycle.ReasonIntegrationRemoved
				if _, err := s.Release(rel); err != nil {
					t.Fatal(err)
				}
				got, _ := s.Latest(paneOf(rel))
				if got.Kind != agentlifecycle.KindRelease {
					t.Fatalf("latest kind = %q", got.Kind)
				}

				notRelease := report(3, agentactivity.StateIdle)
				if _, err := s.Release(notRelease); !errors.Is(err, agentlifecycle.ErrValidation) {
					t.Fatalf("err = %v, want ErrValidation", err)
				}
			})
		}
	})

	t.Run("a recycled pane id on a new server incarnation inherits nothing", func(t *testing.T) {
		for name, s := range stores(t) {
			t.Run(name, func(t *testing.T) {
				old := report(9, agentactivity.StateBlocked)
				mustAppend(t, s, old)

				// Same %pane, new tmux server. Everything is namespaced by
				// incarnation precisely so this cannot inherit the blocked lane.
				fresh := report(1, agentactivity.StateWorking)
				fresh.Identity.ServerIncarnation = "inc-2"
				fresh.Identity.RunID = "run-9"
				mustAppend(t, s, fresh)

				if got, _ := s.Latest(paneOf(old)); got.State != agentactivity.StateBlocked {
					t.Fatalf("old incarnation lost its record: %+v", got)
				}
				got, ok := s.Latest(paneOf(fresh))
				if !ok || got.State != agentactivity.StateWorking {
					t.Fatalf("new incarnation = %+v ok=%v", got, ok)
				}
			})
		}
	})

	t.Run("invalid records never reach the store", func(t *testing.T) {
		for name, s := range stores(t) {
			t.Run(name, func(t *testing.T) {
				bad := report(1, agentactivity.StateWorking)
				bad.State = agentactivity.StateUnknown
				if _, err := s.Append(bad); !errors.Is(err, agentlifecycle.ErrValidation) {
					t.Fatalf("err = %v, want ErrValidation", err)
				}
				if _, ok := s.Latest(paneOf(bad)); ok {
					t.Fatal("an invalid record was stored")
				}
			})
		}
	})

	t.Run("detail is sanitized on the way in", func(t *testing.T) {
		for name, s := range stores(t) {
			t.Run(name, func(t *testing.T) {
				r := report(1, agentactivity.StateWorking)
				r.Detail = "line one\nline two\x1b[31mred\x1b[0m"
				mustAppend(t, s, r)
				got, _ := s.Latest(paneOf(r))
				if strings.ContainsAny(got.Detail, "\n\x1b") {
					t.Fatalf("detail kept control characters: %q", got.Detail)
				}
				if got.Detail != "line one line two[31mred[0m" {
					t.Fatalf("detail = %q", got.Detail)
				}
			})
		}
	})

	t.Run("compaction keeps the latest and bounds history", func(t *testing.T) {
		for name, s := range stores(t) {
			t.Run(name, func(t *testing.T) {
				for i := uint64(1); i <= 40; i++ {
					mustAppend(t, s, report(i, agentactivity.StateWorking))
				}
				if err := s.Compact(); err != nil {
					t.Fatal(err)
				}
				list := s.List(paneOf(report(1, agentactivity.StateWorking)))
				if len(list) != HistoryPerKey {
					t.Fatalf("kept %d records, want %d", len(list), HistoryPerKey)
				}
				got, _ := s.Latest(paneOf(report(1, agentactivity.StateWorking)))
				if got.Sequence != 40 {
					t.Fatalf("compaction lost the latest report: seq %d", got.Sequence)
				}
				// Compaction must not rewind the sequence gate: a replayed old
				// record after a compaction has to still be refused.
				if _, err := s.Append(report(20, agentactivity.StateIdle)); !errors.Is(err, ErrStaleSequence) {
					t.Fatalf("err = %v, want ErrStaleSequence after compaction", err)
				}
			})
		}
	})
}

func mustAppend(t *testing.T, s Store, r agentlifecycle.Report) {
	t.Helper()
	if _, err := s.Append(r); err != nil {
		t.Fatalf("append seq %d: %v", r.Sequence, err)
	}
}

// TestJSONLSurvivesADamagedLog is the "inspectable and repairable" promise held
// to account. The log is meant to be readable and fixable with ordinary tools,
// which means it will sometimes be hand-edited badly, and a machine that loses
// power mid-append leaves a truncated final line.
func TestJSONLSurvivesADamagedLog(t *testing.T) {
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
	damaged := string(data) +
		"this is not json at all\n" +
		"\n" +
		`{"schemaVersion":1,"id":"trunc"` + "\n" +
		`{"schemaVersion":99,"id":"future","kind":"state"}` + "\n"
	if err := os.WriteFile(path, []byte(damaged), 0o644); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenPath(path)
	if err != nil {
		t.Fatalf("a damaged log must still open: %v", err)
	}
	reopened.SetClock(func() time.Time { return testNow })

	got, ok := reopened.Latest(paneOf(report(2, agentactivity.StateBlocked)))
	if !ok || got.Sequence != 2 || got.State != agentactivity.StateBlocked {
		t.Fatalf("good records were lost with the bad ones: %+v ok=%v", got, ok)
	}
	// Sequencing must still be anchored to what survived.
	if _, err := reopened.Append(report(1, agentactivity.StateIdle)); !errors.Is(err, ErrStaleSequence) {
		t.Fatalf("err = %v, want ErrStaleSequence", err)
	}
}

// TestJSONLRejectsATamperedSequenceOnLoad covers the case the file format
// invites: someone edits the log by hand and rewinds a sequence, or resurrects
// a finished run. Refolding through the same admission rules as an append means
// the edit is dropped on load rather than believed.
func TestJSONLRejectsATamperedSequenceOnLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	s, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	s.SetClock(func() time.Time { return testNow })
	mustAppend(t, s, report(1, agentactivity.StateWorking))
	mustAppend(t, s, report(2, agentactivity.StateIdle))

	// Append a hand-written record that rewinds to an earlier sequence claiming
	// the pane is blocked.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	tampered := report(1, agentactivity.StateBlocked)
	tampered.ID = "tampered"
	line, err := jsonLine(tampered)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	reopened, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	reopened.SetClock(func() time.Time { return testNow })
	got, _ := reopened.Latest(paneOf(tampered))
	if got.State != agentactivity.StateIdle || got.ID == "tampered" {
		t.Fatalf("a rewound hand-edited record took authority: %+v", got)
	}
}

// TestJSONLConcurrentWriters proves the lock and the re-fold-under-lock rule do
// their job: every accepted sequence lands, none is lost to a lost update, and
// the fold afterwards agrees with the file.
func TestJSONLConcurrentWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)

	const writers = 8
	var wg sync.WaitGroup
	accepted := make([]bool, writers)

	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// A separate store instance per goroutine, as separate processes
			// would have: each holds its own fd, so they genuinely contend on
			// the flock rather than on the in-process mutex.
			s, err := OpenPath(path)
			if err != nil {
				return
			}
			s.SetClock(func() time.Time { return testNow })
			if _, err := s.Append(report(uint64(i+1), agentactivity.StateWorking)); err == nil {
				accepted[i] = true
			}
		}(i)
	}
	wg.Wait()

	final, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	final.SetClock(func() time.Time { return testNow })
	stored := map[uint64]bool{}
	for _, r := range final.List(paneOf(report(1, agentactivity.StateWorking))) {
		stored[r.Sequence] = true
	}
	for i, ok := range accepted {
		if ok && !stored[uint64(i+1)] {
			t.Fatalf("sequence %d was accepted but is not in the log", i+1)
		}
	}
	if len(stored) == 0 {
		t.Fatal("no writer succeeded at all")
	}
}

// TestReadAllDoesNotWrite backs the claim that inspection is free of side
// effects: `sidecar agent explain` and an agent reading the file must not
// create, lock, compact, or repair anything.
func TestReadAllDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)

	missing, err := ReadAll(path)
	if err != nil || missing != nil {
		t.Fatalf("ReadAll on a missing log = %v, %v", missing, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("ReadAll created %d files", len(entries))
	}
}

func jsonLine(r agentlifecycle.Report) (string, error) {
	data, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	return string(data) + "\n", nil
}
