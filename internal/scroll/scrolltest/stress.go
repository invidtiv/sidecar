// Package scrolltest provides the shared inertial-wheel stress fixture used by
// boundary tests across sidecar. It exists so every scrollable surface proves
// the same journey: a hard flick's tail is dropped before Update and View, and
// the first reverse event still passes immediately.
//
// The fixture is deliberately free of sleeps and timers: it drives the
// pre-update boundary answer directly, so it is deterministic and cheap enough
// to run for every surface.
package scrolltest

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// DefaultTailEvents is the number of same-direction events a hard Magic Mouse
// or trackpad flick can queue.
const DefaultTailEvents = 300

// Wheel builds a vertical wheel event at a screen/local point.
func Wheel(x, y int, down bool) tea.MouseWheelMsg {
	button := tea.MouseWheelUp
	if down {
		button = tea.MouseWheelDown
	}
	return tea.MouseWheelMsg{X: x, Y: y, Button: button}
}

// Tail describes one stress run against a surface already sitting at a
// boundary.
type Tail struct {
	// Name labels the surface under test in failure messages.
	Name string

	// X, Y is the pointer position, in whatever coordinate space Dropped
	// expects (screen coordinates for app overlays, header-adjusted local
	// coordinates for plugins).
	X, Y int

	// Down is the direction of the inertial tail. The reverse event is its
	// opposite.
	Down bool

	// Events is how many tail events to feed; zero means DefaultTailEvents.
	Events int

	// Dropped reports whether the surface would discard this exact event before
	// Update and View. Pass a plugin's WheelAtBoundary, a modal's
	// WheelAtBoundary closure, or a wrapper around app.FilterInput.
	Dropped func(tea.MouseWheelMsg) bool
}

// Run feeds the same-direction tail and then one reverse event, asserting that
// every tail event is dropped and the reverse event survives.
//
// It reports the first failing event rather than every one, so a broken surface
// produces one readable failure instead of hundreds.
func Run(t testing.TB, tail Tail) {
	t.Helper()
	if tail.Dropped == nil {
		t.Fatalf("%s: stress fixture needs a Dropped function", tail.Name)
	}
	events := tail.Events
	if events <= 0 {
		events = DefaultTailEvents
	}
	for i := range events {
		if !tail.Dropped(Wheel(tail.X, tail.Y, tail.Down)) {
			t.Fatalf("%s: inertial tail event %d of %d reached Update; the boundary must drop the whole tail", tail.Name, i+1, events)
		}
	}
	if tail.Dropped(Wheel(tail.X, tail.Y, !tail.Down)) {
		t.Fatalf("%s: the first reverse event after the boundary was dropped; reverse motion must stay immediate", tail.Name)
	}
}
