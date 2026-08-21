package resourceview

import (
	"github.com/marcus/sidecar/internal/tabs"
)

// TabHit is a drawn tab's click target, relative to the strip's first column.
type TabHit = tabs.Hit

// TabStrip is the header row: only tabs, packed left to right.
type TabStrip = tabs.Strip

// fitResourceLabel end-truncates, because the discriminating part of a
// locator is its front: CASH-1245 and CASH-1246 differ in the last character,
// but a strip that dropped the project prefix would show two identical tabs.
func fitResourceLabel(text string, _, _, maxWidth int, _ bool) string {
	return tabs.FitEnd(text, maxWidth)
}

func tabLabels(t *Tabs) []tabs.Label {
	labels := make([]tabs.Label, len(t.Items))
	for i, item := range t.Items {
		text := ""
		if item.Value != nil {
			text = item.Value.TabLabel()
		}
		labels[i] = tabs.Label{Text: text}
	}
	return labels
}

// LayoutTabStrip is the Resource leaf's header row. Both hosts draw this and
// register their hit regions from the same result, so a click cannot land on
// a tab that was never rendered.
func LayoutTabStrip(t *Tabs, width int, focused bool) tabs.Strip {
	if t == nil {
		return tabs.Strip{}
	}
	return tabs.LayoutStrip(tabLabels(t), t.Group.Active, width, focused, fitResourceLabel)
}
