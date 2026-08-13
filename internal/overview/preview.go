package overview

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	appmsg "github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/termpreview"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// The global Workspaces preview is one terminal box. Not a pane tree, not a
// docview, not a workspace plugin instance per project: the plan is explicit
// that rendering pane-tree layouts globally is deferred, and that a project
// whose own preview has a document open still previews here as its selected
// pane's captured output alone.
//
// The box has two states, and they differ only in where the output comes from.
// Watching is deliberately cheap: exactly one capture in flight at a time — for
// the selected pane and nothing else — started by a selection change and
// repeated only while the tab is visible. No control-mode client, no watcher,
// and none of them per project. A capture that arrives for a superseded
// generation or a pane the cursor has already left is dropped. Typing
// (internal/overview/interactive.go) hands the same pane to internal/tty's
// embedded terminal, which owns the live feed for as long as the user is in it;
// the capture cadence stands down meanwhile so one pane never has two readers.
//
// Both states put their output in the same kind of buffer and draw it through
// the same window, which is why selection, the wheel and copy behave identically
// in each. Nothing captured is persisted: the buffer lives in memory for as long
// as the tab is on screen and is released when it is not, so a pane's contents
// never reach config, state, or diagnostics.

const (
	// previewCaptureLines is the scrollback each capture asks tmux for. It is
	// bounded on purpose: the preview is for recognising a workspace, not for
	// reading its history, and the owning project's Workspaces plugin is one
	// Enter away when the full buffer is what the user wants.
	previewCaptureLines = 200

	// previewFocusedPoll / previewVisiblePoll are the adaptive cadences. A
	// focused preview is what the user is reading, so it refreshes fastest; a
	// visible-but-unfocused one tracks the board's own live cadence. Anything
	// hidden, or with no live pane behind it, does not poll at all.
	previewFocusedPoll = 2 * time.Second
	previewVisiblePoll = livePollEvery

	// The default share and the floors below it match the project plugin's outer
	// split: neither pane may be squeezed into an unreadable column.
	defaultWorkspaceSidebarPercent = 40
	globalListMinWidth             = 28
	globalPreviewMinWidth          = 44
	globalDividerWidth             = 1
	globalPanelOverhead            = 4 // left/right border plus one column of padding
	globalContentInset             = 2 // left border plus left padding
)

// previewFocus is which of the two panes owns the keyboard.
type previewFocus uint8

const (
	focusList previewFocus = iota
	focusPreview
)

// previewMsg is one completed capture, tagged with everything needed to reject
// it: the generation it was started under and the exact workspace and pane it
// was started for.
type previewMsg struct {
	Generation  int
	WorkspaceID string
	PaneID      string
	Output      string
	Err         error
	At          time.Time
}

// previewPollMsg is the adaptive refresh tick for an unchanged live pane.
type previewPollMsg struct {
	Generation  int
	WorkspaceID string
}

// previewState is the whole of the global preview: which item it is showing,
// what was captured, how far back the reader has scrolled, and whether anyone
// is looking at it.
type previewState struct {
	visible bool
	focus   previewFocus
	// full is the narrow layout's state: at widths that cannot sustain two
	// useful panes the list is full width, and the preview replaces it rather
	// than sharing it.
	full bool

	// generation supersedes in-flight captures. Every selection change, refresh,
	// and visibility change bumps it, so a slow capture cannot paint over a pane
	// the user has already moved off.
	generation  int
	workspaceID string
	capture     previewCapture
	reason      string
	// offset is rows scrolled back from the live bottom. Zero follows output.
	offset int
	// freeze holds the window still for the duration of a pointer gesture. The
	// rule, and the offset the window resumes following from, are the shared
	// layer's — the project surface freezes its own panes by the same one.
	freeze      tty.WindowFreeze
	scheduled   bool
	requestedAt time.Time

	// buffer is the captured output while the pane is being watched. It is the
	// same kind of buffer the live terminal keeps, so selection, the wheel and
	// copy work identically in both states rather than only while typing.
	buffer *tty.OutputBuffer

	// selection, pointer and wheel are the shared interaction layer's state: what
	// is selected, what the gesture in flight will mean, and how much of a flick
	// the surface has taken.
	selection ui.SelectionState
	pointer   tty.Pointer
	wheel     tty.WheelBurst

	// scrollbackLimitShown records that the reader has been told once where the
	// rest of this pane's history is.
	scrollbackLimitShown bool

	// terminal is the embedded terminal the preview hands the keyboard to. It is
	// built on first use and reused afterwards, so a browser nobody has typed in
	// still costs nothing.
	terminal             previewTerminal
	interactiveHintShown bool

	metrics PreviewMetrics
}

