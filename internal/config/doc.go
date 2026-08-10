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
//	  "features": { "flags": { "tasks_plugin": true } },
//	  "plugins":  { "tasks":  { "position": "after-workspaces" } }
//	}
//
// "position" is the anchor the Tasks tab is inserted after — either
// "after-workspaces" (the default) or "after-notes". Any other value is
// coerced back to the default by Validate. When the configured anchor is not
// registered, placement falls back to the other anchor, and then to the end of
// the tab list; see internal/plugins/assembly.
//
// The resulting tab shortcut number is derived state. Tasks is not "tab N":
// disabling td-monitor, git-status, file-browser, conversations, or notes
// shifts it. Read the position from the registered plugin order, never from a
// hardcoded index.
//
// There is deliberately no Tasks store or JSONL path here. The embedded Tasks
// package performs its own normal configuration resolution.
package config
