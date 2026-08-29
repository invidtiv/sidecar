package overview

import (
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/mouse"
	appmsg "github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/paneframe"
	"github.com/marcus/sidecar/internal/panelayout"
	sharedscroll "github.com/marcus/sidecar/internal/scroll"
	"github.com/marcus/sidecar/internal/termpanes"
	"github.com/marcus/sidecar/internal/termpreview"
	"github.com/marcus/sidecar/internal/tty"
)

// Watching and typing into a pane from the global browser use internal/tty's
// embeddable terminal — the exact component the project Workspaces plugin
// drives, with the same control-mode producer, exit key, double-escape, geometry,
// and key mapping. This file is only the wiring: which selection is eligible,
// where the terminal is drawn, and whether the keyboard belongs to it.
//
// Creating a shell from the global space remains out of scope: nothing here
// starts a session, and a selection with no live pane is refused rather than
// given one.

const (
	// E is the remaining explicit type key. i is find-TD-task, not a way in.
	interactiveEnterKeyAlt = tty.EnterInteractiveKeyAlt
)

// SetTerminalConfig adopts the user's terminal settings, so the browser's live
// pane and the project plugin's answer the same chords for the same acts. It is
// a setter rather than a constructor argument because this package must not
// import internal/config: the app resolves the values once and hands the same
// value to both surfaces.
func (m *Model) SetTerminalConfig(config tty.Config) {
	m.terminalConfig = config
}

// TerminalConfig is the resolved configuration this surface answers. The app
// asks so the keymap binding, the footer, and the palette describe the keys the
// surface actually answers.
func (m *Model) TerminalConfig() tty.Config {
	config := m.terminalConfig
	defaults := tty.DefaultConfig()
	if config.ExitKey == "" {
		config.ExitKey = defaults.ExitKey
	}
	if config.CopyKey == "" {
		config.CopyKey = defaults.CopyKey
	}
	if config.PasteKey == "" {
		config.PasteKey = defaults.PasteKey
	}
	if config.SelectAllKey == "" {
		config.SelectAllKey = defaults.SelectAllKey
	}
	// The browser has no attach path — attaching stays the owning project's — and
	// an attach chord that only exited would be an undocumented third way out and
	// a keystroke stolen from the pane.
	config.AttachKey = ""
	config.ScrollbackLines = previewScrollbackLines
	return config
}

// InteractiveExitKey is the chord that ends interactive mode here.
func (m *Model) InteractiveExitKey() string { return m.TerminalConfig().ExitKey }

// previewTerminal is the embedded-terminal seam. *tty.Model is the only
// implementation; tests substitute a recorder so proving the interaction path
// never has to reach a real tmux server.
type previewTerminal interface {
	Open(tty.Target) tea.Cmd
	Close()
	Update(tea.Msg) tea.Cmd
	SetDimensions(width, height int) tea.Cmd
	SendUnknownSequence(tea.Msg) tea.Cmd
	IsActive() bool

	// ReleaseInput drops keyboard-specific state without closing the watched
	// producer. Close releases the selected target and its control subscription.
	ReleaseInput()
	Exit()

	// The pane as content: what it has drawn, how big its grid is, and where its
	// cursor sits. The browser renders its own window over this rather than the
	// component's live tail, so a scrolled-back window and a highlighted
	// selection are drawn the same way whether or not anyone is typing.
	Buffer() *tty.OutputBuffer
	PaneSize() (width, height int)
	CursorState() (row, col int, visible bool)

	// The pane as history: how much of it tmux holds, and where an older range
	// this surface read goes. Which range to read, and when, is the shared
	// reach's; the component owns the buffer it lands in.
	History() tty.HistoryInfo
	PrependHistory(content string, baseLine int) bool

	// The pane as a mouse target. Whether a click or a notch belongs to the
	// application is the shared layer's rule; delivering it is the component's.
	PaneMouseReporting() bool
	SendClick(col, row int) tea.Cmd
	SendWheelNotches(up bool, col, row, notches int) tea.Cmd
	NoteMouseActivity()

	// NoteInput records input this surface delivered by a path of its own, on
	// the clock the pane's capture cadence decays from.
	NoteInput()
}

var _ previewTerminal = (*tty.Model)(nil)

// newPreviewTerminal builds the browser's terminal with the host contract the
// component calls back through. It is a variable so the seam can be substituted
// without a tmux server behind it; a substitute is handed the same hooks, so a
// test drives the real ways in and out.
var newPreviewTerminal = func(config tty.Config, hooks tty.Hooks) previewTerminal {
	model := tty.New(&config)
	model.SetHooks(hooks)
	return model
}

