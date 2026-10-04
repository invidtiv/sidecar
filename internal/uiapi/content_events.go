package uiapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
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

// startContentWatches validates before hello, and owns no watches after the
// socket closes. Each pending content batch coalesces into at most 32 refs.
func (s *Server) startContentWatches(ctx context.Context, refs []ContentRef, pending *eventPending) (func(), error) {
	source, ok := s.contentBackend().(ContentWatchSource)
	if !ok && len(refs) != 0 {
		return nil, fmt.Errorf("content backend does not support live watches")
	}
	dirs := map[string]bool{}
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
		for _, target := range targets {
			path := target.Path
			if !target.Dir {
				path = filepath.Dir(path)
			}
			dirs[path] = true
		}
		if len(dirs) > 64 {
			stop()
			return nil, fmt.Errorf("open content exceeds 64 watched directories; narrow the visible panes")
		}
		watcher, err := livewatch.NewPathWatcher(livewatch.Config{})
		if err != nil {
			stop()
			return nil, err
		}
		watcher.Watch(targets...)
		watchers = append(watchers, watcher)
		done := make(chan struct{})
		stops = append(stops, done)
		go func(ref ContentRef, watcher *livewatch.PathWatcher, done chan struct{}) {
			defer close(done)
			currentTargets := targets
			var watchErr error
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
					currentTargets, watchErr = source.WatchProject(ctx, ref.Project, ref.Workspace, refParams(ref))
					if watchErr != nil {
						watcher.Watch()
						pending.put(EventMessage{Type: "error", Error: &ErrorDetail{Code: "rejected", Message: watchErr.Error()}})
					} else {
						watcher.Watch(currentTargets...)
					}
				case <-reconcile.C:
					// Re-register only the resolved paths, never read/poll content here.
					watcher.Watch(currentTargets...)
				}
			}
		}(ref, watcher, done)
	}
	return stop, nil
}
