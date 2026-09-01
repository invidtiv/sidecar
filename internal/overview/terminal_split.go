package overview

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/layoutapply"
	"github.com/marcus/sidecar/internal/paneframe"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/panereposition"
	"github.com/marcus/sidecar/internal/termpanes"
	"github.com/marcus/sidecar/internal/termpreview"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspaceinventory"
	"github.com/marcus/sidecar/internal/workspaceops"
)

// previewSplitSeed is a --run/--type to send after the Sessions terminal
// split's tmux session exists, matching the project plugin's termPanelSeed.
type previewSplitSeed struct {
	session string
	run     string
	typeCmd string
}

var ensurePreviewTerminalSession = termpanes.EnsureSession

// applyCreateShellSplit is the Sessions half of `sidecar create shell --split`.
// The project plugin writes `shell:<tmux>`; this surface is keyed by the
// catalog row ID. When Sessions is hidden the request is left for the project
// plugin; when it is showing, this is the surface the user is looking at.
func (m *Model) applyCreateShellSplit(req uirequest.Request, payload uirequest.CreatePayload, placement string) tea.Cmd {
	if !m.preview.visible {
		if req.Origin.Sessions {
			m.ackCreateDeclined(req, layoutapply.SessionsNotOnScreenReason)
		}
		return nil
	}
	ws, ok := m.resolveCreateSplitWorkspace(req)
	if !ok {
		m.ackCreateDeclined(req, "no Sessions row matches this shell")
		return nil
	}
	if cmd := m.focusSessionsLayoutRow(ws.ID); cmd != nil {
		_ = cmd
	}
	selected, has := m.SelectedWorkspace()
	if !has || selected.ID != ws.ID {
		m.ackCreateDeclined(req, "no Sessions row named "+ws.ID+" is on screen")
		return nil
	}
	if !features.IsEnabled(features.WorkspaceTerminalPanel.Name) {
		m.ackCreateDeclined(req, features.WorkspaceTerminalPanel.Name+" is off")
		return nil
	}
	if panelayout.LiveCapReached(m.preview.paneRoot) {
		m.ackCreateDeclined(req, termpanes.CapDisabledReason)
		return nil
	}
	plan, planned := panelayout.PlanOpen(m.preview.paneRoot, panelayout.Shell, m.lastPreviewBoxes())
	if !planned {
		m.ackCreateDeclined(req, termpanes.CapDisabledReason)
		return nil
	}
	plan = panelayout.ApplyAxisOverride(plan, placement)
	trial, _ := panelayout.ApplyPlan(panelayout.Clone(m.preview.paneRoot), plan, &panelayout.Node{Kind: panelayout.Shell})
	peer, placed := m.previewPeerBox()
	if !placed {
		m.ackCreateDeclined(req, "Terminal split needs a visible preview")
		return nil
	}
	if _, _, fits := panelayout.LayoutPanes(trial, peer, previewPaneFloors()); !fits {
		m.ackCreateDeclined(req, "the window is too small to split")
		return nil
	}
	before := panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Shell)
	session := termpanes.SessionName(selected.TmuxName)
	if payload.Run != "" || payload.Type != "" {
		m.pendingSplitSeed = &previewSplitSeed{session: session, run: payload.Run, typeCmd: payload.Type}
	}
	cmd := m.openPreviewTerminalSplit(payload.DisplayName, plan)
	after := panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Shell)
	if after == nil || after == before {
		m.pendingSplitSeed = nil
		m.ackCreateDeclined(req, "the window is too small to split")
		return nil
	}
	m.ackCreate(req, selected.ID)
	return cmd
}

func (m *Model) resolveCreateSplitWorkspace(req uirequest.Request) (workspaceinventory.Workspace, bool) {
	session := strings.TrimSpace(req.Origin.TmuxSession)
	if session == "" {
		return m.SelectedWorkspace()
	}
	for _, ws := range m.catalog {
		if ws.Remote() {
			continue
		}
		if ws.TmuxName == session {
			return ws, true
		}
	}
	return workspaceinventory.Workspace{}, false
}

type previewTerminalSplitCreatedMsg struct {
	WorkspaceID string
	LeafID      int
	Session     string
	PaneID      string
	Err         error
}