// syncPreviewTerminal reconciles the one resource-bearing preview with the one
// pane actually visible on the Output surface. The catalog remains metadata:
// list collection never opens a model for any other row.
func (m *Model) syncPreviewTerminal() tea.Cmd {
	if !m.preview.visible {
		m.closePreviewTerminal()
		return nil
	}
	workspace, ok := m.SelectedWorkspace()
	if !ok || workspace.ID != m.preview.workspaceID {
		m.closePreviewTerminal()
		return nil
	}
	if reason, unavailable := previewUnavailable(workspace); unavailable {
		m.preview.reason = reason
		m.closePreviewTerminal()
		return nil
	}

	desired := tty.Target{Session: workspace.TmuxName, Pane: workspace.PaneID, Host: workspace.HostID}
	leaf, state := m.primaryTerminalLeaf(), m.primaryTerminalState()
	if state.terminal == nil {
		state.terminal = newPreviewTerminal(m.TerminalConfig(), m.previewTerminalHooksFor(leaf))
	}
	if state.terminal.IsActive() && m.primaryTarget() == desired {
		leaf.Buffer = state.terminal.Buffer()
		return m.syncTerminalLeafGeometry(leaf.ID)
	}

	m.closePreviewTerminal()
	m.preview.reason = ""
	m.setPrimaryTarget(desired)
	var cmds []tea.Cmd
	// Point the terminal at the right machine before it opens. Host is part of
	// the target identity, so a change between a local and a remote row
	// reaches the close-and-reopen path above and this runs on a fresh
	// activation rather than mutating a live one.
	//
	// The capability is discovered rather than declared on previewTerminal:
	// four tests substitute that seam with fakes that have no business
	// knowing about ssh, and widening the interface for a case none of them
	// exercise would be a worse trade than an assertion here.
	if workspace.Remote() {
		remote, ok := state.terminal.(interface {
			UseRemoteControl(tty.ControlSpawner)
		})
		spawn := m.hostControlSpawner(workspace.HostID)
		if !ok || spawn == nil {
			m.preview.reason = "Cannot open a live view of " + workspace.HostID + " right now"
			m.closePreviewTerminal()
			return nil
		}
		remote.UseRemoteControl(spawn)
	}
	if width, height, ok := m.terminalLeafSize(leaf.ID); ok {
		leaf.Target.Width, leaf.Target.Height = width, height
		cmds = append(cmds, state.terminal.SetDimensions(width, height))
	}
	cmds = append(cmds, state.terminal.Open(desired))
	leaf.Buffer = state.terminal.Buffer()
	m.tracef("preview terminal open workspace=%s pane=%s", workspace.ID, workspace.PaneID)
	return tea.Batch(cmds...)
}

func (m *Model) closePreviewTerminal() {
	leaf, state := m.primaryTerminalLeaf(), m.primaryTerminalState()
	if leaf.Interactive && state.terminal != nil {
		state.terminal.ReleaseInput()
	}
	if state.terminal != nil && state.terminal.IsActive() {
		state.terminal.Close()
	}
	leaf.Interactive = false
	m.setPrimaryTarget(tty.Target{})
	leaf.Buffer = nil
	// The reach belongs to the pane being released: a read still in flight is
	// for a target this surface no longer holds.
	leaf.History = tty.HistoryReach{}
}

// previewTerminalHooks is everything this surface owns about a live pane, said
// once, to the component that owns the rest. Answering any of it outside the
// component instead — a chord read before the key is forwarded, a mode ending
// noticed by polling IsActive afterwards — is a second implementation of a
// pipeline the project surface already drives through these same hooks.
func (m *Model) previewTerminalHooks() tty.Hooks {
	return m.previewTerminalHooksFor(m.previewTerminalLeaf())
}

