package tty

import "testing"

// The pane's real geometry, not the size this model asked for, decides what the
// viewport shows (td-73fa86).
func TestModelViewRendersActualPaneGeometry(t *testing.T) {
	tests := []struct {
		name          string
		paneWidth     int
		paneHeight    int
		cursorRow     int
		cursorCol     int
		wantView      string
		wantX         int
		wantY         int
		wantIndicator string
	}{
		{
			name:       "equal size",
			paneWidth:  10,
			paneHeight: 2,
			cursorRow:  1,
			cursorCol:  3,
			wantView:   "2222222222\n3333333333",
			wantX:      3,
			wantY:      1,
		},
		{
			name:       "pane smaller letterboxes to the pane",
			paneWidth:  5,
			paneHeight: 1,
			cursorRow:  0,
			cursorCol:  2,
			wantView:   "33333",
			wantX:      2,
			wantY:      0,
		},
		{
			name:       "pane larger clips and follows the cursor",
			paneWidth:  20,
			paneHeight: 4,
			cursorRow:  3,
			cursorCol:  15,
			// Columns 0-5 scroll off so the cursor at column 15 stays in view;
			// the captured lines are only 10 columns of the 20-column pane.
			wantView:      "2222\n3333",
			wantX:         9,
			wantY:         1,
			wantIndicator: "20x4, showing 10x2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := New(nil)
			model.Width = 10
			model.Height = 2
			model.Enter("current", "")
			model.State.OutputBuf.Write("0000000000\n1111111111\n2222222222\n3333333333")
			model.State.PaneWidth = tt.paneWidth
			model.State.PaneHeight = tt.paneHeight
			model.State.CursorRow = tt.cursorRow
			model.State.CursorCol = tt.cursorCol
			model.State.CursorVisible = true

			if got := model.View(); got != tt.wantView {
				t.Fatalf("View() = %q, want %q", got, tt.wantView)
			}
			cursor := model.Cursor()
			if cursor == nil {
				t.Fatalf("Cursor() = nil, want visible cursor")
			}
			if cursor.X != tt.wantX || cursor.Y != tt.wantY {
				t.Fatalf("Cursor() = (%d,%d), want (%d,%d)", cursor.X, cursor.Y, tt.wantX, tt.wantY)
			}
			if got := model.SizeIndicator(); got != tt.wantIndicator {
				t.Fatalf("SizeIndicator() = %q, want %q", got, tt.wantIndicator)
			}
		})
	}
}

// A pane wider than the viewport scrolls horizontally rather than emitting
// over-long lines that would wrap over the surrounding layout.
func TestModelViewClipsWideLinesWithCursorOffset(t *testing.T) {
	model := New(nil)
	model.Width = 5
	model.Height = 2
	model.Enter("current", "")
	model.State.OutputBuf.Write("0123456789\n0123456789")
	model.State.PaneWidth = 10
	model.State.PaneHeight = 2
	model.State.CursorRow = 1
	model.State.CursorCol = 8
	model.State.CursorVisible = true

	if got, want := model.View(), "45678\n45678"; got != want {
		t.Fatalf("View() = %q, want %q", got, want)
	}
	cursor := model.Cursor()
	if cursor == nil || cursor.X != 4 || cursor.Y != 1 {
		t.Fatalf("Cursor() = %#v, want (4,1)", cursor)
	}
}
