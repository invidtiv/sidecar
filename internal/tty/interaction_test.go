package tty

import (
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

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

// A terminal surface reserves the scrollbar's column whatever it is drawing, and
// never reports a width no pane could be sized to.
func TestContentWidthReservesTheScrollbarColumn(t *testing.T) {
	tests := []struct{ surface, want int }{
		{80, 79}, {2, 1}, {1, 1}, {0, 0},
	}
	for _, tt := range tests {
		if got := ContentWidth(tt.surface); got != tt.want {
			t.Errorf("ContentWidth(%d) = %d, want %d", tt.surface, got, tt.want)
		}
	}
}

func TestGateKeyClassifiesEscapesAndMouseFragments(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name string
		in   KeyGateInput
		want KeyGate
	}{
		{"an ordinary key is input",
			KeyGateInput{Msg: tea.KeyPressMsg{Code: 'a', Text: "a"}, Now: now}, KeyGateSend},
		{"a first escape is held",
			KeyGateInput{Msg: tea.KeyPressMsg{Code: tea.KeyEscape}, Now: now}, KeyGateHoldEscape},
		{"a second escape leaves the mode",
			KeyGateInput{Msg: tea.KeyPressMsg{Code: tea.KeyEscape}, EscapePressed: true, Now: now},
			KeyGateExitDoubleEscape},
		{"a mouse report fragment is dropped",
			KeyGateInput{Msg: tea.KeyPressMsg{Code: '[', Text: "[<35;10;20M"}, Now: now}, KeyGateDrop},
		{"a bracket right after an escape is a CSI continuation",
			KeyGateInput{Msg: tea.KeyPressMsg{Code: '[', Text: "["},
				EscapePressed: true, EscapeAt: now, Now: now}, KeyGateDrop},
		{"a bracket right after a mouse event is a leak",
			KeyGateInput{Msg: tea.KeyPressMsg{Code: '[', Text: "["},
				LastMouseAt: now, Now: now}, KeyGateDrop},
		{"a bracket typed on its own is typing",
			KeyGateInput{Msg: tea.KeyPressMsg{Code: '[', Text: "["},
				EscapePressed: true, EscapeAt: now.Add(-time.Second),
				LastMouseAt: now.Add(-time.Second), Now: now}, KeyGateSend},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GateKey(tt.in); got != tt.want {
				t.Errorf("GateKey = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestShouldSnapBackOnlyForTyping(t *testing.T) {
	settled := 2 * SnapBackCooldown
	tests := []struct {
		name        string
		msg         tea.KeyPressMsg
		sinceScroll time.Duration
		want        bool
	}{
		{"a typed character", tea.KeyPressMsg{Code: 'a', Text: "a"}, settled, true},
		{"enter", tea.KeyPressMsg{Code: tea.KeyEnter}, settled, true},
		{"a chord", tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, settled, true},
		{"escape may open a mouse report", tea.KeyPressMsg{Code: tea.KeyEscape}, settled, false},
		{"a mouse fragment", tea.KeyPressMsg{Code: '[', Text: "[<35;10;20M"}, settled, false},
		{"multi-character text", tea.KeyPressMsg{Code: 'a', Text: "abc"}, settled, false},
		{"anything mid-flick", tea.KeyPressMsg{Code: 'a', Text: "a"}, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShouldSnapBack(tt.msg, tt.sinceScroll); got != tt.want {
				t.Errorf("ShouldSnapBack = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMapScrollbackKeyNeedsShiftAndSizesPagesWithContext(t *testing.T) {
	shift := func(code rune) tea.KeyPressMsg {
		return tea.KeyPressMsg{Code: code, Mod: tea.ModShift}
	}
	if _, ok := MapScrollbackKey(ScrollbackLive, tea.KeyPressMsg{Code: tea.KeyUp}, 20); ok {
		t.Fatal("an unshifted key was taken from the pane")
	}
	if _, ok := MapScrollbackKey(ScrollbackLive, shift('a'), 20); ok {
		t.Fatal("shift+a was read as a scrollback key")
	}

	tests := []struct {
		name string
		code rune
		want ScrollbackMove
	}{
		{"up walks back one row", tea.KeyUp, ScrollbackMove{Rows: 1}},
		{"down walks towards live", tea.KeyDown, ScrollbackMove{Rows: -1}},
		{"a page keeps one row of context", tea.KeyPgUp, ScrollbackMove{Rows: 19}},
		{"a page down likewise", tea.KeyPgDown, ScrollbackMove{Rows: -19}},
		{"home is the oldest output", tea.KeyHome, ScrollbackMove{ToOldest: true}},
		{"end is the live edge", tea.KeyEnd, ScrollbackMove{ToLive: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := MapScrollbackKey(ScrollbackLive, shift(tt.code), 20)
			if !ok || got != tt.want {
				t.Errorf("MapScrollbackKey = %+v (ok=%v), want %+v", got, ok, tt.want)
			}
		})
	}

	// A surface with no room for a page still moves.
	if got, _ := MapScrollbackKey(ScrollbackLive, shift(tea.KeyPgUp), 1); got.Rows != 1 {
		t.Errorf("a one-row surface paged by %d rows, want 1", got.Rows)
	}
}

// One rule answers both states. The navigation keys reach the same moves in
// each; what differs is the shift a live pane requires, because while a pane is
// taking input every unshifted key is its own.
func TestMapScrollbackKeyAnswersBothStates(t *testing.T) {
	const rows = 20
	tests := []struct {
		name    string
		state   ScrollbackState
		key     tea.KeyPressMsg
		want    ScrollbackMove
		claimed bool
	}{
		{"live takes a shifted page up", ScrollbackLive, tea.KeyPressMsg{Code: tea.KeyPgUp, Mod: tea.ModShift}, ScrollbackMove{Rows: 19}, true},
		{"live leaves a bare page up to the pane", ScrollbackLive, tea.KeyPressMsg{Code: tea.KeyPgUp}, ScrollbackMove{}, false},
		{"watched takes the same key bare", ScrollbackWatched, tea.KeyPressMsg{Code: tea.KeyPgUp}, ScrollbackMove{Rows: 19}, true},
		{"watched takes it shifted too", ScrollbackWatched, tea.KeyPressMsg{Code: tea.KeyPgUp, Mod: tea.ModShift}, ScrollbackMove{Rows: 19}, true},
		{"watched pages down by a page", ScrollbackWatched, tea.KeyPressMsg{Code: tea.KeyPgDown}, ScrollbackMove{Rows: -19}, true},
		{"watched home is the oldest output", ScrollbackWatched, tea.KeyPressMsg{Code: tea.KeyHome}, ScrollbackMove{ToOldest: true}, true},
		{"watched end is the live edge", ScrollbackWatched, tea.KeyPressMsg{Code: tea.KeyEnd}, ScrollbackMove{ToLive: true}, true},
		{"watched k walks back one row", ScrollbackWatched, tea.KeyPressMsg{Code: 'k', Text: "k"}, ScrollbackMove{Rows: 1}, true},
		{"watched j walks towards live", ScrollbackWatched, tea.KeyPressMsg{Code: 'j', Text: "j"}, ScrollbackMove{Rows: -1}, true},
		{"watched g is the oldest output", ScrollbackWatched, tea.KeyPressMsg{Code: 'g', Text: "g"}, ScrollbackMove{ToOldest: true}, true},
		{"watched G is the live edge", ScrollbackWatched, tea.KeyPressMsg{Code: 'g', Text: "G", Mod: tea.ModShift}, ScrollbackMove{ToLive: true}, true},
		{"watched ctrl+u takes half the surface", ScrollbackWatched, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}, ScrollbackMove{Rows: 10}, true},
		{"watched ctrl+d takes half the surface", ScrollbackWatched, tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}, ScrollbackMove{Rows: -10}, true},
		// A letter is text the pane is owed, and shift only capitalises it.
		{"live leaves k to the pane", ScrollbackLive, tea.KeyPressMsg{Code: 'k', Text: "k"}, ScrollbackMove{}, false},
		{"live leaves K to the pane", ScrollbackLive, tea.KeyPressMsg{Code: 'k', Text: "K", Mod: tea.ModShift}, ScrollbackMove{}, false},
		{"live leaves ctrl+d to the pane", ScrollbackLive, tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}, ScrollbackMove{}, false},
		{"a watched pane still types nothing anywhere else", ScrollbackWatched, tea.KeyPressMsg{Code: 'a', Text: "a"}, ScrollbackMove{}, false},
		{"alt+j is a chord, not a pager key", ScrollbackWatched, tea.KeyPressMsg{Code: 'j', Mod: tea.ModAlt}, ScrollbackMove{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := MapScrollbackKey(tt.state, tt.key, rows)
			if ok != tt.claimed || got != tt.want {
				t.Errorf("MapScrollbackKey = %+v (ok=%v), want %+v (ok=%v)", got, ok, tt.want, tt.claimed)
			}
		})
	}
}

// The two sets differ by the shift a live pane requires and by nothing else: the
// keys no pane types answer in both states, and each pager alias reaches a move
// the navigation set already has.
func TestScrollbackSetsDifferOnlyByShift(t *testing.T) {
	const rows = 20
	navigation := []rune{tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown, tea.KeyHome, tea.KeyEnd}
	for _, code := range navigation {
		bare := tea.KeyPressMsg{Code: code}
		shifted := tea.KeyPressMsg{Code: code, Mod: tea.ModShift}

		live, liveOK := MapScrollbackKey(ScrollbackLive, shifted, rows)
		if !liveOK {
			t.Fatalf("a live pane refused %v", shifted)
		}
		if _, ok := MapScrollbackKey(ScrollbackLive, bare, rows); ok {
			t.Fatalf("a live pane took the unshifted %v", bare)
		}
		watched, watchedOK := MapScrollbackKey(ScrollbackWatched, bare, rows)
		if !watchedOK {
			t.Fatalf("a watched pane refused %v", bare)
		}
		if live != watched {
			t.Fatalf("%v moved by %+v live and %+v watched", bare, live, watched)
		}
	}

	aliases := map[tea.KeyPressMsg]rune{
		{Code: 'k', Text: "k"}:                    tea.KeyUp,
		{Code: 'j', Text: "j"}:                    tea.KeyDown,
		{Code: 'g', Text: "g"}:                    tea.KeyHome,
		{Code: 'g', Text: "G", Mod: tea.ModShift}: tea.KeyEnd,
	}
	for alias, code := range aliases {
		got, ok := MapScrollbackKey(ScrollbackWatched, alias, rows)
		if !ok {
			t.Fatalf("a watched pane refused %v", alias)
		}
		want, _ := MapScrollbackKey(ScrollbackWatched, tea.KeyPressMsg{Code: code}, rows)
		if got != want {
			t.Fatalf("%v moved by %+v, want %+v", alias, got, want)
		}
	}
}

func TestWheelBurstCoalescesAFlickWithoutLosingDistance(t *testing.T) {
	var burst WheelBurst
	start := time.Now()

	// The first notch of a gesture is applied straight away: a single click of a
	// wheel must never wait for a second one.
	if delta, ok := burst.Add(-3, start); !ok || delta != -3 {
		t.Fatalf("first notch = %d (applied=%v), want -3 applied", delta, ok)
	}
	// Everything inside the window is held, and nothing is dropped.
	if _, ok := burst.Add(-3, start.Add(time.Millisecond)); ok {
		t.Fatal("a notch inside the debounce window was applied on its own")
	}
	if _, ok := burst.Add(-3, start.Add(2*time.Millisecond)); ok {
		t.Fatal("a second notch inside the window was applied on its own")
	}
	delta, ok := burst.Add(-3, start.Add(WheelDebounceInterval))
	if !ok || delta != -9 {
		t.Fatalf("flushed delta = %d (applied=%v), want the two held-back notches with this one (-9)",
			delta, ok)
	}

	// A flick under way is filtered harder, so its remaining events land sooner.
	for i := range WheelBurstThreshold {
		burst.Add(-3, start.Add(WheelDebounceInterval+time.Duration(i)*time.Millisecond))
	}
	if _, ok := burst.Add(-3, start.Add(WheelDebounceInterval+WheelBurstDebounce)); !ok {
		t.Fatal("a burst was still debounced at the slower non-burst interval")
	}

	if _, running := burst.Remaining(start.Add(WheelDebounceInterval)); !running {
		t.Error("the flick reported no remaining window while it was still going")
	}
	if _, running := burst.Remaining(start.Add(2 * WheelBurstTimeout)); running {
		t.Error("the flick never ended")
	}
	burst.Reset()
	if _, running := burst.Remaining(start); running {
		t.Error("a reset burst is still running")
	}
}

func TestCopyNoticeSaysTheSameThingOnEverySurface(t *testing.T) {
	empty := DefaultConfig().Notice(CopyResult{Empty: true})
	if empty.Message != "Nothing selected — ctrl+a selects all output" || empty.IsError {
		t.Errorf("empty copy notice = %+v", empty)
	}
	failed := DefaultConfig().Notice(CopyResult{Err: errors.New("clipboard unavailable")})
	if failed.Message != "Copy failed: clipboard unavailable" || !failed.IsError {
		t.Errorf("failed copy notice = %+v", failed)
	}
	copied := DefaultConfig().Notice(CopyResult{Lines: 3})
	if copied.Message != "Copied 3 line(s)" || copied.IsError {
		t.Errorf("successful copy notice = %+v", copied)
	}
	if copied.Duration != CopyNoticeDuration {
		t.Errorf("copy notice duration = %s, want %s", copied.Duration, CopyNoticeDuration)
	}
}
