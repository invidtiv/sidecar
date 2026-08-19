package overview

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/livepanes"
	"github.com/marcus/sidecar/internal/livewatch"
	appmsg "github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/termpreview"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// The global Workspaces preview is one terminal box with the same shared pane
// placement rules the project workspace uses for clicked documents and issues.
// It is not a workspace plugin instance; its per-workspace layouts live only
// in this model for the lifetime of the Overview plugin.
//
// The selected visible pane is always produced by the same tty.Model component
// the project Workspaces plugin uses. Watching and typing are therefore two
// input-ownership states over one control-mode emulator, not two presentation
// pipelines. Inventory remains cheap and read-only: no terminal is opened for
// list rows, hidden tabs, or unselected panes.
//
// Nothing is written to disk. The bounded terminal buffer lives only while the
// selected Output preview is visible; secondary pane models stay cached in
// memory so cursoring to another shell and back preserves their layout and
// scroll positions. Leaving the tab or selecting a row with no live pane closes
// the control subscription and releases the terminal buffer.

const (
	// The initial bounded live/history window this surface captures. tty.Model
	// owns the seed, alt-screen split, and subsequent frames. It bounds the
	// capture, not how far back the reader can go: older ranges are read lazily
	// by the shared reach (preview_history.go), which ends where tmux's history
	// does.
	previewScrollbackLines = tty.DefaultScrollbackLines

	// The default share and the floors below it match the project plugin's outer
	// split: neither pane may be squeezed into an unreadable column.
	defaultWorkspaceSidebarPercent = 40
	globalListMinWidth             = 28
	globalPreviewMinWidth          = 44
	globalDividerWidth             = 1
	globalPanelOverhead            = 4 // left/right border plus one column of padding
	globalContentInset             = 2 // left border plus left padding
)

// Test barrier at the ownership revocation boundary. Nil in production.
var previewBeforeDeactivate func()

// previewFocus is which of the two panes owns the keyboard.
type previewFocus uint8

const (
	focusList previewFocus = iota
	focusPreview
)

// previewState is the whole of the global preview: which item it is showing,
// how far back the reader has scrolled, and whether anyone is looking at it.
type previewState struct {
	visible bool
	focus   previewFocus
	// full is the narrow layout's state: at widths that cannot sustain two
	// useful panes the list is full width, and the preview replaces it rather
	// than sharing it.
	full bool

	// generation scopes terminal activation to the selected row. contentEpoch
	// gives each newly opened secondary model a process-local identity so a
	// result from a closed model cannot land on a reopened one for the same path.
	generation   int
	contentEpoch uint64
	workspaceID  string
	reason       string
	// offset is rows scrolled back from the live bottom. Zero follows output.
	offset int
	// freeze holds the window still for the duration of a pointer gesture. The
	// rule, and the offset the window resumes following from, are the shared
	// layer's — the project surface freezes its own panes by the same one.
	freeze tty.WindowFreeze

	// buffer aliases terminal.Buffer while that model owns the visible target.
	// Keeping the alias makes the viewport independent of input ownership.
	buffer *tty.OutputBuffer

	// selection, pointer and wheel are the shared interaction layer's state: what
	// is selected, what the gesture in flight will mean, and how much of a flick
	// the surface has taken.
	selection ui.SelectionState
	pointer   tty.Pointer
	wheel     tty.WheelBurst

	// history is the surface's reach into the pane's older scrollback: the
	// shared layer's request state, adopted rather than restated, so this
	// surface reads exactly as far back as the project plugin's does.
	history tty.HistoryReach

	// terminal is the single producer for the selected visible pane. interactive
	// says whether keys are also routed to it; terminalTarget scopes its lifetime.
	terminal             previewTerminal
	terminalTarget       tty.Target
	interactive          bool
	interactiveHintShown bool

	// Memory-only secondary previews may sit beside the terminal. The shared
	// pane tree places document and issue leaves beside it, and the
	// cache keeps that live layout when the global cursor visits another row.
	doc             *previewDoc
	issue           *previewIssue
	diff            *previewDiff
	resource        *previewResource
	paneRoot        *panelayout.Node
	paneFocus       int
	paneNextID      int
	paneDragSplitID int
	paneCache       map[string]previewPaneCache
	// paneSizeCmds holds geometry a content asserted from inside a render, where
	// there is no runtime to dispatch it with. See paneHost.QueueSizeCmd.
	paneSizeCmds []tea.Cmd

	// Live refresh: one filesystem watcher per preview-pane kind, created when
	// the pane is on screen and released when it closes. The lifecycle is
	// livepanes'; what this surface owns is which previews are visible and how
	// each kind re-reads itself. See live_preview.go.
	live *livepanes.Set
	// Resolving either of these runs git or walks parents, so both are cached
	// per worktree rather than recomputed on each reconcile.
	tdStoreTargets     map[string][]livewatch.Target
	tdStoreResolving   map[string]bool
	diffAdminTargets   map[string][]livewatch.Target
	diffAdminResolving map[string]bool

	linkMemo previewLinkMemo
}

