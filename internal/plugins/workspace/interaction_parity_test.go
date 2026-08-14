package workspace

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

// A pointer report is delivered by the terminal component, so a pane that died
// under it fails as the component's own message. The mode has to end with it, or
// reconciliation reopens the dead pane and the user is told nothing.
func TestAFailedForwardedClickLeavesInteractiveModeExactlyOnce(t *testing.T) {
	p := newInteractiveInputTestPlugin()
	p.width, p.height = 100, 30
	p.shellSelected = true
	p.interactiveState.TargetPane = "%7"
	terminal := attachLiveTerminal(p, true)

	dead := tty.SessionDeadMsg{Scope: terminal.Scope()}
	// The component ends itself on a failed send and tells the host why, which is
	// what ends the mode around it.
	terminal.Update(dead)
	if terminal.IsActive() {
		t.Fatal("the component kept a dead pane open")
	}
	if p.viewMode == ViewModeInteractive || p.interactiveState != nil {
		t.Fatal("a dead pane left the plugin in interactive mode")
	}
	if p.toastMessage != "Session ended" {
		t.Fatalf("toast = %q, want the session-ended notice", p.toastMessage)
	}

	p.toastMessage = ""
	terminal.Update(dead)
	p.update(dead)
	if p.toastMessage != "" {
		t.Fatalf("a second delivery of the same death exited again: %q", p.toastMessage)
	}
}

// A press anywhere but a terminal ends both the gesture it armed and the mode it
// was armed in. Ending only the gesture leaves a live pane holding the keyboard
// behind a divider drag.
func TestPressingAwayFromTheTerminalEndsTheGestureAndTheMode(t *testing.T) {
	for _, away := range []string{regionPaneDivider, regionSidebar, regionTermPanelDivider} {
		t.Run(away, func(t *testing.T) {
			p := newInteractiveInputTestPlugin()
			p.width, p.height = 100, 30
			p.shellSelected = true
			p.mouseHandler = mouse.NewHandler()
			p.pointer.Arm(tty.ClickActivate, 50, 5)

			p.handleMouseClick(mouse.MouseAction{
				Type: mouse.ActionClick, X: 2, Y: 4,
				Region: &mouse.Region{ID: away},
			})

			if p.pointer.Resolution != tty.ClickNone {
				t.Fatalf("a press on %s left the terminal's click armed", away)
			}
			if p.viewMode == ViewModeInteractive || p.interactiveState != nil {
				t.Fatalf("a press on %s left the pane holding the keyboard", away)
			}
		})
	}
}

// Selections are recorded in the buffer's absolute coordinates, so they name the
// same rows wherever the window goes: scrolling away from a highlight and back
// leaves it where the user made it.
func TestAWheelScrollKeepsAnAbsoluteSelection(t *testing.T) {
	p := newInteractiveInputTestPlugin()
	p.width, p.height = 100, 30
	p.shellSelected = true
	buffer := tty.NewOutputBuffer(outputBufferCap)
	buffer.ApplySnapshot(tty.CaptureSnapshot(tty.CaptureInput{
		Output:     strings.Repeat("scrollback\n", 100),
		BaseLine:   500,
		Absolute:   true,
		PaneHeight: 10,
	}))
	p.shells = []*ShellSession{{Name: "one", TmuxName: "sc-one", Agent: &Agent{OutputBuf: buffer}}}
	p.previewScroll = 40
	p.selection.SelectRange(
		ui.SelectionPoint{Line: 520, Col: 0}, ui.SelectionPoint{Line: 520, Col: 5}, false)

	p.scrollPreview(-3)

	if !p.selection.HasSelection() {
		t.Fatal("a wheel notch destroyed a selection that names absolute rows")
	}
	if p.selection.Start.Line != 520 {
		t.Fatalf("selection moved to line %d", p.selection.Start.Line)
	}
}

