package tty

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestNew(t *testing.T) {
	// Test with nil config (uses defaults)
	m := New(nil)
	if m == nil {
		t.Fatal("expected non-nil model")
	}
	if m.Config.ExitKey != "ctrl+\\" {
		t.Errorf("expected default ExitKey, got %s", m.Config.ExitKey)
	}
	if m.Config.ScrollbackLines != 600 {
		t.Errorf("expected default ScrollbackLines=600, got %d", m.Config.ScrollbackLines)
	}
}

func TestNew_WithConfig(t *testing.T) {
	cfg := &Config{
		ExitKey:         "ctrl+q",
		ScrollbackLines: 1000,
	}
	m := New(cfg)
	if m.Config.ExitKey != "ctrl+q" {
		t.Errorf("expected ExitKey='ctrl+q', got %s", m.Config.ExitKey)
	}
	if m.Config.ScrollbackLines != 1000 {
		t.Errorf("expected ScrollbackLines=1000, got %d", m.Config.ScrollbackLines)
	}
	// Non-overridden values should be defaults
	if m.Config.AttachKey != "ctrl+]" {
		t.Errorf("expected default AttachKey, got %s", m.Config.AttachKey)
	}
}

func TestModel_IsActive(t *testing.T) {
	m := New(nil)

	// Should be inactive initially
	if m.IsActive() {
		t.Error("expected IsActive=false initially")
	}

	// After setting state
	m.State = &State{Active: true}
	if !m.IsActive() {
		t.Error("expected IsActive=true after setting state")
	}

	// After setting active=false
	m.State.Active = false
	if m.IsActive() {
		t.Error("expected IsActive=false after setting active=false")
	}
}

func TestModel_Exit(t *testing.T) {
	m := New(nil)
	m.State = &State{
		Active:        true,
		TargetSession: "test-session",
	}

	m.Exit()

	if m.State != nil {
		t.Error("expected State=nil after Exit")
	}
}

