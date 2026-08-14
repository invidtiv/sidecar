package tty

import (
	"time"

	"github.com/marcus/sidecar/internal/mouse"
)

// WheelRoute is who owns a wheel notch over a terminal surface.
type WheelRoute uint8

const (
	// WheelLocal scrolls the captured output the surface is showing. This is
	// the fallback for every notch the application has not claimed, and it is
	// what makes the wheel work over a plain shell.
	WheelLocal WheelRoute = iota
	// WheelPane encodes the notch as an SGR wheel report for the application
	// running in the pane. Full-screen apps draw their own scrollback inside the
	// pane and keep tmux's history empty, so consuming the notch locally would
	// slide the viewport across the app's live frame and leave the layout
	// looking torn.
	WheelPane
)

// MaxWheelNotchesPerFlush caps how many wheel reports one debounced burst can
// send. A fast trackpad flick can coalesce a large delta, and every notch is a
// separate `tmux send-keys`; past a point the app has scrolled as far as the
// gesture meant anyway.
const MaxWheelNotchesPerFlush = 10

// WheelInput is one wheel event over a terminal surface. Delta is a line count,
// negative upwards. InPane reports that the pointer position maps onto a cell of
// the pane's grid; a notch over chrome is never the application's.
//
// MouseReporting and InPane are facts about the pane under the pointer, asked in
// every state: whether the keyboard happens to be in that pane says nothing
// about who drew what is on screen there.
type WheelInput struct {
	Delta          int
	Shift, Alt     bool
	MouseReporting bool
	InPane         bool
	// WritesEnabled says the host may write to the pane at all. A forwarded
	// notch is input, gated exactly as typing is, so a host that cannot write
	// keeps every notch for its own window.
	WritesEnabled bool
}

// RouteWheel decides who owns a notch, and how many whole notches it is worth.
//
// The application only owns the wheel while it has asked for mouse reports. Alt
// is the "give me the terminal, not the app" modifier; Shift is checked too for
// symmetry with click handling, though a shift+wheel is normally mapped to
// horizontal scroll before it arrives.
func RouteWheel(in WheelInput) (WheelRoute, int) {
	if !in.WritesEnabled || in.Delta == 0 || !in.MouseReporting || in.Shift || in.Alt || !in.InPane {
		return WheelLocal, 0
	}
	return WheelPane, min(WheelNotches(in.Delta), MaxWheelNotchesPerFlush)
}

// Wheel burst tuning (td-3b15ee). A trackpad delivers a flick as a long run of
// tiny events, and each one that reaches a surface is a repaint — and, when the
// application owns the wheel, a separate send. Coalescing them keeps a flick
// travelling the same distance on every surface instead of as far as each
// surface happens to be able to keep up with.
const (
	// WheelDebounceInterval is the base coalescing window (~60fps).
	WheelDebounceInterval = 16 * time.Millisecond

	// WheelBurstDebounce replaces it once a flick is under way (~30fps): a burst
	// is filtered harder, because its events arrive faster than any surface can
	// usefully draw them.
	WheelBurstDebounce = 12 * time.Millisecond

	// WheelBurstThreshold is the number of events in a row that makes a flick.
	WheelBurstThreshold = 3

	// WheelBurstTimeout is how long after the last event a flick is still
	// running. It has to outlast the trailing garbage a split SGR report leaves
	// behind, and it bounds how long anything keyed to the flick is deferred.
	WheelBurstTimeout = 500 * time.Millisecond
)

// WheelBurst coalesces a trackpad flick into whole steps. It is the one place
// the "how much of this burst has the surface earned" rule lives, so a fast
// flick travels the same distance whichever surface it lands on.
//
// It holds only the burst's own bookkeeping: no viewport, no transport, no
// clock of its own — the caller passes the time, so a test can drive a whole
// flick without sleeping.
type WheelBurst struct {
	pending int
	count   int
	lastAt  time.Time
}

// Add takes one wheel event and reports the delta to apply, if any. A held-back
// event is not lost: its delta joins the next one that gets through.
func (b *WheelBurst) Add(delta int, now time.Time) (int, bool) {
	since := now.Sub(b.lastAt)
	if since < WheelBurstTimeout {
		b.count++
	} else {
		b.count = 1
	}
	debounce := WheelDebounceInterval
	if b.count > WheelBurstThreshold {
		debounce = WheelBurstDebounce
	}
	b.pending += delta
	if since < debounce {
		return 0, false
	}
	b.lastAt = now
	flushed := b.pending
	b.pending = 0
	return flushed, true
}

// Pending is the delta held back from the flick so far. It is carried by the
// next event that gets through, never dropped.
func (b *WheelBurst) Pending() int { return b.pending }

// Reset drops a burst in progress, for a surface that is no longer the one being
// scrolled.
func (b *WheelBurst) Reset() {
	b.pending = 0
	b.count = 0
}

// LastAt is when the burst last let an event through, which is what a snap-back
// cooldown measures against.
func (b *WheelBurst) LastAt() time.Time { return b.lastAt }

// Remaining is how much of the burst window is left, and false once the flick is
// over. A surface that repaints from a poll defers it for this long: a capture
// taken mid-flick is stale before it arrives.
func (b *WheelBurst) Remaining(now time.Time) (time.Duration, bool) {
	if b.count <= 0 {
		return 0, false
	}
	elapsed := now.Sub(b.lastAt)
	if elapsed >= WheelBurstTimeout {
		return 0, false
	}
	return WheelBurstTimeout - elapsed, true
}

// WheelNotches converts a scroll delta in lines back into whole wheel notches,
// never rounding a real scroll down to nothing.
//
// A delta is a line count — one notch expands into mouse.WheelScrollLines — but
// the pane wants notches, and the app applies its own lines-per-notch on top.
// Forwarding the line count makes every notch scroll that many times too far.
func WheelNotches(delta int) int {
	lines := max(delta, -delta)
	return max(lines/mouse.WheelScrollLines, 1)
}
