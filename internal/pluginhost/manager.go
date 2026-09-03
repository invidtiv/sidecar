package pluginhost

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/marcus/sidecar/internal/resource"
)

// Default concurrency caps. They are small on purpose: a provider invocation is
// a process spawn, and on a machine with an endpoint security agent every spawn
// carries a large fixed tax. Excess work queues and stays cancellable.
const (
	DefaultMaxConcurrent            = 4
	DefaultMaxConcurrentPerProvider = 2
)

// Status is one instance's diagnostic state. It carries no locator, no
// document, and no provider output.
type Status struct {
	Instance     string
	State        State
	Info         Info
	MatcherCount int
	LastChecked  time.Time
	// LastError is the typed error of the last failed describe, or nil.
	LastError *resource.Error
	// LastOutcome is the single-token outcome of the last describe.
	LastOutcome string
	Duration    time.Duration
}

// ManagerOptions configure a Manager. The zero value is usable.
type ManagerOptions struct {
	MaxConcurrent            int
	MaxConcurrentPerProvider int
	Log                      *slog.Logger
	// Now is injectable for cache-expiry tests.
	Now func() time.Time
}

// Manager is the host-owned, long-lived owner of provider process policy: it
// describes instances, publishes the matcher snapshot, caches successful
// resolves, deduplicates identical in-flight resolves, and bounds concurrency.
//
// It is not a Sidecar plugin. It has no tab, no View, and no independent
// lifecycle; the app injects its read-only snapshot and its Resolve method into
// whichever surface needs them, so the project Workspace and the global
// Workspaces browser share one status, cache, and process policy.
type Manager struct {
	mu        sync.Mutex
	providers []Provider
	disabled  map[string]bool
	statuses  map[string]*Status
	order     []string
	// lastGood is the newest matcher set each instance authoritatively
	// declared. It is what a non-authoritative describe failure falls back to,
	// which is why it is kept per instance rather than read back out of the
	// snapshot: one provider timing out must not disturb another's.
	lastGood map[string][]Matcher

	snapshots *SnapshotStore

	// cache holds only successful, sanitized documents, keyed by instance and
	// canonical identity and scoped to the describe generation. There is no
	// durable body cache in v1.
	cache map[cacheKey]cacheEntry
	// alias maps the locator that was asked for onto the identity the provider
	// answered with, so a second click on the same key is a cache hit even when
	// the provider re-keyed it.
	alias map[cacheKey]string
	// inflight deduplicates identical resolves.
	inflight map[cacheKey]*resolveCall

	globalSem chan struct{}
	perSem    map[string]chan struct{}
	perLimit  int

	log *slog.Logger
	now func() time.Time
}

type cacheKey struct {
	generation uint64
	instance   string
	key        string
}

type cacheEntry struct {
	doc     resource.Document
	expires time.Time
}

type resolveCall struct {
	done chan struct{}
	doc  resource.Document
	err  error
}

// NewManager builds a manager with no providers.
func NewManager(opts ManagerOptions) *Manager {
	maxConcurrent := opts.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = DefaultMaxConcurrent
	}
	perProvider := opts.MaxConcurrentPerProvider
	if perProvider <= 0 {
		perProvider = DefaultMaxConcurrentPerProvider
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Manager{
		disabled:  make(map[string]bool),
		statuses:  make(map[string]*Status),
		lastGood:  make(map[string][]Matcher),
		snapshots: NewSnapshotStore(),
		cache:     make(map[cacheKey]cacheEntry),
		alias:     make(map[cacheKey]string),
		inflight:  make(map[cacheKey]*resolveCall),
		globalSem: make(chan struct{}, maxConcurrent),
		perSem:    make(map[string]chan struct{}),
		perLimit:  perProvider,
		log:       opts.Log,
		now:       now,
	}
}

// SetProviders replaces the configured set, in configuration order — which is
// matcher precedence. disabled names instances that are configured but turned
// off; they keep a status and contribute no matchers.
//
// An instance that disappears becomes StateRemoved rather than vanishing, so a
// diagnostic surface can still explain why a saved reference is not resolving.
func (m *Manager) SetProviders(providers []Provider, disabled []string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.providers = append([]Provider(nil), providers...)
	m.disabled = make(map[string]bool, len(disabled))
	for _, id := range disabled {
		m.disabled[id] = true
	}

	present := make(map[string]bool, len(providers)+len(disabled))
	m.order = m.order[:0]
	for _, p := range providers {
		id := p.Instance()
		present[id] = true
		m.order = append(m.order, id)
		if _, ok := m.statuses[id]; !ok {
			m.statuses[id] = &Status{Instance: id, State: StateUnchecked}
		}
	}
	for _, id := range disabled {
		present[id] = true
		if _, ok := m.statuses[id]; !ok {
			m.statuses[id] = &Status{Instance: id}
		}
		m.statuses[id].State = StateDisabled
		m.statuses[id].MatcherCount = 0
		// Disabling is authoritative: the instance has no matchers until it is
		// re-enabled and describes itself again. Keeping the old set around
		// would let re-enabling resurrect a stale one before the fresh describe
		// lands.
		delete(m.lastGood, id)
		m.order = append(m.order, id)
	}
	for id, st := range m.statuses {
		if !present[id] {
			st.State = StateRemoved
			st.MatcherCount = 0
			delete(m.lastGood, id)
			m.order = append(m.order, id)
		}
	}
}

