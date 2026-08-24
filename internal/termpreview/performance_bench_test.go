package termpreview

import (
	"testing"

	"github.com/marcus/sidecar/internal/terminallink"
	"github.com/marcus/sidecar/internal/terminalperf"
	terminalfixture "github.com/marcus/sidecar/internal/testfixture/terminal"
	"github.com/marcus/sidecar/internal/tty"
)

var (
	benchmarkRows  []string
	benchmarkColor string
	benchmarkSpans []terminallink.Span
)

func openCodeRowsInput() (RowsInput, terminalfixture.OpenCode) {
	fixture := terminalfixture.NewOpenCode(160, 44)
	buffer := tty.NewOutputBuffer(200)
	buffer.ApplySnapshot(tty.PaneSnapshot{Output: fixture.Frame(3)})
	layout := tty.FitViewport(tty.ViewportInput{
		Buffer: buffer, Width: fixture.Width, Height: fixture.Height,
		PaneWidth: fixture.Width, PaneHeight: fixture.Height, Follow: true,
	})
	return RowsInput{
		Buffer: buffer, Layout: layout, PaneHeight: fixture.Height, Follow: true,
		Backgrounds: tty.BackgroundAuto,
	}, fixture
}

func BenchmarkDrawRowsOpenCodeFixture(b *testing.B) {
	input, _ := openCodeRowsInput()
	counters := &terminalperf.Counters{}
	restore := terminalperf.Install(counters)
	b.Cleanup(restore)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		benchmarkRows = DrawRows(input)
	}
	b.StopTimer()
	snapshot := counters.Snapshot()
	b.ReportMetric(float64(snapshot.TerminalViewsRendered)/float64(b.N), "terminal_views/op")
	b.ReportMetric(float64(snapshot.RowCacheHits)/float64(b.N), "row_cache_hits/op")
	b.ReportMetric(float64(snapshot.RowCacheMisses)/float64(b.N), "row_cache_misses/op")
}

func BenchmarkCanvasBackgroundOpenCodeFixture(b *testing.B) {
	input, fixture := openCodeRowsInput()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		benchmarkColor = CanvasBackground(input.Buffer, 3, fixture.Height)
	}
}

func BenchmarkLinkScanOpenCodeFixture(b *testing.B) {
	fixture := terminalfixture.NewOpenCode(160, 44)
	line := fixture.Frame(0)
	resolverCalls := 0
	resolve := func(raw string) (string, terminallink.Extra, bool) {
		resolverCalls++
		return raw, terminallink.Extra{Raw: raw}, true
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		benchmarkSpans = terminallink.ScanWith(line, terminallink.Options{Resolve: resolve})
	}
	b.StopTimer()
	b.ReportMetric(float64(resolverCalls)/float64(b.N), "resolver_calls/op")
}
