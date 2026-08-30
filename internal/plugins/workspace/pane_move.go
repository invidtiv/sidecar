package workspace

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/panereposition"
)

func (p *Plugin) paneMoveScope() string {
	epoch := uint64(0)
	if p.ctx != nil {
		epoch = p.ctx.Epoch
	}
	return fmt.Sprintf("%d:%s:%s", epoch, p.paneLayoutSurface, panereposition.Fingerprint(p.paneRoot))
}

func (p *Plugin) paneMoveCanStart() bool {
	if !features.IsEnabled(features.PaneMove.Name) || p.viewMode != ViewModeList || p.activePane != PanePreview ||
		p.docEditActive() || p.docSearchActive() || p.docFindActive() {
		return false
	}
	leaf := panelayout.Find(p.paneRoot, p.paneFocus)
	return leaf != nil && leaf.Split == nil
}

func (p *Plugin) paneMoveActive() bool {
	if !features.IsEnabled(features.PaneMove.Name) || p.viewMode != ViewModeList || p.activePane != PanePreview {
		p.paneMove.Reset()
		return false
	}
	return p.paneMove.Reconcile(p.paneMoveScope(), p.paneRoot)
}

// handlePaneMoveKey is the project surface's thin adapter over the shared
// interaction state and panelayout's structural move policy.
func (p *Plugin) handlePaneMoveKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	scope := p.paneMoveScope()
	if p.paneMoveActive() {
		action := panereposition.Decode(msg.String())
		if action.Exit {
			p.paneMove.Reset()
			return true, nil
		}
		if !action.Move {
			return true, nil
		}
		return true, p.moveFocusedPane(action.Direction)
	}
	if msg.String() != "M" || !p.paneMoveCanStart() {
		return false, nil
	}
	p.paneMove.Start(scope, p.paneFocus)
	return true, nil
}

func (p *Plugin) moveFocusedPane(direction panelayout.Direction) tea.Cmd {
	leafID := p.paneMove.LeafID()
	destination, ok := panelayout.MoveDirection(p.paneRoot, leafID, direction)
	if !ok {
		p.showPaneMoveNotice(panereposition.BoundaryReason(direction))
		return nil
	}
	peer, ok := p.previewPeerBox()
	if !ok {
		p.showPaneMoveNotice(panereposition.AreaHiddenReason)
		return nil
	}
	outcome := panelayout.PlanMove(p.paneRoot, leafID, destination, peer, paneTreeFloors())
	if outcome.Status != panelayout.MoveMoved {
		p.showPaneMoveNotice(outcome.Reason)
		return nil
	}

	// Move the deck's passive projection first. It validates the complete
	// candidate without touching the host tree and removes host-only Shell
	// leaves before adoption, so a later reconcile cannot restore the old order.
	trial, _ := panelayout.ApplyMove(panelayout.Clone(p.paneRoot), outcome.Plan)
	if p.contentDeck != nil && !p.contentDeck.AdoptLayout(trial) {
		p.showPaneMoveNotice(panereposition.LayoutChangedReason)
		return nil
	}
	if p.contentDeck != nil {
		p.contentDeck.FocusLeaf(leafID)
	}

	source := panelayout.Find(p.paneRoot, leafID)
	p.paneRoot, p.paneFocus = panelayout.ApplyMove(p.paneRoot, outcome.Plan)
	if panelayout.Find(p.paneRoot, leafID) != source || p.paneFocus != leafID {
		p.paneMove.Reset()
		p.showPaneMoveNotice(panereposition.LayoutChangedReason)
		return nil
	}
	p.paneDragSplitID = 0
	p.paneMove.Start(p.paneMoveScope(), leafID)
	p.saveSelectionState()
	return tea.Batch(p.docTerminalResizeCmds()...)
}

func (p *Plugin) showPaneMoveNotice(reason string) {
	p.toastMessage = panereposition.Reason(reason)
	p.toastTime = time.Now()
}
