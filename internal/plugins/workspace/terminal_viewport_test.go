package workspace

import (
	"fmt"
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

// terminalTextLines returns the rendered rows with viewport chrome removed: the
// scrollbar owns the final column unconditionally (td-0818ef), and lines are
// padded out to it, so tests asserting on pane text strip both.
func terminalTextLines(result terminalViewportResult) []string {
	lines := strings.Split(ansi.Strip(result.Content), "\n")
	for i, line := range lines {
		if result.Layout.ShowScrollbar {
			line = ansi.Truncate(line, max(ansi.StringWidth(line)-1, 0), "")
		}
		lines[i] = strings.TrimRight(line, " ")
	}
	return lines
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
	// No pane geometry: sparse shell scrollback still trims trailing blanks so
	// follow shows content rather than a sea of empty rows.
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

	// The scrollbar column pads the block out to the viewport, so assert on the
	// pane text: one content row followed by blank rows.
	lines := terminalTextLines(result)
	if lines[0] != "prompt" {
		t.Fatalf("content = %q, want prompt", lines[0])
	}
	for i, line := range lines[1:] {
		if line != "" {
			t.Fatalf("row %d = %q, want blank padding", i+1, line)
		}
	}
	if input != before {
		t.Fatal("renderTerminalViewport mutated its input")
	}
	if result.Layout.EffectiveCount != 1 {
		t.Fatalf("effective count = %d, want 1", result.Layout.EffectiveCount)
	}
}

// Passive follow of a known live grid must not let TrimTrailing walk Start up
// into history. That off-by-one painted the previous bottom chrome under the
// header until interactive mode (which never trims) re-aligned it.
func TestPassiveFollowKeepsLiveGridDespiteTrailingBlanks(t *testing.T) {
	buffer := tty.NewOutputBuffer(100)
	// One history row that looks like "bottom chrome", then a 4-row pane whose
	// final row is blank — the shape full-screen TUIs leave at the prompt.
	buffer.ApplySnapshot(tty.PaneSnapshot{
		Output:      "old-bottom-chrome\nrow0\nrow1\nrow2\n",
		HistoryRows: 1,
		PaneRows:    4,
	})

	result := renderTerminalViewport(terminalViewportInput{
		Buffer:       buffer,
		Width:        40,
		Height:       4,
		Follow:       true,
		TrimTrailing: true, // passive auto-scroll sets this
		PaneHeight:   4,
		PaneWidth:    40,
	}, ui.NewTruncateCache(32))

	if result.Layout.Start != 1 {
		t.Fatalf("Start = %d, want PaneTop 1", result.Layout.Start)
	}
	if result.Layout.PaneTop != 1 {
		t.Fatalf("PaneTop = %d, want 1", result.Layout.PaneTop)
	}
	// EffectiveCount stays the full buffer while following the live grid so
	// blank final rows remain addressable.
	if result.Layout.EffectiveCount != 5 {
		t.Fatalf("EffectiveCount = %d, want 5 (history + full pane)", result.Layout.EffectiveCount)
	}

	lines := terminalTextLines(result)
	if len(lines) != 4 {
		t.Fatalf("rendered %d lines, want 4: %#v", len(lines), lines)
	}
	if lines[0] != "row0" {
		t.Fatalf("first visible = %q, want row0 (history must not leak under the header)", lines[0])
	}
	if lines[1] != "row1" || lines[2] != "row2" {
		t.Fatalf("pane body = %#v, want row1/row2", lines[1:3])
	}
	if lines[3] != "" {
		t.Fatalf("final pane row = %q, want blank (TUI bottom spacing)", lines[3])
	}
}

// Without pane geometry, scrolled-back sparse output still trims so MaxOffset
// does not include a trailing blank sea.
func TestScrolledBackSparseOutputStillTrimsTrailing(t *testing.T) {
	buffer := testTerminalBuffer("a\nb\nc\n\n\n")
	layout := calculateTerminalViewportLayout(terminalViewportInput{
		Buffer:       buffer,
		Width:        40,
		Height:       2,
		Offset:       0,
		TrimTrailing: true,
		// Follow false, no PaneHeight: pure scrollback browse.
	})
	if layout.EffectiveCount != 3 {
		t.Fatalf("EffectiveCount = %d, want 3 after trim", layout.EffectiveCount)
	}
	if layout.MaxOffset != 1 {
		t.Fatalf("MaxOffset = %d, want 1", layout.MaxOffset)
	}
}

// A capture that lost trailing blanks (tmux strips them) still fills the
// viewport when following a known pane height, so chrome does not jump.
func TestPassiveFollowPadsShortLiveGridToViewport(t *testing.T) {
	buffer := tty.NewOutputBuffer(100)
	buffer.ApplySnapshot(tty.PaneSnapshot{
		Output:      "row0\nrow1\nrow2",
		HistoryRows: 0,
		PaneRows:    3,
	})

	result := renderTerminalViewport(terminalViewportInput{
		Buffer:     buffer,
		Width:      20,
		Height:     4,
		Follow:     true,
		PaneHeight: 4,
		PaneWidth:  20,
	}, ui.NewTruncateCache(32))

	lines := terminalTextLines(result)
	if len(lines) != 4 {
		t.Fatalf("rendered %d lines, want 4 padded: %#v", len(lines), lines)
	}
	if lines[0] != "row0" || lines[3] != "" {
		t.Fatalf("padded grid = %#v, want row0..blank", lines)
	}
}

// Interactive follow of the same full-screen grid must agree with passive on
// the window start so entering interactive mode cannot "jump" chrome.
func TestPassiveAndInteractiveFollowShareLiveGridWindow(t *testing.T) {
	buffer := tty.NewOutputBuffer(100)
	buffer.ApplySnapshot(tty.PaneSnapshot{
		Output:      "hist\na\nb\nc\n",
		HistoryRows: 1,
		PaneRows:    4,
	})
	passive := calculateTerminalViewportLayout(terminalViewportInput{
		Buffer: buffer, Width: 40, Height: 4, Follow: true,
		TrimTrailing: true, PaneHeight: 4, PaneWidth: 40,
	})
	interactive := calculateTerminalViewportLayout(terminalViewportInput{
		Buffer: buffer, Width: 40, Height: 4, Follow: true,
		Interactive: true, PaneHeight: 4, PaneWidth: 40,
	})
	if passive.Start != interactive.Start || passive.End != interactive.End {
		t.Fatalf("passive window [%d,%d) != interactive [%d,%d)",
			passive.Start, passive.End, interactive.Start, interactive.End)
	}
	if passive.Start != 1 {
		t.Fatalf("shared Start = %d, want PaneTop 1", passive.Start)
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
		Active:       true,
		VisibleStart: 91,
		VisibleEnd:   99,
		PaneHeight:   3,
		PaneWidth:    20,
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
			if poll.WorkspaceName != "work" || poll.Generation != 1 {
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

			_, gotTermHeight, _ := p.calculateTermPanelDimensions()
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

// td-26bdb2: the scrollbar used to be joined to an unpadded block, so
// lipgloss.JoinHorizontal aligned it to the widest line — landing it right after
// the shell prompt and sliding rightwards as the user typed.
func TestRenderTerminalViewportPinsScrollbarToRightEdge(t *testing.T) {
	// Short lines, more of them than fit, so a scrollbar is shown.
	buffer := testTerminalBuffer("prompt>\na\nb\nc\nd\ne\nf\ng")

	result := renderTerminalViewport(terminalViewportInput{
		Buffer:     buffer,
		Width:      40,
		Height:     4,
		Follow:     true,
		TotalItems: 8,
	}, ui.NewTruncateCache(32))

	if !result.Layout.ShowScrollbar {
		t.Fatalf("expected a scrollbar for %d lines in %d rows", buffer.LineCount(), 4)
	}

	for i, line := range strings.Split(result.Content, "\n") {
		if got := ansi.StringWidth(line); got != 40 {
			t.Errorf("line %d: width = %d, want 40 (scrollbar not at the right edge): %q",
				i, got, ansi.Strip(line))
		}
	}
}

func TestTerminalContentWidthReservesStableScrollbarColumn(t *testing.T) {
	buffer := testTerminalBuffer("123456789\nabcdefghi\nABCDEFGHI")
	p := &Plugin{
		shellSelected: true,
		shells:        []*ShellSession{{Agent: &Agent{OutputBuf: buffer}}},
	}

	for _, height := range []int{2, 3} {
		if got := p.terminalContentWidth(10); got != 9 {
			t.Fatalf("content width at height %d = %d, want stable width 9", height, got)
		}
	}

	// Once tmux has wrapped to the reserved width, the last pane cell remains
	// visible immediately beside the scrollbar instead of being truncated.
	result := renderTerminalViewport(terminalViewportInput{
		Buffer:     buffer,
		Width:      10,
		Height:     2,
		Follow:     true,
		PaneWidth:  9,
		PaneHeight: 2,
		TotalItems: 3,
	}, ui.NewTruncateCache(32))
	for i, line := range strings.Split(ansi.Strip(result.Content), "\n") {
		if !strings.HasPrefix(line, []string{"abcdefghi", "ABCDEFGHI"}[i]) {
			t.Fatalf("line %d lost its rightmost pane cell: %q", i, line)
		}
	}
}

func TestRenderTerminalViewportKeepsScrollbarColumnAcrossHistoryThreshold(t *testing.T) {
	cache := ui.NewTruncateCache(32)
	for _, total := range []int{2, 3} {
		buffer := testTerminalBuffer("123456789\nabcdefghi")
		result := renderTerminalViewport(terminalViewportInput{
			Buffer: buffer, Width: 10, Height: 2, Follow: true,
			PaneWidth: 9, PaneHeight: 2, TotalItems: total,
		}, cache)
		if !result.Layout.ShowScrollbar || result.Layout.DisplayWidth != 9 {
			t.Fatalf("total %d: scrollbar=%v display width=%d, want reserved column and width 9",
				total, result.Layout.ShowScrollbar, result.Layout.DisplayWidth)
		}
		for i, line := range strings.Split(ansi.Strip(result.Content), "\n") {
			if got := ansi.StringWidth(line); got != 10 {
				t.Fatalf("total %d line %d width = %d, want 10: %q", total, i, got, line)
			}
		}
	}
}

func TestPadLinesToWidth(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		width int
		want  []int
	}{
		{name: "pads short lines", lines: []string{"ab", "abcd"}, width: 6, want: []int{6, 6}},
		{name: "leaves long lines alone", lines: []string{"abcdefgh"}, width: 4, want: []int{8}},
		{name: "ignores ansi when measuring", lines: []string{"\x1b[31mab\x1b[0m"}, width: 5, want: []int{5}},
		{name: "non-positive width is a no-op", lines: []string{"ab"}, width: 0, want: []int{2}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := padLinesToWidth(append([]string(nil), tt.lines...), tt.width)
			for i, want := range tt.want {
				if w := ansi.StringWidth(got[i]); w != want {
					t.Errorf("line %d width = %d, want %d", i, w, want)
				}
			}
		})
	}
}

// td-d29821: a capture is scrollback + pane rows, so the pane's cursor row is
// not a display row. Placing it as if it were floats the cursor above the live
// line by however much scrollback the capture carried.
func TestTerminalViewportCursorAccountsForScrollback(t *testing.T) {
	// One scrollback line, then the pane's three rows. The cursor sits on pane
	// row 2 (the live prompt).
	buffer := testTerminalBuffer("scrollback\ncmd\noutput\nprompt>")

	in := terminalViewportInput{
		Buffer:        buffer,
		Width:         80,
		Height:        44,
		Follow:        true,
		Interactive:   true,
		CursorRow:     2,
		CursorCol:     8,
		CursorVisible: true,
		PaneHeight:    3,
		PaneWidth:     80,
	}

	_, y, ok := terminalViewportCursorPosition(in)
	if !ok {
		t.Fatal("expected the cursor to be visible")
	}
	// The window shows the pane, not the scrollback line above it, so the
	// prompt is the third rendered row.
	if got := renderedTerminalLine(t, in, y); got != "prompt>" {
		t.Errorf("cursor row %d renders %q, want the pane's cursor row %q", y, got, "prompt>")
	}
}

// A full-screen program fills the pane, so the buffer's tail *is* the pane and
// every pane row maps to the display row of the same index. This is the case
// that already worked and must not regress.
func TestTerminalViewportCursorFullScreenPaneUnchanged(t *testing.T) {
	const paneHeight = 10
	const historySize = 25

	lines := make([]string, 0, historySize+paneHeight)
	for i := range historySize + paneHeight {
		lines = append(lines, fmt.Sprintf("line-%02d", i))
	}
	buffer := testTerminalBuffer(strings.Join(lines, "\n"))

	for _, cursorRow := range []int{0, 4, paneHeight - 1} {
		in := terminalViewportInput{
			Buffer:        buffer,
			Width:         80,
			Height:        paneHeight,
			Follow:        true,
			Interactive:   true,
			CursorRow:     cursorRow,
			CursorVisible: true,
			PaneHeight:    paneHeight,
			PaneWidth:     80,
		}

		_, y, ok := terminalViewportCursorPosition(in)
		if !ok {
			t.Fatalf("cursorRow %d: expected the cursor to be visible", cursorRow)
		}
		if y != cursorRow {
			t.Errorf("cursorRow %d: cursor y = %d, want %d", cursorRow, y, cursorRow)
		}
		want := fmt.Sprintf("line-%02d", historySize+cursorRow)
		if got := renderedTerminalLine(t, in, y); got != want {
			t.Errorf("cursorRow %d renders %q, want %q", cursorRow, got, want)
		}
	}
}

// Once tmux history exceeds the capture window the buffer no longer starts at
// absolute line 0. Placement is geometric, so the absolute base — which search
// and selection still need — must not move the cursor.
func TestTerminalViewportCursorIgnoresAbsoluteBase(t *testing.T) {
	relative := testTerminalBuffer("scrollback\ncmd\noutput\nprompt>")
	absolute := tty.NewOutputBuffer(100)
	absolute.UpdateSnapshot("scrollback\ncmd\noutput\nprompt>", 600)

	in := terminalViewportInput{
		Width:         80,
		Height:        44,
		Follow:        true,
		Interactive:   true,
		CursorRow:     2,
		CursorVisible: true,
		PaneHeight:    3,
		PaneWidth:     80,
	}

	in.Buffer = relative
	_, want, ok := terminalViewportCursorPosition(in)
	if !ok {
		t.Fatal("expected the cursor to be visible on the relative buffer")
	}
	in.Buffer, in.AbsoluteBase = absolute, 600
	_, got, ok := terminalViewportCursorPosition(in)
	if !ok {
		t.Fatal("expected the cursor to be visible on the absolute buffer")
	}
	if got != want {
		t.Errorf("cursor row = %d on an absolute buffer, want %d", got, want)
	}
}

// The user-visible defect (td-d29821): the capture path issues its
// display-message and its capture-pane as two separate writes, so lines can
// scroll into history between them and the buffer then holds more history rows
// than the metadata knew about. Placing the cursor from history_size drew it
// that many rows too high, and it stayed there until the pane was re-seeded.
//
// Both buffer shapes are exercised. A buffer whose producer stated its split
// reads it back; one that never received a split falls back to its tail. Neither
// consults history_size, so neither can drift with it.
func TestTerminalViewportCursorSurvivesHistoryDrift(t *testing.T) {
	const paneHeight = 6
	const staleHistorySize = 12
	const cursorRow = 4

	for _, historyRows := range []int{staleHistorySize, staleHistorySize + 2, staleHistorySize - 2} {
		lines := make([]string, 0, historyRows+paneHeight)
		for i := range historyRows {
			lines = append(lines, fmt.Sprintf("history-%02d", i))
		}
		for i := range paneHeight {
			lines = append(lines, fmt.Sprintf("screen-%02d", i))
		}
		content := strings.Join(lines, "\n")

		stated := tty.NewOutputBuffer(100)
		// The absolute base is deliberately derived from the stale history_size,
		// as the capture path's is: the split, not the base, is what places the
		// cursor.
		stated.ApplySnapshot(tty.PaneSnapshot{
			Output: content, BaseLine: staleHistorySize - historyRows, Absolute: true,
			HistoryRows: historyRows, PaneRows: paneHeight,
		})

		for _, buffer := range []struct {
			name string
			buf  *tty.OutputBuffer
		}{
			{"split stated by the producer", stated},
			{"split never stated", testTerminalBuffer(content)},
		} {
			in := terminalViewportInput{
				Buffer:        buffer.buf,
				Width:         80,
				Height:        paneHeight,
				Follow:        true,
				Interactive:   true,
				CursorRow:     cursorRow,
				CursorVisible: true,
				PaneHeight:    paneHeight,
				PaneWidth:     80,
			}
			_, y, ok := terminalViewportCursorPosition(in)
			if !ok {
				t.Fatalf("%s, historyRows %d: expected the cursor to be visible", buffer.name, historyRows)
			}
			want := fmt.Sprintf("screen-%02d", cursorRow)
			if got := renderedTerminalLine(t, in, y); got != want {
				t.Errorf("%s, historyRows %d (metadata said %d): cursor row %d renders %q, want %q",
					buffer.name, historyRows, staleHistorySize, y, got, want)
			}
		}
	}
}

// The regression the geometry inference introduced (td-d29821): a rendering
// whose final pane rows are blank can reach the buffer one or more rows shorter
// than the pane — a frame's row-separated grid loses its blank last row to the
// terminator rule, and a plain capture-pane never emits those rows at all.
// "The pane is the buffer's last PaneHeight rows" then names the wrong line and
// the cursor is drawn above the line it belongs on. The split the producer
// states does not move with the content's tail.
func TestTerminalViewportCursorIgnoresAShortBufferTail(t *testing.T) {
	const paneHeight = 6
	const historyRows = 9
	const deliveredPaneRows = 4
	const cursorRow = 1

	lines := make([]string, 0, historyRows+deliveredPaneRows)
	for i := range historyRows {
		lines = append(lines, fmt.Sprintf("history-%02d", i))
	}
	for i := range deliveredPaneRows {
		lines = append(lines, fmt.Sprintf("screen-%02d", i))
	}
	buffer := tty.NewOutputBuffer(100)
	buffer.ApplySnapshot(tty.PaneSnapshot{
		Output: strings.Join(lines, "\n"), Absolute: true,
		HistoryRows: historyRows, PaneRows: paneHeight,
	})

	in := terminalViewportInput{
		Buffer:        buffer,
		Width:         80,
		Height:        paneHeight,
		Follow:        true,
		Interactive:   true,
		CursorRow:     cursorRow,
		CursorVisible: true,
		PaneHeight:    paneHeight,
		PaneWidth:     80,
	}
	_, y, ok := terminalViewportCursorPosition(in)
	if !ok {
		t.Fatal("expected the cursor to be visible")
	}
	want := fmt.Sprintf("screen-%02d", cursorRow)
	if got := renderedTerminalLine(t, in, y); got != want {
		t.Errorf("cursor row %d renders %q, want %q", y, got, want)
	}
}

// renderedTerminalLine returns the plain text the viewport draws on row y, so a
// cursor assertion can name the line the cursor must land on rather than a bare
// index that says nothing about what the user sees.
func renderedTerminalLine(t *testing.T, in terminalViewportInput, y int) string {
	t.Helper()
	// Render with the native cursor so the content is the pane's own text; the
	// painted-cursor path would overwrite the very cell under assertion.
	in.NativeCursor = true
	result := renderTerminalViewport(in, ui.NewTruncateCache(64))
	lines := terminalTextLines(result)
	if y < 0 || y >= len(lines) {
		t.Fatalf("cursor row %d outside the %d rendered rows", y, len(lines))
	}
	return lines[y]
}

// paneSplitBuffer builds a buffer whose producer stated the split: history
// rows first, then paneRows live grid rows.
func paneSplitBuffer(historyRows, paneRows int) *tty.OutputBuffer {
	buffer := tty.NewOutputBuffer(100)
	lines := make([]string, 0, historyRows+paneRows)
	for i := 0; i < historyRows; i++ {
		lines = append(lines, fmt.Sprintf("hist%d", i))
	}
	for i := 0; i < paneRows; i++ {
		lines = append(lines, fmt.Sprintf("pane%d", i))
	}
	buffer.ApplySnapshot(tty.PaneSnapshot{
		Output:      strings.Join(lines, "\n"),
		HistoryRows: historyRows,
		PaneRows:    paneRows,
	})
	return buffer
}

// A pane taller than the viewport is clipped, and following then anchors the
// window on the cursor row rather than the pane's tail (td-73fa86). That holds
// whether or not the cursor is *drawn*: a full-screen app that hides it still
// has a live row, and anchoring on the tail would show the padding below it.
func TestClippedFollowAnchorsOnCursorRowEvenWhenCursorHidden(t *testing.T) {
	for _, visible := range []bool{true, false} {
		buffer := paneSplitBuffer(4, 6)
		layout := calculateTerminalViewportLayout(terminalViewportInput{
			Buffer: buffer, Width: 20, Height: 3, Follow: true,
			Interactive: true, CursorVisible: visible, CursorRow: 1,
			PaneWidth: 20, PaneHeight: 6,
		})
		if layout.PaneTop != 4 {
			t.Fatalf("cursorVisible=%v: PaneTop = %d, want 4", visible, layout.PaneTop)
		}
		// Cursor line 5, three visible rows: the window starts at 3, not at the
		// tail (7).
		if layout.Start != 3 {
			t.Fatalf("cursorVisible=%v: Start = %d, want 3 (cursor-anchored, not tail 7)",
				visible, layout.Start)
		}
	}
}

// The cursor is hidden, not clamped, when it falls outside the rendered window:
// drawing it on the nearest row would put it on a line it is not on.
func TestTerminalViewportCursorHiddenOutsideWindow(t *testing.T) {
	// Off the top: the split is known but no pane geometry has been observed, so
	// the window follows the buffer's tail and pane row 0 is above it.
	above := terminalViewportInput{
		Buffer: paneSplitBuffer(5, 15), Width: 20, Height: 3, Follow: true,
		Interactive: true, CursorVisible: true, CursorRow: 0, CursorCol: 2,
	}
	if x, y, ok := terminalViewportCursorPosition(above); ok {
		t.Fatalf("cursor above the window = (%d,%d,true), want hidden", x, y)
	}

	// Past the bottom: a stale cursor row beyond the pane's last row.
	below := terminalViewportInput{
		Buffer: paneSplitBuffer(8, 2), Width: 20, Height: 5, Follow: true,
		Interactive: true, CursorVisible: true, CursorRow: 4, CursorCol: 2,
		PaneWidth: 20, PaneHeight: 2,
	}
	if x, y, ok := terminalViewportCursorPosition(below); ok {
		t.Fatalf("cursor below the window = (%d,%d,true), want hidden", x, y)
	}
}

// Every row of a multi-line selection must carry the highlight, including the
// middle rows an app has painted with its own background (grok).
func TestTerminalViewportHighlightsEveryRowOfAStyledSelection(t *testing.T) {
	rowBg := "\x1b[48;2;30;30;40m"
	buffer := testTerminalBuffer(
		rowBg + "first row\n" +
			rowBg + "second row\n" +
			rowBg + "third row\n" +
			rowBg + "fourth row\n")
	selection := &ui.SelectionState{}
	selection.Clear()
	selection.SelectRange(
		ui.SelectionPoint{Line: 0, Col: 2},
		ui.SelectionPoint{Line: 2, Col: 4},
		false,
	)

	result := renderTerminalViewport(terminalViewportInput{
		Buffer:    buffer,
		Width:     40,
		Height:    6,
		Follow:    true,
		Selection: selection,
	}, ui.NewTruncateCache(32))

	selBg := ui.GetSelectionBgANSI()
	rendered := strings.Split(result.Content, "\n")
	if len(rendered) < 4 {
		t.Fatalf("rendered %d rows, want at least 4", len(rendered))
	}
	for i, line := range rendered[:3] {
		highlight := strings.Index(line, selBg)
		if highlight < 0 {
			t.Errorf("selected row %d carries no highlight: %q", i, line)
			continue
		}
		// The row paints its own background first; a highlight emitted before it
		// is painted straight over — the middle-row symptom.
		if own := strings.Index(line, rowBg); own >= 0 && own > highlight {
			t.Errorf("selected row %d applies its own background over the highlight: %q", i, line)
		}
	}
	if strings.Contains(rendered[3], selBg) {
		t.Errorf("row past the selection was highlighted: %q", rendered[3])
	}
}

func TestTerminalViewportUsesFullscreenCanvasForDefaultCells(t *testing.T) {
	canvas := "\x1b[48;2;20;20;20m"
	panel := "\x1b[48;2;36;36;36m"
	buffer := tty.NewOutputBuffer(100)
	buffer.ApplySnapshot(tty.PaneSnapshot{
		Output:      canvas + "Output  Diff  Task   INTERACTIVE\x1b[0m\n" + canvas + "   \x1b[0m\n" + panel + "panel\x1b[49m default\n" + canvas + "status\x1b[0m",
		HistoryRows: 0,
		PaneRows:    4,
	})

	result := renderTerminalViewport(terminalViewportInput{
		Buffer: buffer, Width: 30, Height: 4, Follow: true,
		Interactive: true, PaneWidth: 30, PaneHeight: 4, AltScreen: true,
	}, ui.NewTruncateCache(32))
	rows := strings.Split(result.Content, "\n")
	if len(rows) != 4 {
		t.Fatalf("rendered %d rows, want 4", len(rows))
	}
	for i, row := range rows {
		if !strings.HasPrefix(row, canvas) {
			t.Errorf("row %d does not establish canvas background: %q", i, row)
		}
	}
	if !strings.Contains(rows[2], panel+"panel\x1b[49m"+canvas+" default") {
		t.Errorf("explicit panel/default transition = %q", rows[2])
	}
}

func TestTerminalViewportDoesNotInferCanvasFromIsolatedColoredRow(t *testing.T) {
	rowBg := "\x1b[48;2;20;20;20m"
	buffer := tty.NewOutputBuffer(10)
	buffer.ApplySnapshot(tty.PaneSnapshot{
		Output: "prompt\n" + rowBg + "   \x1b[0m\nplain\nmore", PaneRows: 4,
	})
	result := renderTerminalViewport(terminalViewportInput{
		Buffer: buffer, Width: 20, Height: 4, Follow: true,
		Interactive: true, PaneWidth: 20, PaneHeight: 4,
	}, ui.NewTruncateCache(16))
	rows := strings.Split(result.Content, "\n")
	if strings.HasPrefix(rows[0], rowBg) || strings.HasPrefix(rows[2], rowBg) {
		t.Fatalf("isolated coloured row became a canvas: %q", rows)
	}
}

// A diff in ordinary scrollback puts an added-line background on most visible
// rows. That is highlighting, not a canvas: promoting it repainted the whole
// pane — prose, blank rows and all — in the diff's green.
func TestTerminalViewportDoesNotTreatScrollbackDiffAsCanvas(t *testing.T) {
	green := "\x1b[48;2;0;80;0m"
	var rows []string
	for range 8 {
		rows = append(rows, green+"+ added line\x1b[49m")
	}
	rows = append(rows, "Ran 3 shell commands", "Found and fixed it.")
	buffer := tty.NewOutputBuffer(100)
	buffer.ApplySnapshot(tty.PaneSnapshot{Output: strings.Join(rows, "\n"), PaneRows: len(rows)})

	result := renderTerminalViewport(terminalViewportInput{
		Buffer: buffer, Width: 40, Height: len(rows), Follow: true,
		Interactive: true, PaneWidth: 40, PaneHeight: len(rows), AltScreen: false,
	}, ui.NewTruncateCache(32))

	rendered := strings.Split(result.Content, "\n")
	for _, row := range rendered[8:] {
		if strings.Contains(row, green) {
			t.Errorf("prose row inherited the diff background: %q", row)
		}
	}
}

// tmux writes only the SGR delta, so a background opened on one row stays open
// on every row after it. Rows are rendered independently — sliced, truncated,
// padded — so each has to close what it opened or the colour smears.
func TestTerminalViewportClosesBackgroundAtEndOfRow(t *testing.T) {
	green := "\x1b[48;2;0;80;0m"
	buffer := tty.NewOutputBuffer(100)
	// Exactly what `capture-pane -e` delivers for a background erased to end of
	// line: the trailing filled cells are trimmed and the reset goes with them.
	buffer.ApplySnapshot(tty.PaneSnapshot{
		Output:   green + "+ added",
		PaneRows: 1,
	})

	result := renderTerminalViewport(terminalViewportInput{
		Buffer: buffer, Width: 20, Height: 1, Follow: true,
		Interactive: true, PaneWidth: 20, PaneHeight: 1,
	}, ui.NewTruncateCache(32))

	row := strings.Split(result.Content, "\n")[0]
	// The reset closes the text, so the padding that follows is default-background
	// rather than a green bar running to the pane edge.
	if !strings.Contains(row, "+ added"+ui.RowBackgroundDefault) {
		t.Errorf("row does not close its background: %q", row)
	}
	if tail := row[strings.LastIndex(row, ui.RowBackgroundDefault):]; strings.Contains(tail, green) {
		t.Errorf("row re-opens the background after closing it: %q", row)
	}
}

// The canvas is emitted once and then carried by the pen, so a fullscreen TUI's
// later rows contain no background sequence of their own. Detection has to
// resolve that inheritance or only the first row would vote.
func TestTerminalViewportDetectsCanvasCarriedAcrossRows(t *testing.T) {
	canvas := "\x1b[48;2;20;20;20m"
	buffer := tty.NewOutputBuffer(100)
	buffer.ApplySnapshot(tty.PaneSnapshot{
		Output:   canvas + "header\nbody\nfooter\nstatus",
		PaneRows: 4,
	})

	result := renderTerminalViewport(terminalViewportInput{
		Buffer: buffer, Width: 20, Height: 4, Follow: true,
		Interactive: true, PaneWidth: 20, PaneHeight: 4, AltScreen: true,
	}, ui.NewTruncateCache(32))

	for i, row := range strings.Split(result.Content, "\n") {
		if !strings.HasPrefix(row, canvas) {
			t.Errorf("row %d lost the carried canvas: %q", i, row)
		}
	}
}
