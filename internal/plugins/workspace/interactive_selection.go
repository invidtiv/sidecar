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
	// The preview pane's ViewRect is the outer panel, so its content starts
	// inside the border and padding. The term panel's ViewRect is already the
	// child's content rect, so it needs no inset.
	contentInset := previewContentInset
	if p.effectiveSelectionTermPanel() {
		contentInset = 0
	}
	relX := x - p.selection.ViewRect.X - contentInset
	if relX < 0 {
		return 0, false
	}
	// A pane wider than the viewport is drawn scrolled, so screen column 0 is
	// the pane's ColOffset (td-73fa86).
	relX += p.terminalSelectionViewportLayout().Fit.ColOffset

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

// selectionHitLayout returns the buffer window hit testing must map against, or
// false when no window is on screen to map onto.
func (p *Plugin) selectionHitLayout() (terminalViewportLayout, bool) {
	if p.selection.ViewRect.W == 0 || p.selection.ViewRect.H == 0 {
		return terminalViewportLayout{}, false
	}
	layout := p.terminalSelectionViewportLayout()
	if layout.End <= layout.Start && p.interactiveState != nil {
		// Compatibility for callers that construct only the old cached state.
		layout.Start = p.interactiveState.VisibleStart
		layout.End = p.interactiveState.VisibleEnd
	}
	if layout.End <= layout.Start {
		return terminalViewportLayout{}, false
	}
	return layout, true
}

// interactiveOutputRowAtY converts a screen row to a 0-indexed output row of the
// surface being selected. The result is deliberately unbounded: a click needs to
// reject rows outside the output area, while a drag clamps them.
func (p *Plugin) interactiveOutputRowAtY(y int) int {
	contentRow := y - p.selection.ViewRect.Y
	if !p.effectiveSelectionTermPanel() {
		// ViewRect is the outer preview panel, so step over its border first;
		// the remaining rows are the same stack terminalSurfaceGeometry uses.
		contentRow -= previewBorderRows
	}
	// Every surface spends its first content row on its header.
	return contentRow - terminalHeaderRows
}

// interactiveContentLeft is the screen column of the surface's first content
// cell. The preview pane's ViewRect is the outer panel, so its content starts
// inside the border and padding; the term panel's is already a content rect.
func (p *Plugin) interactiveContentLeft() int {
	if p.effectiveSelectionTermPanel() {
		return p.selection.ViewRect.X
	}
	return p.selection.ViewRect.X + previewContentInset
}

// absoluteBufferLine lifts a window-relative line index into the buffer's
// absolute coordinates when it keeps any.
func (p *Plugin) absoluteBufferLine(lineIdx int) int {
	buf := p.interactiveOutputBuffer()
	if buf == nil {
		return lineIdx
	}
	if base, _, absolute := buf.AbsoluteRange(); absolute {
		return lineIdx + base
	}
	return lineIdx
}

func (p *Plugin) interactiveLineIndexAtY(y int) (int, bool) {
	layout, ok := p.selectionHitLayout()
	if !ok {
		return 0, false
	}
	outputRow := p.interactiveOutputRowAtY(y)
	if outputRow < 0 {
		return 0, false
	}
	lineIdx := layout.Start + outputRow
	if lineIdx >= layout.End {
		return 0, false
	}
	return p.absoluteBufferLine(lineIdx), true
}

// interactiveClampedPoint maps a pointer position onto the nearest visible cell
// instead of refusing positions outside the output area. A held pointer
// routinely leaves the pane mid-gesture — below the last row, left of the
// content, out over the sidebar — and a selection that stops tracking there
// reads as broken. Anchoring clicks keep using the strict hit test, because a
// click on the header or the padding is not a click on text.
func (p *Plugin) interactiveClampedPoint(x, y int) (int, int, bool) {
	layout, ok := p.selectionHitLayout()
	if !ok {
		return 0, 0, false
	}
	outputRow := min(max(p.interactiveOutputRowAtY(y), 0), layout.End-layout.Start-1)
	lineIdx := p.absoluteBufferLine(layout.Start + outputRow)
	// Past the right edge, VisualColAtRelativeX already lands on the end of the
	// line, which is what dragging off the right of a row should select.
	col, ok := p.interactiveColAtX(max(x, p.interactiveContentLeft()), lineIdx)
	if !ok {
		return 0, 0, false
	}
	return lineIdx, col, true
}

// selectionDragScrollStep bounds how far one motion event past an edge walks the
// window through scrollback. Unbounded, a pointer flicked to the top of the
// screen would skip hundreds of lines the user never saw.
const selectionDragScrollStep = 3

