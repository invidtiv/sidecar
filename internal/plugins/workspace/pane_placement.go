package workspace

import "github.com/marcus/sidecar/internal/panelayout"

type paneOpen = panelayout.OpenPlan

func planPaneOpen(root *PaneNode, kind PaneKind, boxes map[int]Box) (paneOpen, bool) {
	return panelayout.PlanOpen(root, kind, boxes)
}

// lastPaneBoxes is the tiled leaf geometry for the current preview box.
// PlanOpen reads areas from these boxes; a tree that does not fit (the zoomed
// LayoutTree case) has no areas to offer.
func (p *Plugin) lastPaneBoxes() map[int]Box {
	content, ok := p.previewContentBox()
	if !ok {
		return nil
	}
	leaves, _, fits := LayoutPanes(p.paneRoot, content, paneTreeFloors())
	if !fits {
		return nil
	}
	boxes := make(map[int]Box, len(leaves))
	for _, leaf := range leaves {
		if leaf.Node == nil {
			continue
		}
		boxes[leaf.Node.ID] = leaf.Box
	}
	return boxes
}

func paneFitMessage(name string, axis SplitAxis) string {
	if axis == SplitRows {
		return name + " pane needs a taller window; layout left unchanged"
	}
	return name + " pane needs a wider window; layout left unchanged"
}

func firstPaneLeafOfKind(root *PaneNode, kind PaneKind) *PaneNode {
	return panelayout.FirstOfKind(root, kind)
}
