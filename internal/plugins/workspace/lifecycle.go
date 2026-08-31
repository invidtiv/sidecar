package workspace

import (
	"sync"

	"github.com/marcus/sidecar/internal/agentresolve"
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

// lifecycleSource returns the current source, or nil.
//
// Nil is the ordinary state and is not a degraded one: with no source, the
// shared resolver returns exactly what agentactivity.Detect returned before the
// extraction, which is what lets the Phase A compatibility fixtures pass
// unmodified.
func lifecycleSource() agentresolve.Source {
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
