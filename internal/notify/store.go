package notify

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/marcus/sidecar/internal/config"
)

// FileName is the notification log inside the sidecar state dir.
const FileName = "notifications.jsonl"

// ErrNotFound is returned for an id the store has never seen.
var ErrNotFound = errors.New("notification not found")

// Store is the narrow persistence interface every surface talks to. It is
// deliberately small enough to reimplement over SQLite — or over nothing at
// all, as MemStore does in tests — without any caller changing.
type Store interface {
	// Post records a notification, filling in its defaults, and returns the
	// stored record. Posting an id that already exists is a no-op that returns
	// the existing record, so a CLI fallback path can never double-file.
	Post(n Notification) (Notification, error)
	MarkRead(id string) error
	Dismiss(id string) error
	// List returns every retained notification, newest first.
	List() ([]Notification, error)
	// Sweep drops dismissed notifications past the retention window and
	// returns how many it removed.
	Sweep(now time.Time) (int, error)
	Close() error
}

// Path returns the notification log path under stateDir.
func Path(stateDir string) string {
	return filepath.Join(stateDir, FileName)
}

// eventKind names an appended record. The log is append-only: state is the
// fold of its events, which is what makes a concurrent CLI append and a TUI
// append merge instead of overwrite.
type eventKind string

const (
	eventPosted    eventKind = "posted"
	eventRead      eventKind = "read"
	eventDismissed eventKind = "dismissed"
)

type event struct {
	Event        eventKind     `json:"event"`
	At           time.Time     `json:"at"`
	ID           string        `json:"id,omitempty"`
	Notification *Notification `json:"notification,omitempty"`
}

// JSONLStore is the default store: one append-only JSONL file under the state
// dir, folded and compacted on open.
type JSONLStore struct {
	mu      sync.Mutex
	path    string
	order   []string
	records map[string]Notification
	// events counts the lines currently in the file, so compaction can tell
	// whether rewriting would actually shorten it.
	events int
}

var _ Store = (*JSONLStore)(nil)

// Open loads (and compacts) the notification log under stateDir. A missing
// file is an empty store, not an error.
func Open(stateDir string) (*JSONLStore, error) {
	if stateDir == "" {
		return nil, errors.New("notify: empty state dir")
	}
	if err := config.AssertIsolatedPath(stateDir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return nil, err
	}
	s := &JSONLStore{
		path:    Path(stateDir),
		records: map[string]Notification{},
	}
	if err := s.openLoad(time.Now().UTC()); err != nil {
		return nil, err
	}
	return s, nil
}

