package reveal

import "testing"

// Design 1h: one whole row per frame, revealed top-down.
func TestRevealAddsOneRowPerFrame(t *testing.T) {
	defer SetAnimatedForTests(true)()

	s := New(4)
	want := []int{1, 2, 3, 4, 4}
	for i, w := range want {
		if got := s.Rows(); got != w {
			t.Fatalf("frame %d: rows = %d, want %d", i, got, w)
		}
		s.Advance()
	}
	if s.Phase() != Shown || s.Animating() {
		t.Fatalf("a fully revealed block is still animating: phase=%v", s.Phase())
	}
}

// Retraction is bottom-up, which is the same integer running the other way —
// the border is never redrawn mid-motion.
func TestRetractRemovesOneRowPerFrame(t *testing.T) {
	defer SetAnimatedForTests(true)()

	s := New(3)
	for s.Animating() {
		s.Advance()
	}
	s.Leave()
	want := []int{3, 2, 1, 0}
	for i, w := range want {
		if got := s.Rows(); got != w {
			t.Fatalf("exit frame %d: rows = %d, want %d", i, got, w)
		}
		s.Advance()
	}
	if s.Phase() != Gone || s.Visible() {
		t.Fatalf("a retracted block is not gone: phase=%v rows=%d", s.Phase(), s.Rows())
	}
}

// The degraded mode is the whole of the motion: the block appears, and later it
// disappears. No frames are owed, so no ticks are scheduled.
func TestDegradedTerminalSkipsTheMotion(t *testing.T) {
	defer SetAnimatedForTests(false)()

	s := New(5)
	if s.Rows() != 5 || s.Animating() {
		t.Fatalf("degraded reveal: rows=%d animating=%v, want 5/false", s.Rows(), s.Animating())
	}
	s.Leave()
	if s.Rows() != 0 || s.Phase() != Gone {
		t.Fatalf("degraded retract: rows=%d phase=%v", s.Rows(), s.Phase())
	}
}

// A block changes height between frames — a countdown row goes, a peek line
// arrives. A shown block follows immediately; one still entering keeps its
// progress and gets a new target.
func TestResizeTracksTheBlockHeight(t *testing.T) {
	defer SetAnimatedForTests(true)()

	s := New(6)
	s.Advance() // rows 2
	s.Resize(3)
	if s.Rows() != 2 || s.Phase() != Entering {
		t.Fatalf("mid-reveal resize: rows=%d phase=%v", s.Rows(), s.Phase())
	}
	s.Advance()
	if s.Rows() != 3 || s.Phase() != Shown {
		t.Fatalf("resize target not honoured: rows=%d phase=%v", s.Rows(), s.Phase())
	}
	s.Resize(5)
	if s.Rows() != 5 {
		t.Fatalf("a shown block did not grow with its content: rows=%d", s.Rows())
	}
}

func TestClipKeepsTheTopRows(t *testing.T) {
	defer SetAnimatedForTests(true)()

	block := "a\nb\nc\nd"
	s := New(4)
	if got := s.Clip(block); got != "a" {
		t.Fatalf("first frame clip = %q, want %q", got, "a")
	}
	s.Advance()
	if got := s.Clip(block); got != "a\nb" {
		t.Fatalf("second frame clip = %q", got)
	}
	for s.Animating() {
		s.Advance()
	}
	if got := s.Clip(block); got != block {
		t.Fatalf("settled clip = %q, want the whole block", got)
	}
	s.Leave()
	s.Advance()
	if got := s.Clip(block); got != "a\nb\nc" {
		t.Fatalf("retracting clip = %q, want the bottom row gone", got)
	}
}
