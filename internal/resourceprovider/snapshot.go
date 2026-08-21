package resourceprovider

import (
	"fmt"
	"regexp"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/resource"
	"github.com/marcus/sidecar/internal/terminallink"
)

// CompiledMatcher is one live external matcher. It is immutable once built and
// carries its compiled RE2 so the scanner never compiles anything on a frame.
type CompiledMatcher struct {
	// Instance is the configured provider instance ID.
	Instance string
	// ID is the provider-stable matcher ID stored in resource references.
	ID string
	// Pattern is the RE2 source, kept for diagnostics.
	Pattern string
	// Priority orders matchers within one provider; higher runs earlier.
	Priority int
	// Order is the instance's position in configuration, which is the outer
	// precedence key.
	Order int

	re *regexp.Regexp
}                                                // Regexp returns the compiled expression. The whole match is the locator.
func (m CompiledMatcher) Regexp() *regexp.Regexp { return m.re }

// Snapshot is an immutable, ordered set of compiled matchers. The scanner holds
// one for the duration of a scan; a replacement never mutates one in place.
//
// It also carries each instance's claimed hosts, keyed by instance ID. Claims
// ride on the same snapshot as the matchers so that a claimed URL can only be
// reclassified by a matcher that is live in exactly this generation — and so
// that a refused replacement keeps claims and matchers consistent with each
// other.
type Snapshot struct {
	generation uint64
	matchers   []CompiledMatcher
	claims     map[string][]string
}

// Generation identifies this snapshot. Cache entries and in-flight resolves are
// scoped to it, so a re-describe cannot serve a document keyed by a matcher set
// that no longer exists.
func (s *Snapshot) Generation() uint64 {
	if s == nil {
		return 0
	}
	return s.generation
}

// Matchers returns the ordered matchers. The slice is a copy; the compiled
// expressions inside it are shared and safe for concurrent use.
func (s *Snapshot) Matchers() []CompiledMatcher {
	if s == nil {
		return nil
	}
	return append([]CompiledMatcher(nil), s.matchers...)
}

// Len reports how many matchers are live.
func (s *Snapshot) Len() int {
	if s == nil {
		return 0
	}
	return len(s.matchers)
}

// TerminalMatchers adapts the snapshot into the scanner's vocabulary, already
// in precedence order. It is the only crossing between provider machinery and
// the scanner: terminallink learns a pattern and a reference, never a process,
// a document, or this package.
//
// Each matcher carries its instance's claimed hosts. That is how the URL-yield
// rule reaches the scanner without widening this crossing: the scanner sees a
// host list on the same value that already named the pattern and the reference,
// and it can only reclassify a built-in URL span when one of those hosts
// matches AND this matcher matches the entire URL.
func (s *Snapshot) TerminalMatchers() []terminallink.ResourceMatcher {
	if s == nil || len(s.matchers) == 0 {
		return nil
	}
	out := make([]terminallink.ResourceMatcher, 0, len(s.matchers))
	for _, m := range s.matchers {
		out = append(out, terminallink.ResourceMatcher{
			Provider:   m.Instance,
			ID:         m.ID,
			Re:         m.re,
			ClaimHosts: s.claimHostsFor(m.Instance),
		})
	}
	return out
}

// ClaimHosts reports an instance's claimed hostnames in their normalized,
// lowercase form. Unknown instances return nil.
func (s *Snapshot) ClaimHosts(instance string) []string {
	if s == nil {
		return nil
	}
	return s.claimHostsFor(instance)
}

func (s *Snapshot) claimHostsFor(instance string) []string {
	if len(s.claims) == 0 {
		return nil
	}
	return s.claims[instance]
}

// Lookup finds a matcher by instance and ID.
func (s *Snapshot) Lookup(instance, id string) (CompiledMatcher, bool) {
	if s == nil {
		return CompiledMatcher{}, false
	}
	for _, m := range s.matchers {
		if m.Instance == instance && m.ID == id {
			return m, true
		}
	}
	return CompiledMatcher{}, false
}

// DescribedSet is one instance's contribution to a snapshot, in configured
// order.
type DescribedSet struct {
	Instance string
	Order    int
	Matchers []Matcher
	// ClaimHosts is the instance configuration's claimed hostnames. It travels
	// with the described matchers so a claimed URL is only ever reclassified by
	// a matcher from this same set.
	ClaimHosts []string
}