// OpenPath is Open against an explicit file path. Tests and any future caller
// that owns its own directory use it; ordinary code uses Open.
func OpenPath(path string) (*JSONLStore, error) {
	if path == "" {
		return nil, errors.New("notify: empty store path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	s := &JSONLStore{path: path, records: map[string]Notification{}}
	if err := s.openLoad(time.Now().UTC()); err != nil {
		return nil, err
	}
	return s, nil
}

// Path returns the file this store writes.
func (s *JSONLStore) Path() string { return s.path }

// openLoad folds and compacts the log at open time, under the same exclusive
// lock every other write takes.
func (s *JSONLStore) openLoad(now time.Time) error {
	return s.withFileLock(func() error {
		if err := s.reload(); err != nil {
			return err
		}
		pruned := s.prune(now)
		// Compaction on load: rewrite whenever the log carries more lines than
		// surviving records, so a long-lived install does not accumulate an
		// unbounded read/dismiss tail.
		if pruned > 0 || s.events > len(s.order) {
			return s.rewrite()
		}
		return nil
	})
}

// reload discards the in-memory fold and rebuilds it from the file. The file is
// the authority, not this process's memory: every writer appends, so re-folding
// before a write (and before a rewrite) is what stops two processes sharing one
// log from erasing each other's records. Callers hold the file lock.
func (s *JSONLStore) reload() error {
	s.order = nil
	s.records = map[string]Notification{}
	s.events = 0

	f, err := os.Open(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	lines := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev event
		if err := json.Unmarshal(line, &ev); err != nil {
			// A truncated or hand-edited line is skipped rather than fatal:
			// losing one alert must never cost the user the whole log.
			continue
		}
		lines++
		s.apply(ev)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	s.events = lines
	return nil
}

// lockTimeout bounds how long a writer waits for the log. It matches
// internal/shellstate's manifest lock, which is the same shape of problem: a
// small JSON(L) file several sidecar processes append to.
const lockTimeout = 5 * time.Second

// withFileLock runs fn holding an exclusive flock on <path>.lock. Every read
// that precedes a write, and every rewrite, runs inside one, so a fold can never
// be built from a file another process is midway through replacing.
func (s *JSONLStore) withFileLock(fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	lock, err := os.OpenFile(s.path+".lock", os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	defer func() {
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		_ = lock.Close()
	}()
	deadline := time.Now().Add(lockTimeout)
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("notify: lock acquisition timeout after %v", lockTimeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fn()
}

func (s *JSONLStore) apply(ev event) {
	switch ev.Event {
	case eventPosted:
		if ev.Notification == nil || ev.Notification.ID == "" {
			return
		}
		n := *ev.Notification
		if _, exists := s.records[n.ID]; !exists {
			s.order = append(s.order, n.ID)
		}
		s.records[n.ID] = n
	case eventRead:
		if n, ok := s.records[ev.ID]; ok && n.ReadAt == nil {
			at := ev.At.UTC()
			n.ReadAt = &at
			s.records[n.ID] = n
		}
	case eventDismissed:
		if n, ok := s.records[ev.ID]; ok && n.DismissedAt == nil {
			at := ev.At.UTC()
			n.DismissedAt = &at
			// Dismissing implies seen, on the fold as well as on the write.
			if n.ReadAt == nil {
				n.ReadAt = &at
			}
			s.records[n.ID] = n
		}
	}
}

// prune drops retained-out records, returning how many went.
func (s *JSONLStore) prune(now time.Time) int {
	kept := s.order[:0]
	removed := 0
	for _, id := range s.order {
		n, ok := s.records[id]
		if !ok {
			removed++
			continue
		}
		if Expired(n, now) {
			delete(s.records, id)
			removed++
			continue
		}
		kept = append(kept, id)
	}
	s.order = kept
	return removed
}

// rewrite replaces the log with one posted event per surviving record.
func (s *JSONLStore) rewrite() error {
	tmp := fmt.Sprintf("%s.tmp.%d", s.path, os.Getpid())
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	count := 0
	for _, id := range s.order {
		n, ok := s.records[id]
		if !ok {
			continue
		}
		rec := n
		if err := enc.Encode(event{Event: eventPosted, At: n.CreatedAt, ID: n.ID, Notification: &rec}); err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return err
		}
		count++
	}
	if err := w.Flush(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	s.events = count
	return nil
}

func (s *JSONLStore) append(ev event) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	s.events++
	return nil
}

// Post implements Store.
func (s *JSONLStore) Post(n Notification) (Notification, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	n = Normalize(n, time.Now())
	var out Notification
	err := s.withFileLock(func() error {
		// Re-fold first: another process may have posted since this store last
		// looked, and a dedupe decision made against a stale memory is how a
		// record gets filed twice.
		if err := s.reload(); err != nil {
			return err
		}
		if existing, ok := s.records[n.ID]; ok {
			out = existing
			return nil
		}
		rec := n
		if err := s.append(event{Event: eventPosted, At: n.CreatedAt, ID: n.ID, Notification: &rec}); err != nil {
			return err
		}
		s.order = append(s.order, n.ID)
		s.records[n.ID] = n
		out = n
		return nil
	})
	if err != nil {
		return Notification{}, err
	}
	return out, nil
}

// MarkRead implements Store.
func (s *JSONLStore) MarkRead(id string) error {
	return s.mark(id, eventRead)
}

// Dismiss implements Store.
func (s *JSONLStore) Dismiss(id string) error {
	return s.mark(id, eventDismissed)
}

func (s *JSONLStore) mark(id string, kind eventKind) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.withFileLock(func() error {
		if err := s.reload(); err != nil {
			return err
		}
		n, ok := s.records[id]
		if !ok {
			return fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		if kind == eventRead && n.ReadAt != nil {
			return nil
		}
		if kind == eventDismissed && n.DismissedAt != nil {
			return nil
		}
		at := time.Now().UTC()
		if err := s.append(event{Event: kind, At: at, ID: id}); err != nil {
			return err
		}
		switch kind {
		case eventRead:
			n.ReadAt = &at
		case eventDismissed:
			n.DismissedAt = &at
			// Dismissing implies seen: an unread counter that keeps counting a
			// notification the user has thrown away is a bug, not a feature.
			if n.ReadAt == nil {
				n.ReadAt = &at
			}
		}
		s.records[id] = n
		return nil
	})
}

// Get returns one record.
func (s *JSONLStore) Get(id string) (Notification, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.records[id]
	return n, ok
}

// List implements Store.
func (s *JSONLStore) List() ([]Notification, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot(), nil
}

func (s *JSONLStore) snapshot() []Notification {
	out := make([]Notification, 0, len(s.order))
	for _, id := range s.order {
		if n, ok := s.records[id]; ok {
			out = append(out, n)
		}
	}
	SortNewestFirst(out)
	return out
}

// Sweep implements Store. It is also this store's cross-process read point: it
// re-folds the file first, so records another process appended since the last
// sweep become visible here instead of being overwritten by the rewrite.
func (s *JSONLStore) Sweep(now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	err := s.withFileLock(func() error {
		if err := s.reload(); err != nil {
			return err
		}
		removed = s.prune(now)
		if removed == 0 {
			return nil
		}
		return s.rewrite()
	})
	return removed, err
}

// Close implements Store. The log is written through on every call, so there
// is nothing buffered to flush; Close exists so the interface can front a
// store that does hold a handle.
func (s *JSONLStore) Close() error { return nil }

// ReadAll folds the log at path without opening a store: no compaction, no
// writes, no locks. `sidecar notify list` uses it so listing works with no TUI
// running and never rewrites a file another process is appending to.
func ReadAll(path string) ([]Notification, error) {
	s := &JSONLStore{path: path, records: map[string]Notification{}}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev event
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		s.apply(ev)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	s.prune(time.Now().UTC())
	return s.snapshot(), nil
}
