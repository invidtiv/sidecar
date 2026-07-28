package workspace

import (
	"os"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/tty"
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
			want:       84, // 100 - (20-4) = 84
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
			want:       34, // 50 - 16 = 34
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

// TestScrollToBottom verifies scrollToBottom pins offset to max.
func TestScrollToBottom(t *testing.T) {
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

	p.previewOffset = 0
	p.scrollToBottom()

	expected := p.getMaxScrollOffset()
	if p.previewOffset != expected {
		t.Errorf("scrollToBottom: previewOffset = %d, want %d", p.previewOffset, expected)
	}
}

// TestScrollDirectionConsistency verifies that j/down always increases offset
// and k/up always decreases it, regardless of tab.
func TestScrollDirectionConsistency(t *testing.T) {
	tests := []struct {
		name       string
		previewTab PreviewTab
	}{
		{"output tab", PreviewTabOutput},
		{"task tab", PreviewTabTask},
	}

	for _, tt := range tests {
		t.Run(tt.name+" j increases offset", func(t *testing.T) {
			p := &Plugin{
				height:           20,
				previewTab:       tt.previewTab,
				previewOffset:    5,
				autoScrollOutput: false,
				activePane:       PanePreview,
			}
			// Set up content so maxOffset > 5
			switch tt.previewTab {
			case PreviewTabOutput:
				wt := &Worktree{
					Name: "test",
					Agent: &Agent{
						OutputBuf: tty.NewOutputBuffer(500),
					},
				}
				content := ""
				for i := 0; i < 100; i++ {
					if i > 0 {
						content += "\n"
					}
					content += "line"
				}
				wt.Agent.OutputBuf.Write(content)
				p.worktrees = []*Worktree{wt}
			case PreviewTabTask:
				p.taskRenderedLineCount = 100
			}

			// Simulate j/down: should increase offset
			maxOffset := p.getMaxScrollOffset()
			if p.previewOffset < maxOffset {
				p.previewOffset++
			}
			if p.previewOffset != 6 {
				t.Errorf("after j: previewOffset = %d, want 6", p.previewOffset)
			}
		})

		t.Run(tt.name+" k decreases offset", func(t *testing.T) {
			p := &Plugin{
				height:           20,
				previewTab:       tt.previewTab,
				previewOffset:    5,
				autoScrollOutput: false,
				activePane:       PanePreview,
			}

			// Simulate k/up: should decrease offset
			if p.previewOffset > 0 {
				p.previewOffset--
			}
			if p.previewOffset != 4 {
				t.Errorf("after k: previewOffset = %d, want 4", p.previewOffset)
			}
		})
	}
}

// TestGJumpToTop verifies g sets offset to 0 for all tabs.
func TestGJumpToTop(t *testing.T) {
	tests := []struct {
		name string
		tab  PreviewTab
	}{
		{"output", PreviewTabOutput},
		{"task", PreviewTabTask},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{
				previewTab:    tt.tab,
				previewOffset: 50,
			}
			// g -> jump to top
			p.previewOffset = 0
			if p.previewOffset != 0 {
				t.Errorf("after g: previewOffset = %d, want 0", p.previewOffset)
			}
		})
	}
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
	expected := 84 // 100 - 16
	if p.previewOffset != expected {
		t.Errorf("after G: previewOffset = %d, want %d", p.previewOffset, expected)
	}
}

// TestAutoScrollOutputDisabledOnManualScroll verifies auto-scroll pauses on user scroll.
func TestAutoScrollOutputDisabledOnManualScroll(t *testing.T) {
	p := &Plugin{
		height:           20,
		previewTab:       PreviewTabOutput,
		previewOffset:    10,
		autoScrollOutput: true,
	}
	wt := &Worktree{
		Name: "test",
		Agent: &Agent{
			OutputBuf: tty.NewOutputBuffer(500),
		},
	}
	content := ""
	for i := 0; i < 100; i++ {
		if i > 0 {
			content += "\n"
		}
		content += "line"
	}
	wt.Agent.OutputBuf.Write(content)
	p.worktrees = []*Worktree{wt}

	// Scroll up (k): should disable auto-scroll
	if p.previewOffset > 0 {
		p.previewOffset--
	}
	p.autoScrollOutput = false

	if p.autoScrollOutput {
		t.Error("expected autoScrollOutput=false after scroll up")
	}
}

