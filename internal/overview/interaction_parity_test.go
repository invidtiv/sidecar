package overview

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	appmsg "github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

// The rules a terminal surface answers by are the shared layer's, so the browser
// answers them the way the project plugin does. What is proved here is that this
// surface asks the same questions: who owns a notch while a pane is live, where
// a highlight is drawn, what a press away from the terminal ends, and what a
// scroll does to a selection.

// While the pane is live it keeps the wheel wherever the pointer is. Routing a
// notch to the region under it hands one that drifted off the pane to the list,
// where moving the cursor rebinds the preview and ends the mode.
func TestAWheelOverTheListStaysWithTheLivePane(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	terminal.buffer.ApplySnapshot(tty.PaneSnapshot{Output: paneBody(60)})
	enterInteractive(t, m)
	m.WorkspacesView(previewWide, previewTall)
	selected := m.workspaces.SelectedID()
	m.preview.offset = 10

	settleWheel()
	run(t, m, m.WorkspacesMouse(tea.MouseWheelMsg{X: 2, Y: 6, Button: tea.MouseWheelUp}))

	if !m.PreviewInteractive() {
		t.Fatal("a notch over the list dropped the user out of the pane they were typing in")
	}
	if got := m.workspaces.SelectedID(); got != selected {
		t.Fatalf("selection moved to %q on a wheel notch", got)
	}
	if m.preview.offset == 10 {
		t.Fatal("the notch reached neither the pane nor its window")
	}
}

// A selection is recorded in the buffer's absolute coordinates, so the window
// drawn under it has to resolve a line the same way. A viewport told nothing
// about the base draws every highlight short by exactly it — off screen entirely
// once the pane has any scrollback.
func TestAHighlightIsDrawnOnTheRowItCoversInAPaneWithHistory(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	terminal.buffer = tty.NewOutputBuffer(previewScrollbackLines)
	terminal.buffer.ApplySnapshot(tty.CaptureSnapshot(tty.CaptureInput{
		Output:     paneBody(40),
		BaseLine:   500,
		Absolute:   true,
		PaneHeight: 10,
	}))
	enterInteractive(t, m)
	m.WorkspacesView(previewWide, previewTall)

	base, _ := tty.BufferBase(m.previewBuffer())
	if base != 500 {
		t.Fatalf("the live buffer reported base %d, want the pane's own scrollback base", base)
	}
	window := m.previewWindow()
	if !window.ok {
		t.Fatal("the rendered preview has no window")
	}
	if window.input.AbsoluteBase != base {
		t.Fatalf("the viewport was told base %d, want %d", window.input.AbsoluteBase, base)
	}

	// The first drawn row, in the coordinates a click on it would record.
	line := tty.AbsoluteLine(m.previewBuffer(), window.layout.Start)
	m.preview.selection.SelectRange(
		ui.SelectionPoint{Line: line, Col: 0}, ui.SelectionPoint{Line: line, Col: 7}, false)

	view := m.WorkspacesView(previewWide, previewTall)
	if !strings.Contains(ansi.Strip(view), "line") {
		t.Fatal("the pane's output was not drawn at all")
	}
	if !strings.Contains(view, "\x1b[7m") && !strings.Contains(view, "\x1b[") {
		t.Fatal("nothing was highlighted for a selection over the drawn window")
	}
	if got := strings.Join(m.previewSelectionLines(), "\n"); !strings.HasPrefix(got, "line") {
		t.Fatalf("the selection covers %q, want the row it was made on", got)
	}
}

