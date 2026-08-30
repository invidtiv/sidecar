package overview

import (
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/panereposition"
)

// LayoutMoveModalOpenReason declines an agent move while a human is mid-draft
// in the reposition modal, for the reason the project surface states.
const LayoutMoveModalOpenReason = "the reposition modal is open on that surface; finish or cancel it first"

// paneLayoutShortcutLeaf resolves M from the active Workspaces window. The
// preview targets its focused leaf; the list targets the selected row's
// Primary leaf. Input and overlay states retain printable keys.
func (m *Model) paneLayoutShortcutLeaf() int {
	if m == nil || !features.IsEnabled(features.PaneMove.Name) || m.preview.paneRoot == nil ||
		m.paneLayoutModal != nil || m.PreviewInteractive() || m.renameOpen || m.createOpen ||
		m.deleteOpen || m.viewFlyoutOpen || m.WorkspacesFilterFocused() ||
		m.previewDocEditing() || m.previewDocSearchActive() || m.previewDocFindActive() ||
		m.terminalSearch.InputActive {
		return 0
	}
	var leaf *panelayout.Node
	if m.PreviewFocused() {
		leaf = panelayout.Find(m.preview.paneRoot, m.preview.paneFocus)
	} else {
		leaf = panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Terminal)
	}
	if leaf == nil || leaf.Split != nil {
		return 0
	}
	return leaf.ID
}

// layoutMoveFocusedLeaf answers `layout move --focused` with the same leaf M
// resolves on this surface: the focused preview pane, or the selected row's
// Primary terminal when the list has focus.
//
// The keyboard's input-mode gating is deliberately dropped — a CLI call is not
// competing for the keyboard — while a live modal draft still conflicts and is
// refused by commitLayoutMove.
func (m *Model) layoutMoveFocusedLeaf() int {
	if m == nil || m.preview.paneRoot == nil {
		return 0
	}
	var leaf *panelayout.Node
	if m.PreviewFocused() {
		leaf = panelayout.Find(m.preview.paneRoot, m.preview.paneFocus)
	} else {
		leaf = panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Terminal)
	}
	if leaf == nil || leaf.Split != nil {
		return 0
	}
	return leaf.ID
}

// commitLayoutMove installs an accepted plan through the same path the modal
// commits through.
//
// A remote row is moved exactly like a local one, and only here: this changes
// the LOCAL viewer's pane tree. No layout mutation is sent to the machine the
// workspace lives on, and browse-state geometry goes through the existing
// synchronizer, which does not resize a remote tmux server.
func (m *Model) commitLayoutMove(plan panelayout.MovePlan) (string, tea.Cmd) {
	if m == nil || m.preview.paneRoot == nil {
		return panereposition.LayoutChangedReason, nil
	}
	if m.paneLayoutModal != nil {
		return LayoutMoveModalOpenReason, nil
	}
	if m.preview.deck != nil && !m.preview.deck.CanAdoptLayout(panereposition.TrialMove(m.preview.paneRoot, plan)) {
		return panereposition.LayoutChangedReason, nil
	}
	zoomLeaf := m.paneZoom.Leaf(m.paneLayoutScope(), m.preview.paneRoot)
	root, focus, reason := panereposition.ApplyLive(m.preview.paneRoot, plan)
	if reason != "" {
		return reason, nil
	}
	m.preview.paneRoot = root
	if m.preview.paneFocus == plan.LeafID {
		m.preview.paneFocus = focus
	}
	if m.preview.deck != nil {
		if !m.preview.deck.AdoptLayout(root) {
			return panereposition.LayoutChangedReason, nil
		}
		m.preview.deck.FocusLeaf(m.preview.paneFocus)
	}
	m.paneZoom.Set(m.paneLayoutScope(), m.preview.paneRoot, zoomLeaf)
	m.persistSessionsLayout()
	return "", m.syncTerminalGeometry()
}

func (m *Model) handlePaneMoveKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	if msg.String() != "M" {
		return false, nil
	}
	leafID := m.paneLayoutShortcutLeaf()
	if leafID == 0 {
		return false, nil
	}
	return true, m.openPaneLayoutModal(leafID)
}

// reserveHeader and composeHeader bind the shared pane-header chrome to this
// surface's preview tree, so the layout control is offered only when a leaf
// here can go somewhere and the glyph and its hit box share one measurement.
func (m *Model) reserveHeader(width int, closable bool) panereposition.HeaderReserve {
	return panereposition.ReserveMovableHeader(width, m.paneHeaderMovable(), closable)
}

func (m *Model) composeHeader(tabsRow string, width int, closable, layoutHovered, closeHovered bool) string {
	movable := m.paneHeaderMovable()
	return panereposition.ComposeMovableHeader(tabsRow, width, movable, closable, movable && layoutHovered, closeHovered)
}

func (m *Model) paneHeaderMovable() bool {
	return m != nil && panereposition.Movable(m.preview.paneRoot)
}
