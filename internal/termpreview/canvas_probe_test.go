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

type canvasProbeCapture struct {
	name    string
	content string
}

// TestCanvasProbeLiveCaptures is an opt-in diagnostic, not a regression test.
// It reads saved `tmux capture-pane -e` dumps or captures one live target
// read-only, then reports what the canvas detector sees before and after the
// screen-model seam:
//
//	CANVAS_PROBE_DIR=/path/to/captures go test ./internal/termpreview -run CanvasProbeLive -v
//	CANVAS_PROBE_TARGET=%42 go test ./internal/termpreview -run CanvasProbeLive -v
//
// A directory may contain captures from several widths or TUIs. The live mode
// never sends input or resizes the target, so it is safe to use on a session
// whose current geometry is the evidence under investigation.
func TestCanvasProbeLiveCaptures(t *testing.T) {
	var captures []canvasProbeCapture
	dir := os.Getenv("CANVAS_PROBE_DIR")
	if dir != "" {
		files, _ := filepath.Glob(filepath.Join(dir, "cap-*.txt"))
		for _, file := range files {
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			captures = append(captures, canvasProbeCapture{name: filepath.Base(file), content: string(raw)})
		}
	}
	if target := os.Getenv("CANVAS_PROBE_TARGET"); target != "" {
		raw, err := exec.Command("tmux", "capture-pane", "-p", "-e", "-t", target).Output()
		if err != nil {
			t.Fatalf("capture live tmux target %q: %v", target, err)
		}
		captures = append(captures, canvasProbeCapture{name: "tmux:" + target, content: string(raw)})
	}
	if len(captures) == 0 {
		t.Skip("set CANVAS_PROBE_DIR or CANVAS_PROBE_TARGET")
	}

	requireStable := os.Getenv("CANVAS_PROBE_REQUIRE_STABLE") == "1"
	baselineName, baselineCanvas := "", ""
	haveBaseline := false
	for _, capture := range captures {
		content := strings.TrimSuffix(capture.content, "\n")
		lines := strings.Split(content, "\n")
		buffer := tty.NewOutputBuffer(len(lines) + 10)
		buffer.ApplySnapshot(tty.PaneSnapshot{Output: content, PaneRows: len(lines)})

		got := CanvasBackground(buffer, 0, len(lines))
		t.Logf("=== %s: rows=%d canvas=%q", capture.name, len(lines), got)

		// The live interactive path replays the capture through the screen
		// model and renders frames from the emulator, so detection must also
		// hold on that serialization, not just on raw capture-pane output.
		width := 0
		for _, l := range lines {
			if w := ansi.StringWidth(l); w > width {
				width = w
			}
		}
		model := screenmodel.New(width, len(lines))
		if err := model.Seed(screenmodel.Seed{Output: content, Width: width, Height: len(lines)}); err != nil {
			t.Fatalf("seed: %v", err)
		}
		frame, err := model.Frame()
		if err != nil {
			t.Fatalf("frame: %v", err)
		}
		fbuf := tty.NewOutputBuffer(len(lines) + 10)
		fbuf.ApplySnapshot(tty.PaneSnapshot{Output: frame.Output, PaneRows: len(lines)})
		fgot := CanvasBackground(fbuf, 0, len(lines))
		t.Logf("    via screenmodel: canvas=%q", fgot)
		if requireStable {
			if !haveBaseline {
				baselineName, baselineCanvas = capture.name, fgot
				haveBaseline = true
			} else if fgot != baselineCanvas {
				t.Errorf("screen-model canvas changed across adjacent captures: %s=%q, %s=%q",
					baselineName, baselineCanvas, capture.name, fgot)
			}
		}
		if fgot != got {
			t.Logf("    MISMATCH raw=%q model=%q", got, fgot)
		}

		inherited := ""
		counts := map[string]int{}
		blanks := map[string]int{}
		firsts := map[string]int{}
		painted := 0
		short := func(bg string) string {
			return strings.TrimPrefix(strings.TrimSuffix(bg, "m"), "\x1b[")
		}
		for i, row := range buffer.LinesRange(0, len(lines)) {
			resolved := analyzeRawRow(row, 0).resolve(inherited)
			inherited = resolved.trailing
			blank := resolved.blank
			if len(resolved.backgrounds) > 0 {
				painted++
				if resolved.first != "" {
					firsts[resolved.first]++
				}
				for _, bg := range resolved.backgrounds {
					counts[bg]++
					if blank {
						blanks[bg]++
					}
				}
			}
			keys := make([]string, 0, len(resolved.backgrounds))
			for _, bg := range resolved.backgrounds {
				keys = append(keys, short(bg))
			}
			// first is what the row opens in, and a candidate that does not own
			// the row starts is rejected however well it covers them.
			t.Logf("row %2d blank=%v first=%q bgs=%v", i, blank, short(resolved.first), keys)
		}
		t.Logf("painted=%d share-bar=%d row-start-bar=%d", painted, CanvasRowShare(painted), painted/2+1)
		for bg, n := range counts {
			t.Logf("count %q = %d (blank rows %d, row starts %d)", short(bg), n, blanks[bg], firsts[bg])
		}
	}
}