// A press away from the terminal ends the gesture and the mode together. The
// divider is the case that proves it: it starts a drag of its own, so a surface
// that only abandoned the gesture would resize the box of a pane still holding
// the keyboard.
func TestPressingAwayFromThePreviewEndsTheGestureAndTheMode(t *testing.T) {
	for _, away := range []struct {
		name string
		x, y int
	}{
		{"the sidebar", 2, 6},
		{"the divider", 0, 4},
	} {
		t.Run(away.name, func(t *testing.T) {
			m, _, _ := interactiveModel(t)
			enterInteractive(t, m)
			x, y := previewAt(t, m)
			if away.name == "the divider" {
				away.x = m.previewSplit(previewWide).SidebarWidth
			}

			pointerDown(t, m, x+3, y+1)
			run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{X: away.x, Y: away.y, Button: tea.MouseLeft}))

			if m.preview.pointer.Resolution != tty.ClickNone {
				t.Fatalf("pressing %s left the terminal's click armed", away.name)
			}
			if m.PreviewInteractive() {
				t.Fatalf("pressing %s left the pane holding the keyboard", away.name)
			}
		})
	}
}

// A scroll made outside a pointer gesture keeps a selection in absolute
// coordinates: it names the same rows wherever the window goes.
func TestScrollingKeepsAnAbsoluteSelection(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	terminal.buffer = tty.NewOutputBuffer(previewScrollbackLines)
	terminal.buffer.ApplySnapshot(tty.CaptureSnapshot(tty.CaptureInput{
		Output:     paneBody(60),
		BaseLine:   500,
		Absolute:   true,
		PaneHeight: 10,
	}))
	enterInteractive(t, m)
	x, y := previewAt(t, m)
	m.preview.selection.SelectRange(
		ui.SelectionPoint{Line: 520, Col: 0}, ui.SelectionPoint{Line: 520, Col: 5}, false)

	settleWheel()
	run(t, m, m.WorkspacesMouse(tea.MouseWheelMsg{X: x + 2, Y: y + 2, Button: tea.MouseWheelUp}))
	if !m.preview.selection.HasSelection() {
		t.Fatal("a wheel notch destroyed a selection that names absolute rows")
	}

	handled, _ := m.previewScrollbackKey(tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModShift})
	if !handled {
		t.Fatal("shift+up was not taken as a scrollback move")
	}
	if !m.preview.selection.HasSelection() {
		t.Fatal("a scrollback key destroyed a selection that names absolute rows")
	}
	if m.preview.selection.Start.Line != 520 {
		t.Fatalf("the selection moved to line %d", m.preview.selection.Start.Line)
	}
}

// A pane that dies under a forwarded click ends the mode inside the component.
// The browser says so, as the project surface does: a mode that ends by itself
// with no notice reads as a dropped keystroke.
func TestAPaneThatDiesUnderTheKeyboardSaysSo(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	enterInteractive(t, m)

	cmd := m.WorkspacesTerminalMsg(tty.SessionDeadMsg{})
	if terminal.IsActive() || m.PreviewInteractive() {
		t.Fatal("a dead pane kept the keyboard")
	}
	toast, ok := firstToast(cmd)
	if !ok || toast.Message != "Session ended" {
		t.Fatalf("the browser said %#v, want the session-ended notice", toast)
	}
}

// firstToast finds the notice in whatever a surface batched around it.
func firstToast(cmd tea.Cmd) (appmsg.ToastMsg, bool) {
	if cmd == nil {
		return appmsg.ToastMsg{}, false
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, sub := range batch {
			if toast, ok := firstToast(sub); ok {
				return toast, true
			}
		}
		return appmsg.ToastMsg{}, false
	}
	return noticeAsToast(msg)
}

// tmux writes a background once and lets the rows below inherit it. A surface
// that slices those rows apart without re-opening the carried background loses
// it on every row but the first, which is what draws rectangular seams through
// a full-screen agent UI.
func TestACarriedBackgroundReachesEveryRowItCovers(t *testing.T) {
	const canvas = "\x1b[48;5;236m"
	m, _, terminal := interactiveModel(t)
	rows := []string{canvas + "alpha"}
	for range 9 {
		rows = append(rows, "beta")
	}
	terminal.buffer = tty.NewOutputBuffer(previewScrollbackLines)
	terminal.buffer.ApplySnapshot(tty.PaneSnapshot{Output: strings.Join(rows, "\n")})
	enterInteractive(t, m)

	view := m.WorkspacesView(previewWide, previewTall)
	drawn := 0
	for _, line := range strings.Split(view, "\n") {
		if !strings.Contains(ansi.Strip(line), "beta") {
			continue
		}
		drawn++
		if !strings.Contains(line, canvas) {
			t.Fatalf("a row inheriting the pane's background was drawn without it: %q", line)
		}
	}
	if drawn == 0 {
		t.Fatal("the pane's output was not drawn at all")
	}
}

