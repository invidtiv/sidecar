package plugin

import tea "charm.land/bubbletea/v2"

// CursorProvider is an optional capability for plugins that own the terminal
// cursor. Coordinates are local to the plugin content area; the app applies its
// header offset and suppresses the cursor while blurred or covered by a modal.
type CursorProvider interface {
	Cursor() *tea.Cursor
}

// MouseModeProvider is an optional capability for plugins that need a more
// specific mouse reporting mode in their current view. Sidecar defaults to
// all-motion for hover-driven UI and lets embedded terminal contexts request
// cell-motion so only clicks, drags, releases, and wheels are reported.
type MouseModeProvider interface {
	PreferredMouseMode() tea.MouseMode
}
