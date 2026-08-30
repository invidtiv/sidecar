package overview

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	appmsg "github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/panereposition"
)

func (m *Model) paneLayoutScope() string {
	if m == nil {
		return ""
	}
	return fmt.Sprintf("sessions:%s:%d", m.preview.workspaceID, m.preview.contentEpoch)
}

func (m *Model) openPaneLayoutModal(leafID int) tea.Cmd {
	if m == nil || m.preview.paneRoot == nil {
		return nil
	}
	peer, ok := m.previewPeerBox()
	if !ok {
		return nil
	}
	node := panelayout.Find(m.preview.paneRoot, leafID)
	if node == nil || node.Split != nil {
		return nil
	}
	// This synchronously releases local input and the remote ownership lease
	// before the controller takes its first draft snapshot.
	release := m.exitPreviewInteractive()
	m.paneLayoutModal = panereposition.NewController(
		m.paneLayoutScope(), m.preview.paneRoot, leafID, peer, previewPaneFloors(),
		m.paneZoom.Active(m.paneLayoutScope(), m.preview.paneRoot, leafID), node.Kind.Name(),
	)
	return release
}

func (m *Model) handlePaneLayoutModalKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.paneLayoutModal == nil {
		return nil
	}
	result, cmd := m.paneLayoutModal.HandleKey(msg)
	return tea.Batch(cmd, m.applyPaneLayoutModalResult(result))
}

func (m *Model) handlePaneLayoutModalMouse(msg tea.MouseMsg) tea.Cmd {
	if m.paneLayoutModal == nil || m.workspacesMouse == nil {
		return nil
	}
	return m.applyPaneLayoutModalResult(m.paneLayoutModal.HandleMouse(msg, m.workspacesMouse))
}

func (m *Model) applyPaneLayoutModalResult(result panereposition.ModalResult) tea.Cmd {
	if result.Reason != "" {
		return appmsg.ShowToast(result.Reason, 3*time.Second)
	}
	switch result.Action {
	case panereposition.ModalCancel:
		m.paneLayoutModal = nil
	case panereposition.ModalCommit:
		controller := m.paneLayoutModal
		peer, ok := m.previewPeerBox()
		if controller == nil || !ok {
			m.paneLayoutModal = nil
			return appmsg.ShowToast(panereposition.LayoutChangedReason, 3*time.Second)
		}
		if m.preview.deck != nil && !m.preview.deck.CanAdoptLayout(controller.Draft()) {
			m.paneLayoutModal = nil
			return appmsg.ShowToast(panereposition.LayoutChangedReason, 3*time.Second)
		}
		commit := controller.Commit(m.paneLayoutScope(), m.preview.paneRoot, peer, previewPaneFloors())
		if commit.Reason != "" {
			m.paneLayoutModal = nil
			return appmsg.ShowToast(commit.Reason, 3*time.Second)
		}
		m.preview.paneRoot, m.preview.paneFocus = commit.Root, commit.Focus
		if m.preview.deck != nil {
			if !m.preview.deck.AdoptLayout(commit.Root) {
				m.paneLayoutModal = nil
				return appmsg.ShowToast(panereposition.LayoutChangedReason, 3*time.Second)
			}
			m.preview.deck.FocusLeaf(commit.Focus)
		}
		if commit.ZoomLeaf != 0 {
			m.paneZoom.Set(m.paneLayoutScope(), m.preview.paneRoot, commit.ZoomLeaf)
		} else {
			m.paneZoom.Reset()
		}
		m.paneLayoutModal = nil
		m.persistSessionsLayout()
		return m.syncTerminalGeometry()
	}
	return nil
}
