package tty

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// A notch the application owns notes the activity, pins the window and is sent
// as a report — in that order, and never as a local scroll. Every host routes a
// notch through this one path, so a surface cannot forward without pinning.
func TestWheelHandlerForwardsAnOwnedNotchAndPinsTheWindow(t *testing.T) {
	var acts []string
	var burst WheelBurst
	handler := WheelHandler{
		Burst:          &burst,
		WritesEnabled:  true,
		MouseReporting: func() bool { return true },
		PaneCoords:     func(x, y int) (int, int, bool) { return x, y, true },
		PinToLive:      func() { acts = append(acts, "pin") },
		NoteActivity:   func() { acts = append(acts, "activity") },
		SendNotches: func(up bool, col, row, notches int) tea.Cmd {
			acts = append(acts, "send")
			if !up || col != 4 || row != 7 || notches < 1 {
				t.Fatalf("send(up=%v col=%d row=%d notches=%d)", up, col, row, notches)
			}
			return nil
		},
		ScrollLocal: func(int) tea.Cmd { acts = append(acts, "local"); return nil },
	}
	now := time.Now()
	handler.Handle(WheelGesture{Delta: -WheelNotches(1), X: 4, Y: 7, Now: now})
	handler.Handle(WheelGesture{Delta: -3, X: 4, Y: 7, Now: now.Add(WheelBurstTimeout)})
	if len(acts) < 3 || acts[len(acts)-3] != "activity" || acts[len(acts)-2] != "pin" || acts[len(acts)-1] != "send" {
		t.Fatalf("acts = %v, want the activity noted and the window pinned before the send", acts)
	}
	for _, act := range acts {
		if act == "local" {
			t.Fatal("a notch the application owns also scrolled the surface's own window")
		}
	}
}

// Everything the application has not claimed scrolls the host's own window,
// which is what makes the wheel work over a plain shell — and it is still the
// reader reading this pane, so every notch counts as activity: a scrolled pane
// repainted at the idle tier is the surface lagging behind the wheel.
func TestWheelHandlerScrollsLocallyWhenTheApplicationHasNotClaimedTheWheel(t *testing.T) {
	var scrolled, noted int
	var burst WheelBurst
	handler := WheelHandler{
		Burst:          &burst,
		WritesEnabled:  true,
		MouseReporting: func() bool { return false },
		NoteActivity:   func() { noted++ },
		SendNotches:    func(bool, int, int, int) tea.Cmd { t.Fatal("forwarded to a pane that wants no mouse"); return nil },
		ScrollLocal:    func(delta int) tea.Cmd { scrolled += delta; return nil },
	}
	now := time.Now()
	handler.Handle(WheelGesture{Delta: -1, Now: now})
	handler.Handle(WheelGesture{Delta: -2, Now: now.Add(WheelBurstTimeout)})
	if scrolled != -3 {
		t.Fatalf("scrolled %d, want the whole coalesced flick (-3)", scrolled)
	}
	if noted != 2 {
		t.Fatalf("noted %d local notches as activity, want every one of the 2 that landed", noted)
	}
}

// A forwarded notch is input, so a host that may not write to the pane keeps
// every notch for its own window — and is never even asked where the pane's
// cells are, because a joined capture has no pane grid to answer from.
func TestWheelHandlerRefusesToForwardWithoutWritesEnabled(t *testing.T) {
	var scrolled, noted int
	var burst WheelBurst
	handler := WheelHandler{
		Burst:          &burst,
		MouseReporting: func() bool { return true },
		PaneCoords: func(x, y int) (int, int, bool) {
			t.Fatal("pane coordinates were computed for a host that may not write")
			return 0, 0, false
		},
		PinToLive: func() { t.Fatal("the window was pinned for a notch that never left") },
		// The notch is still the reader reading this pane, so it is still activity;
		// what writes being off forbids is the send, not the cadence.
		NoteActivity: func() { noted++ },
		SendNotches: func(bool, int, int, int) tea.Cmd {
			t.Fatal("a notch was forwarded with writes disabled")
			return nil
		},
		ScrollLocal: func(delta int) tea.Cmd { scrolled += delta; return nil },
	}
	handler.Handle(WheelGesture{Delta: -3, X: 4, Y: 7, Now: time.Now()})
	if scrolled != -3 {
		t.Fatalf("scrolled %d, want the notch on the host's own window (-3)", scrolled)
	}
	if noted != 1 {
		t.Fatalf("noted %d, want the notch counted as the reader reading the pane", noted)
	}
}
