package termpreview

import (
	"fmt"
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

	draw := DrawRows(RowsInput{
		Buffer:      buffer,
		Layout:      layout,
		PaneHeight:  10,
		Interactive: true,
		Follow:      true,
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

func TestDrawRowsChildCanvasOverridesTheHostDefault(t *testing.T) {
	host := "\x1b[48;2;40;43;51m"
	lines := []string{
		canvasBlack + "header\x1b[0m",
		canvasBlack + "body\x1b[0m",
		canvasBlack + "   \x1b[0m",
		canvasBlack + "status\x1b[0m",
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
	if draw.CanvasBackground != canvasBlack {
		t.Fatalf("resolved background = %q, want child canvas %q", draw.CanvasBackground, canvasBlack)
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
		Buffer: buffer, Layout: layout, PaneHeight: 16, Interactive: true, Follow: true,
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

// A full-height panel rides every row the canvas does, so the row-count vote
// ties. Captured live from opencode: every row opens in the canvas
// (48;2;10;10;10) and the side panel (48;2;20;20;20) opens mid-row, so the tie
// breaks to the background that owns the row starts.
func TestCanvasBackgroundBreaksFullHeightPanelTieOnRowStarts(t *testing.T) {
	canvas := "\x1b[48;2;10;10;10m"
	panel := "\x1b[48;2;20;20;20m"
	var lines []string
	lines = append(lines, canvas+"  chat  "+panel+"  panel  "+canvas)
	for i := range 10 {
		if i%2 == 0 {
			lines = append(lines, canvas+"        "+panel+"  panel  "+canvas)
		} else {
			// Padding rows are whitespace in both backgrounds.
			lines = append(lines, canvas+"        "+panel+"         "+canvas)
		}
	}
	lines = append(lines, canvas+"  input "+panel+"         "+canvas)
	buffer := canvasBuffer(t, lines, len(lines))
	if got := CanvasBackground(buffer, 0, len(lines)); got != canvas {
		t.Fatalf("canvas = %q, want %q when a full-height panel ties the row vote", got, canvas)
	}
}

// When the tied candidates also split the row starts evenly there is no single
// canvas to find, and detection must abstain rather than guess.
func TestCanvasBackgroundAbstainsWhenRowStartsTieToo(t *testing.T) {
	left := "\x1b[48;2;10;10;10m"
	right := "\x1b[48;2;20;20;20m"
	var lines []string
	for i := range 12 {
		a, b := left, right
		if i%2 == 1 {
			a, b = right, left
		}
		// Half the rows are blank in both backgrounds, so the share bar and
		// blank-row gates pass for either candidate and only the row-start
		// tie forces the abstention.
		if i%4 < 2 {
			lines = append(lines, a+"        "+b+"        \x1b[49m")
		} else {
			lines = append(lines, a+"  half  "+b+"  half  \x1b[49m")
		}
	}
	buffer := canvasBuffer(t, lines, len(lines))
	if got := CanvasBackground(buffer, 0, len(lines)); got != "" {
		t.Fatalf("canvas = %q, want abstention when row starts tie as well", got)
	}
}

// The screen model's serialization closes every row with a reset and trims
// BCE tails, so an opencode pane that proves its canvas through blank rows in
// a raw capture comes back with no blank canvas rows at all — the pane filled
// on the first raw frame and reverted when the model took over. Shaped from a
// live frame: painted rows open in the canvas margin, boxes ride on top, the
// untouched interior abstains, and nothing is blank.
func TestCanvasBackgroundAcceptsModelSerializedCanvasWithoutBlankRows(t *testing.T) {
	canvas := "\x1b[48;2;10;10;10m"
	box := "\x1b[48;2;20;20;20m"
	var lines []string
	for range 8 {
		lines = append(lines, canvas+"  │"+box+"  message text  \x1b[m")
	}
	lines = append(lines, canvas+"  Thought: 7.4s\x1b[m")
	for range 30 {
		lines = append(lines, "")
	}
	for range 4 {
		lines = append(lines, canvas+"  │"+box+"  input  \x1b[m")
	}
	buffer := canvasBuffer(t, lines, len(lines))
	if got := CanvasBackground(buffer, 0, len(lines)); got != canvas {
		t.Fatalf("canvas = %q, want %q from model-serialized rows with no blank canvas rows", got, canvas)
	}
}

// A second background beside the candidate is not the same as one on top of
// it: a mostly-added diff has red deletion rows next to its green rows, and
// with col-0 green and no blank rows that shape reached every other gate. The
// fallback demands same-row co-occurrence, which line-level highlighting
// never has.
func TestNoBlankRowFallbackRejectsMixedDiffHighlight(t *testing.T) {
	green := "\x1b[48;2;0;80;0m"
	red := "\x1b[48;2;80;0;0m"
	var lines []string
	for range 10 {
		lines = append(lines, green+"+ added line\x1b[49m")
	}
	lines = append(lines, red+"- removed line\x1b[49m")
	buffer := canvasBuffer(t, lines, len(lines))
	if got := CanvasBackground(buffer, 0, len(lines)); got != "" {
		t.Fatalf("mostly-green diff with a deletion row promoted to canvas %q", got)
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

// An inset block passes the blank-row test a highlight fails: Cursor's CLI
// draws the user's message as a three-row bubble whose top and bottom rows are
// blank padding, indented one column from the pane edge. In a transcript that
// paints nothing else but the input field, the bubble covered three of the four
// painted rows and became the pane's canvas — and gave it back a moment later
// when streamed output pushed its last row out of the live grid. Shaped from a
// live cursor-agent 2026.08.11 capture at 120x40, abridged in both dimensions;
// the transcript rows carry no background, so a taller grid leaves the four
// painted rows the detector votes over unchanged.
func TestCanvasBackgroundRejectsAnInsetMessageBubble(t *testing.T) {
	bubble := "\x1b[48;2;36;36;40m"
	field := "\x1b[48;2;21;21;21m"
	pad := strings.Repeat(" ", 100)
	lines := []string{
		"",
		" \x1b[2mCounting to forty, one sentence each.\x1b[0m",
		"",
		// The bubble: indented by one unpainted column, blank padding rows
		// above and below the message.
		" " + bubble + pad + "\x1b[49m",
		" " + bubble + " Count slowly from 1 to 40." + pad + "\x1b[49m",
		" " + bubble + pad + "\x1b[49m",
		"",
		" \x1b[2m1.\x1b[0m One is the first counting number.",
		"",
		" \x1b[2m2.\x1b[0m Two is the only even prime.",
		"",
		" \x1b[32m⠙\x1b[39m \x1b[1mComposing\x1b[0m",
		// The input box: half-block borders drawn as foreground glyphs, then
		// the field itself.
		" \x1b[38;2;21;21;21m" + strings.Repeat("▄", 100) + "\x1b[39m",
		" " + field + " → Add a follow-up" + pad + "\x1b[49m",
		" \x1b[38;2;21;21;21m" + strings.Repeat("▀", 100) + "\x1b[39m",
		"  \x1b[2mComposer 2.5\x1b[0m",
	}
	buffer := canvasBuffer(t, lines, len(lines))
	if got := CanvasBackground(buffer, 0, len(lines)); got != "" {
		t.Fatalf("inset message bubble promoted to canvas %q", got)
	}
}

// Codex's composer is a full-width four-row band: two painted blank rows, the
// prompt row, and a footer row that inherits the same background. A one-column
// resize can wrap one transcript line across the live-grid edge, changing the
// composer from four of six painted rows to four of five. Four of five reaches
// CanvasRowShare even though the band occupies only the bottom of the pane.
// Both adjacent shapes must abstain so child-default transcript cells keep the
// host terminal background at every width.
func TestCanvasBackgroundRejectsCodexComposerAcrossAdjacentWrapBoundaries(t *testing.T) {
	host := "\x1b[48;2;40;43;51m"
	composer := "\x1b[48;2;30;30;30m"
	diff := "\x1b[48;2;33;58;43m"
	for _, extraPaintedRows := range []int{1, 2} {
		t.Run(fmt.Sprintf("four_of_%d_painted", 4+extraPaintedRows), func(t *testing.T) {
			lines := []string{diff + "+ changed line\x1b[49m"}
			if extraPaintedRows == 2 {
				lines = append(lines, diff+"+ wrapped continuation\x1b[49m")
			}
			for range 43 - len(lines) {
				lines = append(lines, "default-background transcript")
			}
			lines = append(lines,
				composer+strings.Repeat(" ", 80)+"\x1b[49m",
				composer+"› Ask Codex to do anything\x1b[49m",
				composer+strings.Repeat(" ", 80)+"\x1b[49m",
				composer+"gpt-5.6-sol xhigh\x1b[49m",
			)
			buffer := canvasBuffer(t, lines, len(lines))
			if got := CanvasBackground(buffer, 0, len(lines)); got != "" {
				t.Fatalf("localized Codex composer promoted to canvas %q", got)
			}
			layout := tty.FitViewport(tty.ViewportInput{
				Buffer: buffer, Width: 100, Height: len(lines), Follow: true,
				Interactive: true, PaneWidth: 100, PaneHeight: len(lines),
			})
			draw := DrawRows(RowsInput{
				Buffer: buffer, Layout: layout, DefaultBackground: host,
				PaneHeight: len(lines), Interactive: true, Follow: true,
			})
			if draw.CanvasBackground != host {
				t.Fatalf("resolved background = %q, want host %q", draw.CanvasBackground, host)
			}
		})
	}
}