// previewTerminalHooksFor binds hooks to the leaf's state, not to a captured
// leaf ID: a deck projection may rekey the leaf while its terminal keeps
// running, and a hook that remembered the old ID would answer for a pane that
// no longer exists.
func (m *Model) previewTerminalHooksFor(hookLeaf *termpanes.Leaf) tty.Hooks {
	return tty.Hooks{
		OnKey:      m.previewTerminalKey,
		BeforeSend: m.beforePreviewSend,
		OnExit: func() tea.Cmd {
			// User-chosen ways out (ctrl+\, esc esc) land on the list, still
			// showing this session. There is no watched-preview rest state.
			return tea.Batch(m.releasePreviewKeyboard(), m.focusList())
		},
		OnSessionEnded: func() tea.Cmd {
			leafID := hookLeaf.ID
			if leaf := panelayout.Find(m.preview.paneRoot, leafID); leaf != nil && leaf.Kind == panelayout.Shell {
				return m.closePreviewShellLeaf(leafID, termpanes.CloseSessionEnded)
			}
			// A pane that died under a keystroke or a forwarded click ends the mode
			// inside the component. The project surface raises the same toast, and a
			// mode that ends by itself with no notice reads as a dropped keystroke.
			m.setPrimaryTarget(tty.Target{})
			m.preview.reason = "The session for this workspace has ended"
			return tea.Batch(m.releasePreviewKeyboard(), m.focusList(),
				appmsg.Alert(notify.SourceSession, notify.SeverityInfo, "Session ended"))
		},
		// Watching continues after keyboard ownership ends, exactly as in the
		// project Workspaces preview.
		ExitAction: tty.ExitReleasesInput,
	}
}

// previewTerminalKey is the component's OnKey hook: the chords that act on the
// terminal surface rather than on the pane inside it.
func (m *Model) previewTerminalKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	handled, cmd := m.terminalKey(msg)
	return cmd, handled
}

// beforePreviewSend is the component's BeforeSend hook. Typing is owed a view of
// itself: a window left in scrollback would take the keystroke and show none of
// it.
func (m *Model) beforePreviewSend(msg tea.KeyPressMsg) {
	if m.previewTerminalLeaf().Scroll == 0 && !m.previewTerminalLeaf().Freeze.Active() {
		return
	}
	if m.TerminalConfig().IsPasteChord(msg) || tty.ShouldSnapBack(msg, m.now().Sub(m.previewTerminalLeaf().Wheel.LastAt())) {
		m.pinPreviewToLive()
	}
}

// releasePreviewKeyboard gives the keyboard back while the same tty.Model keeps
// producing the watched pane. Where that leaves the window is the shared rule's
// answer — the reader's own position, thawing a pin the ended gesture no longer
// owns — and asking tty.LeaveLiveWindow for it is what keeps this surface and
// the project workspace's leaveInteractiveMode the same one (td-651ca2).
func (m *Model) releasePreviewKeyboard() tea.Cmd {
	m.tracef("preview interactive exit workspace=%s", m.preview.workspaceID)
	m.previewTerminalLeaf().Interactive = false
	m.clearPreviewSelection()
	m.previewTerminalLeaf().Scroll = tty.LeaveLiveWindow(&m.previewTerminalLeaf().Freeze, m.previewTerminalLeaf().Scroll, m.previewMaxOffset())
	return nil
}

// PreviewInteractive reports that the focused preview is forwarding keys to a
// live pane. The app asks so the keymap context, the footer hints, the mouse
// mode and the native cursor all follow the same fact.
func (m *Model) PreviewInteractive() bool {
	leaf := m.previewTerminalLeaf()
	return leaf.ID == m.preview.paneFocus && leaf.Interactive && m.previewTerminalActive()
}

func (m *Model) previewTerminalActive() bool {
	return m.previewTerminalState().terminal != nil && m.previewTerminalState().terminal.IsActive()
}

// PreviewCanType reports that the selection has a live pane this surface could
// hand the keyboard to. The footer asks so it never offers a key the preview
// itself is refusing.
func (m *Model) PreviewCanType() bool {
	workspace, ok := m.SelectedWorkspace()
	if !ok {
		return false
	}
	_, unavailable := previewUnavailable(workspace)
	return !unavailable
}

