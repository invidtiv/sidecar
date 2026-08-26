package overview

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/tty"
)

// Leaving the pane must not blank it: keyboard ownership changes while the
// same producer and loaded scrollback remain in place.
func TestLeavingInteractiveKeepsTheOutputProducer(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	run(t, m, m.previewSelect())
	if m.previewBuffer() == nil {
		t.Fatal("the watched preview captured nothing to begin with")
	}
	press(t, m, "enter")
	if !m.PreviewInteractive() {
		t.Fatal("the pane never became live")
	}
	run(t, m, terminal.Update(tty.CaptureResultMsg{Output: longPaneOutput(40)}))
	// Measured on the live rows, not the capture taken before entry: those are
	// the ones the reader is looking at when they leave.
	live := m.previewBuffer().Lines()
	m.jumpPreviewWindow(2)

	terminal.hooks.OnExit()

	if m.PreviewInteractive() {
		t.Fatal("the mode did not end")
	}
	if m.previewBuffer() == nil {
		t.Fatal("the preview dropped its buffer before the replacement capture arrived")
	}
	if got := m.previewBuffer().Lines(); len(got) != len(live) {
		t.Fatalf("buffer = %d lines, want the %d it was still drawing", len(got), len(live))
	}
	// Where the window lands is tty.LeaveLiveWindow's answer, and the number is
	// written here rather than read back from that function: a host asked to
	// prove it kept the reader's window must not compute its expectation from
	// the rule it is being held to, or a rule that snapped to the live edge
	// would produce its own passing answer. The rule's own value is pinned in
	// TestLeaveLiveWindowKeepsTheReadersWindow; this is the host obeying it.
	if m.previewTerminalLeaf().Scroll != 2 {
		t.Fatalf("scroll position = %d, want the 2 rows back the reader was at", m.previewTerminalLeaf().Scroll)
	}
	view := m.WorkspacesView(previewWide, previewTall)
	if strings.Contains(ansi.Strip(view), "No output captured") {
		t.Fatalf("the preview reads as empty over a pane with output:\n%s", view)
	}
}

// A window a gesture pinned is handed back to the bottom-relative model when the
// mode ends, on this surface as on the project workspace's: the same shared rule
// answers both, so neither keeps a pin whose gesture is over (td-651ca2).
func TestLeavingALivePaneThawsAPinnedWindow(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	run(t, m, m.previewSelect())
	enterInteractive(t, m)
	run(t, m, terminal.Update(tty.CaptureResultMsg{Output: longPaneOutput(40)}))
	m.WorkspacesView(previewWide, previewTall)

	m.freezePreviewWindow()
	if !m.previewTerminalLeaf().Freeze.Active() {
		t.Fatal("the window was not pinned to begin with")
	}
	m.scrollPreview(20)
	pinned := m.previewTerminalLeaf().Freeze.Start()

	terminal.hooks.OnExit()

	// The bound is measured after the exit, as production does: leaving the
	// mode changes whether trailing rows are trimmed, and a bound taken in the
	// interactive state could disagree with the one the leave rule actually saw.
	bound := m.previewMaxOffset()

	if m.previewTerminalLeaf().Freeze.Active() {
		t.Fatal("the window stayed pinned after the mode that pinned it ended")
	}
	want := tty.ThawOffsetFrom(pinned, bound)
	if want == 0 {
		t.Fatalf("the fixture never left the live edge (pin %d, bound %d)", pinned, bound)
	}
	if m.previewTerminalLeaf().Scroll != want {
		t.Fatalf("window = %d rows back, want the %d the thawed pin sits at", m.previewTerminalLeaf().Scroll, want)
	}
}

// A selection is bound to the pane it was made over, so binding the preview to a
// different item still replaces everything.
func TestBindingADifferentItemStillReplacesTheContent(t *testing.T) {
	m, _, _ := interactiveModel(t)
	run(t, m, m.previewSelect())
	press(t, m, "enter")

	m.workspaces.SelectID("b")
	m.WorkspacesView(previewWide, previewTall)
	if got, _ := m.SelectedWorkspace(); got.ID != "b" {
		t.Fatalf("the selection is %q, want the other item", got.ID)
	}
	run(t, m, m.bindPreview(true))

	if m.previewBuffer() == nil || strings.Contains(m.previewBuffer().String(), "live pane body") {
		t.Fatal("the preview did not replace another pane's producer when the selection moved")
	}
}

