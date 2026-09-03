package conversations

import (
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/plugin"
)

// Descriptor is what Sidecar knows about the Conversations panel before it
// runs.
//
// Conversations is the one surface whose feature flag is not an alias: the flag
// is the preview opt-in and plugins.conversations.enabled is the panel switch,
// and both have to be true. Turning the panel off therefore leaves the opt-in
// alone rather than silently revoking it.
func Descriptor() plugin.Descriptor {
	return plugin.Descriptor{
		ID:         pluginID,
		Name:       "Conversations",
		Icon:       pluginIcon,
		Class:      plugin.ClassEmbedded,
		Scope:      plugin.ScopeProject,
		Placements: []plugin.Placement{plugin.PlacementTab},
		Detail:     "Session history from supported agent harnesses",
		Enabled: func(cfg *config.Config) bool {
			return features.IsEnabled(features.ConversationsPlugin.Name) && cfg.Plugins.Conversations.Enabled
		},
		SetEnabled: func(p *config.PluginsConfig, on bool) { p.Conversations.Enabled = on },
		New:        func() plugin.Plugin { return New() },
	}
}