// previewCapture is the identity of the last capture the box accepted. The
// contents live in the buffer beside it; this is what late captures are rejected
// against and what the header hints are phrased from.
type previewCapture struct {
	PaneID     string
	CapturedAt time.Time
	Err        error
}

// PreviewMetrics is what the cadence was tuned against: how many captures the
// preview actually ran, how many late ones it threw away, and how long the
// newest selection waited for its first frame. It is read by tests and by the
// trace log; it is not persisted.
type PreviewMetrics struct {
	Captures    int
	Rejected    int
	Polls       int
	LastLatency time.Duration
}

// PreviewMetrics returns a copy of the preview's work counters.
func (m *Model) PreviewMetrics() PreviewMetrics { return m.preview.metrics }

// WorkspacesPreviewVisible reports whether the preview believes anyone is
// looking at it. It exists so the app can prove that scope and tab changes
// actually reach the thing that decides whether to capture a pane.
func (m *Model) WorkspacesPreviewVisible() bool { return m.preview.visible }

func (m *Model) now() time.Time {
	if m.collector.Now != nil {
		return m.collector.Now()
	}
	return time.Now()
}

// SetWorkspacesVisible tells the preview whether anyone is looking at it. It is
// the switch behind "polls only while the global Workspaces tab is visible":
// becoming visible captures the selected pane immediately, and becoming hidden
// cancels the in-flight capture, stops the poll, and drops the captured output.
func (m *Model) SetWorkspacesVisible(visible bool) tea.Cmd {
	if m.preview.visible == visible {
		return nil
	}
	m.preview.visible = visible
	// Moving between the catalog's two projections supersedes any activation
	// still being validated. Agents and Workspaces share one cache, one
	// generation, and one poll, so nothing else here marks the moment the user
	// left the view they pressed enter on — and a destination that opens itself
	// after the user moved on is exactly the surprise the generation/request
	// machinery exists to prevent.
	m.requestID++
	if !visible {
		m.releasePreview()
		return nil
	}
	return m.previewSelect()
}

// releasePreview cancels whatever the preview was doing and forgets what it
// captured. Captured terminal contents are memory-only, and a tab nobody is
// looking at has no reason to keep holding them — including the live terminal's
// buffer and its control-mode subscription.
func (m *Model) releasePreview() {
	if m.preview.terminal != nil {
		m.preview.terminal.ReleaseInput()
	}
	m.preview.generation++
	m.preview.workspaceID = ""
	m.resetPreviewContent()
	m.preview.reason = ""
	m.preview.scheduled = false
}

// resetPreviewContent drops the captured output and everything anchored to it.
// A selection names buffer lines, so it cannot outlive the buffer it named.
func (m *Model) resetPreviewContent() {
	m.preview.capture = previewCapture{}
	m.preview.buffer = nil
	m.preview.offset = 0
	m.preview.freeze = tty.WindowFreeze{}
	m.preview.selection.Clear()
	m.preview.pointer.Abandon()
	m.preview.pointer.ResetUnit()
}

// previewSync starts a capture when the selection has moved to a different
// item. It is called after every interaction and after every inventory
// increment, so the preview follows the cursor without the list needing to know
// that a preview exists.
func (m *Model) previewSync() tea.Cmd {
	if !m.preview.visible {
		return nil
	}
	selected, ok := m.workspaces.Selected()
	if !ok {
		if m.preview.workspaceID != "" {
			m.releasePreview()
		}
		return nil
	}
	if selected.ID == m.preview.workspaceID {
		return nil
	}
	return m.previewSelect()
}

// previewSelect binds the preview to the current selection and captures it
// straight away. Every caller is a path the user can feel — a selection change,
// the tab becoming visible, an explicit refresh — so none of them waits out a
// poll interval before showing anything.
func (m *Model) previewSelect() tea.Cmd { return m.bindPreview(false) }