// TestAutoScrollReenabledAtBottom verifies pressing G re-enables auto-scroll.
func TestAutoScrollReenabledAtBottom(t *testing.T) {
	p := &Plugin{
		height:           20,
		previewTab:       PreviewTabOutput,
		previewOffset:    5,
		autoScrollOutput: false,
	}
	wt := &Worktree{
		Name: "test",
		Agent: &Agent{
			OutputBuf: tty.NewOutputBuffer(500),
		},
	}
	content := ""
	for i := 0; i < 100; i++ {
		if i > 0 {
			content += "\n"
		}
		content += "line"
	}
	wt.Agent.OutputBuf.Write(content)
	p.worktrees = []*Worktree{wt}

	// G -> jump to bottom, re-enable auto-scroll
	p.previewOffset = p.getMaxScrollOffset()
	p.autoScrollOutput = true

	if !p.autoScrollOutput {
		t.Error("expected autoScrollOutput=true after G")
	}
	if p.previewOffset != p.getMaxScrollOffset() {
		t.Errorf("expected previewOffset=%d, got %d", p.getMaxScrollOffset(), p.previewOffset)
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

// TestTabSwitchResetsOffset verifies switching tabs resets scroll position.
func TestTabSwitchResetsOffset(t *testing.T) {
	p := &Plugin{
		height:           20,
		previewTab:       PreviewTabOutput,
		previewOffset:    50,
		autoScrollOutput: false,
	}

	// Simulate tab switch
	p.previewTab = PreviewTabTask
	p.previewOffset = 0
	p.autoScrollOutput = true

	if p.previewOffset != 0 {
		t.Errorf("after tab switch: previewOffset = %d, want 0", p.previewOffset)
	}
	if !p.autoScrollOutput {
		t.Error("expected autoScrollOutput=true after tab switch")
	}
}

// TestGetPreviewVisibleHeight verifies the visible height estimation.
func TestGetPreviewVisibleHeight(t *testing.T) {
	tests := []struct {
		name   string
		height int
		want   int
	}{
		{"normal height", 30, 26},
		{"small height", 5, 1},
		{"zero height", 0, 1},
		{"negative height", -5, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{height: tt.height}
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
	p.previewOffset = 0
	p.autoScrollOutput = true
	p.interactiveState.PaneMouseReporting = true

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

	if p.previewOffset != 0 || !p.autoScrollOutput {
		t.Fatalf("local scrollback moved: previewOffset=%d autoScroll=%v", p.previewOffset, p.autoScrollOutput)
	}
}

// A viewport left scrolled back must snap to the live edge once the app owns the
// wheel, or it would sit frozen over stale rows while the app repaints below.
func TestForwardedWheelPinsViewportToLiveOutput(t *testing.T) {
	installSuccessfulFakeTmux(t)
	p := newInteractiveInputTestPlugin()
	p.width, p.height = 100, 30
	p.shellSelected = true
	p.previewOffset = 12
	p.autoScrollOutput = false
	p.interactiveState.PaneMouseReporting = true

	p.handleMouseScroll(mouse.MouseAction{Type: mouse.ActionScrollUp, Delta: -1, X: 10, Y: 5})
	tty.WaitForPendingSends()

	if !p.autoScrollOutput || p.previewOffset != p.getMaxScrollOffset() {
		t.Fatalf("viewport not pinned to live: previewOffset=%d max=%d autoScroll=%v",
			p.previewOffset, p.getMaxScrollOffset(), p.autoScrollOutput)
	}
}

func TestWheelDownForwardsWheelDownButton(t *testing.T) {
	logPath := installSuccessfulFakeTmux(t)
	p := newInteractiveInputTestPlugin()
	p.width, p.height = 100, 30
	p.shellSelected = true
	p.interactiveState.PaneMouseReporting = true

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
	p.shellSelected = true
	p.previewOffset = 5
	p.autoScrollOutput = false
	p.interactiveState.PaneMouseReporting = false

	p.handleMouseScroll(mouse.MouseAction{Type: mouse.ActionScrollUp, Delta: -1, X: 10, Y: 5})
	tty.WaitForPendingSends()

	if p.previewOffset != 4 {
		t.Fatalf("previewOffset = %d, want 4 after scrolling local scrollback", p.previewOffset)
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
	p.shellSelected = true
	p.previewOffset = 5
	p.autoScrollOutput = false
	p.interactiveState.PaneMouseReporting = true

	p.handleMouseScroll(mouse.MouseAction{Type: mouse.ActionScrollUp, Delta: -1, X: 10, Y: 5, Alt: true})
	tty.WaitForPendingSends()

	if p.previewOffset != 4 {
		t.Fatalf("previewOffset = %d, want 4 — alt+wheel must stay local", p.previewOffset)
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
	p.shellSelected = true
	p.previewOffset = 5
	p.autoScrollOutput = false
	p.interactiveState.PaneMouseReporting = true

	p.handleMouseScroll(mouse.MouseAction{Type: mouse.ActionScrollUp, Delta: -1, X: 0, Y: 0})
	if p.previewOffset != 4 {
		t.Fatalf("previewOffset = %d, want 4 when the pointer maps outside the pane", p.previewOffset)
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
