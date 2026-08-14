package overview

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/tty"
)

// Who owns a wheel notch is a property of the pane, not of where the keyboard
// is: a watched pane whose application has asked for mouse reports gets the
// notch at its own coordinates, and this surface's window stays on the live
// edge. Watched is the state this browser spends most of its time in, and it is
// where an agent that draws its own scrollback used to have this window dragged
// across its live frame.
func TestTheWheelOverAWatchedPaneRoutesToTheAppOrTheWindow(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	terminal.buffer.ApplySnapshot(tty.PaneSnapshot{Output: paneBody(60)})
	m.WorkspacesView(previewWide, previewTall)
	if m.PreviewInteractive() {
		t.Fatal("test premise: nobody is typing into this pane")
	}
	surface, ok := m.previewSurface()
	if !ok {
		t.Fatal("the rendered preview has no terminal surface")
	}
	notch := func(mod tea.KeyMod) {
		t.Helper()
		settleWheel()
		run(t, m, m.WorkspacesMouse(tea.MouseWheelMsg{
			X: surface.X + 2, Y: surface.Y + 3, Button: tea.MouseWheelUp, Mod: mod}))
	}

	// A watched plain shell has asked for nothing, so the notch is the window's,
	// exactly as it has always been.
	terminal.inputNoted = 0
	notch(0)
	if len(terminal.wheel) != 0 {
		t.Fatalf("a notch reached a pane that never asked for mouse events: %+v", terminal.wheel)
	}
	if m.preview.offset == 0 {
		t.Fatal("the wheel did nothing over a watched pane with no mouse reporting")
	}
	// A locally scrolled pane is being read too, and its capture cadence decays
	// from this clock.
	if terminal.inputNoted == 0 {
		t.Fatal("a local notch left the pane being recaptured at its idle tier")
	}

	// Once the application asks for mouse events the notch is its own, in its
	// own 1-indexed coordinates.
	terminal.reporting = true
	terminal.inputNoted = 0
	m.preview.offset = 0
	notch(0)
	if len(terminal.wheel) != 1 {
		t.Fatalf("the watched pane received wheel notches %+v, want one", terminal.wheel)
	}
	if got := terminal.wheel[0]; !got.up || got.col != 3 || got.row != 4 || got.notches < 1 {
		t.Fatalf("wheel notch = %+v, want an upward notch at the pane's own 3,4", got)
	}
	if m.preview.offset != 0 {
		t.Fatalf("the window moved to %d while the app owned the wheel", m.preview.offset)
	}
	if terminal.inputNoted == 0 {
		t.Fatal("the notch was not noted as input; the pane would be recaptured at its idle tier")
	}

	// While the app owns the wheel it owns what the pane shows, so a window left
	// scrolled back is pinned to the live frame rather than left over stale rows.
	m.preview.offset = 4
	notch(0)
	if m.preview.offset != 0 {
		t.Fatalf("the window stayed at %d while the app owned the wheel", m.preview.offset)
	}
	terminal.wheel = terminal.wheel[:1]

	// Alt is the escape hatch for reading the capture behind an alt-screen app,
	// and it means the same thing watched as it does live.
	notch(tea.ModAlt)
	if len(terminal.wheel) != 1 {
		t.Fatalf("alt+wheel was forwarded to the app: %+v", terminal.wheel)
	}
	if m.preview.offset == 0 {
		t.Fatal("alt+wheel did not scroll the window")
	}
}

// A forwarded notch is input, and input to a pane is gated exactly as typing is.
// This surface never consulted the write flag at all before; with it off, every
// notch stays with the window in every state.
func TestTheWheelIsNotForwardedWithWritesDisabled(t *testing.T) {
	cfg := config.Default()
	cfg.Features.Flags[features.TmuxInteractiveInput.Name] = false
	features.Init(cfg)
	t.Cleanup(func() { features.Init(config.Default()) })

	m, _, terminal := interactiveModel(t)
	terminal.buffer.ApplySnapshot(tty.PaneSnapshot{Output: paneBody(60)})
	terminal.reporting = true
	enterInteractive(t, m)
	m.WorkspacesView(previewWide, previewTall)
	surface, ok := m.previewSurface()
	if !ok {
		t.Fatal("the rendered preview has no terminal surface")
	}

	settleWheel()
	run(t, m, m.WorkspacesMouse(tea.MouseWheelMsg{
		X: surface.X + 2, Y: surface.Y + 3, Button: tea.MouseWheelUp}))

	if len(terminal.wheel) != 0 {
		t.Fatalf("a notch was forwarded with write support disabled: %+v", terminal.wheel)
	}
	if m.preview.offset == 0 {
		t.Fatal("the notch that stayed here moved nothing")
	}
}
