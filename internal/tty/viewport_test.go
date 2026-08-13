package tty

import (
	"strings"
	"testing"
)

// The first notch back from the live edge is worth exactly one scroll step, on
// every geometry (td-bbbbfe).
//
// Offset 0 used to be placed by the untrimmed live grid while offset 1 was
// placed by the trimmed count, so the first notch jumped by whatever the two
// disagreed about: the pane's blank tail, or the rows a letterboxed pane leaves
// unused. Measured at 38, 7 and 2 rows in different geometries before the
// window's origin and its bound became one number.
func TestFirstNotchOffTheLiveEdgeMovesOneRow(t *testing.T) {
	tests := []struct {
		name       string
		history    int
		paneRows   int
		blankTail  int
		viewHeight int
	}{
		{name: "pane taller than the viewport", history: 200, paneRows: 40, viewHeight: 20},
		{name: "blank tail under a tall pane", history: 200, paneRows: 40, blankTail: 37, viewHeight: 20},
		{name: "pane shorter than the viewport", history: 200, paneRows: 10, viewHeight: 20},
		{name: "blank tail under a short pane", history: 200, paneRows: 12, blankTail: 7, viewHeight: 20},
		{name: "pane exactly one row shorter", history: 200, paneRows: 19, viewHeight: 20},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := tt.paneRows - tt.blankTail
			if content < 1 {
				t.Fatalf("fixture has no drawn pane rows")
			}
			rows := make([]string, 0, tt.history+tt.paneRows)
			for range tt.history {
				rows = append(rows, "history row")
			}
			for range content {
				rows = append(rows, "pane row")
			}
			for range tt.blankTail {
				rows = append(rows, "")
			}
			buffer := NewOutputBuffer(1000)
			buffer.ApplySnapshot(CaptureSnapshot(CaptureInput{
				Output:     strings.Join(rows, "\n"),
				PaneHeight: tt.paneRows,
			}))

			// A watched surface: it trims trailing blanks wherever there is no
			// live grid behind them, which is the state the boundary bug lived in.
			in := ViewportInput{
				Buffer:       buffer,
				Width:        80,
				Height:       tt.viewHeight,
				PaneHeight:   tt.paneRows,
				PaneWidth:    80,
				TrimTrailing: TrimsTrailingRows(false),
			}

			live := FitViewport(withPlacement(in, PlaceWindow(nil, 0)))
			bound := WindowBound(in)
			if live.Start != bound {
				t.Fatalf("live edge starts at %d but the bound is %d — the window's origin "+
					"and its bound are not one measurement", live.Start, bound)
			}

			for _, step := range []int{1, 3} {
				got := FitViewport(withPlacement(in, PlaceWindow(nil, step)))
				if want := live.Start - step; got.Start != want {
					t.Errorf("offset %d starts at %d, want %d (%d rows off the live edge)",
						step, got.Start, want, step)
				}
				if got.End <= got.Start {
					t.Errorf("offset %d draws %d rows", step, got.End-got.Start)
				}
			}

			// And the far end still reaches the oldest row rather than stopping
			// a dead notch short of it.
			if oldest := FitViewport(withPlacement(in, PlaceWindow(nil, bound))); oldest.Start != 0 {
				t.Errorf("at the bound the window starts at %d, want the oldest row 0", oldest.Start)
			}
		})
	}
}

func withPlacement(in ViewportInput, placement WindowPlacement) ViewportInput {
	in.Offset, in.OffsetFromBottom, in.Follow = placement.Offset, placement.FromBottom, placement.Follow
	return in
}
