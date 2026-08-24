package contentlink

import (
	"container/list"
	"path/filepath"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	MaxResolutionEntries = 2048
	fileNegativeTTL      = 2 * time.Second
	resolvedTTL          = 30 * time.Second
)

// Pending names side-effecting work a caller may run after rendering. Only
// file and diff candidates are produced. Raw is the exact rendered token.
type Pending struct {
	Kind Kind
	Raw  string
}

// ResolutionRequest is the root-aware identity carried through asynchronous
// resolution. Token is never reused and lets Apply reject stale results.
type ResolutionRequest struct {
	Root      string
	Candidate Pending
	Token     uint64
}

type ResolutionResult struct {
	Request ResolutionRequest
	Ref     Ref
	Found   bool
}

type BeginOutcome uint8

const (
	BeginRejected BeginOutcome = iota
	BeginRequested
	BeginReady
	BeginInFlight
)

type resolution struct {
	ref   Ref
	found bool
}

type resolutionKey struct {
	root string
	Pending
}

type resolutionEntry struct {
	result  resolution
	expires time.Time
	lru     *list.Element
}

// ResolutionSnapshot is an immutable ready-only view used during rendering.
// A missing key means unresolved, not a negative result.
type ResolutionSnapshot struct {
	entries    map[Pending]resolution
	generation uint64
}

func (s ResolutionSnapshot) Lookup(kind Kind, raw string) (ref Ref, found, ready bool) {
	result, ready := s.entries[Pending{Kind: kind, Raw: raw}]
	return result.ref, result.found, ready
}

func (s ResolutionSnapshot) Generation() uint64 { return s.generation }

// ResolutionIndex is a bounded root-aware true LRU. Resolution work happens
// outside this type; Begin deduplicates work, Apply rejects stale tokens, and
// snapshots give presentation a ready-only immutable copy. Put and Snapshot
// retain the rootless document/deck contract.
type ResolutionIndex struct {
	mu       sync.Mutex
	limit    int
	now      func() time.Time
	entries  map[resolutionKey]*resolutionEntry
	lru      *list.List
	inflight map[resolutionKey]uint64
	versions map[string]uint64
	next     uint64
}

func NewResolutionIndex(limit int) *ResolutionIndex {
	return NewResolutionIndexWithClock(limit, time.Now)
}

func NewResolutionIndexWithClock(limit int, now func() time.Time) *ResolutionIndex {
	if limit <= 0 || limit > MaxResolutionEntries {
		limit = MaxResolutionEntries
	}
	if now == nil {
		now = time.Now
	}
	return &ResolutionIndex{limit: limit, now: now, entries: make(map[resolutionKey]*resolutionEntry), lru: list.New(), inflight: make(map[resolutionKey]uint64), versions: make(map[string]uint64)}
}

func normalizeResolutionRoot(root string) string {
	if root == "" {
		return ""
	}
	return filepath.Clean(root)
}

func (i *ResolutionIndex) Put(candidate Pending, ref Ref, found bool) bool {
	return i.PutForRoot("", candidate, ref, found)
}

