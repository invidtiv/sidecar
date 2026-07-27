package workspace

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

func testTerminalBuffer(content string) *tty.OutputBuffer {
	buffer := tty.NewOutputBuffer(100)
	buffer.Write(content)
	return buffer
}

func TestCalculateTerminalViewportLayout(t *testing.T) {
	buffer := testTerminalBuffer("0\n1\n2\n3\n4\n5\n6\n7\n8\n9")

	tests := []struct {
		name  string
		input terminalViewportInput
		start int
		end   int
	}{
		{
			name:  "follow live edge",
			input: terminalViewportInput{Buffer: buffer, Width: 80, Height: 3, Follow: true},
			start: 7,
			end:   10,
		},
		{
			name:  "absolute offset",
			input: terminalViewportInput{Buffer: buffer, Width: 80, Height: 3, Offset: 2},
			start: 2,
			end:   5,
		},
		{
			name:  "offset from bottom",
			input: terminalViewportInput{Buffer: buffer, Width: 80, Height: 3, Offset: 2, OffsetFromBottom: true},
			start: 5,
			end:   8,
		},
		{
			name: "interactive pane bounds",
			input: terminalViewportInput{
				Buffer: buffer, Width: 80, Height: 8, Follow: true,
				Interactive: true, PaneWidth: 20, PaneHeight: 4,
			},
			start: 6,
			end:   10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateTerminalViewportLayout(tt.input)
			if got.Start != tt.start || got.End != tt.end {
				t.Fatalf("range = [%d,%d), want [%d,%d)", got.Start, got.End, tt.start, tt.end)
			}
		})
	}
}

func TestTerminalViewportTrimsTrailingEmptyWithoutCopyingState(t *testing.T) {
	buffer := testTerminalBuffer("prompt\n\n\n")
	input := terminalViewportInput{
		Buffer:       buffer,
		Width:        80,
		Height:       4,
		Follow:       true,
		TrimTrailing: true,
	}
	before := input
	result := renderTerminalViewport(input, ui.NewTruncateCache(32))

	if result.Content != "prompt" {
		t.Fatalf("content = %q, want prompt", result.Content)
	}
	if input != before {
		t.Fatal("renderTerminalViewport mutated its input")
	}
	if result.Layout.EffectiveCount != 1 {
		t.Fatalf("effective count = %d, want 1", result.Layout.EffectiveCount)
	}
}

func TestTerminalViewportExpandsAndTruncatesOnce(t *testing.T) {
	buffer := testTerminalBuffer("a\t0123456789")
	result := renderTerminalViewport(terminalViewportInput{
		Buffer: buffer,
		Width:  6,
		Height: 1,
		Follow: true,
	}, ui.NewTruncateCache(32))

	if strings.Contains(result.Content, "\t") {
		t.Fatalf("tab was not expanded: %q", result.Content)
	}
	if got := ansi.StringWidth(result.Content); got > 6 {
		t.Fatalf("visible width = %d, want <= 6", got)
	}
}

func TestTerminalPanelUsesPanelBufferForSelection(t *testing.T) {
	panel := testTerminalBuffer("panel-0\npanel-1\npanel-2")
	agent := testTerminalBuffer("agent-0")
	p := &Plugin{
		viewMode:        ViewModeInteractive,
		termPanelOutput: panel,
		interactiveState: &InteractiveState{
			Active:    true,
			TermPanel: true,
		},
		worktrees: []*Worktree{{Agent: &Agent{OutputBuf: agent}}},
	}

	if got := p.interactiveOutputBuffer(); got != panel {
		t.Fatal("terminal-panel selection did not use terminal-panel output")
	}
}

func TestTerminalRenderersDoNotMutateViewportState(t *testing.T) {
	agentBuffer := testTerminalBuffer("0\n1\n2\n3\n4\n5")
	panelBuffer := testTerminalBuffer("a\nb\nc\nd\ne")
	state := &InteractiveState{
		Active:           true,
		VisibleStart:     91,
		VisibleEnd:       99,
		ContentRowOffset: 7,
		PaneHeight:       3,
		PaneWidth:        20,
	}
	p := &Plugin{
		viewMode:         ViewModeInteractive,
		previewTab:       PreviewTabOutput,
		selectedIdx:      0,
		previewOffset:    2,
		autoScrollOutput: true,
		termPanelOutput:  panelBuffer,
		termPanelScroll:  99,
		interactiveState: state,
		worktrees: []*Worktree{{
			Agent: &Agent{OutputBuf: agentBuffer},
		}},
		truncateCache: ui.NewTruncateCache(32),
	}
	p.selection.Clear()
	beforeState := *state
	beforePreviewOffset := p.previewOffset
	beforePanelScroll := p.termPanelScroll

	_ = p.renderOutputContent(20, 4)
	if *state != beforeState {
		t.Fatalf("agent render mutated interactive state: got %+v want %+v", *state, beforeState)
	}
	state.TermPanel = true
	beforeState = *state
	_ = p.renderTermPanelOutput(20, 4)

	if *state != beforeState {
		t.Fatalf("panel render mutated interactive state: got %+v want %+v", *state, beforeState)
	}
	if p.previewOffset != beforePreviewOffset {
		t.Fatalf("render mutated previewOffset: got %d want %d", p.previewOffset, beforePreviewOffset)
	}
	if p.termPanelScroll != beforePanelScroll {
		t.Fatalf("render mutated termPanelScroll: got %d want %d", p.termPanelScroll, beforePanelScroll)
	}
}