func (m *Model) createPreviewTerminalSplit() tea.Cmd {
	if !features.IsEnabled(features.WorkspaceTerminalPanel.Name) || m.createForm == nil {
		return nil
	}
	if reason := m.createForm.KindDisabledReason(); reason != "" {
		m.setCreateError(reason)
		return nil
	}
	workspace, ok := m.SelectedWorkspace()
	if !ok || workspace.ID == "" || workspace.TmuxName == "" {
		m.setCreateError("Choose a live workspace")
		return nil
	}
	if panelayout.LiveCapReached(m.preview.paneRoot) {
		m.setCreateError(termpanes.CapDisabledReason)
		return nil
	}
	plan, ok := panelayout.PlanOpen(m.preview.paneRoot, panelayout.Shell, m.lastPreviewBoxes())
	if !ok {
		m.setCreateError(termpanes.CapDisabledReason)
		return nil
	}
	plan = panelayout.ApplyAxisOverride(plan, m.createForm.PlacementSplit())
	trial, _ := panelayout.ApplyPlan(panelayout.Clone(m.preview.paneRoot), plan, &panelayout.Node{Kind: panelayout.Shell})
	peer, placed := m.previewPeerBox()
	if !placed {
		m.setCreateError("Terminal split needs a visible preview")
		return nil
	}
	if _, _, fits := panelayout.LayoutPanes(trial, peer, previewPaneFloors()); !fits {
		m.setCreateError("Terminal split needs a larger window")
		return nil
	}

	name := strings.TrimSpace(m.createForm.TerminalName())
	m.createBusy = true
	m.setCreateError("")
	m.createModal = nil
	return m.openPreviewTerminalSplit(name, plan)
}

// openPreviewTerminalSplit commits a live terminal leaf at the given plan —
// the same create path the pane-switcher modal uses after it has planned and
// fit-tested. Layout apply reuses it so a CLI split and a modal split cannot
// disagree about the tree.
func (m *Model) openPreviewTerminalSplit(name string, plan panelayout.OpenPlan) tea.Cmd {
	workspace, ok := m.SelectedWorkspace()
	if !ok || workspace.ID == "" || workspace.TmuxName == "" {
		return nil
	}
	node := &panelayout.Node{Kind: panelayout.Shell}
	if plan.Split != 0 {
		m.preview.paneRoot, m.preview.paneFocus = panelayout.ApplyPlan(m.preview.paneRoot, plan, node)
	} else {
		var planned bool
		plan, planned = panelayout.PlanOpen(m.preview.paneRoot, panelayout.Shell, m.lastPreviewBoxes())
		if !planned {
			return nil
		}
		m.preview.paneRoot, m.preview.paneFocus = panelayout.ApplyPlan(m.preview.paneRoot, plan, node)
	}
	m.preview.paneNextID = panelayout.MaxID(m.preview.paneRoot) + 1
	leaf := m.terminalLeaf(node.ID)
	leaf.Requested = true
	leaf.Name = strings.TrimSpace(name)
	if leaf.Name == "" {
		leaf.Name = "Terminal"
	}
	leaf.Session = termpanes.SessionName(workspace.TmuxName)
	leaf.Target.Source = "shell"
	leaf.Target.SourceID = workspace.ID
	m.persistSessionsLayout()
	workDir := workspace.Path
	workspaceID, leafID, session := workspace.ID, leaf.ID, leaf.Session
	return func() tea.Msg {
		paneID, err := ensurePreviewTerminalSession(session, workDir)
		return previewTerminalSplitCreatedMsg{WorkspaceID: workspaceID, LeafID: leafID, Session: session, PaneID: paneID, Err: err}
	}
}

