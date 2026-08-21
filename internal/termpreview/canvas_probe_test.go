package termpreview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
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

		inherited := ""
		counts := map[string]int{}
		blanks := map[string]int{}
		painted := 0
		for i, row := range buffer.LinesRange(0, len(lines)) {
			text, next, _ := ui.CarryRowBackground(row, inherited)
			inherited = next
			bgs := rowBackgrounds(text)
			blank := strings.TrimSpace(ansi.Strip(text)) == ""
			if len(bgs) > 0 {
				painted++
				for bg := range bgs {
					counts[bg]++
					if blank {
						blanks[bg]++
					}
				}
			}
			keys := make([]string, 0, len(bgs))
			for bg := range bgs {
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
