package tty

import (
	"testing"

	"github.com/marcus/sidecar/internal/mouse"
)

func TestRouteWheelFallsBackToTheLocalViewport(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   WheelInput
		want WheelRoute
	}{
		{
			"a plain shell has not asked for mouse reports",
			WheelInput{Delta: -mouse.WheelScrollLines, InPane: true, WritesEnabled: true},
			WheelLocal,
		},
		{
			// Nothing here says whether the pane holds the keyboard, which is the
			// point: who owns a notch is a property of the pane.
			"an app that asked for reports owns the notch",
			WheelInput{Delta: -mouse.WheelScrollLines, MouseReporting: true, InPane: true, WritesEnabled: true},
			WheelPane,
		},
		{
			"writes disabled keeps every notch for the host's own window",
			WheelInput{Delta: -mouse.WheelScrollLines, MouseReporting: true, InPane: true},
			WheelLocal,
		},
		{
			"alt takes the wheel back from the app",
			WheelInput{Delta: -mouse.WheelScrollLines, MouseReporting: true, InPane: true, Alt: true, WritesEnabled: true},
			WheelLocal,
		},
		{
			"shift takes the wheel back from the app",
			WheelInput{Delta: -mouse.WheelScrollLines, MouseReporting: true, InPane: true, Shift: true, WritesEnabled: true},
			WheelLocal,
		},
		{
			"a notch over chrome is nobody's mouse report",
			WheelInput{Delta: -mouse.WheelScrollLines, MouseReporting: true, WritesEnabled: true},
			WheelLocal,
		},
		{
			"no notch at all",
			WheelInput{MouseReporting: true, InPane: true, WritesEnabled: true},
			WheelLocal,
		},
	} {
		route, _ := RouteWheel(tc.in)
		if route != tc.want {
			t.Errorf("%s: route = %v, want %v", tc.name, route, tc.want)
		}
	}
}

func TestRouteWheelReportsWholeNotches(t *testing.T) {
	_, notches := RouteWheel(WheelInput{
		Delta: 2 * mouse.WheelScrollLines, MouseReporting: true, InPane: true, WritesEnabled: true,
	})
	if notches != 2 {
		t.Errorf("notches = %d, want 2", notches)
	}

	_, capped := RouteWheel(WheelInput{
		Delta: 500 * mouse.WheelScrollLines, MouseReporting: true, InPane: true, WritesEnabled: true,
	})
	if capped != MaxWheelNotchesPerFlush {
		t.Errorf("a flicked trackpad sent %d notches, want at most %d", capped, MaxWheelNotchesPerFlush)
	}
}

func TestWheelNotchesNeverRoundsAScrollAway(t *testing.T) {
	if got := WheelNotches(1); got != 1 {
		t.Errorf("WheelNotches(1) = %d, want 1", got)
	}
	if got := WheelNotches(-mouse.WheelScrollLines); got != 1 {
		t.Errorf("WheelNotches of one notch up = %d, want 1", got)
	}
}
