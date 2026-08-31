package lifecyclestore

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/agentlifecycle"
)

// Phase E step 3 measurements for the read path.
//
// The reader is the cost that recurs: every polling surface folds this log for
// every pane on every refresh, so its shape over a large store is the number
// that decides whether a daemon is ever needed. The writer's cost is dominated
// by process start and fsync and is measured outside Go.

// benchLog writes n records spread over panes so the fold does real map work
// rather than repeatedly overwriting one key.
func benchLog(b *testing.B, n int) string {
	b.Helper()
	path := filepath.Join(b.TempDir(), FileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	const panes = 8
	for i := 0; i < n; i++ {
		r := report(uint64(i/panes+1), agentactivity.StateWorking)
		r.ID = fmt.Sprintf("bench-%d", i)
		r.Identity.PaneID = fmt.Sprintf("%%%d", i%panes)
		line, err := jsonLine(r)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := f.WriteString(line); err != nil {
			b.Fatal(err)
		}
	}
	return path
}

func benchReadAll(b *testing.B, n int) {
	path := benchLog(b, n)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := ReadAll(path)
		if err != nil {
			b.Fatal(err)
		}
		if len(got) != n {
			b.Fatalf("read %d records, want %d", len(got), n)
		}
	}
}

// BenchmarkReadAll is the lock-free inspection path StoreSource uses on every
// cache miss.
func BenchmarkReadAll100(b *testing.B)   { benchReadAll(b, 100) }
func BenchmarkReadAll1000(b *testing.B)  { benchReadAll(b, 1000) }
func BenchmarkReadAll10000(b *testing.B) { benchReadAll(b, 10000) }

func benchOpenFold(b *testing.B, n int) {
	path := benchLog(b, n)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		// Each iteration starts from the same unfolded file, because Open
		// compacts and a second open would measure a much shorter log.
		fresh := filepath.Join(b.TempDir(), FileName)
		data, err := os.ReadFile(path)
		if err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(fresh, data, 0o644); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()

		s, err := OpenPath(fresh)
		if err != nil {
			b.Fatal(err)
		}
		s.SetClock(func() time.Time { return testNow })
	}
}

// BenchmarkOpenFold is what one `sidecar agent report` process pays before it
// can write: open, fold the whole log through the admission rules, and compact
// if the bounds call for it.
func BenchmarkOpenFold100(b *testing.B)   { benchOpenFold(b, 100) }
func BenchmarkOpenFold1000(b *testing.B)  { benchOpenFold(b, 1000) }
func BenchmarkOpenFold10000(b *testing.B) { benchOpenFold(b, 10000) }

// BenchmarkLatest is the per-pane answer a polling surface asks for, including
// the lock and the re-fold under it that keeps a cross-process append visible.
func BenchmarkLatest1000(b *testing.B) {
	path := benchLog(b, 1000)
	s, err := OpenPath(path)
	if err != nil {
		b.Fatal(err)
	}
	s.SetClock(func() time.Time { return testNow })
	key := PaneKey{ServerIncarnation: "inc-1", PaneID: "%3"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := s.Latest(key); !ok {
			b.Fatal("no record for the benchmarked pane")
		}
	}
}

// BenchmarkValidate is the per-record cost every writer and every refold pays.
func BenchmarkValidate(b *testing.B) {
	r := report(1, agentactivity.StateWorking)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := agentlifecycle.Validate(r, testNow); err != nil {
			b.Fatal(err)
		}
	}
}
