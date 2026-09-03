package tdmonitor

import (
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/plugin"
)

// Descriptor is what Sidecar knows about the td panel before it runs.
func Descriptor() plugin.Descriptor {
	return plugin.Descriptor{
		ID:         pluginID,
		Name:       pluginName,
		Icon:       pluginIcon,
		Class:      plugin.ClassEmbedded,
		Scope:      plugin.ScopeProject,
		Placements: []plugin.Placement{plugin.PlacementTab},
		Detail:     "Issues and task state from the current project",
		Enabled:    func(cfg *config.Config) bool { return cfg.Plugins.TDMonitor.Enabled },
		SetEnabled: func(p *config.PluginsConfig, on bool) { p.TDMonitor.Enabled = on },
		New:        func() plugin.Plugin { return New() },
	}
}
