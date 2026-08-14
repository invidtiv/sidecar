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
	if p.previewMaxScroll() <= p.terminalSurfaceRows(false) {
		t.Fatalf("test premise: %d rows of buffer is not deeper than one page", p.previewMaxScroll())
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
	bound := p.previewMaxScroll()

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
	p.previewScroll = p.previewMaxScroll()

	cmd := p.handleKeyPress(tea.KeyPressMsg{Code: 'k', Text: "k"})
	if cmd == nil {
		t.Fatal("a key held at the bound asked for no older history")
	}
	source, ok := p.terminalHistoryFor(false)
	if !ok || !p.terminalHistory[source.Key].Loading {
		t.Fatalf("no read is in flight for the watched pane: source %v", ok)
	}
}
