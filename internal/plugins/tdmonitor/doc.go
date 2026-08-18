// Package tdmonitor embeds the td task manager monitor TUI as a sidecar
// plugin, providing task/issue tracking with kanban boards and setup flows.
//
// Embedding & Theme Contract:
//
// The embedded td monitor model receives Sidecar's resolved palette as a
// host-neutral monitor.Theme at construction (via monitor.EmbeddedOptions.Theme)
// and dynamically at runtime via model.SetTheme(buildTheme()).
//
// When themes change in Sidecar (preview movement, cancellation restore,
// confirmation, project switching, or Configuration changes), the host
// delivers msg.ThemeChangedMsg to all plugins. The tdmonitor plugin consumes
// this message and updates the running monitor's theme in place without
// resetting navigation, selection, modal, form, or polling state.
//
// Outer gradient chrome continues to be provided via styles.CreateTDPanelRenderer
// and styles.CreateTDModalRenderer closures, which derive special and depth
// gradient stops dynamically from the active theme's semantic colors.
package tdmonitor
