// Package assembly owns the ordered list of plugins sidecar registers, and
// therefore the tab order.
//
// It exists so tab order is data rather than the sequence of `if enabled {
// register(...) }` statements it used to be in cmd/sidecar/main.go. Plan is a
// pure function of config plus feature flags, so every enabled/disabled
// combination is unit-testable without booting the application.
//
// It lives under internal/plugins rather than internal/app because the plugin
// packages themselves import internal/app; an assembly there would be an
// import cycle.
package assembly

import (
	"log/slog"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/plugins/conversations"
	"github.com/marcus/sidecar/internal/plugins/filebrowser"
	"github.com/marcus/sidecar/internal/plugins/gitstatus"
	"github.com/marcus/sidecar/internal/plugins/notes"
	"github.com/marcus/sidecar/internal/plugins/tdmonitor"
	"github.com/marcus/sidecar/internal/plugins/workspace"
)

// Plugin IDs used as ordering anchors. They mirror each plugin's own ID().
const (
	IDTDMonitor     = "td-monitor"
	IDGitStatus     = "git-status"
	IDFileBrowser   = "file-browser"
	IDConversations = "conversations"
	IDWorkspace     = "workspace-manager"
	IDNotes         = "notes"
)

// Entry is one plugin slot in tab order. New is deferred so Plan stays free of
// plugin construction and callers that only care about order (tests, tooling)
// never build a plugin.
type Entry struct {
	ID  string
	New func() plugin.Plugin
}

// ConversationsWanted reports whether the Conversations plugin should be
// registered and whether history adapters should be constructed.
//
// Both the conversations_plugin feature flag and plugins.conversations.enabled
// must be true. When false, Sidecar must not construct adapters, run Detect,
// open session-store watchers, or show the Conversations tab.
func ConversationsWanted(cfg *config.Config) bool {
	if cfg == nil {
		cfg = config.Default()
	}
	if !features.IsEnabled(features.ConversationsPlugin.Name) {
		return false
	}
	return cfg.Plugins.Conversations.Enabled
}

// NotesWanted reports whether the Notes surface belongs in the project tab
// ring. td owns Notes persistence, so disabling the existing td panel also
// hides Notes without rewriting the user's independent Notes preference.
func NotesWanted(cfg *config.Config) bool {
	if cfg == nil {
		cfg = config.Default()
	}
	return cfg.Plugins.TDMonitor.Enabled && features.IsEnabled(features.NotesPlugin.Name)
}

// Plan returns the plugins to register, in tab order.
//
// Tab shortcut numbers are derived from this list. Nothing may assume a plugin
// occupies a fixed position: any of the preceding plugins can be disabled.
func Plan(cfg *config.Config) []Entry {
	var base []Entry

	if cfg == nil {
		cfg = config.Default()
	}

	if cfg.Plugins.TDMonitor.Enabled {
		base = append(base, Entry{IDTDMonitor, func() plugin.Plugin { return tdmonitor.New() }})
	}
	if cfg.Plugins.GitStatus.Enabled {
		base = append(base, Entry{IDGitStatus, func() plugin.Plugin { return gitstatus.New() }})
	}
	if cfg.Plugins.FileBrowser.Enabled {
		base = append(base, Entry{IDFileBrowser, func() plugin.Plugin { return filebrowser.New() }})
	}
	if ConversationsWanted(cfg) {
		base = append(base, Entry{IDConversations, func() plugin.Plugin { return conversations.New() }})
	}
	// The workspace plugin has no enable switch; it is sidecar's core tab.
	base = append(base, Entry{IDWorkspace, func() plugin.Plugin { return workspace.New() }})
	if NotesWanted(cfg) {
		base = append(base, Entry{IDNotes, func() plugin.Plugin { return notes.New() }})
	}

	// Tasks is deliberately absent: it is a global tab owned by the app shell
	// (internal/app/scope.go), not a project plugin. Registering it here would
	// put its store, session, and agent queue back under registry.Reinit, which
	// rebuilt the whole Tasks model on every project switch.
	return base
}

func indexOf(entries []Entry, id string) int {
	for i, e := range entries {
		if e.ID == id {
			return i
		}
	}
	return -1
}

// Register registers the planned plugins with the registry, in order.
// Registration failures degrade silently (the registry records them as
// unavailable); anything unexpected is logged.
func Register(reg *plugin.Registry, cfg *config.Config, logger *slog.Logger) {
	for _, entry := range Plan(cfg) {
		if err := reg.Register(entry.New()); err != nil && logger != nil {
			logger.Warn("failed to register plugin", "id", entry.ID, "err", err)
		}
	}
}
