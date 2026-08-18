package workspace

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/state"
)

// contentPaneOpen is one kind's half of the open dance. Placement, the trial
// split, the refusal toast, focus and the persist are the shared helper's;
// what a kind still answers is how its content attaches to a leaf and whether
// that attach actually took.
//
// attach is told whether the leaf is fresh, because a retarget onto a leaf
// whose content is gone is not the same event as a brand new split: the doc
// pane declines the first and builds its pane on the second.
type contentPaneOpen struct {
	kind     PaneKind
	name     string
	reopen   func() tea.Cmd
	attach   func(id int, fresh bool) tea.Cmd
	attached func(int) bool
	// planned, when set, is handed the placement before it is acted on. It
	// exists for debug logging; it must not mutate the tree.
	planned func(paneOpen)
}

func (p *Plugin) openContentPane(spec contentPaneOpen) tea.Cmd {
	reopen := spec.reopen()
	plan, ok := p.planOpen(spec.kind)
	if !ok {
		return reopen
	}
	if spec.planned != nil {
		spec.planned(plan)
	}
	if plan.Retarget != 0 {
		leaf := FindPane(p.paneRoot, plan.Retarget)
		if leaf == nil || leaf.Split != nil {
			return reopen
		}
		cmd := spec.attach(leaf.ContentID, false)
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
	cmd := spec.attach(id, true)
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

// forgetContentPane forgets a content leaf outright: unlike hide, the hidden
// snapshot goes with it, which is what makes last-x forget the tab set that
// q/esc keeps. closeContentPane is the click dispatcher that routes here per
// kind; this is what every one of those routes ends in.
func (p *Plugin) forgetContentPane(leafID int) tea.Cmd {
	if !p.closeContentLeaf(leafID) {
		return nil
	}
	p.hiddenPaneLayout = nil
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
