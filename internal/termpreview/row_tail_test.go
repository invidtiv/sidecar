package termpreview

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/tty/screenmodel"
)

// tmux trims each row's trailing blank cells out of `capture-pane -e` but keeps
// the SGR change that applied to them, so the row's own trailing background is
// a fact about those cells rather than something to infer. `capture-pane -e -N`
// is the same pane with the blanks left in, which makes it the oracle for what
// the trimmed capture means.
//
// The fixtures are real Codex renderings of one file edit at three widths,
// recorded off an isolated tmux server. Diff highlighting is where this shows
// up first: a `-` or `+` row paints to the pane edge, and every cell past the
// text is a cell the capture describes only by leaving its colour open.
func TestDrawRowsMatchesPaddedCaptureBackgrounds(t *testing.T) {
	for _, width := range []int{85, 100, 121} {
		padded := readCaptureFixture(t, fmt.Sprintf("codex-edit-w%d.pad", width))
		// What production reads: the -N capture is both the input and the
		// oracle, so any disagreement is a screen Sidecar was handed intact and
		// then distorted.
		t.Run(fmt.Sprintf("padded/w%d", width), func(t *testing.T) {
			assertBackgroundsMatch(t, padded, padded, width)
		})
		// The trimmed form of the same screen still has to reach the same
		// answer, because the reconstruction is what makes a row shorter than
		// its pane come out right after truncation or a horizontal offset.
		t.Run(fmt.Sprintf("trimmed/w%d", width), func(t *testing.T) {
			assertBackgroundsMatch(t, readCaptureFixture(t, fmt.Sprintf("codex-edit-w%d.trim", width)), padded, width)
		})
	}
}

func assertBackgroundsMatch(t *testing.T, input, oracle []string, width int) {
	t.Helper()
	height := len(oracle)
	res := drawCapture(t, input, width, height)
	if len(res.Rows) != height {
		t.Fatalf("drew %d rows, oracle has %d", len(res.Rows), height)
	}
	want := screenmodel.DecodeCapture(strings.Join(oracle, "\n"), width, height)
	got := screenmodel.DecodeCapture(strings.Join(res.Rows, "\n"), width, height)

	var bg []screenmodel.Mismatch
	for _, m := range screenmodel.CompareGrids(want, got, width, height) {
		if m.Field == "bg" {
			bg = append(bg, m)
		}
	}
	if len(bg) == 0 {
		return
	}
	t.Errorf("%d background cells disagree with tmux at width %d", len(bg), width)
	for i, m := range bg {
		if i == 8 {
			t.Logf("... and %d more", len(bg)-i)
			break
		}
		t.Log(m.String())
	}
}

// A row the capture spells as one SGR change and nothing else is a described
// row: tmux trimmed its blanks but said what colour they were. Codex's composer
// is made of them, and dropping them leaves a default-coloured stripe through
// the input box.
func TestDrawRowsFillsRowsThatAreOnlyABackgroundChange(t *testing.T) {
	const composer = "\x1b[48;2;30;30;30m"
	composerBg := screenmodel.RGBColor(30, 30, 30)
	rows := transcript(16)
	rows = append(rows,
		composer,
		composer+"> ask me anything\x1b[0m"+composer,
		composer,
		"\x1b[49mstatus line",
	)
	res := drawCapture(t, rows, 40, len(rows))
	if res.CanvasBackground != "" {
		t.Fatalf("fixture was read as a full-pane canvas %q, so it no longer isolates the tail rule", res.CanvasBackground)
	}
	for _, row := range []int{16, 18} {
		if bg := backgroundAt(res.Rows[row], 39); bg != composerBg {
			t.Errorf("row %d column 39 = %q, want the composer background %q", row, bg, composerBg)
		}
	}
	if bg := backgroundAt(res.Rows[19], 39); bg == composerBg {
		t.Errorf("the status line took the composer background past its text")
	}
}

// The row above closes its own background, so the blank row under it is not
// part of anything and must not inherit. This is the separator between two
// coloured blocks in a transcript, which used to come out painted.
func TestDrawRowsLeavesUndescribedBlankRowsAlone(t *testing.T) {
	const green = "\x1b[48;2;33;58;43m"
	greenBg := screenmodel.RGBColor(33, 58, 43)
	rows := transcript(16)
	rows = append(rows, green+"+ added line", "", "following prose")
	res := drawCapture(t, rows, 40, len(rows))
	if res.CanvasBackground != "" {
		t.Fatalf("fixture was read as a full-pane canvas %q, so it no longer isolates the tail rule", res.CanvasBackground)
	}
	if bg := backgroundAt(res.Rows[16], 39); bg != greenBg {
		t.Errorf("the added line stops painting at its text: column 39 = %q, want %q", bg, greenBg)
	}
	if bg := backgroundAt(res.Rows[17], 0); bg == greenBg {
		t.Errorf("the blank separator row inherited the diff background")
	}
}

// Suppressing backgrounds has to suppress the reconstructed cells too, or the
// mode that exists to stop a wall of colour draws the wall itself.
func TestDrawRowsDoesNotFillTailsWhenBackgroundsAreOff(t *testing.T) {
	const green = "\x1b[48;2;33;58;43m"
	buffer := canvasBuffer(t, []string{green + "+ added line"}, 1)
	layout := tty.FitViewport(tty.ViewportInput{
		Buffer: buffer, Width: 40, Height: 1, Follow: true,
		Interactive: true, PaneWidth: 40, PaneHeight: 1,
	})
	res := DrawRows(RowsInput{
		Buffer: buffer, Layout: layout, PaneHeight: 1,
		Interactive: true, Follow: true, Pad: true,
		Backgrounds: tty.BackgroundNever,
	})
	if strings.Contains(res.Rows[0], green) {
		t.Errorf("BackgroundNever still painted the row tail: %q", res.Rows[0])
	}
}

// transcript is n rows of unpainted prose: the surroundings that keep a small
// coloured block from being read as the pane's canvas.
func transcript(n int) []string {
	rows := make([]string, 0, n)
	for i := range n {
		rows = append(rows, fmt.Sprintf("transcript line %d", i))
	}
	return rows
}

func drawCapture(t *testing.T, rows []string, width, height int) DrawResult {
	t.Helper()
	buffer := canvasBuffer(t, rows, len(rows))
	layout := tty.FitViewport(tty.ViewportInput{
		Buffer: buffer, Width: width, Height: height, Follow: true,
		Interactive: true, PaneWidth: width, PaneHeight: len(rows),
	})
	return DrawRows(RowsInput{
		Buffer: buffer, Layout: layout, PaneHeight: len(rows),
		Interactive: true, Follow: true, Pad: true,
	})
}

func readCaptureFixture(t *testing.T, name string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "capture", name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
}

// backgroundAt reports the canonical background at a drawn row's column.
func backgroundAt(line string, col int) screenmodel.Color {
	grid := screenmodel.DecodeCapture(line, col+1, 1)
	return grid.Normalize(col+1, 1)[0][col].Bg
}
