package cli

import (
	"context"
	"sync"
	"time"

	"github.com/marcus/sidecar/internal/managedtarget"
)

// worktreeDiscoveryTimeout bounds one shared Git worktree discovery.
const worktreeDiscoveryTimeout = 10 * time.Second

// worktreeDiscovery memoizes Git worktree discovery for one shared catalog
// collection. It is collected inventory (the same `git worktree list` the
// collection's rows came from), not authorization: each request still reads
// shell manifests, registered worktrees and tmux itself.
//
// A discovery runs under its own bounded context, never a caller's, so a
// caller that gives up cannot leave a truncated answer for the others; that
// caller just stops waiting.
type worktreeDiscovery struct {
	mu    sync.Mutex
	roots map[string]*discoveryFlight
}

type discoveryFlight struct {
	done  chan struct{}
	roots []string
}

func newWorktreeDiscovery() *worktreeDiscovery {
	return &worktreeDiscovery{roots: map[string]*discoveryFlight{}}
}

// Discover is a managedtarget.DiscoverFunc.
func (d *worktreeDiscovery) Discover(ctx context.Context, proj managedtarget.Project) []string {
	if ctx == nil {
		ctx = context.Background()
	}
	d.mu.Lock()
	flight, ok := d.roots[proj.Path]
	if !ok {
		flight = &discoveryFlight{done: make(chan struct{})}
		d.roots[proj.Path] = flight
		go func() {
			defer close(flight.done)
			runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), worktreeDiscoveryTimeout)
			defer cancel()
			flight.roots = managedtarget.DiscoverWorktreeRoots(runCtx, proj)
		}()
	}
	d.mu.Unlock()
	select {
	case <-flight.done:
		return append([]string(nil), flight.roots...)
	case <-ctx.Done():
		return nil
	}
}
