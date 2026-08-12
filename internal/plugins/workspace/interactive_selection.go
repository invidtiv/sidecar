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

// A held pointer past an edge keeps scrolling on a timer, the way every real
// terminal does; motion events alone stop arriving the moment the hand stops.
const (
	selectionAutoScrollInterval = 70 * time.Millisecond
	selectionAutoScrollStep     = 5
	// A release lost off-window is only noticed when the pointer comes back, so
	// an unbounded chain would keep dragging the selection through all of
	// scrollback (and onto the clipboard under copy-on-select) in the meantime.
	// Roughly 1.5s of holding still without any fresh motion pauses the chain;
	// real motion re-arms it, so a user genuinely parked at the edge only has to
	// twitch to keep going.
	selectionAutoScrollMaxRun = 20
)

// selectionAutoScrollTickMsg drives one step of that timer. The generation
// pins it to the gesture that scheduled it, so a tick in flight when the button
// comes up (or when a release is lost off-window) is discarded.
type selectionAutoScrollTickMsg struct {
	generation uint64
}

// selectionEdgeScrollRows turns a pointer row that has run past the content into
// the rows to scroll, negative above the top. Speed scales with the overshoot so
// a pointer parked just past the edge crawls while one thrown to the window edge
// moves quickly, bounded so no single step skips a screenful.
func selectionEdgeScrollRows(outputRow, rows, limit int) int {
	switch {
	case outputRow < 0:
		return -min(-outputRow, limit)
	case outputRow >= rows:
		return min(outputRow-rows+1, limit)
	}
	return 0
}

// interactiveDragPoint maps an in-progress drag position onto the buffer,
// scrolling the surface first when the pointer has run past an edge so a
// selection can reach content that is not on screen.
func (p *Plugin) interactiveDragPoint(x, y int) (int, int, bool) {
	layout, ok := p.selectionHitLayout()
	if !ok {
		return 0, 0, false
	}
	rows := layout.End - layout.Start
	p.scrollTerminalSelectionViewport(
		selectionEdgeScrollRows(p.interactiveOutputRowAtY(y), rows, selectionDragScrollStep))
	return p.interactiveClampedPoint(x, y)
}

// selectionAutoScrollHoldExpired reports when a run of ticks with no fresh drag
// motion behind it has gone on long enough to be treated as a lost release
// rather than a pointer deliberately parked past the edge.
func selectionAutoScrollHoldExpired(ticks int) bool {
	return ticks > selectionAutoScrollMaxRun
}

// selectionAutoScrollDelta reports how far one tick should move the window for a
// pointer held at y, and zero once the pointer is back inside the content.
func (p *Plugin) selectionAutoScrollDelta(y int) int {
	layout, ok := p.selectionHitLayout()
	if !ok {
		return 0
	}
	return selectionEdgeScrollRows(
		p.interactiveOutputRowAtY(y), layout.End-layout.Start, selectionAutoScrollStep)
}

// scheduleSelectionAutoScroll queues the next step of the held-pointer scroll,
// at most one tick in flight at a time.
func (p *Plugin) scheduleSelectionAutoScroll() tea.Cmd {
	if p.selectionAutoScrollPending {
		return nil
	}
	p.selectionAutoScrollPending = true
	generation := p.selectionGeneration
	return tea.Tick(selectionAutoScrollInterval, func(time.Time) tea.Msg {
		return selectionAutoScrollTickMsg{generation: generation}
	})
}