// enterPreviewInteractive hands the selected pane's keyboard to the user.
//
// It refuses exactly what the preview already refuses to read: an item with no
// live pane, or an ambiguous match the catalog will not guess between. The
// refusal is the reason the preview already shows, said out loud as a toast,
// because a key that silently does nothing is indistinguishable from a bug.
func (m *Model) enterPreviewInteractive() tea.Cmd {
	if m.PreviewInteractive() {
		return nil
	}

	workspace, ok := m.SelectedWorkspace()
	if !ok {
		return nil
	}
	if reason, unavailable := previewUnavailable(workspace); unavailable {
		m.preview.reason = reason
		return appmsg.Blocked(reason)
	}
	// A remote pane is watched, not typed into. Input is already dropped by
	// the read-only sender, but entering the mode anyway would put "typing" in
	// the header of a pane that cannot receive a keystroke — a worse failure
	// than refusing, because it looks like it worked.
	//
	// Phase B brings the in-band sender and the cross-host lease rules that
	// make typing into another machine's pane safe; the refusal says so.
	if workspace.Remote() {
		reason := "Watching " + workspace.HostID + " — typing into a remote pane arrives in a later release"
		m.preview.reason = ""
		return appmsg.Blocked(reason)
	}
	leaf := m.previewTerminalLeaf()
	if node := panelayout.Find(m.preview.paneRoot, m.preview.paneFocus); node == nil || !panelayout.IsLive(node.Kind) {
		leaf = m.primaryTerminalLeaf()
		m.preview.paneFocus = leaf.ID
	}
	open := m.syncPreviewTerminal()
	if node := panelayout.Find(m.preview.paneRoot, leaf.ID); node != nil && node.Kind == panelayout.Shell {
		open = m.syncTerminalLeaf(leaf.ID)
	}
	if !m.previewTerminalActive() {
		return open
	}

	m.preview.focus = focusPreview
	leaf.Interactive = true
	// The pane taking the keyboard is the terminal leaf taking focus. Saying so
	// here is what keeps the ring and the keys one value: without it a handover
	// that started from a document leaf would type into the shell while the ring
	// stayed on the document.
	m.focusPreviewLeaf(leaf.ID)
	if m.previewNarrow() {
		m.preview.full = true
	}
	m.jumpPreviewWindow(0)
	// A standing selection cannot survive the handover: subsequent relative
	// captures re-base line offsets and invalidate absolute anchors.
	m.clearPreviewSelection()
	var cmds []tea.Cmd
	cmds = append(cmds, open)
	if buffer := m.previewTerminalState().terminal.Buffer(); buffer != nil {
		m.previewTerminalLeaf().Buffer = buffer
	}
	m.tracef("preview interactive enter workspace=%s pane=%s", workspace.ID, workspace.PaneID)
	if !m.preview.interactiveHintShown {
		m.preview.interactiveHintShown = true
		cmds = append(cmds, appmsg.ShowFlash("Typing into "+workspace.Name+" — "+m.InteractiveExitKey()+" or esc esc to stop"))
	}
	return tea.Batch(cmds...)
}

// switchPreviewInteractive rebinds a live pane to the current selection. Used
// when the user clicks another list row while typing: stay interactive on the
// new live pane, or refuse and land on the list if it has none.
func (m *Model) switchPreviewInteractive() tea.Cmd {
	sync := m.previewSync()
	if m.PreviewCanType() {
		return tea.Batch(sync, m.enterPreviewInteractive())
	}
	return tea.Batch(sync, m.focusList())
}

// exitPreviewInteractive takes the keyboard back from a live pane on this
// surface's own initiative — focus moved, the selection changed, the tab was
// hidden. The ways out the *user* chooses are the component's, and it calls
// OnExit for them, so this must not be a second path: it is the same one, asked
// for from outside.
func (m *Model) exitPreviewInteractive() tea.Cmd {
	if !m.PreviewInteractive() {
		return nil
	}
	m.previewTerminalState().terminal.ReleaseInput()
	return m.releasePreviewKeyboard()
}

// forwardToTerminal hands one message to the live terminal. What a key means,
// which chords never reach the pane, and every way the mode can end are the
// component's, and it reports each of them through the hooks this surface
// registered — so nothing here inspects what the message did afterwards.
func (m *Model) forwardToTerminal(msg tea.Msg) tea.Cmd {
	if !m.PreviewInteractive() {
		return nil
	}
	return m.previewTerminalState().terminal.Update(msg)
}

// pressPreview arms a pointer gesture over the preview box. Nothing is decided
// here: a release without motion activates the pane (or hands the click to the
// application running in it), while motion selects text instead. Deciding on
// mouse-down would mean a drag that starts on the terminal could never select.
//
// A press does not take the keyboard. Click-to-type focuses on release via
// enterPreviewInteractive; a drag-select must not rest in watched-preview.
func (m *Model) pressPreview(action mouse.MouseAction) tea.Cmd {
	geometry, ok := m.previewGeometry()
	if !ok || action.Region == nil {
		return nil
	}
	// A document pane is drawn beside the terminal, so its highlight is one of
	// the selections a gesture starting here takes over from.
	m.clearPreviewDocSelections(nil)

	modified := action.Shift || action.Alt
	linkCmd, claimed := m.activatePreviewLinkAt(action, modified)
	want := tty.ResolveClick(tty.ClickIntent{
		Live:           m.PreviewInteractive(),
		MouseReporting: m.PreviewInteractive() && m.previewTerminalState().terminal.PaneMouseReporting(),
		Modified:       modified,
		LinkClaimed:    claimed,
	})
	if claimed {
		// Arm a no-op release so the click is claimed (LinkClaimed) and is
		// not "start typing". Shift/alt never reach here.
		m.workspacesMouse.StartDrag(action.X, action.Y, previewRegionKind, 0)
		m.previewTerminalLeaf().Pointer.Press(geometry, m.previewBuffer(), &m.previewTerminalLeaf().Selection, tty.PressEvent{
			X: action.X, Y: action.Y,
			Shift: action.Shift, Alt: action.Alt,
			Rect: action.Region.Rect, Want: want,
			SameSource: true,
		})
		return linkCmd
	}

	// Track the gesture even when the buffer is empty or the press lands on
	// padding: a plain click still needs its release, and motion can become
	// selectable once it reaches a row.
	m.workspacesMouse.StartDrag(action.X, action.Y, previewRegionKind, 0)
	m.previewTerminalLeaf().Pointer.Press(geometry, m.previewBuffer(), &m.previewTerminalLeaf().Selection, tty.PressEvent{
		X: action.X, Y: action.Y,
		Shift: action.Shift, Alt: action.Alt,
		Rect: action.Region.Rect, Want: want,
		// One terminal is drawn here, so every gesture is in the same source.
		SameSource: true,
	})
	return nil
}