// bindPreview binds the preview to the current selection and captures it.
//
// keepContent asks for what is already drawn to stay on screen until the
// replacement capture arrives, and is honoured only while the selection is still
// the item that content came from. Dropping the buffer first leaves a pane that
// plainly has output reading "no output captured" for a round trip and forgets
// where the window was scrolled to — the project surface keeps its loaded
// scrollback across the same handover.
func (m *Model) bindPreview(keepContent bool) tea.Cmd {
	workspace, selected := m.SelectedWorkspace()
	keep := keepContent && selected && workspace.ID == m.preview.workspaceID

	// Binding the preview to a selection ends any live terminal: it is attached
	// to the pane the user was typing in, and it must not keep the keyboard —
	// or its control-mode subscription — while the browser shows another item.
	if m.preview.terminal != nil {
		m.preview.terminal.ReleaseInput()
	}
	m.preview.generation++
	m.preview.scheduled = false
	if keep {
		// The selection was made over the terminal's own buffer, which the
		// handover replaces, so it cannot survive it even though the rows do.
		m.clearPreviewSelection()
	} else {
		m.resetPreviewContent()
	}
	m.preview.reason = ""

	if !selected {
		m.preview.workspaceID = ""
		return nil
	}
	m.preview.workspaceID = workspace.ID

	// An item with no single live pane behind it is not captured at all. There
	// is nothing to read, and guessing among several panes is exactly what the
	// catalog refuses to do — and there is no replacement coming for kept
	// content to wait for.
	if reason, unavailable := previewUnavailable(workspace); unavailable {
		m.preview.reason = reason
		m.resetPreviewContent()
		return nil
	}
	return m.capturePreviewCmd(workspace)
}

// previewUnavailable explains, in the user's terms, why an item has no live
// preview. The empty reason means it does.
func previewUnavailable(workspace workspaceinventory.Workspace) (string, bool) {
	switch {
	case workspace.Ambiguous:
		return "Several panes match this workspace — refusing to guess which one is yours", true
	case workspace.PaneID == "":
		return "No live session for this workspace", true
	case !workspace.Live:
		return "The session for this workspace has ended", true
	}
	return "", false
}

// capturePreviewCmd captures exactly one pane: the selected one. The capture
// function is the collector's, so a test injects it in one place and the
// browser never opens a second way to talk to tmux.
func (m *Model) capturePreviewCmd(workspace workspaceinventory.Workspace) tea.Cmd {
	capture := m.collector.Capture
	if capture == nil {
		return nil
	}
	generation, id, pane := m.preview.generation, workspace.ID, workspace.PaneID
	m.preview.requestedAt = m.now()
	m.preview.metrics.Captures++
	m.tracef("preview generation=%d capture workspace=%s pane=%s", generation, id, pane)
	return func() tea.Msg {
		output, err := capture(pane, previewCaptureLines)
		return previewMsg{Generation: generation, WorkspaceID: id, PaneID: pane, Output: output, Err: err, At: time.Now()}
	}
}

// applyPreview accepts a capture if it is still the one the surface asked for.
func (m *Model) applyPreview(msg previewMsg) tea.Cmd {
	if msg.Generation != m.preview.generation || msg.WorkspaceID != m.preview.workspaceID {
		m.preview.metrics.Rejected++
		m.tracef("preview generation=%d drained stale_generation=%d workspace=%s", m.preview.generation, msg.Generation, msg.WorkspaceID)
		return nil
	}
	m.preview.metrics.LastLatency = m.now().Sub(m.preview.requestedAt)
	if msg.Err != nil {
		m.preview.capture = previewCapture{PaneID: msg.PaneID, Err: msg.Err, CapturedAt: msg.At}
		m.preview.reason = "Could not read this pane: " + msg.Err.Error()
		return m.schedulePreviewPoll()
	}
	m.preview.reason = ""
	m.preview.capture = previewCapture{PaneID: msg.PaneID, CapturedAt: msg.At}
	if m.preview.buffer == nil {
		m.preview.buffer = tty.NewOutputBuffer(previewCaptureLines)
	}
	// The same call the live terminal makes with its own captures, so a watched
	// pane and a driven one are one kind of content behind one kind of window.
	m.preview.buffer.ApplySnapshot(tty.PaneSnapshot{Output: msg.Output})
	return m.schedulePreviewPoll()
}

// schedulePreviewPoll arms the next refresh, or nothing at all. This is the
// whole of the adaptive cadence: hidden previews and previews with no live pane
// behind them schedule no work, and a visible one refreshes faster while it has
// focus than while it is merely on screen.
func (m *Model) schedulePreviewPoll() tea.Cmd {
	interval := m.previewInterval()
	if interval == 0 || m.preview.scheduled {
		return nil
	}
	generation, id := m.preview.generation, m.preview.workspaceID
	m.preview.scheduled = true
	return tea.Tick(interval, func(time.Time) tea.Msg {
		return previewPollMsg{Generation: generation, WorkspaceID: id}
	})
}

