package workspace

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/panereposition"
)

func (p *Plugin) paneLayoutModalScope() string {
	if p == nil {
		return ""
	}
	epoch := uint64(0)
	if p.ctx != nil {
		epoch = p.ctx.Epoch
	}
	return fmt.Sprintf("workspace:%d:%s", epoch, p.paneLayoutSurface)
}

func (p *Plugin) openPaneLayoutModal(leafID int) tea.Cmd {
	if p == nil || p.paneRoot == nil || (p.viewMode != ViewModeList && p.viewMode != ViewModeInteractive) {
		return nil
	}
	peer, ok := p.previewPeerBox()
	if !ok {
		return nil
	}
	leaf := panelayout.Find(p.paneRoot, leafID)
	if leaf == nil || leaf.Split != nil {
		return nil
	}
	// exitInteractiveMode synchronously releases PTY ownership and any remote
	// geometry lease before the controller snapshots its first draft.
	p.exitInteractiveMode()
	p.paneLayoutModal = panereposition.NewController(
		p.paneLayoutModalScope(), p.paneRoot, leafID, peer, paneTreeFloors(),
		p.paneZoom.Active(p.paneLayoutModalScope(), p.paneRoot, leafID), leaf.Kind.Name(),
	)
	return nil
}

func (p *Plugin) handlePaneLayoutModalKey(msg tea.KeyPressMsg) tea.Cmd {
	if p.paneLayoutModal == nil {
		return nil
	}
	result, cmd := p.paneLayoutModal.HandleKey(msg)
	return tea.Batch(cmd, p.applyPaneLayoutModalResult(result))
}

func (p *Plugin) handlePaneLayoutModalMouse(msg tea.MouseMsg) tea.Cmd {
	if p.paneLayoutModal == nil || p.mouseHandler == nil {
		return nil
	}
	return p.applyPaneLayoutModalResult(p.paneLayoutModal.HandleMouse(msg, p.mouseHandler))
}

func (p *Plugin) applyPaneLayoutModalResult(result panereposition.ModalResult) tea.Cmd {
	if result.Reason != "" {
		p.showPaneMoveNotice(result.Reason)
		return nil
	}
	switch result.Action {
	case panereposition.ModalCancel:
		p.paneLayoutModal = nil
	case panereposition.ModalCommit:
		controller := p.paneLayoutModal
		peer, ok := p.previewPeerBox()
		if controller == nil || !ok {
			p.paneLayoutModal = nil
			p.showPaneMoveNotice(panereposition.LayoutChangedReason)
			return nil
		}
		if p.contentDeck != nil && !p.contentDeck.CanAdoptLayout(controller.Draft()) {
			p.paneLayoutModal = nil
			p.showPaneMoveNotice(panereposition.LayoutChangedReason)
			return nil
		}
		commit := controller.Commit(p.paneLayoutModalScope(), p.paneRoot, peer, paneTreeFloors())
		if commit.Reason != "" {
			p.paneLayoutModal = nil
			p.showPaneMoveNotice(commit.Reason)
			return nil
		}
		p.paneRoot, p.paneFocus = commit.Root, commit.Focus
		if p.contentDeck != nil {
			if !p.contentDeck.AdoptLayout(commit.Root) {
				p.paneLayoutModal = nil
				p.showPaneMoveNotice(panereposition.LayoutChangedReason)
				return nil
			}
			p.contentDeck.FocusLeaf(commit.Focus)
		}
		if commit.ZoomLeaf != 0 {
			p.paneZoom.Set(p.paneLayoutModalScope(), p.paneRoot, commit.ZoomLeaf)
		} else {
			p.paneZoom.Reset()
		}
		p.paneLayoutModal = nil
		p.paneDragSplitID = 0
		p.saveSelectionState()
		return tea.Batch(p.docTerminalResizeCmds()...)
	}
	return nil
}
