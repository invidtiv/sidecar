package filebrowser

import (
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/plugin"
)

// Descriptor is what Sidecar knows about the Files panel before it runs.
func Descriptor() plugin.Descriptor {
	return plugin.Descriptor{
		ID:         pluginID,
		Name:       pluginName,
		Icon:       pluginIcon,
		Class:      plugin.ClassEmbedded,
		Scope:      plugin.ScopeProject,
		Placements: []plugin.Placement{plugin.PlacementTab},
		Detail:     "Project browser and inline editing",
		Enabled:    func(cfg *config.Config) bool { return cfg.Plugins.FileBrowser.Enabled },
		SetEnabled: func(p *config.PluginsConfig, on bool) { p.FileBrowser.Enabled = on },
		New:        func() plugin.Plugin { return New() },
	}
}