// Every way out of the mode releases input rather than only closing the
// terminal: Exit leaves a half-read SGR mouse report held, and the next session
// receives its tail as keystrokes.
func TestEveryWayOutOfTheLivePaneReleasesInput(t *testing.T) {
	for name, leave := range map[string]func(m *Model, terminal *fakeTerminal){
		"the surface takes the keyboard back": func(m *Model, _ *fakeTerminal) { run(t, m, m.exitPreviewInteractive()) },
		"the selection is rebound":            func(m *Model, _ *fakeTerminal) { run(t, m, m.previewSelect()) },
		"the tab is hidden":                   func(m *Model, _ *fakeTerminal) { m.releasePreview() },
	} {
		t.Run(name, func(t *testing.T) {
			m, _, terminal := interactiveModel(t)
			enterInteractive(t, m)
			released, exits := terminal.released, terminal.exits
			leave(m, terminal)
			if terminal.released == released {
				t.Fatal("the mode ended without releasing the held mouse fragment")
			}
			if terminal.exits != exits {
				t.Fatal("the terminal was closed behind the component's own exit action")
			}
		})
	}
}

// A scrolled-back window says so on both surfaces, and names the key that gets
// back to the live edge. Without it the global tab shows a scrollbar and nothing
// else: no statement that output is arriving below the window.
func TestLivePaneHeaderStatesTheWindowIsOffTheLiveEdge(t *testing.T) {
	m, _, _ := interactiveModel(t)
	enterInteractive(t, m)
	m.previewTerminalState().terminal.Buffer().ApplySnapshot(tty.PaneSnapshot{Output: longPaneOutput(200)})
	m.jumpPreviewWindow(30)

	view := ansi.Strip(m.WorkspacesView(previewWide, previewTall))
	// Stated in full or compactly, by the shared width rule; the fact is what the
	// reader is owed.
	if !strings.Contains(view, "lines back") && !strings.Contains(view, "▲30") {
		t.Fatalf("the header never says the window is off the live edge:\n%s", view)
	}
	if !strings.Contains(view, tty.LiveEdgeKey) {
		t.Fatalf("the header never names the key that returns to live:\n%s", view)
	}
}

func longPaneOutput(lines int) string {
	rows := make([]string, lines)
	for i := range rows {
		rows[i] = "line of pane output"
	}
	return strings.Join(rows, "\n")
}

// Leaving the pane must not move it backwards in time. The capture the preview
// held before the user started typing is minutes old by the time they stop, and
// the poll that would refresh it is suspended for as long as the pane is live —
// so a browser that falls back to it redraws the pane as it was before the
// session, under the scroll offset the reader left on the live rows. What the
// user was just looking at is what must still be on screen.
func TestLeavingInteractiveKeepsShowingWhatWasOnScreen(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	run(t, m, m.previewSelect())
	before := ansi.Strip(m.WorkspacesView(previewWide, previewTall))
	if !strings.Contains(before, "live pane body") {
		t.Fatalf("the watched preview never drew its producer:\n%s", before)
	}

	press(t, m, "enter")
	run(t, m, terminal.Update(tty.CaptureResultMsg{Output: "TYPED IN LIVE PANE\nsecond live line"}))
	live := ansi.Strip(m.WorkspacesView(previewWide, previewTall))
	if !strings.Contains(live, "TYPED IN LIVE PANE") || !strings.Contains(live, "second live line") {
		t.Fatalf("the live pane never drew what was typed into it:\n%s", live)
	}

	terminal.hooks.OnExit()
	if m.PreviewInteractive() {
		t.Fatal("the mode did not end")
	}

	after := ansi.Strip(m.WorkspacesView(previewWide, previewTall))
	if !strings.Contains(after, "TYPED IN LIVE PANE") || !strings.Contains(after, "second live line") {
		t.Fatalf("leaving the pane dropped what the user was looking at:\n%s", after)
	}
	if strings.Contains(after, "live pane body") {
		t.Fatalf("leaving the pane redrew an older frame:\n%s", after)
	}
}
