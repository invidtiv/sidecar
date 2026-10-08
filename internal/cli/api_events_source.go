package cli

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/hostnotify"
	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/hostserve"
	"github.com/marcus/sidecar/internal/livewatch"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/tmuxformat"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/uiapi"
)

// WatchCatalog reuses hostserve's manifest watches, inventory split and the
// desktop's agent status collector. Only changed row facts invalidate the
// catalog; advancing observation times and preview bytes do not.
func (b *mobileBackend) WatchCatalog(ctx context.Context) (<-chan struct{}, error) {
	projects, err := configuredProjects()
	if err != nil {
		return nil, err
	}
	changes := make(chan struct{}, 1)
	signal := func() {
		// Whatever changed, no request may reuse a collection that predates it.
		b.invalidateCatalog()
		select {
		case changes <- struct{}{}:
		default:
		}
	}
	watch, err := livewatch.NewPathWatcher(livewatch.Config{Quiet: 200 * time.Millisecond, MaxLatency: time.Second, Ignore: func(path string) bool {
		// Ignore unrelated per-project caches and atomic-write scratch files.
		return path != config.ConfigPath() && filepath.Base(path) != "shells.json" && filepath.Dir(path) != filepath.Join(b.env.StateDir, "projects")
	}})
	if err != nil {
		return nil, fmt.Errorf("watch API configuration: %w", err)
	}
	watch.Watch(b.workspaceWatchTargets(projects)...)
	b.watchMu.Lock()
	if b.watchCancel != nil {
		b.watchMu.Unlock()
		watch.Stop()
		return nil, fmt.Errorf("catalog observer already started")
	}
	ctx, b.watchCancel = context.WithCancel(ctx)
	b.watchDone = make(chan struct{})
	done := b.watchDone
	b.watchMu.Unlock()
	go func() {
		defer close(done)
		defer watch.Stop()
		start := func(projects []hostserve.Project) func() {
			localCtx, stopLocal := context.WithCancel(ctx)
			localDone := make(chan struct{})
			go func(done chan struct{}, observed []hostserve.Project, runCtx context.Context) {
				defer close(done)
				var previous [32]byte
				aliases := map[string]string{}
				err := hostserve.Serve(runCtx, hostserve.Options{ObservationOnly: true, Out: io.Discard, Projects: observed, OnNotifications: func(events notify.LaneEvents) {
					if cfg, err := config.Load(); err == nil {
						notify.ApplyConfig(cfg.Notifications)
					}
					store, err := notify.Open(b.env.StateDir)
					if err != nil {
						log.Printf("API notification store: %v", err)
						return
					}
					defer func() { _ = store.Close() }()
					for _, n := range events.Post {
						result, err := store.Post(n)
						if err != nil {
							log.Printf("API notification post: %v", err)
						} else if n.Transition != nil && n.Transition.Class == notify.TransitionWaiting && result.ID != n.ID {
							aliases[n.ID] = result.ID
						}
					}
					for _, id := range events.Dismiss {
						canonical := id
						if aliases[id] != "" {
							canonical = aliases[id]
						}
						if err := store.Dismiss(canonical); err != nil {
							log.Printf("API notification withdrawal: %v", err)
						}
						delete(aliases, id)
					}
				}, OnSnapshot: func(snapshot hostproto.Snapshot) {
					current := catalogObservationDigest(snapshot)
					if current != previous {
						previous = current
						signal()
					}
				}})
				if err != nil && runCtx.Err() == nil {
					signal()
				}
			}(localDone, projects, localCtx)
			return func() { stopLocal(); <-localDone }
		}
		stopLocal := start(projects)
		defer func() { stopLocal() }()
		var remote <-chan struct{} // nil when no registry exists
		if b.registry != nil {
			ch := make(chan struct{}, 1)
			remote = ch
			go func() {
				previous := make(map[string]string)
				aliases := make(map[string]string)
				for {
					select {
					case <-ctx.Done():
						return
					case update, ok := <-b.registry.Updates():
						if !ok {
							return
						}
						// Live transitions are independent of snapshot digests. An otherwise
						// unchanged host update must still reach the centre.
						if err := b.persistRemoteNotifications(update, aliases); err != nil {
							log.Printf("API remote notifications: %v", err)
						}
						digest := remoteCatalogObservationDigest(update)
						if previous[update.HostID] == digest {
							continue
						}
						previous[update.HostID] = digest
						select {
						case ch <- struct{}{}:
						default:
						}
					}
				}
			}()
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-remote:
				signal()
			case <-b.terminalChanges:
				signal()
			case <-watch.Signals():
				signal()
				// Project changes replace only this read-only local observer.
				updated, err := configuredProjects()
				if err != nil {
					continue
				}
				watch.Watch(b.workspaceWatchTargets(updated)...)
				if !reflect.DeepEqual(projects, updated) {
					stopLocal()
					stopLocal = start(updated)
					projects = updated
				}
			}
		}
	}()
	return changes, nil
}

