package cli

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/hostserve"
	"github.com/marcus/sidecar/internal/livewatch"
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
		select {
		case changes <- struct{}{}:
		default:
		}
	}
	watch, err := livewatch.NewPathWatcher(livewatch.Config{Quiet: 200 * time.Millisecond, MaxLatency: time.Second})
	if err != nil {
		return nil, fmt.Errorf("watch API configuration: %w", err)
	}
	watch.Watch(livewatch.File(config.ConfigPath()))
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
				err := hostserve.Serve(runCtx, hostserve.Options{Out: io.Discard, Projects: observed, OnSnapshot: func(snapshot hostproto.Snapshot) {
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
				for {
					select {
					case <-ctx.Done():
						return
					case update, ok := <-b.registry.Updates():
						if !ok {
							return
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
			case <-watch.Signals():
				signal()
				// Project changes replace only this read-only local observer.
				updated, err := configuredProjects()
				if err != nil {
					continue
				}
				stopLocal()
				stopLocal = start(updated)
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
