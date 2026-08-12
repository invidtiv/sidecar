package tty

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// A notch the application owns pins the window, notes the activity and is sent
// as a report — in that order, and never as a local scroll. Every host routes a
// notch through this one path, so a surface cannot forward without pinning.
func TestWheelHandlerForwardsAnOwnedNotchAndPinsTheWindow(t *testing.T) {
	var acts []string
	var burst WheelBurst
	handler := WheelHandler{
		Burst:          &burst,
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
	if len(acts) < 3 || acts[len(acts)-3] != "pin" || acts[len(acts)-2] != "activity" || acts[len(acts)-1] != "send" {
		t.Fatalf("acts = %v, want the window pinned and the activity noted before the send", acts)
	}
	for _, act := range acts {
		if act == "local" {
			t.Fatal("a notch the application owns also scrolled the surface's own window")
		}
	}
}

// Everything the application has not claimed scrolls the host's own window,
// which is what makes the wheel work over a plain shell.
func TestWheelHandlerScrollsLocallyWhenTheApplicationHasNotClaimedTheWheel(t *testing.T) {
	var scrolled int
	var burst WheelBurst
	handler := WheelHandler{
		Burst:          &burst,
		MouseReporting: func() bool { return false },
		SendNotches:    func(bool, int, int, int) tea.Cmd { t.Fatal("forwarded to a pane that wants no mouse"); return nil },
		ScrollLocal:    func(delta int) tea.Cmd { scrolled += delta; return nil },
	}
	now := time.Now()
	handler.Handle(WheelGesture{Delta: -1, Now: now})
	handler.Handle(WheelGesture{Delta: -2, Now: now.Add(WheelBurstTimeout)})
	if scrolled != -3 {
		t.Fatalf("scrolled %d, want the whole coalesced flick (-3)", scrolled)
	}
}
