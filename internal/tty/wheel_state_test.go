package tty

import (
	"reflect"
	"testing"
)

// Who owns a wheel notch is a property of the pane, and the rule is held to that
// here rather than in each host: WheelInput carries the gesture and the facts
// about the pane under the pointer, and nothing a host could answer differently
// depending on where the keyboard is. That is how the two states drifted apart —
// the question was asked only while a pane was live — so a field naming focus,
// interactivity or a mode would reopen it, and must be argued for rather than
// added.
func TestTheWheelRuleTakesNoAccountOfWhereTheKeyboardIs(t *testing.T) {
	want := map[string]bool{
		"Delta":          true,
		"Shift":          true,
		"Alt":            true,
		"MouseReporting": true,
		"InPane":         true,
		"WritesEnabled":  true,
	}
	input := reflect.TypeOf(WheelInput{})
	got := make(map[string]bool, input.NumField())
	for i := range input.NumField() {
		got[input.Field(i).Name] = true
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("WheelInput names %v, want exactly the gesture and the pane's own facts %v", got, want)
	}
}

// The route is the same answer whichever host asks it, so a host's own state
// cannot enter into it. Both surfaces' parity tests read their expectation from
// this function for exactly that reason; what is pinned here is that the answer
// depends on the pane's facts alone.
func TestTheRouteForAPaneIsTheSameAnswerEveryTimeItIsAsked(t *testing.T) {
	for _, reporting := range []bool{false, true} {
		for _, inPane := range []bool{false, true} {
			for _, writes := range []bool{false, true} {
				in := WheelInput{
					Delta: -3, MouseReporting: reporting, InPane: inPane, WritesEnabled: writes,
				}
				route, notches := RouteWheel(in)
				forwards := reporting && inPane && writes
				if (route == WheelPane) != forwards {
					t.Fatalf("RouteWheel(%+v) = %v, want forwarding=%v", in, route, forwards)
				}
				if again, againNotches := RouteWheel(in); again != route || againNotches != notches {
					t.Fatalf("RouteWheel(%+v) answered %v/%d then %v/%d", in, route, notches, again, againNotches)
				}
			}
		}
	}
}
