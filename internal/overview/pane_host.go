package overview

import (
	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/paneframe"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/termpreview"
	"github.com/marcus/sidecar/internal/ui"
)

// paneHost binds the global Workspaces browser to the shared frame. It is the
// counterpart of the project plugin's paneHost, and the two are deliberately
// the same shape: everything a user can see about windowing — per-leaf borders,
// focus and interactive chrome, handle colours, the widened divider target, the
// order hit regions are registered in — is decided in internal/paneframe, and
// each surface only answers what is in its own leaves.
//
// A windowing change that lands in one of these two files and not the other is
// the parity bug this seam exists to prevent.
type paneHost struct{ m *Model }

var _ paneframe.Host = paneHost{}

func (h paneHost) Content(node *panelayout.Node) paneframe.Content { return h.m.paneContent(node) }

func (h paneHost) Focus() int { return h.m.preview.paneFocus }

// SetFocus is the write half of Focus, and it routes through the same setter
// every keyboard cycle uses. focusPreviewLeaf moves paneFocus, the panel's own
// focus flag, the per-content focused bools and the live pane's keyboard in one
// act, so a pointer cannot leave the ring on one leaf and the keys on another.
func (h paneHost) SetFocus(node *panelayout.Node) {
	m := h.m
	if node == nil || node.Split != nil {
		return
	}
	// A button going down on the terminal leaf moves the ring's leaf but does
	// not, by itself, take the keyboard off the list. This surface has no
	// "focused but not typing" preview: a press there may still become a
	// drag-selection, and activation is the release. paneFocus moves anyway, so
	// when the release does hand the pane its keyboard the ring is on the leaf
	// the pointer chose rather than on whichever neighbour held it last.
	//
	// The condition is previewOwnsChrome, not PreviewFocused, because that is
	// what DRAWS the ring. With the sidebar hidden the preview owns the chrome
	// while the list still owns the keyboard, and deferring there would light an
	// active border on the terminal while j/k moved the list — this ticket's own
	// bug in a narrower case.
	if node.Kind == panelayout.Terminal && !m.previewOwnsChrome() {
		m.preview.paneFocus = node.ID
		return
	}
	m.queuePreviewCmd(m.setFocusTarget(panelayout.Target{Kind: panelayout.TargetLeaf, Leaf: node.ID}))
}

// Layout is the tree as this frame places it now. It is the same call
// renderPreviewPeer composes from and registerPreviewOutputRegions registers
// from, so the boxes a pointer is tested against are the boxes that were drawn.
func (h paneHost) Layout() (panelayout.Layout, bool) {
	m := h.m
	if m.preview.paneRoot == nil {
		return panelayout.Layout{}, false
	}
	peer, ok := m.previewPeerBox()
	if !ok {
		return panelayout.Layout{}, false
	}
	return m.layoutPreviewPanes(peer)
}

func (h paneHost) HandleState(splitID int) ui.HandleState {
	return h.m.dividerHandleState(previewPaneDividerKind, splitID)
}

// QueueSizeCmd holds a content's geometry assertion for the next update. No
// content on this surface returns one today — the live pane is resized from
// syncTerminalGeometry, on the state change that moved its box — but a render
// has no runtime to dispatch one with, so one that appears is kept rather than
// silently dropped.
func (h paneHost) QueueSizeCmd(cmd tea.Cmd) { h.m.queuePreviewCmd(cmd) }

// queuePreviewCmd keeps a command produced somewhere with no way to return one:
// a render, or a focus change made inside a setter whose callers do not deal in
// commands. Model.Update drains the queue, so the command is deferred by one
// update rather than dropped.
func (m *Model) queuePreviewCmd(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	m.preview.paneSizeCmds = append(m.preview.paneSizeCmds, cmd)
}

