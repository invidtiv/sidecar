package workspace

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/state"
)

type contentPaneOpen struct {
	kind     PaneKind
	name     string
	reopen   func() tea.Cmd
	attach   func(int) tea.Cmd
	attached func(int) bool
}

func (p *Plugin) openContentPane(spec contentPaneOpen) tea.Cmd {
	reopen := spec.reopen()
	plan, ok := p.planOpen(spec.kind)
	if !ok {
		return reopen
	}
	if plan.Retarget != 0 {
		leaf := FindPane(p.paneRoot, plan.Retarget)
		if leaf == nil || leaf.Split != nil {
			return reopen
		}
		cmd := spec.attach(leaf.ContentID)
		if !spec.attached(leaf.ContentID) {
			return reopen
		}
		p.paneFocus, p.activePane = leaf.ID, PanePreview
		p.saveSelectionState()
		return tea.Batch(reopen, cmd)
	}
	peer, placed := p.previewPeerBox()
	if !placed {
		return reopen
	}
	id := p.paneNextID
	newLeaf := &PaneNode{ID: id, Kind: spec.kind, ContentID: id}
	trial, focus := SplitLeaf(clonePaneTree(p.paneRoot), plan.Split, plan.Axis, clonePaneTree(newLeaf))
	if focus != id {
		return reopen
	}
	if _, _, fits := LayoutPanes(trial, peer, paneTreeFloors()); !fits {
		p.toastMessage, p.toastTime = paneFitMessage(spec.name, plan.Axis), time.Now()
		return reopen
	}
	p.paneRoot, p.paneFocus = SplitLeaf(p.paneRoot, plan.Split, plan.Axis, newLeaf)
	if p.paneFocus != id {
		return reopen
	}
	p.paneNextID = maxInt(p.paneNextID, maxPaneID(p.paneRoot)+1)
	p.activePane = PanePreview
	cmd := spec.attach(id)
	p.saveSelectionState()
	return tea.Batch(reopen, cmd, p.resizeDocTerminalCmd())
}

func (p *Plugin) hideContentPane(leafID int) tea.Cmd {
	root, surface, ok := p.selectedTerminalSurface()
	if ok {
		p.rememberHiddenPaneLayout(root, surface)
	}
	if !p.closeContentLeaf(leafID) {
		p.hiddenPaneLayout = nil
		return nil
	}
	p.activePane = PanePreview
	p.saveSelectionState()
	return p.resizeDocTerminalCmd()
}

func (p *Plugin) reopenHiddenContentPane(kind PaneKind, active bool, hasTabs func(*state.PaneLayoutJSON) bool, savedKind, name string) tea.Cmd {
	if active {
		return nil
	}
	_, surface, ok := p.selectedTerminalSurface()
	if !ok {
		return nil
	}
	layout := p.hiddenLayoutFor(surface)
	if layout == nil || !hasTabs(layout) {
		return nil
	}
	if p.liveContentBesides(kind) {
		return p.reinsertHiddenContentLeaf(kind, firstLayoutLeafOfKind(layout, savedKind), name)
	}
	p.hiddenPaneLayout = nil
	return p.restorePaneLayout(layout)
}