func TestShiftPageUpScrollsSidecarViewport(t *testing.T) {
	buffer := tty.NewOutputBuffer(200)
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, "line")
	}
	buffer.Write(strings.Join(lines, "\n"))
	p := &Plugin{
		width:            80,
		height:           20,
		viewMode:         ViewModeInteractive,
		previewTab:       PreviewTabOutput,
		selectedIdx:      0,
		autoScrollOutput: true,
		interactiveState: &InteractiveState{Active: true},
		worktrees: []*Worktree{{
			Agent: &Agent{OutputBuf: buffer},
		}},
	}

	handled, _ := p.handleInteractiveScrollbackKey(tea.KeyPressMsg{Code: tea.KeyPgUp, Mod: tea.ModShift})
	if !handled {
		t.Fatal("shift+PageUp was forwarded instead of scrolling sidecar")
	}
	if p.autoScrollOutput {
		t.Fatal("shift+PageUp did not leave live-follow mode")
	}
	if p.previewOffset >= p.getMaxScrollOffset() {
		t.Fatalf("shift+PageUp did not move back: offset=%d max=%d", p.previewOffset, p.getMaxScrollOffset())
	}
}

func TestTerminalPanelSelectionMapsFromPanelViewport(t *testing.T) {
	panel := testTerminalBuffer("0\n1\n2\n3\n4\n5\n6\n7\n8\n9")
	p := &Plugin{
		width:            80,
		height:           20,
		viewMode:         ViewModeInteractive,
		termPanelVisible: true,
		termPanelOutput:  panel,
		interactiveState: &InteractiveState{Active: true, TermPanel: true},
	}
	p.selection.ViewRect = mouse.Rect{X: 10, Y: 5, W: 40, H: 8}

	layout := p.interactiveViewportLayout()
	line, ok := p.interactiveLineIndexAtY(6) // first row after the panel hint
	if !ok {
		t.Fatal("terminal-panel output row did not map to its buffer")
	}
	if line != layout.Start {
		t.Fatalf("mapped line = %d, want viewport start %d", line, layout.Start)
	}
}

func TestAgentUpdatesDoNotHijackInteractiveTerminalPanel(t *testing.T) {
	tests := []struct {
		name string
		msg  tea.Msg
	}{
		{
			name: "changed output",
			msg: AgentOutputMsg{
				WorkspaceName: "work",
				Output:        "\x1b[?2004l\x1b[?1000l",
				Status:        StatusActive,
				HasCursor:     true,
				CursorRow:     1,
				CursorCol:     2,
				PaneHeight:    10,
				PaneWidth:     20,
			},
		},
		{
			name: "unchanged output",
			msg: AgentPollUnchangedMsg{
				WorkspaceName: "work",
				CurrentStatus: StatusActive,
				HasCursor:     true,
				CursorRow:     1,
				CursorCol:     2,
				PaneHeight:    10,
				PaneWidth:     20,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := &InteractiveState{
				Active:                true,
				TermPanel:             true,
				BracketedPasteEnabled: true,
				MouseReportingEnabled: true,
				CursorRow:             8,
				CursorCol:             9,
				PaneHeight:            30,
				PaneWidth:             90,
			}
			p := &Plugin{
				focused:          true,
				viewMode:         ViewModeInteractive,
				previewTab:       PreviewTabOutput,
				selectedIdx:      0,
				interactiveState: state,
				pollGeneration:   map[string]int{"work": 4},
				worktrees: []*Worktree{{
					Name:  "work",
					Agent: &Agent{OutputBuf: tty.NewOutputBuffer(10)},
				}},
			}
			before := *state

			_, cmd := p.Update(tt.msg)
			if *state != before {
				t.Fatalf("agent update overwrote terminal-panel modes: got %+v want %+v", *state, before)
			}
			if cmd == nil {
				t.Fatal("agent update did not schedule a continuation")
			}
			result := cmd()
			poll, ok := result.(pollAgentMsg)
			if !ok {
				t.Fatalf("continuation = %T, want pollAgentMsg (terminal panel poll hijacked agent chain)", result)
			}
			if poll.WorkspaceName != "work" || poll.Generation != 4 {
				t.Fatalf("agent continuation = %+v", poll)
			}
		})
	}
}

