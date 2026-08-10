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

// Entry is one plugin slot in tab order. New is deferred so Plan stays free of
// plugin construction and callers that only care about order (tests, tooling)
// never build a plugin.
type Entry struct {
	ID  string
	New func() plugin.Plugin
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
	if cfg.Plugins.Conversations.Enabled {
		base = append(base, Entry{IDConversations, func() plugin.Plugin { return conversations.New() }})
	}
	// The workspace plugin has no enable switch; it is sidecar's core tab.
	base = append(base, Entry{IDWorkspace, func() plugin.Plugin { return workspace.New() }})
	if features.IsEnabled(features.NotesPlugin.Name) {
		base = append(base, Entry{IDNotes, func() plugin.Plugin { return notes.New() }})
	}

	if !features.IsEnabled(features.TasksPlugin.Name) {
		return base
	}
	return insertTasks(base, cfg.Plugins.Tasks.Position)
}

// insertTasks places the Tasks entry after the configured anchor.
//
// Fallback rule, in order:
//  1. the configured anchor, when it is present;
//  2. the other anchor, when it is present (a user who asked for after-notes
//     with notes disabled gets the default placement rather than a tab that
//     wanders to whatever happens to be last);
//  3. the end of the list, when neither anchor is present.
func insertTasks(base []Entry, position string) []Entry {
	entry := Entry{IDTasks, func() plugin.Plugin { return tasks.New() }}

	anchors := []string{IDWorkspace, IDNotes}
	if position == config.TasksPositionAfterNotes {
		anchors = []string{IDNotes, IDWorkspace}
	}

	for _, anchor := range anchors {
		if idx := indexOf(base, anchor); idx >= 0 {
			out := make([]Entry, 0, len(base)+1)
			out = append(out, base[:idx+1]...)
			out = append(out, entry)
			out = append(out, base[idx+1:]...)
			return out
		}
	}
	return append(append([]Entry(nil), base...), entry)
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
