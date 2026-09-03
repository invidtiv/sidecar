package plugin

import (
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/version"
)

// Descriptor is everything Sidecar knows about a plugin before it runs.
//
// It is data. The tab assembly, the settings page, the global tab list, the
// palette, and `sidecar plugin list` all read this one value rather than each
// keeping its own list of what exists — which is how "global plugin" stopped
// being a hardcoded special case for Tasks.
//
// A descriptor never reaches for the application: it names a class, a scope,
// where its content can be placed, how to answer "is this enabled", and (for
// the embedded class) how to construct the plugin. Everything host-shaped is
// the host's business.
type Descriptor struct {
	// ID is stable: the config key under plugins, the CLI name, and the
	// persisted tab identity. It matches the constructed plugin's ID().
	ID string
	// Name is the header label. It is not required to match Plugin.Name(),
	// which is what help and the palette document the surface by.
	Name string
	// Icon is the single-character header glyph.
	Icon string
	// Class decides who renders: an embedded plugin owns its own Bubble Tea
	// frame, a protocol plugin is rendered by the host.
	Class Class
	// Scope decides lifecycle: a project plugin lives in the registry and is
	// reinitialized on a project switch, a global plugin is built once and
	// survives both project switches and scope toggles.
	Scope Scope
	// Placements are the surfaces this plugin's content can occupy.
	Placements []Placement
	// Detail is the one line the settings page prints under the name, saying
	// what the surface is for.
	Detail string
	// Why is the sentence the install route leads with, for a plugin backed by
	// a command the user has to have. Empty for one that ships in-repo.
	Why string
	// Beta marks a surface that carries the beta badge.
	Beta bool
	// Enabled answers whether Sidecar builds this plugin, reading
	// plugins.<id>.enabled and any legacy switch the plugin migrated from so a
	// config written before the unified key keeps working. A nil Enabled means
	// the plugin has no switch at all — the workspace tab is exactly that.
	Enabled func(*config.Config) bool
	// Preference answers what the user chose, ignoring anything outside this
	// plugin's own switch. It exists because the two questions differ: Notes
	// needs the td panel, so a Notes preference of "on" with td off must still
	// render as ON in settings rather than reading as a choice nobody made.
	// Nil means the preference and the effective answer are the same question.
	Preference func(*config.Config) bool
	// SetEnabled writes the unified plugins.<id>.enabled key. It never touches
	// a legacy feature flag: the flag is a read-only alias, so a user who once
	// set it keeps it while the config key becomes the answer. Nil means the
	// plugin has no switch.
	SetEnabled func(*config.PluginsConfig, bool)
	// New constructs the in-process plugin. Embedded class only; it is deferred
	// so a caller that only wants the order (tests, the CLI, the settings page)
	// never builds a plugin.
	New func() Plugin
	// Integration names the external command and formula the plugin needs, for
	// the settings page's install route. The zero value means the plugin ships
	// inside Sidecar and has nothing to install.
	Integration version.Descriptor
	// Instance is the configured entry a protocol descriptor was projected
	// from, carrying the config section it was read from. Nil for the embedded
	// class, which has a Go literal instead.
	Instance *config.PluginInstance
}

// Source reports the config section this descriptor was read from, or "" for an
// embedded plugin, whose descriptor is a Go literal.
func (d Descriptor) Source() string {
	if d.Instance == nil {
		return ""
	}
	return d.Instance.Source
}

// Class is who renders a plugin.
type Class string

const (
	// ClassEmbedded is a Go plugin compiled into Sidecar with its own UI.
	ClassEmbedded Class = "embedded"
	// ClassProtocol is an external executable the host renders. Its descriptor
	// is projected from a config entry by ProtocolDescriptor.
	ClassProtocol Class = "protocol"
)

// Scope is a plugin's lifecycle.
type Scope string

const (
	// ScopeProject plugins are registry members, reinitialized per project.
	ScopeProject Scope = "project"
	// ScopeGlobal plugins are built once and outlive every project switch.
	ScopeGlobal Scope = "global"
)

// Placement is where a plugin's content can show.
type Placement string

const (
	// PlacementTab is a navbar entry.
	PlacementTab Placement = "tab"
	// PlacementPanes means the plugin's content can open as leaves in the pane
	// decks of both workspace projections.
	PlacementPanes Placement = "panes"
)

// IsEnabled answers the descriptor's own switch against cfg. A descriptor with
// no Enabled func is always on: that is the honest answer for a plugin the user
// cannot turn off, and it keeps every caller free of nil checks.
func (d Descriptor) IsEnabled(cfg *config.Config) bool {
	if d.Enabled == nil {
		return true
	}
	if cfg == nil {
		cfg = config.Default()
	}
	return d.Enabled(cfg)
}

// IsPreferred answers the user's own switch for this plugin, ignoring
// dependencies on other surfaces. The settings page renders this.
func (d Descriptor) IsPreferred(cfg *config.Config) bool {
	if d.Preference != nil {
		if cfg == nil {
			cfg = config.Default()
		}
		return d.Preference(cfg)
	}
	return d.IsEnabled(cfg)
}

// HasSwitch reports whether the user can turn this plugin off at all.
func (d Descriptor) HasSwitch() bool { return d.SetEnabled != nil }

// HasPlacement reports whether the plugin declares p.
func (d Descriptor) HasPlacement(p Placement) bool {
	for _, candidate := range d.Placements {
		if candidate == p {
			return true
		}
	}
	return false
}

// NeedsCommand reports whether enabling this plugin depends on a command
// outside Sidecar.
func (d Descriptor) NeedsCommand() bool { return d.Integration.Executable != "" }