func TestBottomSplitDimensionsMatchRenderedTerminalContent(t *testing.T) {
	for _, shellSelected := range []bool{false, true} {
		t.Run(map[bool]string{false: "worktree", true: "shell"}[shellSelected], func(t *testing.T) {
			p := &Plugin{
				width:            100,
				height:           30,
				shellSelected:    shellSelected,
				termPanelVisible: true,
				termPanelLayout:  TermPanelBottom,
				termPanelSize:    50,
			}
			_, previewContentHeight := p.calculatePreviewDimensions()
			containerHeight := previewContentHeight + 1
			termBoxHeight := containerHeight * p.termPanelEffectiveSize() / 100
			outputBoxHeight := containerHeight - termBoxHeight - 1

			_, gotTermHeight := p.calculateTermPanelDimensions()
			_, gotOutputHeight := p.calculateAgentPaneDimensions()
			if gotTermHeight != termBoxHeight-1 {
				t.Fatalf("terminal content height = %d, rendered child content = %d", gotTermHeight, termBoxHeight-1)
			}
			if gotOutputHeight != outputBoxHeight-1 {
				t.Fatalf("output content height = %d, rendered child content = %d", gotOutputHeight, outputBoxHeight-1)
			}
		})
	}
}

func TestTerminalPanelSelectionStopsOnlyPanelFollow(t *testing.T) {
	panel := testTerminalBuffer("0\n1\n2\n3\n4\n5\n6\n7\n8\n9")
	handler := mouse.NewHandler()
	p := &Plugin{
		width:            80,
		height:           20,
		viewMode:         ViewModeInteractive,
		autoScrollOutput: true,
		termPanelVisible: true,
		termPanelOutput:  panel,
		mouseHandler:     handler,
		interactiveState: &InteractiveState{Active: true, TermPanel: true},
	}
	rect := mouse.Rect{X: 10, Y: 5, W: 40, H: 8}
	action := mouse.MouseAction{
		Type:   mouse.ActionClick,
		X:      rect.X,
		Y:      rect.Y + 1,
		Region: &mouse.Region{ID: regionTermPanelContent, Rect: rect},
	}

	p.prepareInteractiveDrag(action)
	if !p.selection.Anchor.Valid() {
		t.Fatal("panel selection did not establish an anchor")
	}
	if !p.autoScrollOutput {
		t.Fatal("panel selection disabled independent agent auto-follow")
	}
	frozen := p.interactiveViewportLayout()
	panel.Write("0\n1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11")
	afterAppend := p.interactiveViewportLayout()
	if afterAppend.Start != frozen.Start {
		t.Fatalf("panel selection viewport moved on append: start %d -> %d", frozen.Start, afterAppend.Start)
	}
}

func TestTerminalEmptyStatesRespectNarrowWidth(t *testing.T) {
	cache := ui.NewTruncateCache(32)
	emptyBuffer := &Plugin{
		selectedIdx:   0,
		truncateCache: cache,
		worktrees: []*Worktree{{
			Agent: &Agent{},
		}},
	}
	noAgent := &Plugin{
		selectedIdx:   0,
		truncateCache: cache,
		worktrees:     []*Worktree{{}},
	}
	orphan := &Plugin{
		selectedIdx:   0,
		truncateCache: cache,
		worktrees: []*Worktree{{
			IsOrphaned: true,
		}},
	}
	noShell := &Plugin{
		shellSelected: true,
		truncateCache: cache,
	}

	for name, content := range map[string]string{
		"empty-buffer": emptyBuffer.renderOutputContent(5, 3),
		"no-agent":     noAgent.renderOutputContent(5, 3),
		"orphan":       orphan.renderOutputContent(5, 8),
		"no-shell":     noShell.renderShellOutput(5, 8),
		"panel":        emptyBuffer.renderTermPanelOutput(5, 3),
	} {
		for _, line := range strings.Split(content, "\n") {
			if width := ansi.StringWidth(line); width > 5 {
				t.Fatalf("%s empty-state width = %d, want <= 5: %q", name, width, line)
			}
		}
	}
}
