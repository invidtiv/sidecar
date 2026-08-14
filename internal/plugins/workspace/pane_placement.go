package workspace

import "github.com/marcus/sidecar/internal/panelayout"

type paneOpen = panelayout.OpenPlan

func planPaneOpen(root *PaneNode, kind PaneKind) (paneOpen, bool) {
	return panelayout.PlanOpen(root, kind)
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
