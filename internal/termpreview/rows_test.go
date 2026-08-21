package termpreview

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
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

// Trailing default-background blanks are what a full-screen TUI looks like
// after the pane grows and before it repaints: the old canvas is still there,
// the new rows are unpainted. They must not drown the canvas vote, or the
// extra rows stay on Sidecar's surface instead of the TUI's black.
func TestCanvasBackgroundIgnoresTrailingDefaultBackgroundRows(t *testing.T) {
	lines := []string{canvasBlack + "header", canvasBlack + "body", canvasBlack + "   ", "status"}
	for range 20 {
		lines = append(lines, "")
	}
	buffer := canvasBuffer(t, lines, len(lines))

	if got := CanvasBackground(buffer, 0, len(lines)); got != canvasBlack {
		t.Fatalf("canvas = %q, want %q with trailing default-bg rows ignored", got, canvasBlack)
	}
}

func TestCanvasBackgroundStillRejectsAFullPaneDiff(t *testing.T) {
	green := "\x1b[48;2;0;80;0m"
	var rows []string
	for range 10 {
		rows = append(rows, green+"+ added line\x1b[49m")
	}
	buffer := canvasBuffer(t, rows, len(rows))
	if bg := CanvasBackground(buffer, 0, len(rows)); bg != "" {
		t.Fatalf("a fully green diff was promoted to canvas %q", bg)
	}
}

func TestDrawRowsFillsUnusedRowsAndColumnsWithCanvas(t *testing.T) {
	// Painted region is 4×8; the allotted box is 10×20, the way a capture
	// shorter and narrower than the Sidecar pane looks after a resize.
	lines := []string{
		canvasBlack + "header",
		"body",
		"",
		canvasBlack + "status\x1b[49m tail",
	}
	buffer := canvasBuffer(t, lines, len(lines))
	layout := tty.FitViewport(tty.ViewportInput{
		Buffer: buffer, Width: 30, Height: 10, Follow: true,
		Interactive: true, PaneWidth: 12, PaneHeight: 4, Scrollbar: true,
	})

	drawn := DrawRows(RowsInput{
		Buffer:      buffer,
		Layout:      layout,
		PaneHeight:  4,
		Interactive: true,
		Follow:      true,
	})
	if len(drawn) != layout.DisplayHeight {
		t.Fatalf("drew %d rows, want pane height %d", len(drawn), layout.DisplayHeight)
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

func TestPadCanvasBoxFillsTheAllottedBox(t *testing.T) {
	panel := "\x1b[48;2;36;36;36m"
	content := canvasBlack + "ab\n" + panel + "cd\x1b[49m e"
	got := PadCanvasBox(content, canvasBlack, 8, 5)
	rows := strings.Split(got, "\n")
	if len(rows) != 5 {
		t.Fatalf("padded to %d rows, want 5", len(rows))
	}
	for i, row := range rows {
		if ansi.StringWidth(row) != 8 {
			t.Errorf("row %d width = %d, want 8: %q", i, ansi.StringWidth(row), row)
		}
		if !strings.HasPrefix(row, canvasBlack) {
			t.Errorf("row %d missing canvas: %q", i, row)
		}
	}
	if !strings.Contains(rows[1], panel+"cd\x1b[49m"+canvasBlack+" e") {
		t.Errorf("explicit panel/default transition = %q", rows[1])
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
	})
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
