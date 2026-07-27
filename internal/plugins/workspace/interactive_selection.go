package workspace

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
	"github.com/charmbracelet/x/ansi"
	app "github.com/marcus/sidecar/internal/app"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

// interactiveColAtX maps a viewport X coordinate to a visual column in the given line.
// The returned column is in visual space (post-tab-expansion, accounting for multi-width chars).
func (p *Plugin) interactiveColAtX(x, lineIdx int) (int, bool) {
	contentInset := panelOverhead / 2
	if p.interactiveState != nil && p.interactiveState.TermPanel {
		contentInset = 0
	}
	relX := x - p.selection.ViewRect.X - contentInset
	if relX < 0 {
		return 0, false
	}

	buf := p.interactiveOutputBuffer()
	if buf == nil {
		return 0, true
	}
	if lineIdx < 0 || lineIdx >= buf.LineCount() {
		return 0, true
	}

	lines := buf.LinesRange(lineIdx, lineIdx+1)
	if len(lines) == 0 {
		return 0, true
	}
	expanded := ui.ExpandTabs(lines[0], tabStopWidth)

	return ui.VisualColAtRelativeX(expanded, relX), true
}

// interactiveCharAtXY maps viewport coordinates to line index + visual column.
func (p *Plugin) interactiveCharAtXY(x, y int) (int, int, bool) {
	lineIdx, ok := p.interactiveLineIndexAtY(y)
	if !ok {
		return 0, 0, false
	}
	col, ok := p.interactiveColAtX(x, lineIdx)
	return lineIdx, col, ok
}

func (p *Plugin) interactiveLineIndexAtY(y int) (int, bool) {
	if p.interactiveState == nil || !p.interactiveState.Active {
		return 0, false
	}
	if p.selection.ViewRect.W == 0 || p.selection.ViewRect.H == 0 {
		return 0, false
	}
	layout := p.interactiveViewportLayout()
	if layout.End <= layout.Start {
		// Compatibility for callers that construct only the old cached state.
		layout.Start = p.interactiveState.VisibleStart
		layout.End = p.interactiveState.VisibleEnd
	}
	if layout.End <= layout.Start {
		return 0, false
	}

	contentRow := y - p.selection.ViewRect.Y
	contentRowOffset := 1 // terminal hint line
	if !p.interactiveState.TermPanel {
		contentRow-- // preview panel top border
		if !p.shellSelected {
			contentRowOffset += 2 // worktree tabs and spacer
		}
		if !p.flashPreviewTime.IsZero() && time.Since(p.flashPreviewTime) < flashDuration {
			contentRowOffset++
		}
	}
	if contentRow < 0 {
		return 0, false
	}
	if layout.EffectiveCount == 0 && p.interactiveState.ContentRowOffset > 0 {
		contentRowOffset = p.interactiveState.ContentRowOffset
	}
	outputRow := contentRow - contentRowOffset
	if outputRow < 0 {
		return 0, false
	}
	lineIdx := layout.Start + outputRow
	if lineIdx < layout.Start || lineIdx >= layout.End {
		return 0, false
	}
	return lineIdx, true
}

// prepareInteractiveDrag stores the click position and starts drag tracking
// without initializing selection. Selection only activates on actual drag motion.
func (p *Plugin) prepareInteractiveDrag(action mouse.MouseAction) tea.Cmd {
	if action.Region == nil {
		return nil
	}
	// Set ViewRect before charAtXY so interactiveLineIndexAtY can use it
	p.selection.ViewRect = action.Region.Rect

	lineIdx, col, ok := p.interactiveCharAtXY(action.X, action.Y)
	if !ok {
		p.selection.Clear()
		return nil
	}

	p.selection.PrepareDrag(lineIdx, col, action.Region.Rect)
	if p.interactiveState != nil && p.interactiveState.TermPanel {
		// Stop following the live panel while the user establishes a local
		// selection. Do not disturb the independent agent/shell follow state.
		p.termPanelScroll = max(p.termPanelScroll, 1)
	} else {
		p.autoScrollOutput = false
	}

	p.mouseHandler.StartDrag(action.X, action.Y, regionPreviewPane, lineIdx)
	return nil
}

