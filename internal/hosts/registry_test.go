package hosts

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentstatus"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func withFeature(t *testing.T, enabled bool) {
	t.Helper()
	features.Init(&config.Config{})
	features.SetOverride(features.SidecarRemoteHosts.Name, enabled)
	t.Cleanup(func() { features.SetOverride(features.SidecarRemoteHosts.Name, features.SidecarRemoteHosts.Default) })
}

// TestFromConfigIsEmptyWhenTheFeatureIsOff is the rollback guarantee. With the
// flag off there must be no host, so there is no client, no ssh child and no
// remote row — the local path cannot differ because nothing is reachable.
func TestFromConfigIsEmptyWhenTheFeatureIsOff(t *testing.T) {
	withFeature(t, false)
	cfg := &config.Config{Hosts: config.HostsConfig{List: []config.HostConfig{{ID: "a", Target: "a"}}}}
	if hosts := FromConfig(cfg); len(hosts) != 0 {
		t.Errorf("hosts = %v with the feature off, want none", hosts)
	}
}

func TestFromConfigReadsRegisteredHosts(t *testing.T) {
	withFeature(t, true)
	cfg := &config.Config{Hosts: config.HostsConfig{List: []config.HostConfig{
		{ID: "mac-mini", Target: "mini.local", Binary: "/opt/sidecar", Config: "/etc/sc.json"},
		{Target: "bare-target"}, // ID defaults to the target
		{Target: "  "},          // blank target is not a host
		{ID: "off", Target: "x", Disabled: true},
		{ID: "mac-mini", Target: "duplicate"}, // first registration keeps the name
	}}}
	hosts := FromConfig(cfg)
	if len(hosts) != 2 {
		t.Fatalf("hosts = %+v, want 2", hosts)
	}
	// Sorted by ID, so the order a user sees does not depend on map iteration
	// or on how they happened to write the file.
	if hosts[0].ID != "bare-target" || hosts[1].ID != "mac-mini" {
		t.Errorf("hosts not ordered by ID: %+v", hosts)
	}
	if hosts[0].Target != "bare-target" {
		t.Errorf("ID did not default to the target: %+v", hosts[0])
	}
	if hosts[1].RemoteBinary != "/opt/sidecar" || hosts[1].RemoteConfig != "/etc/sc.json" {
		t.Errorf("per-host settings lost: %+v", hosts[1])
	}
	if hosts[1].Target != "mini.local" {
		t.Errorf("a duplicate ID overwrote the first registration: %+v", hosts[1])
	}
}

func TestHostSameIgnoresNothingThatMatters(t *testing.T) {
	base := Host{ID: "a", Target: "t", RemoteBinary: "b", RemoteConfig: "c", Env: []string{"K=V"}}
	if !base.Same(base) {
		t.Fatal("a host is not the same as itself")
	}
	for name, other := range map[string]Host{
		"target":  {ID: "a", Target: "other", RemoteBinary: "b", RemoteConfig: "c", Env: []string{"K=V"}},
		"binary":  {ID: "a", Target: "t", RemoteBinary: "other", RemoteConfig: "c", Env: []string{"K=V"}},
		"config":  {ID: "a", Target: "t", RemoteBinary: "b", RemoteConfig: "other", Env: []string{"K=V"}},
		"env":     {ID: "a", Target: "t", RemoteBinary: "b", RemoteConfig: "c", Env: []string{"K=OTHER"}},
		"env len": {ID: "a", Target: "t", RemoteBinary: "b", RemoteConfig: "c"},
		"id":      {ID: "other", Target: "t", RemoteBinary: "b", RemoteConfig: "c", Env: []string{"K=V"}},
	} {
		if base.Same(other) {
			t.Errorf("%s change was not noticed", name)
		}
	}
}