func catalogObservationDigest(snapshot hostproto.Snapshot) [32]byte {
	snapshot.Generation = 0
	snapshot.ObservedAt = time.Time{}
	snapshot.Projects = append([]hostproto.Project(nil), snapshot.Projects...)
	for i := range snapshot.Projects {
		p := &snapshot.Projects[i]
		p.Items = append([]hostproto.Item(nil), p.Items...)
		for j := range p.Items {
			item := &p.Items[j]
			item.ObservedAt = time.Time{}
			item.Preview = ""
			if item.Agent != nil {
				a := *item.Agent
				a.CapturedAt = time.Time{}
				item.Agent = &a
			}
		}
	}
	data, _ := json.Marshal(snapshot)
	return sha256.Sum256(data)
}

func remoteCatalogObservationDigest(update hosts.Update) string {
	var digest [32]byte
	if update.Snapshot != nil {
		digest = catalogObservationDigest(*update.Snapshot)
	}
	return fmt.Sprintf("%d:%s:%s:%x", update.Incarnation, update.Health.State, update.Health.Detail, digest)
}

// GeometryHolder reads just the owning session's lease option. It never
// captures output or runs a catalog query. Unknown/unreachable is null rather
// than a made-up holder. U1 presence may supply more specific client labels.
func (b *mobileBackend) GeometryHolder(ctx context.Context, term uiapi.TerminalInfo) *uiapi.GeometryHolder {
	args := []string{"display-message", "-t", term.Session, "-p", "#{@sidecar-owner}"}
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var data []byte
	var err error
	remote := term.OwnerHostID != "" && !strings.HasPrefix(term.OwnerHostID, "local:")
	if remote {
		if b.registry == nil {
			return nil
		}
		data, err = b.registry.RunTmux(readCtx, term.OwnerHostID, args)
	} else {
		data, err = exec.CommandContext(readCtx, "tmux", tmuxformat.ClientArgs(args...)...).Output()
	}
	if err != nil {
		return nil
	}
	owner := tty.LeaseOwnerID(strings.TrimSpace(string(data)))
	if owner == "" || !tty.ValidInstanceID(owner) {
		return nil
	}
	host := tty.InstanceHost(owner)
	if index := strings.LastIndex(host, "-mobile-"); index >= 0 {
		if tty.InstancePID(owner) == os.Getpid() && !remote {
			return &uiapi.GeometryHolder{Kind: "api", Label: "API terminal"}
		}
		return &uiapi.GeometryHolder{Kind: "mobile", Label: "Mobile app"}
	}
	return &uiapi.GeometryHolder{Kind: "tui", Label: "TUI on " + host}
}

// Workspace reads include forgotten records which are absent from catalog rows.
// Watch the durable manifests themselves so tombstone-only edits still push.
func (b *mobileBackend) workspaceWatchTargets(projects []hostserve.Project) []livewatch.Target {
	targets := []livewatch.Target{livewatch.File(config.ConfigPath()), livewatch.Dir(filepath.Join(b.env.StateDir, "projects"))}
	roots := make([]string, 0, len(projects))
	for _, p := range projects {
		roots = append(roots, p.Path)
	}
	for _, dir := range projectdir.LookupAllWithBase(b.env.StateDir, roots) {
		targets = append(targets, livewatch.File(filepath.Join(dir, "shells.json")))
	}
	return targets
}

// persistRemoteNotifications consumes authenticated live events, never synthesizing
// transitions from catalog state or replaying a reconnect baseline.
func (b *mobileBackend) persistRemoteNotifications(update hosts.Update, aliases map[string]string) error {
	if len(update.Notify) == 0 {
		return nil
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	events := hostnotify.Adapt(update, cfg.Notifications.SSH.ManagedHosts, time.Now().UTC())
	if len(events.Post)+len(events.Dismiss)+len(events.DismissTransitions) == 0 {
		return nil
	}
	notify.ApplyConfig(cfg.Notifications)
	store, err := notify.Open(b.env.StateDir)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	// A host may withdraw an old wait and announce a new episode in one
	// update. Apply the wire order so that withdrawal cannot retire the new wait.
	now := time.Now().UTC()
	for _, event := range update.Notify {
		next := hostnotify.Adapt(hosts.Update{HostID: update.HostID, Notify: []hostproto.NotifyEvent{event}}, cfg.Notifications.SSH.ManagedHosts, now)
		if err := hostnotify.Apply(store, next, aliases); err != nil {
			return err
		}
	}
	return nil
}
