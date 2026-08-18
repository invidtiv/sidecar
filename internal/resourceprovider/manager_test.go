package resourceprovider

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/resource"
)

// fakeProvider is the in-memory Provider the manager tests run against. It is
// the reason Provider is an interface: cache, dedupe, and concurrency policy are
// host behavior and should be testable without a process.
type fakeProvider struct {
	instance string
	desc     Description
	descErr  error

	mu        sync.Mutex
	resolves  int
	describes int
	inFlight  int
	maxSeen   int

	delay    time.Duration
	identity func(locator string) string
	err      error
}

func (f *fakeProvider) Instance() string { return f.instance }

func (f *fakeProvider) Describe(ctx context.Context) (Description, error) {
	f.mu.Lock()
	f.describes++
	f.mu.Unlock()
	if f.descErr != nil {
		return Description{}, f.descErr
	}
	return f.desc, nil
}

func (f *fakeProvider) Resolve(ctx context.Context, ref resource.Reference) (resource.Document, error) {
	f.mu.Lock()
	f.resolves++
	f.inFlight++
	if f.inFlight > f.maxSeen {
		f.maxSeen = f.inFlight
	}
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.inFlight--
		f.mu.Unlock()
	}()

	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return resource.Document{}, ctx.Err()
		}
	}
	if f.err != nil {
		return resource.Document{}, f.err
	}
	identity := ref.Locator
	if f.identity != nil {
		identity = f.identity(ref.Locator)
	}
	return resource.Document{Identity: identity, Title: "doc " + identity, FreshFor: time.Minute}, nil
}

func (f *fakeProvider) counts() (resolves, describes int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.resolves, f.describes
}

func ref(instance, locator string) resource.Reference {
	return resource.Reference{Instance: instance, Matcher: "m", Locator: locator}
}

func TestManagerDescribeAllPublishesASnapshot(t *testing.T) {
	a := &fakeProvider{instance: "a", desc: Description{
		Info:     Info{Kind: "a"},
		Matchers: []Matcher{{ID: "m", Pattern: "A-[0-9]+"}},
	}}
	b := &fakeProvider{instance: "b", desc: Description{
		Info:     Info{Kind: "b"},
		Matchers: []Matcher{{ID: "m", Pattern: "B-[0-9]+"}},
	}}

	m := NewManager(ManagerOptions{})
	m.SetProviders([]Provider{a, b}, nil)

	statuses := m.DescribeAll(context.Background())
	if len(statuses) != 2 {
		t.Fatalf("statuses = %+v", statuses)
	}
	for _, st := range statuses {
		if st.State != StateReady || st.MatcherCount != 1 {
			t.Fatalf("status = %+v", st)
		}
	}
	if m.Snapshot().Len() != 2 {
		t.Fatalf("snapshot len = %d", m.Snapshot().Len())
	}
	// Configuration order is matcher precedence.
	if got := ids(m.Snapshot().Matchers()); got[0] != "a/m" || got[1] != "b/m" {
		t.Fatalf("order = %v", got)
	}
}

func TestManagerDescribeFailureStates(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want State
	}{
		{"protocol mismatch", &TransportError{Reason: ReasonProtocol}, StateIncompatible},
		{"invalid describe", &TransportError{Reason: ReasonInvalidDescribe}, StateIncompatible},
		{"missing command", &TransportError{Reason: ReasonSpawn}, StateIncompatible},
		{"timeout", &TransportError{Reason: ReasonTimeout}, StateTemporarilyFailed},
		{"crash", &TransportError{Reason: ReasonExit}, StateTemporarilyFailed},
		{"provider says invalid_config", &resource.Error{Code: resource.CodeInvalidConfig}, StateIncompatible},
		{"provider says unavailable", &resource.Error{Code: resource.CodeUnavailable}, StateTemporarilyFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &fakeProvider{instance: "p", descErr: tc.err}
			m := NewManager(ManagerOptions{})
			m.SetProviders([]Provider{p}, nil)
			st := m.DescribeAll(context.Background())[0]
			if st.State != tc.want {
				t.Fatalf("state = %q, want %q", st.State, tc.want)
			}
			if st.LastError == nil {
				t.Fatal("a failed describe must carry a typed error")
			}
		})
	}
}