// TestSyncKeepsUnchangedHostsConnected. A config reload must not blink every
// host's rows out and reconnect them.
func TestSyncKeepsUnchangedHostsConnected(t *testing.T) {
	registry := NewRegistry(ClientOptions{
		Dial:       func(context.Context) (*Conn, error) { return nil, context.Canceled },
		MinBackoff: time.Hour,
	})
	defer registry.Stop()
	ctx := context.Background()

	registry.Sync(ctx, []Host{{ID: "a", Target: "a"}, {ID: "b", Target: "b"}})
	first, ok := registry.Client("a")
	if !ok {
		t.Fatal("host a was not started")
	}
	firstAIncarnation, ok := registry.Incarnation("a")
	if !ok {
		t.Fatal("host a has no incarnation")
	}
	firstBIncarnation, ok := registry.Incarnation("b")
	if !ok {
		t.Fatal("host b has no incarnation")
	}

	// Same registration for a, changed target for b, c added, and nothing
	// removed.
	registry.Sync(ctx, []Host{{ID: "a", Target: "a"}, {ID: "b", Target: "moved"}, {ID: "c", Target: "c"}})
	again, ok := registry.Client("a")
	if !ok || again != first {
		t.Error("an unchanged host was restarted")
	}
	againAIncarnation, _ := registry.Incarnation("a")
	if againAIncarnation != firstAIncarnation {
		t.Errorf("unchanged host incarnation = %d, want %d", againAIncarnation, firstAIncarnation)
	}
	changed, ok := registry.Client("b")
	if !ok || changed.Host().Target != "moved" {
		t.Errorf("a changed host did not restart: %+v", changed)
	}
	changedBIncarnation, _ := registry.Incarnation("b")
	if changedBIncarnation == firstBIncarnation {
		t.Errorf("retargeted host retained incarnation %d", firstBIncarnation)
	}
	if len(registry.Clients()) != 3 {
		t.Errorf("clients = %d, want 3", len(registry.Clients()))
	}

	// Dropping a host stops it.
	registry.Sync(ctx, []Host{{ID: "a", Target: "a"}})
	if len(registry.Clients()) != 1 {
		t.Errorf("clients = %d after removing two, want 1", len(registry.Clients()))
	}
}

func TestRegistryStopIsIdempotentAndClearsClients(t *testing.T) {
	registry := NewRegistry(ClientOptions{
		Dial:       func(context.Context) (*Conn, error) { return nil, context.Canceled },
		MinBackoff: time.Hour,
	})
	registry.Sync(context.Background(), []Host{{ID: "a", Target: "a"}})
	registry.Stop()
	registry.Stop()
	if len(registry.Clients()) != 0 {
		t.Error("Stop left clients behind")
	}
	// Sync after Stop must not resurrect anything.
	registry.Sync(context.Background(), []Host{{ID: "b", Target: "b"}})
	if len(registry.Clients()) != 0 {
		t.Error("Sync started a client after Stop")
	}
}

func remoteSnapshot() hostproto.Snapshot {
	return hostproto.Snapshot{
		Generation: 3,
		ObservedAt: time.Now(),
		Projects: []hostproto.Project{{
			Key: "/home/me/proj", Name: "proj", Root: "/home/me/proj",
			Items: []hostproto.Item{
				{
					ID: "/home/me/proj:shell:s1", ProjectKey: "/home/me/proj", ProjectName: "proj",
					Kind: "shell", Key: "s1", Name: "Claude pane", Session: "proj-claude", PaneID: "%4",
					Provider: "claude", Live: true, Preview: "screen text",
					Agent: &hostproto.Presentation{Lane: "blocked", Label: "blocked", Attention: true, Freshness: "current"},
				},
				{
					ID: "/home/me/proj:worktree:/home/me/proj", ProjectKey: "/home/me/proj",
					Kind: "worktree", Key: "/home/me/proj", Name: "proj", IsMain: true, Live: true,
				},
			},
		}},
	}
}

