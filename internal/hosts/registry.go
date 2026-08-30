package hosts

import (
	"context"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/marcus/sidecar/internal/agentstatus"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// FromConfig reads the registered hosts.
//
// It returns nothing at all when the feature is off. That is the rollback
// guarantee stated as code rather than as a promise: with the flag off there
// is no host, so there is no client, no ssh child, and no remote row — the
// local path cannot differ because nothing else is reachable from here.
func FromConfig(cfg *config.Config) []Host {
	if cfg == nil || !features.IsEnabled(features.SidecarRemoteHosts.Name) {
		return nil
	}
	hosts := make([]Host, 0, len(cfg.Hosts.List))
	seen := make(map[string]bool, len(cfg.Hosts.List))
	for _, entry := range cfg.Hosts.List {
		target := strings.TrimSpace(entry.Target)
		if target == "" || entry.Disabled {
			continue
		}
		id := strings.TrimSpace(entry.ID)
		if id == "" {
			id = target
		}
		// Two hosts with one ID would collide their workspace rows, and the
		// second one silently wins. First registered keeps the name.
		if seen[id] {
			continue
		}
		seen[id] = true
		hosts = append(hosts, Host{
			ID:           id,
			Target:       target,
			RemoteBinary: strings.TrimSpace(entry.Binary),
			RemoteConfig: strings.TrimSpace(entry.Config),
			Env:          append([]string(nil), entry.Env...),
		})
	}
	sort.Slice(hosts, func(a, b int) bool { return hosts[a].ID < hosts[b].ID })
	return hosts
}

// DisabledFromConfig lists the hosts a user has registered but switched off.
//
// They are returned separately because they must still be SEEN. `disabled` is
// for a machine that is off this week, and a host that simply vanished from
// the browser would be indistinguishable from one whose entry was deleted —
// which is the opposite of what the setting is for. The caller shows them as
// their own row state; no client is created and no connection is attempted.
func DisabledFromConfig(cfg *config.Config) []string {
	if cfg == nil || !features.IsEnabled(features.SidecarRemoteHosts.Name) {
		return nil
	}
	var ids []string
	for _, entry := range cfg.Hosts.List {
		if !entry.Disabled || strings.TrimSpace(entry.Target) == "" {
			continue
		}
		id := strings.TrimSpace(entry.ID)
		if id == "" {
			id = strings.TrimSpace(entry.Target)
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Registry owns the set of live host clients.
//
// Its job is to make "which machines are we watching" a single answer that
// survives a config reload: Sync computes the difference and starts or stops
// only what changed, so a host whose settings did not move keeps its stream
// and its rows rather than blinking out and reconnecting.
type Registry struct {
	mu           sync.Mutex
	clients      map[string]*Client
	cancels      map[string]context.CancelFunc
	incarnations map[string]uint64
	updates      chan Update
	options      ClientOptions
	dir          string
	stopped      bool

	// forwardMu and forwarders guard the merged stream's lifetime: Stop waits
	// for every forwarder to finish before closing the channel they send on.
	forwardMu     sync.RWMutex
	forwarders    sync.WaitGroup
	updatesClosed bool
}

// nextHostIncarnation is process-wide so stopping and recreating a Registry
// cannot make a queued update collide with a replacement client's identity.
var nextHostIncarnation atomic.Uint64

// NewRegistry builds an empty registry. opts is applied to every client;
// leave it zero for production defaults.
func NewRegistry(opts ClientOptions) *Registry {
	return &Registry{
		clients:      make(map[string]*Client),
		cancels:      make(map[string]context.CancelFunc),
		incarnations: make(map[string]uint64),
		// Buffered so a burst of host transitions cannot stall a client's
		// reader loop while the UI is mid-frame.
		updates: make(chan Update, 64),
		options: opts,
	}
}

// Updates is the merged stream from every host.
func (r *Registry) Updates() <-chan Update { return r.updates }

// Sync reconciles the running clients against the registered hosts.
func (r *Registry) Sync(ctx context.Context, hosts []Host) {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return
	}
	wanted := make(map[string]Host, len(hosts))
	for _, host := range hosts {
		wanted[host.ID] = host
	}

	var stopping []context.CancelFunc
	var stoppingClients []*Client
	for id, client := range r.clients {
		host, keep := wanted[id]
		if keep && host.Same(client.Host()) {
			continue
		}
		// Either gone, or its settings changed — a changed target is a
		// different machine as far as the stream is concerned.
		stopping = append(stopping, r.cancels[id])
		stoppingClients = append(stoppingClients, client)
		client.Close()
		delete(r.clients, id)
		delete(r.cancels, id)
		delete(r.incarnations, id)
	}

	var starting []*Client
	var contexts []context.Context
	var incarnations []uint64
	for id, host := range wanted {
		if _, running := r.clients[id]; running {
			continue
		}
		options := r.options
		if options.ControlDir == "" {
			options.ControlDir = r.controlDirLocked(id)
		}
		client := NewClient(host, options)
		clientCtx, cancel := context.WithCancel(ctx)
		r.clients[id] = client
		r.cancels[id] = cancel
		incarnation := nextHostIncarnation.Add(1)
		r.incarnations[id] = incarnation
		starting = append(starting, client)
		contexts = append(contexts, clientCtx)
		incarnations = append(incarnations, incarnation)
	}
	r.mu.Unlock()
	for _, cancel := range stopping {
		if cancel != nil {
			cancel()
		}
	}
	// A retargeted host reuses its per-ID control path. Do not let the new
	// client create a master there until the old client's bounded -O exit has
	// completed, or late cleanup can kill the replacement connection.
	for _, client := range stoppingClients {
		client.waitTransportStopped()
	}

	// Publish the initial health for every host being started, so a
	// registered machine appears the moment it is registered rather than when
	// its first connection resolves. An ssh dial to a machine that is off runs
	// to a full connect timeout; without this, that host is invisible until it
	// fails, which reads as "Sidecar forgot about it".
	for _, client := range starting {
		client.publish(Update{HostID: client.host.ID, Health: client.Health()})
	}

	for i, client := range starting {
		r.forwarders.Add(1)
		go client.Run(contexts[i])
		go r.forward(client, incarnations[i])
	}
}

// controlDirLocked gives each host its own private directory for its ssh
// control socket. Per host rather than shared, so one host's stuck master
// cannot block another's.
//
// The root is under /tmp rather than os.MkdirTemp's default, and that is not a
// preference. A unix socket path is capped near 104 bytes, and macOS sets
// TMPDIR to /var/folders/<2>/<28>/T/ — 49 characters before anything of ours
// is added. With a per-run random suffix and a host directory on top, ssh
// fails with `unix_listener: path too long`, which surfaces as a host that is
// simply unreachable with no hint that the path is the problem. Observed, not
// anticipated.
func (r *Registry) controlDirLocked(id string) string {
	if r.dir == "" {
		dir, err := os.MkdirTemp("/tmp", "sc-hosts-")
		if err != nil {
			// Fall back rather than lose the feature: a long path still works
			// for hosts whose sanitised ID is short enough.
			dir, err = os.MkdirTemp("", "sc-hosts-")
			if err != nil {
				return ""
			}
		}
		r.dir = dir
	}
	// The ID reaches a filesystem path, so anything that is not plainly safe
	// is replaced rather than escaped. Collisions between two sanitised names
	// are harmless: the directory holds only a socket.
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, id)
	path := r.dir + "/" + safe
	_ = os.MkdirAll(path, 0o700)
	return path
}

func (r *Registry) forward(client *Client, incarnation uint64) {
	defer r.forwarders.Done()
	for update := range client.Updates() {
		update.Incarnation = incarnation
		r.forwardMu.RLock()
		if !r.updatesClosed {
			select {
			case r.updates <- update:
			default:
				// Dropped rather than blocked: the newest state is what
				// matters, and a stalled forward would freeze that host's
				// reader loop.
			}
		}
		r.forwardMu.RUnlock()
	}
}

// Incarnation returns the concrete running client identity for id.
func (r *Registry) Incarnation(id string) (uint64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	incarnation, ok := r.incarnations[id]
	return incarnation, ok
}

// Clients returns the live clients, ordered by host ID.
func (r *Registry) Clients() []*Client {
	r.mu.Lock()
	defer r.mu.Unlock()
	clients := make([]*Client, 0, len(r.clients))
	for _, client := range r.clients {
		clients = append(clients, client)
	}
	sort.Slice(clients, func(a, b int) bool { return clients[a].host.ID < clients[b].host.ID })
	return clients
}

// Client returns one host's client.
func (r *Registry) Client(id string) (*Client, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	client, ok := r.clients[id]
	return client, ok
}

// RunSidecar runs a sidecar verb on one registered host, decoding its --json
// result into out. See Client.RunSidecar; this only resolves the host, so that
// "there is no such host" and "that host is not connected" are one refusal
// shape rather than two unrelated ones at the call site.
func (r *Registry) RunSidecar(ctx context.Context, hostID string, args []string, out any) error {
	client, ok := r.Client(hostID)
	if !ok {
		return &RunError{
			Failure: FailUnavailable, HostID: hostID, Args: args, ExitCode: -1,
			Detail: "no host is registered as " + hostID,
		}
	}
	return client.RunSidecar(ctx, args, out)
}

// MarkStaleIfQuiet ages every connected host, returning true if any moved.
func (r *Registry) MarkStaleIfQuiet() bool {
	changed := false
	for _, client := range r.Clients() {
		if client.MarkStaleIfQuiet() {
			changed = true
		}
	}
	return changed
}

// Stop shuts every client down.
func (r *Registry) Stop() {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return
	}
	r.stopped = true
	cancels := make([]context.CancelFunc, 0, len(r.cancels))
	for _, cancel := range r.cancels {
		cancels = append(cancels, cancel)
	}
	clients := make([]*Client, 0, len(r.clients))
	for _, client := range r.clients {
		clients = append(clients, client)
	}
	dir := r.dir
	r.clients = make(map[string]*Client)
	r.cancels = make(map[string]context.CancelFunc)
	r.mu.Unlock()

	for _, client := range clients {
		client.Close()
	}
	for _, cancel := range cancels {
		if cancel != nil {
			cancel()
		}
	}
	// Close the merged stream so a consumer blocked on Updates() ends rather
	// than parking for the life of the process. It is closed after the
	// clients, and forward() is the only sender, so nothing can be mid-send.
	r.closeUpdates()
	// Client.Close starts each production ControlMaster teardown concurrently,
	// so N configured hosts cost one bounded shutdown window rather than N.
	// Join before removing the socket root: after that, ssh -O exit can no
	// longer address a master that still needs to be reaped.
	for _, client := range clients {
		client.waitTransportStopped()
	}
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
}

// closeUpdates ends the merged stream once, guarding the forwarders.
func (r *Registry) closeUpdates() {
	// Wait BEFORE taking the lock. Waiting while holding it deadlocks against
	// any forwarder still in flight, because a forwarder takes the read side
	// to send.
	r.forwarders.Wait()
	r.forwardMu.Lock()
	defer r.forwardMu.Unlock()
	if !r.updatesClosed {
		r.updatesClosed = true
		close(r.updates)
	}
}

// ProjectResults converts one host's snapshot into the same inventory type the
// local collector produces.
//
// This is the join. A remote row becomes an ordinary workspaceinventory
// workspace carrying a HostID, so every projection downstream — the catalog,
// the board, the list, the lane grouping, the pin logic — works on it without
// knowing it came from another machine. The alternative, a parallel remote row
// type with its own rendering, is how two surfaces drift apart.
func ProjectResults(hostID string, snapshot hostproto.Snapshot, stale bool) []workspaceinventory.ProjectResult {
	results := make([]workspaceinventory.ProjectResult, 0, len(snapshot.Projects))
	for _, project := range snapshot.Projects {
		result := workspaceinventory.ProjectResult{
			// The key is host-scoped. Two machines with the same checkout path
			// are two different projects, and keying on the path alone would
			// merge them into one row set.
			ProjectKey:  ScopedKey(hostID, project.Key),
			ProjectName: project.Name,
			ProjectRoot: project.Root,
			ObservedAt:  snapshot.ObservedAt,
		}
		if project.Err != "" {
			result.Err = remoteError(project.Err)
		}
		for _, item := range project.Items {
			result.Workspaces = append(result.Workspaces, workspace(hostID, item, stale))
		}
		results = append(results, result)
	}
	return results
}

// ScopedKey namespaces a remote identifier by its host.
func ScopedKey(hostID, key string) string { return hostID + "\x1f" + key }

// SplitScopedKey reverses ScopedKey, reporting whether the key was scoped.
func SplitScopedKey(key string) (hostID, rest string, ok bool) {
	if index := strings.IndexByte(key, '\x1f'); index >= 0 {
		return key[:index], key[index+1:], true
	}
	return "", key, false
}

func workspace(hostID string, item hostproto.Item, stale bool) workspaceinventory.Workspace {
	ws := workspaceinventory.Workspace{
		ID:          ScopedKey(hostID, item.ID),
		HostID:      hostID,
		ProjectKey:  ScopedKey(hostID, item.ProjectKey),
		ProjectName: item.ProjectName,
		ProjectRoot: item.ProjectRoot,
		Kind:        workspaceinventory.Kind(item.Kind),
		Key:         item.Key,
		Name:        item.Name,
		Path:        item.Path,
		Branch:      item.Branch,
		TaskID:      item.TaskID,
		TmuxName:    item.Session,
		PaneID:      item.PaneID,
		Provider:    item.Provider,
		Live:        item.Live,
		Ambiguous:   item.Ambiguous,
		IsMain:      item.IsMain,
		ObservedAt:  item.ObservedAt,
		Preview:     item.Preview,
	}
	if item.Kind == string(workspaceinventory.KindWorktree) {
		ws.Plain = item.Agent == nil
	}
	if item.Agent != nil {
		ws.Presentation = presentation(*item.Agent)
		if stale {
			// A stale host's rows keep their lane — last-known is still the
			// best answer — but must never claim to be current. Attention is
			// dropped with it: a notification badge sourced from data that may
			// be a minute old is worse than none.
			ws.Presentation.Freshness = agentstatus.FreshnessStale
			ws.Presentation.Attention = false
		}
	}
	return ws
}

func presentation(p hostproto.Presentation) agentstatus.Presentation {
	return agentstatus.Presentation{
		Lane:       agentstatus.LaneID(p.Lane),
		Icon:       p.Icon,
		Label:      p.Label,
		Attention:  p.Attention,
		Evidence:   p.Evidence,
		ChangedAt:  p.ChangedAt,
		CapturedAt: p.CapturedAt,
		Health:     p.Health,
		Semantic:   p.Semantic,
		Freshness:  agentstatus.Freshness(p.Freshness),
		Inferred:   p.Inferred,
	}
}

// remoteError carries a host's error text without pretending it happened here.
type remoteError string

func (e remoteError) Error() string { return string(e) }