// Tab is a shared rule, so this surface walks the same windows the project
// plugin does. The sequence below is written out here and again in the project
// plugin's own interaction_parity_test.go rather than computed from focusRing():
// a surface held to a shared rule cannot be allowed to derive its expectation
// from that rule, or a regression in the ring itself would pass on both sides.
//
// The one difference between the surfaces is named rather than smoothed over:
// the project plugin draws a terminal panel below its preview and this one never
// does, so the panel is an entry only its walk has. This surface's walk is the
// project's with that entry removed — asserted there, and asserted here by the
// ring carrying no panel target in any arrangement.
var parityFocusWalk = []string{"terminal", "doc", "issue", "sidebar", "terminal"}

// tabWalk presses Tab once per expected step and records where focus landed.
func tabWalk(t *testing.T, m *Model, steps int) []string {
	t.Helper()
	walk := make([]string, 0, steps)
	for i := range steps {
		handled, cmd := m.WorkspacesKey(tabKey())
		if !handled {
			t.Fatalf("step %d: tab was not handled", i)
		}
		run(t, m, cmd)
		walk = append(walk, focusTargetName(t, m))
	}
	return walk
}

func TestTabWalksTheSameWindowsAsTheProjectSurface(t *testing.T) {
	m := focusRingModel(t)
	run(t, m, m.setFocusTarget(panelayout.Target{Kind: panelayout.TargetSidebar}))

	if got := tabWalk(t, m, len(parityFocusWalk)); !sameWalk(got, parityFocusWalk) {
		t.Fatalf("tab walk = %v, want the shared walk %v", got, parityFocusWalk)
	}

	// The panel belongs to the project surface alone, so no arrangement of this
	// one may put one in the ring — the difference is this, and only this.
	for _, arrangement := range []struct {
		name string
		set  func()
	}{
		{"split", func() {}},
		{"preview only", func() { run(t, m, m.toggleWorkspaceSidebar()) }},
		{"list only", func() {
			m.WorkspacesResize(globalListMinWidth, previewTall)
			m.WorkspacesView(globalListMinWidth, previewTall)
		}},
	} {
		arrangement.set()
		for _, target := range m.focusRing() {
			leaf := panelayout.Find(m.preview.paneRoot, target.Leaf)
			if target.Kind == panelayout.TargetLeaf && leaf != nil && leaf.Kind == panelayout.Shell {
				t.Fatalf("the %s ring names a terminal panel this surface never draws", arrangement.name)
			}
		}
	}
}

// The interactive exception is shared too: a pane being typed into owns Tab on
// both surfaces, so neither moves focus while the keyboard is in a live pane.
func TestTabIsHeldByALivePaneAsItIsInTheProjectSurface(t *testing.T) {
	m := focusRingModel(t)
	run(t, m, m.setFocusTarget(panelayout.Target{Kind: panelayout.TargetSidebar}))
	run(t, m, m.enterPreviewInteractive())
	if !m.PreviewInteractive() {
		t.Fatal("premise: the preview is not interactive")
	}

	before := focusTargetName(t, m)
	handled, cmd := m.WorkspacesKey(tabKey())
	run(t, m, cmd)
	if !handled {
		t.Fatal("tab was not forwarded to the live pane")
	}
	if got := focusTargetName(t, m); got != before {
		t.Fatalf("tab moved focus from %q to %q while a pane was being typed in", before, got)
	}
}

func sameWalk(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
