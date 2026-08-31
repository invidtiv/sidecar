package termpreview

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/terminalperf"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

const canvasBlack = "\x1b[48;2;20;20;20m"

func canvasBuffer(t *testing.T, lines []string, paneRows int) *tty.OutputBuffer {
	t.Helper()
	buffer := tty.NewOutputBuffer(200)
	buffer.ApplySnapshot(tty.PaneSnapshot{
		Output:   strings.Join(lines, "\n"),
		PaneRows: paneRows,
	})
	return buffer
}

func TestDrawRowsFillsUnusedRowsAndColumnsWithCanvas(t *testing.T) {
	// Four 0m-closed painted rows in a 10-row, wider pane: the capture is
	// shorter and narrower than the allotted box after a resize.
	lines := []string{
		canvasBlack + "header\x1b[0m",
		canvasBlack + "body\x1b[0m",
		canvasBlack + "   \x1b[0m",
		canvasBlack + "status\x1b[49m tail",
	}
	buffer := canvasBuffer(t, lines, len(lines))
	layout := tty.FitViewport(tty.ViewportInput{
		Buffer: buffer, Width: 30, Height: 10, Follow: true,
		Interactive: true, PaneWidth: 12, PaneHeight: 10, Scrollbar: true,
	})
	if layout.DisplayHeight <= len(lines) {
		t.Fatalf("display height %d does not add unused rows below %d captured", layout.DisplayHeight, len(lines))
	}

	draw := DrawRows(RowsInput{
		Buffer:            buffer,
		Layout:            layout,
		DefaultBackground: canvasBlack,
		PaneHeight:        10,
		Interactive:       true,
		Follow:            true,
	})
	drawn := draw.Rows
	if len(drawn) != layout.DisplayHeight {
		t.Fatalf("drew %d rows, want allotted height %d", len(drawn), layout.DisplayHeight)
	}

	fillWidth := layout.PadWidth
	if fillWidth < layout.DisplayWidth {
		fillWidth = layout.DisplayWidth
	}
	for i, row := range drawn {
		if !strings.HasPrefix(row, canvasBlack) {
			t.Errorf("row %d is not painted with the canvas: %q", i, row)
		}
		if w := ansi.StringWidth(row); w < fillWidth {
			t.Errorf("row %d is %d columns, want at least the allotted %d: %q", i, w, fillWidth, row)
		}
	}
	if !strings.Contains(drawn[3], "\x1b[49m"+canvasBlack+" tail") {
		t.Errorf("default-bg tail was not re-painted: %q", drawn[3])
	}
}

func TestDrawRowsResolvesChildDefaultCellsToTheHostBackground(t *testing.T) {
	host := "\x1b[48;2;40;43;51m"
	panel := "\x1b[48;2;69;74;81m"
	lines := []string{
		"plain default row",
		panel + "panel" + ui.RowBackgroundDefault + " default tail",
		"",
	}
	buffer := canvasBuffer(t, lines, len(lines))
	layout := tty.FitViewport(tty.ViewportInput{
		Buffer: buffer, Width: 30, Height: len(lines), Follow: true,
		Interactive: true, PaneWidth: 30, PaneHeight: len(lines),
	})
	draw := DrawRows(RowsInput{
		Buffer: buffer, Layout: layout, DefaultBackground: host,
		PaneHeight: len(lines), Interactive: true, Follow: true,
	})
	if draw.CanvasBackground != host {
		t.Fatalf("resolved default background = %q, want host %q", draw.CanvasBackground, host)
	}
	if !strings.HasPrefix(draw.Rows[0], host) {
		t.Errorf("plain child-default row did not open in host background: %q", draw.Rows[0])
	}
	if !strings.Contains(draw.Rows[1], panel+"panel"+ui.RowBackgroundDefault+host+" default tail") {
		t.Errorf("explicit child panel or host-default tail was lost: %q", draw.Rows[1])
	}
}

