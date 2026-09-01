package hostserve

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/livewatch"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

const (
	requestQuiet      = 200 * time.Millisecond
	requestMaxLatency = time.Second
)

type uiRequestFile struct {
	ID        string                     `json:"id"`
	CreatedAt time.Time                  `json:"createdAt"`
	TTLMs     int                        `json:"ttlMs"`
	Origin    uiRequestFileOrigin        `json:"origin"`
	Action    string                     `json:"action"`
	Target    hostproto.UIRequestTarget  `json:"target"`
	Options   hostproto.UIRequestOptions `json:"options"`
	Payload   json.RawMessage            `json:"payload"`
}

type uiRequestFileOrigin struct {
	TmuxSession string `json:"tmuxSession"`
	Namespace   string `json:"namespace"`
	ProjectKey  string `json:"projectKey"`
	WorkDir     string `json:"workDir"`
	HostID      string `json:"hostId"`
	Sessions    bool   `json:"sessions"`
	SessionsRow string `json:"sessionsRow"`
}

// requestWatch observes stateDir/requests the way manifestWatch observes
// shells.json: fsnotify, coalesce, never start a collection of its own.
type requestWatch struct {
	watcher *livewatch.PathWatcher
	dir     string
	seen    map[string]struct{}
}

func startRequestWatch() *requestWatch {
	w := &requestWatch{seen: make(map[string]struct{})}
	stateDir := config.StateDir()
	if stateDir == "" {
		return w
	}
	if err := config.AssertIsolatedPath(stateDir); err != nil {
		return w
	}
	w.dir = filepath.Join(stateDir, "requests")
	w.markExisting()
	watcher, err := livewatch.NewPathWatcher(livewatch.Config{
		Quiet:      requestQuiet,
		MaxLatency: requestMaxLatency,
		Ignore:     ignoreRequestNoise,
	})
	if err != nil {
		return w
	}
	w.watcher = watcher
	w.bind()
	return w
}

func (w *requestWatch) bind() {
	if w == nil || w.watcher == nil || w.dir == "" {
		return
	}
	if info, err := os.Stat(w.dir); err == nil && info.IsDir() {
		w.watcher.Watch(livewatch.Dir(w.dir))
		return
	}
	if dir := nearestExisting(w.dir); dir != "" {
		w.watcher.Watch(livewatch.Dir(dir))
	}
}

func ignoreRequestNoise(path string) bool {
	base := filepath.Base(path)
	return strings.Contains(base, ".tmp.") || strings.HasSuffix(base, ".acks")
}

func (w *requestWatch) markExisting() {
	if w == nil || w.dir == "" {
		return
	}
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !isRelayRequestName(entry.Name()) {
			continue
		}
		req, err := readUIRequestFile(filepath.Join(w.dir, entry.Name()))
		if err != nil || req.ID == "" {
			continue
		}
		w.seen[req.ID] = struct{}{}
	}
}

func (w *requestWatch) signals() <-chan struct{} {
	if w == nil || w.watcher == nil {
		return nil
	}
	return w.watcher.Signals()
}

func (w *requestWatch) stop() {
	if w == nil || w.watcher == nil {
		return
	}
	w.watcher.Stop()
	w.watcher = nil
}

func (w *requestWatch) drain(now time.Time, hostID, viewerInstance string, ownerOf func(string) string) []hostproto.UIRequest {
	if w == nil || w.dir == "" || viewerInstance == "" {
		return nil
	}
	w.bind()
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return nil
	}
	var out []hostproto.UIRequest
	for _, entry := range entries {
		if entry.IsDir() || !isRelayRequestName(entry.Name()) {
			continue
		}
		req, err := readUIRequestFile(filepath.Join(w.dir, entry.Name()))
		if err != nil || req.ID == "" {
			continue
		}
		if _, seen := w.seen[req.ID]; seen {
			continue
		}
		ttl := time.Duration(req.TTLMs) * time.Millisecond
		if ttl <= 0 || req.CreatedAt.IsZero() || now.Sub(req.CreatedAt) > ttl {
			w.seen[req.ID] = struct{}{}
			continue
		}
		if req.Action != hostproto.UIRequestActionOpen && req.Action != hostproto.UIRequestActionLayout {
			w.seen[req.ID] = struct{}{}
			continue
		}
		if req.Origin.TmuxSession == "" {
			w.seen[req.ID] = struct{}{}
			continue
		}
		if ownerOf == nil || ownerOf(req.Origin.TmuxSession) != viewerInstance {
			continue
		}
		event := wireUIRequest(req, hostID)
		if err := (hostproto.Message{Kind: hostproto.KindUIRequest, UIRequest: &event}).Validate(); err != nil {
			w.seen[req.ID] = struct{}{}
			continue
		}
		w.seen[req.ID] = struct{}{}
		out = append(out, event)
	}
	return out
}

func isRelayRequestName(name string) bool {
	if strings.Contains(name, ".tmp.") {
		return false
	}
	return strings.HasSuffix(name, "-open.json") || strings.HasSuffix(name, "-layout.json")
}

func readUIRequestFile(path string) (uiRequestFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return uiRequestFile{}, err
	}
	var req uiRequestFile
	if err := json.Unmarshal(data, &req); err != nil {
		return uiRequestFile{}, err
	}
	return req, nil
}

func wireUIRequest(req uiRequestFile, hostID string) hostproto.UIRequest {
	event := hostproto.UIRequest{
		ID:        req.ID,
		Action:    req.Action,
		CreatedAt: req.CreatedAt.UTC(),
		TTLMs:     req.TTLMs,
		Origin: hostproto.UIRequestOrigin{
			TmuxSession: req.Origin.TmuxSession,
			Namespace:   req.Origin.Namespace,
			ProjectKey:  req.Origin.ProjectKey,
			WorkDir:     req.Origin.WorkDir,
			HostID:      hostID,
			Sessions:    req.Origin.Sessions,
			SessionsRow: req.Origin.SessionsRow,
		},
		Target:  req.Target,
		Options: req.Options,
	}
	if len(req.Payload) > 0 && string(req.Payload) != "null" {
		event.Payload = req.Payload
	}
	return event
}

func readSessionLeaseOwner(runner workspaceinventory.Runner, session string) string {
	if runner == nil || session == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := runner.Output(ctx, "tmux", "display-message", "-t", session, "-p", "#{@sidecar-owner}")
	if err != nil {
		return ""
	}
	return tty.LeaseOwnerID(strings.TrimSpace(string(out)))
}