// TestProjectResultsProducesOrdinaryInventoryRows is the join the whole design
// rests on: a remote row must be an ordinary workspace so every downstream
// projection works on it unchanged.
func TestProjectResultsProducesOrdinaryInventoryRows(t *testing.T) {
	results := ProjectResults("mac-mini", remoteSnapshot(), false)
	if len(results) != 1 || len(results[0].Workspaces) != 2 {
		t.Fatalf("results = %+v", results)
	}
	shell := results[0].Workspaces[0]
	if !shell.Remote() || shell.HostID != "mac-mini" {
		t.Errorf("row is not marked remote: %+v", shell)
	}
	if !shell.HasAgent() || shell.Presentation.Lane != agentstatus.LaneBlocked {
		t.Errorf("presentation lost: %+v", shell.Presentation)
	}
	if shell.TmuxName != "proj-claude" {
		t.Errorf("session lost; a viewer cannot open a control channel: %+v", shell)
	}
	if shell.Preview != "screen text" {
		t.Errorf("preview lost: %q", shell.Preview)
	}
	// Item() is what the catalog and the list actually consume.
	item := shell.Item()
	if !item.Remote() || item.Agent == nil || item.Preview == "" {
		t.Errorf("catalog row lost remote identity or data: %+v", item)
	}

	// A main worktree with no agent is a plain workspace, exactly as locally.
	worktree := results[0].Workspaces[1]
	if worktree.HasAgent() {
		t.Errorf("an agentless worktree was given agent semantics: %+v", worktree)
	}
	if !worktree.IsMain {
		t.Error("worktree identity lost")
	}
}

// TestProjectResultsScopesKeysByHost. Two machines with the same checkout path
// are two projects; keying on the path alone would merge their rows.
func TestProjectResultsScopesKeysByHost(t *testing.T) {
	a := ProjectResults("host-a", remoteSnapshot(), false)
	b := ProjectResults("host-b", remoteSnapshot(), false)
	if a[0].ProjectKey == b[0].ProjectKey {
		t.Error("two hosts produced the same project key")
	}
	if a[0].Workspaces[0].ID == b[0].Workspaces[0].ID {
		t.Error("two hosts produced the same workspace ID")
	}
	hostID, rest, ok := SplitScopedKey(a[0].Workspaces[0].ID)
	if !ok || hostID != "host-a" || !strings.Contains(rest, "shell:s1") {
		t.Errorf("scoped key does not round-trip: %q %q %v", hostID, rest, ok)
	}
	if _, _, scoped := SplitScopedKey("/plain/local/key"); scoped {
		t.Error("a local key was reported as host-scoped")
	}
}

// TestStaleRowsKeepTheirLaneButNotTheirClaim. Last-known state is more useful
// than a blank host, as long as it never claims to be current — and an
// attention badge sourced from possibly-minute-old data is worse than none.
func TestStaleRowsKeepTheirLaneButNotTheirClaim(t *testing.T) {
	fresh := ProjectResults("h", remoteSnapshot(), false)[0].Workspaces[0]
	stale := ProjectResults("h", remoteSnapshot(), true)[0].Workspaces[0]

	if stale.Presentation.Lane != fresh.Presentation.Lane {
		t.Errorf("a stale row lost its lane: %v vs %v", stale.Presentation.Lane, fresh.Presentation.Lane)
	}
	if stale.Presentation.Freshness != agentstatus.FreshnessStale {
		t.Errorf("a stale row claims freshness %q", stale.Presentation.Freshness)
	}
	if stale.Presentation.Attention {
		t.Error("a stale row still demands attention")
	}
	if !fresh.Presentation.Attention {
		t.Error("a fresh row lost its attention flag")
	}
}

func TestProjectResultsCarriesAProjectError(t *testing.T) {
	snapshot := remoteSnapshot()
	snapshot.Projects[0].Err = "configured project missing"
	results := ProjectResults("h", snapshot, false)
	if results[0].Err == nil || !strings.Contains(results[0].Err.Error(), "missing") {
		t.Errorf("project error lost: %v", results[0].Err)
	}
}

// TestLocalWorkspacesAreNotRemote guards the discriminator every action site
// will check.
func TestLocalWorkspacesAreNotRemote(t *testing.T) {
	var local workspaceinventory.Workspace
	if local.Remote() || local.Item().Remote() {
		t.Error("a locally collected workspace reports itself remote")
	}
}