func (m *Model) applyPreviewTerminalSplitCreated(msg previewTerminalSplitCreatedMsg) tea.Cmd {
	m.createBusy = false
	if msg.Err != nil {
		m.pendingSplitSeed = nil
	}
	leaf := m.preview.terminalPanes.Leaf(msg.LeafID)
	current := msg.WorkspaceID == m.preview.workspaceID && leaf != nil && leaf.Session == msg.Session
	if !current {
		cached, ok := m.preview.paneCache[msg.WorkspaceID]
		if !ok || cached.terminals == nil {
			return nil
		}
		leaf = cached.terminals.Leaf(msg.LeafID)
		if leaf == nil || leaf.Session != msg.Session {
			return nil
		}
		if msg.Err != nil {
			cached.root, cached.focus = panelayout.Close(cached.root, msg.LeafID)
			cached.terminals.Release(msg.LeafID)
			m.preview.paneCache[msg.WorkspaceID] = cached
			return nil
		}
		leaf.PaneID = msg.PaneID
		leaf.Target.Session, leaf.Target.Pane = msg.Session, msg.PaneID
		m.closeCreateShell()
		return nil
	}
	if msg.Err != nil {
		m.preview.paneRoot, m.preview.paneFocus = panelayout.Close(m.preview.paneRoot, msg.LeafID)
		m.preview.terminalPanes.Release(msg.LeafID)
		m.createModal = nil
		m.setCreateError(msg.Err.Error())
		m.persistSessionsLayout()
		return nil
	}
	leaf.PaneID = msg.PaneID
	leaf.Target.Session, leaf.Target.Pane = msg.Session, msg.PaneID
	m.closeCreateShell()
	m.persistSessionsLayout()
	return tea.Batch(m.syncTerminalLeaf(msg.LeafID), m.syncTerminalGeometry(), m.applyPendingSplitSeed(msg.Session))
}

func (m *Model) applyPendingSplitSeed(session string) tea.Cmd {
	seed := m.pendingSplitSeed
	if seed == nil || seed.session == "" || seed.session != session {
		return nil
	}
	m.pendingSplitSeed = nil
	run, typeCmd := seed.run, seed.typeCmd
	if run == "" && typeCmd == "" {
		return nil
	}
	return func() tea.Msg {
		ctx := context.Background()
		var err error
		if run != "" {
			err = workspaceops.StartAgentInShell(ctx, session, run)
		} else {
			err = workspaceops.TypeInShell(ctx, session, typeCmd)
		}
		if err != nil {
			return previewSplitSeedFailedMsg{Err: err}
		}
		return nil
	}
}

type previewSplitSeedFailedMsg struct{ Err error }

func (m *Model) syncTerminalLeaf(id int) tea.Cmd {
	leaf := m.terminalLeaf(id)
	if leaf.Session == "" || leaf.PaneID == "" {
		return nil
	}
	state := m.terminalState(id)
	if state.terminal == nil {
		state.terminal = newPreviewTerminal(m.TerminalConfig(), m.previewTerminalHooksFor(leaf))
	}
	desired := tty.Target{Session: leaf.Session, Pane: leaf.PaneID}
	if state.terminal.IsActive() && leaf.Target.Session == desired.Session && leaf.Target.Pane == desired.Pane {
		leaf.Buffer = state.terminal.Buffer()
		return m.syncTerminalLeafGeometry(id)
	}
	if state.terminal.IsActive() {
		state.terminal.Close()
	}
	leaf.Target.Session, leaf.Target.Pane = desired.Session, desired.Pane
	var cmds []tea.Cmd
	if width, height, ok := m.terminalLeafSize(id); ok {
		leaf.Target.Width, leaf.Target.Height = width, height
		cmds = append(cmds, state.terminal.SetDimensions(width, height))
	}
	cmds = append(cmds, state.terminal.Open(desired))
	leaf.Buffer = state.terminal.Buffer()
	return tea.Batch(cmds...)
}

func (m *Model) syncPreviewTerminals() tea.Cmd {
	cmds := []tea.Cmd{m.syncPreviewTerminal()}
	if m.preview.terminalPanes != nil {
		m.preview.terminalPanes.Range(func(id int, leaf *termpanes.Leaf) bool {
			if leaf.Target.Source == "shell" || leaf.Session != "" {
				cmds = append(cmds, m.syncTerminalLeaf(id))
			}
			return true
		})
	}
	return tea.Batch(cmds...)
}

func (m *Model) detachPreviewTerminals(preserve bool) {
	if m.preview.terminalPanes == nil {
		return
	}
	m.preview.terminalPanes.Range(func(_ int, leaf *termpanes.Leaf) bool {
		state, _ := leaf.HostState.(*previewTerminalState)
		if state != nil && state.terminal != nil {
			if buffer := state.terminal.Buffer(); buffer != nil {
				leaf.Buffer = buffer
			}
			state.terminal.ReleaseInput()
			if state.terminal.IsActive() {
				state.terminal.Close()
			}
		}
		leaf.Interactive = false
		leaf.Pointer.Abandon()
		leaf.Pointer.ResetUnit()
		leaf.Wheel.Reset()
		if state != nil {
			state.termBar = previewTermBar{}
		}
		if !preserve {
			leaf.Buffer = nil
			leaf.Scroll = 0
			leaf.History = tty.HistoryReach{}
		}
		return true
	})
}

