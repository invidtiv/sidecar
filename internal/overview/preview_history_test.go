package overview

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/tty"
)

// numberedPaneLines is a pane whose every row names its own absolute line, so a
// window can be checked against the row it actually landed on.
func numberedPaneLines(start, count int) string {
	lines := make([]string, count)
	for i := range lines {
		lines[i] = fmt.Sprintf("line-%04d", start+i)
	}
	return strings.Join(lines, "\n")
}

// watchedHistoryModel is a watched preview over a pane holding more history than
// the capture that seeded it: 620 lines loaded from absolute 600, with 1200
// above the grid in tmux.
func watchedHistoryModel(t *testing.T) (*Model, *fakeTerminal, *[]tty.HistoryRange) {
	t.Helper()
	m, _, terminal := interactiveModel(t)
	terminal.buffer = tty.NewOutputBuffer(tty.HistoryBufferLines)
	terminal.buffer.UpdateSnapshot(numberedPaneLines(600, 620), 600)
	terminal.history = tty.HistoryInfo{HistorySize: 1200, HasHistory: true}

	reads := &[]tty.HistoryRange{}
	original := capturePreviewHistory
	capturePreviewHistory = func(target string, start, end int) (tty.CaptureRange, error) {
		if target != "%1" {
			t.Errorf("history was read from %q, want the pane being previewed", target)
		}
		*reads = append(*reads, tty.HistoryRange{Start: start, End: end})
		return tty.CaptureRange{
			Output:      numberedPaneLines(0, 600),
			HistorySize: 1200,
			StartLine:   0,
			EndLine:     600,
		}, nil
	}
	t.Cleanup(func() { capturePreviewHistory = original })

	m.WorkspacesView(previewWide, previewTall)
	if m.PreviewInteractive() {
		t.Fatal("test premise: nobody is typing into this pane")
	}
	return m, terminal, reads
}

// How far back a surface reads is a property of the layer, not of the surface.
// The browser used to dead-end at its own capture and say so; it now walks back
// a chunk at a time until tmux's history is exhausted, and lands on the row the
// project surface lands on — absolute line 0, the twin assertion in
// internal/plugins/workspace's TestAWatchedPreviewReachesTmuxsOldestLine.
func TestAWatchedGlobalPreviewReachesTmuxsOldestLine(t *testing.T) {
	m, terminal, reads := watchedHistoryModel(t)
	x, y := previewAt(t, m)

	m.jumpPreviewWindow(m.previewMaxOffset())
	settleWheel()
	cmd := m.WorkspacesMouse(tea.MouseWheelMsg{X: x + 2, Y: y + 2, Button: tea.MouseWheelUp})
	if cmd == nil {
		t.Fatal("a watched window at the bound never reached for the history behind it")
	}
	run(t, m, cmd)

	if len(*reads) != 1 {
		t.Fatalf("reads = %+v, want one range per bound-hit", *reads)
	}
	// The 600 lines below absolute 600, addressed as capture-pane counts them:
	// back from the pane's own history size.
	if got := (*reads)[0]; got.Start != -1200 || got.End != -601 {
		t.Fatalf("read range = [%d,%d], want the chunk immediately older than the buffer", got.Start, got.End)
	}
	base, _, absolute := terminal.buffer.AbsoluteRange()
	if !absolute || base != 0 {
		t.Fatalf("buffer base = %d absolute=%v, want the pane's oldest line", base, absolute)
	}

	// The window can now be placed on that oldest row, which is what "reads as
	// far back as the project surface" means where the reader can see it.
	m.jumpPreviewWindow(m.previewMaxOffset())
	window := m.previewWindow()
	if !window.ok {
		t.Fatal("the rendered preview has no window")
	}
	if window.layout.AbsoluteStart != 0 {
		t.Fatalf("the oldest window starts at absolute %d, want line 0", window.layout.AbsoluteStart)
	}
	if first := m.previewBuffer().Lines()[window.layout.Start]; first != "line-0000" {
		t.Fatalf("the oldest window's first row is %q, want line-0000", first)
	}
}

// At tmux's oldest line there is nothing left to read, and the reader is told
// so rather than left pushing against a window that silently stops moving. It is
// said once, and it is a fact about the pane: the project surface says it in the
// same words.
func TestTheEndOfTmuxsHistoryIsSaidOnce(t *testing.T) {
	m, _, _ := watchedHistoryModel(t)
	x, y := previewAt(t, m)
	// The notch itself, unrun: a toast is the answer this surface hands back, and
	// delivering it here would consume it before it could be read.
	notch := func() tea.Cmd {
		t.Helper()
		m.jumpPreviewWindow(m.previewMaxOffset())
		settleWheel()
		return m.WorkspacesMouse(tea.MouseWheelMsg{X: x + 2, Y: y + 2, Button: tea.MouseWheelUp})
	}

	run(t, m, notch())

	toast, ok := firstToast(notch())
	if !ok || toast.Message != tty.HistoryExhaustedNotice {
		t.Fatalf("message at the end of history = %#v, want %q", toast, tty.HistoryExhaustedNotice)
	}

	if _, ok := firstToast(notch()); ok {
		t.Fatal("the end of history was announced twice")
	}
}

// A read that lands after the reader has moved on must not move the window
// under them. The generation says which request is still theirs.
func TestASupersededPreviewReadIsRefused(t *testing.T) {
	m, terminal, _ := watchedHistoryModel(t)
	m.jumpPreviewWindow(m.previewMaxOffset())
	if cmd := m.reachOlderPreviewHistory(20); cmd == nil {
		t.Fatal("the reach never opened a read")
	}
	stale := m.preview.history.RequestGen
	m.pinPreviewToLive()

	m.Update(previewHistoryLoadedMsg{
		Target:     m.preview.terminalTarget,
		Capture:    tty.CaptureRange{Output: numberedPaneLines(0, 600), HistorySize: 1200, StartLine: 0, EndLine: 600},
		Generation: stale,
	})

	if base, _, _ := terminal.buffer.AbsoluteRange(); base != 600 {
		t.Fatalf("a superseded read prepended anyway: base = %d", base)
	}
	if m.preview.offset != 0 {
		t.Fatalf("a superseded read moved the window to %d", m.preview.offset)
	}
}

// A reach in flight is said the same way on both surfaces. The header used to
// offer "▲ N older lines available" for as long as the read was running, because
// this surface never told the shared derivation a read was open — the same pane,
// at the same bound, with a different header in each place it is drawn.
func TestTheGlobalHeaderSaysAReadIsInFlight(t *testing.T) {
	m, _, _ := watchedHistoryModel(t)
	m.jumpPreviewWindow(m.previewMaxOffset())
	// The reach, unrun: the read is in flight exactly while its command has not
	// been delivered, which is the state the header has to describe.
	if cmd := m.reachOlderPreviewHistory(20); cmd == nil {
		t.Fatal("the reach never opened a read")
	}

	view := ansi.Strip(m.WorkspacesView(previewWide, previewTall))
	if !strings.Contains(view, "loading older history") && !strings.Contains(view, "loading…") {
		t.Fatalf("the header never says a read of older history is running:\n%s", view)
	}
	if strings.Contains(view, "older lines available") {
		t.Fatalf("the header still offers history it is already reading:\n%s", view)
	}
}
