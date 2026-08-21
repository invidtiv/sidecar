package noteview

import (
	"github.com/marcus/sidecar/internal/tabs"
)

// TabHit is a drawn tab's click target.
type TabHit = tabs.Hit

// TabStrip is the header row: only tabs, packed left to right.
type TabStrip = tabs.Strip

func fitNoteLabel(text string, _, _, maxWidth int, _ bool) string {
	return tabs.FitEnd(text, maxWidth)
}

func tabLabels(group Tabs) []tabs.Label {
	labels := make([]tabs.Label, len(group.Items))
	for i, item := range group.Items {
		text := ""
		if item.Value != nil {
			text = item.Value.Title()
		}
		labels[i] = tabs.Label{Text: text}
	}
	return labels
}

// LayoutTabStrip is the note header: an end-truncated title strip so the
// note ID stays visible. Hosts draw this row and register hit regions from
// the same Tabs so a click cannot land on a tab that was never rendered.
func LayoutTabStrip(group Tabs, width int, focused bool) tabs.Strip {
	return tabs.LayoutStrip(tabLabels(group), group.Active, width, focused, fitNoteLabel)
}