// advanceSelectionAutoScroll scrolls one step for a pointer still held past an
// edge and re-arms itself. It stops when the gesture ended, the pointer came
// back inside the content, or the buffer has no more rows in that direction.
func (p *Plugin) advanceSelectionAutoScroll(msg selectionAutoScrollTickMsg) tea.Cmd {
	if msg.generation != p.selectionGeneration {
		return nil
	}
	p.selectionAutoScrollPending = false
	if p.isModalViewMode() {
		// A modal swallows the release (handleMouseDragEnd refuses it), so the
		// gesture can never finish itself. End it here or the pane keeps scrolling
		// underneath the modal.
		p.beginSelectionGesture()
		return nil
	}
	if !p.selection.Anchor.Valid() {
		return nil
	}
	p.selectionAutoScrollTicks++
	if selectionAutoScrollHoldExpired(p.selectionAutoScrollTicks) {
		return nil
	}
	delta := p.selectionAutoScrollDelta(p.selectionDragY)
	if delta == 0 {
		return nil
	}
	before := p.terminalSelectionViewportLayout().Start
	p.scrollTerminalSelectionViewport(delta)
	if p.terminalSelectionViewportLayout().Start == before {
		return nil
	}
	p.extendSelectionDragTo(p.selectionDragX, p.selectionDragY)
	return p.scheduleSelectionAutoScroll()
}

// extendSelectionDragTo moves the live selection to the cell nearest the pointer,
// snapped to the gesture's unit.
func (p *Plugin) extendSelectionDragTo(x, y int) bool {
	lineIdx, col, ok := p.interactiveClampedPoint(x, y)
	if !ok {
		return false
	}
	if !p.extendSelectionToUnit(lineIdx, col) {
		p.selection.HandleDrag(lineIdx, col)
	}
	return true
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
	p.beginSelectionGesture()
	if !canExtend {
		// A plain click is a character gesture. A shift-click keeps whatever
		// granularity it is extending, the way xterm does.
		p.resetSelectionUnit()
	}
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
			// Shift-clicking the header or the padding is a reach for the nearest
			// text, as in xterm — never an instruction to drop the selection.
			if clampedLine, clampedCol, clamped := p.interactiveClampedPoint(action.X, action.Y); clamped {
				p.extendSelectionTo(clampedLine, clampedCol)
			}
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
		p.extendSelectionTo(lineIdx, col)
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
		// The anchor unit's span is in the old surface's coordinates.
		p.resetSelectionUnit()
	}
	p.selectionTermPanel = termPanel
	if termPanel && !p.selection.Anchor.Valid() {
		p.termPanelSelectionOffset = p.terminalSelectionViewportLayout().Start
	}
}

// clickResolution is what a mouse-down over a terminal surface will mean if the
// gesture ends without motion. The two outcomes are mutually exclusive for one
// gesture and every abort path (lost release, double-click, a click elsewhere)
// has to drop whichever is pending, so one enum beats a flag per outcome: it
// makes "activate and forward" unrepresentable and each reset a single write.
type clickResolution uint8

const (
	clickResolutionNone clickResolution = iota
	// clickResolutionActivate makes a passive terminal live.
	clickResolutionActivate
	// clickResolutionForward hands the click to the app running in a live
	// terminal that has asked for mouse reports.
	clickResolutionForward
)

// prepareTerminalClickOrDrag keeps a passive terminal's viewport stable until
// the pointer gesture has declared itself. A drag selects the rows that were
// actually under the pointer; a release without motion activates the terminal.
// Entering interactive mode on mouse-down used to resize/reframe the pane and
// clear the anchor before the first motion event arrived.
func (p *Plugin) prepareTerminalClickOrDrag(action mouse.MouseAction) tea.Cmd {
	p.pendingClickResolution = clickResolutionNone
	if !action.Shift && !action.Alt {
		p.armPendingClick(clickResolutionActivate, action)
	}
	return p.prepareInteractiveDrag(action)
}