// Snapshot returns the live matcher snapshot. It never returns nil.
func (m *Manager) Snapshot() *Snapshot { return m.snapshots.Current() }

// SnapshotError reports the most recent failed snapshot replacement.
func (m *Manager) SnapshotError() error { return m.snapshots.LastError() }

// DescribeAll describes every enabled instance concurrently, publishes a new
// matcher snapshot from the results, and returns each instance's status.
//
// It never runs on the first-frame path. The app starts it from a cancellable
// command that waits on the ready-frame latch.
func (m *Manager) DescribeAll(ctx context.Context) []Status {
	m.mu.Lock()
	providers := append([]Provider(nil), m.providers...)
	m.mu.Unlock()

	type described struct {
		instance string
		index    int
		order    int
		desc     Description
		err      error
		took     time.Duration
	}
	results := make([]described, len(providers))
	// Claimed hosts are host-side instance configuration, not describe output:
	// they are read once here and travel with whatever matchers the instance
	// contributes, including the kept set of a non-authoritative failure.
	claims := make([][]string, len(providers))
	for i, p := range providers {
		if source, ok := p.(claimHostsProvider); ok {
			claims[i] = source.ClaimHosts()
		}
	}

	var wg sync.WaitGroup
	for i, p := range providers {
		wg.Add(1)
		go func(i int, p Provider) {
			defer wg.Done()
			started := m.now()
			if err := m.acquire(ctx, p.Instance()); err != nil {
				results[i] = described{instance: p.Instance(), index: i, order: i, err: err, took: m.now().Sub(started)}
				return
			}
			defer m.release(p.Instance())
			desc, err := p.Describe(ctx)
			results[i] = described{instance: p.Instance(), index: i, order: i, desc: desc, err: err, took: m.now().Sub(started)}
		}(i, p)
	}
	wg.Wait()

	// What happens to a provider's live matchers depends on who has authority
	// over the answer, not on whether the call went well:
	//
	//   success                      -> replace them
	//   typed error response         -> drop them; the provider said it has none
	//   transport or validation fail -> keep them; the host has no new answer
	//
	// The third row is the one worth stating out loud. Dropping a working
	// matcher set because a describe timed out would make terminal output stop
	// recognizing links for a reason the user never sees, so a non-authoritative
	// failure changes the reported state and nothing else.
	var sets []DescribedSet
	m.mu.Lock()
	for _, r := range results {
		st, ok := m.statuses[r.instance]
		if !ok {
			st = &Status{Instance: r.instance}
			m.statuses[r.instance] = st
		}
		st.LastChecked = m.now()
		st.Duration = r.took
		st.LastOutcome = OutcomeCode(r.err)

		if r.err != nil {
			st.State = stateForDescribeError(r.err)
			st.LastError = AsResourceError(r.err)

			if authoritativeDescribeFailure(r.err) {
				delete(m.lastGood, r.instance)
				st.MatcherCount = 0
				continue
			}
			kept := m.lastGood[r.instance]
			st.MatcherCount = len(kept)
			if len(kept) > 0 {
				sets = append(sets, DescribedSet{Instance: r.instance, Order: r.order, Matchers: kept, ClaimHosts: claims[r.index]})
			}
			continue
		}

		st.State = StateReady
		st.LastError = nil
		st.Info = r.desc.Info
		st.MatcherCount = len(r.desc.Matchers)
		m.lastGood[r.instance] = r.desc.Matchers
		sets = append(sets, DescribedSet{Instance: r.instance, Order: r.order, Matchers: r.desc.Matchers, ClaimHosts: claims[r.index]})
	}
	m.mu.Unlock()

	// A refused replacement keeps the previous snapshot whole; the error is
	// reported through SnapshotError rather than by discarding working matchers.
	// Everything reaching here has already passed per-provider validation, so
	// this is a belt-and-braces guard rather than the usual path.
	_ = m.snapshots.Replace(sets)

	if m.log != nil {
		for _, r := range results {
			m.log.Debug("terminal resource provider describe",
				"instance", r.instance,
				"method", MethodDescribe,
				"duration_ms", r.took.Milliseconds(),
				"outcome", OutcomeCode(r.err),
				"matchers", len(r.desc.Matchers),
			)
		}
	}

	return m.Statuses()
}

// Statuses returns every known instance's diagnostic state, in configuration
// order followed by removed instances.
func (m *Manager) Statuses() []Status {
	m.mu.Lock()
	defer m.mu.Unlock()

	seen := make(map[string]bool, len(m.statuses))
	out := make([]Status, 0, len(m.statuses))
	for _, id := range m.order {
		if seen[id] {
			continue
		}
		seen[id] = true
		if st, ok := m.statuses[id]; ok {
			out = append(out, *st)
		}
	}
	rest := make([]string, 0)
	for id := range m.statuses {
		if !seen[id] {
			rest = append(rest, id)
		}
	}
	sort.Strings(rest)
	for _, id := range rest {
		out = append(out, *m.statuses[id])
	}
	return out
}

