package lifecyclestore

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

	"github.com/marcus/sidecar/internal/agentlifecycle"
	"github.com/marcus/sidecar/internal/config"
)

// FileName is the lifecycle log's name inside Sidecar's host-local state dir.
const FileName = "agent-lifecycle.jsonl"

// lockTimeout bounds how long a writer waits for the log.
//
// It is short on purpose. This store is written by provider hooks, which run in
// the agent's critical path, and the plan's rule is that a reporting failure
// must never delay or change the provider's operation. Waiting a long time for
// a lock would violate that far more visibly than dropping a report does.
const lockTimeout = 2 * time.Second

// JSONL is the host-local append-only lifecycle log.
//
// It is shared by every Sidecar process on the machine: short-lived hook
// invocations of `sidecar agent report`, the TUI, and the CLI. Correctness under
// that sharing comes from one rule — every mutation re-folds the file inside the
// exclusive lock before deciding anything. The file is the authority; this
// struct's maps are a cache of it. A fold built before the lock would let two
// concurrent hooks each believe they hold the same sequence.
type JSONL struct {
	mu   sync.Mutex
	path string
	ix   *index
	// lines is the number of records on disk, so compaction can tell whether a
	// rewrite would actually shorten the file.
	lines int
	now   func() time.Time
}

// Open returns the lifecycle store inside a Sidecar state directory.
func Open(stateDir string) (*JSONL, error) {
	if stateDir == "" {
		return nil, errors.New("lifecyclestore: empty state dir")
	}
	// Refuses to touch the developer's real state tree when isolation is
	// asserted, which it is by default inside any go test binary. Every proof
	// run in this repo depends on this being here.
	if err := config.AssertIsolatedPath(stateDir); err != nil {
		return nil, err
	}
	return OpenPath(filepath.Join(stateDir, FileName))
}

