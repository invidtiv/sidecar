package cli

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/mobile"
)

type shareHarness struct {
	catalog *sharedCatalog
	calls   atomic.Int32
	release chan struct{}
	fence   catalogFence
	fenceMu sync.Mutex
	now     time.Time
	nowMu   sync.Mutex
	fail    atomic.Bool
}

func newShareHarness() *shareHarness {
	h := &shareHarness{fence: catalogFence{configGeneration: "g1", tmuxServer: "s1"}, now: time.Unix(1000, 0)}
	h.catalog = newSharedCatalog(func() mobile.CatalogProvider {
		return func(ctx context.Context) (mobile.CatalogInput, error) {
			n := h.calls.Add(1)
			if h.release != nil {
				select {
				case <-h.release:
				case <-ctx.Done():
					return mobile.CatalogInput{}, ctx.Err()
				}
			}
			if h.fail.Load() {
				return mobile.CatalogInput{}, errors.New("collection failed")
			}
			return mobile.CatalogInput{ObservedAt: time.Unix(int64(n), 0)}, nil
		}
	})
	h.catalog.fence = func(context.Context) (catalogFence, error) {
		h.fenceMu.Lock()
		defer h.fenceMu.Unlock()
		return h.fence, nil
	}
	h.catalog.now = func() time.Time {
		h.nowMu.Lock()
		defer h.nowMu.Unlock()
		return h.now
	}
	return h
}

func (h *shareHarness) advance(d time.Duration) {
	h.nowMu.Lock()
	h.now = h.now.Add(d)
	h.nowMu.Unlock()
}

func (h *shareHarness) setFence(f catalogFence) {
	h.fenceMu.Lock()
	h.fence = f
	h.fenceMu.Unlock()
}

func (h *shareHarness) get(t *testing.T) int64 {
	t.Helper()
	input, err := h.catalog.Provider()(context.Background())
	if err != nil {
		t.Error(err) // callable from goroutines
		return -1
	}
	return input.ObservedAt.Unix()
}

// The pile-up that reported this machine offline: a page load and every
// connected events stream asked at once and each ran a full collection.
// Concurrent requests now share one.
func TestSharedCatalogCoalescesConcurrentRequests(t *testing.T) {
	h := newShareHarness()
	h.release = make(chan struct{})
	var wg sync.WaitGroup
	got := make([]int64, 12)
	for i := range got {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i] = h.get(t)
		}(i)
	}
	deadline := time.Now().Add(2 * time.Second)
	for h.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond) // let every request join
	close(h.release)
	wg.Wait()
	if calls := h.calls.Load(); calls != 1 {
		t.Fatalf("%d collections for one burst, want 1", calls)
	}
	for _, v := range got {
		if v != 1 {
			t.Fatalf("requests saw different collections: %v", got)
		}
	}
}

func TestSharedCatalogReuseIsBoundedByAgeFenceAndInvalidation(t *testing.T) {
	h := newShareHarness()
	first, second := h.get(t), h.get(t)
	if first != 1 || second != 1 {
		t.Fatalf("a fresh collection was not reused (%d, %d)", first, second)
	}
	h.advance(sharedCatalogMaxAge)
	if got := h.get(t); got != 2 {
		t.Fatalf("collection older than max age reused (got %d)", got)
	}
	h.setFence(catalogFence{configGeneration: "g2", tmuxServer: "s1"})
	if got := h.get(t); got != 3 {
		t.Fatalf("collection from another config generation reused (got %d)", got)
	}
	h.setFence(catalogFence{configGeneration: "g2", tmuxServer: "s2"})
	if got := h.get(t); got != 4 {
		t.Fatalf("collection from another tmux server reused (got %d)", got)
	}
	h.catalog.Invalidate()
	if got := h.get(t); got != 5 {
		t.Fatalf("collection reused across an invalidation (got %d)", got)
	}
	if got := h.get(t); got != 5 {
		t.Fatalf("collection after invalidation not reused (got %d)", got)
	}
}

// A request that arrives after an invalidation never joins a collection that
// started before it, even one still running.
func TestSharedCatalogInvalidationDetachesInFlightCollection(t *testing.T) {
	h := newShareHarness()
	h.release = make(chan struct{})
	first := make(chan int64, 1)
	go func() { first <- h.get(t) }()
	for h.calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	h.catalog.Invalidate()
	second := make(chan int64, 1)
	go func() { second <- h.get(t) }()
	for h.calls.Load() < 2 {
		time.Sleep(time.Millisecond)
	}
	close(h.release)
	if a, b := <-first, <-second; a == b {
		t.Fatalf("post-invalidation request joined the earlier collection (%d, %d)", a, b)
	}
}

func TestSharedCatalogNeverReusesAFailure(t *testing.T) {
	h := newShareHarness()
	h.fail.Store(true)
	if _, err := h.catalog.Provider()(context.Background()); err == nil {
		t.Fatal("failure not reported")
	}
	h.fail.Store(false)
	if got := h.get(t); got != 2 {
		t.Fatalf("failed collection reused (got %d)", got)
	}
}

// The request that started a collection can leave; the others still get it.
func TestSharedCatalogSurvivesTheStartingRequestLeaving(t *testing.T) {
	h := newShareHarness()
	h.release = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	firstErr := make(chan error, 1)
	go func() {
		_, err := h.catalog.Provider()(ctx)
		firstErr <- err
	}()
	for h.calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	second := make(chan int64, 1)
	go func() { second <- h.get(t) }()
	time.Sleep(10 * time.Millisecond)
	cancel()
	if err := <-firstErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("leaving request got %v", err)
	}
	close(h.release)
	if got := <-second; got != 1 || h.calls.Load() != 1 {
		t.Fatalf("remaining request got %d after %d collections", got, h.calls.Load())
	}
}
