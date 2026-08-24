package tty

import (
	"fmt"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/tty/screenmodel"
)

// The decision gate asks whether replay work and allocations scale with the
// byte delta and the current grid rather than with the 600-line capture window.
// These benchmarks isolate the two per-burst paths inside Sidecar so that
// question has a number instead of an argument.
//
// What they do NOT measure, and what no in-process benchmark can measure:
// tmux's own cost of rendering and serialising a 624-line capture, and the
// round trip for it. The capture path's
// real cost is therefore *under*-stated here, and the model path's advantage is
// correspondingly understated. The end-to-end latency comparison in the soak
// test is the measurement that includes both sides.

// benchCapture builds a capture-pane-shaped payload: `history` scrolled-off
// lines plus `height` visible rows, lightly styled, as the capture path
// receives it.
func benchCapture(history, height, width int) []string {
	lines := make([]string, 0, history+height+1)
	lines = append(lines, "10,5,1,24,80,600,0,zsh,title")
	body := strings.Repeat("x", width-20)
	for i := range history + height {
		lines = append(lines, fmt.Sprintf("\x1b[3%dmline %05d\x1b[0m %s", i%8, i, body))
	}
	return lines
}

// BenchmarkCapturePathPerBurst is the work the capture path does in Sidecar for
// one output burst: parse a full 600-line capture response into a snapshot.
func BenchmarkCapturePathPerBurst(b *testing.B) {
	lines := benchCapture(600, 24, 80)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := parseControlSnapshot("s", "%1", 600, lines); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkModelPathWritePerBurst is the work the byte-fed path does for one
// output burst of a realistic size: writing the delta bytes into the model.
func BenchmarkModelPathWritePerBurst(b *testing.B) {
	for _, size := range []int{64, 512, 4096} {
		b.Run(fmt.Sprint(size, "B"), func(b *testing.B) {
			model := screenmodel.New(80, 24)
			defer model.Close()
			payload := []byte(strings.Repeat("abcdefgh\r\n", size/10))
			b.ReportAllocs()
			for b.Loop() {
				if err := model.Write(payload); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkModelPathRenderPerFrame is the other half: rendering a frame. Loaded
// history is an immutable view maintained incrementally by Write, so the 600-row
// case must cost the same order as the zero-history case: current-grid work,
// not a second rendering of the capture window.
func BenchmarkModelPathRenderPerFrame(b *testing.B) {
	for _, history := range []int{0, 600} {
		b.Run(fmt.Sprint("history=", history), func(b *testing.B) {
			model := screenmodel.New(80, 24)
			defer model.Close()
			var payload strings.Builder
			for i := range history + 24 {
				fmt.Fprintf(&payload, "\x1b[3%dmline %05d\x1b[0m %s\r\n", i%8, i, strings.Repeat("x", 40))
			}
			if err := model.Write([]byte(payload.String())); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := model.Frame(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkShadowComparePerCapture measures the diagnostic itself, so its cost
// is never mistaken for the model path's cost.
func BenchmarkShadowComparePerCapture(b *testing.B) {
	model := screenmodel.New(80, 24)
	defer model.Close()
	var payload strings.Builder
	for i := range 24 {
		fmt.Fprintf(&payload, "\x1b[3%dmline %05d\x1b[0m %s\r\n", i%8, i, strings.Repeat("x", 40))
	}
	if err := model.Write([]byte(payload.String())); err != nil {
		b.Fatal(err)
	}
	frame, err := model.DiagnosticFrame()
	if err != nil {
		b.Fatal(err)
	}
	in := screenCompareInput{
		CaptureOutput: frame.CombinedOutput(), Width: 80, Height: 24,
		CursorRow: frame.CursorRow, CursorCol: frame.CursorCol,
		CursorVisible: frame.CursorVisible, HistorySize: frame.HistorySize,
		CursorTrustworthy: true,
	}
	b.ReportAllocs()
	for b.Loop() {
		compareCaptureWithModel(in, frame)
	}
}
