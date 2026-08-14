package workspace

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

// TestGetMaxScrollOffset tests the unified max scroll offset calculation.
func TestGetMaxScrollOffset(t *testing.T) {
	tests := []struct {
		name       string
		height     int // plugin height
		lineCount  int // buffer line count
		previewTab PreviewTab
		want       int
	}{
		{
			name:       "output with content taller than viewport",
			height:     20,
			lineCount:  100,
			previewTab: PreviewTabOutput,
			want:       83, // 100 - (20-3) = 83
		},
		{
			name:       "output with content shorter than viewport",
			height:     20,
			lineCount:  5,
			previewTab: PreviewTabOutput,
			want:       0,
		},
		{
			name:       "output with zero content",
			height:     20,
			lineCount:  0,
			previewTab: PreviewTabOutput,
			want:       0,
		},
		{
			name:       "diff tab returns 0 (uses own scroll)",
			height:     20,
			lineCount:  100,
			previewTab: PreviewTabDiff,
			want:       0,
		},
		{
			name:       "task tab with content",
			height:     20,
			lineCount:  50,
			previewTab: PreviewTabTask,
			// Diff and Task keep the two-row tab chrome: 50 - (20-2-2) = 34.
			want: 34,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{
				height:     tt.height,
				previewTab: tt.previewTab,
			}

			// Set up content based on tab type
			switch tt.previewTab {
			case PreviewTabOutput:
				wt := &Worktree{
					Name: "test",
					Agent: &Agent{
						OutputBuf: tty.NewOutputBuffer(500),
					},
				}
				// Fill buffer with lines
				content := ""
				for i := 0; i < tt.lineCount; i++ {
					if i > 0 {
						content += "\n"
					}
					content += "line"
				}
				if content != "" {
					wt.Agent.OutputBuf.Write(content)
				}
				p.worktrees = []*Worktree{wt}
				p.selectedIdx = 0
			case PreviewTabTask:
				p.taskRenderedLineCount = tt.lineCount
			}

			got := p.getMaxScrollOffset()
			if got != tt.want {
				t.Errorf("getMaxScrollOffset() = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestPreviewWindowJumps verifies the terminal window's two ends: the live
// bottom it follows from, and the oldest rows it can name.
func TestPreviewWindowJumps(t *testing.T) {
	p := &Plugin{
		height:     20,
		previewTab: PreviewTabOutput,
	}
	wt := &Worktree{
		Name: "test",
		Agent: &Agent{
			OutputBuf: tty.NewOutputBuffer(500),
		},
	}
	// 100 lines of content
	content := ""
	for i := 0; i < 100; i++ {
		if i > 0 {
			content += "\n"
		}
		content += "line"
	}
	wt.Agent.OutputBuf.Write(content)
	p.worktrees = []*Worktree{wt}
	p.selectedIdx = 0

	p.jumpPreviewWindow(p.previewMaxScroll())
	if start := p.terminalViewportLayoutFor(false).Start; start != 0 {
		t.Errorf("jump to oldest: drawn window starts at %d, want the oldest row 0", start)
	}

	p.jumpPreviewWindow(0)
	if p.previewScroll != 0 {
		t.Errorf("jump to live: previewScroll = %d, want 0", p.previewScroll)
	}
}

// TestScrollDirectionConsistency verifies that j/down always moves towards newer
// content and k/up towards older, in whichever model the tab is placed by: a
// document by an absolute line from its top, a terminal by rows back from its
// live bottom.
func TestScrollDirectionConsistency(t *testing.T) {
	t.Run("task tab j increases offset", func(t *testing.T) {
		p := &Plugin{
			height:                20,
			previewTab:            PreviewTabTask,
			previewOffset:         5,
			activePane:            PanePreview,
			taskRenderedLineCount: 100,
		}
		if maxOffset := p.getMaxScrollOffset(); p.previewOffset < maxOffset {
			p.previewOffset++
		}
		if p.previewOffset != 6 {
			t.Errorf("after j: previewOffset = %d, want 6", p.previewOffset)
		}
	})

	t.Run("task tab k decreases offset", func(t *testing.T) {
		p := &Plugin{
			height:                20,
			previewTab:            PreviewTabTask,
			previewOffset:         5,
			activePane:            PanePreview,
			taskRenderedLineCount: 100,
		}
		if p.previewOffset > 0 {
			p.previewOffset--
		}
		if p.previewOffset != 4 {
			t.Errorf("after k: previewOffset = %d, want 4", p.previewOffset)
		}
	})

	t.Run("output tab j moves towards the live bottom", func(t *testing.T) {
		p := scrollTestOutputPlugin(5)
		p.scrollPreviewWindow(-1)
		if p.previewScroll != 4 {
			t.Errorf("after j: previewScroll = %d, want 4", p.previewScroll)
		}
	})

	t.Run("output tab k moves back through scrollback", func(t *testing.T) {
		p := scrollTestOutputPlugin(5)
		p.scrollPreviewWindow(1)
		if p.previewScroll != 6 {
			t.Errorf("after k: previewScroll = %d, want 6", p.previewScroll)
		}
	})
}

// scrollTestOutputPlugin is an Output tab with 100 lines of captured output and
// its window scroll rows back from the live bottom.
func scrollTestOutputPlugin(scroll int) *Plugin {
	p := &Plugin{
		height:        20,
		previewTab:    PreviewTabOutput,
		activePane:    PanePreview,
		previewScroll: scroll,
	}
	buffer := tty.NewOutputBuffer(500)
	buffer.Write(strings.TrimSuffix(strings.Repeat("line\n", 100), "\n"))
	p.worktrees = []*Worktree{{Name: "test", Agent: &Agent{OutputBuf: buffer}}}
	return p
}

// A captured pane usually ends in blank rows, and a window that is not
// following trims them. The furthest back the window may sit has to be the
// bound of the window the render draws, not the raw line count: a count-based
// bound leaves a dead zone of notches at the top of scrollback where nothing
// moves, and the scrollback-history load — which fires when the window reaches
// the bound — only starts after the reader pushes through it.
func TestScrollbackStopsAtTheOldestDrawnRow(t *testing.T) {
	p := &Plugin{previewTab: PreviewTabOutput, width: 120, height: 40}
	buffer := tty.NewOutputBuffer(outputBufferCap)
	buffer.ApplySnapshot(tty.CaptureSnapshot(tty.CaptureInput{
		Output:     strings.Repeat("agent output line\n", 110) + strings.Repeat("\n", 10),
		PaneHeight: 10,
	}))
	p.shellSelected = true
	p.shells = []*ShellSession{{Name: "one", TmuxName: "sc-one", Agent: &Agent{OutputBuf: buffer}}}

	bound := p.previewMaxScroll()
	if bound == 0 {
		t.Fatal("the fixture has no scrollback to walk back through")
	}
	// The bound is the live edge's own start, so the offset the window is placed
	// by and the bound it is clamped to are one measurement of one window. A
	// count-based bound is a different number — here the fixture's blank tail
	// and its letterboxed pane both move it — and mixing the two is what left a
	// dead notch at each end (td-bbbbfe).
	if liveStart := p.terminalViewportLayoutFor(false).Start; liveStart != bound {
		t.Fatalf("live edge starts at %d but the bound is %d — offset 0 and the bound "+
			"are not measured off the same window", liveStart, bound)
	}

	// Walk back further than any bound could allow.
	p.scrollPreviewWindow(1000)
	if p.previewScroll != bound {
		t.Fatalf("previewScroll = %d, want the bound %d", p.previewScroll, bound)
	}
	if start := p.terminalViewportLayoutFor(false).Start; start != 0 {
		t.Fatalf("drawn window starts at %d, want the oldest row 0", start)
	}

	// One notch forward has to move the rows on screen: a window parked past the
	// oldest drawn row would spend the whole dead zone before anything happened.
	p.scrollPreviewWindow(-1)
	if start := p.terminalViewportLayoutFor(false).Start; start != 1 {
		t.Fatalf("after one notch back towards live the window starts at %d, want 1", start)
	}
}

// TestGJumpToTop verifies g reaches the oldest content the tab holds.
func TestGJumpToTop(t *testing.T) {
	t.Run("task", func(t *testing.T) {
		p := &Plugin{previewTab: PreviewTabTask, previewOffset: 50}
		p.previewOffset = 0
		if p.previewOffset != 0 {
			t.Errorf("after g: previewOffset = %d, want 0", p.previewOffset)
		}
	})

	t.Run("output", func(t *testing.T) {
		p := scrollTestOutputPlugin(0)
		p.jumpPreviewWindow(p.previewMaxScroll())
		if start := p.terminalViewportLayoutFor(false).Start; start != 0 {
			t.Errorf("after g: drawn window starts at %d, want the oldest row 0", start)
		}
	})
}

// TestGGJumpToBottom verifies G sets offset to maxOffset for all tabs.
func TestGGJumpToBottom(t *testing.T) {
	p := &Plugin{
		height:                20,
		previewTab:            PreviewTabTask,
		previewOffset:         0,
		taskRenderedLineCount: 100,
	}

	p.previewOffset = p.getMaxScrollOffset()
	expected := 84 // Task tab: 100 - (20 - borders - the two-row tab chrome)
	if p.previewOffset != expected {
		t.Errorf("after G: previewOffset = %d, want %d", p.previewOffset, expected)
	}
}

// Following output is derived from the window's position rather than tracked
// beside it: a window scrolled back is not following, and G returns it.
func TestFollowIsDerivedFromTheWindowPosition(t *testing.T) {
	p := scrollTestOutputPlugin(0)
	if follow, _, _ := p.terminalScrollState(false); !follow {
		t.Error("a window at the live bottom is not following output")
	}

	p.scrollPreviewWindow(1)
	follow, offset, fromBottom := p.terminalScrollState(false)
	if follow || offset != 1 || !fromBottom {
		t.Errorf("after scrolling up: (%v,%d,%v), want (false,1,true)", follow, offset, fromBottom)
	}

	p.jumpPreviewWindow(0)
	if follow, _, _ := p.terminalScrollState(false); !follow {
		t.Error("G did not put the window back on the live edge")
	}
}

// TestPageScrollClamping verifies Ctrl+D/Ctrl+U clamp to bounds.
func TestPageScrollClamping(t *testing.T) {
	t.Run("Ctrl+D clamps to maxOffset", func(t *testing.T) {
		p := &Plugin{
			height:                20,
			previewTab:            PreviewTabTask,
			previewOffset:         80,
			taskRenderedLineCount: 100,
		}
		pageSize := p.height / 2 // 10
		maxOffset := p.getMaxScrollOffset()

		p.previewOffset += pageSize
		if p.previewOffset > maxOffset {
			p.previewOffset = maxOffset
		}

		if p.previewOffset != maxOffset {
			t.Errorf("after Ctrl+D past end: previewOffset = %d, want %d", p.previewOffset, maxOffset)
		}
	})

	t.Run("Ctrl+U clamps to 0", func(t *testing.T) {
		p := &Plugin{
			height:                20,
			previewTab:            PreviewTabTask,
			previewOffset:         3,
			taskRenderedLineCount: 100,
		}
		pageSize := p.height / 2 // 10

		p.previewOffset -= pageSize
		if p.previewOffset < 0 {
			p.previewOffset = 0
		}

		if p.previewOffset != 0 {
			t.Errorf("after Ctrl+U past top: previewOffset = %d, want 0", p.previewOffset)
		}
	})
}

// TestTabSwitchResetsOffset verifies switching tabs resets both scroll models:
// the document to the top of its content, the terminal to its live bottom.
func TestTabSwitchResetsOffset(t *testing.T) {
	p := &Plugin{
		height:        20,
		previewTab:    PreviewTabOutput,
		previewOffset: 50,
		previewScroll: 12,
	}

	p.previewTab = PreviewTabTask
	p.resetPreviewScroll()

	if p.previewOffset != 0 {
		t.Errorf("after tab switch: previewOffset = %d, want 0", p.previewOffset)
	}
	if p.previewScroll != 0 {
		t.Errorf("after tab switch: previewScroll = %d, want the live bottom", p.previewScroll)
	}
}

// TestGetPreviewVisibleHeight verifies the visible height estimation.
func TestGetPreviewVisibleHeight(t *testing.T) {
	tests := []struct {
		name   string
		height int
		tab    PreviewTab
		shell  bool
		want   int
	}{
		// A terminal surface spends one row on its own header...
		{"output normal height", 30, PreviewTabOutput, false, 27},
		{"output small height", 5, PreviewTabOutput, false, 2},
		{"shell normal height", 30, PreviewTabDiff, true, 27},
		// ...while Diff and Task keep the tab row and its blank spacer.
		{"diff normal height", 30, PreviewTabDiff, false, 26},
		{"task normal height", 30, PreviewTabTask, false, 26},
		{"task small height", 5, PreviewTabTask, false, 1},
		{"zero height", 0, PreviewTabOutput, false, 1},
		{"negative height", -5, PreviewTabOutput, false, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{height: tt.height, previewTab: tt.tab, shellSelected: tt.shell}
			if tt.shell {
				p.shells = []*ShellSession{{TmuxName: "shell"}}
			}
			got := p.getPreviewVisibleHeight()
			if got != tt.want {
				t.Errorf("getPreviewVisibleHeight() = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestScrollPreviewUnified verifies mouse scrollPreview uses unified top-down semantics.
func TestScrollPreviewUnified(t *testing.T) {
	t.Run("scroll up decreases offset for task tab", func(t *testing.T) {
		p := &Plugin{
			height:                20,
			previewTab:            PreviewTabTask,
			previewOffset:         10,
			taskRenderedLineCount: 100,
		}

		p.scrollPreview(-1) // scroll up
		if p.previewOffset != 9 {
			t.Errorf("after scroll up: previewOffset = %d, want 9", p.previewOffset)
		}
	})

	t.Run("scroll down increases offset for task tab", func(t *testing.T) {
		p := &Plugin{
			height:                20,
			previewTab:            PreviewTabTask,
			previewOffset:         10,
			taskRenderedLineCount: 100,
		}

		p.scrollPreview(1) // scroll down
		if p.previewOffset != 11 {
			t.Errorf("after scroll down: previewOffset = %d, want 11", p.previewOffset)
		}
	})

	t.Run("scroll up at top stays at 0", func(t *testing.T) {
		p := &Plugin{
			height:                20,
			previewTab:            PreviewTabTask,
			previewOffset:         0,
			taskRenderedLineCount: 100,
		}

		p.scrollPreview(-1) // scroll up at top
		if p.previewOffset != 0 {
			t.Errorf("after scroll up at top: previewOffset = %d, want 0", p.previewOffset)
		}
	})
}

// Wheel notches over an interactive pane belong to the app running there as
// soon as it has enabled mouse tracking. Claude Code turns on 1003+1006 and
// keeps tmux's history empty, so consuming the notch as local scrollback slid
// the viewport across its live frame and tore the layout.
func TestWheelForwardsToPaneWhenAppTracksMouse(t *testing.T) {
	logPath := installSuccessfulFakeTmux(t)
	p := newInteractiveInputTestPlugin()
	p.width, p.height = 100, 30
	p.shellSelected = true
	attachLiveTerminal(p, true)

	cmd := p.handleMouseScroll(mouse.MouseAction{Type: mouse.ActionScrollUp, Delta: -1, X: 10, Y: 5})
	if cmd == nil {
		t.Fatal("expected a command forwarding the wheel notch to the pane")
	}
	tty.WaitForPendingSends()

	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	// SGR wheel-up press: ESC [ < 6 4 ; ... M, hex-encoded because the report
	// contains semicolons.
	if !strings.Contains(string(logged), "3c 36 34 3b") {
		t.Fatalf("no SGR wheel-up report reached tmux: %s", logged)
	}

	if p.previewScroll != 0 {
		t.Fatalf("local scrollback moved: previewScroll=%d", p.previewScroll)
	}
}

// A viewport left scrolled back must snap to the live edge once the app owns the
// wheel, or it would sit frozen over stale rows while the app repaints below.
func TestForwardedWheelPinsViewportToLiveOutput(t *testing.T) {
	installSuccessfulFakeTmux(t)
	p := newInteractiveInputTestPlugin()
	p.width, p.height = 100, 30
	p.shellSelected = true
	p.previewScroll = 12
	attachLiveTerminal(p, true)

	p.handleMouseScroll(mouse.MouseAction{Type: mouse.ActionScrollUp, Delta: -1, X: 10, Y: 5})
	tty.WaitForPendingSends()

	if p.previewScroll != 0 {
		t.Fatalf("viewport not pinned to live: previewScroll=%d", p.previewScroll)
	}
}

func TestWheelDownForwardsWheelDownButton(t *testing.T) {
	logPath := installSuccessfulFakeTmux(t)
	p := newInteractiveInputTestPlugin()
	p.width, p.height = 100, 30
	p.shellSelected = true
	attachLiveTerminal(p, true)

	p.handleMouseScroll(mouse.MouseAction{Type: mouse.ActionScrollDown, Delta: 1, X: 10, Y: 5})
	tty.WaitForPendingSends()

	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logged), "3c 36 35 3b") {
		t.Fatalf("no SGR wheel-down report reached tmux: %s", logged)
	}
}

// A plain shell tracks no mouse, so the wheel keeps scrolling the captured
// scrollback exactly as before.
func TestWheelScrollsScrollbackWhenAppIgnoresMouse(t *testing.T) {
	logPath := installSuccessfulFakeTmux(t)
	p := newInteractiveInputTestPlugin()
	p.width, p.height = 100, 30
	givePaneScrollableOutput(p, 120)
	// Far enough back from the live bottom that a notch has somewhere to go.
	p.previewScroll = 5
	attachLiveTerminal(p, false)

	p.handleMouseScroll(mouse.MouseAction{Type: mouse.ActionScrollUp, Delta: -1, X: 10, Y: 5})
	tty.WaitForPendingSends()

	if p.previewScroll != 6 {
		t.Fatalf("previewScroll = %d, want 6 after scrolling local scrollback back a row", p.previewScroll)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if strings.Contains(string(logged), "send-keys") {
		t.Fatalf("wheel was forwarded to a pane that tracks no mouse: %s", logged)
	}
}

// Alt is the "give me the terminal, not the app" modifier for the wheel. The
// event is built the way mouse.HandleMouse builds it for alt+wheel — plain
// ActionScrollUp carrying Alt — so the escape hatch is exercised as it is
// actually reachable. (Shift+wheel never gets here; HandleMouse maps it to
// horizontal scroll.)
func TestWheelWithAltScrollsScrollbackDespiteMouseTracking(t *testing.T) {
	logPath := installSuccessfulFakeTmux(t)
	p := newInteractiveInputTestPlugin()
	p.width, p.height = 100, 30
	givePaneScrollableOutput(p, 120)
	// Far enough back from the live bottom that a notch has somewhere to go.
	p.previewScroll = 5
	attachLiveTerminal(p, true)

	p.handleMouseScroll(mouse.MouseAction{Type: mouse.ActionScrollUp, Delta: -1, X: 10, Y: 5, Alt: true})
	tty.WaitForPendingSends()

	if p.previewScroll != 6 {
		t.Fatalf("previewScroll = %d, want 6 — alt+wheel must stay local", p.previewScroll)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if strings.Contains(string(logged), "send-keys") {
		t.Fatalf("alt+wheel was forwarded to the pane: %s", logged)
	}
}

// A pointer that does not land inside the pane has no coordinates to report, so
// the notch falls back to the viewport rather than being dropped.
func TestWheelOutsidePaneFallsBackToScrollback(t *testing.T) {
	installSuccessfulFakeTmux(t)
	p := newInteractiveInputTestPlugin()
	p.width, p.height = 100, 30
	givePaneScrollableOutput(p, 120)
	// Far enough back from the live bottom that a notch has somewhere to go.
	p.previewScroll = 5
	attachLiveTerminal(p, true)

	p.handleMouseScroll(mouse.MouseAction{Type: mouse.ActionScrollUp, Delta: -1, X: 0, Y: 0})
	if p.previewScroll != 6 {
		t.Fatalf("previewScroll = %d, want 6 when the pointer maps outside the pane", p.previewScroll)
	}
}

// The mouse flag has to come from tmux: `capture-pane -e` emits rendering
// escapes only, so DECSET mode sequences never appear in captured output.
func TestPaneMetadataCarriesMouseFlag(t *testing.T) {
	args := capturePaneWithCursorArgs("sidecar-test", "%1", false)
	found := false
	for _, arg := range args {
		if strings.Contains(arg, "#{mouse_any_flag}") {
			found = true
		}
	}
	if !found {
		t.Fatalf("cursor metadata does not ask tmux for the mouse flag: %#v", args)
	}

	if cursor := parseCapturedCursor("12,4,1,30,100,7,1"); !cursor.Valid || !cursor.MouseReporting {
		t.Fatalf("parsed cursor = %#v, want MouseReporting", cursor)
	}
	if cursor := parseCapturedCursor("12,4,1,30,100,7,0"); !cursor.Valid || cursor.MouseReporting {
		t.Fatalf("parsed cursor = %#v, want MouseReporting false", cursor)
	}
	// Metadata predating the field still parses.
	if cursor := parseCapturedCursor("12,4,1,30,100,7"); !cursor.Valid || cursor.MouseReporting {
		t.Fatalf("parsed legacy cursor = %#v", cursor)
	}
}

// One physical wheel notch must reach the pane as one wheel report. Vertical
// wheel actions carry Delta in lines (mouse.WheelScrollLines per notch), and
// forwarding that line count made every notch scroll roughly three times as far
// as it does in a real terminal.
func TestForwardedWheelSendsOneReportPerNotch(t *testing.T) {
	logPath := installSuccessfulFakeTmux(t)
	p := newInteractiveInputTestPlugin()
	p.width, p.height = 100, 30
	p.shellSelected = true
	attachLiveTerminal(p, true)

	// Exactly what mouse.HandleMouse produces for a single wheel-up notch.
	p.handleMouseScroll(mouse.MouseAction{
		Type: mouse.ActionScrollUp, Delta: -mouse.WheelScrollLines, X: 10, Y: 5,
	})
	tty.WaitForPendingSends()

	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	// Hex-encoded "ESC [ < 6 4 ;" — the start of an SGR wheel-up report.
	if got := strings.Count(string(logged), "1b 5b 3c 36 34 3b"); got != 1 {
		t.Fatalf("one notch produced %d wheel reports, want 1: %s", got, logged)
	}
}

// A forwarded wheel changed the pane, so the capture that repaints it must not
// be deferred behind the scroll-burst window the local viewport uses, and the
// notch has to count as activity or polling decays to its slow tier.
func TestForwardedWheelKeepsRepaintPrompt(t *testing.T) {
	installSuccessfulFakeTmux(t)
	p := newInteractiveInputTestPlugin()
	p.width, p.height = 100, 30
	p.shellSelected = true
	attachLiveTerminal(p, true)
	p.interactiveState.LastKeyTime = time.Now().Add(-time.Hour)

	p.handleMouseScroll(mouse.MouseAction{
		Type: mouse.ActionScrollUp, Delta: -mouse.WheelScrollLines, X: 10, Y: 5,
	})
	tty.WaitForPendingSends()

	if time.Since(p.interactiveState.LastKeyTime) > time.Minute {
		t.Fatal("forwarded wheel did not count as activity; polling would decay to its slow tier")
	}
	if delay, deferred := p.interactiveScrollDelay(); deferred {
		t.Fatalf("capture deferred by %s after a wheel the app consumed", delay)
	}

	// The deferral still applies when the wheel moves sidecar's own viewport,
	// which repaints without a capture.
	attachLiveTerminal(p, false)
	if _, deferred := p.interactiveScrollDelay(); !deferred {
		t.Fatal("scroll-burst deferral lost for locally handled scrolling")
	}
}

// Pinning the viewport is a jump, so a selection anchored to buffer lines would
// be left highlighting rows the user never picked.
func TestPinningViewportClearsSelection(t *testing.T) {
	p := newInteractiveInputTestPlugin()
	p.previewScroll = 12
	p.selection.SelectRange(ui.SelectionPoint{Line: 2, Col: 0}, ui.SelectionPoint{Line: 4, Col: 5}, false)
	if !p.selection.HasSelection() {
		t.Fatal("test setup did not produce a selection")
	}

	p.pinTerminalWindowToLive(false)
	if p.selection.HasSelection() {
		t.Fatal("selection survived the jump to the live edge")
	}

	// An already-live viewport is left alone, selection included.
	p.selection.SelectRange(ui.SelectionPoint{Line: 2, Col: 0}, ui.SelectionPoint{Line: 4, Col: 5}, false)
	p.pinTerminalWindowToLive(false)
	if !p.selection.HasSelection() {
		t.Fatal("selection cleared even though the viewport was already live")
	}
}

// The terminal panel's bound is the bound of the window the panel draws, taken
// from the drawn layout like every other surface's. It used to hand-roll the
// trim off the panel's own dimensions, which is a second derivation of one
// window: a pane letterboxed into the panel box put the two several rows apart,
// so the first notch off the live edge jumped instead of stepping (td-bbbbfe).
func TestPanelBoundIsTheDrawnPanelWindow(t *testing.T) {
	p := passiveWheelPanelPlugin(t)
	// A pane shorter than the panel box, with history loaded above it: the
	// geometry the two derivations disagreed about.
	rows := make([]string, 0, 120)
	for i := range 120 {
		rows = append(rows, fmt.Sprintf("panel row %03d", i))
	}
	panel := tty.NewOutputBuffer(400)
	panel.ApplySnapshot(tty.CaptureSnapshot(tty.CaptureInput{
		Output: strings.Join(rows, "\n"), BaseLine: 500, Absolute: true,
		PaneHeight: 3,
	}))
	p.termPanelOutput = panel

	bound := p.termPanelMaxScroll()
	p.termPanelScroll = 0
	live := p.terminalViewportLayoutFor(true).Start
	if live != bound {
		t.Fatalf("panel live edge starts at %d but its bound is %d — the panel's window "+
			"and its bound are not one measurement", live, bound)
	}

	p.scrollTermPanelWindow(1)
	if got := p.terminalViewportLayoutFor(true).Start; got != live-1 {
		t.Fatalf("one notch back off the panel's live edge started at %d, want %d", got, live-1)
	}

	p.scrollTermPanelWindow(bound)
	if got := p.terminalViewportLayoutFor(true).Start; got != 0 {
		t.Fatalf("at the panel's bound the window starts at %d, want the oldest row 0", got)
	}
}
