package workspace

import (
	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/plugin"
)

// The project surface extends its Tab ring with the shell's notification centre.
var _ plugin.FocusCycler = (*Plugin)(nil)

// paneTreeShowing reports whether the pane tree is what the preview is drawing.
// Worktree terminals are terminals: the tree is on screen whenever it exists.
func (p *Plugin) paneTreeShowing() bool {
	return p.paneRoot != nil
}

// termPanelOnScreen answers the renderers' question, not the state flag's: the
// panel is a window Tab can land on only when it is actually drawn. The split
// falls back to output-only when the preview is too small for it, and focusing
// a panel nobody drew would hand it the next keystrokes with no focus ring to
// show for it.
func (p *Plugin) termPanelOnScreen() bool {
	if !p.termPanelVisible || !p.paneTreeShowing() {
		return false
	}
	_, _, fits := p.termPanelSplitBoxes()
	return fits
}

// focusRing lists the windows Tab walks, in the order the preview draws them:
// the sidebar, then every tree leaf in placement order, then the terminal panel.
// Intra-Diff focus (file list ↔ hunks) stays h/l/enter's, never Tab's. With
// the workspace_doc_panes flag off there is no tree at all, so the preview is
// one window beside the sidebar.
func (p *Plugin) focusRing() []panelayout.Target {
	if p.paneRoot == nil {
		ring := make([]panelayout.Target, 0, 2)
		if p.sidebarVisible {
			ring = append(ring, panelayout.Target{Kind: panelayout.TargetSidebar})
		}
		return append(ring, panelayout.Target{Kind: panelayout.TargetLeaf, Leaf: terminalLeafID(p.paneRoot)})
	}
	return panelayout.Ring(p.paneRoot, p.sidebarVisible, p.termPanelOnScreen())
}

// currentFocusTarget names the window that holds focus now, reading the same
// state setFocusTarget writes so a cycle starts where the frame drew focus.
func (p *Plugin) currentFocusTarget() panelayout.Target {
	switch {
	case p.activePane == PaneSidebar:
		return panelayout.Target{Kind: panelayout.TargetSidebar}
	case p.termPanelOnScreen() && p.termPanelFocused:
		return panelayout.Target{Kind: panelayout.TargetTermPanel}
	default:
		return panelayout.Target{Kind: panelayout.TargetLeaf, Leaf: p.paneFocus}
	}
}

// setFocusTarget is the sole writer of focus state — activePane, paneFocus and
// termPanelFocused move together here and nowhere else, so a click and a Tab
// leave the surface in the same shape. Doc and issue focus are derived from
// paneFocus, so a leaf needs no second bool kept in step.
func (p *Plugin) setFocusTarget(t panelayout.Target) {
	// A pane search is a modal on that pane, so it belongs to whoever holds the
	// keyboard: focus landing anywhere else dismisses it, the way clicking off a
	// modal does. Leaving it open would leave a box drawn with a cursor in it
	// that no keystroke could reach, and none could close.
	defer p.closeUnfocusedDocSearches()
	// Interactive mode is a live pane holding the keyboard, and it is only ever
	// legal on the window that has focus. Ending it HERE, rather than at each
	// site that moves focus, is what keeps the ring honest: a ring drawn on one
	// leaf while keys land in another is exactly what a per-site rule leaks the
	// first time a site forgets.
	if p.viewMode == ViewModeInteractive && !p.targetOwnsTerminalKeyboard(t) {
		p.exitInteractiveMode()
	}
	switch t.Kind {
	case panelayout.TargetSidebar:
		p.activePane = PaneSidebar
		p.termPanelFocused = false
	// TargetTermPanel is the panel's transitional entry: deleted when windowing
	// M1 absorbs the panel as a tree leaf, which folds this arm into the next.
	case panelayout.TargetTermPanel:
		p.activePane = PanePreview
		p.paneFocus = terminalLeafID(p.paneRoot)
		p.termPanelFocused = true
		// Focus is an explicit navigation of the panel, so its window stops
		// being pinned where a document or a gesture left it. Without this the
		// panel arrives frozen and the first key moves nothing.
		p.thawTermPanelWindow()
		p.syncDeckFocus()
	default:
		p.activePane = PanePreview
		p.paneFocus = t.Leaf
		p.termPanelFocused = false
		p.syncDeckFocus()
	}
}

