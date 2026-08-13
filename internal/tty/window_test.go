package tty

import "testing"

// The placement every host renders from: a distance back from the live bottom
// that follows output at zero, or an absolute start while a gesture holds it.
func TestPlaceWindowFollowsOnlyAtTheLiveEdge(t *testing.T) {
	var freeze WindowFreeze

	if got := PlaceWindow(&freeze, 0); got != (WindowPlacement{FromBottom: true, Follow: true}) {
		t.Fatalf("a window at the live edge placed as %+v", got)
	}
	if got := PlaceWindow(&freeze, 7); got != (WindowPlacement{Offset: 7, FromBottom: true}) {
		t.Fatalf("a scrolled-back window placed as %+v", got)
	}

	freeze.Freeze(42)
	if got := PlaceWindow(&freeze, 7); got != (WindowPlacement{Offset: 42}) {
		t.Fatalf("a pinned window placed as %+v, want the absolute start it was pinned to", got)
	}
	if got := WindowAnchor(&freeze, 7); got != 42 {
		t.Fatalf("anchor = %d, want the pinned start", got)
	}
}

// Scrolling counts back through scrollback and clamps to the buffer; a pinned
// window moves in the coordinate it is pinned in and keeps its offset, so a
// gesture's edge-autoscroll never hands the window back to the live bottom
// mid-drag.
func TestScrollWindowClampsAndLeavesAPinnedWindowPinned(t *testing.T) {
	var freeze WindowFreeze

	if got := ScrollWindow(&freeze, 5, 3, 20); got != 8 {
		t.Fatalf("offset = %d, want 8", got)
	}
	if got := ScrollWindow(&freeze, 18, 9, 20); got != 20 {
		t.Fatalf("offset = %d, want the furthest the buffer goes (20)", got)
	}
	if got := ScrollWindow(&freeze, 2, -9, 20); got != 0 {
		t.Fatalf("offset = %d, want the live edge", got)
	}
	// A wheel notch counts up the screen, which is the other direction.
	if got := ScrollWindowRows(&freeze, 5, 3, 20); got != 2 {
		t.Fatalf("offset = %d, want 2 — a notch down moves towards live", got)
	}

	freeze.Freeze(30)
	if got := ScrollWindow(&freeze, 5, 4, 40); got != 5 {
		t.Fatalf("offset = %d, want the untouched 5 while pinned", got)
	}
	if freeze.Start() != 26 {
		t.Fatalf("pinned start = %d, want 26 — scrolling back is a smaller start", freeze.Start())
	}
}

// The chosen leave-live behavior: the reader's window survives the end of the
// mode, and a pin is thawed to the rows it was holding rather than kept.
func TestLeaveLiveWindowKeepsTheReadersWindow(t *testing.T) {
	var freeze WindowFreeze

	if got := LeaveLiveWindow(&freeze, 12, 40); got != 12 {
		t.Fatalf("offset = %d, want the 12 rows back the reader was at", got)
	}

	freeze.Freeze(25)
	if got := LeaveLiveWindow(&freeze, 0, 40); got != 15 {
		t.Fatalf("offset = %d, want the pinned rows as a distance back (15)", got)
	}
	if freeze.Active() {
		t.Fatal("leaving the mode left the window pinned")
	}
}
