package workspace

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	app "github.com/marcus/sidecar/internal/app"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

// terminalSelectionGeometry places the terminal surface a pointer gesture is
// working in. The preview pane's ViewRect is the outer panel, so its content
// starts inside the border and below the header; the term panel's ViewRect is
// already the child's content rect, and every surface spends its first content
// row on its header. An off-screen or empty window yields a geometry that only
// column mapping can use, so anchoring and dragging refuse it.
func (p *Plugin) terminalSelectionGeometry() tty.Geometry {
	layout, onScreen := p.selectionHitLayout()
	if !onScreen {
		layout = p.terminalSelectionViewportLayout()
		layout.Start, layout.End = 0, 0
	}
	rect := p.selection.ViewRect
	left, top := rect.X, rect.Y+terminalHeaderRows
	if !p.effectiveSelectionTermPanel() {
		left += previewContentInset
		top += previewBorderRows
	}
	return tty.GeometryFor(left, top, layout, tabStopWidth)
}

// interactiveColAtX maps a viewport X coordinate to a visual column in the given line.
func (p *Plugin) interactiveColAtX(x, lineIdx int) (int, bool) {
	return tty.ColAt(p.terminalSelectionGeometry(), p.interactiveOutputBuffer(), x, lineIdx)
}

// interactiveCharAtXY maps viewport coordinates to line index + visual column.
func (p *Plugin) interactiveCharAtXY(x, y int) (int, int, bool) {
	cell, ok := tty.CellAt(p.terminalSelectionGeometry(), p.interactiveOutputBuffer(), x, y)
	return cell.Line, cell.Col, ok
}

// selectionHitLayout returns the buffer window hit testing must map against, or
// false when no window is on screen to map onto.
func (p *Plugin) selectionHitLayout() (terminalViewportLayout, bool) {
	if p.selection.ViewRect.W == 0 || p.selection.ViewRect.H == 0 {
		return terminalViewportLayout{}, false
	}
	layout := p.terminalSelectionViewportLayout()
	if layout.End <= layout.Start {
		return terminalViewportLayout{}, false
	}
	return layout, true
}

// selectionAutoScrollTickMsg drives one step of the held-pointer edge scroll.
// The generation pins it to the gesture that scheduled it, so a tick in flight
// when the button comes up (or when a release is lost off-window) is discarded.
type selectionAutoScrollTickMsg struct {
	generation uint64
}

// selectionAutoScrollDelta reports how far one tick should move the window for a
// pointer held at y, and zero once the pointer is back inside the content.
func (p *Plugin) selectionAutoScrollDelta(y int) int {
	return tty.EdgeScrollDelta(p.terminalSelectionGeometry(), y, tty.AutoScrollStep)
}

func (p *Plugin) scheduleSelectionAutoScroll() tea.Cmd {
	return p.pointer.ScheduleAutoScroll(func(generation uint64) tea.Msg {
		return selectionAutoScrollTickMsg{generation: generation}
	})
}

// advanceSelectionAutoScroll scrolls one step for a pointer still held past an
// edge and re-arms itself. It stops when the gesture ended, the pointer came
// back inside the content, or the buffer has no more rows in that direction.
func (p *Plugin) advanceSelectionAutoScroll(msg selectionAutoScrollTickMsg) tea.Cmd {
	if msg.generation != p.pointer.Generation() {
		// A tick from a gesture that is already over, which must not end the one
		// running now.
		return nil
	}
	if p.isModalViewMode() {
		// A modal swallows the release (handleMouseDragEnd refuses it), so the
		// gesture can never finish itself. End it here or the pane keeps scrolling
		// underneath the modal.
		p.pointer.Begin()
		return nil
	}
	if !p.pointer.AdvanceAutoScroll(msg.generation, p.selectionAutoScrollTarget()) {
		return nil
	}
	return p.scheduleSelectionAutoScroll()
}

