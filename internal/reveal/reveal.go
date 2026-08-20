// Package reveal is the row-count state machine behind design frame 1h — the
// only motion sidecar allows outside the intro.
//
// The spec in one sentence: a block of N rows arrives one whole row per frame,
// ~90ms apart, revealed top-down and retracted bottom-up, with no subpixel
// travel and no fades. Because both directions only ever change *how many rows
// from the top are painted*, the whole thing is one integer, and a caller
// renders its block and keeps the first Rows() lines.
//
// Nothing here draws, and nothing here knows what the rows contain: the toast
// stack is the first caller, and any future motion is meant to be the second.
package reveal

import (
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Step is one animation frame. Design 1h says ~90ms; polish round 2 speeds
// reveal and retract by ~25% because at 90ms a five-row block took most of half
// a second to arrive. The status flash moves at the same cadence, so the two
// motions in the system still run at one speed rather than two that nearly
// agree (the flash takes an extra fade step to keep its fade the same length in
// wall-clock time).
const Step = 67 * time.Millisecond

// Phase is where a block is in its life.
type Phase int

const (
	// Entering: rows are being added from the top.
	Entering Phase = iota
	// Shown: every row is painted and nothing is scheduled.
	Shown
	// Leaving: rows are being removed from the bottom.
	Leaving
	// Gone: nothing is painted and the caller may drop the state.
	Gone
)

// State is one block's reveal. The zero value is a gone, empty block; use New.
type State struct {
	total int
	rows  int
	phase Phase
}

// New starts a block of total rows entering. On a terminal that cannot be
// trusted with per-frame repaints the block is simply shown at full height,
// which is the spec'd degraded mode and needs no ticks at all.
func New(total int) *State {
	s := &State{total: max(0, total)}
	if !Animated() || s.total <= 1 {
		s.rows, s.phase = s.total, Shown
		return s
	}
	s.rows, s.phase = 1, Entering
	if s.rows >= s.total {
		s.phase = Shown
	}
	return s
}

// Rows is how many rows from the top of the block to paint this frame.
func (s *State) Rows() int {
	if s == nil {
		return 0
	}
	return s.rows
}

// Phase reports the current phase.
func (s *State) Phase() Phase {
	if s == nil {
		return Gone
	}
	return s.phase
}

// Visible reports whether anything is painted.
func (s *State) Visible() bool { return s.Rows() > 0 }

// Animating reports whether the block still owes frames. A host schedules its
// tick while any of its blocks says yes and stops when none do — motion must
// not keep a 90ms timer alive for a screen that has settled.
func (s *State) Animating() bool {
	if s == nil {
		return false
	}
	return s.phase == Entering || s.phase == Leaving
}

// Resize updates the block's full height, which changes between frames: a
// countdown row disappears, a body wraps differently, a collapsed stack gains
// a peek line. A block already shown grows and shrinks with it immediately; a
// block still entering keeps its progress and simply has a new target.
func (s *State) Resize(total int) {
	if s == nil {
		return
	}
	s.total = max(0, total)
	switch s.phase {
	case Shown:
		s.rows = s.total
	case Entering:
		if s.rows >= s.total {
			s.rows, s.phase = s.total, Shown
		}
	case Leaving:
		s.rows = min(s.rows, s.total)
	}
}

// Leave starts the retraction. It is idempotent, and a block that never got
// its motion (degraded terminal) leaves the same way it arrived: at once.
func (s *State) Leave() {
	if s == nil || s.phase == Leaving || s.phase == Gone {
		return
	}
	if !Animated() {
		s.rows, s.phase = 0, Gone
		return
	}
	s.phase = Leaving
	if s.rows <= 0 {
		s.phase = Gone
	}
}

// Advance moves the block one frame. It is a no-op once the block has settled,
// so a stale tick costs nothing.
func (s *State) Advance() {
	if s == nil {
		return
	}
	switch s.phase {
	case Entering:
		s.rows++
		if s.rows >= s.total {
			s.rows, s.phase = s.total, Shown
		}
	case Leaving:
		s.rows--
		if s.rows <= 0 {
			s.rows, s.phase = 0, Gone
		}
	}
}

// Clip returns the first Rows() lines of an already-rendered block. Retracting
// bottom-up is exactly "keep fewer lines from the top", which is why the border
// never has to be redrawn from scratch mid-motion.
func (s *State) Clip(block string) string {
	rows := s.Rows()
	if rows <= 0 {
		return ""
	}
	lines := strings.Split(block, "\n")
	if rows >= len(lines) {
		return block
	}
	return strings.Join(lines[:rows], "\n")
}

var (
	animateOnce sync.Once
	animateOK   bool
	// animatedOverride pins the answer for tests. The real answer is resolved
	// from the environment exactly once per process, which is right for a
	// running app and useless for a test that needs both modes.
	animatedOverride atomic.Pointer[bool]
)

// SetAnimatedForTests pins Animated to v and returns a func restoring the
// environment-derived answer.
func SetAnimatedForTests(v bool) func() {
	animatedOverride.Store(&v)
	return func() { animatedOverride.Store(nil) }
}

// Animated reports whether this terminal gets motion at all. A dumb or absent
// TERM cannot be trusted with per-frame repaints, and SIDECAR_NO_ANIMATION is
// the explicit opt-out for a slow link where 90ms frames cost more than the
// motion is worth. Both the status flash and the toast reveal ask here — one
// answer, resolved once, because an environment lookup per frame is the kind of
// per-frame syscall the latency rules exist to prevent.
func Animated() bool {
	if v := animatedOverride.Load(); v != nil {
		return *v
	}
	animateOnce.Do(func() {
		term := strings.ToLower(strings.TrimSpace(os.Getenv("TERM")))
		if term == "" || term == "dumb" {
			return
		}
		if v := strings.TrimSpace(os.Getenv("SIDECAR_NO_ANIMATION")); v != "" && v != "0" {
			return
		}
		animateOK = true
	})
	return animateOK
}
