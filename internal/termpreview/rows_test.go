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
	// Grok (and capture-pane of a 0m-resetting TUI) closes each painted row, so
	// the new unpainted rows after a resize do not inherit the canvas. Without
	// trimming those trailing default-bg blanks, 4-of-24 misses the 90% share.
	lines := []string{
		canvasBlack + "header\x1b[0m",
		canvasBlack + "body\x1b[0m",
		canvasBlack + "   \x1b[0m",
		canvasBlack + "status\x1b[0m",
	}
	for range 20 {
		lines = append(lines, "")
	}
	if CanvasRowShare(len(lines)) <= 4 {
		t.Fatalf("fixture would not drown without the trim: share(%d)=%d", len(lines), CanvasRowShare(len(lines)))
	}
	buffer := canvasBuffer(t, lines, len(lines))

	if got := CanvasBackground(buffer, 0, len(lines)); got != canvasBlack {
		t.Fatalf("canvas = %q, want %q with trailing 0m-closed default-bg rows ignored", got, canvasBlack)
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

	drawn := DrawRows(RowsInput{
		Buffer:      buffer,
		Layout:      layout,
		PaneHeight:  10,
		Interactive: true,
		Follow:      true,
	})
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
	drawn := DrawRows(RowsInput{
		Buffer: buffer, Layout: layout, PaneHeight: 16, Interactive: true, Follow: true,
	})
	got := PadCanvasBox(strings.Join(drawn, "\n"), canvasBlack, 30, 16)
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

// A TUI that repaints itself in sections leaves interior rows the grid never
// touched — default-attribute cells tmux captures as empty. In a real terminal
// those show the terminal's own default, which is the colour the application
// matched to its canvas; they must abstain from the vote, not drown it.
// Regression: an opencode pane mid-repaint flipped between filled and unfilled
// as those abstentions pushed coverage under the near-total bar.
func TestCanvasBackgroundAbstainsInteriorDefaultRows(t *testing.T) {
	var lines []string
	// Eight painted rows that each close their background ([0m), then eight
	// untouched rows, then more painted rows including a painted blank.
	for range 8 {
		lines = append(lines, canvasBlack+"content\x1b[0m")
	}
	for range 8 {
		lines = append(lines, "")
	}
	lines = append(lines, canvasBlack+"\x1b[0m")
	for range 2 {
		lines = append(lines, canvasBlack+"status\x1b[0m")
	}
	buffer := canvasBuffer(t, lines, len(lines))

	// Under the old all-rows share: 9 of 19 measured < CanvasRowShare(19)=17,
	// so the canvas was missed and the untouched rows fell through to the
	// surface. The blank-row requirement still holds: the canvas appears on
	// the painted blank row (row 16).
	if CanvasRowShare(9) > 9 {
		t.Fatal("fixture premise: nine painted rows cannot reach their own share")
	}
	if got := CanvasBackground(buffer, 0, len(lines)); got != canvasBlack {
		t.Fatalf("canvas = %q, want %q with interior default rows abstaining", got, canvasBlack)
	}
}

// The abstention rule does not open the door to highlighting: a diff's
// added-line green paints content rows only, so it never lands on a blank row
// and fails the requirement regardless of how few other rows carry any
// background.
func TestInteriorAbstentionsStillRejectHighlightOnlyCanvases(t *testing.T) {
	green := "\x1b[48;2;0;80;0m"
	var lines []string
	for range 4 {
		lines = append(lines, green+"+ added line\x1b[49m")
	}
	for range 12 {
		lines = append(lines, "")
	}
	buffer := canvasBuffer(t, lines, len(lines))
	if got := CanvasBackground(buffer, 0, len(lines)); got != "" {
		t.Fatalf("green highlight promoted to canvas %q once unpainted rows abstain", got)
	}
}
