package workspace

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/panereposition"
)

// paneLayoutShortcutLeaf resolves M from the window that owns the keyboard.
// A focused preview targets that leaf; the sidebar targets the selected row's
// Primary leaf. Text inputs and overlays keep printable keys for themselves.
func (p *Plugin) paneLayoutShortcutLeaf() int {
	if p == nil || !features.IsEnabled(features.PaneMove.Name) || p.viewMode != ViewModeList ||
		p.paneRoot == nil || p.paneLayoutModal != nil || p.docInfo != nil ||
		p.viewFlyoutActive() || p.docEditActive() || p.docSearchActive() || p.docFindActive() ||
		p.terminalSearch.InputActive || p.filterFocused() {
		return 0
	}
	var leaf *panelayout.Node
	switch p.activePane {
	case PaneSidebar:
		leaf = panelayout.FirstOfKind(p.paneRoot, panelayout.Terminal)
	case PanePreview:
		leaf = panelayout.Find(p.paneRoot, p.paneFocus)
	}
	if leaf == nil || leaf.Split != nil {
		return 0
	}
	return leaf.ID
}

// handlePaneMoveKey keeps the existing command ID and feature flag while M's
// user contract is the transactional reposition modal exposed by the header.
func (p *Plugin) handlePaneMoveKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	if msg.String() != "M" {
		return false, nil
	}
	leafID := p.paneLayoutShortcutLeaf()
	if leafID == 0 {
		return false, nil
	}
	return true, p.openPaneLayoutModal(leafID)
}

// LayoutMoveModalOpenReason declines an agent move while a human is mid-draft
// in the reposition modal. The draft is validated against the tree it was
// opened on; a structural edit underneath it would silently invalidate the
// user's work rather than merge with it.
const LayoutMoveModalOpenReason = "the reposition modal is open on that surface; finish or cancel it first"

// layoutMoveFocusedLeaf answers `layout move --focused` with the same leaf M
// resolves: the focused preview pane, or the selected row's Primary terminal
// when the list has focus. One key and one flag naming two different panes is
// exactly the drift the shared planner exists to prevent.
//
// It deliberately drops the keyboard's input-mode gating. Those guards exist so
// a printable key typed into a filter is not stolen; a CLI call is not
// competing for the keyboard, and only a live modal draft actually conflicts.
func (p *Plugin) layoutMoveFocusedLeaf() int {
	if p == nil || p.paneRoot == nil {
		return 0
	}
	var leaf *panelayout.Node
	if p.activePane == PaneSidebar {
		leaf = panelayout.FirstOfKind(p.paneRoot, panelayout.Terminal)
	} else {
		leaf = panelayout.Find(p.paneRoot, p.paneFocus)
	}
	if leaf == nil || leaf.Split != nil {
		return 0
	}
	return leaf.ID
}

// commitLayoutMove installs an accepted plan through the same path the modal
// commits through: identity-preserving apply, deck adoption asked before the
// live tree is touched, zoom that follows its leaf, persistence, and the
// existing terminal geometry synchronizer. No second structural implementation
// and no ad hoc resize.
func (p *Plugin) commitLayoutMove(plan panelayout.MovePlan) (string, tea.Cmd) {
	if p == nil || p.paneRoot == nil {
		return panereposition.LayoutChangedReason, nil
	}
	if p.paneLayoutModal != nil {
		return LayoutMoveModalOpenReason, nil
	}
	if p.contentDeck != nil && !p.contentDeck.CanAdoptLayout(panereposition.TrialMove(p.paneRoot, plan)) {
		return panereposition.LayoutChangedReason, nil
	}
	zoomLeaf := p.paneZoom.Leaf(p.paneLayoutModalScope(), p.paneRoot)
	root, focus, reason := panereposition.ApplyLive(p.paneRoot, plan)
	if reason != "" {
		return reason, nil
	}
	p.paneRoot = root
	if p.paneFocus == plan.LeafID {
		// An agent move never takes focus from a pane the user is in; it only
		// follows the leaf it was already on.
		p.paneFocus = focus
	}
	if p.contentDeck != nil {
		if !p.contentDeck.AdoptLayout(root) {
			return panereposition.LayoutChangedReason, nil
		}
		p.contentDeck.FocusLeaf(p.paneFocus)
	}
	p.paneZoom.Set(p.paneLayoutModalScope(), p.paneRoot, zoomLeaf)
	p.paneDragSplitID = 0
	p.saveSelectionState()
	return "", tea.Batch(p.docTerminalResizeCmds()...)
}

func (p *Plugin) showPaneMoveNotice(reason string) {
	p.toastMessage = panereposition.Reason(reason)
	p.toastTime = time.Now()
}

// reserveHeader and composeHeader are this surface's single binding of the
// shared pane-header chrome to its own tree: the layout control is offered
// only when a leaf here can actually go somewhere. Every header renderer and
// the region sink go through these, so the drawn glyph and its hit box are
// always measured from the same answer.
func (p *Plugin) reserveHeader(width int, closable bool) panereposition.HeaderReserve {
	return panereposition.ReserveMovableHeader(width, p.paneHeaderMovable(), closable)
}

func (p *Plugin) composeHeader(tabsRow string, width int, closable, layoutHovered, closeHovered bool) string {
	movable := p.paneHeaderMovable()
	return panereposition.ComposeMovableHeader(tabsRow, width, movable, closable, movable && layoutHovered, closeHovered)
}

func (p *Plugin) paneHeaderMovable() bool {
	return p != nil && panereposition.Movable(p.paneRoot)
}