// previewInterval is the cadence the preview is currently owed, and zero for
// "do no work at all": hidden, nothing selected, or an item with no live pane
// behind it.
func (m *Model) previewInterval() time.Duration {
	if !m.preview.visible || m.preview.workspaceID == "" || m.preview.reason != "" {
		return 0
	}
	// A live terminal is already following this pane. Capturing it as well would
	// give one pane two readers and paint the terminal's own frames over with
	// staler ones.
	if m.PreviewInteractive() {
		return 0
	}
	if m.preview.focus == focusPreview {
		return previewFocusedPoll
	}
	return previewVisiblePoll
}

// pollPreview re-captures an unchanged selection.
func (m *Model) pollPreview(msg previewPollMsg) tea.Cmd {
	if msg.Generation != m.preview.generation || msg.WorkspaceID != m.preview.workspaceID {
		m.preview.metrics.Rejected++
		return nil
	}
	m.preview.scheduled = false
	if !m.preview.visible {
		return nil
	}
	workspace, ok := m.SelectedWorkspace()
	if !ok {
		return nil
	}
	if reason, unavailable := previewUnavailable(workspace); unavailable {
		m.preview.reason = reason
		m.resetPreviewContent()
		return nil
	}
	m.preview.metrics.Polls++
	return m.capturePreviewCmd(workspace)
}

// PreviewFocused reports that the preview owns the keyboard.
func (m *Model) PreviewFocused() bool { return m.preview.focus == focusPreview }

// previewKey handles a key while the preview has focus.
//
// While the pane is live every key belongs to it, ctrl+c included: the project
// plugin forwards it so a user can interrupt what is running in the pane
// (internal/app's workspace-interactive branch), and an embedded terminal that
// could not send SIGINT would be the odd one out. The ways out are the terminal
// component's own — the exit key or a double escape, both answered inside it —
// and quitting sidecar is one exit key away.
//
// While the pane is merely being watched, these keys scroll it, move focus, or
// ask for the keyboard.
func (m *Model) previewKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	key := msg.String()
	if m.PreviewInteractive() {
		// Every live key goes to the component, exit chords and this surface's own
		// chords included: it consults them through OnKey before anything becomes
		// input, and a surface that answered them out here as well would send them
		// to the pane twice.
		return true, m.forwardToTerminal(msg)
	}
	// The same acts on the terminal surface the live pane routes through OnKey.
	// The selection they act on exists in both states, so a watched pane answers
	// them too; everything after this is the browser's own navigation.
	if handled, cmd := m.terminalKey(msg); handled {
		return true, cmd
	}
	page := max(1, m.previewRows()/2)
	switch key {
	case interactiveEnterKey, interactiveEnterKeyAlt:
		return true, m.enterPreviewInteractive()
	case "left", "h", "esc":
		return true, m.focusList()
	case "j", "down":
		m.scrollWatchedPreview(-1)
	case "k", "up":
		m.scrollWatchedPreview(1)
	case "ctrl+d", "pgdown":
		m.scrollWatchedPreview(-page)
	case "ctrl+u", "pgup":
		m.scrollWatchedPreview(page)
	case "G", "end":
		m.clearPreviewSelectionOnScroll()
		m.jumpPreviewWindow(0)
	case "g", "home":
		m.clearPreviewSelectionOnScroll()
		m.jumpPreviewWindow(m.previewMaxOffset())
	default:
		return false, nil
	}
	return true, nil
}

// scrollWatchedPreview moves the window with the keyboard, which is a scroll
// made outside a pointer gesture: a selection anchored to the rows it leaves
// behind would highlight rows the user never picked.
func (m *Model) scrollWatchedPreview(delta int) {
	m.clearPreviewSelectionOnScroll()
	m.scrollPreview(delta)
}

// terminalKey answers the chords that act on the terminal surface rather than on
// the browser around it. They are answered in both of the preview's states,
// because the selection they act on exists in both.
func (m *Model) terminalKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	// The set and the order are the shared layer's. This surface has no chords
	// of its own to answer ahead of them: the search and the panel toggles the
	// project surface answers first belong to a panel this one does not draw.
	cmd, handled := m.TerminalConfig().ResolveSurfaceChord(msg, tty.SurfaceChords{
		Copy:      m.copyPreviewSelectionCmd,
		SelectAll: func() tea.Cmd { m.selectAllPreviewOutput(); return nil },
		Scrollback: func(key tea.KeyPressMsg) (tea.Cmd, bool) {
			handled, cmd := m.previewScrollbackKey(key)
			return cmd, handled
		},
	})
	return handled, cmd
}

