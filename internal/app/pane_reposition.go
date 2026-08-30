package app

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/features"
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
	// A live inline edit is a tmux session holding a buffer that
	// releaseAppContentInputs kills outright. Every other caller of that release
	// is a surface teardown — a plugin switch, a scope change, shutdown, another
	// deck taking over — which drops `laidOut` and leaves the editor nowhere to
	// be drawn. The reposition modal is the one caller that keeps this deck laid
	// out and comes back to it, so nothing forces the buffer to die and it must
	// not be discarded unasked. Raise the same Save/Discard/Cancel dialog the
	// click-away path uses and re-enter here once the user has chosen, so the
	// door the user came through — M or the header ⊞ — cannot decide whether
	// their unsaved edit survives.
	//
	// Focus follows the editor because the dialog's keys only route while its
	// own leaf is focused; the leaf being asked about is also the one to show.
	if e := h.appContentDocumentEdit(false); e != nil && e.editing() {
		if h.deck.FocusedLeaf() != e.leafID {
			h.deck.FocusLeaf(e.leafID)
			h.syncInnerFocus()
		}
		if h.guardAppContentDocumentEdit(func() tea.Cmd { return m.openAppPaneLayoutModal(h, leafID) }) {
			m.updateContext()
			return nil
		}
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

// appPaneMoveShortcutLeaf resolves M inside an app content deck. Only a laid-out
// deck's focused passive leaf is a target: the primary plugin leaf, the deck's
// own info overlay, and an already-open modal keep the key. The plugin's list,
// input, and editor contexts never reach this rung — handleAppContentKey answers
// below every surface that types — so the modal cannot be opened from one.
//
// The deck exists at all only while PluginContentPanes is on, which is what
// keeps the entry absent when that flag is off.
func (m *Model) appPaneMoveShortcutLeaf(h *appContentDeck, leaf *panelayout.Node) int {
	if h == nil || leaf == nil || !features.IsEnabled(features.PaneMove.Name) {
		return 0
	}
	if !h.laidOut || h.root == nil || h.layoutModal != nil || h.info != nil {
		return 0
	}
	if leaf.Split != nil || leaf.Kind == panelayout.Primary {
		return 0
	}
	if target := panelayout.Find(h.root, leaf.ID); target == nil || target.Split != nil {
		return 0
	}
	return leaf.ID
}

// appPaneMoveKey is the M entry the deck's workspace-* contexts advertise. It is
// the second door onto the modal the header ⊞ already opens, never a second
// interaction.
func (m *Model) appPaneMoveKey(h *appContentDeck, leaf *panelayout.Node, key tea.KeyPressMsg) (tea.Cmd, bool) {
	if key.String() != "M" {
		return nil, false
	}
	leafID := m.appPaneMoveShortcutLeaf(h, leaf)
	if leafID == 0 {
		return nil, false
	}
	return m.openAppPaneLayoutModal(h, leafID), true
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

// reserveHeader and composeHeader bind the shared pane-header chrome to this
// deck's tree: the layout control is offered only when a leaf here can go
// somewhere, and the renderer and the region sink share one measurement.
func (h *appContentDeck) reserveHeader(width int, closable bool) panereposition.HeaderReserve {
	return panereposition.ReserveMovableHeader(width, h.paneHeaderMovable(), closable)
}

func (h *appContentDeck) composeHeader(tabsRow string, width int, closable, layoutHovered, closeHovered bool) string {
	movable := h.paneHeaderMovable()
	return panereposition.ComposeMovableHeader(tabsRow, width, movable, closable, movable && layoutHovered, closeHovered)
}

func (h *appContentDeck) paneHeaderMovable() bool {
	return h != nil && panereposition.Movable(h.root)
}