func TestModel_GetTarget(t *testing.T) {
	m := New(nil)

	// Inactive model returns empty string
	if got := m.GetTarget(); got != "" {
		t.Errorf("expected empty target for inactive model, got %s", got)
	}

	// With pane ID
	m.State = &State{
		Active:        true,
		TargetPane:    "%5",
		TargetSession: "my-session",
	}
	if got := m.GetTarget(); got != "%5" {
		t.Errorf("expected pane ID '%s', got %s", "%5", got)
	}

	// Without pane ID, returns session
	m.State.TargetPane = ""
	if got := m.GetTarget(); got != "my-session" {
		t.Errorf("expected session 'my-session', got %s", got)
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.ExitKey != "ctrl+\\" {
		t.Errorf("unexpected default ExitKey: %s", cfg.ExitKey)
	}
	if cfg.AttachKey != "ctrl+]" {
		t.Errorf("unexpected default AttachKey: %s", cfg.AttachKey)
	}
	if cfg.CopyKey != "alt+c" {
		t.Errorf("unexpected default CopyKey: %s", cfg.CopyKey)
	}
	if cfg.PasteKey != "alt+v" {
		t.Errorf("unexpected default PasteKey: %s", cfg.PasteKey)
	}
	if cfg.ScrollbackLines != 600 {
		t.Errorf("unexpected default ScrollbackLines: %d", cfg.ScrollbackLines)
	}
}

func TestModelIgnoresMessagesFromAnotherOwner(t *testing.T) {
	first := New(nil)
	second := New(nil)
	first.Enter("shared-target", "")
	second.Enter("shared-target", "")

	second.State.OutputBuf.Write("second output")
	first.Update(CaptureResultMsg{
		Scope:          second.Scope(),
		PollGeneration: first.State.PollGeneration,
		Target:         "shared-target",
		Output:         "foreign output",
	})
	if got := first.State.OutputBuf.String(); got != "" {
		t.Fatalf("foreign capture changed first model: %q", got)
	}

	first.Update(SessionDeadMsg{Scope: second.Scope()})
	if !first.IsActive() {
		t.Fatal("foreign session-dead message exited first model")
	}
}

func TestModelIgnoresMessagesFromPreviousActivation(t *testing.T) {
	model := New(nil)
	model.Enter("same-target", "")
	stale := model.Scope()
	model.Exit()
	model.Enter("same-target", "")

	model.Update(CaptureResultMsg{
		Scope:          stale,
		PollGeneration: model.State.PollGeneration,
		Target:         "same-target",
		Output:         "stale output",
	})
	if got := model.State.OutputBuf.String(); got != "" {
		t.Fatalf("stale capture changed re-entered model: %q", got)
	}

	model.Update(SessionDeadMsg{Scope: stale})
	if !model.IsActive() {
		t.Fatal("stale session-dead message exited re-entered model")
	}
}

func TestModelAcceptsCurrentScopedCapture(t *testing.T) {
	model := New(nil)
	model.Enter("current", "")
	model.Update(CaptureResultMsg{
		Scope:          model.Scope(),
		PollGeneration: model.State.PollGeneration,
		Target:         "current",
		Output:         "current output",
	})
	if got := model.State.OutputBuf.String(); got != "current output" {
		t.Fatalf("current capture = %q, want current output", got)
	}
}

func TestModelRejectsOutOfOrderCaptureResult(t *testing.T) {
	model := New(nil)
	model.Enter("current", "")
	scope := model.Scope()

	// Model a newer poll superseding an older capture that is still in flight.
	model.State.PollGeneration = 2
	model.Update(CaptureResultMsg{
		Scope:          scope,
		PollGeneration: 2,
		Target:         "current",
		Output:         "newer output",
		CursorRow:      7,
	})
	if got := model.State.OutputBuf.String(); got != "newer output" {
		t.Fatalf("newer capture = %q, want newer output", got)
	}

	model.Update(CaptureResultMsg{
		Scope:          scope,
		PollGeneration: 1,
		Target:         "current",
		Output:         "older output",
		CursorRow:      1,
	})
	if got := model.State.OutputBuf.String(); got != "newer output" {
		t.Fatalf("out-of-order capture replaced newer output: %q", got)
	}
	if got := model.State.CursorRow; got != 7 {
		t.Fatalf("out-of-order capture replaced cursor row: %d", got)
	}
}

func TestModelViewShowsBottomOfScrollback(t *testing.T) {
	model := New(nil)
	model.Height = 2
	model.Enter("current", "")
	model.State.CursorVisible = false
	model.State.OutputBuf.Write("oldest\nmiddle\nnewest")

	if got := model.View(); got != "middle\nnewest" {
		t.Fatalf("View() = %q, want bottom two lines", got)
	}
}

func TestModelExposesNativeCursorWithoutPaintingContent(t *testing.T) {
	model := New(nil)
	model.Width = 8
	model.Height = 3
	model.Enter("current", "")
	model.State.OutputBuf.Write("one\ntwo\nthree")
	model.State.CursorRow = 1
	model.State.CursorCol = 3
	model.State.CursorVisible = true
	model.State.PaneHeight = 3

	if got := model.View(); got != "one\ntwo\nthree" {
		t.Fatalf("View() painted cursor into content: %q", got)
	}
	cursor := model.Cursor()
	if cursor == nil || cursor.X != 3 || cursor.Y != 1 ||
		cursor.Shape != tea.CursorBlock || !cursor.Blink {
		t.Fatalf("Cursor() = %#v", cursor)
	}
	if mode := model.PreferredMouseMode(); mode != tea.MouseModeCellMotion {
		t.Fatalf("PreferredMouseMode() = %v, want cell motion", mode)
	}
}

func TestModelNativeCursorAdjustsPaneHeightAndBounds(t *testing.T) {
	model := New(nil)
	model.Width = 5
	model.Height = 2
	model.Enter("current", "")
	model.State.CursorVisible = true
	model.State.CursorRow = 4
	model.State.CursorCol = 9
	model.State.PaneHeight = 5

	cursor := model.Cursor()
	if cursor == nil || cursor.X != 4 || cursor.Y != 1 {
		t.Fatalf("adjusted Cursor() = %#v, want (4,1)", cursor)
	}
	// A cursor above the pane's tail pulls the window up rather than falling off
	// the top: the row being typed into has to stay visible (td-73fa86).
	model.State.CursorRow = 1
	if cursor := model.Cursor(); cursor == nil || cursor.Y != 1 {
		t.Fatalf("anchored Cursor() = %#v, want Y=1", cursor)
	}
	// A cursor row past the pane itself is out of bounds and owns no cell.
	model.State.CursorRow = 9
	if cursor := model.Cursor(); cursor != nil {
		t.Fatalf("off-viewport Cursor() = %#v, want nil", cursor)
	}
	model.State.CursorRow = 4
	model.Exit()
	if cursor := model.Cursor(); cursor != nil {
		t.Fatalf("inactive Cursor() = %#v, want nil", cursor)
	}
	if mode := model.PreferredMouseMode(); mode != tea.MouseModeNone {
		t.Fatalf("inactive PreferredMouseMode() = %v, want none", mode)
	}
}

// A host with no attach path clears AttachKey, and the chord is then the pane's
// input like any other key rather than a silent third way out.
func TestEmptyAttachKeyIsForwardedToThePane(t *testing.T) {
	input := &fakeTerminalInputSender{}
	m := New(nil)
	m.Config.AttachKey = ""
	m.input = input
	m.Enter("session", "%1")

	attached := false
	m.OnAttach = func() tea.Cmd { attached = true; return nil }
	m.Update(tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl})

	if attached || !m.IsActive() {
		t.Fatal("ctrl+] ended the mode even though no attach key is configured")
	}
	if len(input.calls) == 0 || input.calls[0].kind != "keys" {
		t.Fatalf("ctrl+] did not reach the pane: %#v", input.calls)
	}
}