// selectionAutoScrollTarget is this surface's window, for the shared driver.
func (p *Plugin) selectionAutoScrollTarget() tty.AutoScrollTarget {
	return tty.AutoScrollTarget{
		Geometry:  p.terminalSelectionGeometry,
		Buffer:    p.interactiveOutputBuffer,
		Selection: &p.selection,
		Scroll: func(delta int) bool {
			before := p.terminalSelectionViewportLayout().Start
			p.scrollTerminalSelectionViewport(delta)
			return p.terminalSelectionViewportLayout().Start != before
		},
	}
}

// extendSelectionDragTo moves the live selection to the cell nearest the pointer,
// snapped to the gesture's unit.
func (p *Plugin) extendSelectionDragTo(x, y int) bool {
	return p.pointer.DragTo(
		p.terminalSelectionGeometry(), p.interactiveOutputBuffer(), &p.selection, x, y)
}

// scrollTerminalSelectionViewport moves the surface the selection is anchored in
// by delta rendered rows, positive downwards, clamped to the buffer. A window a
// gesture is holding is placed from an absolute start on either surface, so one
// rule — the shared window's — covers both, and reconciling rendered rows with
// a window counted back from the live bottom happens there rather than here.
func (p *Plugin) scrollTerminalSelectionViewport(delta int) {
	if delta == 0 {
		return
	}
	layout := p.terminalSelectionViewportLayout()
	if p.selectionTermPanel {
		p.termPanelScroll = tty.ScrollWindowRows(&p.termPanelFreeze, p.termPanelScroll, delta, layout.MaxOffset)
		return
	}
	p.previewScroll = tty.ScrollWindowRows(&p.previewFreeze, p.previewScroll, delta, layout.MaxOffset)
}

// prepareInteractiveDrag arms the pointer over a terminal surface. want is what
// a release without motion will mean; selection only activates on actual drag
// motion.
func (p *Plugin) prepareInteractiveDrag(action mouse.MouseAction, want tty.ClickResolution) tea.Cmd {
	if action.Region == nil {
		return nil
	}
	targetTermPanel := action.Region.ID == regionTermPanelContent
	sameSource := p.selectionTermPanel == targetTermPanel
	p.prepareTerminalSelectionSource(targetTermPanel)
	// Name the surface before the geometry is built, so hit testing can use it.
	p.pointer.AdoptSurface(&p.selection, action.Region.Rect)
	// Track the pointer gesture even when the buffer is empty or the click lands
	// on terminal padding. A plain click still needs a release event to activate
	// the terminal, while motion can become selectable once it reaches a row.
	p.mouseHandler.StartDrag(action.X, action.Y, action.Region.ID, 0)
	p.pointer.Press(
		p.terminalSelectionGeometry(), p.interactiveOutputBuffer(), &p.selection,
		tty.PressEvent{
			X: action.X, Y: action.Y,
			Shift: action.Shift, Alt: action.Alt,
			Rect: action.Region.Rect, Want: want, SameSource: sameSource,
		})
	return nil
}

// prepareTerminalSelectionSource moves all selection gestures onto one terminal
// surface. Coordinates and a terminal panel's frozen viewport are source-local,
// so every selection entry point must cross this boundary before hit-testing.
func (p *Plugin) prepareTerminalSelectionSource(termPanel bool) {
	if p.selectionTermPanel != termPanel {
		p.selection.Clear()
		// The anchor unit's span is in the old surface's coordinates.
		p.pointer.ResetUnit()
		// A panel pin belongs to the selection being dropped here, so it goes with
		// it — before the new source reads the panel's window back.
		p.releaseTermPanelGesturePin()
	}
	p.selectionTermPanel = termPanel
	if termPanel && !p.selection.Anchor.Valid() {
		// Pin the panel before anything hit-tests against it: a gesture reads the
		// rows it was armed on, whatever output arrives underneath.
		p.pinTermPanelWindow(p.terminalSelectionViewportLayout().Start, false)
	}
}

// prepareTerminalClickOrDrag keeps a passive terminal's viewport stable until
// the pointer gesture has declared itself. A drag selects the rows that were
// actually under the pointer; a release without motion activates the terminal.
// Entering interactive mode on mouse-down resizes and reframes the pane, which
// would clear the anchor before the first motion event arrived.
func (p *Plugin) prepareTerminalClickOrDrag(action mouse.MouseAction) tea.Cmd {
	return p.prepareInteractiveDrag(action, tty.ResolveClick(tty.ClickIntent{
		Modified: action.Shift || action.Alt,
	}))
}

