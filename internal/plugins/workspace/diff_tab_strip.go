package workspace

import (
	"github.com/marcus/sidecar/internal/tabs"
	"github.com/marcus/sidecar/internal/workspacediff"
)

// diffTabHit is a drawn Diff target tab's click target.
type diffTabHit struct {
	LeafID int
	Index  int
	Close  bool
}

func fitDiffLabel(text string, _, _, maxWidth int, _ bool) string {
	return tabs.FitEnd(text, maxWidth)
}

func layoutDiffTabStrip(diff *diffPane, width int, focused bool) tabs.Strip {
	var group workspacediff.Group
	if diff != nil {
		group = diff.tabs
	}
	labels := make([]tabs.Label, len(group.Items))
	for i, item := range group.Items {
		text := item.Key
		if item.Value != nil {
			text = item.Value.Target.TabLabel()
		}
		labels[i] = tabs.Label{Text: text}
	}
	return tabs.LayoutStrip(labels, group.Active, width, focused, fitDiffLabel)
}