// interactiveDragPoint maps an in-progress drag position onto the buffer,
// scrolling the surface first when the pointer has run past an edge so a
// selection can reach content that is not on screen.
func (p *Plugin) interactiveDragPoint(x, y int) (int, int, bool) {
	layout, ok := p.selectionHitLayout()
	if !ok {
		return 0, 0, false
	}
	outputRow := p.interactiveOutputRowAtY(y)
	rows := layout.End - layout.Start
	switch {
	case outputRow < 0:
		p.scrollTerminalSelectionViewport(-min(-outputRow, selectionDragScrollStep))
	case outputRow >= rows:
		p.scrollTerminalSelectionViewport(min(outputRow-rows+1, selectionDragScrollStep))
	}
	return p.interactiveClampedPoint(x, y)
}

// scrollTerminalSelectionViewport moves the surface the selection is anchored in
// by delta rows, clamped to the buffer. Both surfaces browse scrollback by an
// absolute top offset while selecting, so one derivation covers each of them.
func (p *Plugin) scrollTerminalSelectionViewport(delta int) {
	if delta == 0 {
		return
	}
	layout := p.terminalSelectionViewportLayout()
	target := min(max(layout.Start+delta, 0), layout.MaxOffset)
	if p.selectionTermPanel {
		p.termPanelSelectionOffset = target
		return
	}
	p.previewOffset = target
	p.autoScrollOutput = false
}

// prepareInteractiveDrag stores the click position and starts drag tracking
// without initializing selection. Selection only activates on actual drag motion.
func (p *Plugin) prepareInteractiveDrag(action mouse.MouseAction) tea.Cmd {
	if action.Region == nil {
		return nil
	}
	targetTermPanel := action.Region.ID == regionTermPanelContent
	sameSource := p.selectionTermPanel == targetTermPanel
	canExtend := action.Shift && p.selection.HasSelection() && sameSource
	p.prepareTerminalSelectionSource(targetTermPanel)
	// Set ViewRect before charAtXY so interactiveLineIndexAtY can use it.
	p.selection.ViewRect = action.Region.Rect
	// Track the pointer gesture even when the buffer is empty or the click lands
	// on terminal padding. A plain click still needs a release event to activate
	// the terminal, while motion can become selectable once it reaches a row.
	p.mouseHandler.StartDrag(action.X, action.Y, action.Region.ID, 0)

	lineIdx, col, ok := p.interactiveCharAtXY(action.X, action.Y)
	if !ok {
		if canExtend {
			// Shift-clicking the header or the padding below the output is a miss,
			// not an instruction to throw away the selection being extended.
			return nil
		}
		p.selection.Clear()
		// Clear drops ViewRect, but the gesture is still live: a drag that starts
		// on chrome or on empty padding must still be able to anchor itself once
		// it reaches a row (see anchorDragFromOrigin).
		p.selection.ViewRect = action.Region.Rect
		return nil
	}

	if canExtend {
		p.selection.ExtendTo(ui.SelectionPoint{Line: lineIdx, Col: col})
		return nil
	}
	// The term panel needs nothing further here: freezing termPanelSelectionOffset
	// above holds it still while selecting, and the agent/shell follow state is
	// independent and must not be disturbed.
	p.selection.PrepareDragMode(lineIdx, col, action.Region.Rect, action.Alt)

	return nil
}

// prepareTerminalSelectionSource moves all selection gestures onto one terminal
// surface. Coordinates and a terminal panel's frozen viewport are source-local,
// so every selection entry point must cross this boundary before hit-testing.
func (p *Plugin) prepareTerminalSelectionSource(termPanel bool) {
	if p.selectionTermPanel != termPanel {
		p.selection.Clear()
	}
	p.selectionTermPanel = termPanel
	if termPanel && !p.selection.Anchor.Valid() {
		p.termPanelSelectionOffset = p.terminalSelectionViewportLayout().Start
	}
}

// prepareTerminalClickOrDrag keeps a passive terminal's viewport stable until
// the pointer gesture has declared itself. A drag selects the rows that were
// actually under the pointer; a release without motion activates the terminal.
// Entering interactive mode on mouse-down used to resize/reframe the pane and
// clear the anchor before the first motion event arrived.
func (p *Plugin) prepareTerminalClickOrDrag(action mouse.MouseAction) tea.Cmd {
	p.activateTerminalAfterClick = !action.Shift && !action.Alt
	return p.prepareInteractiveDrag(action)
}

func (p *Plugin) handleInteractiveSelectionDrag(action mouse.MouseAction) tea.Cmd {
	// Freeze before anything reads or moves the window: previewOffset is ignored
	// while follow mode is active and is commonly still zero, so leaving it that
	// way lets the next render interpret zero as the top of the buffer and a drag
	// from the live edge jumps through all of scrollback.
	p.freezeTerminalSelectionViewport()
	lineIdx, col, ok := p.interactiveDragPoint(action.X, action.Y)
	if !ok {
		return nil
	}
	p.selection.HandleDrag(lineIdx, col)
	return nil
}

