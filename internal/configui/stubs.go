package configui

// pageBody is the detail-pane content for a destination that has no page of its
// own yet.
//
// Every mockup destination now has a real page, so nothing routes here; it
// remains as the shape a new destination starts from and as the honest answer
// for a page ID that does not exist.
func pageBody(id PageID, width int) []string {
	_ = width
	return []string{
		PaneTitle(PageTitle(id)),
		"",
		Muted("This page is not available."),
	}
}