// prepareInteractiveTerminalGesture arms a click over a live terminal without
// deciding yet whether it belongs to the app or to a selection. Motion always
// selects locally and only a release without motion reaches the app: a press
// forwarded on mouse-down is swallowed by apps like Claude Code and grok, which
// leaves the pane unselectable for every gesture after the first.
func (p *Plugin) prepareInteractiveTerminalGesture(action mouse.MouseAction) tea.Cmd {
	// Drop the previous gesture's resolution first, before any branch below can
	// return without arming a new one — prepareInteractiveDrag refuses an action
	// with no region, and a surviving resolution fires at a stale position on the
	// next release.
	p.pointer.Resolution = tty.ClickNone
	modified := action.Shift || action.Alt
	linkCmd, claimed := p.activateTerminalLinkAt(action, modified)
	terminal := p.activeInteractiveTerminal()
	resolution := tty.ResolveClick(tty.ClickIntent{
		Live:           true,
		MouseReporting: terminal != nil && terminal.PaneMouseReporting(),
		Modified:       modified,
		LinkClaimed:    claimed,
	})
	if claimed {
		// A claimed press arms nothing, so the previous gesture's resolution is
		// replaced by the shared resolver's answer for it rather than left waiting
		// for a release that will never mean anything.
		p.pointer.Resolution = resolution
		return linkCmd
	}
	return p.prepareInteractiveDrag(action, resolution)
}

// activateTerminalLinkAt opens the link under a press, and reports whether one
// took the click.
//
// A validated link is Sidecar-owned even while the application has enabled
// mouse reporting: the same text is visibly decorated in passive and live
// terminal views, so an ordinary click must honor that promise before the
// gesture is offered to the application. Modified clicks remain selection
// gestures and never activate links, and a link whose activation is refused —
// a stale path, a failed revalidation — leaves an otherwise ordinary gesture.
func (p *Plugin) activateTerminalLinkAt(action mouse.MouseAction, modified bool) (tea.Cmd, bool) {
	if modified {
		return nil, false
	}
	link, context, termPanel, found := p.terminalLinkAt(action)
	if !found {
		return nil, false
	}
	paneTarget := p.paneRoot != nil &&
		(link.Kind == terminalIssueLink ||
			link.Kind == terminalDiffLink ||
			(link.Kind == terminalPathLink && docPaneTarget(link.Value)))
	// Preserve the exact live window containing the link before opening the
	// document changes pane geometry. Claude commonly moves that transcript
	// into history and publishes a sparse live grid after the resize; leaving
	// follow enabled would replace the clicked context with that sparse grid.
	freeze := p.captureTerminalViewportForDocOpen(termPanel)
	cmd, ok := p.activateResolvedTerminalLink(link, context, termPanel)
	if !ok {
		return nil, false
	}
	// URLs and non-document file navigation do not resize this surface.
	// Bare markdown and authoritative path:line routes both create a doc pane,
	// a td id creates an issue pane, and a git spec creates a Diff pane;
	// those all move the terminal's box.
	if !paneTarget {
		return cmd, true
	}
	leaf := p.openedPaneLeaf(link.Kind)
	if leaf == nil {
		return cmd, true
	}
	p.applyTerminalViewportFreeze(freeze)
	// A content pane is not keyboard-focusable while terminal input is live.
	// Link activation transfers focus out of the terminal, so leave interactive
	// routing now rather than retaining stale interactive geometry/input
	// ownership beside the newly focused pane.
	p.exitInteractiveMode()
	p.activePane = PanePreview
	p.paneFocus = leaf.ID
	p.termPanelFocused = false
	return cmd, true
}

// openedPaneLeaf returns the leaf a link of this kind opens into, so the click
// path can hand it focus without asking a second time what kind of content it
// just opened.
func (p *Plugin) openedPaneLeaf(kind terminalLinkKind) *PaneNode {
	switch kind {
	case terminalIssueLink:
		_, leaf := p.activeIssuePane()
		return leaf
	case terminalDiffLink:
		_, leaf := p.activeDiffPane()
		return leaf
	default:
		_, leaf := p.activeDocPane()
		return leaf
	}
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
	source    *tty.OutputBuffer
	termPanel bool
	identity  string
}