// A wheel notch reaches the application as the SGR report a real terminal would
// send — but only once a host has hit-tested it. A raw mouse message offered to
// the component is activity and nothing more: the host owns which gesture was a
// selection, and which viewport cell the pointer was actually over.
func TestWheelReachesAnApplicationThatAskedForMouseEvents(t *testing.T) {
	input := &fakeTerminalInputSender{}
	m := New(nil)
	m.input = input
	m.Enter("session", "%1")
	m.State.MouseReportingEnabled = true

	m.Update(tea.MouseWheelMsg{X: 4, Y: 2, Button: tea.MouseWheelUp})
	if len(input.calls) != 0 {
		t.Fatalf("a raw wheel message was forwarded without the host routing it: %#v", input.calls)
	}
	if m.State.LastMouseEventTime.IsZero() {
		t.Fatal("a mouse message did not count as mouse activity, so the bare-[ gate is blind to it")
	}

	m.SendWheelNotches(true, 5, 3, 2)
	m.SendWheelNotches(false, 5, 3, 1)
	if len(input.calls) != 2 {
		t.Fatalf("wheel calls = %#v", input.calls)
	}
	if got := input.calls[0]; got.kind != "wheel" || !got.up || got.col != 5 || got.row != 3 || got.notches != 2 {
		t.Fatalf("wheel up call = %#v, want two notches at the pane's own 5,3", got)
	}
	if got := input.calls[1]; got.kind != "wheel" || got.up {
		t.Fatalf("wheel down call = %#v", got)
	}
}

func TestNormalizeBackgroundMode(t *testing.T) {
	cases := []struct {
		in   BackgroundMode
		want BackgroundMode
	}{
		{"", BackgroundAuto},
		{BackgroundAuto, BackgroundAuto},
		{BackgroundBounded, BackgroundBounded},
		{BackgroundNever, BackgroundNever},
		{"plaid", BackgroundAuto},
	}
	for _, tc := range cases {
		if got := NormalizeBackgroundMode(tc.in); got != tc.want {
			t.Errorf("NormalizeBackgroundMode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDefaultConfigCarriesBackgroundDefaults(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Backgrounds != BackgroundAuto {
		t.Errorf("DefaultConfig.Backgrounds = %q, want auto", cfg.Backgrounds)
	}
	if cfg.BackgroundSpanMax != DefaultBackgroundSpanMax {
		t.Errorf("DefaultConfig.BackgroundSpanMax = %d, want %d", cfg.BackgroundSpanMax, DefaultBackgroundSpanMax)
	}
}
