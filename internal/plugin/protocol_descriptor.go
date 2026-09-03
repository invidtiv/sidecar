package plugin

import "github.com/marcus/sidecar/internal/config"

// ProtocolDescriptors projects the configured external plugin instances onto
// descriptors, in configuration order.
//
// A protocol plugin has no Go literal anywhere: its config entry is its
// manifest, and describe fills in the rest at run time. That is the whole of
// why there is no manifest file format — a config entry plus a describe answer
// is already every fact the host needs, and a manifest would be a third place
// for the same truth to live.
//
// This is a pure function of configuration. It runs no plugin, resolves nothing
// on PATH, and reads nothing from disk, so it is safe anywhere config already
// is — including before the first frame.
func ProtocolDescriptors(cfg *config.Config) []Descriptor {
	if cfg == nil {
		return nil
	}
	instances := cfg.PluginInstances()
	out := make([]Descriptor, 0, len(instances))
	for _, instance := range instances {
		out = append(out, ProtocolDescriptor(instance))
	}
	return out
}

// ProtocolDescriptor projects one configured instance onto a descriptor.
//
// Name is the instance ID until a describe result supplies the plugin's own
// display name. That is deliberate: the descriptor exists before anything has
// run, and inventing a prettier name here would mean the settings page and the
// CLI disagreed with the header the moment describe landed.
func ProtocolDescriptor(instance config.PluginInstance) Descriptor {
	entry := instance
	d := Descriptor{
		ID:         instance.ID,
		Name:       instance.ID,
		Class:      ClassProtocol,
		Scope:      protocolScope(instance.Scope),
		Placements: protocolPlacements(instance.Placements),
		Detail:     "External plugin from " + instance.Source,
		Instance:   &entry,
		Enabled: func(*config.Config) bool {
			return entry.Enabled
		},
	}
	// Only an entry in plugins.external can be switched through the plugins
	// section. A terminalResources entry lives in a section this signature
	// cannot reach, so it has no descriptor switch and is toggled with
	// `sidecar plugin enable|disable`, which edits whichever section owns it.
	if instance.Source == config.PluginSourceExternal {
		id := instance.ID
		d.SetEnabled = func(plugins *config.PluginsConfig, enabled bool) {
			for i := range plugins.External {
				if plugins.External[i].ID == id {
					plugins.External[i].Enabled = enabled
					return
				}
			}
		}
	}
	return d
}

func protocolScope(scope string) Scope {
	if scope == config.PluginScopeProject {
		return ScopeProject
	}
	return ScopeGlobal
}

func protocolPlacements(placements []string) []Placement {
	if len(placements) == 0 {
		return []Placement{PlacementPanes}
	}
	out := make([]Placement, 0, len(placements))
	for _, p := range placements {
		switch p {
		case config.PluginPlacementTab:
			out = append(out, PlacementTab)
		case config.PluginPlacementPanes:
			out = append(out, PlacementPanes)
		}
	}
	return out
}
