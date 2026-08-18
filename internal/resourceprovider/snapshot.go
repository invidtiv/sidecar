package resourceprovider

import (
	"fmt"
	"regexp"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/marcus/sidecar/internal/resource"
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
}

// Regexp returns the compiled expression. The whole match is the locator.
func (m CompiledMatcher) Regexp() *regexp.Regexp { return m.re }

// Snapshot is an immutable, ordered set of compiled matchers. The scanner holds
// one for the duration of a scan; a replacement never mutates one in place.
type Snapshot struct {
	generation uint64
	matchers   []CompiledMatcher
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
	s.nextGen++
	s.current.Store(&Snapshot{generation: s.nextGen, matchers: compiled})
	s.lastErr = nil
	return nil
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