// pruneDeletedTerminalRows releases peer state whose owning catalog row no
// longer exists. Passive pane caches keep their existing policy; this only
// prevents a removed row from retaining a live terminal adapter indefinitely.
func (m *Model) pruneDeletedTerminalRows() {
	for workspaceID, cached := range m.preview.paneCache {
		if _, exists := m.catalog[workspaceID]; exists || cached.terminals == nil {
			continue
		}
		var peerIDs []int
		cached.terminals.Range(func(id int, leaf *termpanes.Leaf) bool {
			if leaf.Target.Source == "shell" {
				peerIDs = append(peerIDs, id)
			}
			return true
		})
		for _, id := range peerIDs {
			if leaf := cached.terminals.Leaf(id); leaf != nil {
				if state, ok := leaf.HostState.(*previewTerminalState); ok && state != nil && state.terminal != nil {
					state.terminal.ReleaseInput()
					if state.terminal.IsActive() {
						state.terminal.Close()
					}
				}
			}
			cached.terminals.Release(id)
		}
		m.preview.paneCache[workspaceID] = cached
	}
}

// graftPreviewShellLeaves restores this row's live terminal splits onto a tree
// the content deck produced. The deck's tree is the passive content panes' —
// it has never heard of a Shell leaf — so adopting it verbatim would silently
// close a running terminal, which is what once made opening a document replace
// a terminal split. Each split goes back beside the sibling it was made
// against, at its remembered axis and ratio; a sibling the projection removed
// falls back to wrapping the whole tree. It returns the tree and the leaf ID
// of the focused shell, when the focus was a shell that survived.
func (m *Model) graftPreviewShellLeaves(old, fresh *panelayout.Node) (*panelayout.Node, int) {
	if fresh == nil || m.preview.terminalPanes == nil {
		return fresh, 0
	}
	focusShell := 0
	wasFocused := m.preview.paneFocus
	for _, graft := range panereposition.CaptureLeafGrafts(old, panelayout.Shell) {
		oldShellID := graft.LeafID
		leaf := m.preview.terminalPanes.Leaf(graft.LeafID)
		if leaf == nil || leaf.Target.Source != "shell" {
			continue
		}
		shellID := graft.LeafID
		// The deck allocates IDs against its own tree, so a new passive leaf
		// can take the shell's number; the shell moves, its state moves with it.
		if panelayout.Find(fresh, shellID) != nil {
			next := panelayout.MaxID(fresh) + 1
			m.preview.terminalPanes.Rekey(shellID, next)
			shellID = next
		}
		node := &panelayout.Node{ID: shellID, Kind: panelayout.Shell}
		graft.LeafID = shellID
		fresh = panereposition.ApplyLeafGraft(fresh, graft, node)
		if wasFocused == oldShellID {
			focusShell = shellID
		}
	}
	return fresh, focusShell
}

func (m *Model) terminalLeafBox(id int) (termpreview.Box, bool) {
	peer, ok := m.previewPeerBox()
	if !ok {
		return termpreview.Box{}, false
	}
	layout, ok := m.layoutPreviewPanes(peer)
	if !ok {
		return termpreview.Box{}, false
	}
	for _, placed := range layout.Leaves {
		if placed.Node != nil && placed.Node.ID == id {
			return paneframe.Inset(placed.Box), true
		}
	}
	return termpreview.Box{}, false
}

func (m *Model) terminalLeafSize(id int) (int, int, bool) {
	box, ok := m.terminalLeafBox(id)
	if !ok {
		return 0, 0, false
	}
	surface := termpreview.SurfaceIn(box)
	if !surface.OK {
		return 0, 0, false
	}
	return tty.ContentWidth(surface.Width), surface.Height, true
}

func (m *Model) syncTerminalLeafGeometry(id int) tea.Cmd {
	state := m.terminalState(id)
	if state.terminal == nil || !state.terminal.IsActive() {
		return nil
	}
	width, height, ok := m.terminalLeafSize(id)
	if !ok {
		return nil
	}
	leaf := m.terminalLeaf(id)
	leaf.Target.Width, leaf.Target.Height = width, height
	return state.terminal.SetDimensions(width, height)
}
