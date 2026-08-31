package termpreview

import (
	"fmt"
	"strings"
	"testing"
	"unsafe"

	"github.com/marcus/sidecar/internal/terminalperf"
	"github.com/marcus/sidecar/internal/tty"
)

func TestRowAnalyzerAnalyzesEachRequiredRawRowOnceAndReusesFacts(t *testing.T) {
	lines := make([]string, 500)
	for i := range lines {
		lines[i] = fmt.Sprintf("row-%03d", i)
	}
	buffer := tty.NewOutputBuffer(600)
	buffer.Update(strings.Join(lines, "\n"))
	analyzer := &RowAnalyzer{}
	in := RowsInput{
		Buffer: buffer,
		Layout: tty.Viewport{
			Start: 50, End: 70, EffectiveCount: 20,
			DisplayWidth: 80, DisplayHeight: 20,
			PaneTop: 400,
		},
		PaneHeight:  40,
		Follow:      true,
		Backgrounds: tty.BackgroundAuto,
		Analyzer:    analyzer,
	}

	counters := &terminalperf.Counters{}
	restore := terminalperf.Install(counters)
	t.Cleanup(restore)
	first := DrawRows(in)
	firstSnapshot := counters.Snapshot()
	// The visible window is [50,70) and its lookback clamps to zero, so the
	// required band is [0,70): 70 distinct raw rows retained and analyzed.
	if firstSnapshot.RowCacheMisses != 70 {
		t.Fatalf("first draw counters = %+v, want 70 analyses", firstSnapshot)
	}
	if len(analyzer.rows) != 70 {
		t.Fatalf("retained rows = %d, want the 70-row band", len(analyzer.rows))
	}

	secondCounters := &terminalperf.Counters{}
	restoreSecond := terminalperf.Install(secondCounters)
	second := DrawRows(in)
	restoreSecond()
	secondSnapshot := secondCounters.Snapshot()
	if secondSnapshot.RowCacheMisses != 0 || secondSnapshot.RowCacheHits != 70 {
		t.Fatalf("same-revision draw counters = %+v, want all facts reused", secondSnapshot)
	}
	if strings.Join(first.Rows, "\n") != strings.Join(second.Rows, "\n") || first.CanvasBackground != second.CanvasBackground {
		t.Fatal("same-revision cache changed the drawn result")
	}

	lines[30] = "row-030 changed"
	buffer.Update(strings.Join(lines, "\n"))
	changedCounters := &terminalperf.Counters{}
	restoreChanged := terminalperf.Install(changedCounters)
	_ = DrawRows(in)
	restoreChanged()
	changed := changedCounters.Snapshot()
	if changed.RowCacheMisses != 1 || changed.RowCacheHits != 69 {
		t.Fatalf("one-row revision counters = %+v, want one changed fingerprint only", changed)
	}

	// ANSI styling is part of the exact raw-row identity. A style-only change
	// must invalidate that row without discarding facts for its neighbours.
	lines[51] = "\x1b[31mrow-051\x1b[0m"
	buffer.Update(strings.Join(lines, "\n"))
	styledCounters := &terminalperf.Counters{}
	restoreStyled := terminalperf.Install(styledCounters)
	styled := DrawRows(in)
	restoreStyled()
	styledSnapshot := styledCounters.Snapshot()
	if styledSnapshot.RowCacheMisses != 1 || styledSnapshot.RowCacheHits != 69 {
		t.Fatalf("style-only revision counters = %+v, want one changed fingerprint only", styledSnapshot)
	}
	if !strings.Contains(strings.Join(styled.Rows, "\n"), "\x1b[31mrow-051") {
		t.Fatal("style-only revision reused stale rendered bytes")
	}
}

