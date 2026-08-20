package workspace

import (
	"github.com/marcus/sidecar/internal/docview"
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
		return docview.LayoutSearchTabStrip(group, doc.mode.HeaderLabel(), width, focused)
	}
	return docview.LayoutTabStrip(group, width, focused)
}
