package workspace

import (
	"fmt"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/tty"
)

// watchedWheelPlugin draws a shell's pane in list mode — nobody is typing into
// it — with the component that produces it open, which is what this surface
// holds while merely watching.
func watchedWheelPlugin(t *testing.T, mouseReporting bool) *Plugin {
	t.Helper()
	p := New()
	p.width, p.height = 100, 30
	p.sidebarWidth = 40
	p.viewMode = ViewModeList
	p.shellSelected = true

	buffer := testTerminalBuffer(strings.Repeat("watched row\n", 60))
	p.shells = []*ShellSession{{
		Name: "one", TmuxName: "sidecar-sh-one",
		Agent: &Agent{OutputBuf: buffer, TmuxSession: "sidecar-sh-one", TmuxPane: "%7"},
	}}
	p.selectedShellIdx = 0

	model := p.newWorkspaceTerminal()
	model.State = &tty.State{
		Active:                true,
		TargetSession:         "sidecar-sh-one",
		TargetPane:            "%7",
		MouseReportingEnabled: mouseReporting,
		PaneWidth:             80,
		PaneHeight:            20,
		OutputBuf:             buffer,
	}
	p.primaryTerminal = model
	p.primaryTerminalTarget = workspaceTerminalTarget{
		Session: "sidecar-sh-one", Pane: "%7", Source: "shell", SourceID: "sidecar-sh-one",
	}
	p.SetFocused(true)
	if p.viewMode == ViewModeInteractive || p.interactiveState != nil {
		t.Fatal("test premise: the fixture must be watched, not live")
	}
	return p
}

// sgrWheelHex is the hex-encoded SGR wheel report tmux is sent, as the send-keys
// argv carries it.
func sgrWheelHex(up bool, col, row int) string {
	button := 65
	if up {
		button = 64
	}
	report := fmt.Sprintf("\x1b[<%d;%d;%dM", button, col, row)
	parts := make([]string, 0, len(report))
	for _, b := range []byte(report) {
		parts = append(parts, fmt.Sprintf("%02x", b))
	}
	return strings.Join(parts, " ")
}

func readTmuxLog(t *testing.T, path string) string {
	t.Helper()
	tty.WaitForPendingSends()
	logged, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(logged)
}

// An unfocused pane whose application has asked for mouse reports gets the notch
// at its own 1-indexed cell, and this surface's window stays on the live edge —
// the same answer the focused pane gives, which is the whole point: the send is
// addressed to a pane and needs no focus.
func TestWatchedWheelForwardsToAMouseReportingPane(t *testing.T) {
	logPath := installSuccessfulFakeTmux(t)
	p := watchedWheelPlugin(t, true)
	// A window left scrolled back would sit over stale rows while the app
	// repaints below, so a claimed notch pins it first.
	p.previewScroll = 3

	col, row, ok := p.terminalMouseCoords(false, 60, 8)
	if !ok {
		t.Fatal("test premise: the pointer does not land on a pane cell")
	}
	if col < 1 || row < 1 {
		t.Fatalf("pane coordinates (%d,%d) are not 1-indexed", col, row)
	}

	cmd := p.handleMouseScroll(mouse.MouseAction{
		Type: mouse.ActionScrollUp, Delta: -mouse.WheelScrollLines, X: 60, Y: 8,
		Region: &mouse.Region{ID: regionPreviewPane},
	})
	if cmd == nil {
		t.Fatal("a notch over a watched mouse-reporting pane produced no command")
	}
	runCommandTree(cmd)

	logged := readTmuxLog(t, logPath)
	if want := sgrWheelHex(true, col, row); !strings.Contains(logged, want) {
		t.Fatalf("watched pane never received the wheel report %q: %s", want, logged)
	}
	if !strings.Contains(logged, "-t %7") {
		t.Fatalf("the report was not addressed to the watched pane: %s", logged)
	}
	if p.previewScroll != 0 {
		t.Fatalf("previewScroll = %d, want the window pinned to the live edge", p.previewScroll)
	}
}

// Where no component is producing the pane, the capture is the only observer of
// what the application asked for — and that observation used to be parsed,
// carried on the poll message and then dropped. It has to outlive the poll, or
// the same pane answers the wheel differently depending on who is drawing it.
func TestAnObservedMouseFlagOutlivesThePollThatSawIt(t *testing.T) {
	logPath := installSuccessfulFakeTmux(t)
	p := watchedWheelPlugin(t, false)
	p.primaryTerminal = nil
	p.primaryTerminalTarget = workspaceTerminalTarget{}

	p.update(ShellOutputMsg{
		TmuxName: "sidecar-sh-one", Output: strings.Repeat("watched row\n", 60),
		HasCursor: true, MouseReporting: true, PaneWidth: 80, PaneHeight: 20,
	})
	if !p.paneMouseReporting(false) {
		t.Fatal("the observed mouse flag did not survive the poll that saw it")
	}

	p.previewScroll = 3
	col, row, ok := p.terminalMouseCoords(false, 60, 8)
	if !ok {
		t.Fatal("test premise: the pointer does not land on a pane cell")
	}
	runCommandTree(p.handleMouseScroll(mouse.MouseAction{
		Type: mouse.ActionScrollUp, Delta: -mouse.WheelScrollLines, X: 60, Y: 8,
		Region: &mouse.Region{ID: regionPreviewPane},
	}))

	logged := readTmuxLog(t, logPath)
	if want := sgrWheelHex(true, col, row); !strings.Contains(logged, want) {
		t.Fatalf("the pane never received the wheel report %q: %s", want, logged)
	}
	if p.previewScroll != 0 {
		t.Fatalf("previewScroll = %d, want the window pinned to the live edge", p.previewScroll)
	}
}