type previewLinkMemo struct {
	root     string
	buffer   *tty.OutputBuffer
	revision uint64
	specs    map[string]previewSpecResolution
	newSpecs int
}

type previewSpecResolution struct {
	value string
	ok    bool
}

type previewPaneCache struct {
	root     *panelayout.Node
	focus    int
	nextID   int
	doc      *previewDoc
	issue    *previewIssue
	diff     *previewDiff
	resource *previewResource
}

// WorkspacesPreviewVisible reports whether the preview believes anyone is
// looking at it. It exists so the app can prove that scope and tab changes
// actually reach terminal reconciliation.
func (m *Model) WorkspacesPreviewVisible() bool { return m.preview.visible }

// WorkspacesPreviewActive reports whether the visible-selection terminal owns a
// live target. It is diagnostic state for lifecycle tests, not another surface.
func (m *Model) WorkspacesPreviewActive() bool { return m.previewTerminalActive() }

func (m *Model) now() time.Time {
	if m.collector.Now != nil {
		return m.collector.Now()
	}
	return time.Now()
}

// SetWorkspacesVisible tells the preview whether anyone is looking at it.
// Becoming visible opens one model for the selected Output pane; becoming
// hidden closes that model and its control subscription.
func (m *Model) SetWorkspacesVisible(visible bool) tea.Cmd {
	if m.preview.visible == visible {
		if visible && m.currentPreviewOwnership() == 0 {
			m.activatePreviewOwnership()
		}
		return nil
	}
	m.preview.visible = visible
	if visible {
		m.activatePreviewOwnership()
	} else {
		m.deactivatePreviewOwnership()
	}
	// Moving between the catalog's two projections supersedes any activation
	// still being validated. Agents and Workspaces share one cache, one
	// generation, and one poll, so nothing else here marks the moment the user
	// left the view they pressed enter on — and a destination that opens itself
	// after the user moved on is exactly the surprise the generation/request
	// machinery exists to prevent.
	m.requestID++
	if !visible {
		m.pulseGeneration++
		m.pulseScheduled = false
		m.releasePreview()
		return nil
	}
	return m.previewSelect()
}

func (m *Model) currentPreviewOwnership() uint64 {
	lease := m.ensurePreviewOwnership()
	lease.mu.RLock()
	defer lease.mu.RUnlock()
	if !lease.active {
		return 0
	}
	return lease.generation
}

func (m *Model) ensurePreviewOwnership() *previewOwnershipLease {
	if m.previewOwnership == nil {
		m.previewOwnership = &previewOwnershipLease{}
	}
	return m.previewOwnership
}

func (m *Model) activatePreviewOwnership() {
	lease := m.ensurePreviewOwnership()
	lease.mu.Lock()
	defer lease.mu.Unlock()
	lease.generation++
	lease.active = true
}

