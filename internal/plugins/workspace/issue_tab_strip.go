package workspace

import "github.com/marcus/sidecar/internal/issueview"

// issueTabHit is a drawn tab's click target. Index is the tab in the group;
// LeafID is the pane-tree leaf so two issue panes cannot steal each other's click.
type issueTabHit struct {
	LeafID int
	Index  int
	Close  bool
}

// layoutIssueTabStrip is the issue leaf's tab strip: an end-truncated title
// strip. The same strip is what registerIssueTabRegions hit-tests.
func layoutIssueTabStrip(issue *issuePane, width int, focused bool) issueview.TabStrip {
	var group issueview.Tabs
	if issue != nil {
		group = issue.tabs
	}
	return issueview.LayoutTabStrip(group, width, focused)
}