// captureTerminalViewportForDocOpen records the live surface's current window
// before a document split resizes the tmux pane. It deliberately does not
// mutate scroll state: a link can fail fresh-root or file revalidation at click
// time, and a refused activation must remain an otherwise ordinary gesture.
// Both surfaces record the window as the absolute row it starts at; the panel
// translates that back to a distance from the live bottom when it thaws.
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
		buffer: snapshot, source: p.liveTerminalOutputBuffer(termPanel),
		termPanel: termPanel, identity: p.terminalProjectionIdentity(termPanel),
	}
	return freeze
}

func (p *Plugin) applyTerminalViewportFreeze(freeze terminalViewportFreeze) {
	p.terminalDocProjection = freeze.projection
	if freeze.termPanel {
		// The clicked window wins over anything an earlier gesture pinned: this is
		// the context the document was opened from, and it outlives the selection
		// the click made.
		p.releaseTermPanelWindowPin()
		p.pinTermPanelWindow(freeze.start, true)
		return
	}
	// Same for the primary surface: the clicked window wins over anything an
	// earlier gesture pinned, and it outlives the selection the click made.
	p.releasePreviewWindowPin()
	p.pinPreviewWindow(freeze.start, true)
}

func (p *Plugin) projectedTerminalBuffer(termPanel bool) *tty.OutputBuffer {
	projection := p.terminalDocProjection
	if projection.buffer == nil || projection.termPanel != termPanel ||
		projection.identity != p.terminalProjectionIdentity(termPanel) ||
		projection.source == nil || projection.source != p.liveTerminalOutputBuffer(termPanel) {
		return nil
	}
	return projection.buffer
}

func (p *Plugin) terminalProjectionIdentity(termPanel bool) string {
	if termPanel {
		return "panel:" + p.termPanelSession + "\x00" + p.termPanelPaneID
	}
	if p.selectingShell() {
		if shell := p.getSelectedShell(); shell != nil {
			return "shell:" + shell.TmuxName + "\x00" + p.terminalLinkTarget(false)
		}
		return ""
	}
	if wt := p.selectedWorktree(); wt != nil {
		return workspaceSurfaceIdentity(wt) + "\x00" + p.terminalLinkTarget(false)
	}
	return ""
}

func (p *Plugin) releaseTerminalDocProjection(termPanel bool) {
	if p.terminalDocProjection.buffer != nil && p.terminalDocProjection.termPanel == termPanel {
		p.terminalDocProjection = terminalDocProjection{}
	}
}

func (p *Plugin) handleInteractiveSelectionDrag(action mouse.MouseAction) tea.Cmd {
	// Freeze before anything reads or moves the window: a gesture reads the rows
	// it was armed on, whatever output arrives underneath them.
	p.freezeTerminalSelectionViewport()
	// The tick re-reads this position, so a pointer held still past an edge keeps
	// scrolling after motion events stop arriving. Real motion also restarts the
	// hold budget that bounds a chain running on a lost release.
	p.pointer.NoteDragMotion(action.X, action.Y)
	// Scroll first when the pointer has run past an edge, so a selection can
	// reach content that is not on screen.
	p.scrollTerminalSelectionViewport(
		tty.EdgeScrollDelta(p.terminalSelectionGeometry(), action.Y, tty.DragScrollStep))
	if !p.extendSelectionDragTo(action.X, action.Y) {
		return nil
	}
	if p.selectionAutoScrollDelta(action.Y) == 0 {
		return nil
	}
	return p.scheduleSelectionAutoScroll()
}

// freezeTerminalSelectionViewport pins the surface being dragged to the window
// the user can currently see. The panel is normally pinned earlier, when its
// selection source is prepared; freezing again here is the shared rule's no-op,
// so a gesture that outlived an intervening thaw still holds its own rows.
func (p *Plugin) freezeTerminalSelectionViewport() {
	layout := p.terminalSelectionViewportLayout()
	if p.selectionTermPanel {
		p.pinTermPanelWindow(layout.Start, false)
		return
	}
	// Following the live grid can place the window past MaxOffset — the pane's
	// own top, below the last row an offset can name. An offset beyond it is
	// clamped by the next render, so pinning it unclamped freezes the window
	// somewhere it will not be drawn.
	p.pinPreviewWindow(min(max(layout.Start, 0), max(layout.MaxOffset, 0)), false)
}

