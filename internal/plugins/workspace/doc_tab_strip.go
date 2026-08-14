package workspace

import "github.com/marcus/sidecar/internal/docview"

// docTabHit is a drawn tab's click target. Index is the tab in the group;
// LeafID is the pane-tree leaf so two doc panes cannot steal each other's click.
type docTabHit struct {
	LeafID int
	Index  int
}

type docTabPlacement = docview.TabHit
type docTabStrip = docview.TabStrip

// layoutDocTabStrip is the doc leaf's header: a left-truncated path tab strip
// and nothing else. The same strip is what registerDocPaneRegions hit-tests.
func layoutDocTabStrip(doc *docPane, width int, focused bool) docTabStrip {
	var tabs docview.Tabs
	if doc != nil {
		tabs = doc.tabs
	}
	return docview.LayoutTabStrip(tabs, width, focused)
}