// previewScrollbackKey walks the window through scrollback while a pane is live.
// Shift is the modifier because every unshifted key is the pane's.
func (m *Model) previewScrollbackKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	if !m.PreviewInteractive() {
		return false, nil
	}
	// Every key typed into a live pane comes through here, so the window's height
	// is resolved only for the keys the shared rule claims.
	if !tty.IsScrollbackKey(msg) {
		return false, nil
	}
	move, ok := tty.MapScrollbackKey(msg, m.previewRows())
	if !ok {
		return false, nil
	}
	m.clearPreviewSelectionOnScroll()
	switch {
	case move.ToOldest:
		m.jumpPreviewWindow(m.previewMaxOffset())
		return true, m.notePreviewScrollbackLimit()
	case move.ToLive:
		m.jumpPreviewWindow(0)
	default:
		before := m.previewScrollAnchor()
		m.scrollPreview(move.Rows)
		if move.Rows > 0 && m.previewScrollAnchor() == before {
			return true, m.notePreviewScrollbackLimit()
		}
	}
	return true, nil
}

// focusPreviewPane moves focus right. On a narrow tab there is no room for two
// panes, so the preview takes the whole width instead of sharing it.
//
// Focusing captures only when there is nothing on screen yet. The faster
// focused cadence takes effect at the next tick rather than by cancelling the
// one already in flight: moving focus back and forth must not be a way to run
// captures faster than the cadence allows.
func (m *Model) focusPreviewPane() tea.Cmd {
	m.preview.focus = focusPreview
	if m.previewNarrow() {
		m.preview.full = true
	}
	if m.preview.reason == "" && m.previewBuffer() == nil {
		return m.previewSelect()
	}
	return nil
}

// focusList moves focus back to the list. Leaving the preview leaves any live
// terminal with it: the keyboard cannot be in two places, and a pane that kept
// its subscription while the user browsed elsewhere would be a reader nobody is
// reading.
func (m *Model) focusList() tea.Cmd {
	cmd := m.exitPreviewInteractive()
	m.preview.focus = focusList
	m.preview.full = false
	// Focus cannot rest on something nobody draws: with the sidebar hidden the
	// layout is preview-only, so the list comes back with the keyboard.
	m.sidebarVisible = true
	return cmd
}

// scrollPreview moves the window delta rows back through scrollback, negative
// towards the live edge.
func (m *Model) scrollPreview(delta int) {
	if delta == 0 {
		return
	}
	maxOffset := m.previewMaxOffset()
	if m.preview.freeze.Active() {
		m.preview.freeze.Scroll(delta, maxOffset)
		return
	}
	m.preview.offset = min(max(m.preview.offset+delta, 0), maxOffset)
}

// previewScrollAnchor is where the window sits, in whichever coordinate it is
// currently placed by. Callers use it to tell whether a scroll moved anything.
func (m *Model) previewScrollAnchor() int {
	if m.preview.freeze.Active() {
		return m.preview.freeze.Start()
	}
	return m.preview.offset
}

// freezePreviewWindow pins the window to the rows the user can see, before a
// gesture reads or moves it.
func (m *Model) freezePreviewWindow() {
	m.preview.freeze.Freeze(m.previewWindow().layout.Start)
}

// thawPreviewWindow places the window back against the live bottom, where it
// follows new output again, without moving the rows on screen.
func (m *Model) thawPreviewWindow() {
	if offset, thawed := m.preview.freeze.Thaw(m.previewWindow().layout); thawed {
		m.preview.offset = offset
	}
}

// jumpPreviewWindow places the window at an explicit distance back from the
// live bottom, which ends any freeze: a jump is not a gesture reading the rows
// it lands on.
func (m *Model) jumpPreviewWindow(offset int) {
	m.preview.freeze.Release()
	m.preview.offset = offset
}

// scrollPreviewRows moves the window by delta rendered rows, positive downwards,
// which is the direction the shared edge-scroll rule reports.
func (m *Model) scrollPreviewRows(delta int) { m.scrollPreview(-delta) }

