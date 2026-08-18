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

// The normative describe-failure authority table, one row at a time.
//
// The distinction is who has authority over the answer, not whether the call
// went well: a typed error is the provider saying it has no matchers, while a
// transport failure means the host simply has no new answer and must not invent
// an empty one on the provider's behalf.
func TestManagerDescribeAuthorityTable(t *testing.T) {
	live := Description{Matchers: []Matcher{{ID: "m", Pattern: "A-[0-9]+"}}}

	cases := []struct {
		name      string
		second    Description
		secondErr error
		wantLive  int
		wantState State
	}{
		{
			name:      "success replaces",
			second:    Description{Matchers: []Matcher{{ID: "m", Pattern: "B-[0-9]+"}, {ID: "n", Pattern: "C-[0-9]+"}}},
			wantLive:  2,
			wantState: StateReady,
		},
		{
			name:      "typed error drops",
			secondErr: &resource.Error{Code: resource.CodeInvalidConfig, Message: "not set up"},
			wantLive:  0,
			wantState: StateIncompatible,
		},
		{
			name:      "typed unavailable also drops",
			secondErr: &resource.Error{Code: resource.CodeUnavailable},
			wantLive:  0,
			wantState: StateTemporarilyFailed,
		},
		{
			name:      "timeout keeps",
			secondErr: &TransportError{Reason: ReasonTimeout},
			wantLive:  1,
			wantState: StateTemporarilyFailed,
		},
		{
			name:      "crash keeps",
			secondErr: &TransportError{Reason: ReasonExit},
			wantLive:  1,
			wantState: StateTemporarilyFailed,
		},
		{
			name:      "failed validation keeps",
			secondErr: &TransportError{Reason: ReasonInvalidDescribe},
			wantLive:  1,
			wantState: StateIncompatible,
		},
		{
			name:      "protocol mismatch keeps",
			secondErr: &TransportError{Reason: ReasonProtocol},
			wantLive:  1,
			wantState: StateIncompatible,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &fakeProvider{instance: "p", desc: live}
			m := NewManager(ManagerOptions{})
			m.SetProviders([]Provider{p}, nil)
			m.DescribeAll(context.Background())
			if m.Snapshot().Len() != 1 {
				t.Fatal("the first describe did not publish")
			}

			p.desc, p.descErr = tc.second, tc.secondErr
			statuses := m.DescribeAll(context.Background())

			if got := m.Snapshot().Len(); got != tc.wantLive {
				t.Fatalf("live matchers = %d, want %d", got, tc.wantLive)
			}
			if statuses[0].State != tc.wantState {
				t.Fatalf("state = %q, want %q", statuses[0].State, tc.wantState)
			}
			if statuses[0].MatcherCount != tc.wantLive {
				t.Fatalf("reported matcher count = %d, want %d", statuses[0].MatcherCount, tc.wantLive)
			}
		})
	}
}

// One provider failing non-authoritatively must not disturb another's matchers,
// which is why the retained set is kept per instance rather than read back out
// of the snapshot.
func TestManagerOneProviderFailingDoesNotDisturbAnother(t *testing.T) {
	a := &fakeProvider{instance: "a", desc: Description{Matchers: []Matcher{{ID: "m", Pattern: "A-[0-9]+"}}}}
	b := &fakeProvider{instance: "b", desc: Description{Matchers: []Matcher{{ID: "m", Pattern: "B-[0-9]+"}}}}
	m := NewManager(ManagerOptions{})
	m.SetProviders([]Provider{a, b}, nil)
	m.DescribeAll(context.Background())
	if m.Snapshot().Len() != 2 {
		t.Fatal("both providers should be live")
	}

	a.descErr = &TransportError{Reason: ReasonTimeout}
	b.desc = Description{Matchers: []Matcher{{ID: "m", Pattern: "B2-[0-9]+"}, {ID: "n", Pattern: "B3-[0-9]+"}}}
	m.DescribeAll(context.Background())

	got := ids(m.Snapshot().Matchers())
	// a's matcher is retained; b's set is replaced. Configured order holds.
	want := []string{"a/m", "b/m", "b/n"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("live matchers = %v, want %v", got, want)
	}
	am, ok := m.Snapshot().Lookup("a", "m")
	if !ok || am.Pattern != "A-[0-9]+" {
		t.Fatalf("a's retained matcher changed: %+v", am)
	}
}

// A retained set recovers as soon as the provider answers again.
func TestManagerRetainedMatchersRecoverOnTheNextSuccess(t *testing.T) {
	p := &fakeProvider{instance: "p", desc: Description{Matchers: []Matcher{{ID: "m", Pattern: "A-[0-9]+"}}}}
	m := NewManager(ManagerOptions{})
	m.SetProviders([]Provider{p}, nil)
	m.DescribeAll(context.Background())

	p.descErr = &TransportError{Reason: ReasonTimeout}
	m.DescribeAll(context.Background())
	if m.Snapshot().Len() != 1 {
		t.Fatal("the retained set was dropped")
	}

	p.descErr = nil
	p.desc = Description{Matchers: []Matcher{{ID: "m", Pattern: "A-[0-9]+"}, {ID: "n", Pattern: "Z-[0-9]+"}}}
	statuses := m.DescribeAll(context.Background())
	if m.Snapshot().Len() != 2 || statuses[0].State != StateReady {
		t.Fatalf("recovery failed: %d matchers, state %q", m.Snapshot().Len(), statuses[0].State)
	}
}

// Disabling is authoritative, and re-enabling must not resurrect the old set
// before a fresh describe lands.
func TestManagerDisablingDropsMatchersAndDoesNotResurrectThem(t *testing.T) {
	p := &fakeProvider{instance: "p", desc: Description{Matchers: []Matcher{{ID: "m", Pattern: "A-[0-9]+"}}}}
	m := NewManager(ManagerOptions{})
	m.SetProviders([]Provider{p}, nil)
	m.DescribeAll(context.Background())

	m.SetProviders(nil, []string{"p"})
	m.DescribeAll(context.Background())
	if m.Snapshot().Len() != 0 {
		t.Fatalf("a disabled provider still contributes %d matchers", m.Snapshot().Len())
	}
	st, _ := m.Status("p")
	if st.State != StateDisabled || st.MatcherCount != 0 {
		t.Fatalf("status = %+v", st)
	}

	// Re-enable but make describe fail non-authoritatively. There is no
	// retained set to fall back on, so nothing comes back.
	p.descErr = &TransportError{Reason: ReasonTimeout}
	m.SetProviders([]Provider{p}, nil)
	m.DescribeAll(context.Background())
	if m.Snapshot().Len() != 0 {
		t.Fatal("re-enabling resurrected a stale matcher set")
	}
}

// Removing a config entry is authoritative too, and the instance keeps a status
// so a diagnostic surface can still explain an armed reference.
func TestManagerRemovalDropsMatchersButKeepsStatus(t *testing.T) {
	p := &fakeProvider{instance: "p", desc: Description{Matchers: []Matcher{{ID: "m", Pattern: "A-[0-9]+"}}}}
	m := NewManager(ManagerOptions{})
	m.SetProviders([]Provider{p}, nil)
	m.DescribeAll(context.Background())

	m.SetProviders(nil, nil)
	m.DescribeAll(context.Background())
	if m.Snapshot().Len() != 0 {
		t.Fatal("a removed provider still contributes matchers")
	}
	st, ok := m.Status("p")
	if !ok || st.State != StateRemoved {
		t.Fatalf("status = %+v ok=%v", st, ok)
	}
}