func (p *Plugin) handleInteractiveSelectionDrag(action mouse.MouseAction) tea.Cmd {
	lineIdx, col, ok := p.interactiveCharAtXY(action.X, action.Y)
	if !ok {
		return nil
	}

	p.selection.HandleDrag(lineIdx, col)
	return nil
}

func (p *Plugin) finishInteractiveSelection() tea.Cmd {
	p.selection.FinishDrag()
	return nil
}

func (p *Plugin) interactiveOutputBuffer() *tty.OutputBuffer {
	if p.interactiveState != nil && p.interactiveState.TermPanel {
		return p.termPanelOutput
	}
	if p.shellSelected {
		shell := p.getSelectedShell()
		if shell != nil && shell.Agent != nil {
			return shell.Agent.OutputBuf
		}
		return nil
	}
	wt := p.selectedWorktree()
	if wt == nil || wt.Agent == nil {
		return nil
	}
	return wt.Agent.OutputBuf
}

func (p *Plugin) interactiveSelectionLines() []string {
	if !p.selection.HasSelection() {
		return nil
	}
	buf := p.interactiveOutputBuffer()
	if buf == nil {
		return nil
	}

	lineCount := buf.LineCount()
	if lineCount == 0 {
		return nil
	}

	startLine := p.selection.Start.Line
	endLine := p.selection.End.Line
	if startLine > endLine {
		startLine, endLine = endLine, startLine
	}
	if startLine < 0 {
		startLine = 0
	}
	if endLine >= lineCount {
		endLine = lineCount - 1
	}
	if endLine < startLine {
		return nil
	}

	lines := buf.LinesRange(startLine, endLine+1)
	if len(lines) == 0 {
		return nil
	}

	return p.selection.SelectedText(lines, startLine, tabStopWidth)
}

func (p *Plugin) interactiveVisibleLines() []string {
	if p.interactiveState == nil || !p.interactiveState.Active {
		return nil
	}
	buf := p.interactiveOutputBuffer()
	if buf == nil {
		return nil
	}
	layout := p.interactiveViewportLayout()
	start := layout.Start
	end := layout.End
	if end <= start {
		start = p.interactiveState.VisibleStart
		end = p.interactiveState.VisibleEnd
	}
	if end <= start {
		return nil
	}
	return buf.LinesRange(start, end)
}

func (p *Plugin) interactiveViewportLayout() terminalViewportLayout {
	if p.interactiveState == nil || !p.interactiveState.Active {
		return terminalViewportLayout{}
	}
	buffer := p.interactiveOutputBuffer()
	if buffer == nil {
		return terminalViewportLayout{}
	}

	width, height := p.calculatePreviewDimensions()
	termPanel := p.interactiveState.TermPanel
	if termPanel && p.termPanelVisible {
		width, height = p.calculateTermPanelDimensions()
	} else if p.termPanelVisible {
		width, height = p.calculateAgentPaneDimensions()
	}

	input := terminalViewportInput{
		Buffer:      buffer,
		Width:       width,
		Height:      height,
		Follow:      p.autoScrollOutput,
		Offset:      p.previewOffset,
		Interactive: true,
		PaneHeight:  p.interactiveState.PaneHeight,
		PaneWidth:   p.interactiveState.PaneWidth,
	}
	if termPanel {
		input.Follow = p.termPanelScroll == 0
		input.Offset = p.termPanelScroll
		input.OffsetFromBottom = true
	}
	return calculateTerminalViewportLayout(input)
}

func (p *Plugin) copyInteractiveSelectionCmd() tea.Cmd {
	return func() tea.Msg {
		lines := p.interactiveSelectionLines()
		if len(lines) == 0 {
			lines = p.interactiveVisibleLines()
		}
		if len(lines) == 0 {
			return app.ToastMsg{Message: "No output to copy", Duration: 2 * time.Second}
		}

		stripped := make([]string, 0, len(lines))
		for _, line := range lines {
			stripped = append(stripped, ansi.Strip(line))
		}
		text := strings.Join(stripped, "\n")
		if err := clipboard.WriteAll(text); err != nil {
			return app.ToastMsg{Message: "Copy failed: " + err.Error(), Duration: 2 * time.Second, IsError: true}
		}

		return app.ToastMsg{Message: fmt.Sprintf("Copied %d line(s)", len(stripped)), Duration: 2 * time.Second}
	}
}