// SnapshotStore holds the current snapshot and swaps it atomically.
//
// A failed replacement keeps the previous snapshot for the remainder of the
// process and reports the new failure. That is deliberate: dropping working
// matchers because a later describe went wrong would turn a provider hiccup
// into terminal output silently losing links, and relaunch already starts
// clean.
type SnapshotStore struct {
	mu      sync.Mutex
	current atomic.Pointer[Snapshot]
	nextGen uint64
	lastErr error
}

// NewSnapshotStore returns a store holding an empty snapshot.
func NewSnapshotStore() *SnapshotStore {
	s := &SnapshotStore{}
	s.current.Store(&Snapshot{generation: 0})
	return s
}

// Current returns the live snapshot. It never returns nil.
func (s *SnapshotStore) Current() *Snapshot {
	if snap := s.current.Load(); snap != nil {
		return snap
	}
	return &Snapshot{}
}

// LastError reports the most recent failed replacement, or nil if the last
// replacement succeeded.
func (s *SnapshotStore) LastError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}

// Replace compiles every declared matcher and publishes a new snapshot. On any
// failure the previous snapshot stays live and the error is recorded.
func (s *SnapshotStore) Replace(sets []DescribedSet) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(sets) > resource.MaxProviders {
		s.lastErr = fmt.Errorf("resourceprovider: %d providers declared matchers, the limit is %d", len(sets), resource.MaxProviders)
		return s.lastErr
	}

	compiled := make([]CompiledMatcher, 0, 16)
	for _, set := range sets {
		if len(set.Matchers) > resource.MaxMatchersPerProvider {
			s.lastErr = fmt.Errorf("resourceprovider: instance %q declared %d matchers, the limit is %d", set.Instance, len(set.Matchers), resource.MaxMatchersPerProvider)
			return s.lastErr
		}
		seen := make(map[string]bool, len(set.Matchers))
		for _, m := range set.Matchers {
			if m.ID == "" {
				s.lastErr = fmt.Errorf("resourceprovider: instance %q declared a matcher with no id", set.Instance)
				return s.lastErr
			}
			if seen[m.ID] {
				s.lastErr = fmt.Errorf("resourceprovider: instance %q declared matcher id %q twice", set.Instance, m.ID)
				return s.lastErr
			}
			seen[m.ID] = true
			re, err := regexp.Compile(m.Pattern)
			if err != nil {
				s.lastErr = fmt.Errorf("resourceprovider: instance %q matcher %q is not a valid RE2 expression", set.Instance, m.ID)
				return s.lastErr
			}
			compiled = append(compiled, CompiledMatcher{
				Instance: set.Instance,
				ID:       m.ID,
				Pattern:  m.Pattern,
				Priority: m.Priority,
				Order:    set.Order,
				re:       re,
			})
		}
	}

	sortMatchers(compiled)

	claims := make(map[string][]string)
	for _, set := range sets {
		hosts := normalizeClaimHosts(set.ClaimHosts)
		if len(hosts) > 0 {
			claims[set.Instance] = hosts
		}
	}

	s.nextGen++
	s.current.Store(&Snapshot{generation: s.nextGen, matchers: compiled, claims: claims})
	s.lastErr = nil
	return nil
}

// claimHostsProvider is the optional capability by which an adapter surfaces
// its instance configuration's claimed hosts. CommandProvider implements it;
// the Manager reads it when assembling a described set, so a fake or a future
// transport that has nothing to claim simply omits it.
type claimHostsProvider interface {
	ClaimHosts() []string
}

// normalizeClaimHosts lowercases and trims claimed-hostname entries and drops
// anything that is not a bare hostname. internal/config already refuses
// malformed entries loudly; this second pass keeps programmatic callers from
// smuggling a scheme or a path past the scanner, using the same shape rule.
func normalizeClaimHosts(entries []string) []string {
	if len(entries) == 0 {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		if host, ok := config.NormalizeClaimHost(entry); ok {
			out = append(out, host)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// sortMatchers imposes the documented precedence: ascending configured-provider
// order, then descending priority, then matcher ID. Built-in matchers keep
// precedence over all of these; that is the scanner's business, not the
// snapshot's.
func sortMatchers(m []CompiledMatcher) {
	sort.SliceStable(m, func(i, j int) bool {
		if m[i].Order != m[j].Order {
			return m[i].Order < m[j].Order
		}
		if m[i].Priority != m[j].Priority {
			return m[i].Priority > m[j].Priority
		}
		if m[i].ID != m[j].ID {
			return m[i].ID < m[j].ID
		}
		return m[i].Instance < m[j].Instance
	})
}