func TestDrawRowsFallbackIsObservableAndByteEquivalent(t *testing.T) {
	buffer := canvasBuffer(t, []string{"first", "second"}, 2)
	layout := tty.FitViewport(tty.ViewportInput{Buffer: buffer, Width: 20, Height: 2, Follow: true})
	in := RowsInput{Buffer: buffer, Layout: layout, PaneHeight: 2, Follow: true, Backgrounds: tty.BackgroundAuto}

	fallbackCounters := &terminalperf.Counters{}
	restoreFallback := terminalperf.Install(fallbackCounters)
	fallback := DrawRows(in)
	restoreFallback()
	if got := fallbackCounters.Snapshot(); got.RowAnalyzerBypasses != 1 || got.RowCacheMisses == 0 {
		t.Fatalf("fallback counters = %+v, want one observable bypass and cold analysis", got)
	}

	durableCounters := &terminalperf.Counters{}
	restoreDurable := terminalperf.Install(durableCounters)
	in.Analyzer = &RowAnalyzer{}
	durable := DrawRows(in)
	restoreDurable()
	if got := durableCounters.Snapshot(); got.RowAnalyzerBypasses != 0 || got.RowCacheMisses == 0 {
		t.Fatalf("durable counters = %+v, want cold analysis without a bypass", got)
	}
	if strings.Join(fallback.Rows, "\n") != strings.Join(durable.Rows, "\n") || fallback.CanvasBackground != durable.CanvasBackground {
		t.Fatal("fallback analyzer changed rendered terminal output")
	}
}

func TestRowAnalyzerReuseDoesNotFreezeHostBackground(t *testing.T) {
	buffer := canvasBuffer(t, []string{"child default"}, 1)
	layout := tty.FitViewport(tty.ViewportInput{Buffer: buffer, Width: 20, Height: 1, Follow: true})
	analyzer := &RowAnalyzer{}
	in := RowsInput{
		Buffer: buffer, Layout: layout, PaneHeight: 1, Follow: true,
		Backgrounds: tty.BackgroundAuto, Analyzer: analyzer,
		DefaultBackground: "\x1b[48;2;1;2;3m",
	}
	first := DrawRows(in)
	in.DefaultBackground = "\x1b[48;2;9;8;7m"
	counters := &terminalperf.Counters{}
	restore := terminalperf.Install(counters)
	second := DrawRows(in)
	restore()
	if got := counters.Snapshot(); got.RowCacheMisses != 0 || got.RowCacheHits == 0 {
		t.Fatalf("host background change counters = %+v, want reusable raw facts", got)
	}
	if strings.Join(first.Rows, "\n") == strings.Join(second.Rows, "\n") || !strings.Contains(second.Rows[0], in.DefaultBackground) {
		t.Fatal("cached raw facts froze the previous host background")
	}
}

func TestPadCanvasBoxFillsTheAllottedBox(t *testing.T) {
	panel := "\x1b[48;2;36;36;36m"
	// Rows arrive already painted (DrawRows). PadCanvasBox must not re-walk
	// them; it only grows unused columns and unused rows.
	row0 := ui.ApplyTerminalDefaultBackground(canvasBlack+"ab", canvasBlack, 2)
	row1 := ui.ApplyTerminalDefaultBackground(panel+"cd\x1b[49m e", canvasBlack, 4)
	got := PadCanvasBox(row0+"\n"+row1, canvasBlack, 8, 5)
	rows := strings.Split(got, "\n")
	if len(rows) != 5 {
		t.Fatalf("padded to %d rows, want 5", len(rows))
	}
	for i, row := range rows {
		if ansi.StringWidth(row) != 8 {
			t.Errorf("row %d width = %d, want 8: %q", i, ansi.StringWidth(row), row)
		}
		if !strings.Contains(row, canvasBlack) {
			t.Errorf("row %d missing canvas: %q", i, row)
		}
	}
	if !strings.Contains(rows[1], panel+"cd\x1b[49m"+canvasBlack+" e") {
		t.Errorf("explicit panel/default transition = %q", rows[1])
	}
	if strings.Count(rows[1], panel+"cd\x1b[49m"+canvasBlack) != 1 {
		t.Errorf("panel/default transition was applied twice: %q", rows[1])
	}
}

