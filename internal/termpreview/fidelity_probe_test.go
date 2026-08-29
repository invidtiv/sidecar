package termpreview

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/tty/screenmodel"
)

// TestFidelityProbeLive is an opt-in diagnostic, not a regression test. It
// answers one question about a real pane: does Sidecar draw the screen tmux
// says is there?
//
//	FIDELITY_PROBE_TARGET=%42 go test ./internal/termpreview -run FidelityProbeLive -v
//	FIDELITY_PROBE_DIR=/path/to/captures go test ./internal/termpreview -run FidelityProbeLive -v
//
// The oracle is `capture-pane -e -N` of the same pane, which is tmux's own
// answer for every cell including the trailing blanks the trimmed form drops.
// Both sides are decoded to cell grids and compared, so a disagreement names
// the row and column rather than a difference in escape-byte spelling.
//
// A directory holds `<name>.trim` / `<name>.pad` pairs, as written by
// scripts/terminal-fidelity.sh, so several widths can be swept at once. The
// live mode never sends input and never resizes its target, so it is safe on a
// session whose current geometry is the evidence.
func TestFidelityProbeLive(t *testing.T) {
	captures := probeCaptures(t)
	if len(captures) == 0 {
		t.Skip("set FIDELITY_PROBE_TARGET or FIDELITY_PROBE_DIR")
	}
	requireClean := os.Getenv("FIDELITY_PROBE_REQUIRE_CLEAN") == "1"

	for _, capture := range captures {
		padded := splitCaptureRows(capture.padded)
		width, height := captureWidth(padded), len(padded)
		if width == 0 || height == 0 {
			t.Logf("=== %s: empty capture", capture.name)
			continue
		}
		// The -N capture is what production consumes, so it is the subject as
		// well as the oracle: any disagreement is a screen Sidecar was handed
		// intact and then distorted. This is the leg that gates.
		total := probeOne(t, capture.name+"/padded", padded, padded, width, height)
		if requireClean && total > 0 {
			t.Errorf("%s: %d background cells disagree with tmux", capture.name, total)
		}
		// The trimmed form is reported but never gates. A wholly blank row is
		// the same empty string there whether its cells carry a colour or the
		// terminal default, so this leg is expected to differ on exactly those
		// rows; that ambiguity is why captures are taken with -N at all. What it
		// is good for is showing the reconstruction still handles every row the
		// capture does describe.
		probeOne(t, capture.name+"/trimmed", splitCaptureRows(capture.trimmed), padded, width, height)
	}
}

func probeOne(t *testing.T, name string, input, oracle []string, width, height int) int {
	t.Helper()
	buffer := tty.NewOutputBuffer(len(input) + 10)
	buffer.ApplySnapshot(tty.PaneSnapshot{Output: strings.Join(input, "\n"), PaneRows: len(input)})
	layout := tty.FitViewport(tty.ViewportInput{
		Buffer: buffer, Width: width, Height: height, Follow: true,
		Interactive: true, PaneWidth: width, PaneHeight: len(input),
	})
	res := DrawRows(RowsInput{
		Buffer: buffer, Layout: layout, PaneHeight: len(input),
		Interactive: true, Follow: true, Pad: true,
	})

	// Both sides are one continuous SGR stream, so a row's opening state is
	// whatever the row above left behind and they have to decode together.
	want := screenmodel.DecodeCapture(strings.Join(oracle, "\n"), width, height)
	got := screenmodel.DecodeCapture(strings.Join(res.Rows, "\n"), width, height)
	perRow := map[int]int{}
	sample := map[int]screenmodel.Mismatch{}
	total := 0
	for _, m := range screenmodel.CompareGrids(want, got, width, height) {
		if m.Field != "bg" {
			continue
		}
		if perRow[m.Row] == 0 {
			sample[m.Row] = m
		}
		perRow[m.Row]++
		total++
	}
	t.Logf("=== %s: %dx%d, %d background cells disagree across %d rows",
		name, width, height, total, len(perRow))
	for row := range height {
		if perRow[row] == 0 {
			continue
		}
		t.Logf("row %d: %d cells, first %s", row, perRow[row], sample[row].String())
		t.Logf("   tmux %q", oracle[row])
		t.Logf("   drew %q", res.Rows[row])
	}
	return total
}

type probeCapture struct {
	name            string
	trimmed, padded string
}

func probeCaptures(t *testing.T) []probeCapture {
	t.Helper()
	var out []probeCapture
	if dir := os.Getenv("FIDELITY_PROBE_DIR"); dir != "" {
		pads, _ := filepath.Glob(filepath.Join(dir, "*.pad"))
		for _, pad := range pads {
			trim := strings.TrimSuffix(pad, ".pad") + ".trim"
			padded, err := os.ReadFile(pad)
			if err != nil {
				t.Fatal(err)
			}
			trimmed, err := os.ReadFile(trim)
			if err != nil {
				t.Fatalf("%s has no matching .trim: %v", filepath.Base(pad), err)
			}
			out = append(out, probeCapture{
				name:    strings.TrimSuffix(filepath.Base(pad), ".pad"),
				trimmed: string(trimmed),
				padded:  string(padded),
			})
		}
	}
	if target := os.Getenv("FIDELITY_PROBE_TARGET"); target != "" {
		capture := func(extra ...string) string {
			args := append([]string{"capture-pane", "-p", "-e"}, extra...)
			raw, err := exec.Command("tmux", append(args, "-t", target)...).Output()
			if err != nil {
				t.Fatalf("capture live tmux target %q: %v", target, err)
			}
			return string(raw)
		}
		out = append(out, probeCapture{
			name:    "tmux:" + target,
			trimmed: capture(),
			padded:  capture("-N"),
		})
	}
	return out
}

func splitCaptureRows(content string) []string {
	content = strings.TrimSuffix(content, "\n")
	if content == "" {
		return nil
	}
	return strings.Split(content, "\n")
}

// captureWidth is the pane width a -N capture implies: its widest row, since
// -N pads every row it describes out to the cells the pane actually holds.
func captureWidth(padded []string) int {
	width := 0
	for _, row := range padded {
		width = max(width, ansi.StringWidth(row))
	}
	return width
}
