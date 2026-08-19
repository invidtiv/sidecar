package tty

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/ui"
)

// The base a buffer keeps is what a viewport has to be told, or a highlight
// recorded in absolute coordinates is drawn short by exactly it.
func TestBufferBaseReportsTheCoordinateSpaceAGestureRecordsIn(t *testing.T) {
	relative := NewOutputBuffer(100)
	relative.ApplySnapshot(PaneSnapshot{Output: "one\ntwo\nthree"})
	if base, total := BufferBase(relative); base != 0 || total != 3 {
		t.Fatalf("relative buffer base=%d total=%d, want 0 and its own line count", base, total)
	}
	if BufferAbsolute(relative) {
		t.Fatal("a buffer with no absolute range reported one")
	}

	absolute := NewOutputBuffer(100)
	absolute.ApplySnapshot(CaptureSnapshot(CaptureInput{
		Output: "one\ntwo\nthree", BaseLine: 40, Absolute: true, PaneHeight: 3,
	}))
	base, total := BufferBase(absolute)
	if base != 40 || total != 43 {
		t.Fatalf("absolute buffer base=%d total=%d, want 40 and 43", base, total)
	}
	// The line a click on the first drawn row records, which is what the base has
	// to lift a drawn row back to.
	if got := AbsoluteLine(absolute, 0); got != base {
		t.Fatalf("first line = %d, want the base %d", got, base)
	}

	if base, total := BufferBase(nil); base != 0 || total != 0 {
		t.Fatalf("nil buffer base=%d total=%d", base, total)
	}
}

// A scroll made outside a pointer gesture keeps a selection that names the same
// rows wherever the window goes, and drops one that cannot.
func TestScrollKeepsOnlyAnAbsoluteSelection(t *testing.T) {
	absolute := NewOutputBuffer(100)
	absolute.ApplySnapshot(CaptureSnapshot(CaptureInput{
		Output: "one\ntwo", BaseLine: 12, Absolute: true, PaneHeight: 2,
	}))
	if !ScrollKeepsSelection(absolute) {
		t.Fatal("a selection in absolute coordinates was dropped by a scroll")
	}

	relative := NewOutputBuffer(100)
	relative.ApplySnapshot(PaneSnapshot{Output: "one\ntwo"})
	if ScrollKeepsSelection(relative) {
		t.Fatal("a selection over renumbering lines survived a scroll")
	}
	if ScrollKeepsSelection(nil) {
		t.Fatal("a selection with no buffer behind it survived a scroll")
	}
}

// The regions a host calls terminal are the host's; the verdict is not.
func TestPressLeavesTerminalOnlyOutsideTheNamedRegions(t *testing.T) {
	const preview, panel, sidebar = "preview", "panel", "sidebar"
	if PressLeavesTerminal(preview, preview, panel) {
		t.Fatal("a press on a terminal region read as a press away from it")
	}
	if PressLeavesTerminal(panel, preview, panel) {
		t.Fatal("a press on the second terminal region read as a press away from it")
	}
	if !PressLeavesTerminal(sidebar, preview, panel) {
		t.Fatal("a press on the sidebar did not leave the terminal")
	}
	if !PressLeavesTerminal(preview) {
		t.Fatal("a host that draws no terminal kept a press")
	}
}

// The cheap half of the scrollback rule answers exactly what the full one
// claims, so a host can gate its layout work on it.
func TestIsScrollbackKeyMatchesTheKeysTheMoveClaims(t *testing.T) {
	keys := []tea.KeyPressMsg{
		{Code: tea.KeyUp, Mod: tea.ModShift},
		{Code: tea.KeyPgDown, Mod: tea.ModShift},
		{Code: tea.KeyHome, Mod: tea.ModShift},
		{Code: tea.KeyEnd, Mod: tea.ModShift},
		{Code: tea.KeyUp},
		{Code: 'a', Text: "a"},
		{Code: 'a', Text: "A", Mod: tea.ModShift},
	}
	keys = append(keys,
		tea.KeyPressMsg{Code: 'j', Text: "j"},
		tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl},
		tea.KeyPressMsg{Code: tea.KeyPgUp},
	)
	for _, state := range []ScrollbackState{ScrollbackLive, ScrollbackWatched} {
		for _, key := range keys {
			_, want := MapScrollbackKey(state, key, 20)
			if got := IsScrollbackKey(state, key); got != want {
				t.Fatalf("IsScrollbackKey(%v, %v) = %v, want %v", state, key, got, want)
			}
		}
	}
}

// A held-back notch is retained, not dropped: the next event that gets through
// carries it, so a flick travels the distance the hand asked for.
func TestWheelBurstRetainsTheDeltaItHeldBack(t *testing.T) {
	var burst WheelBurst
	start := time.Now()
	if delta, flushed := burst.Add(-3, start); !flushed || delta != -3 {
		t.Fatalf("first notch delta=%d flushed=%v", delta, flushed)
	}
	if delta, flushed := burst.Add(-3, start.Add(WheelDebounceInterval/2)); flushed || delta != 0 {
		t.Fatalf("a notch inside the debounce window was applied: delta=%d", delta)
	}
	if got := burst.Pending(); got != -3 {
		t.Fatalf("pending = %d, want the held-back notch", got)
	}
	delta, flushed := burst.Add(-3, start.Add(2*WheelDebounceInterval))
	if !flushed || delta != -6 {
		t.Fatalf("flushed delta = %d (flushed=%v), want both notches", delta, flushed)
	}
	if got := burst.Pending(); got != 0 {
		t.Fatalf("pending after a flush = %d", got)
	}
}