// OpenPath returns the lifecycle store at an exact path.
func OpenPath(path string) (*JSONL, error) {
	if path == "" {
		return nil, errors.New("lifecyclestore: empty path")
	}
	s := &JSONL{path: path, ix: newIndex(), now: time.Now}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	err := s.withFileLock(func() error {
		if err := s.reload(); err != nil {
			return err
		}
		// Hysteresis rather than "compact whenever anything was dropped": this
		// store is opened by every hook invocation, and rewriting the file on
		// each one would turn a cheap append into a full rewrite per provider
		// event.
		kept := retain(s.ix.records)
		if len(kept) != len(s.ix.records) || s.lines > len(kept)*2+8 {
			return s.rewrite(kept)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s, nil
}

// SetClock overrides the clock used for skew validation. Tests use it.
func (s *JSONL) SetClock(now func() time.Time) { s.now = now }

// Path returns the log's location, for diagnostics.
func (s *JSONL) Path() string { return s.path }

// reload rebuilds the fold from the file. It must be called under the lock.
//
// A malformed line is skipped, never fatal. The log is meant to be inspectable
// and repairable with ordinary tools, which means a human or an agent may well
// have hand-edited it, and a truncated final line is the normal result of a
// machine losing power mid-append. Losing one record must not cost the reader
// every other record.
func (s *JSONL) reload() error {
	f, err := os.Open(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.ix.rebuild(nil)
			s.lines = 0
			return nil
		}
		return err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	// The default 64KB limit would turn one long line into a hard error, which
	// is the opposite of tolerating a damaged file.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var records []agentlifecycle.Report
	lines := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var r agentlifecycle.Report
		if err := json.Unmarshal(line, &r); err != nil {
			continue
		}
		// A record from a future schema is skipped rather than guessed at. This
		// is why every line carries its version.
		if r.SchemaVersion != agentlifecycle.SchemaVersion {
			continue
		}
		lines++
		records = append(records, r)
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	// Refolding through admit rather than trusting the file is what makes a
	// tampered log safe: a hand-inserted record with a rewound sequence or a
	// resurrected run is dropped on load exactly as it would have been on
	// append.
	s.ix.rebuild(nil)
	for _, r := range records {
		if _, store, err := s.ix.admit(r); err == nil && store {
			s.ix.commit(r)
		}
	}
	s.lines = lines
	return nil
}

func (s *JSONL) Append(r agentlifecycle.Report) (agentlifecycle.Acceptance, error) {
	rec, err := prepare(r, s.now())
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var acc agentlifecycle.Acceptance
	err = s.withFileLock(func() error {
		if err := s.reload(); err != nil {
			return err
		}
		a, store, err := s.ix.admit(rec)
		if err != nil {
			return err
		}
		acc = a
		if !store {
			return nil
		}
		if err := s.appendLine(rec); err != nil {
			return err
		}
		s.ix.commit(rec)
		return nil
	})
	if err != nil {
		return "", err
	}
	return acc, nil
}

func (s *JSONL) AppendNext(r agentlifecycle.Report) (agentlifecycle.Report, agentlifecycle.Acceptance, error) {
	rec, err := prepare(r, s.now())
	if err != nil {
		return agentlifecycle.Report{}, "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var acc agentlifecycle.Acceptance
	err = s.withFileLock(func() error {
		// The reload, the assignment, and the write are all inside one exclusive
		// file lock. That is the entire point: two hook processes racing here
		// each see the other's records before choosing a number, so they cannot
		// choose the same one.
		if err := s.reload(); err != nil {
			return err
		}
		rec.Sequence = s.ix.nextSequence(rec)
		a, store, err := s.ix.admit(rec)
		if err != nil {
			return err
		}
		acc = a
		if !store {
			return nil
		}
		if err := s.appendLine(rec); err != nil {
			return err
		}
		s.ix.commit(rec)
		return nil
	})
	if err != nil {
		return agentlifecycle.Report{}, "", err
	}
	return rec, acc, nil
}

func (s *JSONL) Release(r agentlifecycle.Report) (agentlifecycle.Acceptance, error) {
	if r.Kind != agentlifecycle.KindRelease {
		return "", fmt.Errorf("%w: release requires kind %q, got %q",
			agentlifecycle.ErrValidation, agentlifecycle.KindRelease, r.Kind)
	}
	return s.Append(r)
}

func (s *JSONL) Latest(k PaneKey) (agentlifecycle.Report, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var (
		out agentlifecycle.Report
		ok  bool
	)
	// Reads refold too. A hook in another process appends between a TUI poll and
	// the next, and answering from a stale in-memory fold is exactly how a pane
	// would keep showing a lane the provider has already moved on from.
	_ = s.withFileLock(func() error {
		if err := s.reload(); err != nil {
			return err
		}
		out, ok = s.ix.latestFor(k)
		return nil
	})
	return out, ok
}

func (s *JSONL) List(k PaneKey) []agentlifecycle.Report {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []agentlifecycle.Report
	_ = s.withFileLock(func() error {
		if err := s.reload(); err != nil {
			return err
		}
		out = s.ix.listFor(k)
		return nil
	})
	return out
}

func (s *JSONL) Compact() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.withFileLock(func() error {
		if err := s.reload(); err != nil {
			return err
		}
		return s.rewrite(retain(s.ix.records))
	})
}

// appendLine writes one record. Must be called under the lock.
//
// A single write of a short line under O_APPEND is atomic, so a concurrent
// writer can never interleave inside a line. Sync is not optional here: hook
// processes exit immediately after reporting, and an unsynced write that dies
// with the machine loses the transition Sidecar was told about.
func (s *JSONL) appendLine(r agentlifecycle.Report) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	s.lines++
	return nil
}

// rewrite replaces the file with exactly records. Must be called under the lock.
//
// Temp file in the same directory, sync, rename: a reader either sees the whole
// old file or the whole new one, never a half-written log. The temp file is
// removed on every failure path so a crashed compaction leaves no debris beside
// the real log for someone to mistake for it later.
func (s *JSONL) rewrite(records []agentlifecycle.Report) error {
	tmp := fmt.Sprintf("%s.tmp.%d", s.path, os.Getpid())
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	cleanup := func() { _ = os.Remove(tmp) }

	w := bufio.NewWriter(f)
	for _, r := range records {
		data, err := json.Marshal(r)
		if err != nil {
			_ = f.Close()
			cleanup()
			return err
		}
		if _, err := w.Write(append(data, '\n')); err != nil {
			_ = f.Close()
			cleanup()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		_ = f.Close()
		cleanup()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		cleanup()
		return err
	}
	if err := f.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		cleanup()
		return err
	}

	s.ix.rebuild(records)
	s.lines = len(records)
	return nil
}

// withFileLock runs fn holding an exclusive flock on <path>.lock.
//
// The lock is a sidecar file rather than the log itself so that a reader using
// ordinary tools — cat, grep, an agent reading the JSONL — never contends with
// or is blocked by Sidecar's own writers.
func (s *JSONL) withFileLock(fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(s.path+".lock", os.O_CREATE|os.O_RDWR, 0o644)
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
			return fmt.Errorf("lifecyclestore: lock acquisition timeout after %v", lockTimeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fn()
}

// ReadAll folds a lifecycle log without locking or writing anything.
//
// It exists for read-only inspection — `sidecar agent explain`, an agent
// reading the file, a diagnostic dump — where taking an exclusive lock and
// possibly rewriting the file would be wrong. It skips malformed lines the same
// way a normal load does and never repairs, compacts, or creates anything.
func ReadAll(path string) ([]agentlifecycle.Report, error) {
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
	var out []agentlifecycle.Report
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var r agentlifecycle.Report
		if err := json.Unmarshal(line, &r); err != nil {
			continue
		}
		if r.SchemaVersion != agentlifecycle.SchemaVersion {
			continue
		}
		out = append(out, r)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// compile-time proof that both implementations satisfy the interface.
var (
	_ Store = (*Memory)(nil)
	_ Store = (*JSONL)(nil)
)