// Status returns one instance's diagnostic state.
func (m *Manager) Status(instance string) (Status, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.statuses[instance]
	if !ok {
		return Status{}, false
	}
	return *st, true
}

// Resolve turns a reference into a document. Successful results are cached and
// identical in-flight requests share one invocation.
//
// refresh bypasses freshness for this call and re-caches the result. It does
// not drop the previous entry up front: a failed refresh must leave the last
// good document available to whoever still holds it.
func (m *Manager) Resolve(ctx context.Context, ref resource.Reference, refresh bool) (resource.Document, error) {
	provider, err := m.providerFor(ref.Instance)
	if err != nil {
		return resource.Document{}, err
	}
	if !ref.Valid() {
		return resource.Document{}, &TransportError{
			Instance: ref.Instance,
			Method:   MethodResolve,
			Reason:   ReasonInvalidRequest,
			Detail:   "reference is empty or exceeds its bounds",
		}
	}

	gen := m.snapshots.Current().Generation()
	lookup := cacheKey{generation: gen, instance: ref.Instance, key: ref.Locator}

	if !refresh {
		if doc, ok := m.cached(lookup); ok {
			return doc, nil
		}
	}

	call, leader := m.joinInflight(lookup)
	if !leader {
		select {
		case <-call.done:
			return call.doc, call.err
		case <-ctx.Done():
			// Withdrawing from a shared call never cancels it for the caller
			// that started it.
			return resource.Document{}, &TransportError{Instance: ref.Instance, Method: MethodResolve, Reason: ReasonCanceled, Detail: "the request was withdrawn"}
		}
	}

	doc, resolveErr := m.runResolve(ctx, provider, ref)
	if resolveErr == nil {
		m.store(gen, ref, doc)
	}

	m.mu.Lock()
	delete(m.inflight, lookup)
	m.mu.Unlock()
	call.doc, call.err = doc, resolveErr
	close(call.done)

	return doc, resolveErr
}

func (m *Manager) runResolve(ctx context.Context, provider Provider, ref resource.Reference) (resource.Document, error) {
	if err := m.acquire(ctx, ref.Instance); err != nil {
		return resource.Document{}, err
	}
	defer m.release(ref.Instance)
	return provider.Resolve(ctx, ref)
}

func (m *Manager) providerFor(instance string) (Provider, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.disabled[instance] {
		return nil, &resource.Error{Code: resource.CodeInvalidConfig, Message: "This provider is disabled in configuration."}
	}
	for _, p := range m.providers {
		if p.Instance() == instance {
			return p, nil
		}
	}
	return nil, &resource.Error{Code: resource.CodeInvalidConfig, Message: "No provider instance with that id is configured."}
}

func (m *Manager) cached(key cacheKey) (resource.Document, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	target := key
	if identity, ok := m.alias[key]; ok {
		target = cacheKey{generation: key.generation, instance: key.instance, key: identity}
	}
	entry, ok := m.cache[target]
	if !ok || !entry.expires.After(m.now()) {
		return resource.Document{}, false
	}
	return entry.doc, true
}

func (m *Manager) store(gen uint64, ref resource.Reference, doc resource.Document) {
	m.mu.Lock()
	defer m.mu.Unlock()
	identityKey := cacheKey{generation: gen, instance: ref.Instance, key: doc.Identity}
	m.cache[identityKey] = cacheEntry{doc: doc, expires: m.now().Add(doc.FreshFor)}
	m.alias[cacheKey{generation: gen, instance: ref.Instance, key: ref.Locator}] = doc.Identity
}

func (m *Manager) joinInflight(key cacheKey) (*resolveCall, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if call, ok := m.inflight[key]; ok {
		return call, false
	}
	call := &resolveCall{done: make(chan struct{})}
	m.inflight[key] = call
	return call, true
}

// acquire takes a global slot and a per-provider slot. Queued work stays
// cancellable: a caller that gives up while waiting never starts a process.
func (m *Manager) acquire(ctx context.Context, instance string) error {
	canceled := func() error {
		return &TransportError{Instance: instance, Method: MethodResolve, Reason: ReasonCanceled, Detail: "canceled while queued"}
	}
	select {
	case m.globalSem <- struct{}{}:
	case <-ctx.Done():
		return canceled()
	}
	sem := m.providerSem(instance)
	select {
	case sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		<-m.globalSem
		return canceled()
	}
}

func (m *Manager) release(instance string) {
	<-m.providerSem(instance)
	<-m.globalSem
}

func (m *Manager) providerSem(instance string) chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	sem, ok := m.perSem[instance]
	if !ok {
		sem = make(chan struct{}, m.perLimit)
		m.perSem[instance] = sem
	}
	return sem
}
