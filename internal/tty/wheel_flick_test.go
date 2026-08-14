package tty

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/mouse"
)

// A flick belongs to the surface it is over. The delta one surface is holding
// back is its own: crossing to another drops it rather than spending it there,
// which is what a shared burst did — the neighbour's held-back lines arrived on
// top of the first notch over the surface the pointer had just reached.
func TestWheelBurstsHoldOneFlickPerSurface(t *testing.T) {
	var bursts WheelBursts
	at := time.Now()

	// Open a flick over the preview and hold a notch back inside its window.
	bursts.For("preview").Add(-3, at)
	if delta, flush := bursts.For("preview").Add(-3, at.Add(WheelDebounceInterval/2)); flush || delta != 0 {
		t.Fatalf("preview flushed %d mid-window, want the notch held back", delta)
	}

	delta, flush := bursts.For("panel").Add(-3, at.Add(WheelDebounceInterval/2))
	if !flush || delta != -3 {
		t.Fatalf("the panel's first notch was %d (flush=%v), want only its own -3", delta, flush)
	}
	if pending := bursts.For("preview").Pending(); pending != 0 {
		t.Fatalf("the preview still holds %d lines of a flick the pointer has left", pending)
	}
}

// A caller with no surface of its own to name — a poll deferral, a snap-back
// cooldown — asks about the flick the reader is making, whichever surface it is
// over, and about the last time the wheel moved anything.
func TestWheelBurstsAnswerAboutTheFlickInProgress(t *testing.T) {
	var bursts WheelBursts
	at := time.Now()
	if _, scrolling := bursts.Remaining(at); scrolling {
		t.Fatal("a host that has never scrolled reported a flick in progress")
	}

	bursts.For("preview").Add(-3, at)
	panelAt := at.Add(WheelBurstTimeout / 2)
	bursts.For("panel").Add(-3, panelAt)

	if remaining, scrolling := bursts.Remaining(panelAt); !scrolling || remaining != WheelBurstTimeout {
		t.Fatalf("Remaining = (%v, %v), want the panel's whole window", remaining, scrolling)
	}
	if got := bursts.LastAt(); !got.Equal(panelAt) {
		t.Fatalf("LastAt = %v, want the most recent notch on any surface (%v)", got, panelAt)
	}

	bursts.Reset()
	if _, scrolling := bursts.Remaining(panelAt); scrolling {
		t.Fatal("Reset left a flick in progress")
	}
}

// trackpadFlick is one hard two-finger flick as a trackpad delivers it: a long
// run of one-notch events, densest at the start and thinning out as the momentum
// decays. The wall clock is never read — the burst takes the time from its
// caller — so the whole gesture is replayed here in no time at all.
func trackpadFlick() []time.Duration {
	gaps := make([]time.Duration, 0, 60)
	for i := range 60 {
		gaps = append(gaps, time.Duration(4+i/4)*time.Millisecond)
	}
	return gaps
}

// What a flick costs in sends, measured rather than assumed: the watched path
// forwards now, and until this slice it coalesced nothing at all on the terminal
// panel, so a flick there travelled one `tmux send-keys` per raw event. The
// burst is the only thing bounding that rate, and MaxWheelNotchesPerFlush the
// only thing bounding one send's size.
func TestATrackpadFlickIsBoundedBySendRateAndNotchesPerSend(t *testing.T) {
	var burst WheelBurst
	sends, notches, biggest := 0, 0, 0
	handler := WheelHandler{
		Burst:          &burst,
		WritesEnabled:  true,
		MouseReporting: func() bool { return true },
		PaneCoords:     func(x, y int) (int, int, bool) { return x, y, true },
		PinToLive:      func() {},
		SendNotches: func(_ bool, _, _, n int) tea.Cmd {
			sends++
			notches += n
			biggest = max(biggest, n)
			return nil
		},
		ScrollLocal: func(int) tea.Cmd { t.Fatal("a claimed notch also scrolled the host's window"); return nil },
	}

	gaps := trackpadFlick()
	at := time.Now()
	start := at
	for _, gap := range gaps {
		at = at.Add(gap)
		handler.Handle(WheelGesture{Delta: -mouse.WheelScrollLines, X: 4, Y: 7, Now: at})
	}
	span := at.Sub(start)

	// The measurement this slice owes the plan, kept where the next reader of the
	// bound will look for it.
	t.Logf("flick: %d events over %v -> %d sends (%.0f/s), %d notches, largest send %d",
		len(gaps), span, sends, float64(sends)/span.Seconds(), notches, biggest)

	if ceiling := int(span/WheelBurstDebounce) + 1; sends > ceiling {
		t.Fatalf("%d sends over %v, want at most one per %v (%d)", sends, span, WheelBurstDebounce, ceiling)
	}
	if sends >= len(gaps) {
		t.Fatalf("%d sends for %d events: the flick was not coalesced at all", sends, len(gaps))
	}
	if biggest > MaxWheelNotchesPerFlush {
		t.Fatalf("one send carried %d notches, want at most %d", biggest, MaxWheelNotchesPerFlush)
	}
	// Coalescing must not cost distance: every event but the tail the burst is
	// still holding rides out inside some send.
	held := WheelNotches(burst.Pending())
	if burst.Pending() == 0 {
		held = 0
	}
	if notches+held != len(gaps) {
		t.Fatalf("the flick travelled %d notches (+%d held), want all %d events", notches, held, len(gaps))
	}
}
