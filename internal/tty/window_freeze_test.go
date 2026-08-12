package tty

import "testing"

// A gesture pins the window where the user can see it, and the end of the
// gesture places it back against the live bottom without moving the rows on
// screen. Both halves are one rule: a surface that freezes and never thaws has
// stopped following output for good.
func TestWindowFreezeThawsToTheSameRowsItPinned(t *testing.T) {
	var freeze WindowFreeze
	if freeze.Active() {
		t.Fatal("a window nobody is dragging over is pinned")
	}
	freeze.Freeze(30)
	freeze.Freeze(90) // A second freeze inside one gesture keeps the first.
	if !freeze.Active() || freeze.Start() != 30 {
		t.Fatalf("freeze = %v/%d, want the window pinned at 30", freeze.Active(), freeze.Start())
	}
	freeze.Scroll(-5, 100) // Five rows towards older output.
	if freeze.Start() != 35 {
		t.Fatalf("frozen start = %d, want 35", freeze.Start())
	}
	offset, ok := freeze.Thaw(Viewport{Start: 35, MaxOffset: 100})
	if !ok || offset != 65 {
		t.Fatalf("thaw = (%d, %v), want the same rows as 65 back from the live edge", offset, ok)
	}
	if freeze.Active() {
		t.Fatal("the window stayed pinned after the gesture that pinned it ended")
	}
	if _, ok := freeze.Thaw(Viewport{Start: 0, MaxOffset: 100}); ok {
		t.Fatal("thawing a window nobody froze reported an offset to move it to")
	}
}

// A window pinned at the live bottom resumes following, which is what makes a
// drag-select over a live pane leave it live.
func TestWindowFreezeThawsToTheLiveEdge(t *testing.T) {
	var freeze WindowFreeze
	freeze.Freeze(40)
	if offset, ok := freeze.Thaw(Viewport{Start: 40, MaxOffset: 40}); !ok || offset != 0 {
		t.Fatalf("thaw = (%d, %v), want the live edge", offset, ok)
	}
	if ThawOffsetFrom(40, 40) != 0 {
		t.Fatal("a window pinned at the live bottom did not resolve to the live edge")
	}
}
