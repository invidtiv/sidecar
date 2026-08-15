package plugin

import tea "charm.land/bubbletea/v2"

// Plugin defines the interface for all sidecar plugins.
type Plugin interface {
	ID() string
	Name() string
	Icon() string
	Init(ctx *Context) error
	Start() tea.Cmd
	Stop()
	Update(msg tea.Msg) (Plugin, tea.Cmd)
	View(width, height int) string
	IsFocused() bool
	SetFocused(bool)
	Commands() []Command
	FocusContext() string
}

// TextInputConsumer is an optional capability for plugins that need
// alphanumeric key input to be forwarded as typed text instead of being
// intercepted by app-level shortcuts.
type TextInputConsumer interface {
	ConsumesTextInput() bool
}

// WheelBoundaryConsumer is an optional fast-path for plugins with scrollable
// surfaces. Bubble Tea asks it before Update and View; returning true drops an
// inertia event that cannot move the surface under the pointer. Implementations
// must return false when they are not certain (for example, a terminal
// application that owns mouse reporting).
//
// The message coordinates are local to the plugin content box, after Sidecar's
// header has been removed. Implementations may reset gesture-only coalescing
// state when they report a boundary, but must not change visible content.
type WheelBoundaryConsumer interface {
	WheelAtBoundary(tea.MouseWheelMsg) bool
}

// GlobalKeyBlocker is an optional capability for plugins with overlays that
// own the keyboard. While it reports true, Sidecar forwards every key except
// its interrupt instead of running host-level shortcuts.
type GlobalKeyBlocker interface {
	BlocksGlobalKeys() bool
}

// KeyRouter is an optional capability for plugins that own keys the host also
// binds globally. It makes sidecar's key precedence explicit instead of
// implicit in the order of a switch statement:
//
//  1. an open sidecar application modal;
//  2. the active plugin's text-input or blocking-overlay context
//     (TextInputConsumer, or BlocksGlobalKeys here);
//  3. an active plugin contextual binding (ClaimsKey here);
//  4. sidecar global bindings;
//  5. unbound input forwarded to the plugin.
//
// Only plugins that implement it participate in levels 2 (overlay) and 3; every
// other plugin keeps the level-4-then-5 behaviour it has always had.
type KeyRouter interface {
	GlobalKeyBlocker

	// BlocksGlobalKeys reports that the plugin is showing an overlay that owns
	// the keyboard. Every key except the host's interrupt is forwarded.
	// ClaimsKey reports that the plugin has a live contextual binding for a
	// key. It is asked only for keys sidecar would otherwise handle globally,
	// and only when no overlay is blocking.
	ClaimsKey(key string) bool

	// QuitKeyExits reports whether `q` in the plugin's current context should
	// reach sidecar's quit flow. It replaces the host's isRootContext guess for
	// plugins that can answer for themselves.
	QuitKeyExits() bool
}

// FooterStatusProvider is an optional capability for plugins with a condition
// that must stay visible even though the host owns the footer. A plugin that
// suppresses its own status line has no other always-on surface.
type FooterStatusProvider interface {
	// FooterStatus returns text for the host footer and whether it is an error.
	// An empty string means "nothing to say".
	FooterStatus() (string, bool)
}

type WorkspaceSelectionKind string

const (
	WorkspaceSelectionWorktree WorkspaceSelectionKind = "worktree"
	WorkspaceSelectionShell    WorkspaceSelectionKind = "shell"
)

// PendingWorkspaceSelection is delivered synchronously after registry.Reinit,
// before the returned async Start commands run.
type PendingWorkspaceSelection struct {
	Kind WorkspaceSelectionKind
	Key  string
	Path string
}

type PendingWorkspaceSelector interface {
	SetPendingWorkspaceSelection(PendingWorkspaceSelection)
}

// Category represents a logical grouping of commands for the command palette.
type Category string

const (
	CategoryNavigation Category = "Navigation"
	CategoryActions    Category = "Actions"
	CategoryView       Category = "View"
	CategorySearch     Category = "Search"
	CategoryEdit       Category = "Edit"
	CategoryGit        Category = "Git"
	CategorySystem     Category = "System"
)

// Command represents a keybinding command exposed by a plugin.
type Command struct {
	ID          string         // Unique identifier (e.g., "stage-file")
	Name        string         // Short name for footer (e.g., "Stage")
	Description string         // Full description for palette
	Category    Category       // Logical grouping for palette display
	Handler     func() tea.Cmd // Action to execute (optional)
	Context     string         // Activation context
	Priority    int            // Footer display priority: 1=highest, 0=default (treated as 99)
}

// DiagnosticProvider is implemented by plugins that expose diagnostics.
type DiagnosticProvider interface {
	Diagnostics() []Diagnostic
}

// Diagnostic represents a health/status check result.
type Diagnostic struct {
	ID     string
	Status string
	Detail string
}

// OpenFileMsg requests opening a file in an external editor.
// Sent by plugins, handled by app to exec the editor process.
type OpenFileMsg struct {
	Editor string // Editor command (e.g., "vim", "code")
	Path   string // File path to open
	LineNo int    // Line number to open at (0 = start of file)
}

// PluginFocusedMsg is sent to a plugin when it becomes the active plugin.
// Plugins can use this to refresh data or update their state on focus.
type PluginFocusedMsg struct{}

// EpochMessage is implemented by async messages that need staleness detection.
// Messages from async operations should embed an Epoch field and implement this interface.
type EpochMessage interface {
	GetEpoch() uint64
}

// IsStale returns true if the message's epoch doesn't match the current context epoch.
// Use this in Update() handlers to discard messages from previous projects:
//
//	if plugin.IsStale(p.ctx, msg) { return p, nil }
func IsStale(ctx *Context, msg EpochMessage) bool {
	return ctx != nil && msg.GetEpoch() != ctx.Epoch
}
