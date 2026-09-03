// Package assembly owns the ordered catalog of plugins sidecar can host, and
// therefore the tab order.
//
// It exists so tab order is data rather than the sequence of `if enabled {
// register(...) }` statements it used to be in cmd/sidecar/main.go. Descriptors
// is that data; Plan is a pure function of it plus config and feature flags, so
// every enabled/disabled combination is unit-testable without booting the
// application.
//
// It lives under internal/plugins rather than internal/app because the plugin
// packages themselves import internal/app; an assembly there would be an
// import cycle. That is also why internal/app keeps its own list of the
// global-scope descriptors: it cannot import this package, and an assembly test
// pins the two lists together.
package assembly

import (
	"log/slog"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/plugins/conversations"
	"github.com/marcus/sidecar/internal/plugins/filebrowser"
	"github.com/marcus/sidecar/internal/plugins/gitstatus"
	"github.com/marcus/sidecar/internal/plugins/notes"
	"github.com/marcus/sidecar/internal/plugins/tasks"
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
	IDTasks         = "tasks"
)

// Descriptors is every plugin Sidecar can host, in the order the header paints
// them: the project tabs first, in tab order, then the global-space tabs.
//
// It is the one list the tab assembly, the settings page, and
// `sidecar plugin list` read. A plugin that is not here does not exist as far
// as Sidecar is concerned, whether or not its package compiles.
func Descriptors() []plugin.Descriptor {
	return []plugin.Descriptor{
		tdmonitor.Descriptor(),
		gitstatus.Descriptor(),
		filebrowser.Descriptor(),
		conversations.Descriptor(),
		workspace.Descriptor(),
		notes.Descriptor(),
		tasks.Descriptor(),
	}
}

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
	return conversations.Descriptor().IsEnabled(cfg)
}

// NotesWanted reports whether the Notes surface belongs in the project tab
// ring. td owns Notes persistence, so disabling the existing td panel also
// hides Notes without rewriting the user's independent Notes preference.
func NotesWanted(cfg *config.Config) bool {
	return notes.Descriptor().IsEnabled(cfg)
}

// Plan returns the project plugins to register, in tab order.
//
// Tab shortcut numbers are derived from this list. Nothing may assume a plugin
// occupies a fixed position: any of the preceding plugins can be disabled.
//
// Global-scope descriptors are deliberately absent. Tasks is the standing
// example: registering it here would put its store, session, and agent queue
// back under registry.Reinit, which rebuilt the whole Tasks model on every
// project switch.
func Plan(cfg *config.Config) []Entry {
	var base []Entry
	for _, d := range Descriptors() {
		if d.Scope != plugin.ScopeProject || !d.HasPlacement(plugin.PlacementTab) {
			continue
		}
		if !d.IsEnabled(cfg) {
			continue
		}
		base = append(base, Entry{ID: d.ID, New: d.New})
	}
	return base
}

// GlobalDescriptors returns the enabled global-scope tab descriptors, in
// header order. internal/app builds one host per entry.
func GlobalDescriptors(cfg *config.Config) []plugin.Descriptor {
	var out []plugin.Descriptor
	for _, d := range Descriptors() {
		if d.Scope != plugin.ScopeGlobal || !d.HasPlacement(plugin.PlacementTab) {
			continue
		}
		if !d.IsEnabled(cfg) {
			continue
		}
		out = append(out, d)
	}
	return out
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
