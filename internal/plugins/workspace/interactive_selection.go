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
	if p.effectiveSelectionTermPanel() {
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
	localLine := lineIdx
	if base, end, absolute := buf.AbsoluteRange(); absolute {
		if lineIdx < base || lineIdx >= end {
			return 0, true
		}
		localLine = lineIdx - base
	} else if lineIdx < 0 || lineIdx >= buf.LineCount() {
		return 0, true
	}

	lines := buf.LinesRange(localLine, localLine+1)
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
	if p.selection.ViewRect.W == 0 || p.selection.ViewRect.H == 0 {
		return 0, false
	}
	layout := p.terminalSelectionViewportLayout()
	if layout.End <= layout.Start && p.interactiveState != nil {
		// Compatibility for callers that construct only the old cached state.
		layout.Start = p.interactiveState.VisibleStart
		layout.End = p.interactiveState.VisibleEnd
	}
	if layout.End <= layout.Start {
		return 0, false
	}

	contentRow := y - p.selection.ViewRect.Y
	contentRowOffset := 1 // terminal hint line
	if !p.effectiveSelectionTermPanel() {
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
	if layout.EffectiveCount == 0 && p.interactiveState != nil && p.interactiveState.ContentRowOffset > 0 {
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
	buf := p.interactiveOutputBuffer()
	if buf != nil {
		if base, _, absolute := buf.AbsoluteRange(); absolute {
			lineIdx += base
		}
	}
	return lineIdx, true
}

// prepareInteractiveDrag stores the click position and starts drag tracking
// without initializing selection. Selection only activates on actual drag motion.
func (p *Plugin) prepareInteractiveDrag(action mouse.MouseAction) tea.Cmd {
	if action.Region == nil {
		return nil
	}
	targetTermPanel := action.Region.ID == regionTermPanelContent
	canExtend := action.Shift && p.selection.HasSelection() && p.selectionTermPanel == targetTermPanel
	p.selectionTermPanel = targetTermPanel
	// Set ViewRect before charAtXY so interactiveLineIndexAtY can use it.
	p.selection.ViewRect = action.Region.Rect

	lineIdx, col, ok := p.interactiveCharAtXY(action.X, action.Y)
	if !ok {
		p.selection.Clear()
		return nil
	}

	if p.selectionTermPanel {
		p.termPanelSelectionOffset = p.terminalSelectionViewportLayout().Start
	}
	if canExtend {
		p.selection.ExtendTo(ui.SelectionPoint{Line: lineIdx, Col: col})
		return nil
	}
	// The term panel needs nothing further here: freezing termPanelSelectionOffset
	// above holds it still while selecting, and the agent/shell follow state is
	// independent and must not be disturbed.
	p.selection.PrepareDragMode(lineIdx, col, action.Region.Rect, action.Alt)

	p.mouseHandler.StartDrag(action.X, action.Y, regionPreviewPane, lineIdx)
	return nil
}

func (p *Plugin) handleInteractiveSelectionDrag(action mouse.MouseAction) tea.Cmd {
	lineIdx, col, ok := p.interactiveCharAtXY(action.X, action.Y)
	if !ok {
		return nil
	}

	p.selection.HandleDrag(lineIdx, col)
	if !p.selectionTermPanel {
		p.autoScrollOutput = false
	}
	return nil
}

func (p *Plugin) finishInteractiveSelection() tea.Cmd {
	p.selection.FinishDrag()
	if p.selection.HasSelection() && p.copyOnSelectEnabled() {
		return p.copyInteractiveSelectionCmd()
	}
	return nil
}

func (p *Plugin) interactiveOutputBuffer() *tty.OutputBuffer {
	if p.effectiveSelectionTermPanel() {
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

func (p *Plugin) effectiveSelectionTermPanel() bool {
	if p.viewMode == ViewModeInteractive && p.interactiveState != nil && p.interactiveState.Active {
		return p.interactiveState.TermPanel
	}
	return p.selectionTermPanel
}

func (p *Plugin) interactiveSelectionLines() []string {
	if !p.selection.HasSelection() {
		return nil
	}
	buf := p.interactiveOutputBuffer()
	if buf == nil {
		return nil
	}

	if buf.LineCount() == 0 {
		return nil
	}

	startLine := p.selection.Start.Line
	endLine := p.selection.End.Line
	if startLine > endLine {
		startLine, endLine = endLine, startLine
	}
	var lines []string
	if base, end, absolute := buf.AbsoluteRange(); absolute {
		startLine = max(startLine, base)
		endLine = min(endLine, end-1)
		lines = buf.LinesAbsoluteRange(startLine, endLine+1)
	} else {
		startLine = max(startLine, 0)
		endLine = min(endLine, buf.LineCount()-1)
		lines = buf.LinesRange(startLine, endLine+1)
	}
	if len(lines) == 0 {
		return nil
	}

	return p.selection.SelectedText(lines, startLine, tabStopWidth)
}

func (p *Plugin) interactiveVisibleLines() []string {
	buf := p.interactiveOutputBuffer()
	if buf == nil {
		return nil
	}
	layout := p.terminalSelectionViewportLayout()
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
	return p.terminalSelectionViewportLayout()
}

func (p *Plugin) terminalSelectionViewportLayout() terminalViewportLayout {
	buffer := p.interactiveOutputBuffer()
	if buffer == nil {
		return terminalViewportLayout{}
	}

	width, height := p.calculatePreviewDimensions()
	termPanel := p.selectionTermPanel
	if p.interactiveState != nil && p.interactiveState.Active {
		termPanel = p.interactiveState.TermPanel
	}
	if termPanel && p.termPanelVisible {
		width, height = p.calculateTermPanelDimensions()
	} else if p.termPanelVisible {
		width, height = p.calculateAgentPaneDimensions()
	}

	interactive := p.viewMode == ViewModeInteractive && p.interactiveState != nil && p.interactiveState.Active
	input := terminalViewportInput{
		Buffer:      buffer,
		Width:       width,
		Height:      height,
		Follow:      p.autoScrollOutput,
		Offset:      p.previewOffset,
		Interactive: interactive,
	}
	if p.interactiveState != nil {
		input.PaneHeight = p.interactiveState.PaneHeight
		input.PaneWidth = p.interactiveState.PaneWidth
	}
	if termPanel {
		if p.selection.Anchor.Valid() {
			input.Follow = false
			input.Offset = p.termPanelSelectionOffset
		} else {
			input.Follow = p.termPanelScroll == 0
			input.Offset = p.termPanelScroll
			input.OffsetFromBottom = true
		}
	}
	return calculateTerminalViewportLayout(input)
}

func (p *Plugin) selectTerminalWord(action mouse.MouseAction) tea.Cmd {
	point, line, ok := p.terminalPointAndLine(action)
	if !ok {
		return nil
	}
	plain := ansi.Strip(ui.ExpandTabs(line, tabStopWidth))
	isWord := func(r rune) bool {
		return r == '_' || r == '-' || r == '.' || r == '/' || r == ':' ||
			r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
	}
	type visualToken struct {
		text       string
		start, end int
	}
	var tokens []visualToken
	state := ansi.NormalState
	col := 0
	remaining := plain
	for len(remaining) > 0 {
		seq, width, n, newState := ansi.GraphemeWidth.DecodeSequenceInString(remaining, state, nil)
		if n <= 0 {
			break
		}
		if width > 0 {
			tokens = append(tokens, visualToken{text: seq, start: col, end: col + width})
			col += width
		}
		state = newState
		remaining = remaining[n:]
	}
	if len(tokens) == 0 {
		return nil
	}
	index := len(tokens) - 1
	for i, token := range tokens {
		if point.Col < token.end {
			index = i
			break
		}
	}
	left, right := index, index
	tokenWord := func(token visualToken) bool {
		runes := []rune(token.text)
		return len(runes) > 0 && isWord(runes[0])
	}
	if tokenWord(tokens[index]) {
		for left > 0 && tokenWord(tokens[left-1]) {
			left--
		}
		for right+1 < len(tokens) && tokenWord(tokens[right+1]) {
			right++
		}
	}
	p.selection.SelectRange(
		ui.SelectionPoint{Line: point.Line, Col: tokens[left].start},
		ui.SelectionPoint{Line: point.Line, Col: tokens[right].end - 1},
		false,
	)
	if p.copyOnSelectEnabled() {
		return p.copyInteractiveSelectionCmd()
	}
	return nil
}

func (p *Plugin) selectTerminalLine(action mouse.MouseAction) tea.Cmd {
	point, line, ok := p.terminalPointAndLine(action)
	if !ok {
		return nil
	}
	width := ansi.StringWidth(ui.ExpandTabs(line, tabStopWidth))
	p.selection.SelectRange(
		ui.SelectionPoint{Line: point.Line, Col: 0},
		ui.SelectionPoint{Line: point.Line, Col: max(width-1, 0)},
		false,
	)
	if p.copyOnSelectEnabled() {
		return p.copyInteractiveSelectionCmd()
	}
	return nil
}

func (p *Plugin) terminalPointAndLine(action mouse.MouseAction) (ui.SelectionPoint, string, bool) {
	if action.Region == nil {
		return ui.SelectionPoint{}, "", false
	}
	p.selectionTermPanel = action.Region.ID == regionTermPanelContent
	p.selection.ViewRect = action.Region.Rect
	lineIdx, col, ok := p.interactiveCharAtXY(action.X, action.Y)
	if !ok {
		return ui.SelectionPoint{}, "", false
	}
	buf := p.interactiveOutputBuffer()
	if buf == nil {
		return ui.SelectionPoint{}, "", false
	}
	var lines []string
	if _, _, absolute := buf.AbsoluteRange(); absolute {
		lines = buf.LinesAbsoluteRange(lineIdx, lineIdx+1)
	} else {
		lines = buf.LinesRange(lineIdx, lineIdx+1)
	}
	if len(lines) == 0 {
		return ui.SelectionPoint{}, "", false
	}
	return ui.SelectionPoint{Line: lineIdx, Col: col}, lines[0], true
}

func (p *Plugin) selectAllTerminalOutput(termPanel bool) {
	p.selectionTermPanel = termPanel
	buf := p.interactiveOutputBuffer()
	if buf == nil || buf.LineCount() == 0 {
		return
	}
	start, end := 0, buf.LineCount()
	if absoluteStart, absoluteEnd, absolute := buf.AbsoluteRange(); absolute {
		start, end = absoluteStart, absoluteEnd
	}
	last := buf.LinesRange(buf.LineCount()-1, buf.LineCount())
	lastWidth := 0
	if len(last) > 0 {
		lastWidth = ansi.StringWidth(ui.ExpandTabs(last[0], tabStopWidth))
	}
	p.selection.SelectRange(
		ui.SelectionPoint{Line: start, Col: 0},
		ui.SelectionPoint{Line: end - 1, Col: max(lastWidth-1, 0)},
		false,
	)
}

func (p *Plugin) copyOnSelectEnabled() bool {
	return p.ctx != nil && p.ctx.Config != nil && p.ctx.Config.Plugins.Workspace.CopyOnSelect
}

func (p *Plugin) copyInteractiveSelectionCmd() tea.Cmd {
	lines := p.interactiveSelectionLines()
	if len(lines) == 0 {
		lines = p.interactiveVisibleLines()
	}
	return func() tea.Msg {
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
