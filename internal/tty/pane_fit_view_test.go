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

// A pane taller than the viewport anchors on the cursor rather than always
// showing the pane's tail: editing near the top of a taller pane must not type
// into a region the viewport never draws (td-73fa86).
func TestModelViewAnchorsTallPaneOnCursor(t *testing.T) {
	model := New(nil)
	model.Width = 10
	model.Height = 2
	model.Enter("current", "")
	model.State.OutputBuf.Write("0000000000\n1111111111\n2222222222\n3333333333")
	model.State.PaneWidth = 10
	model.State.PaneHeight = 4
	model.State.CursorRow = 1
	model.State.CursorCol = 0
	model.State.CursorVisible = true

	if got, want := model.View(), "0000000000\n1111111111"; got != want {
		t.Fatalf("View() = %q, want %q", got, want)
	}
	cursor := model.Cursor()
	if cursor == nil || cursor.X != 0 || cursor.Y != 1 {
		t.Fatalf("Cursor() = %#v, want (0,1)", cursor)
	}

	// With no cursor to anchor to, the pane's tail — the latest output — wins.
	model.State.CursorVisible = false
	if got, want := model.View(), "2222222222\n3333333333"; got != want {
		t.Fatalf("unanchored View() = %q, want %q", got, want)
	}
}

// tmux trims trailing blank rows from a capture, so the buffer can hold fewer
// lines than the pane is tall. View and Cursor have to agree on which buffer
// line pane row 0 is, or the cursor lands on a line the user is not editing
// (td-73fa86).
func TestModelViewShortBufferKeepsCursorOnEditedRow(t *testing.T) {
	model := New(nil)
	model.Width = 10
	model.Height = 4
	model.Enter("current", "")
	model.State.OutputBuf.Write("aaaaaaaaaa\nbbbbbbbbbb\ncccccccccc\ndddddddddd\neeeeeeeeee\nffffffffff")
	model.State.PaneWidth = 10
	model.State.PaneHeight = 10
	model.State.CursorRow = 5
	model.State.CursorCol = 0
	model.State.CursorVisible = true

	if got, want := model.View(), "cccccccccc\ndddddddddd\neeeeeeeeee\nffffffffff"; got != want {
		t.Fatalf("View() = %q, want %q", got, want)
	}
	cursor := model.Cursor()
	if cursor == nil {
		t.Fatal("Cursor() = nil, want the cursor on the edited row")
	}
	// The edited row is the last of the four rendered lines.
	if cursor.X != 0 || cursor.Y != 3 {
		t.Fatalf("Cursor() = (%d,%d), want (0,3)", cursor.X, cursor.Y)
	}
}

// Mouse coordinates forwarded to tmux have to move with the pixels: a clipped
// pane is drawn scrolled on both axes.
func TestModelPaneCoordsAccountForClipping(t *testing.T) {
	model := New(nil)
	model.Width = 10
	model.Height = 2
	model.Enter("current", "")
	model.State.PaneWidth = 20
	model.State.PaneHeight = 4
	model.State.CursorRow = 3
	model.State.CursorCol = 15
	model.State.CursorVisible = true

	// ColOffset 6 (15-10+1), RowOffset 2 (3-2+1): the top-left visible cell is
	// pane column 7, row 3 in tmux's 1-indexed space.
	col, row, ok := model.PaneCoords(1, 1)
	if !ok || col != 7 || row != 3 {
		t.Fatalf("PaneCoords(1,1) = (%d,%d,%v), want (7,3,true)", col, row, ok)
	}
	if _, _, ok := model.PaneCoords(11, 1); ok {
		t.Fatal("PaneCoords past the rendered width reported a hit")
	}
	if _, _, ok := model.PaneCoords(1, 3); ok {
		t.Fatal("PaneCoords past the rendered height reported a hit")
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
