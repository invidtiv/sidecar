package workspaceinventory

import (
	"sync"

	"github.com/marcus/sidecar/internal/agentintegration"
	"github.com/marcus/sidecar/internal/agentresolve"
	"github.com/marcus/sidecar/internal/config"
)

// The global Sessions surface's binding to the shared lifecycle resolver.
//
// It mirrors internal/plugins/workspace/lifecycle.go deliberately. These are
// two projections of one model, and a lifecycle source installed on one and not
// the other would make the same pane resolve differently depending on which
// screen the user is looking at.
var (
	lifecycleOnce    sync.Once
	lifecycleDefault agentresolve.Source
)

// defaultLifecycleSource returns the process-wide lifecycle source.
//
// Construction does no I/O, so this is safe to reach from a collector that runs
// on a polling cadence. A collector may still override it by setting the field
// directly, which is what tests do.
func defaultLifecycleSource() agentresolve.Source {
	lifecycleOnce.Do(func() {
		if dir := config.StateDir(); dir != "" {
			lifecycleDefault = agentintegration.NewStoreSource(dir)
		}
	})
	return lifecycleDefault
}