func TestRowAnalyzerRotatesWindowsAndModesWithoutRetainingHistoryGaps(t *testing.T) {
	lines := make([]string, 500)
	for i := range lines {
		lines[i] = fmt.Sprintf("row-%03d", i)
	}
	buffer := tty.NewOutputBuffer(600)
	buffer.Update(strings.Join(lines, "\n"))
	analyzer := &RowAnalyzer{}
	in := RowsInput{
		Buffer: buffer,
		Layout: tty.Viewport{
			Start: 0, End: 20, EffectiveCount: 20,
			DisplayWidth: 80, DisplayHeight: 20,
			PaneTop: 400,
		},
		PaneHeight:  40,
		Follow:      true,
		Backgrounds: tty.BackgroundAuto,
		Analyzer:    analyzer,
	}
	_ = DrawRows(in)

	// Scroll far below the original window. The retained band becomes
	// [150,470): the new window plus its lookback, without the original
	// [0,20) or any unrelated history.
	in.Layout.Start, in.Layout.End = 450, 470
	_ = DrawRows(in)
	if len(analyzer.rows) != 320 {
		t.Fatalf("rotated retained rows = %d, want 320", len(analyzer.rows))
	}
	for index := range analyzer.rows {
		if index < 150 || index >= 470 {
			t.Fatalf("rotated cache retained out-of-window row %d", index)
		}
	}

	// A mode change owns a new carried derivation but not new raw facts, so
	// the same band is retained and every row is reused.
	in.Backgrounds = tty.BackgroundNever
	counters := &terminalperf.Counters{}
	restore := terminalperf.Install(counters)
	_ = DrawRows(in)
	restore()
	got := counters.Snapshot()
	if len(analyzer.rows) != 320 || got.RowCacheMisses != 0 || got.RowCacheHits != 320 {
		t.Fatalf("mode rotation counters = %+v retained=%d, want visible-band reuse only", got, len(analyzer.rows))
	}
}

func TestRowAnalyzerRawIdentityUsesExactCollisionGuard(t *testing.T) {
	buffer := tty.NewOutputBuffer(10)
	buffer.Update("same bytes")
	analyzer := &RowAnalyzer{}
	in := RowsInput{
		Buffer:      buffer,
		Layout:      tty.Viewport{Start: 0, End: 1, EffectiveCount: 1, DisplayWidth: 20, DisplayHeight: 1},
		Backgrounds: tty.BackgroundNever,
		Analyzer:    analyzer,
	}
	_ = DrawRows(in)
	// Simulate a fingerprint+length collision in the retained slot. The exact
	// raw-string guard must still reject it.
	analyzer.rows[0].raw = "evil byte!"
	counters := &terminalperf.Counters{}
	restore := terminalperf.Install(counters)
	_ = DrawRows(in)
	restore()
	if got := counters.Snapshot(); got.RowCacheMisses != 1 {
		t.Fatalf("collision-guard counters = %+v, want one exact-string miss", got)
	}
}

func TestRowAnalyzerStoresCollisionGuardInRowOwnedStorage(t *testing.T) {
	buffer := tty.NewOutputBuffer(10)
	buffer.Update("target row\n" + strings.Repeat("x", 1<<20))
	_, snapshot := buffer.SnapshotRanges(tty.RowRange{Start: 0, End: 1})
	source := snapshot[0][0]
	analyzer := &RowAnalyzer{}
	_ = DrawRows(RowsInput{
		Buffer:      buffer,
		Layout:      tty.Viewport{Start: 0, End: 1, EffectiveCount: 1, DisplayWidth: 20, DisplayHeight: 1},
		Backgrounds: tty.BackgroundNever,
		Analyzer:    analyzer,
	})
	cached := analyzer.rows[0].raw
	if cached != source {
		t.Fatalf("cached raw = %q, want %q", cached, source)
	}
	if unsafe.StringData(cached) == unsafe.StringData(source) {
		t.Fatal("cached row still shares the full capture's backing storage")
	}
	if len(analyzer.rows) != 1 {
		t.Fatalf("retained rows = %d, want only the requested row", len(analyzer.rows))
	}
}

func TestRowAnalyzerCarryInvalidationStopsWhenOutgoingBackgroundConverges(t *testing.T) {
	red := "\x1b[41m"
	blue := "\x1b[44m"
	green := "\x1b[42m"
	buffer := tty.NewOutputBuffer(10)
	buffer.Update(strings.Join([]string{red + "a", green + "b", "c"}, "\n"))
	analyzer := &RowAnalyzer{}
	in := RowsInput{
		Buffer:      buffer,
		Layout:      tty.Viewport{Start: 0, End: 3, EffectiveCount: 3, DisplayWidth: 20, DisplayHeight: 3},
		Backgrounds: tty.BackgroundBounded,
		Analyzer:    analyzer,
	}
	_ = DrawRows(in)
	if analyzer.rows[1].resolveCount != 1 || analyzer.rows[2].resolveCount != 1 {
		t.Fatalf("initial resolve counts = %d, %d", analyzer.rows[1].resolveCount, analyzer.rows[2].resolveCount)
	}

	buffer.Update(strings.Join([]string{blue + "a", green + "b", "c"}, "\n"))
	_ = DrawRows(in)
	if analyzer.rows[1].resolveCount != 2 {
		t.Fatalf("changed incoming carry did not invalidate the next row: count=%d", analyzer.rows[1].resolveCount)
	}
	if analyzer.rows[2].resolveCount != 1 {
		t.Fatalf("carry invalidation continued after green convergence: count=%d", analyzer.rows[2].resolveCount)
	}
}