func (i *ResolutionIndex) PutForRoot(root string, candidate Pending, ref Ref, found bool) bool {
	if i == nil || !validResolution(candidate, ref, found) {
		return false
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.putLocked(resolutionKey{root: normalizeResolutionRoot(root), Pending: candidate}, ref, found)
}

func (i *ResolutionIndex) putLocked(key resolutionKey, ref Ref, found bool) bool {
	result := resolution{ref: ref, found: found}
	if entry := i.entries[key]; entry != nil {
		changed := entry.result != result
		entry.result = result
		entry.expires = i.now().Add(resolutionTTL(key.Kind, found))
		i.lru.MoveToFront(entry.lru)
		if changed {
			i.versions[key.root]++
		}
		return changed
	}
	for len(i.entries) >= i.limit {
		i.removeLocked(i.lru.Back())
	}
	elem := i.lru.PushFront(key)
	i.entries[key] = &resolutionEntry{result: result, expires: i.now().Add(resolutionTTL(key.Kind, found)), lru: elem}
	i.versions[key.root]++
	return true
}

func resolutionTTL(kind Kind, found bool) time.Duration {
	if kind == KindFile && !found {
		return fileNegativeTTL
	}
	return resolvedTTL
}

// Begin returns one request for a currently unresolved root-aware candidate.
func (i *ResolutionIndex) Begin(root string, candidate Pending) (ResolutionRequest, bool) {
	request, outcome := i.BeginClassified(root, candidate)
	return request, outcome == BeginRequested
}

// BeginClassified distinguishes a ready cache answer from work already in
// flight. Callers use that distinction for truthful cache-hit diagnostics
// without weakening request deduplication.
func (i *ResolutionIndex) BeginClassified(root string, candidate Pending) (ResolutionRequest, BeginOutcome) {
	if i == nil || !validCandidate(candidate) {
		return ResolutionRequest{}, BeginRejected
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	key := resolutionKey{root: normalizeResolutionRoot(root), Pending: candidate}
	if i.readyLocked(key, i.now()) {
		return ResolutionRequest{}, BeginReady
	}
	if _, ok := i.inflight[key]; ok {
		return ResolutionRequest{}, BeginInFlight
	}
	i.next++
	if i.next == 0 {
		i.next++
	}
	i.inflight[key] = i.next
	return ResolutionRequest{Root: key.root, Candidate: candidate, Token: i.next}, BeginRequested
}

// Apply accepts only the current token. changed reports whether the effective
// ready answer changed; accepted distinguishes a refresh from a stale result.
func (i *ResolutionIndex) Apply(result ResolutionResult) (changed, accepted bool) {
	if i == nil || result.Request.Token == 0 || !validResolution(result.Request.Candidate, result.Ref, result.Found) {
		return false, false
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	key := resolutionKey{root: normalizeResolutionRoot(result.Request.Root), Pending: result.Request.Candidate}
	if i.inflight[key] != result.Request.Token {
		return false, false
	}
	delete(i.inflight, key)
	return i.putLocked(key, result.Ref, result.Found), true
}

func validResolution(candidate Pending, ref Ref, found bool) bool {
	return validCandidate(candidate) && (!found || ref.Kind == candidate.Kind && ref.Value != "")
}

func (i *ResolutionIndex) Snapshot() ResolutionSnapshot { return i.SnapshotForRoot("") }

func (i *ResolutionIndex) SnapshotForRoot(root string) ResolutionSnapshot {
	if i == nil {
		return ResolutionSnapshot{entries: map[Pending]resolution{}}
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	root = normalizeResolutionRoot(root)
	now := i.now()
	entries := make(map[Pending]resolution)
	var touched []*list.Element
	for elem := i.lru.Back(); elem != nil; {
		prev := elem.Prev()
		key := elem.Value.(resolutionKey)
		entry := i.entries[key]
		if !entry.expires.After(now) {
			i.removeLocked(elem)
		} else if key.root == root {
			entries[key.Pending] = entry.result
			touched = append(touched, elem)
		}
		elem = prev
	}
	for _, elem := range touched {
		i.lru.MoveToFront(elem)
	}
	return ResolutionSnapshot{entries: entries, generation: i.versions[root]}
}

func (i *ResolutionIndex) readyLocked(key resolutionKey, now time.Time) bool {
	entry := i.entries[key]
	if entry == nil {
		return false
	}
	if !entry.expires.After(now) {
		i.removeLocked(entry.lru)
		return false
	}
	i.lru.MoveToFront(entry.lru)
	return true
}

func (i *ResolutionIndex) removeLocked(elem *list.Element) {
	if elem == nil {
		return
	}
	delete(i.entries, elem.Value.(resolutionKey))
	i.versions[elem.Value.(resolutionKey).root]++
	i.lru.Remove(elem)
}

func (i *ResolutionIndex) Reset() {
	if i == nil {
		return
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.entries = make(map[resolutionKey]*resolutionEntry)
	i.inflight = make(map[resolutionKey]uint64)
	i.versions = make(map[string]uint64)
	i.lru.Init()
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
