package contentlink

import (
	"sync"
	"unicode/utf8"
)

// Pending names side-effecting work a caller may run after rendering. Only
// file and diff candidates are produced. Raw is the exact rendered token.
type Pending struct {
	Kind Kind
	Raw  string
}

type resolution struct {
	ref   Ref
	found bool
}

// ResolutionSnapshot is an immutable ready-only view used during rendering.
// A missing key means unresolved, not a negative result.
type ResolutionSnapshot struct{ entries map[Pending]resolution }

func (s ResolutionSnapshot) Lookup(kind Kind, raw string) (ref Ref, found, ready bool) {
	result, ready := s.entries[Pending{Kind: kind, Raw: raw}]
	return result.ref, result.found, ready
}

// ResolutionIndex is a bounded per-surface cache. Resolution work happens
// outside this type; Put applies its result on the update path, and Snapshot
// gives View a side-effect-free copy.
type ResolutionIndex struct {
	mu      sync.RWMutex
	limit   int
	entries map[Pending]resolution
	order   []Pending
}

func NewResolutionIndex(limit int) *ResolutionIndex {
	if limit <= 0 || limit > MaxPendingResolutions {
		limit = MaxPendingResolutions
	}
	return &ResolutionIndex{limit: limit, entries: make(map[Pending]resolution)}
}

func (i *ResolutionIndex) Put(candidate Pending, ref Ref, found bool) bool {
	if i == nil || !validCandidate(candidate) || found && (ref.Kind != candidate.Kind || ref.Value == "") {
		return false
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if _, exists := i.entries[candidate]; !exists {
		if len(i.entries) >= i.limit {
			oldest := i.order[0]
			i.order = i.order[1:]
			delete(i.entries, oldest)
		}
		i.order = append(i.order, candidate)
	}
	i.entries[candidate] = resolution{ref: ref, found: found}
	return true
}

func (i *ResolutionIndex) Snapshot() ResolutionSnapshot {
	if i == nil {
		return ResolutionSnapshot{entries: map[Pending]resolution{}}
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	entries := make(map[Pending]resolution, len(i.entries))
	for key, value := range i.entries {
		entries[key] = value
	}
	return ResolutionSnapshot{entries: entries}
}

func (i *ResolutionIndex) Reset() {
	if i == nil {
		return
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.entries = make(map[Pending]resolution)
	i.order = nil
}

func validCandidate(candidate Pending) bool {
	return candidate.Raw != "" && utf8.ValidString(candidate.Raw) && utf8.RuneCountInString(candidate.Raw) <= MaxRenderedColumns && !containsControl(candidate.Raw) &&
		(candidate.Kind == KindFile || candidate.Kind == KindDiff)
}

func appendPending(pending []Pending, candidate Pending) []Pending {
	if !validCandidate(candidate) || len(pending) >= MaxPendingResolutions {
		return pending
	}
	for _, existing := range pending {
		if existing == candidate {
			return pending
		}
	}
	return append(pending, candidate)
}
