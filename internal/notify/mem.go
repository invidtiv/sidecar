package notify

import (
	"fmt"
	"sync"
	"time"
)

// MemStore is a Store that keeps nothing. It is what the app falls back to
// when the state dir cannot be opened, so a read-only or misconfigured state
// tree costs the user persistence, not notifications.
type MemStore struct {
	mu      sync.Mutex
	order   []string
	records map[string]Notification
}

var _ Store = (*MemStore)(nil)

// NewMemStore returns an empty in-memory store.
func NewMemStore() *MemStore {
	return &MemStore{records: map[string]Notification{}}
}

// Post implements Store.
func (s *MemStore) Post(n Notification) (PostResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n = Normalize(n, time.Now())
	if existing, ok := s.records[n.ID]; ok {
		return PostResult{Notification: existing, Reason: PostExistingID}, nil
	}
	if existing, ok := logicalDuplicate(s.order, s.records, n); ok {
		return PostResult{Notification: existing, Reason: PostExistingLogical}, nil
	}
	s.order = append(s.order, n.ID)
	s.records[n.ID] = n
	return PostResult{Notification: n, Created: true, Reason: PostCreated}, nil
}

// MarkRead implements Store.
func (s *MemStore) MarkRead(id string) error { return s.mark(id, false) }

// Dismiss implements Store.
func (s *MemStore) Dismiss(id string) error { return s.mark(id, true) }

func (s *MemStore) mark(id string, dismiss bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.records[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	at := time.Now().UTC()
	if n.ReadAt == nil {
		n.ReadAt = &at
	}
	if dismiss && n.DismissedAt == nil {
		n.DismissedAt = &at
	}
	s.records[id] = n
	return nil
}

// List implements Store.
func (s *MemStore) List() ([]Notification, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Notification, 0, len(s.order))
	for _, id := range s.order {
		if n, ok := s.records[id]; ok {
			out = append(out, n)
		}
	}
	SortNewestFirst(out)
	return out, nil
}

// Sweep implements Store.
func (s *MemStore) Sweep(now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.order[:0]
	removed := 0
	for _, id := range s.order {
		n, ok := s.records[id]
		if !ok || Expired(n, now) {
			delete(s.records, id)
			removed++
			continue
		}
		kept = append(kept, id)
	}
	s.order = kept
	return removed, nil
}

// Close implements Store.
func (s *MemStore) Close() error { return nil }
