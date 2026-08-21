package workspace

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/tty"
)

// watchedTerminalPlugin is a preview drawing a pane nobody is typing into, with
// enough output behind it to scroll through.
func watchedTerminalPlugin(t *testing.T, lines int) *Plugin {
	t.Helper()
	p := newInteractiveInputTestPlugin()
	p.viewMode = ViewModeList
	p.interactiveState = nil
	p.activePane = PanePreview
	p.width, p.height = 120, 40
	// Absolute coordinates, because a reach for older history is addressed in
	// tmux's own line numbers.
	buffer := tty.NewOutputBuffer(outputBufferCap)
	buffer.ApplySnapshot(tty.CaptureSnapshot(tty.CaptureInput{
		Output:     strings.Repeat("scrollback line\n", lines),
		BaseLine:   500,
		Absolute:   true,
		PaneHeight: 10,
	}))
	p.shellSelected = true
	p.shells = []*ShellSession{{Name: "one", TmuxName: "sc-one", Agent: &Agent{OutputBuf: buffer}}}
	if p.terminalMaxScroll(false) <= p.terminalSurfaceRows(false) {
		t.Fatalf("test premise: %d rows of buffer is not deeper than one page", p.terminalMaxScroll(false))
	}
	return p
}

// The pager keys answer on a watched pane, which is the state this surface
// spends most of its time in. They used to do nothing at all here: the surface
// hand-rolled j/k/g/G/ctrl+d/ctrl+u and knew nothing about the keys a reader
// reaches for first.
func TestWatchedPreviewAnswersThePagerKeys(t *testing.T) {
	p := watchedTerminalPlugin(t, 300)
	page := p.terminalSurfaceRows(false) - 1
	bound := p.terminalMaxScroll(false)

	p.handleKeyPress(tea.KeyPressMsg{Code: tea.KeyPgUp})
	if p.previewScroll != page {
		t.Fatalf("pgup left the window at %d, want a page of %d rows back", p.previewScroll, page)
	}
	p.handleKeyPress(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if p.previewScroll != 0 {
		t.Fatalf("pgdown left the window %d rows back, want the live edge", p.previewScroll)
	}
	p.handleKeyPress(tea.KeyPressMsg{Code: tea.KeyHome})
	if p.previewScroll != bound {
		t.Fatalf("home left the window at %d, want the oldest row held, %d", p.previewScroll, bound)
	}
	p.handleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnd})
	if p.previewScroll != 0 {
		t.Fatalf("end left the window %d rows back, want the live edge", p.previewScroll)
	}
}

// A page is the drawn rows of the surface under the keys. It used to be half of
// the plugin's own height, which is a different number on every layout that puts
// anything else on screen — so the same key moved a different distance here than
// it did on the global browser drawing the same pane.
func TestWatchedHalfPageIsTheSurfaceNotThePlugin(t *testing.T) {
	p := watchedTerminalPlugin(t, 300)
	rows := p.terminalSurfaceRows(false)
	want, ok := tty.MapScrollbackKey(tty.ScrollbackWatched, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}, rows)
	if !ok {
		t.Fatal("the shared rule did not claim ctrl+u for a watched pane")
	}
	if want.Rows == p.height/2 {
		t.Fatalf("test premise: half the surface and half the plugin are both %d rows", want.Rows)
	}

	p.handleKeyPress(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	if p.previewScroll != want.Rows {
		t.Fatalf("ctrl+u moved %d rows, want %d — half the rows the surface draws", p.previewScroll, want.Rows)
	}
	p.handleKeyPress(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	if p.previewScroll != 0 {
		t.Fatalf("ctrl+d left the window %d rows back, want the live edge", p.previewScroll)
	}
}

// A watched key at the bound reaches for the history behind it, exactly as a
// wheel notch there does. Depth used to be a property of the input device on
// this surface: the wheel loaded older output and the keys dead-ended.
func TestAWatchedScrollbackKeyReachesForHistoryAtTheBound(t *testing.T) {
	p := watchedTerminalPlugin(t, 300)
	p.recordTerminalHistory("shell", "sc-one", 5000)
	p.previewScroll = p.terminalMaxScroll(false)

	cmd := p.handleKeyPress(tea.KeyPressMsg{Code: 'k', Text: "k"})
	if cmd == nil {
		t.Fatal("a key held at the bound asked for no older history")
	}
	source, ok := p.terminalHistoryFor(false)
	if !ok || !p.terminalHistory[source.Key].Loading {
		t.Fatalf("no read is in flight for the watched pane: source %v", ok)
	}
}

// The shared rule accepts the shifted form of a navigation key in either state —
// shift is what a live pane requires, not what a watched pane refuses — so the
// keys a reader uses on a live pane keep working when they let go of it. This
// surface dispatches on a key's *name*, and "shift+end" is a name no case here
// ever spelled: the whole shifted set was inert while watching, on the surface
// where the global browser answered it.
func TestWatchedPreviewAnswersTheShiftedNavigationKeys(t *testing.T) {
	p := watchedTerminalPlugin(t, 300)
	page := p.terminalSurfaceRows(false) - 1
	bound := p.terminalMaxScroll(false)

	for _, step := range []struct {
		name string
		msg  tea.KeyPressMsg
		want int
	}{
		{"shift+up", tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModShift}, 1},
		{"shift+down", tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift}, 0},
		{"shift+pgup", tea.KeyPressMsg{Code: tea.KeyPgUp, Mod: tea.ModShift}, page},
		{"shift+pgdown", tea.KeyPressMsg{Code: tea.KeyPgDown, Mod: tea.ModShift}, 0},
		{"shift+home", tea.KeyPressMsg{Code: tea.KeyHome, Mod: tea.ModShift}, bound},
		{"shift+end", tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModShift}, 0},
	} {
		p.handleKeyPress(step.msg)
		if p.previewScroll != step.want {
			t.Fatalf("%s left the watched window %d rows back, want %d", step.name, p.previewScroll, step.want)
		}
	}
}

// A key claimed by the watched set releases the terminal's document projection,
// because a projection has no window to move. A shifted key that was claimed
// there and then dispatched nowhere dropped what the reader was looking at and
// moved nothing in its place.
func TestAShiftedWatchedKeyThatDropsTheProjectionMovesTheWindow(t *testing.T) {
	p := watchedTerminalPlugin(t, 300)
	p.terminalDocProjection = terminalDocProjection{buffer: tty.NewOutputBuffer(outputBufferCap)}

	p.handleKeyPress(tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModShift})
	if p.terminalDocProjection.buffer != nil {
		t.Fatal("the projection survived a key that moves the window")
	}
	if p.previewScroll != 1 {
		t.Fatalf("the window moved %d rows after the projection was dropped, want 1", p.previewScroll)
	}
}
