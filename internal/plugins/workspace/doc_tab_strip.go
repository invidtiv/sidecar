package workspace

import (
	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/tabs"
	"github.com/marcus/sidecar/internal/ui"
)

// docTabHit is a drawn tab's click target. Index is the tab in the group;
// LeafID is the pane-tree leaf so two doc panes cannot steal each other's click.
type docTabHit struct {
	LeafID int
	Index  int
}

type docTabPlacement = docview.TabHit
type docTabStrip = docview.TabStrip

// layoutDocTabStrip is the doc leaf's tab strip: a left-truncated path strip.
// The same strip is what registerDocTabRegions hit-tests.
func layoutDocTabStrip(doc *docPane, width int, focused bool) docTabStrip {
	var group docview.Tabs
	if doc != nil {
		group = doc.tabs
	}
	if doc != nil && doc.mode != nil {
		return layoutDocSearchStrip(doc, group, width, focused)
	}
	return docview.LayoutTabStrip(group, width, focused)
}

// layoutDocSearchStrip is the same strip with the active tab renamed to the
// live search surface and its query, so a pane taking search keystrokes never
// reads as a pane showing a file. A pane opened straight into the finder has no
// tabs yet, and the mode is the whole strip.
func layoutDocSearchStrip(doc *docPane, group docview.Tabs, width int, focused bool) docTabStrip {
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
	labels[active] = tabs.Label{Text: doc.mode.headerLabel()}
	return tabs.LayoutStrip(labels, active, width, focused, fitDocSearchLabel)
}

// fitDocSearchLabel truncates from the start, the way a document tab's path
// does, so the end of the query stays visible as it is typed.
func fitDocSearchLabel(text string, _, _, maxWidth int, _ bool) string {
	return ui.TruncateStart(text, maxWidth)
}
