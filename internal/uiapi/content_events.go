package uiapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"sync"
	"time"

	"github.com/marcus/sidecar/internal/contentservice"
	"github.com/marcus/sidecar/internal/livewatch"
)

// ContentRef is one currently open content pane. Reconnect events with a new
// set when panes change; subscriptions belong to the socket, not durable state.
type ContentRef struct {
	Project   string `json:"project"`
	Workspace string `json:"workspace,omitempty"`
	Kind      string `json:"kind"`
	Target    string `json:"target,omitempty"`
	Operation string `json:"operation,omitempty"`
	Path      string `json:"path,omitempty"`
}

type ContentEvent struct {
	Resources []ContentRef `json:"resources"`
}

type ContentWatchSource interface {
	WatchProject(context.Context, string, string, contentservice.ReadParams) ([]livewatch.Target, error)
}

func parseContentRefs(q url.Values) ([]ContentRef, error) {
	values := q["content"]
	if len(values) > 32 {
		return nil, fmt.Errorf("at most 32 open content panes may be watched")
	}
	refs := make([]ContentRef, 0, len(values))
	for _, raw := range values {
		if len(raw) > 8192 {
			return nil, fmt.Errorf("content reference exceeds 8192 bytes")
		}
		decoder := json.NewDecoder(bytes.NewBufferString(raw))
		decoder.DisallowUnknownFields()
		var ref ContentRef
		if err := decoder.Decode(&ref); err != nil {
			return nil, err
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			return nil, fmt.Errorf("content takes one JSON reference per parameter")
		}
		if ref.Project == "" {
			return nil, fmt.Errorf("content project is required")
		}
		switch ref.Kind {
		case contentservice.KindFile, contentservice.KindDiff, contentservice.KindIssue, contentservice.KindNote, contentservice.KindTree:
		default:
			return nil, fmt.Errorf("unknown content kind %q", ref.Kind)
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

func refParams(ref ContentRef) contentservice.ReadParams {
	q := url.Values{"kind": {ref.Kind}, "target": {ref.Target}, "path": {ref.Path}, "operation": {ref.Operation}}
	p, _ := contentParams(q)
	return p
}

// maxContentWatchDirsPerClient bounds the directory registrations one
// credential holds across all of its events streams. Each open pane owns its
// own watcher, and on macOS kqueue spends a descriptor on every entry of every
// watched directory, so the bound counts registrations rather than a union of
// paths, and it is per client rather than per socket. Local callers are trusted
// like the tmux socket and are not limited, as with terminals.
const maxContentWatchDirsPerClient = 64

// errWatchBudget refuses a subscription that would exceed the client's bound.
var errWatchBudget = errors.New("open content exceeds this client's 64 watched directories across its events streams; narrow the visible panes")

type watchBudget struct {
	mu   sync.Mutex
	used map[string]int
}

// reserve claims n more registrations for client, or refuses all of them.
func (b *watchBudget) reserve(c caller, n int) bool {
	if c.listener == ListenerLocal || n <= 0 {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.used[c.client]+n > maxContentWatchDirsPerClient {
		return false
	}
	if b.used == nil {
		b.used = make(map[string]int)
	}
	b.used[c.client] += n
	return true
}

func (b *watchBudget) release(c caller, n int) {
	if c.listener == ListenerLocal || n <= 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.used[c.client] -= n
	if b.used[c.client] <= 0 {
		delete(b.used, c.client)
	}
}

// watchRegistrations is how many directories livewatch registers for targets:
// a directory target registers itself and a file target its parent.
func watchRegistrations(targets []livewatch.Target) int {
	dirs := map[string]bool{}
	for _, target := range targets {
		path := filepath.Clean(target.Path)
		if !target.Dir {
			path = filepath.Dir(path)
		}
		dirs[path] = true
	}
	return len(dirs)
}

// startContentWatches validates before hello, and owns no watches after the
// socket closes. Each pending content batch coalesces into at most 32 refs.
func (s *Server) startContentWatches(ctx context.Context, c caller, refs []ContentRef, pending *eventPending) (func(), error) {
	source, ok := s.contentBackend().(ContentWatchSource)
	if !ok && len(refs) != 0 {
		return nil, fmt.Errorf("content backend does not support live watches")
	}
	var watchers []*livewatch.PathWatcher
	var stops []chan struct{}
	stop := func() {
		for _, watcher := range watchers {
			watcher.Stop()
		}
		for _, done := range stops {
			<-done
		}
	}
	for _, ref := range refs {
		targets, err := source.WatchProject(ctx, ref.Project, ref.Workspace, refParams(ref))
		if err != nil {
			stop()
			return nil, err
		}
		held := watchRegistrations(targets)
		if !s.contentWatches.reserve(c, held) {
			stop()
			return nil, errWatchBudget
		}
		watcher, err := livewatch.NewPathWatcher(livewatch.Config{})
		if err != nil {
			s.contentWatches.release(c, held)
			stop()
			return nil, err
		}
		watcher.Watch(targets...)
		watchers = append(watchers, watcher)
		done := make(chan struct{})
		stops = append(stops, done)
		go func(ref ContentRef, watcher *livewatch.PathWatcher, done chan struct{}, held int) {
			defer close(done)
			defer func() { s.contentWatches.release(c, held) }()
			currentTargets := targets
			reconcile := time.NewTicker(30 * time.Second)
			defer reconcile.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case _, ok := <-watcher.Signals():
					if !ok {
						return
					}
					pending.put(EventMessage{Type: "content", Content: &ContentEvent{Resources: []ContentRef{ref}}})
					// Git changes may alter the set of files in a working diff.
					next, watchErr := source.WatchProject(ctx, ref.Project, ref.Workspace, refParams(ref))
					if watchErr == nil {
						want := watchRegistrations(next)
						if want > held && !s.contentWatches.reserve(c, want-held) {
							watchErr = errWatchBudget
						} else if want < held {
							s.contentWatches.release(c, held-want)
						}
						if watchErr == nil {
							held = want
						}
					}
					if watchErr != nil {
						currentTargets = nil
						watcher.Watch()
						s.contentWatches.release(c, held)
						held = 0
						pending.put(EventMessage{Type: "error", Error: &ErrorDetail{Code: "rejected", Message: watchErr.Error()}})
					} else {
						currentTargets = next
						watcher.Watch(currentTargets...)
					}
				case <-reconcile.C:
					// Re-register only the resolved paths, never read/poll content here.
					watcher.Watch(currentTargets...)
				}
			}
		}(ref, watcher, done, held)
	}
	return stop, nil
}
