package workspace

import (
	"sync"

	"github.com/marcus/sidecar/internal/agentintegration"
	"github.com/marcus/sidecar/internal/agentresolve"
	"github.com/marcus/sidecar/internal/config"
)

// The project Workspace surface's binding to the shared lifecycle resolver.
//
// Both polling paths in this package — worktree agents in agent.go and managed
// shells in shell.go — read the source through [lifecycleSource] and pass it to
// [agentresolve.Result]. Neither decides anything about authority; that lives in
// one place for all three surfaces.
//
// The source is package-level rather than plugin-state for a specific reason:
// the polling functions run inside tea.Cmd closures that are constructed
// without a reference to the plugin, and threading one through every capture
// path would touch far more of the terminal code than a lifecycle binding
// should. It is set once, when the process learns whether an integration is
// installed, and read from command goroutines, so it is guarded.
var lifecycle struct {
	mu  sync.RWMutex
	src agentresolve.Source
}

var defaultLifecycleOnce sync.Once

// lifecycleSource returns the source to consult, installing the default one on
// first use.
//
// The default is enabled rather than opt-in, and that is safe for a specific
// reason rather than by optimism: with no lifecycle log on disk the source
// answers "no evidence", and the shared resolver's no-evidence path returns
// exactly what agentactivity.Detect returned before any of this existed. A
// machine where nobody has installed an integration therefore behaves
// identically, and the first poll costs one stat of a file that is not there.
//
// Construction does no I/O — it joins a path and allocates two maps — so this
// stays off the startup and first-frame paths even though it is reached from a
// polling command.
func lifecycleSource() agentresolve.Source {
	lifecycle.mu.RLock()
	src := lifecycle.src
	lifecycle.mu.RUnlock()
	if src != nil {
		return src
	}
	defaultLifecycleOnce.Do(func() {
		if dir := config.StateDir(); dir != "" {
			SetLifecycleSource(agentintegration.NewStoreSource(dir))
		}
	})
	lifecycle.mu.RLock()
	defer lifecycle.mu.RUnlock()
	return lifecycle.src
}

// SetLifecycleSource installs the lifecycle evidence source for this surface.
//
// Passing nil removes it, which must always be safe and must immediately return
// every pane to ordinary screen detection — that is the "integration removed or
// unhealthy" path, and it has to be a single assignment rather than a teardown
// sequence so it cannot half-happen.
func SetLifecycleSource(src agentresolve.Source) {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	lifecycle.src = src
}
