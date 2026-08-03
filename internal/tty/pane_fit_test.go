package tty

import "testing"

func TestFitPaneMismatchDirections(t *testing.T) {
	tests := []struct {
		name       string
		in         PaneFitInput
		wantWidth  int
		wantHeight int
		wantOffset int
		wantClip   bool
		wantLetter bool
	}{
		{
			name:       "equal size",
			in:         PaneFitInput{ViewWidth: 120, ViewHeight: 40, PaneWidth: 120, PaneHeight: 40},
			wantWidth:  120,
			wantHeight: 40,
		},
		{
			name:       "pane smaller letterboxes",
			in:         PaneFitInput{ViewWidth: 120, ViewHeight: 40, PaneWidth: 80, PaneHeight: 24},
			wantWidth:  80,
			wantHeight: 24,
			wantLetter: true,
		},
		{
			name:       "pane larger clips",
			in:         PaneFitInput{ViewWidth: 120, ViewHeight: 40, PaneWidth: 200, PaneHeight: 50},
			wantWidth:  120,
			wantHeight: 40,
			wantClip:   true,
		},
		{
			name: "clipped width anchors on cursor",
			in: PaneFitInput{
				ViewWidth: 120, ViewHeight: 40, PaneWidth: 200, PaneHeight: 50,
				CursorCol: 150, HasCursor: true,
			},
			wantWidth:  120,
			wantHeight: 40,
			wantOffset: 31,
			wantClip:   true,
		},
		{
			name: "cursor already visible needs no offset",
			in: PaneFitInput{
				ViewWidth: 120, ViewHeight: 40, PaneWidth: 200, PaneHeight: 50,
				CursorCol: 10, HasCursor: true,
			},
			wantWidth:  120,
			wantHeight: 40,
			wantClip:   true,
		},
		{
			name: "offset never exceeds the pane's last column",
			in: PaneFitInput{
				ViewWidth: 120, ViewHeight: 40, PaneWidth: 130, PaneHeight: 50,
				CursorCol: 129, HasCursor: true,
			},
			wantWidth:  120,
			wantHeight: 40,
			wantOffset: 10,
			wantClip:   true,
		},
		{
			name:       "unknown geometry uses the viewport",
			in:         PaneFitInput{ViewWidth: 120, ViewHeight: 40},
			wantWidth:  120,
			wantHeight: 40,
		},
		{
			name: "letterboxed pane never scrolls horizontally",
			in: PaneFitInput{
				ViewWidth: 120, ViewHeight: 40, PaneWidth: 80, PaneHeight: 20,
				CursorCol: 79, HasCursor: true,
			},
			wantWidth:  80,
			wantHeight: 20,
			wantLetter: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fit := FitPane(tt.in)
			if fit.Width != tt.wantWidth || fit.Height != tt.wantHeight {
				t.Fatalf("size = %dx%d, want %dx%d", fit.Width, fit.Height, tt.wantWidth, tt.wantHeight)
			}
			if fit.ColOffset != tt.wantOffset {
				t.Fatalf("ColOffset = %d, want %d", fit.ColOffset, tt.wantOffset)
			}
			if fit.Clipped() != tt.wantClip {
				t.Fatalf("Clipped = %v, want %v", fit.Clipped(), tt.wantClip)
			}
			if fit.Letterboxed() != tt.wantLetter {
				t.Fatalf("Letterboxed = %v, want %v", fit.Letterboxed(), tt.wantLetter)
			}
		})
	}
}

func TestFitPaneWithWidthRederivesOffset(t *testing.T) {
	fit := FitPane(PaneFitInput{
		ViewWidth: 120, ViewHeight: 40, PaneWidth: 200, PaneHeight: 50,
		CursorCol: 150, HasCursor: true,
	})
	narrowed := fit.WithWidth(119, 200, 150, true)
	if narrowed.Width != 119 {
		t.Fatalf("Width = %d, want 119", narrowed.Width)
	}
	if narrowed.ColOffset != 32 {
		t.Fatalf("ColOffset = %d, want 32", narrowed.ColOffset)
	}
}

func TestPaneSizeIndicator(t *testing.T) {
	if got := PaneSizeIndicator(200, 50, 120, 40); got != "200x50, showing 120x40" {
		t.Fatalf("indicator = %q", got)
	}
	if got := PaneSizeIndicator(120, 40, 120, 40); got != "" {
		t.Fatalf("equal size indicator = %q, want empty", got)
	}
	if got := PaneSizeIndicator(80, 24, 120, 40); got != "" {
		t.Fatalf("smaller pane indicator = %q, want empty", got)
	}
	if got := PaneSizeIndicator(0, 0, 120, 40); got != "" {
		t.Fatalf("unknown geometry indicator = %q, want empty", got)
	}
}