// freezeTerminalSelectionViewport pins the preview pane to the window the user
// can currently see. The term panel is frozen earlier, when its selection source
// is prepared, because its offset is only consulted once an anchor exists.
func (p *Plugin) freezeTerminalSelectionViewport() {
	if p.selectionTermPanel || !p.autoScrollOutput {
		return
	}
	p.previewOffset = p.terminalSelectionViewportLayout().Start
	p.autoScrollOutput = false
}

// anchorDragFromOrigin starts a selection for a drag whose mouse-down landed off
// the text — on the header, on the padding below the last row, in the border.
// The gesture is unambiguously a selection by the time it is moving, so anchor
// it at the nearest cell to where the button actually went down rather than
// letting the whole drag do nothing.
func (p *Plugin) anchorDragFromOrigin(action mouse.MouseAction) bool {
	originX := action.X - action.DragDX
	originY := action.Y - action.DragDY
	lineIdx, col, ok := p.interactiveClampedPoint(originX, originY)
	if !ok {
		return false
	}
	p.selection.PrepareDragMode(lineIdx, col, p.selection.ViewRect, action.Alt)
	return true
}

func (p *Plugin) finishInteractiveSelection() tea.Cmd {
	p.selection.FinishDrag()
	// A gesture that never left its anchor cell is a click that jittered, not a
	// selection. Without this, a twitch during a click leaves a one-cell
	// selection, silently copies it under copy-on-select, and swallows the
	// activation the user was asking for.
	if p.selection.HasSelection() && p.selection.Start == p.selection.End {
		p.selection.Clear()
	}
	if p.selection.HasSelection() {
		p.activateTerminalAfterClick = false
		if p.copyOnSelectEnabled() {
			return p.copyInteractiveSelectionCmd()
		}
		return nil
	}

	activate := p.activateTerminalAfterClick
	p.activateTerminalAfterClick = false
	if !activate {
		return nil
	}
	if p.selectionTermPanel {
		return p.enterTermPanelInteractiveMode()
	}
	return p.enterInteractiveMode()
}

func (p *Plugin) interactiveOutputBuffer() *tty.OutputBuffer {
	return p.terminalOutputBuffer(p.effectiveSelectionTermPanel())
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
	// A nil buffer is fine: the layout's geometry (the fit, the display size)
	// comes from the viewport and the pane, and hit testing needs it whether or
	// not any output has been captured yet.
	buffer := p.interactiveOutputBuffer()

	termPanel := p.selectionTermPanel
	if p.interactiveState != nil && p.interactiveState.Active {
		termPanel = p.interactiveState.TermPanel
	}
	// One derivation of the surface's viewport size, shared with the render and
	// cursor paths. The fallback covers the two cases the surface cannot place:
	// an unsized plugin, and the term panel asked for while hidden.
	width, height := p.calculatePreviewDimensions()
	if surface := p.terminalSurfaceGeometry(termPanel); surface.OK {
		width, height = surface.Width, surface.Height
	}

	interactive := p.viewMode == ViewModeInteractive && p.interactiveState != nil && p.interactiveState.Active
	input := terminalViewportInput{
		Buffer:      buffer,
		Width:       width,
		Height:      height,
		Interactive: interactive,
	}
	// Same geometry the render path uses, or hit-testing drifts from the pixels
	// (td-73fa86).
	input.PaneWidth, input.PaneHeight = p.resolvedPaneGeometry(termPanel, p.interactiveDescribes(termPanel))
	if p.interactiveState != nil {
		input.CursorCol = p.interactiveState.CursorCol
		input.CursorRow = p.interactiveState.CursorRow
		input.CursorVisible = p.interactiveState.CursorVisible
	}
	// The scrollbar takes a column from the content, which moves every column
	// the user can click on; hit testing has to see the same viewport the render
	// does (td-73fa86).
	_, input.TotalItems, _ = p.terminalHistorySummary(termPanel, buffer)
	// Exactly the condition the render and cursor paths use. Gating on the anchor
	// alone froze the offset for a selection that belongs to the *other* surface,
	// so hit testing read a different buffer window than the one drawn.
	input.Follow, input.Offset, input.OffsetFromBottom =
		p.terminalScrollState(termPanel, p.selectionTermPanel && p.selection.Anchor.Valid())
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
	p.prepareTerminalSelectionSource(action.Region.ID == regionTermPanelContent)
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
	p.prepareTerminalSelectionSource(termPanel)
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