func (m *Model) deactivatePreviewOwnership() {
	lease := m.ensurePreviewOwnership()
	if previewBeforeDeactivate != nil {
		previewBeforeDeactivate()
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	lease.active = false
	lease.generation++
}

func (m *Model) acquirePreviewOwnership(generation uint64) (func(), bool) {
	lease := m.ensurePreviewOwnership()
	if generation == 0 {
		return nil, false
	}
	lease.mu.RLock()
	if !lease.active || lease.generation != generation {
		lease.mu.RUnlock()
		return nil, false
	}
	return lease.mu.RUnlock, true
}

// releasePreview closes the selected terminal and forgets its memory-only state.
func (m *Model) releasePreview() {
	m.closePreviewTerminal()
	m.stashPreviewPanes()
	m.preview.generation++
	m.preview.workspaceID = ""
	m.resetPreviewContent()
	m.preview.reason = ""
}

// resetPreviewContent drops the captured output and everything anchored to it.
// A selection names buffer lines, so it cannot outlive the buffer it named.
func (m *Model) resetPreviewContent() {
	m.preview.buffer = nil
	m.preview.offset = 0
	m.preview.freeze = tty.WindowFreeze{}
	// The reach names lines of the buffer being dropped, and a read in flight for
	// the old pane must not land on the new one.
	m.preview.history = tty.HistoryReach{}
	m.preview.selection.Clear()
	m.preview.pointer.Abandon()
	m.preview.pointer.ResetUnit()
	m.resetActivePreviewPanes()
}

func (m *Model) resetActivePreviewPanes() {
	m.preview.doc.releaseEdit()
	m.preview.doc = nil
	m.preview.issue = nil
	m.preview.diff = nil
	m.preview.resource = nil
	m.preview.paneRoot = &panelayout.Node{ID: 1, Kind: panelayout.Terminal}
	m.preview.paneFocus = 1
	m.preview.paneNextID = 2
	m.preview.paneDragSplitID = 0
}

func (m *Model) nextPreviewContentEpoch() uint64 {
	m.preview.contentEpoch++
	return m.preview.contentEpoch
}

func (m *Model) stashPreviewPanes() {
	if m.preview.workspaceID == "" || m.preview.paneRoot == nil {
		return
	}
	// Resource answers are deliberately scoped to the selected row and are
	// discarded after a switch. Return pending tabs to an armed state before
	// caching the pane so revisiting the row can resolve them again.
	if res := m.preview.resource; res != nil {
		res.pane.ReArmPending()
	}
	if m.preview.paneCache == nil {
		m.preview.paneCache = make(map[string]previewPaneCache)
	}
	m.preview.paneCache[m.preview.workspaceID] = previewPaneCache{
		root: m.preview.paneRoot, focus: m.preview.paneFocus, nextID: m.preview.paneNextID,
		doc: m.preview.doc, issue: m.preview.issue, diff: m.preview.diff,
		resource: m.preview.resource,
	}
}

func (m *Model) restorePreviewPanes(workspaceID string) {
	if cached, ok := m.preview.paneCache[workspaceID]; ok && cached.root != nil {
		m.preview.paneRoot, m.preview.paneFocus, m.preview.paneNextID = cached.root, cached.focus, cached.nextID
		m.preview.doc, m.preview.issue, m.preview.diff = cached.doc, cached.issue, cached.diff
		m.preview.resource = cached.resource
		m.preview.paneDragSplitID = 0
		return
	}
	m.resetActivePreviewPanes()
}

// previewSync reconciles the one visible terminal when selection or tab state
// changes. Inventory rows themselves never allocate terminal resources.
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
		return m.syncPreviewTerminal()
	}
	return m.previewSelect()
}

// previewSelect binds the preview to the current selection straight away.
func (m *Model) previewSelect() tea.Cmd { return m.bindPreview(false) }