// A shift-scrollback key is the same kind of scroll, and answers the same way.
func TestAScrollbackKeyKeepsAnAbsoluteSelection(t *testing.T) {
	p := newInteractiveInputTestPlugin()
	p.width, p.height = 100, 30
	p.shellSelected = true
	buffer := tty.NewOutputBuffer(outputBufferCap)
	buffer.ApplySnapshot(tty.CaptureSnapshot(tty.CaptureInput{
		Output:     strings.Repeat("scrollback\n", 100),
		BaseLine:   500,
		Absolute:   true,
		PaneHeight: 10,
	}))
	p.shells = []*ShellSession{{Name: "one", TmuxName: "sc-one", Agent: &Agent{OutputBuf: buffer}}}
	p.previewScroll = 40
	p.selection.SelectRange(
		ui.SelectionPoint{Line: 520, Col: 0}, ui.SelectionPoint{Line: 520, Col: 5}, false)

	handled, _ := p.handleInteractiveScrollbackKey(tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModShift})
	if !handled {
		t.Fatal("shift+up was not taken as a scrollback move")
	}
	if !p.selection.HasSelection() {
		t.Fatal("a scrollback key destroyed a selection that names absolute rows")
	}
}

// Leaving the mode on this surface ends input ownership and nothing else. The
// pane stays on screen after the user stops typing into it, so tearing the
// terminal down here would drop the scrollback they just read and hand
// reconciliation an empty buffer to redraw on the same update.
func TestLeavingInteractiveModeKeepsTheLoadedScrollback(t *testing.T) {
	p := newInteractiveInputTestPlugin()
	terminal := attachLiveTerminal(p, false)
	buffer := terminal.State.OutputBuf
	buffer.ApplySnapshot(tty.PaneSnapshot{Output: "older line\nnewer line"})

	p.handleInteractiveKeys(tea.KeyPressMsg{Code: '\\', Mod: tea.ModCtrl})

	if p.viewMode == ViewModeInteractive || p.interactiveState != nil {
		t.Fatal("the exit chord left the plugin in interactive mode")
	}
	if !terminal.IsActive() {
		t.Fatal("leaving the mode closed the terminal the surface still draws")
	}
	if terminal.State.OutputBuf != buffer {
		t.Fatal("leaving the mode replaced the buffer with a fresh one")
	}
	if got := strings.Join(buffer.Lines(), "\n"); !strings.Contains(got, "older line") {
		t.Fatalf("the loaded scrollback did not survive the exit: %q", got)
	}
}

// Leaving a live pane leaves the window where the reader put it, on this surface
// as on the global preview's (td-2e3738). This side used to snap to the live
// edge — drift from the two window models rather than a decision — which threw
// away the scrollback position of a reader who left the mode precisely to read
// what they had scrolled back to.
func TestLeavingALivePaneKeepsTheReadersWindow(t *testing.T) {
	p := newInteractiveInputTestPlugin()
	p.width, p.height = 120, 40
	givePaneScrollableOutput(p, 120)
	if p.previewMaxScroll() < 10 {
		t.Fatalf("the fixture cannot sit off the live edge (bound %d)", p.previewMaxScroll())
	}

	p.jumpPreviewWindow(10)
	p.leaveInteractiveMode()

	if p.viewMode == ViewModeInteractive {
		t.Fatal("the mode did not end")
	}
	// The answer belongs to tty.LeaveLiveWindow, but the number is written here
	// rather than read back from it: a host held to the shared rule cannot be
	// allowed to compute its own expectation from that rule, or a regression
	// that snapped both sides to the live edge would pass on both. The rule's
	// value is pinned once, in tty's TestLeaveLiveWindowKeepsTheReadersWindow.
	if p.previewScroll != 10 {
		t.Fatalf("window = %d rows back, want the 10 the reader left it at", p.previewScroll)
	}
}

// A window a gesture pinned is handed back to the bottom-relative model when the
// mode ends, rather than kept pinned: the gesture is over, and a window left
// pinned has stopped following output for good.
func TestLeavingALivePaneThawsAPinnedWindow(t *testing.T) {
	p := newInteractiveInputTestPlugin()
	p.width, p.height = 120, 40
	givePaneScrollableOutput(p, 120)

	p.freezeTerminalSelectionViewport()
	if !p.previewFreeze.Active() {
		t.Fatal("the window was not pinned to begin with")
	}
	p.scrollTerminalSelectionViewport(-20)
	pinned := p.previewFreeze.Start()

	p.leaveInteractiveMode()

	if p.previewFreeze.Active() {
		t.Fatal("the window stayed pinned after the mode that pinned it ended")
	}
	if want := p.previewMaxScroll() - pinned; p.previewScroll != want {
		t.Fatalf("window = %d rows back, want the %d the pinned rows sit at", p.previewScroll, want)
	}
	if p.previewScroll == 0 {
		t.Fatal("a window left in scrollback was dragged back to the live edge")
	}
}
