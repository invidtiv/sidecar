package app

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	appmsg "github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/panereposition"
	"github.com/marcus/sidecar/internal/ui"
)

func (h *appContentDeck) layoutScope() string {
	if h == nil || h.deck == nil {
		return ""
	}
	return fmt.Sprintf("app:%s:%d", h.key, h.deck.Context().Epoch)
}

func (h *appContentDeck) syncLayoutProjection() {
	if h == nil || h.deck == nil || h.layoutModal != nil {
		return
	}
	fresh := h.deck.Tree()
	if h.root == nil || panereposition.Fingerprint(h.root) != panereposition.Fingerprint(fresh) {
		h.root = fresh
	}
}

func (m *Model) activePaneLayoutController() (*appContentDeck, *panereposition.Controller) {
	h := m.currentContentDeck()
	if h == nil || h.layoutModal == nil {
		return nil, nil
	}
	return h, h.layoutModal
}

func (m *Model) openAppPaneLayoutModal(h *appContentDeck, leafID int) tea.Cmd {
	if h == nil || h.deck == nil || h.root == nil || !h.laidOut {
		return nil
	}
	leaf := panelayout.Find(h.root, leafID)
	if leaf == nil || leaf.Split != nil || leaf.Kind == panelayout.Primary {
		return nil
	}
	// Release every deck-owned editor/search surface before the modal starts;
	// the primary app content deck has no terminal lease of its own.
	h.releaseAppContentInputs()
	h.layoutModal = panereposition.NewController(
		h.layoutScope(), h.root, leafID, h.canvas, appDeckFloors(),
		h.zoom.Active(h.layoutScope(), h.root, leafID), leaf.Kind.Name(),
	)
	m.updateContext()
	return nil
}

func (m *Model) handleAppPaneLayoutKey(msg tea.KeyPressMsg) tea.Cmd {
	h, controller := m.activePaneLayoutController()
	if controller == nil {
		return nil
	}
	result, cmd := controller.HandleKey(msg)
	return tea.Batch(cmd, m.applyAppPaneLayoutResult(h, result))
}

func (m *Model) handleAppPaneLayoutMouse(msg tea.MouseMsg) tea.Cmd {
	h, controller := m.activePaneLayoutController()
	if controller == nil || h.mouse == nil {
		return nil
	}
	return m.applyAppPaneLayoutResult(h, controller.HandleMouse(msg, h.mouse))
}

func (m *Model) applyAppPaneLayoutResult(h *appContentDeck, result panereposition.ModalResult) tea.Cmd {
	if h == nil || h.layoutModal == nil {
		return nil
	}
	if result.Reason != "" {
		return appmsg.ShowToast(result.Reason, 3*time.Second)
	}
	switch result.Action {
	case panereposition.ModalCancel:
		h.layoutModal = nil
		m.updateContext()
	case panereposition.ModalCommit:
		controller := h.layoutModal
		if !h.deck.CanAdoptLayout(controller.Draft()) {
			h.layoutModal = nil
			m.updateContext()
			return appmsg.ShowToast(panereposition.LayoutChangedReason, 3*time.Second)
		}
		commit := controller.Commit(h.layoutScope(), h.root, h.canvas, appDeckFloors())
		if commit.Reason != "" {
			h.layoutModal = nil
			m.updateContext()
			return appmsg.ShowToast(commit.Reason, 3*time.Second)
		}
		if !h.deck.AdoptLayout(commit.Root) {
			h.layoutModal = nil
			m.updateContext()
			return appmsg.ShowToast(panereposition.LayoutChangedReason, 3*time.Second)
		}
		h.root = commit.Root
		if commit.ZoomLeaf != 0 {
			h.zoom.Set(h.layoutScope(), h.root, commit.ZoomLeaf)
		} else {
			h.zoom.Reset()
		}
		h.layoutModal = nil
		h.deck.FocusLeaf(commit.Focus)
		h.syncInnerFocus()
		m.persistAppContentDeck(h)
		m.updateContext()
	}
	return nil
}

func (m Model) renderAppPaneLayoutOverlay(background string) string {
	h, controller := (&m).activePaneLayoutController()
	if controller == nil || h.mouse == nil {
		return background
	}
	return ui.OverlayModal(background, controller.Render(m.width, m.height, h.mouse), m.width, m.height)
}