func TestManagerKeepsMatchersWhenARedescribeFails(t *testing.T) {
	p := &fakeProvider{instance: "p", desc: Description{Matchers: []Matcher{{ID: "m", Pattern: "A-[0-9]+"}}}}
	m := NewManager(ManagerOptions{})
	m.SetProviders([]Provider{p}, nil)
	m.DescribeAll(context.Background())
	if m.Snapshot().Len() != 1 {
		t.Fatal("first describe did not publish")
	}

	// A later describe that returns an uncompilable pattern must not remove the
	// working matcher; it reports the failure instead.
	p.desc = Description{Matchers: []Matcher{{ID: "m", Pattern: "([a-z"}}}
	m.DescribeAll(context.Background())
	if m.Snapshot().Len() != 1 {
		t.Fatal("a failed replacement dropped the live matchers")
	}
	if m.SnapshotError() == nil {
		t.Fatal("the failed replacement was not reported")
	}
}

func TestManagerDisabledAndRemovedInstances(t *testing.T) {
	p := &fakeProvider{instance: "p", desc: Description{Matchers: []Matcher{{ID: "m", Pattern: "A-1"}}}}
	m := NewManager(ManagerOptions{})
	m.SetProviders([]Provider{p}, []string{"off"})

	statuses := m.Statuses()
	if len(statuses) != 2 {
		t.Fatalf("statuses = %+v", statuses)
	}
	if statuses[1].Instance != "off" || statuses[1].State != StateDisabled {
		t.Fatalf("disabled status = %+v", statuses[1])
	}
	if _, err := m.Resolve(context.Background(), ref("off", "X-1"), false); err == nil {
		t.Fatal("a disabled provider must not resolve")
	}

	// Dropping the instance from configuration marks it removed rather than
	// forgetting it: a saved reference still needs an explanation.
	m.SetProviders(nil, nil)
	st, ok := m.Status("p")
	if !ok || st.State != StateRemoved {
		t.Fatalf("removed status = %+v ok=%v", st, ok)
	}
}

func TestManagerCachesSuccessOnly(t *testing.T) {
	p := &fakeProvider{instance: "p"}
	m := NewManager(ManagerOptions{})
	m.SetProviders([]Provider{p}, nil)

	for i := 0; i < 3; i++ {
		if _, err := m.Resolve(context.Background(), ref("p", "A-1"), false); err != nil {
			t.Fatalf("Resolve: %v", err)
		}
	}
	if resolves, _ := p.counts(); resolves != 1 {
		t.Fatalf("cache miss: %d invocations", resolves)
	}

	// A failing locator is never cached.
	p.err = &resource.Error{Code: resource.CodeNotFound}
	for i := 0; i < 3; i++ {
		if _, err := m.Resolve(context.Background(), ref("p", "B-2"), false); err == nil {
			t.Fatal("expected the failure to surface")
		}
	}
	if resolves, _ := p.counts(); resolves != 4 {
		t.Fatalf("a failure was cached: %d invocations", resolves)
	}
}

func TestManagerRefreshBypassesFreshness(t *testing.T) {
	p := &fakeProvider{instance: "p"}
	m := NewManager(ManagerOptions{})
	m.SetProviders([]Provider{p}, nil)

	_, _ = m.Resolve(context.Background(), ref("p", "A-1"), false)
	_, _ = m.Resolve(context.Background(), ref("p", "A-1"), true)
	if resolves, _ := p.counts(); resolves != 2 {
		t.Fatalf("refresh did not bypass the cache: %d invocations", resolves)
	}
}

func TestManagerCacheExpires(t *testing.T) {
	now := time.Unix(0, 0)
	p := &fakeProvider{instance: "p"}
	m := NewManager(ManagerOptions{Now: func() time.Time { return now }})
	m.SetProviders([]Provider{p}, nil)

	_, _ = m.Resolve(context.Background(), ref("p", "A-1"), false)
	now = now.Add(30 * time.Second)
	_, _ = m.Resolve(context.Background(), ref("p", "A-1"), false)
	if resolves, _ := p.counts(); resolves != 1 {
		t.Fatalf("a fresh entry was re-resolved: %d", resolves)
	}
	now = now.Add(2 * time.Minute)
	_, _ = m.Resolve(context.Background(), ref("p", "A-1"), false)
	if resolves, _ := p.counts(); resolves != 2 {
		t.Fatalf("a stale entry was served: %d", resolves)
	}
}

