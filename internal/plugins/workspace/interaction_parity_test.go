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
	p.previewOffset = 40
	p.autoScrollOutput = false
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
	p.previewOffset = 40
	p.autoScrollOutput = false
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