// prepareInteractiveTerminalGesture arms a click over a live terminal without
// deciding yet whether it belongs to the app or to a selection. Forwarding the
// button press on mouse-down, as this used to do whenever the app had mouse
// tracking on, meant apps like Claude Code and grok swallowed every drag after
// the first — the pane only ever selected during the window before the first
// frame reported mouse reporting at all. Motion now always selects locally;
// only a release without motion reaches the app.
func (p *Plugin) prepareInteractiveTerminalGesture(action mouse.MouseAction) tea.Cmd {
	p.pendingClickResolution = clickResolutionNone
	modified := action.Shift || action.Alt
	// A validated link is Sidecar-owned even while the application has enabled
	// mouse reporting: the same text is visibly decorated in passive and live
	// terminal views, so an ordinary click must honor that promise before the
	// gesture is offered to the application. Modified clicks remain selection
	// gestures and never activate links.
	if !modified {
		if link, context, termPanel, found := p.terminalLinkAt(action); found {
			documentTarget := link.Kind == terminalPathLink && docPaneTarget(link.Value, true)
			// Preserve the exact live window containing the link before opening the
			// document changes pane geometry. Claude commonly moves that transcript
			// into history and publishes a sparse live grid after the resize; leaving
			// follow enabled would replace the clicked context with that sparse grid.
			freeze := p.captureTerminalViewportForDocOpen(termPanel)
			if cmd, ok := p.activateResolvedTerminalLink(link, context, termPanel); ok {
				// URLs and non-document file navigation do not resize this surface.
				// Bare markdown and authoritative path:line routes both create a doc pane.
				if !documentTarget {
					return cmd
				}
				_, leaf := p.activeDocPane()
				if leaf == nil {
					return cmd
				}
				p.applyTerminalViewportFreeze(freeze)
				// A document pane is not keyboard-focusable while terminal input is
				// live. Link activation transfers focus out of the terminal, so leave
				// interactive routing now rather than retaining stale interactive
				// geometry/input ownership beside the newly focused document.
				p.exitInteractiveMode()
				p.activePane = PanePreview
				p.paneFocus = leaf.ID
				p.termPanelFocused = false
				return cmd
			}
		}
	}
	forwards := !modified && p.interactiveState != nil && p.interactiveState.MouseReportingEnabled
	if forwards {
		p.armPendingClick(clickResolutionForward, action)
	}
	return p.prepareInteractiveDrag(action)
}

type terminalViewportFreeze struct {
	termPanel  bool
	start      int
	projection terminalDocProjection
}

// terminalDocProjection is a bounded copy of the exact terminal rows visible
// when a document link won the click. Full-screen applications own their grid:
// SIGWINCH may replace it without retaining those rows in tmux history, so a
// coordinate alone cannot preserve the context the user clicked. The live
// terminal buffer continues updating independently beneath this read-only view.
type terminalDocProjection struct {
	buffer    *tty.OutputBuffer
	termPanel bool
	identity  string
}

// captureTerminalViewportForDocOpen records the live surface's current window
// before a document split resizes the tmux pane. It deliberately does not
// mutate scroll state: a link can fail fresh-root or file revalidation at click
// time, and a refused activation must remain an otherwise ordinary gesture.
// The primary surface browses by an absolute top row; the terminal panel's
// established passive contract stores the equivalent distance from the bottom.
func (p *Plugin) captureTerminalViewportForDocOpen(termPanel bool) terminalViewportFreeze {
	previousSource := p.selectionTermPanel
	p.selectionTermPanel = termPanel
	layout := p.terminalSelectionViewportLayout()
	buffer := p.interactiveOutputBuffer()
	p.selectionTermPanel = previousSource
	freeze := terminalViewportFreeze{termPanel: termPanel, start: layout.Start}
	if buffer == nil || layout.End <= layout.Start {
		return freeze
	}
	rows := buffer.LinesRange(layout.Start, layout.End)
	if len(rows) == 0 {
		return freeze
	}
	snapshot := tty.NewOutputBuffer(len(rows))
	snapshot.ApplySnapshot(tty.PaneSnapshot{Output: strings.Join(rows, "\n"), PaneRows: len(rows)})
	freeze.projection = terminalDocProjection{
		buffer: snapshot, termPanel: termPanel, identity: p.terminalProjectionIdentity(termPanel),
	}
	return freeze
}