// A watched plain shell asks for no mouse reports, so the notch keeps moving
// this surface's own window exactly as it always has.
func TestWatchedWheelWithoutMouseReportingScrollsLocally(t *testing.T) {
	logPath := installSuccessfulFakeTmux(t)
	p := watchedWheelPlugin(t, false)
	p.previewScroll = 5

	p.handleMouseScroll(mouse.MouseAction{
		Type: mouse.ActionScrollUp, Delta: -1, X: 60, Y: 8,
		Region: &mouse.Region{ID: regionPreviewPane},
	})

	if p.previewScroll != 6 {
		t.Fatalf("previewScroll = %d, want 6 after a local notch back through scrollback", p.previewScroll)
	}
	if logged := readTmuxLog(t, logPath); strings.Contains(logged, "send-keys") {
		t.Fatalf("a notch was forwarded to a watched pane that tracks no mouse: %s", logged)
	}
}

func TestWatchedTerminalBoundaryDropsOnlyLocalExhaustedInertia(t *testing.T) {
	p := watchedWheelPlugin(t, false)
	p.mouseHandler.HitMap.AddRect(regionPreviewPane, 40, 0, 60, 30, nil)
	down := tea.MouseWheelMsg{X: 60, Y: 8, Button: tea.MouseWheelDown}
	if !p.WheelAtBoundary(down) {
		t.Fatal("local terminal inertia past the live bottom was not bounded")
	}

	up := tea.MouseWheelMsg{X: 60, Y: 8, Button: tea.MouseWheelUp}
	if p.WheelAtBoundary(up) {
		t.Fatal("local terminal wheel toward available history was dropped")
	}
	p.previewScroll = p.previewMaxScroll()
	source, ok := p.terminalHistoryFor(false)
	if !ok {
		t.Fatal("test premise: terminal history source unavailable")
	}
	state := p.terminalHistory[source.Key]
	state.Exhausted = true
	p.terminalHistory[source.Key] = state
	if !p.WheelAtBoundary(up) {
		t.Fatal("local terminal inertia past exhausted history was not bounded")
	}

	p = watchedWheelPlugin(t, true)
	p.mouseHandler.HitMap.AddRect(regionPreviewPane, 40, 0, 60, 30, nil)
	if p.WheelAtBoundary(down) {
		t.Fatal("mouse-reporting application wheel was mistaken for Sidecar scrollback")
	}
}

// Alt is the escape hatch for reading the capture behind an alt-screen app, and
// it means the same thing watched as it does live.
func TestWatchedAltWheelStaysLocal(t *testing.T) {
	logPath := installSuccessfulFakeTmux(t)
	p := watchedWheelPlugin(t, true)
	p.previewScroll = 5

	p.handleMouseScroll(mouse.MouseAction{
		Type: mouse.ActionScrollUp, Delta: -1, X: 60, Y: 8, Alt: true,
		Region: &mouse.Region{ID: regionPreviewPane},
	})

	if p.previewScroll != 6 {
		t.Fatalf("previewScroll = %d, want 6 — alt+wheel must stay local", p.previewScroll)
	}
	if logged := readTmuxLog(t, logPath); strings.Contains(logged, "send-keys") {
		t.Fatalf("alt+wheel was forwarded to the watched pane: %s", logged)
	}
}

// A forwarded notch is input, so with write support off it never leaves the
// host — in either state, on the surface that used to consult the flag only on
// its way into interactive mode.
func TestWheelIsNotForwardedWithWritesDisabled(t *testing.T) {
	cfg := config.Default()
	cfg.Features.Flags[features.TmuxInteractiveInput.Name] = false
	features.Init(cfg)
	t.Cleanup(func() { features.Init(config.Default()) })

	logPath := installSuccessfulFakeTmux(t)
	p := watchedWheelPlugin(t, true)
	p.previewScroll = 5

	p.handleMouseScroll(mouse.MouseAction{
		Type: mouse.ActionScrollUp, Delta: -1, X: 60, Y: 8,
		Region: &mouse.Region{ID: regionPreviewPane},
	})

	if p.previewScroll != 6 {
		t.Fatalf("previewScroll = %d, want the notch on this surface's own window", p.previewScroll)
	}
	if logged := readTmuxLog(t, logPath); strings.Contains(logged, "send-keys") {
		t.Fatalf("a notch was forwarded with write support disabled: %s", logged)
	}
}
