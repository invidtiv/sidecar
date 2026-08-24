package termpreview

import (
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/tty"
)

// piWall is the failure the bounded mode exists for: an application that
// quantizes its theme to 256 colors inside tmux (pi's toolSuccessBg #283228
// becomes index 22) and then paints nearly every row of a session's output
// with it. Each block closes its own background with 0m, so no chain runs
// between rows — the wall is per-row painting.
var piGreen = "\x1b[48;5;22m"

func piWall(rows int) []string {
	lines := make([]string, 0, rows)
	for i := range rows {
		lines = append(lines, piGreen+" tool output line "+strings.Repeat("x", 5)+" \x1b[0m")
		_ = i
	}
	return lines
}

func modeViewport(t *testing.T, buffer *tty.OutputBuffer) tty.Viewport {
	t.Helper()
	return tty.FitViewport(tty.ViewportInput{
		Buffer: buffer, Width: 40, Height: len(buffer.Lines()) + 4, Follow: true,
		Interactive: true, PaneWidth: 40, PaneHeight: len(buffer.Lines()) + 4,
		Scrollbar: true,
	})
}

func drawWithMode(t *testing.T, lines []string, mode tty.BackgroundMode, spanMax int) []string {
	t.Helper()
	buffer := canvasBuffer(t, lines, len(lines)+2)
	layout := modeViewport(t, buffer)
	return DrawRows(RowsInput{
		Buffer:            buffer,
		Layout:            layout,
		AbsoluteBase:      0,
		PaneHeight:        layout.DisplayHeight,
		Interactive:       true,
		Follow:            true,
		Backgrounds:       mode,
		BackgroundSpanMax: spanMax,
	}).Rows
}

func TestDrawRowsBoundedKeepsShortSpans(t *testing.T) {
	drawn := drawWithMode(t, piWall(6), tty.BackgroundBounded, 0)
	painted := 0
	for _, line := range drawn {
		if strings.Contains(line, piGreen) {
			painted++
		}
	}
	if painted != 6 {
		t.Fatalf("a 6-row wall under the default cap kept %d of 6 painted rows", painted)
	}
}

func TestDrawRowsBoundedDropsLongRunPastTheCap(t *testing.T) {
	const cap = 8
	drawn := drawWithMode(t, piWall(20), tty.BackgroundBounded, cap)
	kept, stripped := 0, 0
	for _, line := range drawn {
		switch {
		case strings.Contains(line, piGreen):
			kept++
		case strings.Contains(line, "tool output"):
			stripped++
		}
	}
	if kept != cap {
		t.Fatalf("cap=%d kept %d painted rows, want exactly the first %d", cap, kept, cap)
	}
	if stripped != 20-cap {
		t.Fatalf("stripped %d rows past the cap, want %d", stripped, 20-cap)
	}
}

func TestDrawRowsBoundedCountsPaintedRowsAboveScrolledViewport(t *testing.T) {
	const cap = 8
	lines := piWall(20)
	buffer := canvasBuffer(t, lines, len(lines))

	for _, tc := range []struct {
		name        string
		start       int
		wantPainted int
	}{
		{name: "cap partly consumed above viewport", start: 6, wantPainted: 2},
		{name: "cap already consumed above viewport", start: 10, wantPainted: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			layout := tty.Viewport{
				Start: tc.start, End: len(lines), EffectiveCount: len(lines) - tc.start,
				DisplayWidth: 40, DisplayHeight: len(lines) - tc.start,
			}
			drawn := DrawRows(RowsInput{
				Buffer: buffer, Layout: layout,
				Backgrounds: tty.BackgroundBounded, BackgroundSpanMax: cap,
			}).Rows
			painted, stripped := 0, 0
			for _, line := range drawn {
				switch {
				case strings.Contains(line, piGreen):
					painted++
				case strings.Contains(line, "tool output"):
					stripped++
				}
			}
			if painted != tc.wantPainted {
				t.Fatalf("painted %d visible rows, want %d after %d predecessor rows", painted, tc.wantPainted, tc.start)
			}
			if stripped != len(drawn)-tc.wantPainted {
				t.Fatalf("stripped %d visible rows, want %d", stripped, len(drawn)-tc.wantPainted)
			}
		})
	}
}

