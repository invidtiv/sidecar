package plugin

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/marcus/sidecar/internal/adapter"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/event"
	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/tty"
)

// BindingRegistrar allows plugins to register key bindings dynamically.
// This is implemented by keymap.Registry.
type BindingRegistrar interface {
	RegisterPluginBinding(key, command, context string)
}

// Context provides shared resources to plugins during initialization.
type Context struct {
	WorkDir     string // Actual working directory (worktree path for linked worktrees)
	ProjectRoot string // Main repo root for shared state (same as WorkDir for non-worktrees)
	// HostID is empty for a local project. When set, this TUI is bound to a
	// remote host project: WorkDir/ProjectRoot are not local paths, plugins
	// that still assume a local tree must refuse, and Workspaces lists that
	// host's inventory.
	HostID          string
	HostIncarnation uint64
	ProjectKey      string // owning host's unscoped inventory key; empty when local
	ConfigDir       string
	Config          *config.Config
	Adapters        map[string]adapter.Adapter
	EventBus        *event.Dispatcher
	Logger          *slog.Logger
	Keymap          BindingRegistrar // For plugins to register dynamic bindings
	Epoch           uint64           // Incremented on project switch to invalidate stale async messages
	// HostWorkspaces returns the bound host project's in-memory workspaces.
	// The app fills this from overview.HostCatalog; it is not reset by Reinit.
	// The type is plugin-owned so this package does not import workspaceinventory
	// (that import is a test cycle through agentintegration).
	HostWorkspaces func() []HostWorkspace
	// RemoteControlSpawner returns the control-mode proxy for the bound host,
	// or nil when that host is not connected. Filled by the app from the
	// Sessions spawner; not reset by Reinit.
	RemoteControlSpawner func() tty.ControlSpawner
	// RemoteRunner runs a sidecar command on a registered host (hosts.RunSidecar).
	// The app fills it from the Sessions runner; the workspace plugin uses it
	// for content reads and relayed uirequest acks. Not reset by Reinit.
	RemoteRunner func(ctx context.Context, hostID string, args []string, out any) error
	// HostVerbs is the bound host's advertised CLI verbs, or zero. Filled by
	// the app from the Sessions hello; the workspace plugin builds RemoteSource
	// from this plus RemoteRunner so this package does not import overview.
	HostVerbs func() hostproto.VerbCapabilities
}

// HostWorkspace is one host-side shell or worktree as the bound workspace
// plugin lists it. Fields match workspaceinventory.Workspace enough to
// populate the sidebar and attach a live pane.
type HostWorkspace struct {
	ID         string
	Kind       string
	Name       string
	Key        string
	Path       string
	TmuxName   string
	PaneID     string
	Provider   string
	Branch     string
	TaskID     string
	Live       bool
	IsMain     bool
	IsMissing  bool
	IsBare     bool
	IsDetached bool
	IsLocked   bool
	IsPrunable bool
	CreatedAt  time.Time
}

// Remote reports whether this context is bound to a host project rather than
// a local directory.
func (c *Context) Remote() bool {
	return c != nil && c.HostID != ""
}

// FormatRemoteUnavailable is the one-line reason Files/Git/td/Tasks show when
// bound to a host they cannot yet browse.
func FormatRemoteUnavailable(pluginName, hostID string) string {
	if hostID == "" {
		return pluginName + " is unavailable"
	}
	return fmt.Sprintf("%s is unavailable on [%s]", pluginName, hostID)
}

// HostInventoryMsg tells a bound workspace plugin that the host catalog it
// lists from has changed. The app delivers it after overview.IsHostMessage
// so the sidebar refreshes on the same tick.
type HostInventoryMsg struct{}