// Chrome is a reader of focus: interactive/active on the focused leaf, muted on
// neighbours. Content bytes are not dimmed.
func (h paneHost) Chrome(node *panelayout.Node) paneframe.Chrome {
	m := h.m
	if node == nil || !m.previewOwnsChrome() || m.preview.paneFocus != node.ID {
		return paneframe.ChromeIdle
	}
	if node.Kind == panelayout.Terminal && m.PreviewInteractive() {
		return paneframe.ChromeInteractive
	}
	return paneframe.ChromeActive
}

// previewOwnsChrome reports that the preview side should draw its focused leaf
// as focused. It is PreviewFocused, except when the sidebar is hidden: with no
// list beside it there is nothing for a muted border to distinguish the panel
// from, and this surface has always framed that arrangement as active.
//
// The narrow full-preview arrangement is deliberately NOT a second clause here.
// It reaches this code only with the preview focused — focusList clears full —
// so naming it would add a way for both panels to draw as focused at once if
// that ever stopped being true.
func (m *Model) previewOwnsChrome() bool {
	return m.PreviewFocused() || !m.sidebarVisible
}

// takePaneSizeCmds empties the queue as it hands it over: a geometry assertion
// dispatched on every update after the render that made it is a resize storm.
func (m *Model) takePaneSizeCmds() []tea.Cmd {
	cmds := m.preview.paneSizeCmds
	m.preview.paneSizeCmds = nil
	return cmds
}

// paneRegions is this surface's mouse vocabulary for the frame's region order.
// The frame decides WHEN each target is registered; this decides what it is.
type paneRegions struct{ m *Model }

var _ paneframe.RegionSink = paneRegions{}

// Leaf covers a whole content leaf, OUTER. The terminal leaf registers nothing
// here: the preview region already covers the peer, and a leaf region drawn over
// it would take presses the live pane owns.
func (r paneRegions) Leaf(node *panelayout.Node, outer paneframe.Box) {
	if node == nil || node.Split != nil {
		return
	}
	switch node.Kind {
	case panelayout.Document:
		r.m.registerPreviewDocRegion(paneframe.Inset(outer))
	case panelayout.Issue:
		r.m.registerPreviewIssueRegion(paneframe.Inset(outer))
	case panelayout.Note:
		r.m.registerPreviewNoteRegion(paneframe.Inset(outer))
	case panelayout.Diff:
		r.m.registerPreviewDiffRegion(paneframe.Inset(outer))
	case panelayout.Resource:
		r.m.registerPreviewResourceRegion(paneframe.Inset(outer))
	}
}

func (r paneRegions) Divider(splitID int, hit paneframe.Box) {
	r.m.workspacesMouse.HitMap.AddRect(previewPaneDividerKind, hit.X, hit.Y, hit.W, hit.H, previewPaneDividerHit(splitID))
}

func (r paneRegions) Tabs(node *panelayout.Node, inner paneframe.Box) {
	if node == nil || node.Split != nil {
		return
	}
	switch node.Kind {
	case panelayout.Document:
		r.m.registerPreviewDocTabRegions(inner)
	case panelayout.Issue:
		r.m.registerPreviewIssueTabRegions(inner)
	case panelayout.Note:
		r.m.registerPreviewNoteTabRegions(inner)
	case panelayout.Diff:
		r.m.registerPreviewDiffTabRegions(inner)
	case panelayout.Resource:
		r.m.registerPreviewResourceTabRegions(inner)
	}
}

// Title is the leaf's header name. A shell leaf claims it for the reason it
// does on the project surface: it has no row in this list either — the row it
// belongs to wears a layout badge instead — so the pane's own title is where
// its rename lives. Every other leaf is named by the row that selected it, and
// that row already answers R.
func (r paneRegions) Title(node *panelayout.Node, hit paneframe.Box) {
	if node == nil || node.Split != nil || node.Kind != panelayout.Shell {
		return
	}
	r.m.workspacesMouse.HitMap.AddRect(previewPaneTitleKind, hit.X, hit.Y, hit.W, hit.H, previewPaneTitleHit(node.ID))
}