// bindPreview binds the preview to the current selection. keepContent preserves
// the current window when keyboard ownership changes without changing target.
func (m *Model) bindPreview(keepContent bool) tea.Cmd {
	workspace, selected := m.SelectedWorkspace()
	keep := keepContent && selected && workspace.ID == m.preview.workspaceID

	if m.preview.terminal != nil && m.preview.interactive {
		m.preview.terminal.ReleaseInput()
	}
	m.preview.interactive = false
	m.preview.generation++
	if keep {
		// A standing selection cannot survive the handover: subsequent relative
		// captures re-base line offsets and invalidate absolute anchors.
		m.clearPreviewSelection()
	} else {
		m.stashPreviewPanes()
		m.resetPreviewContent()
	}
	m.preview.reason = ""

	if !selected {
		m.preview.workspaceID = ""
		m.closePreviewTerminal()
		return nil
	}
	m.preview.workspaceID = workspace.ID
	if !keep {
		m.restorePreviewPanes(workspace.ID)
	}
	// An item with no single live pane behind it opens no model. There is nothing
	// to read, and guessing among several panes is exactly what the catalog refuses.
	if reason, unavailable := previewUnavailable(workspace); unavailable {
		m.preview.reason = reason
		m.closePreviewTerminal()
		m.resetPreviewContent()
		return nil
	}
	var pendingCmd tea.Cmd
	if cmd := m.consumePendingView(workspace.TmuxName); cmd != nil {
		pendingCmd = cmd
	}
	return tea.Batch(m.syncPreviewTerminal(), pendingCmd)
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
// While the pane is merely on screen (sidebar hidden), these keys scroll it,
// restore the list, or start typing. There is no watched-preview keyboard mode.
func (m *Model) previewKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	key := msg.String()
	if m.previewDocEditing() {
		// A live editor is the document pane's version of the same fact, and
		// answers first: it is the pane with the keyboard.
		handled, cmd := m.previewDocKey(msg)
		return handled, cmd
	}
	if m.PreviewInteractive() {
		// Every live key goes to the component, exit chords and this surface's own
		// chords included: it consults them through OnKey before anything becomes
		// input, and a surface that answered them out here as well would send them
		// to the pane twice.
		return true, m.forwardToTerminal(msg)
	}
	if handled, cmd := m.previewIssueKey(msg); handled {
		return true, cmd
	}
	if handled, cmd := m.previewResourceKey(msg); handled {
		return true, cmd
	}
	if handled, cmd := m.previewDiffPaneKey(msg); handled {
		return true, cmd
	}
	if handled, cmd := m.previewDocKey(msg); handled {
		return true, cmd
	}
	// A focused content leaf declined this key. Stop here so enter/E cannot
	// start typing in the terminal behind the card, and so h/left cannot
	// steal the keyboard back to the list. WorkspacesKey then lets host
	// globals (@, ?, digits) through and swallows everything else.
	if m.contentLeafFocused() {
		return false, nil
	}
	// The same acts on the terminal surface the live pane routes through OnKey.
	// The selection they act on exists in both states, so a watched pane answers
	// them too; everything after this is the browser's own navigation.
	if handled, cmd := m.terminalKey(msg); handled {
		return true, cmd
	}
	// Everything a scrollback key does — j/k, the pager chords, the jumps — was
	// answered above by the shared rule, in the same words the live pane answers
	// it in. What is left here is the browser's own navigation.
	switch key {
	case "enter", interactiveEnterKeyAlt:
		return true, m.enterPreviewInteractive()
	case "left", "h", "esc":
		return true, m.focusList()
	}
	return false, nil
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

// previewScrollbackKey walks the window through scrollback, in either of the
// preview's states. Which keys move a window is the shared rule's, and so is the
// shift a live pane requires; this surface supplies only its drawn rows, so a
// page is the same distance here as it is anywhere else the pane is read.
func (m *Model) previewScrollbackKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	state := tty.ScrollbackWatched
	if m.PreviewInteractive() {
		state = tty.ScrollbackLive
	}
	// Every key typed into a live pane comes through here, so the window's height
	// is resolved only for the keys the shared rule claims.
	if !tty.IsScrollbackKey(state, msg) {
		return false, nil
	}
	move, ok := tty.MapScrollbackKey(state, msg, m.previewRows())
	if !ok {
		return false, nil
	}
	m.clearPreviewSelectionOnScroll()
	switch {
	case move.ToOldest:
		m.jumpPreviewWindow(m.previewMaxOffset())
		return true, m.reachOlderPreviewHistory(tty.HistoryChunkLines)
	case move.ToLive:
		m.jumpPreviewWindow(0)
		m.preview.history.Cancel()
	default:
		before := m.previewScrollAnchor()
		m.scrollPreview(move.Rows)
		// A move that ran out of loaded buffer reaches for the history behind it,
		// exactly as a wheel notch at the same bound does.
		if move.Rows > 0 && m.previewScrollAnchor() == before {
			return true, m.reachOlderPreviewHistory(move.Rows)
		}
	}
	return true, nil
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
	m.preview.offset = tty.ScrollWindow(&m.preview.freeze, m.preview.offset, delta, m.previewMaxOffset())
}