// syncDeckFocus keeps the content deck's focused leaf on the same window the
// ring names. The global browser does this in focusPreviewLeaf; without it a
// later deck projection copies the last-opened extra pane back onto paneFocus
// and the shell loses the keyboard.
func (p *Plugin) syncDeckFocus() {
	if p.contentDeck == nil || p.activePane != PanePreview {
		return
	}
	p.contentDeck.FocusLeaf(p.paneFocus)
}

// targetOwnsTerminalKeyboard reports that a focus target is a window a live
// pane may keep typing into: the terminal panel, or a terminal leaf. Every
// other window — the sidebar, a document, an issue, a diff — takes the keyboard
// with it when focus lands on it.
func (p *Plugin) targetOwnsTerminalKeyboard(t panelayout.Target) bool {
	switch t.Kind {
	case panelayout.TargetTermPanel:
		return true
	case panelayout.TargetLeaf:
		leaf := FindPane(p.paneRoot, t.Leaf)
		return leaf != nil && leaf.Split == nil && leaf.Kind == PaneTerminal
	default:
		return false
	}
}

// cyclePaneFocus moves focus one window along the ring, wrapping at both ends.
func (p *Plugin) cyclePaneFocus(reverse bool) {
	ring := p.focusRing()
	if len(ring) == 0 {
		return
	}
	p.setFocusTarget(panelayout.CycleTarget(ring, p.currentFocusTarget(), reverse))
}

// focusSidebar is the click path's shorthand for the workspace list.
func (p *Plugin) focusSidebar() {
	p.setFocusTarget(panelayout.Target{Kind: panelayout.TargetSidebar})
}

// focusTermPanel is the click path's shorthand for the terminal panel.
func (p *Plugin) focusTermPanel() {
	p.setFocusTarget(panelayout.Target{Kind: panelayout.TargetTermPanel})
}

// focusLeaf is the click path's shorthand for the leaf a region carries.
func (p *Plugin) focusLeaf(leafID int) {
	p.setFocusTarget(panelayout.Target{Kind: panelayout.TargetLeaf, Leaf: leafID})
}

// AtFocusCycleEnd and FocusCycleStart implement plugin.FocusCycler, which is
// how the shell's notification centre becomes a stop on this surface's Tab
// cycle instead of a cycle of its own. Both read the same ring cyclePaneFocus
// walks, so the stop lands exactly where the wrap would have been.
func (p *Plugin) AtFocusCycleEnd(reverse bool) bool {
	// A live pane search still owns Tab — the surface uses it to leave the
	// input and move focus, and a shell stop taking the key would leave a
	// search box drawn, cursor and all, over a pane that no longer takes keys.
	if p.docSearchActive() || p.terminalSearch.InputActive {
		return false
	}
	return panelayout.AtRingEnd(p.focusRing(), p.currentFocusTarget(), reverse)
}

func (p *Plugin) FocusCycleStart(reverse bool) tea.Cmd {
	if target, ok := panelayout.RingStart(p.focusRing(), reverse); ok {
		p.setFocusTarget(target)
	}
	return nil
}

// The project surface tells the shell when a pane rail is under the pointer, so
// the floating tiers stay off the screen for the length of the drag.
var _ plugin.ResizeDragReporter = (*Plugin)(nil)

// ResizeDragActive reports a live divider drag. It reuses the same predicate
// the no-SIGWINCH-until-drop gate does, so "a rail is being dragged" has one
// definition on this surface rather than two that can disagree.
func (p *Plugin) ResizeDragActive() bool {
	if p == nil || p.mouseHandler == nil || !p.mouseHandler.IsDragging() {
		return false
	}
	return isDividerDragRegion(p.mouseHandler.DragRegion())
}
