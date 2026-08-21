package tty

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/mouse"
)

// paceFlick replays one dense momentum tail through a handler and reports the
// times at which the surface was asked to act on a flushed delta.
func paceFlick(reporting bool) (acts []time.Time, pending int) {
	var burst WheelBurst
	var now time.Time
	handler := WheelHandler{
		Burst:          &burst,
		WritesEnabled:  true,
		MouseReporting: func() bool { return reporting },
		PaneCoords:     func(x, y int) (int, int, bool) { return x, y, true },
		PinToLive:      func() {},
		SendNotches: func(_ bool, _, _, _ int) tea.Cmd {
			acts = append(acts, now)
			return nil
		},
		ScrollLocal: func(int) tea.Cmd {
			acts = append(acts, now)
			return nil
		},
	}
	at := time.Now()
	for i := range 60 {
		at = at.Add(time.Duration(4+i/4) * time.Millisecond)
		now = at
		handler.Handle(WheelGesture{Delta: -mouse.WheelScrollLines, X: 4, Y: 7, Now: at})
	}
	return acts, burst.Pending()
}

// A momentum tail forwarded to an application that owns its own scrollback
// paces itself to WheelPaneDebounce: the tail used to arrive as one send plus
// one capture per base-debounce tick long after the application's view had
// stopped moving, which is the flood that froze the UI at a scrollback edge.
func TestForwardedMomentumPacesItself(t *testing.T) {
	sends, _ := paceFlick(true)
	if len(sends) < 2 {
		t.Fatalf("%d sends for a long tail: the tail was not forwarded at all", len(sends))
	}
	for i := 1; i < len(sends); i++ {
		if gap := sends[i].Sub(sends[i-1]); gap < WheelPaneDebounce {
			t.Fatalf("forwarded sends %d and %d were %v apart, want at least %v",
				i-1, i, gap, WheelPaneDebounce)
		}
	}
}

// Plain-shell scrolling keeps its existing cadence: the pace exists for the
// forwarded path alone, and tightening it there must not slow the local window.
func TestLocalScrollingKeepsItsCadence(t *testing.T) {
	forwarded, _ := paceFlick(true)
	local, _ := paceFlick(false)
	if len(local) <= len(forwarded) {
		t.Fatalf("local acts=%d, forwarded=%d: pacing did not distinguish the paths",
			len(local), len(forwarded))
	}
	for i := 1; i < len(local); i++ {
		if gap := local[i].Sub(local[i-1]); gap >= WheelPaneDebounce && gap < WheelBurstTimeout {
			t.Fatalf("local acts %d and %d were %v apart: the local path inherited the forwarded pace",
				i-1, i, gap)
		}
	}
}

// The pace belongs to one gesture. A new flick after the burst has expired
// answers at the ordinary debounce again instead of inheriting the previous
// gesture's forwarded spacing.
func TestANewGestureDropsThePace(t *testing.T) {
	var burst WheelBurst
	handler := WheelHandler{
		Burst:          &burst,
		WritesEnabled:  true,
		MouseReporting: func() bool { return true },
		PaneCoords:     func(x, y int) (int, int, bool) { return x, y, true },
		SendNotches:    func(_ bool, _, _, _ int) tea.Cmd { return nil },
	}
	at := time.Now()
	first := at.Add(WheelDebounceInterval)
	second := first.Add(WheelPaneDebounce)
	handler.Handle(WheelGesture{Delta: -mouse.WheelScrollLines, X: 4, Y: 7, Now: first})
	handler.Handle(WheelGesture{Delta: -mouse.WheelScrollLines, X: 4, Y: 7, Now: second})
	if !burst.pane {
		t.Fatal("setup: the gesture never became a forwarded one")
	}

	// The burst expires relative to its last event, so the new gesture starts
	// a full timeout after `second`.
	fresh := second.Add(WheelBurstTimeout + time.Millisecond)
	// The first event after a long silence always flushes: the previous flush
	// is ancient history. What must be true is that the new gesture's next
	// event answers at the ordinary debounce, not at the inherited pace.
	if _, ok := burst.Add(-mouse.WheelScrollLines, fresh); !ok {
		t.Fatal("the first event of a new gesture did not flush")
	}
	if _, ok := burst.Add(-mouse.WheelScrollLines, fresh.Add(WheelDebounceInterval)); !ok {
		t.Fatal("a new gesture was still paced by the previous one's forwarded spacing")
	}
}
