package configui

// pageBody is the detail-pane content for a destination that has no page of its
// own yet.
//
// Sidecar Setup, Appearance, Projects, Workspaces, Agents, Terminal, and
// Diagnostics have moved out of here into real pages; the rest
// remain stubs composed from the shared controls, so navigation stays fully
// walkable and a later phase replaces one page at a time without touching the
// surface, the routes, or the search index wiring.
func pageBody(id PageID, width int) []string {
	lines := []string{PaneTitle(PageTitle(id)), ""}
	switch id {
	case PagePanels:
		lines = append(lines,
			Muted("Choose the Sidecar surfaces you want available."),
		)
	case PageAdvanced:
		lines = append(lines,
			Muted("Feature previews and technical controls. Most people never need these."),
		)
	case PageAbout:
		lines = append(lines,
			Muted("Version, installation provenance, update status, and support material."),
		)
	default:
		lines = append(lines, Muted("This page is not available."))
	}
	_ = width
	return lines
}
