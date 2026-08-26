package overview

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/tty"
)

// The window every parity gesture starts from, far enough off the live edge that
// a pin back to it and a step further away from it land on different rows.
const parityWheelStart = 3

// previewWheelOutcome is everything a reader can tell about what one notch did:
// what reached the pane, and where this surface's own window ended up. Two
// states that produce the same outcome are the same experience.
type previewWheelOutcome struct {
	forwarded fakeWheel
	sends     int
	window    int
}

// parityPreviewOutcome replays one notch over this browser's preview in a named
// keyboard state and reports what it did. The pane, its mouse tracking and the
// window the notch starts from are held identical, so the only difference
// between two of these is where the keyboard is.
func parityPreviewOutcome(t *testing.T, live, reporting, alt bool) previewWheelOutcome {
	t.Helper()
	m, _, terminal := interactiveModel(t)
	terminal.buffer.ApplySnapshot(tty.PaneSnapshot{Output: paneBody(60)})
	terminal.reporting = reporting
	if live {
		enterInteractive(t, m)
	}
	x, y := previewAt(t, m)
	if m.PreviewInteractive() != live {
		t.Fatalf("test premise: the pane is live=%v, want %v", m.PreviewInteractive(), live)
	}
	if bound := m.previewMaxOffset(); bound <= parityWheelStart+mouse.WheelScrollLines {
		t.Fatalf("test premise: a bound of %d rows cannot hold a notch past the starting window", bound)
	}

	m.jumpPreviewWindow(parityWheelStart)
	settleWheel()
	var mod tea.KeyMod
	if alt {
		mod = tea.ModAlt
	}
	run(t, m, m.WorkspacesMouse(tea.MouseWheelMsg{X: x + 2, Y: y + 3, Button: tea.MouseWheelUp, Mod: mod}))

	outcome := previewWheelOutcome{sends: len(terminal.wheel), window: m.previewTerminalLeaf().Scroll}
	if len(terminal.wheel) > 0 {
		outcome.forwarded = terminal.wheel[len(terminal.wheel)-1]
	}
	return outcome
}

// The reported bug, as an executable claim on this surface: one notch over a
// given pane does the same thing whether or not the keyboard is in it. The two
// states are compared against each other rather than against a number written
// here twice, and what they are both held to — who owns the notch — is read
// from the shared rule for the pane's own facts, so this browser cannot pass by
// agreeing with itself.
func TestOneNotchOverThePreviewLandsTheSameWayWatchedOrLive(t *testing.T) {
	for _, pane := range []struct {
		name      string
		reporting bool
		alt       bool
	}{
		{"an application that has asked for mouse reports", true, false},
		{"a plain shell", false, false},
		{"alt+wheel over an application that has asked for mouse reports", true, true},
	} {
		t.Run(pane.name, func(t *testing.T) {
			route, _ := tty.RouteWheel(tty.WheelInput{
				Delta: -mouse.WheelScrollLines, Alt: pane.alt,
				MouseReporting: pane.reporting, InPane: true, WritesEnabled: true,
			})

			var watched, live previewWheelOutcome
			t.Run("watched", func(t *testing.T) { watched = parityPreviewOutcome(t, false, pane.reporting, pane.alt) })
			t.Run("live", func(t *testing.T) { live = parityPreviewOutcome(t, true, pane.reporting, pane.alt) })

			if watched != live {
				t.Fatalf("the preview answers the same notch differently: watched %+v, live %+v", watched, live)
			}
			if (watched.sends > 0) != (route == tty.WheelPane) {
				t.Fatalf("the preview sent %d notches for one the shared rule routes %v: %+v",
					watched.sends, route, watched)
			}
			if route == tty.WheelPane {
				// While the application owns the wheel it owns what the pane shows,
				// so the window is handed back to the live edge.
				if watched.window != 0 {
					t.Fatalf("the window stayed %d rows back while the app owned the notch", watched.window)
				}
				if watched.forwarded.col < 1 || watched.forwarded.row < 1 {
					t.Fatalf("the notch was forwarded at (%d,%d), not the pane's own 1-indexed cell",
						watched.forwarded.col, watched.forwarded.row)
				}
				return
			}
			if watched.window != parityWheelStart+mouse.WheelScrollLines {
				t.Fatalf("a local notch left the window at %d, want the notch's %d rows past %d",
					watched.window, mouse.WheelScrollLines, parityWheelStart)
			}
		})
	}
}
