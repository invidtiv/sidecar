// Package startuptrace records how long each phase of sidecar's startup takes.
//
// Startup latency is dominated by work that happens before Bubble Tea paints
// the first frame. On machines with an endpoint security agent (CrowdStrike
// Falcon and friends) every file open and every process spawn is intercepted,
// so work that is cheap on a developer laptop can cost seconds elsewhere.
// This package makes that cost visible.
//
// Tracing is off unless SIDECAR_STARTUP_TRACE is set. Set it to "1" to write
// the report to the debug log, or to "stderr" to also print it to stderr on
// exit (useful when running sidecar under tmux capture).
package startuptrace

import (
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type span struct {
	name     string
	dur      time.Duration
	startOff time.Duration
}

var (
	mu      sync.Mutex
	spans   []span
	counts  map[string]int
	start   time.Time
	enabled bool
	stderrs bool
)

func init() {
	v := os.Getenv("SIDECAR_STARTUP_TRACE")
	enabled = v != "" && v != "0"
	stderrs = v == "stderr"
	start = time.Now()
	counts = make(map[string]int)
}

// Enabled reports whether tracing is active.
func Enabled() bool { return enabled }

// ReportDelay is how long after startup the trace should be dumped, so that
// async work started during boot is included. Override with
// SIDECAR_STARTUP_TRACE_DELAY (a Go duration, e.g. "20s").
func ReportDelay() time.Duration {
	if d, err := time.ParseDuration(os.Getenv("SIDECAR_STARTUP_TRACE_DELAY")); err == nil && d > 0 {
		return d
	}
	return 5 * time.Second
}

// Track times fn and records it under name. It always calls fn.
func Track(name string, fn func()) {
	if !enabled {
		fn()
		return
	}
	t0 := time.Now()
	fn()
	record(name, time.Since(t0), t0.Sub(start))
}

// Begin returns a function that records the elapsed time when called. Use it
// when the work is not conveniently wrappable in a closure:
//
//	defer startuptrace.Begin("phase")()
func Begin(name string) func() {
	if !enabled {
		return func() {}
	}
	t0 := time.Now()
	return func() { record(name, time.Since(t0), t0.Sub(start)) }
}

// Count increments a named counter (e.g. subprocess spawns).
func Count(name string) {
	if !enabled {
		return
	}
	mu.Lock()
	counts[name]++
	mu.Unlock()
}

func record(name string, dur, off time.Duration) {
	mu.Lock()
	spans = append(spans, span{name: name, dur: dur, startOff: off})
	mu.Unlock()
}

// Mark records a zero-length event at the current offset from process start.
func Mark(name string) {
	if !enabled {
		return
	}
	record(name, 0, time.Since(start))
}

// Report writes the collected spans to the logger (and stderr when the trace
// env var is "stderr"). Safe to call when tracing is disabled.
func Report(logger *slog.Logger) {
	if !enabled {
		return
	}
	mu.Lock()
	local := make([]span, len(spans))
	copy(local, spans)
	localCounts := make(map[string]int, len(counts))
	for k, v := range counts {
		localCounts[k] = v
	}
	total := time.Since(start)
	mu.Unlock()

	sort.SliceStable(local, func(i, j int) bool { return local[i].startOff < local[j].startOff })

	var b strings.Builder
	fmt.Fprintf(&b, "\n=== sidecar startup trace (total %s) ===\n", total.Round(time.Microsecond))
	for _, s := range local {
		if s.dur == 0 {
			fmt.Fprintf(&b, "  %8s  %-44s (mark)\n", s.startOff.Round(time.Microsecond), s.name)
			continue
		}
		fmt.Fprintf(&b, "  %8s  %-44s %10s\n", s.startOff.Round(time.Microsecond), s.name, s.dur.Round(time.Microsecond))
	}
	if len(localCounts) > 0 {
		keys := make([]string, 0, len(localCounts))
		for k := range localCounts {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("  counters:\n")
		for _, k := range keys {
			fmt.Fprintf(&b, "    %-42s %d\n", k, localCounts[k])
		}
	}

	if logger != nil {
		logger.Info("startup trace", "report", b.String())
	}
	if stderrs {
		fmt.Fprint(os.Stderr, b.String())
	}
}
