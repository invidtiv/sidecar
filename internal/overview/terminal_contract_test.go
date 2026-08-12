package overview

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	appmsg "github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// The browser drives the same embedded terminal the project plugin does, and it
// must drive it through the same contract: everything the component calls back
// for is registered once, rather than re-implemented around it.
func TestTheBrowsersTerminalIsBuiltWithTheHostContract(t *testing.T) {
	m := New(workspaceinventory.Collector{})
	model, ok := newPreviewTerminal(m.TerminalConfig(), m.previewTerminalHooks()).(*tty.Model)
	if !ok {
		t.Fatal("the browser's terminal is not the shared component")
	}
	for name, hook := range map[string]any{
		"OnKey":          model.OnKey,
		"BeforeSend":     model.BeforeSend,
		"OnExit":         model.OnExit,
		"OnSessionEnded": model.OnSessionEnded,
	} {
		if hook == nil {
			t.Errorf("%s is unwired, so this surface answers it outside the component", name)
		}
	}
	if model.ExitAction != tty.ExitClosesTerminal {
		t.Errorf("ExitAction = %v, want the browser's stated choice", model.ExitAction)
	}
}

// A chord that acts on the terminal surface — copy, select-all, shift-scrollback
// — is the host's, and the component asks for it through OnKey before anything
// becomes input. Answering it outside as well would send it to the pane twice.
func TestLivePaneChordsAreAnsweredThroughTheComponentsHook(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	enterInteractive(t, m)
	terminal.keys = nil

	handled, cmd := m.WorkspacesKey(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	if !handled {
		t.Fatal("select-all was not answered while the pane was live")
	}
	run(t, m, cmd)

	if len(terminal.keys) != 0 {
		t.Fatalf("the chord also reached the pane as %v", terminal.keys)
	}
	if !m.preview.selection.HasSelection() {
		t.Fatal("select-all selected nothing, so the hook never ran")
	}
}

// A pane that dies under a keystroke ends the mode inside the component, which
// says so through OnSessionEnded.
func TestASessionEndingUnderTheUserIsAnnouncedThroughTheHook(t *testing.T) {
	m, _, _ := interactiveModel(t)
	enterInteractive(t, m)

	cmd := m.WorkspacesTerminalMsg(tty.SessionDeadMsg{})
	if cmd == nil {
		t.Fatal("a dead session produced no work at all")
	}
	if m.PreviewInteractive() {
		t.Fatal("the mode survived the pane it was typing into")
	}
	if !strings.Contains(toastText(t, m, cmd), "Session ended") {
		t.Fatal("the mode ended with no notice, which reads as a dropped keystroke")
	}
}

// toastText runs a command tree and reports the text of any toast in it.
func toastText(t *testing.T, m *Model, cmd tea.Cmd) string {
	t.Helper()
	var found string
	var walk func(tea.Cmd)
	walk = func(c tea.Cmd) {
		if c == nil {
			return
		}
		msg := c()
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, sub := range batch {
				walk(sub)
			}
			return
		}
		if toast, ok := msg.(appmsg.ToastMsg); ok {
			found = toast.Message
		}
	}
	walk(cmd)
	return found
}

// A watched pane is a scrollback window and a live one is a grid: which of them
// trailing blank rows belong to is the shared rule, so the same pane cannot draw
// one way here and another in the project's tab.
func TestTrailingRowRuleComesFromTheSharedLayer(t *testing.T) {
	m, _, _ := interactiveModel(t)
	m.WorkspacesView(previewWide, previewTall)

	watched := m.previewWindow()
	if !watched.ok {
		t.Fatal("the rendered preview has no window")
	}
	if watched.input.TrimTrailing != tty.TrimsTrailingRows(false) {
		t.Fatal("a watched window trims by a rule of its own")
	}

	enterInteractive(t, m)
	live := m.previewWindow()
	if live.input.TrimTrailing != tty.TrimsTrailingRows(true) {
		t.Fatal("a live window trims by a rule of its own")
	}
}

// A window scrolled off the live edge is showing history, and a cursor painted
// over history sits on a row the pane is not writing to. The project surface
// refuses it there; so must this one.
func TestNoCursorIsDrawnOverScrolledBackHistory(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	terminal.buffer.ApplySnapshot(tty.PaneSnapshot{Output: paneBody(60)})
	enterInteractive(t, m)
	m.WorkspacesView(previewWide, previewTall)

	if m.WorkspacesCursor() == nil {
		t.Fatal("test premise: the live pane draws no cursor at the live edge")
	}

	m.scrollPreview(4)
	m.WorkspacesView(previewWide, previewTall)

	if cursor := m.WorkspacesCursor(); cursor != nil {
		t.Fatalf("a cursor was painted at %+v over rows the pane is not writing to", cursor)
	}
}

// The window a drag reads must not move under it. The watched buffer is
// renumbered by every capture, so a window still placed against the live bottom
// follows the new rows while the anchor keeps naming the old ones.
func TestADragFreezesTheWindowAgainstAMidGestureCapture(t *testing.T) {
	m, _, _ := interactiveModel(t)
	run(t, m, m.focusPreviewPane())
	run(t, m, m.applyPreview(previewMsg{
		Generation:  m.preview.generation,
		WorkspaceID: m.preview.workspaceID,
		PaneID:      "%1",
		Output:      paneBody(60),
	}))
	x, y := previewAt(t, m)

	pointerDown(t, m, x, y+2)
	dragTo(t, m, x+6, y+3)
	frozen := m.previewWindow().layout
	anchor := m.preview.selection.Anchor
	before, ok := tty.LineTextAt(m.previewBuffer(), anchor.Line)
	if !ok {
		t.Fatal("test premise: the drag anchored on no buffer row")
	}

	// A poll lands mid-drag at the focused cadence and renumbers the buffer.
	run(t, m, m.applyPreview(previewMsg{
		Generation:  m.preview.generation,
		WorkspaceID: m.preview.workspaceID,
		PaneID:      "%1",
		Output:      paneBody(60) + "\n" + strings.Join([]string{"fresh 0", "fresh 1", "fresh 2"}, "\n"),
	}))
	m.WorkspacesView(previewWide, previewTall)

	after, ok := tty.LineTextAt(m.previewBuffer(), anchor.Line)
	if !ok {
		t.Fatal("the anchored row left the buffer mid-drag")
	}
	if after != before {
		t.Fatalf("the anchor named %q when the drag began and %q after one capture", before, after)
	}
	if got := m.previewWindow().layout; got.Start != frozen.Start {
		t.Fatalf("the window moved from %d to %d under the pointer", frozen.Start, got.Start)
	}

	release(t, m, x+6, y+3)
	if m.preview.frozen {
		t.Fatal("the window stayed pinned after the gesture that pinned it ended")
	}
}
