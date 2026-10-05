package cli

import (
	"context"
	"sync"
	"time"

	"github.com/marcus/sidecar/internal/mobile"
	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/tmuxserver"
)

const (
	// sharedCatalogMaxAge bounds how long one local collection answers later
	// requests. It is measured from when the collection started, so it is the
	// whole staleness a reader can see beyond the collection's own duration.
	sharedCatalogMaxAge = 1500 * time.Millisecond
	// sharedCatalogCollectTimeout bounds a collection that has outlived the
	// request that started it but still has other waiters.
	sharedCatalogCollectTimeout = 30 * time.Second
)

// catalogFence is the cheap evidence a cached local collection is still about
// the same world: the owner configuration it was collected under and the tmux
// server it observed. Either changing retires the collection immediately.
type catalogFence struct {
	configGeneration string
	tmuxServer       string
}

// sharedCatalog lets the requests a long-lived backend serves at once share
// one local collection. A UI page load asks for /sessions and a project's
// workspace, the events stream recomputes both for every connected client,
// and each used to run its own full collection at the same moment; on a busy
// machine the pile-up was slow enough to push the local owner past its catalog
// deadline and report this machine offline.
//
// Only collected inventory is shared. Every collection is a fresh provider
// (fresh activity seed), exactly as an unshared request would build. A request
// reuses a collection only when it is in flight or started within
// sharedCatalogMaxAge, no invalidation has arrived since it started, and the
// owner config generation and tmux server still match. Each request then gets
// its own authorization state from authorize (a fresh managed-target scan of
// the shell manifests and registered worktrees, and a fresh tmux pane
// listing), so a row is advertised attachable only if the current resolver
// accepts it; only that scan's Git worktree discovery is shared with the
// collection. Every identity is checked fresh again when a target is resolved
// or opened.
type sharedCatalog struct {
	newProvider func() mobile.CatalogProvider
	authorize   func(mobile.CatalogInput, *worktreeDiscovery) mobile.CatalogInput
	fence       func(context.Context) (catalogFence, error)
	now         func() time.Time
	maxAge      time.Duration

	mu      sync.Mutex
	epoch   uint64
	current *catalogFlight
}

type catalogFlight struct {
	done    chan struct{}
	epoch   uint64
	fence   catalogFence
	started time.Time
	input   mobile.CatalogInput
	// discovery is this collection's Git worktree discovery, shared by the
	// requests that authorize against it.
	discovery *worktreeDiscovery
	err       error
}

// newSharedCatalog shares what newProvider collects and runs authorize on
// every request's copy. newProvider must return only immutable collected data;
// anything that decides authority now belongs in authorize.
func newSharedCatalog(newProvider func() mobile.CatalogProvider, authorize func(mobile.CatalogInput, *worktreeDiscovery) mobile.CatalogInput) *sharedCatalog {
	if authorize == nil {
		authorize = func(input mobile.CatalogInput, _ *worktreeDiscovery) mobile.CatalogInput { return input }
	}
	return &sharedCatalog{newProvider: newProvider, authorize: authorize, fence: currentCatalogFence, now: time.Now, maxAge: sharedCatalogMaxAge}
}

// Invalidate retires every collection started before this call. Requests that
// arrive afterwards never join or reuse one.
func (c *sharedCatalog) Invalidate() {
	c.mu.Lock()
	c.epoch++
	c.mu.Unlock()
}

// Provider is a mobile.CatalogProvider backed by the shared collection.
func (c *sharedCatalog) Provider() mobile.CatalogProvider { return c.input }

func (c *sharedCatalog) input(ctx context.Context) (mobile.CatalogInput, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	fence, err := c.fence(ctx)
	if err != nil {
		// Without the evidence a reuse needs, collect for this caller alone;
		// its own generation checks report the underlying failure.
		input, err := c.newProvider()(ctx)
		if err != nil {
			return mobile.CatalogInput{}, err
		}
		return c.authorize(input, newWorktreeDiscovery()), nil
	}
	c.mu.Lock()
	flight := c.current
	if !c.reusable(flight, fence) {
		flight = &catalogFlight{done: make(chan struct{}), epoch: c.epoch, fence: fence, started: c.now(), discovery: newWorktreeDiscovery()}
		c.current = flight
		go c.collect(context.WithoutCancel(ctx), flight)
	}
	c.mu.Unlock()
	select {
	case <-flight.done:
		if flight.err != nil {
			return mobile.CatalogInput{}, flight.err
		}
		return c.authorize(flight.input, flight.discovery), nil
	case <-ctx.Done():
		return mobile.CatalogInput{}, ctx.Err()
	}
}

// reusable is called with mu held.
func (c *sharedCatalog) reusable(flight *catalogFlight, fence catalogFence) bool {
	if flight == nil || flight.epoch != c.epoch || flight.fence != fence {
		return false
	}
	select {
	case <-flight.done:
		return flight.err == nil && c.now().Sub(flight.started) < c.maxAge
	default:
		return true // in flight, started after the last invalidation
	}
}

func (c *sharedCatalog) collect(ctx context.Context, flight *catalogFlight) {
	ctx, cancel := context.WithTimeout(ctx, sharedCatalogCollectTimeout)
	defer cancel()
	defer close(flight.done)
	flight.input, flight.err = c.newProvider()(ctx)
}

func currentCatalogFence(ctx context.Context) (catalogFence, error) {
	generation, err := currentMobileConfigGeneration(ctx)
	if err != nil {
		return catalogFence{}, err
	}
	return catalogFence{configGeneration: generation, tmuxServer: tmuxServerIdentity()}, nil
}

// tmuxServerIdentity names the server behind this process's socket by the
// socket file's device, inode and modification time (see
// tmuxserver.SocketBinding): a restarted server binds a new socket file.
func tmuxServerIdentity() string {
	return tmuxserver.SocketBinding(tmuxenv.SocketPath())
}