// A provider that re-keys a locator onto a canonical identity must produce one
// cache entry, reachable by either name.
func TestManagerCacheIsKeyedByCanonicalIdentity(t *testing.T) {
	p := &fakeProvider{instance: "p", identity: func(string) string { return "CANON-1" }}
	m := NewManager(ManagerOptions{})
	m.SetProviders([]Provider{p}, nil)

	if _, err := m.Resolve(context.Background(), ref("p", "alias-1"), false); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if _, err := m.Resolve(context.Background(), ref("p", "CANON-1"), false); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolves, _ := p.counts(); resolves != 1 {
		t.Fatalf("the canonical identity was not a cache hit: %d invocations", resolves)
	}
}

// The cache is scoped to the describe generation: a new matcher set means a new
// key space, so nothing described under the old one can be served.
func TestManagerCacheIsScopedToTheDescribeGeneration(t *testing.T) {
	p := &fakeProvider{instance: "p", desc: Description{Matchers: []Matcher{{ID: "m", Pattern: "A-[0-9]+"}}}}
	m := NewManager(ManagerOptions{})
	m.SetProviders([]Provider{p}, nil)
	m.DescribeAll(context.Background())

	_, _ = m.Resolve(context.Background(), ref("p", "A-1"), false)
	m.DescribeAll(context.Background())
	_, _ = m.Resolve(context.Background(), ref("p", "A-1"), false)

	if resolves, _ := p.counts(); resolves != 2 {
		t.Fatalf("a cache entry crossed a describe generation: %d invocations", resolves)
	}
}

func TestManagerDeduplicatesInFlightResolves(t *testing.T) {
	p := &fakeProvider{instance: "p", delay: 150 * time.Millisecond}
	m := NewManager(ManagerOptions{})
	m.SetProviders([]Provider{p}, nil)

	var wg sync.WaitGroup
	var failures atomic.Int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			doc, err := m.Resolve(context.Background(), ref("p", "A-1"), false)
			if err != nil || doc.Identity != "A-1" {
				failures.Add(1)
			}
		}()
	}
	wg.Wait()
	if failures.Load() != 0 {
		t.Fatalf("%d shared callers failed", failures.Load())
	}
	if resolves, _ := p.counts(); resolves != 1 {
		t.Fatalf("identical in-flight resolves were not deduplicated: %d invocations", resolves)
	}
}

func TestManagerBoundsConcurrency(t *testing.T) {
	p := &fakeProvider{instance: "p", delay: 60 * time.Millisecond}
	m := NewManager(ManagerOptions{MaxConcurrent: 8, MaxConcurrentPerProvider: 2})
	m.SetProviders([]Provider{p}, nil)

	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = m.Resolve(context.Background(), ref("p", fmt.Sprintf("A-%d", i)), false)
		}(i)
	}
	wg.Wait()

	p.mu.Lock()
	peak := p.maxSeen
	p.mu.Unlock()
	if peak > 2 {
		t.Fatalf("per-provider concurrency cap exceeded: peak %d", peak)
	}
}

func TestManagerQueuedWorkStaysCancellable(t *testing.T) {
	p := &fakeProvider{instance: "p", delay: 2 * time.Second}
	m := NewManager(ManagerOptions{MaxConcurrent: 1, MaxConcurrentPerProvider: 1})
	m.SetProviders([]Provider{p}, nil)

	blocking := make(chan struct{})
	go func() {
		defer close(blocking)
		_, _ = m.Resolve(context.Background(), ref("p", "A-1"), false)
	}()
	time.Sleep(100 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := m.Resolve(ctx, ref("p", "A-2"), false)
	if time.Since(started) > time.Second {
		t.Fatal("a queued request did not honor cancellation")
	}
	var terr *TransportError
	if !errors.As(err, &terr) || terr.Reason != ReasonCanceled {
		t.Fatalf("err = %v", err)
	}
	// The canceled request never reached the provider.
	p.mu.Lock()
	resolves := p.resolves
	p.mu.Unlock()
	if resolves != 1 {
		t.Fatalf("a canceled queued request still ran: %d invocations", resolves)
	}
	<-blocking
}

func TestManagerRejectsUnknownAndInvalidReferences(t *testing.T) {
	m := NewManager(ManagerOptions{})
	m.SetProviders([]Provider{&fakeProvider{instance: "p"}}, nil)

	if _, err := m.Resolve(context.Background(), ref("nope", "A-1"), false); err == nil {
		t.Fatal("an unconfigured instance must not resolve")
	}
	if _, err := m.Resolve(context.Background(), resource.Reference{Instance: "p"}, false); err == nil {
		t.Fatal("an empty reference must not resolve")
	}
}
