package workspace

import (
	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/paneframe"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/ui"
)

// paneHost binds this plugin to the shared frame. It is a named seam rather
// than methods on Plugin so the frame's vocabulary stays out of the plugin's
// public surface, and so the answer to "what does this workspace tell the frame"
// is one file on both surfaces.
type paneHost struct{ p *Plugin }

var _ paneframe.Host = paneHost{}

func (h paneHost) Content(node *panelayout.Node) paneframe.Content { return h.p.paneContent(node) }

func (h paneHost) Focus() int { return h.p.paneFocus }

// SetFocus is the write half of Focus, and it is deliberately the same setter
// every keyboard cycle uses: setFocusTarget moves activePane, paneFocus,
// termPanelFocused and the live terminal's keyboard together, so a pointer
// cannot leave the ring on one leaf and the keys on another.
func (h paneHost) SetFocus(node *panelayout.Node) {
	if node == nil || node.Split != nil {
		return
	}
	h.p.focusLeaf(node.ID)
}

// Layout is the tree the last frame DREW, not a tree this state could place.
// Answering the second would hand out phantom geometry from any view that
// replaces the preview — the kanban board is the one that bit us: a click on a
// card sitting over a document leaf's old box moved pane focus the user never
// asked to move. Recording it where the tree composes means every reason
// renderDocumentSplit declines a frame is honoured here for free.
func (h paneHost) Layout() (panelayout.Layout, bool) {
	return h.p.paneFrame, h.p.paneFrameDrawn
}

func (h paneHost) HandleState(splitID int) ui.HandleState {
	return h.p.dividerHandleState(regionPaneTreeDivider, splitID)
}

func (h paneHost) QueueSizeCmd(cmd tea.Cmd) { h.p.paneSizeCmds = append(h.p.paneSizeCmds, cmd) }

// Chrome is a reader of setFocusTarget: interactive/active on the focused leaf,
// muted on neighbours. Content bytes are not dimmed.
func (h paneHost) Chrome(node *panelayout.Node) paneframe.Chrome {
	p := h.p
	if node != nil && node.Kind == PaneShell {
		// The panel's keyboard is termPanelFocused's, not paneFocus's: the ring
		// names the terminal leaf for both terminals and that flag says which of
		// them has the keys.
		if p.activePane != PanePreview || !p.shellLeafFocused() {
			return paneframe.ChromeIdle
		}
		if p.interactiveDescribes(true) {
			return paneframe.ChromeInteractive
		}
		return paneframe.ChromeActive
	}
	focused := node != nil && p.activePane == PanePreview && p.paneFocus == node.ID && !p.shellLeafFocused()
	if !focused {
		return paneframe.ChromeIdle
	}
	if node.Kind == PaneTerminal {
		switch {
		case p.viewMode == ViewModeInteractive:
			return paneframe.ChromeInteractive
		case p.previewFlashActive():
			return paneframe.ChromeFlash
		}
	}
	return paneframe.ChromeActive
}

// paneRegions is this plugin's mouse vocabulary for the frame's region order.
// The frame decides WHEN each target is registered; this decides what it is.
type paneRegions struct{ p *Plugin }

var _ paneframe.RegionSink = paneRegions{}

func (r paneRegions) Leaf(node *panelayout.Node, outer paneframe.Box) {
	r.p.registerPaneLeafRegions(node, outer)
}

func (r paneRegions) Divider(splitID int, hit paneframe.Box) {
	r.p.mouseHandler.HitMap.AddRect(regionPaneTreeDivider, hit.X, hit.Y, hit.W, hit.H, splitID)
}

func (r paneRegions) Tabs(node *panelayout.Node, inner paneframe.Box) {
	r.p.registerPaneTabRegions(node, inner)
}

// Title is the leaf's header name. Only a shell leaf claims it: the primary
// terminal is named by the sidebar row that selected it, and that row already
// has R. A shell split has no row of its own — the sidebar badges it rather
// than listing it — so its title is where its rename lives.
func (r paneRegions) Title(node *panelayout.Node, hit paneframe.Box) {
	if node == nil || node.Split != nil || node.Kind != PaneShell {
		return
	}
	r.p.mouseHandler.HitMap.AddRect(regionPaneTitle, hit.X, hit.Y, hit.W, hit.H, node.ID)
}

// Layout is wired by the reposition-modal host adapter. Keeping the rung on
// the shared sink now lets the frame own its precedence while M2 adds the
// concrete region and hover state beside the close control.
func (r paneRegions) Layout(node *panelayout.Node, inner paneframe.Box) {
	if node == nil || node.Split != nil {
		return
	}
	reserve := r.p.reserveHeader(inner.W, node.Kind != PaneTerminal)
	if reserve.LayoutW < 1 {
		return
	}
	r.p.mouseHandler.HitMap.AddRect(regionPaneLayout, inner.X+reserve.LayoutCol, inner.Y, reserve.LayoutW, 1, node.ID)
}

func (r paneRegions) Close(node *panelayout.Node, inner paneframe.Box) {
	r.p.registerPaneCloseRegions(node, inner)
}

// Body is anything a leaf's content owns inside its own box — a diff list
// divider, a file row, a document content link, a note pane's scrollbar, a
// terminal surface's scrollbar — in its chrome-aware content box. It registers
// last so it beats everything the frame put down except a later overlay
// (finder, project search).
func (r paneRegions) Body(node *panelayout.Node, inner paneframe.Box) {
	if node == nil {
		return
	}
	switch node.Kind {
	case PaneDoc:
		r.p.registerDocLinkHits(node, inner)
	case PaneDiff:
		diff := r.p.diffs[node.ContentID]
		if diff == nil {
			return
		}
		r.p.registerDiffLeafHits(diff, inner)
	case PaneNote:
		r.p.registerNoteScrollbarHits(node.ID, inner)
	case PaneTerminal:
		r.p.registerTerminalScrollbarHits(false)
	case PaneShell:
		// The panel surface answers for THE shell leaf; another shell-shaped
		// node must not put a bar over a box its surface does not draw.
		if leaf := r.p.shellLeaf(); leaf != nil && leaf.ID == node.ID {
			r.p.registerTerminalScrollbarHits(true)
		}
	}
}
