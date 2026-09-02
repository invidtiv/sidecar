package plugin

import (
	"fmt"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/startuptrace"
)

// Registry manages plugin registration and lifecycle.
type Registry struct {
	plugins     []Plugin
	unavailable map[string]string // pluginID -> error reason
	ctx         *Context
	mu          sync.RWMutex
}

// NewRegistry creates a new plugin registry with the given context.
func NewRegistry(ctx *Context) *Registry {
	return &Registry{
		plugins:     make([]Plugin, 0),
		unavailable: make(map[string]string),
		ctx:         ctx,
	}
}

// Register adds a plugin to the registry.
// If Init fails, the plugin is marked unavailable (silent degradation).
func (r *Registry) Register(p Plugin) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.safeInit(p); err != nil {
		r.unavailable[p.ID()] = err.Error()
		if r.ctx != nil && r.ctx.Logger != nil {
			r.ctx.Logger.Debug("plugin unavailable", "id", p.ID(), "reason", err)
		}
		return nil // Silent degradation - not an error
	}

	r.plugins = append(r.plugins, p)
	return nil
}

// safeInit calls Init with panic recovery.
func (r *Registry) safeInit(p Plugin) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("panic: %v", rec)
		}
	}()
	defer startuptrace.Begin("plugin.Init:" + p.ID())()
	return p.Init(r.ctx)
}

// Start starts all registered plugins and returns their initial commands.
func (r *Registry) Start() []tea.Cmd {
	r.mu.RLock()
	defer r.mu.RUnlock()

	cmds := make([]tea.Cmd, 0, len(r.plugins))
	for _, p := range r.plugins {
		if cmd := r.safeStart(p); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}

// safeStart calls Start with panic recovery.
func (r *Registry) safeStart(p Plugin) (cmd tea.Cmd) {
	defer func() {
		if rec := recover(); rec != nil {
			if r.ctx != nil && r.ctx.Logger != nil {
				r.ctx.Logger.Error("plugin start panic", "id", p.ID(), "error", rec)
			}
			cmd = nil
		}
	}()
	defer startuptrace.Begin("plugin.Start:" + p.ID())()
	return p.Start()
}

// Stop stops all registered plugins in reverse order.
func (r *Registry) Stop() {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for i := len(r.plugins) - 1; i >= 0; i-- {
		r.safeStop(r.plugins[i])
	}
}

// safeStop calls Stop with panic recovery.
func (r *Registry) safeStop(p Plugin) {
	defer func() {
		if rec := recover(); rec != nil {
			if r.ctx != nil && r.ctx.Logger != nil {
				r.ctx.Logger.Error("plugin stop panic", "id", p.ID(), "error", rec)
			}
		}
	}()
	p.Stop()
}

// Plugins returns all active plugins.
func (r *Registry) Plugins() []Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]Plugin, len(r.plugins))
	copy(result, r.plugins)
	return result
}

// Replace stores p at index i in the live registry. Plugins() returns a copy,
// so a caller that received a new value from Update must persist it here.
func (r *Registry) Replace(i int, p Plugin) {
	if r == nil || p == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if i >= 0 && i < len(r.plugins) {
		r.plugins[i] = p
	}
}

// Get returns a plugin by ID, or nil if not found.
func (r *Registry) Get(id string) Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, p := range r.plugins {
		if p.ID() == id {
			return p
		}
	}
	return nil
}

// Unavailable returns a map of plugin IDs to their failure reasons.
func (r *Registry) Unavailable() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string]string, len(r.unavailable))
	for k, v := range r.unavailable {
		result[k] = v
	}
	return result
}

// Reinit stops all plugins, updates the context with a new WorkDir and ProjectRoot,
// and reinitializes all plugins. Returns the start commands for all plugins.
// Local callers pass empty host identity; HostID/HostIncarnation/ProjectKey are
// cleared in the same lock so a remote bind cannot race Context() mutation.
func (r *Registry) Reinit(newWorkDir, newProjectRoot string) []tea.Cmd {
	return r.ReinitHost(newWorkDir, newProjectRoot, HostBind{})
}

// HostBind is the remote identity plugins are reinitialized against. The zero
// value is this machine, which is what Reinit passes.
type HostBind struct {
	HostID      string
	Incarnation uint64
	// ProjectKey is the owning host's inventory key: canonical(root) on that
	// machine, a path-shaped string that is not a path here.
	ProjectKey string
	// WorktreeKey is the bound worktree's canonical path on that machine, or
	// empty for the project's main checkout. It is the second half of the
	// durable workspace id a plugin reads host content through.
	WorktreeKey string
}

// ReinitHost is Reinit with host identity set in the same lock as WorkDir.
func (r *Registry) ReinitHost(newWorkDir, newProjectRoot string, bind HostBind) []tea.Cmd {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.ctx == nil {
		return nil
	}

	// Stop all plugins in reverse order
	for i := len(r.plugins) - 1; i >= 0; i-- {
		r.safeStop(r.plugins[i])
	}

	// Update context with new working directory, project root, and host bind.
	r.ctx.WorkDir = newWorkDir
	r.ctx.ProjectRoot = newProjectRoot
	r.ctx.HostID = bind.HostID
	r.ctx.HostIncarnation = bind.Incarnation
	r.ctx.ProjectKey = bind.ProjectKey
	r.ctx.HostWorktreeKey = bind.WorktreeKey

	// Increment epoch to invalidate all pending async messages from previous project
	r.ctx.Epoch++

	// Reinitialize all plugins with the new context
	for _, p := range r.plugins {
		if err := r.safeInit(p); err != nil {
			if r.ctx != nil && r.ctx.Logger != nil {
				r.ctx.Logger.Error("plugin reinit failed", "id", p.ID(), "error", err)
			}
		}
	}

	// Collect start commands
	cmds := make([]tea.Cmd, 0, len(r.plugins))
	for _, p := range r.plugins {
		if cmd := r.safeStart(p); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}

// SetHostIncarnation updates the bound host's serve identity without a Reinit.
// A bump while bound is a re-resolve, not a silent continuation.
func (r *Registry) SetHostIncarnation(n uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ctx != nil {
		r.ctx.HostIncarnation = n
	}
}

// Context returns the shared plugin context, or nil when the registry was
// built without one. App-owned surfaces that are not registry plugins (the
// global Tasks host) read it to inherit config, logger, and adapters without
// taking part in the registry's per-project lifecycle.
func (r *Registry) Context() *Context {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.ctx
}