// dragPreview extends the live selection, scrolling the window when the pointer
// has run past an edge so a selection can reach content that is not on screen.
func (m *Model) dragPreview(action mouse.MouseAction) tea.Cmd {
	// Freeze before anything reads or moves the window: a capture landing
	// mid-drag renumbers the watched buffer, and a window still placed against
	// the live bottom follows it, leaving the anchor naming different text than
	// the highlight being dragged.
	m.freezePreviewWindow()
	geometry, ok := m.previewGeometry()
	if !ok {
		return nil
	}
	buffer := m.previewBuffer()
	if !m.previewTerminalLeaf().Selection.Anchor.Valid() &&
		!m.previewTerminalLeaf().Pointer.AnchorFrom(geometry, buffer, &m.previewTerminalLeaf().Selection,
			action.X-action.DragDX, action.Y-action.DragDY, action.Alt) {
		return nil
	}
	// The tick re-reads this position, so a pointer held still past an edge keeps
	// scrolling after motion events stop arriving. Real motion also restarts the
	// hold budget that bounds a chain running on a lost release.
	m.previewTerminalLeaf().Pointer.NoteDragMotion(action.X, action.Y)
	m.scrollPreviewRows(tty.EdgeScrollDelta(geometry, action.Y, tty.DragScrollStep))
	// The window may have moved under the pointer, so ask again before extending.
	geometry, _ = m.previewGeometry()
	if !m.previewTerminalLeaf().Pointer.DragTo(geometry, buffer, &m.previewTerminalLeaf().Selection, action.X, action.Y) {
		return nil
	}
	if tty.EdgeScrollDelta(geometry, action.Y, tty.AutoScrollStep) == 0 {
		return nil
	}
	return m.schedulePreviewAutoScroll()
}

// selectPreviewUnit installs the word or line under the pointer as the gesture's
// anchor unit, so the button still held extends by that unit.
func (m *Model) selectPreviewUnit(action mouse.MouseAction, unit tty.SelectionUnit) tea.Cmd {
	geometry, ok := m.previewGeometry()
	if !ok || action.Region == nil {
		return nil
	}
	m.previewTerminalLeaf().Pointer.AdoptSurface(&m.previewTerminalLeaf().Selection, action.Region.Rect)
	if !m.previewTerminalLeaf().Pointer.SelectUnitAt(geometry, m.previewBuffer(), &m.previewTerminalLeaf().Selection,
		action.X, action.Y, unit) {
		return nil
	}
	// Arm drag tracking exactly as a plain mouse-down does, so the button still
	// held keeps delivering motion to this gesture and its release arrives as a
	// drag end rather than a fresh click.
	m.workspacesMouse.StartDrag(action.X, action.Y, previewRegionKind, 0)
	if m.TerminalConfig().CopyOnSelect {
		return m.copyPreviewSelectionCmd()
	}
	return nil
}

// finishPreviewGesture resolves the gesture: a selection was made, or the click
// meant what the press armed it to mean.
func (m *Model) finishPreviewGesture() tea.Cmd {
	// The gesture is over, so the window goes back to following the live edge
	// from wherever it was pinned.
	m.thawPreviewWindow()
	resolution, selected := m.previewTerminalLeaf().Pointer.Release(&m.previewTerminalLeaf().Selection)
	if selected {
		if m.TerminalConfig().CopyOnSelect {
			return m.copyPreviewSelectionCmd()
		}
		return nil
	}
	switch resolution {
	case tty.ClickActivate:
		// Diff/Task are views of the row. Only the Output terminal types.
		return m.enterPreviewInteractive()
	case tty.ClickForward:
		// The press position, not the release: a click that resolves here never
		// moved, and the send carries a press and a release together.
		pressX, pressY := m.previewTerminalLeaf().Pointer.PressPoint()
		col, row, ok := m.previewPaneCoords(pressX, pressY)
		if !ok {
			return nil
		}
		return m.previewTerminalState().terminal.SendClick(col, row)
	}
	return nil
}