// pinPreviewToLive returns the window to the live edge, dropping a selection
// anchored to rows the jump leaves behind. It is what an application taking the
// wheel owes the viewport: while it owns what the pane shows, a window left
// scrolled back would sit frozen over stale rows as the app repainted below it.
func (m *Model) pinPreviewToLive() {
	if m.preview.offset == 0 && !m.preview.freeze.Active() {
		return
	}
	m.clearPreviewSelection()
	m.jumpPreviewWindow(0)
}

func (m *Model) previewMaxOffset() int { return m.previewWindow().layout.MaxOffset }

// previewRows is the body height of the preview box at the last rendered size.
func (m *Model) previewRows() int {
	if window := m.previewWindow(); window.ok {
		return max(window.layout.DisplayHeight, 1)
	}
	return max(1, m.height-termpreview.HeaderRows)
}

// previewBuffer is the captured output the box is drawing: the live terminal's
// own buffer while the user is typing into it, and the browser's capture buffer
// while it is being watched. One kind of content behind one kind of window is
// what lets selection, the wheel and copy behave the same in both states.
func (m *Model) previewBuffer() *tty.OutputBuffer {
	if m.PreviewInteractive() {
		return m.preview.terminal.Buffer()
	}
	return m.preview.buffer
}

// previewWindow is the drawn window of the preview box: where it sits on screen,
// and which buffer lines land in it. Hit testing, scrolling, the native cursor
// and the renderer all read this one answer, so a click can never land on a
// different cell than the one the user aimed at.
type previewWindow struct {
	surface termpreview.Surface
	input   tty.ViewportInput
	layout  tty.Viewport
	ok      bool
}

func (m *Model) previewViewportInput(width, height int) tty.ViewportInput {
	buffer := m.previewBuffer()
	// Gestures record their points in the buffer's own coordinates, which are
	// absolute the moment a live pane has any scrollback. A window told nothing
	// about the base draws every highlight short by exactly it, so a selection
	// made while typing lands off screen even though the copied text is right.
	base, _ := tty.BufferBase(buffer)
	interactive := m.PreviewInteractive()
	input := tty.ViewportInput{
		Buffer:       buffer,
		AbsoluteBase: base,
		Width:        width,
		Height:       height,
		Scrollbar:    true,
		// Whether tmux's trailing blank rows are padding or the application's own
		// content is the shared rule, so the same pane cannot draw one way in this
		// tab and another in the project's.
		TrimTrailing: tty.TrimsTrailingRows(interactive),
	}
	if m.preview.freeze.Active() {
		input.Offset = m.preview.freeze.Start()
	} else {
		input.Offset, input.OffsetFromBottom = m.preview.offset, true
		input.Follow = m.preview.offset == 0
	}
	if interactive {
		input.Interactive = true
		input.PaneWidth, input.PaneHeight = m.preview.terminal.PaneSize()
		input.CursorRow, input.CursorCol, input.CursorVisible = m.preview.terminal.CursorState()
	}
	return input
}

func (m *Model) previewWindow() previewWindow {
	surface, ok := m.previewSurface()
	if !ok {
		return previewWindow{}
	}
	input := m.previewViewportInput(surface.Width, surface.Height)
	return previewWindow{surface: surface, input: input, layout: tty.FitViewport(input), ok: true}
}

// previewGeometry places the drawn content for the shared hit tests. The rect is
// the surface the layout named, so hit testing and pixels cannot disagree.
func (m *Model) previewGeometry() (tty.Geometry, bool) {
	window := m.previewWindow()
	if !window.ok {
		return tty.Geometry{}, false
	}
	return tty.GeometryFor(window.surface.X, window.surface.Y, window.layout, tty.DefaultTabWidth), true
}

// previewPaneCoords maps a screen position to the 1-indexed pane coordinates
// tmux's mouse protocol expects, and refuses everything that is not a live pane
// cell.
func (m *Model) previewPaneCoords(x, y int) (col, row int, ok bool) {
	if !m.PreviewInteractive() {
		return 0, 0, false
	}
	window := m.previewWindow()
	if !window.ok {
		return 0, 0, false
	}
	paneWidth, paneHeight := m.preview.terminal.PaneSize()
	if paneWidth <= 0 || paneHeight <= 0 {
		paneWidth, paneHeight = window.layout.DisplayWidth, window.layout.DisplayHeight
	}
	return tty.PaneCoordsAt(window.layout, x-window.surface.X, y-window.surface.Y, paneWidth, paneHeight)
}