// Who owns a click means the same thing under control mode and under the
// polling fallback. The fallback's capture bytes never carry the DECSET
// sequences that turn tracking on, so the flag tmux reports with the capture is
// the only thing that can answer — and it is read on every poll, because an
// application turns tracking on and off without redrawing a cell.
func TestAPollingCaptureCarriesMouseOwnershipToTheApplication(t *testing.T) {
	input := &fakeTerminalInputSender{}
	m := New(nil)
	m.input = input
	m.Enter("session", "%1")
	m.State.OutputBuf.Update("prompt$")

	// The same unchanged screen the app was already showing when it asked for
	// the mouse: nothing in these bytes says so.
	poll := func(output string, reporting bool) {
		m.Update(CaptureResultMsg{
			Scope: m.Scope(), PollGeneration: m.State.PollGeneration, Target: "%1",
			Output: output, PaneWidth: 80, PaneHeight: 24, MouseReporting: reporting,
		})
	}

	poll("prompt$", true)
	if !m.PaneMouseReporting() {
		t.Fatal("a capture whose flag reports mouse tracking did not give the app the pointer")
	}
	m.SendClick(3, 4)
	if len(input.calls) != 1 || input.calls[0].kind != "mouse" {
		t.Fatalf("the click did not reach the application: %#v", input.calls)
	}

	poll("prompt$", false)
	if m.PaneMouseReporting() {
		t.Fatal("tracking turned off on an unchanged screen was not noticed")
	}
}

// The input reader can split one SGR mouse report across reads and deliver the
// halves as ordinary key text. Reassembly lives in the component, so every
// surface that hands it the keyboard is protected: a host that only had the
// one-key gate leaked the report's tail into the pane as literal text.
func TestASplitMouseReportNeverReachesThePane(t *testing.T) {
	const report = "[<65;33;12M"
	for split := 1; split < len(report); split++ {
		input := &fakeTerminalInputSender{}
		m := New(nil)
		m.input = input
		m.Enter("session", "%1")
		m.NoteMouseActivity()

		for _, text := range []string{report[:split], report[split:]} {
			runes := []rune(text)
			if cmd := m.Update(tea.KeyPressMsg{Code: runes[0], Text: text}); cmd != nil {
				t.Fatalf("split at %d: %q produced input for the pane", split, text)
			}
		}
		if len(input.calls) != 0 {
			t.Fatalf("split at %d reached the pane: %#v", split, input.calls)
		}
	}
}

// Hosts hand the engine whatever buffer they have, and a host with no terminal
// open has a nil *OutputBuffer — which arrives inside the Buffer interface as a
// typed nil, not as the nil the engine's own guards test for. Every entry point
// the hosts reach has to answer it rather than dereference it.
func TestATypedNilBufferAnswersEveryEntryPoint(t *testing.T) {
	// Not the same value as a nil Buffer: this one carries a type, so every
	// `buf == nil` guard in the engine answers false for it.
	var buf Buffer = (*OutputBuffer)(nil)

	if count := buf.LineCount(); count != 0 {
		t.Errorf("line count = %d, want 0", count)
	}
	if base, total := BufferBase(buf); base != 0 || total != 0 {
		t.Errorf("buffer base = %d, total = %d, want 0 and 0", base, total)
	}
	if BufferAbsolute(buf) || ScrollKeepsSelection(buf) {
		t.Error("a buffer that holds nothing claimed absolute coordinates")
	}
	if text, ok := LineTextAt(buf, 0); ok || text != "" {
		t.Errorf("line text = %q, ok = %v, want nothing", text, ok)
	}
	if _, _, ok := SelectAllSpan(buf, DefaultTabWidth); ok {
		t.Error("select-all spanned a buffer with no lines")
	}
	selection := &ui.SelectionState{}
	selection.SelectRange(ui.SelectionPoint{Line: 0, Col: 0}, ui.SelectionPoint{Line: 2, Col: 3}, false)
	if lines := SelectedLines(buf, selection, DefaultTabWidth); lines != nil {
		t.Errorf("selected lines = %q, want none", lines)
	}
	geometry := Geometry{Content: mouse.Rect{X: 0, Y: 0, W: 20, H: 3}, Start: 0, End: 3, TabWidth: DefaultTabWidth}
	if cell, ok := ClampedCellAt(geometry, buf, 4, 1); !ok || cell.Line != 1 || cell.Col != 0 {
		t.Errorf("clamped cell = %+v, ok = %v, want the drawn row at column 0", cell, ok)
	}
}