// Close is the leaf's header X. A leaf whose content is gone registers nothing,
// the same rule Leaf and Tabs follow: a button that closes a pane that is not
// there is a click that does nothing where something is drawn.
func (r paneRegions) Close(node *panelayout.Node, inner paneframe.Box) {
	if node == nil || node.Split != nil || node.Kind == panelayout.Terminal {
		return
	}
	if r.m.paneContent(node) == nil {
		return
	}
	r.m.registerPreviewCloseRegion(node.Kind, inner)
}

// Body is the terminal leaf's action chips and the diff leaf's own list/hunk
// divider and file rows — targets a content owns inside its own box, which have
// to beat the tree divider and the leaf drawn under them.
func (r paneRegions) Body(node *panelayout.Node, inner paneframe.Box) {
	if node == nil || node.Split != nil {
		return
	}
	switch node.Kind {
	case panelayout.Terminal:
		r.m.registerPreviewActionRegions(inner)
	case panelayout.Document:
		r.m.registerPreviewDocLinkHits(inner)
	case panelayout.Diff:
		r.m.registerPreviewDiffLeafHits(inner)
	}
}

// previewPaneFloors is the INNER minimum each content needs. The shared frame
// adds what a border costs, so this surface and the project plugin budget pane
// chrome identically.
func previewPaneFloors() panelayout.Floors {
	return paneframe.ChromeFloors(panelayout.Floors{
		Terminal: panelayout.Floor{Width: previewTermMinWidth, Height: 3},
		Doc:      panelayout.Floor{Width: previewSecondaryMinWidth, Height: 3},
		Issue:    panelayout.Floor{Width: previewSecondaryMinWidth, Height: 3},
		Note:     panelayout.Floor{Width: previewSecondaryMinWidth, Height: 3},
		Diff:     panelayout.Floor{Width: previewSecondaryMinWidth, Height: 3},
		Resource: panelayout.Floor{Width: previewSecondaryMinWidth, Height: 3},
		// A shell leaf is a terminal, so it budgets what the primary terminal
		// budgets on this surface.
		Shell: panelayout.Floor{Width: previewTermMinWidth, Height: 3},
	})
}

// renderPreviewPeer draws the preview panel's OUTER rectangle.
//
// A lone leaf keeps the single frame this surface has always drawn. A split tree
// dissolves that frame and gives every leaf its own, because two leaves inside
// one outer border cannot show which of them has focus and have no edge between
// them to grab. This is the same branch the project workspace takes, and it is
// what the two surfaces have to keep taking together.
func (m *Model) renderPreviewPeer(peer termpreview.Box) string {
	layout, laid := m.layoutPreviewPanes(peer)
	if !laid || len(layout.Leaves) == 0 {
		// No tree to place: the preview is still one framed box, so a degenerate
		// layout does not cost the panel its perimeter.
		inner := paneframe.Inset(peer)
		return paneframe.WrapLeaf(m.renderPreview(inner.W, inner.H), peer, m.lonePreviewChrome())
	}
	// Regions are re-earned every frame: a pane this frame does not draw must
	// not leave last frame's modal regions on screen. Compose first so
	// document content-link hits exist before Body registers them — the same
	// order the project workspace uses.
	m.clearPreviewDocSearchRegions()
	view := ""
	if len(layout.Leaves) == 1 {
		view = paneframe.ComposeLeaf(paneHost{m}, layout.Leaves[0], layout.Zoomed)
	} else {
		view = paneframe.Compose(paneHost{m}, layout, peer, peer.W, peer.H)
	}
	m.registerPreviewOutputRegions(peer)
	// Last, because a live search surface is drawn over its leaf and its regions
	// have to beat the leaf's own.
	m.registerPreviewDocSearchRegions()
	return view
}

// lonePreviewChrome is the frame around a preview with no placeable tree. The
// panel is the only thing on that side of the split, so it draws as focused
// rather than as an unfocused neighbour of nothing.
func (m *Model) lonePreviewChrome() paneframe.Chrome {
	if m.PreviewInteractive() {
		return paneframe.ChromeInteractive
	}
	return paneframe.ChromeActive
}
