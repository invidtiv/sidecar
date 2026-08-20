package docview

import (
	"github.com/marcus/sidecar/internal/tabs"
	"github.com/marcus/sidecar/internal/ui"
)

// TabMinBudget is the floor for one tab's column share, chrome included.
const TabMinBudget = tabs.MinBudget

// TabHit is a drawn tab's click target. Col is relative to the strip's first
// column; Width is the rendered pill.
type TabHit = tabs.Hit

// TabStrip is the header row: only tabs, packed left to right.
type TabStrip = tabs.Strip

func fitDocLabel(text string, _, _, maxWidth int, _ bool) string {
	return ui.TruncateStart(text, maxWidth)
}

func tabLabels(group Tabs) []tabs.Label {
	labels := make([]tabs.Label, len(group.Items))
	for i, item := range group.Items {
		text := ""
		if item.View != nil {
			text = item.View.Title()
		}
		labels[i] = tabs.Label{Text: text}
	}
	return labels
}

// LayoutTabStrip is the document header: a left-truncated path tab strip and
// nothing else. Overflow lives on the tab group. Hosts draw this row and
// register hit regions from the same Tabs so a click cannot land on a tab
// that was never rendered.
func LayoutTabStrip(group Tabs, width int, focused bool) TabStrip {
	return tabs.LayoutStrip(tabLabels(group), group.Active, width, focused, fitDocLabel)
}

// LayoutSearchTabStrip is the same strip with the active tab renamed to a live
// search surface and its query, so a pane taking search keystrokes never reads
// as a pane showing a file. A pane opened straight into the finder has no tabs
// yet, and the label is the whole strip.
//
// It lives here rather than in a host because both pane surfaces draw the same
// strip for the same surfaces.
func LayoutSearchTabStrip(group Tabs, label string, width int, focused bool) TabStrip {
	labels := make([]tabs.Label, 0, len(group.Items)+1)
	for _, item := range group.Items {
		text := ""
		if item.View != nil {
			text = item.View.Title()
		}
		labels = append(labels, tabs.Label{Text: text})
	}
	active := group.Active
	if len(labels) == 0 {
		labels = append(labels, tabs.Label{})
		active = 0
	}
	if active < 0 || active >= len(labels) {
		active = 0
	}
	labels[active] = tabs.Label{Text: label}
	return tabs.LayoutStrip(labels, active, width, focused, fitSearchLabel)
}

// fitSearchLabel truncates from the start, the way a document tab's path does,
// so the end of the query stays visible as it is typed.
func fitSearchLabel(text string, _, _, maxWidth int, _ bool) string {
	return ui.TruncateStart(text, maxWidth)
}