// The workspace viewport paints in DrawRows then grows the box with
// PadCanvasBox. That composition must keep the panel/default contract the
// fullscreen-canvas tests lock: panel + 49m + canvas + " default", once.
func TestDrawRowsThenPadCanvasBoxPreservesPanelDefault(t *testing.T) {
	panel := "\x1b[48;2;36;36;36m"
	lines := []string{
		canvasBlack + "header\x1b[0m",
		canvasBlack + "   \x1b[0m",
		panel + "panel\x1b[49m default",
		canvasBlack + "status\x1b[0m",
	}
	for range 12 {
		lines = append(lines, "")
	}
	buffer := canvasBuffer(t, lines, len(lines))
	layout := tty.FitViewport(tty.ViewportInput{
		Buffer: buffer, Width: 30, Height: 16, Follow: true,
		Interactive: true, PaneWidth: 30, PaneHeight: 16, Scrollbar: true,
	})
	draw := DrawRows(RowsInput{
		Buffer: buffer, Layout: layout, DefaultBackground: canvasBlack,
		PaneHeight: 16, Interactive: true, Follow: true,
	})
	drawn := draw.Rows
	got := PadCanvasBox(strings.Join(drawn, "\n"), draw.CanvasBackground, 30, 16)
	rows := strings.Split(got, "\n")
	if len(rows) != 16 {
		t.Fatalf("composed %d rows, want 16", len(rows))
	}
	want := panel + "panel\x1b[49m" + canvasBlack + " default"
	if !strings.Contains(rows[2], want) {
		t.Fatalf("explicit panel/default transition = %q", rows[2])
	}
	if strings.Count(rows[2], want) != 1 {
		t.Fatalf("panel/default transition was applied twice: %q", rows[2])
	}
}

func TestPadCanvasBoxWithoutCanvasMatchesFit(t *testing.T) {
	got := PadCanvasBox("ab\ncd", "", 4, 3)
	rows := strings.Split(got, "\n")
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	for i, row := range rows {
		if ansi.StringWidth(row) != 4 {
			t.Errorf("row %d width = %d, want 4: %q", i, ansi.StringWidth(row), row)
		}
		if strings.Contains(row, "\x1b[") {
			t.Errorf("unstyled pad grew an escape: %q", row)
		}
	}
}

func TestDrawRowsDoesNotInventACanvasForPlainScrollback(t *testing.T) {
	buffer := canvasBuffer(t, []string{"prompt", "output", ""}, 3)
	layout := tty.FitViewport(tty.ViewportInput{
		Buffer: buffer, Width: 20, Height: 8, Follow: true,
		Interactive: true, PaneWidth: 20, PaneHeight: 8,
	})
	drawn := DrawRows(RowsInput{
		Buffer: buffer, Layout: layout, PaneHeight: 8, Interactive: true, Follow: true,
	}).Rows
	for i, row := range drawn {
		if strings.Contains(row, canvasBlack) {
			t.Errorf("plain row %d was painted as a canvas: %q", i, row)
		}
	}
}

func TestCarryThenFillKeepsExplicitBackgrounds(t *testing.T) {
	// Sanity that the fill helper used by DrawRows is the same contract
	// ApplyTerminalDefaultBackground already tests: default cells take the
	// canvas, explicit cells do not.
	panel := "\x1b[48;2;36;36;36m"
	got := ui.ApplyTerminalDefaultBackground(panel+"x\x1b[49m y", canvasBlack, 6)
	if !strings.Contains(got, panel+"x\x1b[49m"+canvasBlack+" y") {
		t.Fatalf("panel/default mix = %q", got)
	}
}
