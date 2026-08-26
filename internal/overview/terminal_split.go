package overview

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/paneframe"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/termpanes"
	"github.com/marcus/sidecar/internal/termpreview"
	"github.com/marcus/sidecar/internal/tty"
)

var ensurePreviewTerminalSession = termpanes.EnsureSession

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

	node := &panelayout.Node{Kind: panelayout.Shell}
	m.preview.paneRoot, m.preview.paneFocus = panelayout.ApplyPlan(m.preview.paneRoot, plan, node)
	m.preview.paneNextID = panelayout.MaxID(m.preview.paneRoot) + 1
	leaf := m.terminalLeaf(node.ID)
	leaf.Requested = true
	leaf.Name = strings.TrimSpace(m.createForm.TerminalName())
	if leaf.Name == "" {
		leaf.Name = "Terminal"
	}
	leaf.Session = termpanes.SessionName(workspace.TmuxName)
	leaf.Target.Source = "shell"
	leaf.Target.SourceID = workspace.ID
	m.createBusy = true
	m.setCreateError("")
	m.createModal = nil
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
	return tea.Batch(m.syncTerminalLeaf(msg.LeafID), m.syncTerminalGeometry())
}

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

// previewShellGraft records where a Shell leaf sat before a deck projection
// replaced the tree: its leaf ID, the split it lived in, and the sibling the
// split was made against.
type previewShellGraft struct {
	leafID     int
	anchorID   int
	axis       panelayout.Axis
	ratio      int
	shellFirst bool
}

func capturePreviewShellGrafts(root *panelayout.Node) []previewShellGraft {
	var grafts []previewShellGraft
	var walk func(*panelayout.Node)
	walk = func(n *panelayout.Node) {
		if n == nil || n.Split == nil {
			return
		}
		a, b := n.Split.A, n.Split.B
		if a != nil && b != nil && a.Split == nil && a.Kind == panelayout.Shell {
			grafts = append(grafts, previewShellGraft{leafID: a.ID, anchorID: b.ID, axis: n.Split.Axis, ratio: n.Split.Ratio, shellFirst: true})
		}
		if a != nil && b != nil && b.Split == nil && b.Kind == panelayout.Shell {
			grafts = append(grafts, previewShellGraft{leafID: b.ID, anchorID: a.ID, axis: n.Split.Axis, ratio: n.Split.Ratio, shellFirst: false})
		}
		walk(a)
		walk(b)
	}
	walk(root)
	return grafts
}

// splicePreviewNode replaces the node with anchorID inside root with
// build(node). The root itself is the caller's case.
func splicePreviewNode(root *panelayout.Node, anchorID int, build func(*panelayout.Node) *panelayout.Node) bool {
	if root == nil || root.Split == nil {
		return false
	}
	for _, side := range []**panelayout.Node{&root.Split.A, &root.Split.B} {
		child := *side
		if child == nil {
			continue
		}
		if child.ID == anchorID {
			*side = build(child)
			return true
		}
		if splicePreviewNode(child, anchorID, build) {
			return true
		}
	}
	return false
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
	for _, graft := range capturePreviewShellGrafts(old) {
		leaf := m.preview.terminalPanes.Leaf(graft.leafID)
		if leaf == nil || leaf.Target.Source != "shell" {
			continue
		}
		shellID := graft.leafID
		// The deck allocates IDs against its own tree, so a new passive leaf
		// can take the shell's number; the shell moves, its state moves with it.
		if panelayout.Find(fresh, shellID) != nil {
			next := panelayout.MaxID(fresh) + 1
			m.preview.terminalPanes.Rekey(shellID, next)
			shellID = next
		}
		splitID := max(panelayout.MaxID(fresh), shellID) + 1
		node := &panelayout.Node{ID: shellID, Kind: panelayout.Shell}
		build := func(anchor *panelayout.Node) *panelayout.Node {
			a, b := anchor, node
			if graft.shellFirst {
				a, b = node, anchor
			}
			return &panelayout.Node{ID: splitID, Split: &panelayout.Split{Axis: graft.axis, Ratio: graft.ratio, A: a, B: b}}
		}
		if fresh.ID == graft.anchorID {
			fresh = build(fresh)
		} else if !splicePreviewNode(fresh, graft.anchorID, build) {
			fresh = build(fresh)
		}
		if wasFocused == graft.leafID {
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
