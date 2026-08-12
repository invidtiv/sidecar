package overview

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/tty"
)

// Leaving the pane must not blank it. The replacement capture is a round trip
// away, and a browser that dropped what it had shows "no output captured" over a
// pane that plainly has output — and forgets where the reader had scrolled to.
// The project surface keeps its loaded scrollback across the same handover.
func TestLeavingInteractiveKeepsTheOutputUntilTheReplacementArrives(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	press(t, m, "right")
	run(t, m, m.previewSelect())
	if m.previewBuffer() == nil {
		t.Fatal("the watched preview captured nothing to begin with")
	}
	watched := m.previewBuffer().Lines()
	press(t, m, interactiveEnterKey)
	if !m.PreviewInteractive() {
		t.Fatal("the pane never became live")
	}
	m.jumpPreviewWindow(2)

	// The capture the exit starts is deliberately left in flight: what the
	// browser draws in the meantime is the whole question.
	if cmd := terminal.hooks.OnExit(); cmd == nil {
		t.Fatal("leaving the mode started no replacement capture")
	}

	if m.PreviewInteractive() {
		t.Fatal("the mode did not end")
	}
	if m.previewBuffer() == nil {
		t.Fatal("the preview dropped its buffer before the replacement capture arrived")
	}
	if got := m.previewBuffer().Lines(); len(got) != len(watched) {
		t.Fatalf("buffer = %d lines, want the %d it was still drawing", len(got), len(watched))
	}
	if m.preview.offset != 2 {
		t.Fatalf("scroll position = %d, want the 2 rows back the reader was at", m.preview.offset)
	}
	view := m.WorkspacesView(previewWide, previewTall)
	if strings.Contains(ansi.Strip(view), "No output captured") {
		t.Fatalf("the preview reads as empty over a pane with output:\n%s", view)
	}
}

// A selection is bound to the pane it was made over, so binding the preview to a
// different item still replaces everything.
func TestBindingADifferentItemStillReplacesTheContent(t *testing.T) {
	m, _, _ := interactiveModel(t)
	press(t, m, "right")
	run(t, m, m.previewSelect())
	press(t, m, interactiveEnterKey)

	m.workspaces.SelectID("b")
	m.WorkspacesView(previewWide, previewTall)
	if got, _ := m.SelectedWorkspace(); got.ID != "b" {
		t.Fatalf("the selection is %q, want the other item", got.ID)
	}
	m.bindPreview(true)

	if m.previewBuffer() != nil {
		t.Fatal("the preview kept another pane's output when the selection moved")
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
	m.preview.terminal.Buffer().ApplySnapshot(tty.PaneSnapshot{Output: longPaneOutput(200)})
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
