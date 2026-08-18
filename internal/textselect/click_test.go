package textselect

import "testing"

func TestResolveClickCoversEverySurfaceState(t *testing.T) {
	tests := []struct {
		name string
		in   ClickIntent
		want ClickResolution
	}{
		{"a passive terminal activates", ClickIntent{}, ClickActivate},
		{"a live terminal with no mouse reporting does nothing",
			ClickIntent{Live: true}, ClickNone},
		{"a live terminal whose app tracks the mouse forwards",
			ClickIntent{Live: true, MouseReporting: true}, ClickForward},
		{"a modified click is a selection gesture, never an activation",
			ClickIntent{Modified: true}, ClickNone},
		{"a modified click is not the app's either",
			ClickIntent{Live: true, MouseReporting: true, Modified: true}, ClickNone},
		{"a link outranks the application",
			ClickIntent{Live: true, MouseReporting: true, LinkClaimed: true}, ClickNone},
		{"a link outranks activation",
			ClickIntent{LinkClaimed: true}, ClickNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveClick(tt.in); got != tt.want {
				t.Errorf("ResolveClick(%+v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
