// Package config handles loading, saving, and validating user configuration
// from JSON files, including project settings, plugin options, keymaps, and UI
// preferences.
//
// # Tasks plugin
//
// The embedded Tasks tab is off by default. It is turned on with the
// "tasks_plugin" feature flag, not with a plugin config key:
//
//	{
//	  "features": { "flags": { "tasks_plugin": true } }
//	}
//
// Tasks is a tab of the global space — [Agents] [Workspaces] [Tasks], reached
// with K or the Sidecar brand — not a project plugin, so it is not part of the
// project tab order and "plugins.tasks.position" no longer moves it. The field
// is still accepted and validated so older configs keep loading.
//
// There is deliberately no Tasks store or JSONL path here. The embedded Tasks
// package performs its own normal configuration resolution.
package config