// previewScrollAnchor is where the window sits, in whichever coordinate it is
// currently placed by. Callers use it to tell whether a scroll moved anything.
func (m *Model) previewScrollAnchor() int {
	return tty.WindowAnchor(&m.preview.freeze, m.preview.offset)
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
// which is the direction the shared edge-scroll rule reports. Reconciling that
// with a window counted back from the live bottom is the shared rule's, not this
// surface's.
func (m *Model) scrollPreviewRows(delta int) {
	m.preview.offset = tty.ScrollWindowRows(&m.preview.freeze, m.preview.offset, delta, m.previewMaxOffset())
}

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
	// A jump this large abandons the window a pending read was reaching for.
	m.preview.history.Cancel()
}

// previewMaxOffset is how far back this surface's window can sit, taken from
// the drawn window off its live edge. Reading the current layout's bound
// instead answered a following window with the untrimmed grid's number, so a
// jump to the oldest row landed one notch short of it (td-bbbbfe).
func (m *Model) previewMaxOffset() int {
	window := m.previewWindow()
	if !window.ok {
		return 0
	}
	return tty.WindowBound(window.input)
}

// previewRows is the body height of the preview box at the last rendered size.
func (m *Model) previewRows() int {
	if window := m.previewWindow(); window.ok {
		return max(window.layout.DisplayHeight, 1)
	}
	return max(1, m.height-termpreview.HeaderRows)
}

// previewBuffer is the selected tty.Model's output regardless of whether the
// keyboard is currently routed to it.
func (m *Model) previewBuffer() *tty.OutputBuffer {
	if m.previewTerminalActive() {
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
	// Where the window sits — pinned to an absolute start for a gesture, or a
	// distance back from the live bottom that follows output at zero — is the
	// shared rule's answer, the same one the project surfaces place theirs by.
	placement := tty.PlaceWindow(&m.preview.freeze, m.preview.offset)
	input.Offset, input.OffsetFromBottom, input.Follow = placement.Offset, placement.FromBottom, placement.Follow
	// Pane geometry is not an interactive-only fact. A watched full-screen
	// application needs it too: it is what fills the canvas with the pane's own
	// background and what keeps its intentional blank rows from being trimmed
	// into a window that walks up into history (td-c3644b).
	input.PaneWidth, input.PaneHeight = m.previewPaneSize()
	if interactive {
		input.Interactive = true
		input.CursorRow, input.CursorCol, input.CursorVisible = m.preview.terminal.CursorState()
	}
	return input
}

// previewPaneSize comes from the same model in watched and interactive states.
func (m *Model) previewPaneSize() (width, height int) {
	if m.previewTerminalActive() {
		return m.preview.terminal.PaneSize()
	}
	return 0, 0
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
// tmux's mouse protocol expects, and refuses everything that is not a cell of
// the pane being drawn. It answers about the pane, not about the keyboard: a
// watched pane has cells at the same coordinates a live one does.
func (m *Model) previewPaneCoords(x, y int) (col, row int, ok bool) {
	if !m.previewTerminalActive() {
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
	return m.renderOutputTerminal(width, height)
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
		// The reach is the shared one, so a read of older history is in flight
		// here exactly as it is on the project surface, and is said the same way.
		LoadingOlder: m.preview.history.Loading,
		// Who has the mouse is a property of the pane, asked whether or not this
		// surface holds the keyboard: a watched notch is forwarded too, and this
		// is the note that explains a window that did not move.
		MouseReporting: m.previewTerminalActive() && m.preview.terminal.PaneMouseReporting(),
		PaneLive:       m.PreviewInteractive(),
		PaneWidth:      input.PaneWidth,
		PaneHeight:     input.PaneHeight,
		LiveEdgeKey:    m.previewLiveEdgeKey(),
	})
	// A fetch in flight is the fact the header must keep even after Diff/Task
	// action chips shrink the leftover. Lead with it so AppendStatus cannot
	// drop it behind the lines-back note.
	if m.preview.history.Loading {
		for i, note := range notes {
			if strings.Contains(note.Compact, "loading") || strings.Contains(note.Text, "loading") {
				notes = append([]tty.StatusNote{note}, append(notes[:i], notes[i+1:]...)...)
				break
			}
		}
	}
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
		// Enter is the primary way in from the list; this hint is only shown
		// on leftover preview-only chrome (hidden sidebar).
		parts = append(parts, "enter to type")
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
