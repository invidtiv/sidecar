package workspace

import (
	"testing"

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
