// Package configui owns Sidecar's Configuration surface: its route model, its
// sidebar/detail presentation, and the shared controls every Configuration page
// composes. It renders and answers input; it decides nothing about persistence.
// Reading and writing settings stays behind internal/config, so a page can be
// filled in later without moving the surface.
package configui

// PageID identifies a Configuration destination. It is the stable name later
// phases route to, so it is deliberately independent of the sidebar's order and
// of the page's display title.
type PageID string

const (
	PageSetup       PageID = "setup"
	PageAppearance  PageID = "appearance"
	PageProjects    PageID = "projects"
	PageWorkspaces  PageID = "workspaces"
	PageAgents      PageID = "agents"
	PageTerminal    PageID = "terminal"
	PagePanels      PageID = "panels"
	PageDiagnostics PageID = "diagnostics"
	PageAdvanced    PageID = "advanced"
	PageAbout       PageID = "about"
)

// DefaultPage is where the gear, the palette command, and `sidecar setup` all
// land. Configuration deliberately does not remember the last section.
const DefaultPage = PageSetup

// Page is one sidebar destination.
type Page struct {
	ID    PageID
	Title string
}

// Group is a titled run of sidebar destinations.
type Group struct {
	Title string
	Pages []Page
}

// groups is the information architecture from the design brief, in sidebar
// order. Adding a destination here is all that a new page needs to become
// navigable; its content comes from pageBody.
var groups = []Group{
	{
		Title: "Sidecar",
		Pages: []Page{
			{ID: PageSetup, Title: "Sidecar Setup"},
			{ID: PageAppearance, Title: "Appearance"},
			{ID: PageProjects, Title: "Projects"},
			{ID: PageWorkspaces, Title: "Workspaces"},
			{ID: PageAgents, Title: "Agents"},
			{ID: PageTerminal, Title: "Terminal"},
			{ID: PagePanels, Title: "Panels & Integrations"},
		},
	},
	{
		Title: "System",
		Pages: []Page{
			{ID: PageDiagnostics, Title: "Diagnostics"},
			{ID: PageAdvanced, Title: "Advanced"},
			{ID: PageAbout, Title: "About Sidecar"},
		},
	},
}

// Groups returns the sidebar's groups in order.
func Groups() []Group { return groups }

// PageTitle returns the display title for a page ID.
func PageTitle(id PageID) string {
	for _, group := range groups {
		for _, page := range group.Pages {
			if page.ID == id {
				return page.Title
			}
		}
	}
	return ""
}

// AllPages returns every destination in sidebar order.
func AllPages() []Page {
	var pages []Page
	for _, group := range groups {
		pages = append(pages, group.Pages...)
	}
	return pages
}
