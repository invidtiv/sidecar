package overview

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/tty"
)

// The same keys move the window in both of the preview's states, and the shared
// rule owns the difference between them: a live pane requires shift because
// every unshifted key is its own. This surface supplies only its drawn rows, so
// a page here is the page the project surface moves over the same pane.
func TestWatchedGlobalPreviewAnswersTheScrollbackKeys(t *testing.T) {
	m, _, _ := watchedHistoryModel(t)
	// A click on the preview leaves the keyboard here without handing it to the
	// pane; that is the state these keys are read in.
	m.preview.focus = focusPreview
	rows := m.previewRows()
	bound := m.previewMaxOffset()
	if bound <= rows {
		t.Fatalf("test premise: %d rows of buffer is not deeper than one page", bound)
	}

	press := func(msg tea.KeyPressMsg) {
		t.Helper()
		handled, cmd := m.WorkspacesKey(msg)
		if !handled {
			t.Fatalf("the watched preview refused %v", msg)
		}
		run(t, m, cmd)
	}

	press(tea.KeyPressMsg{Code: tea.KeyPgUp})
	if m.previewTerminalLeaf().Scroll != rows-1 {
		t.Fatalf("pgup left the window at %d, want a page of %d rows back", m.previewTerminalLeaf().Scroll, rows-1)
	}
	press(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if m.previewTerminalLeaf().Scroll != 0 {
		t.Fatalf("pgdown left the window %d rows back, want the live edge", m.previewTerminalLeaf().Scroll)
	}

	// Half a page is half the rows this surface draws — the same number the
	// project surface moves for a surface of the same height.
	want, ok := tty.MapScrollbackKey(tty.ScrollbackWatched, ctrlKey('u'), rows)
	if !ok {
		t.Fatal("the shared rule did not claim ctrl+u for a watched pane")
	}
	press(ctrlKey('u'))
	if m.previewTerminalLeaf().Scroll != want.Rows {
		t.Fatalf("ctrl+u moved %d rows, want %d", m.previewTerminalLeaf().Scroll, want.Rows)
	}
	press(ctrlKey('d'))
	if m.previewTerminalLeaf().Scroll != 0 {
		t.Fatalf("ctrl+d left the window %d rows back, want the live edge", m.previewTerminalLeaf().Scroll)
	}

	// The jumps land where g and G land. The reach the jump opens is the wheel's
	// own, so the command it returns is left unrun: what is asserted here is
	// where the window went.
	if handled, _ := m.WorkspacesKey(tea.KeyPressMsg{Code: tea.KeyHome}); !handled {
		t.Fatal("the watched preview refused home")
	}
	if m.previewTerminalLeaf().Scroll != bound {
		t.Fatalf("home left the window at %d, want the oldest row held, %d", m.previewTerminalLeaf().Scroll, bound)
	}
	press(tea.KeyPressMsg{Code: tea.KeyEnd})
	if m.previewTerminalLeaf().Scroll != 0 {
		t.Fatalf("end left the window %d rows back, want the live edge", m.previewTerminalLeaf().Scroll)
	}
}

// A watched key held at the bound reaches for the history behind it, exactly as
// a wheel notch there does: how far back a pane can be read is a property of
// the pane, not of the device the reader happens to be using.
func TestAWatchedGlobalScrollbackKeyReachesAtTheBound(t *testing.T) {
	m, terminal, reads := watchedHistoryModel(t)
	m.preview.focus = focusPreview
	m.jumpPreviewWindow(m.previewMaxOffset())

	handled, cmd := m.WorkspacesKey(tea.KeyPressMsg{Code: 'k', Text: "k"})
	if !handled || cmd == nil {
		t.Fatalf("a watched key at the bound reached for nothing: handled %v cmd %v", handled, cmd != nil)
	}
	run(t, m, cmd)

	if len(*reads) != 1 {
		t.Fatalf("reads = %+v, want one range per bound-hit", *reads)
	}
	if base, _, absolute := terminal.buffer.AbsoluteRange(); !absolute || base != 0 {
		t.Fatalf("buffer base = %d absolute=%v, want the pane's oldest line", base, absolute)
	}
}

// The two sets differ by the shift a live pane requires and by nothing else:
// the key that moves the window bare while the pane is watched belongs to the
// pane once someone is typing into it.
func TestALivePreviewKeepsTheShiftRequirement(t *testing.T) {
	m, _, _ := watchedHistoryModel(t)
	if handled, _ := m.previewScrollbackKey(tea.KeyPressMsg{Code: tea.KeyPgUp}); !handled {
		t.Fatal("a watched preview refused a bare pgup")
	}

	enterInteractive(t, m)
	if handled, _ := m.previewScrollbackKey(tea.KeyPressMsg{Code: tea.KeyPgUp}); handled {
		t.Fatal("a live pane's bare pgup was taken by the surface")
	}
	if handled, _ := m.previewScrollbackKey(tea.KeyPressMsg{Code: tea.KeyPgUp, Mod: tea.ModShift}); !handled {
		t.Fatal("a live pane's shifted pgup was not taken as a scrollback move")
	}
}