// clearPreviewSelectionOnScroll is what every scroll made outside a pointer
// gesture — a wheel notch, a shift-scrollback key, a jump to either end — does
// to the selection. The rule is the shared layer's so that scrolling away from
// a highlight and back answers the same way on every terminal surface.
func (m *Model) clearPreviewSelectionOnScroll() {
	if tty.ScrollKeepsSelection(m.previewBuffer()) {
		return
	}
	m.clearPreviewSelection()
}

// clearPreviewSelection drops a selection made outside a pointer gesture —
// scrolling away from it, leaving a pane, pinning back to live. The anchor unit
// goes with it: a word span left over from an old double-click would otherwise
// redefine where the next shift-click extends from.
func (m *Model) clearPreviewSelection() {
	m.preview.selection.Clear()
	m.preview.pointer.ResetUnit()
}

// selectAllPreviewOutput selects every line the buffer holds, at character
// granularity so an earlier word gesture cannot redefine the next shift-click.
func (m *Model) selectAllPreviewOutput() {
	m.preview.pointer.ResetUnit()
	start, end, ok := tty.SelectAllSpan(m.previewBuffer(), tty.DefaultTabWidth)
	if !ok {
		return
	}
	m.preview.selection.SelectRange(start, end, false)
}

// previewSelectionLines is the text the selection covers.
func (m *Model) previewSelectionLines() []string {
	return tty.SelectedLines(m.previewBuffer(), &m.preview.selection, tty.DefaultTabWidth)
}

// copyPreviewSelectionCmd writes the selection to the clipboard and says what
// happened. Both the rule and the wording are the shared layer's; only the
// notification type is this surface's.
func (m *Model) copyPreviewSelectionCmd() tea.Cmd {
	return m.TerminalConfig().CopySelectionCmd(m.previewSelectionLines(), func(notice tty.CopyNotice) tea.Msg {
		return appmsg.ToastMsg{
			Message: notice.Message, Duration: notice.Duration, IsError: notice.IsError,
		}
	})
}

// previewNarrow reports a tab too narrow to sustain two useful panes.
func (m *Model) previewNarrow() bool {
	return m.width < globalListMinWidth+globalDividerWidth+globalPreviewMinWidth
}

// previewSplit is the outer two-pane split, taken from the same shared layer
// the project plugin's sidebar/preview split is taken from.
func (m *Model) previewSplit(width int) termpreview.Split {
	return termpreview.SplitFor(width, termpreview.SplitConfig{
		SidebarVisible: true,
		SidebarPercent: m.sidebarWidth,
		DividerWidth:   globalDividerWidth,
		PanelOverhead:  globalPanelOverhead,
		ContentInset:   globalContentInset,
		SidebarMin:     globalListMinWidth,
		PreviewMin:     globalPreviewMinWidth,
	})
}

// renderPreview draws the terminal box: the selected item's identity on the
// header row, then a window of the pane's captured output — or the reason there
// is none. Watching and typing draw the same way, from the same kind of buffer
// through the same window, so a selection, a scrolled-back window and a
// highlighted word look and behave the same in either state.
//
// The size is the layout's own box, which is the box previewWindow places its
// surface in; hit testing therefore maps onto the rows drawn here.
func (m *Model) renderPreview(width, height int) string {
	if width < 1 || height < 1 {
		return ""
	}
	workspace, ok := m.SelectedWorkspace()
	if !ok {
		return termpreview.RenderBuffer(termpreview.RenderBufferInput{
			Width: width, Height: height, Message: "No workspace selected",
		})
	}

	chips := []string{previewChip(workspace.Name, m.PreviewFocused())}
	if workspace.ProjectName != "" {
		chips = append(chips, styles.Muted.Render(workspace.ProjectName))
	}

	hints := m.interactiveHints()
	if !m.PreviewInteractive() {
		hints = previewHints(workspace, m.PreviewFocused())
	}
	message := m.preview.reason
	if message != "" {
		message += "\n\n" + previewMetadata(workspace)
	}

	input := m.previewViewportInput(width, height-termpreview.HeaderRows)
	_, total := tty.BufferBase(input.Buffer)
	layout := tty.FitViewport(input)
	hints = m.appendWindowStatus(styles.Muted.Render(hints), input, layout, width, chips)
	return termpreview.RenderBuffer(termpreview.RenderBufferInput{
		Width:  width,
		Height: height,
		Chips:  chips,
		Hints:  hints,
		Layout: layout,
		Buffer: input.Buffer,
		// The window and the highlight drawn in it must resolve a line the same
		// way, or a selection made over a pane with history is drawn rows away
		// from the text it covers.
		AbsoluteBase: input.AbsoluteBase,
		TotalItems:   total,
		// The live grid behind the window, from the same input the window was
		// fitted to: letterboxing and the pane's canvas background are the shared
		// renderer's to decide, not this surface's to guess at.
		PaneHeight:  input.PaneHeight,
		Interactive: input.Interactive,
		Follow:      input.Follow,
		Selection:   &m.preview.selection,
		TabWidth:    tty.DefaultTabWidth,
		Message:     message,
	})
}