// abandonPreviewGesture ends a gesture whose release will never arrive — the
// pointer left the window, or focus changed. Neither activation nor a forwarded
// click survives a release the app never saw.
func (m *Model) abandonPreviewGesture() tea.Cmd {
	// Before anything else: an edge scroll tick still in flight belongs to a
	// gesture that is over.
	m.previewTerminalLeaf().Pointer.Abandon()
	if m.previewTerminalLeaf().Selection.Anchor.Valid() {
		// The release happened, outside the window. Close the selection where the
		// shared handler abandons its drag.
		return m.finishPreviewGesture()
	}
	m.thawPreviewWindow()
	return nil
}

// previewAutoScrollTickMsg drives one step of the held-pointer edge scroll. The
// generation pins it to the gesture that scheduled it, so a tick in flight when
// the button comes up is discarded.
type previewAutoScrollTickMsg struct {
	generation uint64
}

func (m *Model) schedulePreviewAutoScroll() tea.Cmd {
	return m.previewTerminalLeaf().Pointer.ScheduleAutoScroll(func(generation uint64) tea.Msg {
		return previewAutoScrollTickMsg{generation: generation}
	})
}

// advancePreviewAutoScroll scrolls one step for a pointer still held past an
// edge and re-arms itself. It stops when the gesture ended, the pointer came
// back inside the content, or the window has no more rows in that direction.
func (m *Model) advancePreviewAutoScroll(msg previewAutoScrollTickMsg) tea.Cmd {
	if !m.previewTerminalLeaf().Pointer.AdvanceAutoScroll(msg.generation, m.previewAutoScrollTarget()) {
		return nil
	}
	return m.schedulePreviewAutoScroll()
}

// previewAutoScrollTarget is this surface's window, for the shared driver.
func (m *Model) previewAutoScrollTarget() tty.AutoScrollTarget {
	return tty.AutoScrollTarget{
		Geometry:  func() tty.Geometry { geometry, _ := m.previewGeometry(); return geometry },
		Buffer:    func() tty.Buffer { return m.previewBuffer() },
		Selection: &m.previewTerminalLeaf().Selection,
		Scroll: func(delta int) bool {
			before := m.previewScrollAnchor()
			m.scrollPreviewRows(delta)
			return m.previewScrollAnchor() != before
		},
	}
}

// wheelPreview routes a wheel notch. The application running in the pane owns it
// only while it has asked for mouse reports; every other notch scrolls the window
// the surface is drawing, which is what makes the wheel work over a plain shell.
//
// Whether the pane has the mouse is asked whether or not it holds the keyboard:
// the send is addressed to a pane, not to a client, and a watched agent that
// draws its own scrollback would otherwise have this surface's window dragged
// across its live frame.
func (m *Model) wheelPreview(action mouse.MouseAction) tea.Cmd {
	return tty.WheelHandler{
		Burst: &m.previewTerminalLeaf().Wheel,
		// A forwarded notch is input, and input to a pane is gated exactly as
		// typing is.
		WritesEnabled: features.IsEnabled(features.TmuxInteractiveInput.Name),
		MouseReporting: func() bool {
			return m.previewTerminalActive() && m.previewTerminalState().terminal.PaneMouseReporting()
		},
		PaneCoords: m.previewPaneCoords,
		PinToLive:  m.pinPreviewToLive,
		// The notch is user input, and the pane's capture cadence decays from the
		// component's own clock: a pane being scrolled is being read.
		NoteActivity: m.notePreviewInput,
		SendNotches: func(up bool, col, row, notches int) tea.Cmd {
			return m.previewTerminalState().terminal.SendWheelNotches(up, col, row, notches)
		},
		ScrollLocal: m.scrollPreviewByWheel,
		OnHold:      func() { m.reuseWorkspacesViewOnce = true },
	}.Handle(tty.WheelGesture{
		Delta: action.Delta, X: action.X, Y: action.Y,
		Shift: action.Shift, Alt: action.Alt, Now: m.now(),
	})
}