func TestDrawRowsBoundedCountsInheritedCarryAboveScrolledViewport(t *testing.T) {
	const cap = 8
	lines := []string{piGreen + "band begins above viewport"}
	for range 19 {
		lines = append(lines, "carried row")
	}
	buffer := canvasBuffer(t, lines, len(lines))
	layout := tty.Viewport{
		Start: 10, End: len(lines), EffectiveCount: len(lines) - 10,
		DisplayWidth: 40, DisplayHeight: len(lines) - 10,
	}
	drawn := DrawRows(RowsInput{
		Buffer: buffer, Layout: layout,
		Backgrounds: tty.BackgroundBounded, BackgroundSpanMax: cap,
	}).Rows
	for i, line := range drawn {
		if strings.Contains(line, piGreen) {
			t.Fatalf("visible row %d reopened an inherited band whose cap was consumed above the viewport: %q", i, line)
		}
		if !strings.Contains(line, "carried row") {
			t.Fatalf("visible row %d lost content while stripping inherited background: %q", i, line)
		}
	}
}

func TestDrawRowsBoundedRestartsAfterAnUnpaintedRow(t *testing.T) {
	// Two 8-row walls separated by an unpainted blank row are two short spans,
	// not one long one: each keeps its background.
	var lines []string
	for range 2 {
		for i := range 8 {
			lines = append(lines, piGreen+" block row "+string(rune('a'+i))+" \x1b[0m")
		}
		lines = append(lines, "")
	}
	drawn := drawWithMode(t, lines, tty.BackgroundBounded, 12)
	painted := 0
	for _, line := range drawn {
		if strings.Contains(line, piGreen) {
			painted++
		}
	}
	if painted != 16 {
		t.Fatalf("two 8-row walls painted %d of 16 rows; the blank row must reset the band count", painted)
	}
}

func TestDrawRowsNeverStripsEveryBackground(t *testing.T) {
	lines := append(piWall(5), canvasBlack+"canvas row\x1b[49m")
	drawn := drawWithMode(t, lines, tty.BackgroundNever, 0)
	for i, line := range drawn {
		if strings.Contains(line, "\x1b[48;") || strings.Contains(line, "\x1b[41m") {
			t.Fatalf("row %d still carries a background under never: %q", i, line)
		}
	}
	if !strings.Contains(strings.Join(drawn, "\n"), "tool output") ||
		!strings.Contains(strings.Join(drawn, "\n"), "canvas row") {
		t.Fatalf("never mode lost text: %q", drawn)
	}
}

func TestDrawRowsAutoStillFillsTheCanvas(t *testing.T) {
	// Auto is the historical behavior; the new modes must not have moved it.
	lines := []string{
		canvasBlack + "header\x1b[0m",
		canvasBlack + "body\x1b[0m",
		canvasBlack + "   \x1b[0m",
		canvasBlack + "status\x1b[0m",
	}
	buffer := canvasBuffer(t, lines, 10)
	layout := tty.FitViewport(tty.ViewportInput{
		Buffer: buffer, Width: 30, Height: 10, Follow: true,
		Interactive: true, PaneWidth: 12, PaneHeight: 10, Scrollbar: true,
	})
	drawn := DrawRows(RowsInput{
		Buffer: buffer, Layout: layout, AbsoluteBase: 0,
		PaneHeight: 10, Interactive: true, Follow: true,
	}).Rows
	filled := 0
	for _, line := range drawn {
		if strings.Contains(line, canvasBlack) || strings.Contains(line, "48;2;20;20;20") {
			filled++
		}
	}
	if filled == 0 {
		t.Fatalf("auto mode stopped filling default cells with the detected canvas")
	}
}

func TestRenderBodyBoundedSkipsCanvasDetection(t *testing.T) {
	// A 14-row wall renders as its capped prefix under bounded (12 painted
	// rows), while auto may keep every painted row plus a canvas fill.
	lines := piWall(14)
	buffer := canvasBuffer(t, lines, len(lines))
	layout := modeViewport(t, buffer)
	height := layout.DisplayHeight + HeaderRows
	countPainted := func(body string) int {
		n := 0
		for _, line := range strings.Split(body, "\n") {
			if strings.Contains(line, piGreen) {
				n++
			}
		}
		return n
	}
	bounded := RenderBody(RenderBufferInput{
		Width: 40, Height: height,
		Layout: layout, Buffer: buffer,
		PaneHeight: layout.DisplayHeight, Interactive: true, Follow: true,
		Backgrounds:       tty.BackgroundBounded,
		BackgroundSpanMax: 12,
	})
	if n := countPainted(bounded); n != 12 {
		t.Fatalf("bounded body painted %d rows, want exactly the 12-row cap", n)
	}
	auto := RenderBody(RenderBufferInput{
		Width: 40, Height: height,
		Layout: layout, Buffer: buffer,
		PaneHeight: layout.DisplayHeight, Interactive: true, Follow: true,
	})
	if n := countPainted(auto); n <= 12 {
		t.Fatalf("auto body painted %d rows; detection or fill should exceed the cap on this fixture", n)
	}
}
