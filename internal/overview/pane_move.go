package overview

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/features"
	appmsg "github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/panereposition"
)

func (m *Model) paneMoveScope() string {
	return fmt.Sprintf("%s:%s", m.preview.workspaceID, panereposition.Fingerprint(m.preview.paneRoot))
}

func (m *Model) paneMoveCanStart() bool {
	if !features.IsEnabled(features.PaneMove.Name) || !m.PreviewFocused() || m.PreviewInteractive() ||
		m.renameOpen || m.createOpen || m.deleteOpen || m.WorkspacesFilterFocused() ||
		m.previewDocEditing() || m.previewDocSearchActive() || m.previewDocFindActive() {
		return false
	}
	leaf := panelayout.Find(m.preview.paneRoot, m.preview.paneFocus)
	return leaf != nil && leaf.Split == nil
}

func (m *Model) paneMoveActive() bool {
	if !m.paneMoveCanStart() {
		m.preview.paneMove.Reset()
		return false
	}
	return m.preview.paneMove.Reconcile(m.paneMoveScope(), m.preview.paneRoot)
}

// handlePaneMoveKey is the Sessions surface's thin adapter over the shared
// interaction state and panelayout's structural move policy.
func (m *Model) handlePaneMoveKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	scope := m.paneMoveScope()
	if m.paneMoveActive() {
		action := panereposition.Decode(msg.String())
		if action.Exit {
			m.preview.paneMove.Reset()
			return true, nil
		}
		if !action.Move {
			return true, nil
		}
		return true, m.moveFocusedPane(action.Direction)
	}
	if msg.String() != "M" || !m.paneMoveCanStart() {
		return false, nil
	}
	m.preview.paneMove.Start(scope, m.preview.paneFocus)
	return true, nil
}

func (m *Model) moveFocusedPane(direction panelayout.Direction) tea.Cmd {
	leafID := m.preview.paneMove.LeafID()
	destination, ok := panelayout.MoveDirection(m.preview.paneRoot, leafID, direction)
	if !ok {
		return appmsg.ShowFlash(panereposition.BoundaryReason(direction))
	}
	peer, ok := m.previewPeerBox()
	if !ok {
		return appmsg.ShowFlash(panereposition.AreaHiddenReason)
	}
	outcome := panelayout.PlanMove(m.preview.paneRoot, leafID, destination, panelayout.Box(peer), previewPaneFloors())
	if outcome.Status != panelayout.MoveMoved {
		return appmsg.ShowFlash(panereposition.Reason(outcome.Reason))
	}

	trial, _ := panelayout.ApplyMove(panelayout.Clone(m.preview.paneRoot), outcome.Plan)
	if m.preview.deck != nil && !m.preview.deck.AdoptLayout(trial) {
		return appmsg.ShowFlash(panereposition.LayoutChangedReason)
	}
	if m.preview.deck != nil {
		m.preview.deck.FocusLeaf(leafID)
	}

	source := panelayout.Find(m.preview.paneRoot, leafID)
	m.preview.paneRoot, m.preview.paneFocus = panelayout.ApplyMove(m.preview.paneRoot, outcome.Plan)
	if panelayout.Find(m.preview.paneRoot, leafID) != source || m.preview.paneFocus != leafID {
		m.preview.paneMove.Reset()
		return appmsg.ShowFlash(panereposition.LayoutChangedReason)
	}
	m.preview.paneDragSplitID = 0
	m.preview.paneMove.Start(m.paneMoveScope(), leafID)
	m.persistSessionsLayout()
	return m.syncTerminalGeometry()
}
