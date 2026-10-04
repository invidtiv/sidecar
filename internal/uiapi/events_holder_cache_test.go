package uiapi

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

type countedHolderBackend struct {
	*fakeBackend
	mu     sync.Mutex
	calls  int
	holder *GeometryHolder
}

func (b *countedHolderBackend) GeometryHolder(context.Context, TerminalInfo) *GeometryHolder {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls++
	return b.holder
}

func TestEventTerminalsSharesLegacyHolderAcrossClients(t *testing.T) {
	b := &countedHolderBackend{fakeBackend: newFakeBackend(), holder: &GeometryHolder{Kind: "tui", Label: "TUI on aerie"}}
	s := &Server{opts: Options{Backend: b, Now: time.Now}, clients: newClientRegistry(time.Now)}
	for i := range 12 {
		who := caller{listener: ListenerLocal, client: fmt.Sprintf("credential-%d", i)}
		client, ok := s.clients.add("terminal", who)
		if !ok {
			t.Fatal("add")
		}
		client.observe([]byte(`{"type":"opened","target":{"owner_host_id":"local:aerie","session":"shared","pane":"%1"}}`))
		terms := s.eventTerminals(context.Background(), who)
		if len(terms) != 1 || terms[0].Holder == nil || *terms[0].Holder != *b.holder {
			t.Fatalf("client %d holder: %+v", i, terms)
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.calls != 1 {
		t.Fatalf("12 client projections read the same session holder %d times; want 1", b.calls)
	}
}

func TestEventsWebsocketsShareLegacyHolderObservation(t *testing.T) {
	b := &countedHolderBackend{fakeBackend: newFakeBackend(), holder: &GeometryHolder{Kind: "tui", Label: "TUI on aerie"}}
	now := time.Now()
	h := newHarness(t, func(o *Options) { o.Backend = b; o.Now = func() time.Time { return now } })
	who := caller{listener: ListenerLocal, auth: "local", client: "local"}
	terminal, _ := h.s.clients.add("terminal", who)
	defer h.s.clients.remove(terminal)
	terminal.observe([]byte(`{"type":"opened","target":{"session":"shared","pane":"%1"}}`))
	for range 6 {
		conn := dialEvents(t, h, "", nil, true)
		for _, kind := range []string{"hello", "catalog", "terminals"} {
			event := readEvent(t, conn)
			if event.Type != kind {
				t.Fatalf("event=%+v want %s", event, kind)
			}
			if kind == "terminals" && (event.Terminals == nil || len(*event.Terminals) != 1 || (*event.Terminals)[0].Holder == nil) {
				t.Fatalf("legacy terminal missing: %+v", event)
			}
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.calls != 1 {
		t.Fatalf("6 events sockets made %d holder observations; want 1", b.calls)
	}
}

func TestLegacyHolderCacheExpiryNullHostIsolationAndCopy(t *testing.T) {
	b := &countedHolderBackend{fakeBackend: newFakeBackend(), holder: &GeometryHolder{Kind: "tui", Label: "original"}}
	var cache legacyHolderCache
	now := time.Now()
	clock := func() time.Time { return now }
	get := func(host string) *GeometryHolder {
		return cache.get(context.Background(), context.Background(), clock, b, TerminalInfo{OwnerHostID: host, Session: "same"})
	}
	first := get("one")
	first.Label = "client mutation"
	b.mu.Lock()
	b.holder.Label = "adapter mutation"
	b.mu.Unlock()
	if got := get("one"); got == nil || got.Label != "original" {
		t.Fatalf("cached observation mutated: %v", got)
	}
	if got := get("two"); got == nil || got.Label != "adapter mutation" {
		t.Fatalf("host observations were shared: %v", got)
	}
	b.mu.Lock()
	b.holder = nil
	b.mu.Unlock()
	now = now.Add(legacyHolderTTL)
	if got := get("one"); got != nil {
		t.Fatalf("expired holder=%v want null", got)
	}
	if got := get("one"); got != nil {
		t.Fatalf("cached null=%v", got)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.calls != 3 {
		t.Fatalf("null observation wasn't cached: %d reads want 3", b.calls)
	}
}

type blockedHolderBackend struct {
	*fakeBackend
	started chan context.Context
	release chan struct{}
	mu      sync.Mutex
	calls   int
}

func (b *blockedHolderBackend) GeometryHolder(ctx context.Context, _ TerminalInfo) *GeometryHolder {
	b.mu.Lock()
	b.calls++
	b.mu.Unlock()
	b.started <- ctx
	select {
	case <-b.release:
		return &GeometryHolder{Kind: "tui", Label: "shared"}
	case <-ctx.Done():
		return nil
	}
}

func TestLegacyHolderCacheSharesInFlightAndSurvivesCallerCancellation(t *testing.T) {
	b := &blockedHolderBackend{fakeBackend: newFakeBackend(), started: make(chan context.Context, 20), release: make(chan struct{})}
	t.Cleanup(func() { close(b.release) })
	var cache legacyHolderCache
	ctx, cancel := holderCacheTestContext(t)
	defer cancel()
	firstCtx, firstCancel := context.WithCancel(ctx)
	first := make(chan *GeometryHolder, 1)
	term := TerminalInfo{Session: "one"}
	go func() { first <- cache.get(firstCtx, ctx, time.Now, b, term) }()
	var sourceCtx context.Context
	select {
	case sourceCtx = <-b.started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	firstCancel()
	select {
	case got := <-first:
		if got != nil {
			t.Fatalf("cancelled caller=%v", got)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case <-sourceCtx.Done():
		t.Fatal("disconnect cancelled shared observation")
	default:
	}
	results := make(chan *GeometryHolder, 12)
	for range 12 {
		go func() { results <- cache.get(ctx, ctx, time.Now, b, term) }()
	}
	// Release the provider only after its first request is in flight. Callers
	// arriving later must consume the same completed cache observation.
	b.release <- struct{}{}
	for range 12 {
		select {
		case got := <-results:
			if got == nil || got.Label != "shared" {
				t.Fatalf("shared result=%v", got)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.calls != 1 {
		t.Fatalf("shared in-flight read invoked adapter %d times", b.calls)
	}
}

func TestLegacyHolderCacheBoundsConcurrentReads(t *testing.T) {
	b := &blockedHolderBackend{fakeBackend: newFakeBackend(), started: make(chan context.Context, 20), release: make(chan struct{})}
	t.Cleanup(func() { close(b.release) })
	var cache legacyHolderCache
	ctx, cancel := holderCacheTestContext(t)
	defer cancel()
	results := make(chan *GeometryHolder, maxLegacyHolderReads)
	for i := range maxLegacyHolderReads {
		go func() { results <- cache.get(ctx, ctx, time.Now, b, TerminalInfo{Session: fmt.Sprint(i)}) }()
		select {
		case <-b.started:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if got := cache.get(ctx, ctx, time.Now, b, TerminalInfo{Session: "overflow"}); got != nil {
		t.Fatalf("saturated first observation=%v want null", got)
	}
	b.mu.Lock()
	calls := b.calls
	b.mu.Unlock()
	if calls != maxLegacyHolderReads {
		t.Fatalf("read bound bypassed: %d", calls)
	}
	for range maxLegacyHolderReads {
		b.release <- struct{}{}
	}
	for range maxLegacyHolderReads {
		select {
		case <-results:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	go func() { results <- cache.get(ctx, ctx, time.Now, b, TerminalInfo{Session: "overflow"}) }()
	select {
	case <-b.started:
	case <-ctx.Done():
		t.Fatal("capacity did not recover")
	}
	b.release <- struct{}{}
	select {
	case got := <-results:
		if got == nil {
			t.Fatal("overflow did not recover")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestLegacyHolderCacheBoundsEntriesWithoutBypassingFreshCache(t *testing.T) {
	b := &countedHolderBackend{fakeBackend: newFakeBackend(), holder: &GeometryHolder{Kind: "tui", Label: "shared"}}
	var cache legacyHolderCache
	now := time.Now()
	clock := func() time.Time { return now }
	get := func(session string) *GeometryHolder {
		return cache.get(context.Background(), context.Background(), clock, b, TerminalInfo{Session: session})
	}
	for i := range maxLegacyHolderEntries {
		if get(fmt.Sprint(i)) == nil {
			t.Fatalf("entry %d unexpectedly saturated", i)
		}
	}
	if got := get("overflow"); got != nil {
		t.Fatalf("fresh cache cap bypassed: %v", got)
	}
	if get("0") == nil {
		t.Fatal("fresh entry evicted")
	}
	b.mu.Lock()
	calls := b.calls
	b.mu.Unlock()
	if calls != maxLegacyHolderEntries {
		t.Fatalf("entry cap bypassed: %d reads", calls)
	}
	now = now.Add(legacyHolderTTL)
	if get("overflow") == nil {
		t.Fatal("expired entry capacity did not recover")
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if len(cache.entries) != maxLegacyHolderEntries {
		t.Fatalf("unbounded cache: %d", len(cache.entries))
	}
}

func TestLegacyHolderCacheShutdownCancelsSharedObservation(t *testing.T) {
	b := &blockedHolderBackend{fakeBackend: newFakeBackend(), started: make(chan context.Context, 1), release: make(chan struct{})}
	var cache legacyHolderCache
	lifetime, stop := context.WithCancel(context.Background())
	defer stop()
	ctx, cancel := holderCacheTestContext(t)
	defer cancel()
	result := make(chan *GeometryHolder, 1)
	go func() { result <- cache.get(ctx, lifetime, time.Now, b, TerminalInfo{Session: "one"}) }()
	var sourceCtx context.Context
	select {
	case sourceCtx = <-b.started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	stop()
	select {
	case <-sourceCtx.Done():
	case <-ctx.Done():
		t.Fatal("shutdown did not cancel shared adapter read")
	}
	select {
	case got := <-result:
		if got != nil {
			t.Fatalf("cancelled adapter read=%v", got)
		}
	case <-ctx.Done():
		t.Fatal("shutdown did not release events waiter")
	}
}

// Bound dependency failures by the enclosing test budget, without imposing a
// scheduling performance target on concurrent cache assertions.
func holderCacheTestContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	deadline, ok := t.Deadline()
	if !ok {
		return context.WithCancel(t.Context())
	}
	return context.WithDeadline(t.Context(), deadline.Add(-2*time.Second))
}
