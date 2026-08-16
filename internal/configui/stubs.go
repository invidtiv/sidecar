package configui

// pageBody is the detail-pane content for a destination.
//
// Every page here is a stub composed from the shared controls: navigation is
// fully walkable now, and a later phase replaces one function body at a time
// without touching the surface, the routes, or the search index wiring.
func pageBody(id PageID, width int) []string {
	lines := []string{PaneTitle(PageTitle(id)), ""}
	switch id {
	case PageSetup:
		lines = append(lines,
			Body("A few things will make Sidecar ready to work for you."),
			Muted("Readiness checks and focused repairs arrive with Sidecar Setup itself."),
			SectionHeader("Coming next"),
			Muted("  Project, tmux, terminal-color, and agent-instruction checks, each"),
			Muted("  opening a focused repair that explains a change before making it."),
		)
	case PageAppearance:
		lines = append(lines,
			Muted("Choose how Sidecar looks in your terminal."),
			SectionHeader("Theme"),
			Muted("  The unified built-in and community theme list lands here."),
		)
	case PageProjects:
		lines = append(lines,
			Muted("The projects Sidecar knows about, and the path-adding journey."),
		)
	case PageWorkspaces:
		lines = append(lines,
			Muted("Defaults used when you create a new workspace."),
		)
	case PageAgents:
		lines = append(lines,
			Muted("Choose which agents Sidecar offers when you create work."),
		)
	case PageTerminal:
		lines = append(lines,
			Muted("Set the terminal behavior Sidecar owns."),
		)
	case PagePanels:
		lines = append(lines,
			Muted("Choose the Sidecar surfaces you want available."),
		)
	case PageDiagnostics:
		lines = append(lines,
			Muted("Check the parts Sidecar depends on."),
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