// thawTerminalSelectionViewport is the other half of the freeze, owed at the end
// of every gesture that took one. Both halves are the shared rule's; which
// surface they apply to is all this call site decides.
func (p *Plugin) thawTerminalSelectionViewport() {
	if p.selectionTermPanel {
		return
	}
	p.thawPreviewGesturePin()
}

// anchorDragFromOrigin starts a selection for a drag whose mouse-down landed off
// the text — on the header, on the padding below the last row, in the border.
func (p *Plugin) anchorDragFromOrigin(action mouse.MouseAction) bool {
	return p.pointer.AnchorFrom(
		p.terminalSelectionGeometry(), p.interactiveOutputBuffer(), &p.selection,
		action.X-action.DragDX, action.Y-action.DragDY, action.Alt)
}

func (p *Plugin) finishInteractiveSelection() tea.Cmd {
	// The gesture is over, so the window goes back to following output from
	// wherever it was pinned.
	p.thawTerminalSelectionViewport()
	resolution, selected := p.pointer.Release(&p.selection)
	if selected {
		if p.copyOnSelectEnabled() {
			return p.copyInteractiveSelectionCmd()
		}
		return nil
	}
	// A click that selected nothing leaves no reader holding the panel's rows, so
	// the pin the gesture was armed with ends with the gesture — handed back as a
	// distance from the live bottom, the same half the primary surface just paid
	// above, so a gesture that scrolled the window keeps the rows it scrolled to.
	p.thawTermPanelGesturePin()

	switch resolution {
	case tty.ClickActivate:
		if p.selectionTermPanel {
			return p.enterTermPanelInteractiveMode()
		}
		return p.enterInteractiveMode()
	case tty.ClickForward:
		// The press position, not the release: a click that resolves here never
		// moved, and forwardClickToTmux sends press and release together. The
		// component polls for the frame its own send provokes, so nothing is
		// scheduled alongside it: two owners of that poll is two captures of
		// every forwarded click.
		pressX, pressY := p.pointer.PressPoint()
		return p.forwardClickToTmux(pressX, pressY)
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
	return tty.SelectedLines(p.interactiveOutputBuffer(), &p.selection, tabStopWidth)
}

// terminalSelectionViewportLayout is the window a pointer gesture maps against:
// the one the render path draws, built from the same input rather than a second
// construction of it (td-73fa86).
func (p *Plugin) terminalSelectionViewportLayout() terminalViewportLayout {
	return p.terminalViewportLayoutFor(p.effectiveSelectionTermPanel())
}

// terminalViewportLayoutFor is that window for a named surface, so a caller that
// means the primary terminal — the freeze's bound, for one — gets it whichever
// surface a selection currently sits on.
func (p *Plugin) terminalViewportLayoutFor(termPanel bool) terminalViewportLayout {
	return calculateTerminalViewportLayout(p.terminalWindowInputFor(termPanel))
}

// terminalWindowInputFor is the render path's input for a named surface, so a
// caller that wants the drawn window's geometry — its bound, for one — builds it
// once rather than reconstructing the viewport size beside it.
func (p *Plugin) terminalWindowInputFor(termPanel bool) terminalViewportInput {
	// One derivation of the surface's viewport size, shared with the render and
	// cursor paths. The fallback covers the two cases the surface cannot place:
	// an unsized plugin, and the term panel asked for while hidden.
	width, height := p.calculatePreviewDimensions()
	if surface := p.terminalSurfaceGeometry(termPanel); surface.OK {
		width, height = surface.Width, surface.Height
	}
	// A nil buffer is fine: the layout's geometry (the fit, the display size)
	// comes from the viewport and the pane, and hit testing needs it whether or
	// not any output has been captured yet.
	return p.terminalWindowInput(termPanel, p.terminalOutputBuffer(termPanel), width, height)
}

func (p *Plugin) selectTerminalWord(action mouse.MouseAction) tea.Cmd {
	return p.selectTerminalUnit(action, tty.SelectUnitWord)
}

func (p *Plugin) selectTerminalLine(action mouse.MouseAction) tea.Cmd {
	return p.selectTerminalUnit(action, tty.SelectUnitLine)
}

// selectTerminalUnit installs the word or line under the pointer as the
// gesture's anchor unit, so the button still held extends by that unit.
func (p *Plugin) selectTerminalUnit(action mouse.MouseAction, unit tty.SelectionUnit) tea.Cmd {
	if action.Region == nil {
		return nil
	}
	p.prepareTerminalSelectionSource(action.Region.ID == regionTermPanelContent)
	p.pointer.AdoptSurface(&p.selection, action.Region.Rect)
	if !p.pointer.SelectUnitAt(
		p.terminalSelectionGeometry(), p.interactiveOutputBuffer(), &p.selection,
		action.X, action.Y, unit) {
		return nil
	}
	// Arm drag tracking exactly as a plain mouse-down does, so the button still
	// held after a double or triple click keeps delivering motion to this gesture
	// and its release arrives as a drag end rather than a fresh click.
	p.mouseHandler.StartDrag(action.X, action.Y, action.Region.ID, 0)
	if p.copyOnSelectEnabled() {
		return p.copyInteractiveSelectionCmd()
	}
	return nil
}

// clearTerminalSelection drops a selection made outside a pointer gesture —
// scrolling away from it, leaving interactive mode, opening a link. The unit
// goes with it: a word span left over from an old double-click would otherwise
// redefine where the next shift-click extends from.
func (p *Plugin) clearTerminalSelection() {
	p.selection.Clear()
	p.pointer.ResetUnit()
	// The panel window a gesture pinned was holding those rows still for this
	// selection; nothing is reading them now, so it goes back to following output
	// from where it stands rather than snapping to the offset behind the pin.
	p.thawTermPanelGesturePin()
}

// clearTerminalSelectionOnScroll is what every scroll made outside a pointer
// gesture — a wheel notch, a shift-scrollback key — does to the selection. The
// rule is the shared layer's so that scrolling away from a highlight and back
// answers the same way on every terminal surface.
func (p *Plugin) clearTerminalSelectionOnScroll(termPanel bool) {
	if tty.ScrollKeepsSelection(p.terminalOutputBuffer(termPanel)) {
		return
	}
	p.clearTerminalSelection()
}

func (p *Plugin) terminalPointAndLine(action mouse.MouseAction) (ui.SelectionPoint, string, bool) {
	if action.Region == nil {
		return ui.SelectionPoint{}, "", false
	}
	p.prepareTerminalSelectionSource(action.Region.ID == regionTermPanelContent)
	p.pointer.AdoptSurface(&p.selection, action.Region.Rect)
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
	return tty.LineTextAt(p.interactiveOutputBuffer(), lineIdx)
}

func (p *Plugin) selectAllTerminalOutput(termPanel bool) {
	// Select-all is its own anchor, at character granularity: a word span from an
	// earlier double-click must not survive to redefine the next shift-click.
	p.pointer.ResetUnit()
	p.prepareTerminalSelectionSource(termPanel)
	start, end, ok := tty.SelectAllSpan(p.interactiveOutputBuffer(), tabStopWidth)
	if !ok {
		return
	}
	p.selection.SelectRange(start, end, false)
}

func (p *Plugin) copyOnSelectEnabled() bool {
	return p.terminalConfig().CopyOnSelect
}

// copyInteractiveSelectionCmd writes the selection to the clipboard and says what
// happened. Both the rule and the wording are the shared layer's; only the
// notification type is this surface's.
func (p *Plugin) copyInteractiveSelectionCmd() tea.Cmd {
	return p.terminalConfig().CopySelectionCmd(p.interactiveSelectionLines(), func(notice tty.CopyNotice) tea.Msg {
		return app.ToastMsg{
			Message: notice.Message, Duration: notice.Duration, IsError: notice.IsError,
		}
	})
}