func (p *Plugin) applyTerminalViewportFreeze(freeze terminalViewportFreeze) {
	p.terminalDocProjection = freeze.projection
	if freeze.termPanel {
		p.termPanelSelectionOffset = freeze.start
		p.termPanelDocFrozen = true
		return
	}
	p.previewOffset = freeze.start
	p.autoScrollOutput = false
}

func (p *Plugin) projectedTerminalBuffer(termPanel bool) *tty.OutputBuffer {
	projection := p.terminalDocProjection
	if projection.buffer == nil || projection.termPanel != termPanel ||
		projection.identity != p.terminalProjectionIdentity(termPanel) {
		return nil
	}
	return projection.buffer
}

func (p *Plugin) terminalProjectionIdentity(termPanel bool) string {
	if termPanel {
		return "panel:" + p.termPanelSession + "\x00" + p.termPanelPaneID
	}
	if p.shellSelected {
		if shell := p.getSelectedShell(); shell != nil {
			return "shell:" + shell.TmuxName + "\x00" + p.terminalLinkTarget(false)
		}
		return ""
	}
	if wt := p.selectedWorktree(); wt != nil {
		return "workspace:" + stablePathKey(wt.Path) + "\x00" + p.terminalLinkTarget(false)
	}
	return ""
}

func (p *Plugin) releaseTerminalDocProjection(termPanel bool) {
	if p.terminalDocProjection.buffer != nil && p.terminalDocProjection.termPanel == termPanel {
		p.terminalDocProjection = terminalDocProjection{}
	}
}

func (p *Plugin) armPendingClick(resolution clickResolution, action mouse.MouseAction) {
	p.pendingClickResolution = resolution
	p.pendingClickX, p.pendingClickY = action.X, action.Y
}