// previewWheelAtBoundary is the read-only half of wheelPreview used by the
// Bubble Tea input filter. An application that requested mouse reports is never
// treated as bounded here: its internal viewport is not Sidecar state. Local
// scrollback can drop at the live edge, and at the oldest loaded row only after
// tmux has confirmed there is no older history to fetch.
func (m *Model) previewWheelAtBoundary(action mouse.MouseAction) bool {
	_, _, inPane := m.previewPaneCoords(action.X, action.Y)
	route, _ := tty.RouteWheel(tty.WheelInput{
		Delta:          action.Delta,
		Shift:          action.Shift,
		Alt:            action.Alt,
		MouseReporting: m.previewTerminalActive() && m.previewTerminalState().terminal.PaneMouseReporting(),
		InPane:         inPane,
		WritesEnabled:  features.IsEnabled(features.TmuxInteractiveInput.Name),
	})
	if route == tty.WheelPane {
		return false
	}
	maximum := m.previewMaxOffset()
	position := maximum - m.previewTerminalLeaf().Scroll
	if m.previewTerminalLeaf().Freeze.Active() {
		position = m.previewTerminalLeaf().Freeze.Start()
	}
	boundary := (sharedscroll.Bounds{Position: position, Maximum: maximum}).AtBoundary(action.Delta)
	if !boundary {
		return false
	}
	if action.Delta < 0 && m.previewTerminalActive() && !m.previewTerminalLeaf().History.Exhausted {
		return false
	}
	m.previewTerminalLeaf().Wheel.Reset()
	return true
}

// notePreviewInput records input this surface delivered to the pane against the
// clock the component's capture cadence decays from.
func (m *Model) notePreviewInput() {
	if m.previewTerminalState().terminal == nil {
		return
	}
	m.previewTerminalState().terminal.NoteInput()
}

// scrollPreviewByWheel moves this surface's own window by a coalesced notch, and
// reaches for older history when a scroll up had nowhere left to go.
func (m *Model) scrollPreviewByWheel(delta int) tea.Cmd {
	m.clearPreviewSelectionOnScroll()
	before := m.previewScrollAnchor()
	// A notch counts up the screen and the window counts back from the live
	// bottom; the shared rule owns that reconciliation.
	m.scrollPreviewRows(delta)
	if delta > 0 && m.previewTerminalLeaf().Scroll == 0 {
		// Back at the live edge: whatever older history the reader was reaching
		// for is no longer where they are looking.
		m.previewTerminalLeaf().History.Cancel()
	}
	if delta < 0 && m.previewScrollAnchor() == before {
		return m.reachOlderPreviewHistory(-delta)
	}
	return nil
}

// notePreviewScrollbackLimit says out loud that tmux has no more history for
// this pane.
//
// It used to mean that this surface gave up at its own capture bound, which was
// a fact about the browser rather than about the pane. The reach now ends where
// tmux's history ends, so what is left to say is the same thing the project
// surface says, in the same words.
func (m *Model) notePreviewScrollbackLimit() tea.Cmd {
	if !m.previewTerminalLeaf().History.NoteEnd() {
		return nil
	}
	return appmsg.ShowFlash(tty.HistoryExhaustedNotice)
}

// WorkspacesTerminalMsg offers every active terminal in the current row one of
// the terminal component's own messages. Every message is scope-tagged, so the
// owning activation acts and its peers ignore it. Routing only to the focused
// leaf would make an unfocused peer miss output, control and session events.
func (m *Model) WorkspacesTerminalMsg(msg tea.Msg) tea.Cmd {
	if !tty.IsTerminalMessage(msg) {
		return nil
	}
	// A live pane editor is the surface's second embedded terminal, and its
	// messages arrive on the same bus, scope-tagged the same way.
	cmds := []tea.Cmd{m.PreviewDocEditMsg(msg)}
	var terminals []previewTerminal
	if m.preview.terminalPanes != nil {
		m.preview.terminalPanes.Range(func(_ int, leaf *termpanes.Leaf) bool {
			state, _ := leaf.HostState.(*previewTerminalState)
			if state != nil && state.terminal != nil && state.terminal.IsActive() {
				terminals = append(terminals, state.terminal)
			}
			return true
		})
	}
	for _, terminal := range terminals {
		cmds = append(cmds, terminal.Update(msg))
	}
	return tea.Batch(cmds...)
}

// WorkspacesTerminalKeySequence routes an unparsed CSI sequence — a modified
// key like shift+enter, which never becomes a KeyPressMsg — into a live pane,
// and reports whether it took it. Without it those keys would be dropped on
// their way past the global scope, and the pane would see a plain enter.
func (m *Model) WorkspacesTerminalKeySequence(msg tea.Msg) (bool, tea.Cmd) {
	if !m.PreviewInteractive() {
		return false, nil
	}
	return true, m.previewTerminalState().terminal.SendUnknownSequence(msg)
}

