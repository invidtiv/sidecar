package termpreview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/tty/screenmodel"
)

// TestCanvasProbeLiveCaptures is a diagnostic, not a regression test. It reads
// raw `tmux capture-pane -e` dumps from the scratchpad and reports what the
// canvas detector sees for each. Run with -run CanvasProbeLive -v.
func TestCanvasProbeLiveCaptures(t *testing.T) {
	dir := os.Getenv("CANVAS_PROBE_DIR")
	if dir == "" {
		t.Skip("set CANVAS_PROBE_DIR to a directory of capture files")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "cap-*.txt"))
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		content := strings.TrimSuffix(string(raw), "\n")
		lines := strings.Split(content, "\n")
		buffer := tty.NewOutputBuffer(len(lines) + 10)
		buffer.ApplySnapshot(tty.PaneSnapshot{Output: content, PaneRows: len(lines)})

		got := CanvasBackground(buffer, 0, len(lines))
		t.Logf("=== %s: rows=%d canvas=%q", filepath.Base(f), len(lines), got)

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
		if fgot != got {
			t.Logf("    MISMATCH raw=%q model=%q", got, fgot)
			_ = os.WriteFile(f+".frame", []byte(frame.Output), 0o644)
		}

		inherited := ""
		counts := map[string]int{}
		blanks := map[string]int{}
		painted := 0
		for i, row := range buffer.LinesRange(0, len(lines)) {
			resolved := analyzeRawRow(row, 0).resolve(inherited)
			inherited = resolved.trailing
			blank := resolved.blank
			if len(resolved.backgrounds) > 0 {
				painted++
				for _, bg := range resolved.backgrounds {
					counts[bg]++
					if blank {
						blanks[bg]++
					}
				}
			}
			keys := make([]string, 0, len(resolved.backgrounds))
			for _, bg := range resolved.backgrounds {
				keys = append(keys, strings.TrimPrefix(strings.TrimSuffix(bg, "m"), "\x1b["))
			}
			t.Logf("row %2d blank=%v bgs=%v", i, blank, keys)
		}
		t.Logf("painted=%d share-bar=%d", painted, CanvasRowShare(painted))
		for bg, n := range counts {
			t.Logf("count %q = %d (blank rows %d)", strings.TrimPrefix(strings.TrimSuffix(bg, "m"), "\x1b["), n, blanks[bg])
		}
	}
}