// appendWindowStatus adds the shared facts about the drawn window to the
// header's right region: that it is off the live edge and how to get back, that
// older lines exist above it, that the pane is clipped, that the application has
// the mouse. The project surface states the same ones from the same derivation.
//
// The region here is narrower than the project's header, so notes are dropped by
// the shared width rule — least important first — rather than by this surface
// never having said them.
func (m *Model) appendWindowStatus(hints string, input tty.ViewportInput, layout tty.Viewport, width int, chips []string) string {
	// The columns left of the chips, which the header draws first and never
	// clips. A budget of one is a header with no room: a zero would read as
	// "unbudgeted" and let a note overrun the row the terminal is drawn under.
	budget := width - globalPanelOverhead
	for _, chip := range chips {
		budget -= ansi.StringWidth(chip) + 1
	}
	budget = max(budget, 1)
	notes := tty.WindowStatus(tty.WindowStatusInput{
		Layout:       layout,
		AbsoluteBase: input.AbsoluteBase,
		// The browser holds one bounded capture per pane and never extends it at
		// the top, so there is no older history to offer or to be loading.
		MouseReporting: m.PreviewInteractive() && m.preview.terminal.PaneMouseReporting(),
		PaneWidth:      input.PaneWidth,
		PaneHeight:     input.PaneHeight,
		LiveEdgeKey:    m.previewLiveEdgeKey(),
	})
	return tty.AppendStatus(hints, notes, budget, func(note string) string { return styles.Muted.Render(note) })
}

// previewLiveEdgeKey is the chord that puts this surface's window back on the
// live edge in the state it is in: every unshifted key belongs to a live pane,
// so the shifted one is named there and the plain one while merely watching.
func (m *Model) previewLiveEdgeKey() string {
	if m.PreviewInteractive() {
		return tty.LiveEdgeKey
	}
	return "end"
}

func previewChip(name string, focused bool) string {
	if name == "" {
		name = "Workspace"
	}
	if focused {
		return styles.Title.Render("▸ " + name)
	}
	return styles.Title.Render(name)
}

// previewHints is the header's right region while the pane is being watched:
// what the item is doing, and — when there is a live pane behind it and this
// surface can act on it — how to hand the keyboard over.
func previewHints(workspace workspaceinventory.Workspace, focused bool) string {
	parts := make([]string, 0, 2)
	if workspace.HasAgent() && workspace.Presentation.Label != "" {
		parts = append(parts, workspace.Presentation.Label)
	} else if workspace.Live {
		parts = append(parts, "live")
	}
	if _, unavailable := previewUnavailable(workspace); unavailable {
		parts = append(parts, "no live pane")
	} else if focused {
		// Only where it is true. With the list focused this key does nothing here,
		// and a hint for a key that does nothing is indistinguishable from a bug.
		parts = append(parts, interactiveEnterKey+" to type")
	}
	return strings.Join(parts, " · ")
}

// previewMetadata is what the pane shows instead of output: the identity a user
// needs to decide whether to open the item in its owning project.
func previewMetadata(workspace workspaceinventory.Workspace) string {
	lines := []string{"project  " + workspace.ProjectName}
	lines = append(lines, "kind     "+string(workspace.Kind))
	if workspace.Branch != "" {
		lines = append(lines, "branch   "+workspace.Branch)
	}
	if workspace.TaskID != "" {
		lines = append(lines, "task     "+workspace.TaskID)
	}
	if workspace.Provider != "" {
		lines = append(lines, "agent    "+workspace.Provider)
	}
	if workspace.Path != "" {
		lines = append(lines, "path     "+workspace.Path)
	}
	return strings.Join(lines, "\n")
}
