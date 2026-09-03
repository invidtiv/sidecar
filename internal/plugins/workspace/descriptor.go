package workspace

import "github.com/marcus/sidecar/internal/plugin"

// Descriptor is what Sidecar knows about the Workspaces panel before it runs.
//
// It has no Enabled func and no SetEnabled: Workspaces is Sidecar's core tab
// and there is no switch that turns it off, which a nil Enabled says exactly.
func Descriptor() plugin.Descriptor {
	return plugin.Descriptor{
		ID:         pluginID,
		Name:       pluginName,
		Icon:       pluginIcon,
		Class:      plugin.ClassEmbedded,
		Scope:      plugin.ScopeProject,
		Placements: []plugin.Placement{plugin.PlacementTab},
		Detail:     "Shells, worktrees, and agents for the current project",
		New:        func() plugin.Plugin { return New() },
	}
}