func (p *Plugin) handleInteractiveSelectionDrag(action mouse.MouseAction) tea.Cmd {
	// Freeze before anything reads or moves the window: previewOffset is ignored
	// while follow mode is active and is commonly still zero, so leaving it that
	// way lets the next render interpret zero as the top of the buffer and a drag
	// from the live edge jumps through all of scrollback.
	p.freezeTerminalSelectionViewport()
	// The tick re-reads this position, so a pointer held still past an edge keeps
	// scrolling after motion events stop arriving. Real motion also restarts the
	// hold budget that bounds a chain running on a lost release.
	p.selectionDragX, p.selectionDragY = action.X, action.Y
	p.selectionAutoScrollTicks = 0
	lineIdx, col, ok := p.interactiveDragPoint(action.X, action.Y)
	if !ok {
		return nil
	}
	if !p.extendSelectionToUnit(lineIdx, col) {
		p.selection.HandleDrag(lineIdx, col)
	}
	if p.selectionAutoScrollDelta(action.Y) == 0 {
		return nil
	}
	return p.scheduleSelectionAutoScroll()
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
	// End the gesture before anything else: a scroll tick still in flight must not
	// keep dragging the window after the button is up.
	unit := p.selectionUnit
	p.beginSelectionGesture()
	// A character gesture that never left its anchor cell is a click that
	// jittered, not a selection. Without this, a twitch during a click leaves a
	// one-cell selection, silently copies it under copy-on-select, and swallows
	// the activation the user was asking for. A word gesture on a one-character
	// word legitimately ends here, so it is exempt.
	if unit == selectionUnitChar && p.selection.HasSelection() && p.selection.Start == p.selection.End {
		p.selection.Clear()
	}
	if p.selection.HasSelection() {
		p.pendingClickResolution = clickResolutionNone
		if p.copyOnSelectEnabled() {
			return p.copyInteractiveSelectionCmd()
		}
		return nil
	}

	resolution := p.pendingClickResolution
	p.pendingClickResolution = clickResolutionNone
	switch resolution {
	case clickResolutionActivate:
		if p.selectionTermPanel {
			return p.enterTermPanelInteractiveMode()
		}
		return p.enterInteractiveMode()
	case clickResolutionForward:
		// The press position, not the release: a click that resolves here never
		// moved, and forwardClickToTmux sends press and release together.
		return tea.Batch(
			p.forwardClickToTmux(p.pendingClickX, p.pendingClickY),
			p.pollInteractivePaneImmediate(),
		)
	}
	return nil
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

// selectionUnit is the granularity a gesture extends by. Double- and
// triple-clicks keep dragging in whole words and whole lines, as xterm and
// iTerm2 do, until the next plain click.
type selectionUnit uint8

const (
	selectionUnitChar selectionUnit = iota
	selectionUnitWord
	selectionUnitLine
)

func (p *Plugin) selectTerminalWord(action mouse.MouseAction) tea.Cmd {
	return p.selectTerminalUnit(action, selectionUnitWord)
}

func (p *Plugin) selectTerminalLine(action mouse.MouseAction) tea.Cmd {
	return p.selectTerminalUnit(action, selectionUnitLine)
}

// selectTerminalUnit installs the word or line under the pointer and records it
// as the gesture's anchor unit, so a button still held extends by that unit and
// never eats into the unit the gesture started on.
func (p *Plugin) selectTerminalUnit(action mouse.MouseAction, unit selectionUnit) tea.Cmd {
	point, line, ok := p.terminalPointAndLine(action)
	if !ok {
		return nil
	}
	start, end, ok := selectionSpanAt(unit, line, point.Line, point.Col)
	if !ok {
		return nil
	}
	p.beginSelectionGesture()
	// Arm drag tracking exactly as a plain mouse-down does, so the button still
	// held after a double or triple click keeps delivering motion to this gesture
	// and its release arrives as a drag end rather than a fresh click.
	p.mouseHandler.StartDrag(action.X, action.Y, action.Region.ID, 0)
	p.selectionDragX, p.selectionDragY = action.X, action.Y
	p.selectionUnit = unit
	p.selectionUnitStart, p.selectionUnitEnd = start, end
	// The mouse-down that opened this gesture asked for the terminal, or for the
	// app's click; a double or triple click withdraws that, or the release would
	// fire it under the selection the user just made.
	p.pendingClickResolution = clickResolutionNone
	p.selection.SelectRange(start, end, false)
	if p.copyOnSelectEnabled() {
		return p.copyInteractiveSelectionCmd()
	}
	return nil
}

// extendSelectionTo grows an existing selection to a point, snapped to the
// gesture's unit when one is in flight.
func (p *Plugin) extendSelectionTo(lineIdx, col int) {
	if p.extendSelectionToUnit(lineIdx, col) {
		return
	}
	p.selection.ExtendTo(ui.SelectionPoint{Line: lineIdx, Col: col})
}

// extendSelectionToUnit extends a word or line gesture to the unit under the
// pointer. The anchor unit stays whole in either direction: dragging backwards
// pins the far edge of the anchor, which is what makes word-drag feel like a
// terminal rather than a character drag that happens to start on a word.
func (p *Plugin) extendSelectionToUnit(lineIdx, col int) bool {
	if p.selectionUnit == selectionUnitChar ||
		!p.selectionUnitStart.Valid() || !p.selectionUnitEnd.Valid() {
		return false
	}
	line, ok := p.terminalLineText(lineIdx)
	if !ok {
		return false
	}
	start, end, ok := selectionSpanAt(p.selectionUnit, line, lineIdx, col)
	if !ok {
		return false
	}
	if start.Before(p.selectionUnitStart) {
		p.selection.SelectRange(p.selectionUnitEnd, start, false)
	} else {
		p.selection.SelectRange(p.selectionUnitStart, end, false)
	}
	p.selection.Active = true
	return true
}

// beginSelectionGesture closes whatever gesture was running: any auto-scroll
// tick still in flight belongs to the old generation and is dropped.
func (p *Plugin) beginSelectionGesture() {
	p.selectionGeneration++
	p.selectionAutoScrollPending = false
	p.selectionAutoScrollTicks = 0
}

// clearTerminalSelection drops a selection made outside a pointer gesture —
// scrolling away from it, leaving interactive mode, opening a link. The unit
// goes with it: a word span left over from an old double-click would otherwise
// redefine where the next shift-click extends from.
func (p *Plugin) clearTerminalSelection() {
	p.selection.Clear()
	p.resetSelectionUnit()
}

func (p *Plugin) resetSelectionUnit() {
	p.selectionUnit = selectionUnitChar
	p.selectionUnitStart = ui.SelectionPoint{Line: -1, Col: -1}
	p.selectionUnitEnd = ui.SelectionPoint{Line: -1, Col: -1}
}

// selectionSpanAt returns the inclusive visual span of the unit covering col in
// line. Character gestures have no span to snap to.
func selectionSpanAt(unit selectionUnit, line string, lineIdx, col int) (ui.SelectionPoint, ui.SelectionPoint, bool) {
	expanded := ui.ExpandTabs(line, tabStopWidth)
	switch unit {
	case selectionUnitWord:
		startCol, endCol, ok := terminalWordSpan(expanded, col)
		if !ok {
			return ui.SelectionPoint{}, ui.SelectionPoint{}, false
		}
		return ui.SelectionPoint{Line: lineIdx, Col: startCol},
			ui.SelectionPoint{Line: lineIdx, Col: endCol}, true
	case selectionUnitLine:
		width := ansi.StringWidth(expanded)
		return ui.SelectionPoint{Line: lineIdx, Col: 0},
			ui.SelectionPoint{Line: lineIdx, Col: max(width-1, 0)}, true
	}
	return ui.SelectionPoint{}, ui.SelectionPoint{}, false
}

// terminalWordSpan returns the inclusive visual column span of the word covering
// col in a tab-expanded line. A run of word runes selects whole; anything else
// selects the single cell under the pointer, matching xterm.
func terminalWordSpan(line string, col int) (int, int, bool) {
	plain := ansi.Strip(line)
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
	at := 0
	remaining := plain
	for len(remaining) > 0 {
		seq, width, n, newState := ansi.GraphemeWidth.DecodeSequenceInString(remaining, state, nil)
		if n <= 0 {
			break
		}
		if width > 0 {
			tokens = append(tokens, visualToken{text: seq, start: at, end: at + width})
			at += width
		}
		state = newState
		remaining = remaining[n:]
	}
	if len(tokens) == 0 {
		return 0, 0, false
	}
	index := len(tokens) - 1
	for i, token := range tokens {
		if col < token.end {
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
	return tokens[left].start, tokens[right].end - 1, true
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
	line, ok := p.terminalLineText(lineIdx)
	if !ok {
		return ui.SelectionPoint{}, "", false
	}
	return ui.SelectionPoint{Line: lineIdx, Col: col}, line, true
}

// terminalLineText reads one line of the selected surface, in whichever
// coordinate space its buffer keeps.
func (p *Plugin) terminalLineText(lineIdx int) (string, bool) {
	buf := p.interactiveOutputBuffer()
	if buf == nil {
		return "", false
	}
	var lines []string
	if _, _, absolute := buf.AbsoluteRange(); absolute {
		lines = buf.LinesAbsoluteRange(lineIdx, lineIdx+1)
	} else {
		lines = buf.LinesRange(lineIdx, lineIdx+1)
	}
	if len(lines) == 0 {
		return "", false
	}
	return lines[0], true
}

func (p *Plugin) selectAllTerminalOutput(termPanel bool) {
	// Select-all is its own anchor, at character granularity: a word span from an
	// earlier double-click must not survive to redefine the next shift-click.
	p.resetSelectionUnit()
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
	return func() tea.Msg {
		if len(lines) == 0 {
			// A copy chord with nothing selected must not replace the clipboard
			// with a screen dump — cmd+c is reflex, and the clipboard may hold
			// something the user still needs. Select-all is the explicit path.
			return app.ToastMsg{Message: "Nothing selected — ctrl+a selects all output", Duration: 2 * time.Second}
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