// WorkspacesTerminalPaste routes bracketed-paste content into a live pane and
// reports whether it took it. A paste with no terminal up belongs to the filter
// or to nobody.
func (m *Model) WorkspacesTerminalPaste(content string) (bool, tea.Cmd) {
	if !m.PreviewInteractive() || content == "" {
		return false, nil
	}
	return true, m.forwardToTerminal(tea.PasteMsg{Content: content})
}

// WorkspacesCursor is the live pane's own cursor, in tab-local coordinates. It
// is placed against the window this surface drew, not against a second one the
// terminal component rendered for itself, so the cell the cursor sits in is the
// cell a click there is forwarded to.
//
// It is nil for every other state: a watched pane has no cursor to place, and
// drawing one would invite typing into something that forwards nothing.
func (m *Model) WorkspacesCursor() *tea.Cursor {
	// A document pane hosting an inline editor draws the same kind of native
	// cursor a live terminal does, and answers first: the two are never live at
	// once, and the editor is the one with the keyboard when it is.
	if cursor := m.previewDocEditCursor(); cursor != nil {
		return cursor
	}
	if !m.PreviewInteractive() {
		return nil
	}
	window := m.previewWindow()
	if !window.ok {
		return nil
	}
	// A window scrolled off the live edge is showing history: the same rule the
	// project surface draws its cursor by, so shift+up hides it in both.
	if !tty.ShouldOverlayCursor(window.input.Interactive, window.input.CursorVisible, window.input.Follow) {
		return nil
	}
	x, y, ok := tty.ViewportCursor(window.layout, window.input)
	if !ok {
		return nil
	}
	return tty.PlaceCursor(window.surface.X+x, window.surface.Y+y)
}

// previewPeerBox is the preview panel's OUTER rectangle inside the tab — the
// peer of the list. It is the box the layout named, so the pane tree is placed,
// the terminal sized, and the cursor put against exactly what WorkspacesView
// drew. Multi-leaf layout starts here so each leaf owns its own chrome instead
// of sharing one outer frame.
func (m *Model) previewPeerBox() (termpreview.Box, bool) {
	layout := m.workspacesLayout()
	return layout.peer, layout.previewDrawn
}

// previewBox is the 1-leaf INNER box: Inset(previewPeerBox()). Multi-leaf has no
// single inner canvas — callers read previewPeerBox and previewPaneBox instead.
func (m *Model) previewBox() (termpreview.Box, bool) {
	peer, ok := m.previewPeerBox()
	if !ok {
		return termpreview.Box{}, false
	}
	return paneframe.Inset(peer), true
}

// previewSurface is the terminal viewport inside that box: the box minus the
// one header row, taken from the shared layer rather than recomputed.
func (m *Model) previewSurface() (termpreview.Surface, bool) {
	box, ok := m.previewTerminalBox()
	if !ok {
		return termpreview.Surface{}, false
	}
	surface := termpreview.SurfaceIn(box)
	if !surface.OK || surface.Width < 1 || surface.Height < 1 {
		return termpreview.Surface{}, false
	}
	return surface, true
}

// syncTerminalGeometry keeps the live pane sized to the box the layout gives
// it. Every caller is a path that moves that box — a window resize, a divider
// drag, the sidebar toggle — because the terminal has no other way to learn its
// new size: an idle pane under control mode emits nothing to ride along with.
// SetDimensions is a no-op when nothing moved.
func (m *Model) syncTerminalGeometry() tea.Cmd {
	var cmds []tea.Cmd
	if m.preview.terminalPanes != nil {
		m.preview.terminalPanes.Range(func(id int, _ *termpanes.Leaf) bool {
			cmds = append(cmds, m.syncTerminalLeafGeometry(id))
			return true
		})
	}
	return tea.Batch(cmds...)
}

// previewTerminalSize is the pane size this box can actually draw: the surface
// minus the column the scrollbar reserves. A pane sized to the full surface
// would be one column wider than what is drawn, so it would be permanently
// clipped and every forwarded click near the right edge would land a column off.
func (m *Model) previewTerminalSize() (width, height int, ok bool) {
	return m.terminalLeafSize(m.previewTerminalLeaf().ID)
}

// interactiveHints is the header's right region while the pane is live. It says
// the two things a user in this mode needs: that keys are going to the pane, and
// how to stop.
func (m *Model) interactiveHints() string {
	return "typing · " + m.InteractiveExitKey() + " or esc esc to stop"
}
