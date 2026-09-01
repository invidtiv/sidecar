package overview

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func TestHostCatalogEmptyWhenNoRegistry(t *testing.T) {
	m := New(workspaceinventory.Collector{})
	if got := m.HostCatalog(); got != nil {
		t.Fatalf("HostCatalog() = %+v, want nil with no registry", got)
	}

	// hostModel folds rows in without a registry. The accessor must still
	// return nothing — those rows are test fixtures, not a running catalog.
	seeded := hostModel(t, "aerie", hosts.Health{State: hosts.StateOnline}, catalogSnapshot())
	if got := seeded.HostCatalog(); got != nil {
		t.Fatalf("HostCatalog() = %+v with host data but no registry, want nil", got)
	}
}

func TestHostCatalogOnlineHost(t *testing.T) {
	m := hostModel(t, "aerie", hosts.Health{State: hosts.StateOnline, Detail: "live"}, catalogSnapshot())
	attachCatalogRegistry(t, m)
	m.hostIncarnations = map[string]uint64{"aerie": 7}

	got := m.HostCatalog()
	if len(got) != 1 {
		t.Fatalf("HostCatalog() = %+v, want one host", got)
	}
	entry := got[0]
	if entry.ID != "aerie" {
		t.Errorf("ID = %q, want aerie", entry.ID)
	}
	if entry.Health.State != hosts.StateOnline || entry.Health.Detail != "live" {
		t.Errorf("Health = %+v, want online/live", entry.Health)
	}
	if entry.Incarnation != 7 {
		t.Errorf("Incarnation = %d, want 7", entry.Incarnation)
	}
	if len(entry.Projects) != 1 {
		t.Fatalf("Projects = %+v, want one", entry.Projects)
	}
	project := entry.Projects[0]
	if project.Name != "Sidecar" || project.Root != "/home/me/sidecar" {
		t.Errorf("project name/root = %+v", project)
	}
	if strings.Contains(project.Key, "\x1f") {
		t.Errorf("ProjectKey is still scoped: %q", project.Key)
	}
	if project.Key != "/home/me/sidecar" {
		t.Errorf("ProjectKey = %q, want unscoped /home/me/sidecar", project.Key)
	}
	stored := m.hostResults["aerie"][0].ProjectKey
	if stored != hosts.ScopedKey("aerie", project.Key) {
		t.Errorf("unscoped key %q does not round-trip through ScopedKey to %q", project.Key, stored)
	}
	if len(project.Workspaces) != 3 {
		t.Fatalf("Workspaces = %d, want shells and worktrees from hostResults", len(project.Workspaces))
	}
	var sawWorktree, sawShell bool
	for _, ws := range project.Workspaces {
		switch ws.Kind {
		case workspaceinventory.KindWorktree:
			sawWorktree = true
		case workspaceinventory.KindShell:
			sawShell = true
		}
	}
	if !sawWorktree || !sawShell {
		t.Errorf("catalog dropped in-memory items: worktree=%v shell=%v", sawWorktree, sawShell)
	}

	// Callers must not mutate registry state through the result.
	project.Workspaces[0].Name = "mutated"
	entry.Projects[0].Name = "mutated"
	if m.hostResults["aerie"][0].Workspaces[0].Name == "mutated" {
		t.Error("mutating catalog workspaces changed hostResults")
	}
	if m.hostResults["aerie"][0].ProjectName == "mutated" {
		t.Error("mutating catalog project name changed hostResults")
	}
}

func TestHostCatalogDisconnectedAndStaleStillYieldRows(t *testing.T) {
	t.Run("stale", func(t *testing.T) {
		m := hostModel(t, "aerie", hosts.Health{State: hosts.StateStale, Detail: "quiet"}, catalogSnapshot())
		attachCatalogRegistry(t, m)
		got := m.HostCatalog()
		if len(got) != 1 || got[0].Health.State != hosts.StateStale {
			t.Fatalf("stale catalog = %+v", got)
		}
		if len(got[0].Projects) != 1 || got[0].Projects[0].Key != "/home/me/sidecar" {
			t.Fatalf("stale host dropped rows: %+v", got[0].Projects)
		}
	})
	t.Run("disconnected with leftover rows", func(t *testing.T) {
		m := hostModel(t, "aerie", hosts.Health{State: hosts.StateUnreachable, Detail: "ssh failed"}, nil)
		m.hostResults["aerie"] = hosts.ProjectResults("aerie", *catalogSnapshot(), true)
		attachCatalogRegistry(t, m)
		got := m.HostCatalog()
		if len(got) != 1 || got[0].Health.State != hosts.StateUnreachable {
			t.Fatalf("disconnected catalog = %+v", got)
		}
		if len(got[0].Projects) != 1 || got[0].Projects[0].Name != "Sidecar" {
			t.Fatalf("disconnected host dropped leftover rows: %+v", got[0].Projects)
		}
		if got[0].Projects[0].Key != "/home/me/sidecar" {
			t.Errorf("disconnected ProjectKey = %q, want unscoped", got[0].Projects[0].Key)
		}
	})
}

func TestHostCatalogIncarnationMatchesRegistry(t *testing.T) {
	m := hostModel(t, "aerie", hosts.Health{State: hosts.StateOnline}, catalogSnapshot())
	registry := hosts.NewRegistry(hosts.ClientOptions{
		Dial:       func(context.Context) (*hosts.Conn, error) { return nil, context.Canceled },
		MinBackoff: time.Hour,
	})
	t.Cleanup(registry.Stop)
	m.hostRegistry = registry
	registry.Sync(context.Background(), []hosts.Host{{ID: "aerie", Target: "aerie.local"}})
	want, ok := registry.Incarnation("aerie")
	if !ok || want == 0 {
		t.Fatalf("registry incarnation = %d found=%v", want, ok)
	}
	// Production copies registry.Incarnation into hostIncarnations at Sync.
	m.hostIncarnations = map[string]uint64{"aerie": want}

	got := m.HostCatalog()
	if len(got) != 1 {
		t.Fatalf("HostCatalog() = %+v", got)
	}
	if got[0].Incarnation != want {
		t.Errorf("catalog incarnation = %d, registry = %d", got[0].Incarnation, want)
	}

	// When the map has not been filled yet, fall back to the registry.
	m.hostIncarnations = nil
	got = m.HostCatalog()
	if len(got) != 1 || got[0].Incarnation != want {
		t.Errorf("fallback incarnation = %+v, want %d", got, want)
	}
}

func attachCatalogRegistry(t *testing.T, m *Model) {
	t.Helper()
	m.hostRegistry = hosts.NewRegistry(hosts.ClientOptions{
		Dial:       func(context.Context) (*hosts.Conn, error) { return nil, context.Canceled },
		MinBackoff: time.Hour,
	})
	t.Cleanup(m.hostRegistry.Stop)
}

func catalogSnapshot() *hostproto.Snapshot {
	return &hostproto.Snapshot{
		Generation: 1,
		ObservedAt: time.Now(),
		Projects: []hostproto.Project{{
			Key: "/home/me/sidecar", Name: "Sidecar", Root: "/home/me/sidecar",
			Items: []hostproto.Item{
				{
					ID: "/home/me/sidecar:worktree:/home/me/sidecar", ProjectKey: "/home/me/sidecar",
					ProjectName: "Sidecar", ProjectRoot: "/home/me/sidecar",
					Kind: "worktree", Key: "/home/me/sidecar", Name: "main", IsMain: true,
				},
				{
					ID: "/home/me/sidecar:worktree:/home/me/sidecar-feature", ProjectKey: "/home/me/sidecar",
					ProjectName: "Sidecar", ProjectRoot: "/home/me/sidecar",
					Kind: "worktree", Key: "/home/me/sidecar-feature", Name: "feature", Branch: "feature",
				},
				{
					ID: "/home/me/sidecar:shell:s1", ProjectKey: "/home/me/sidecar",
					ProjectName: "Sidecar", ProjectRoot: "/home/me/sidecar",
					Kind: "shell", Key: "s1", Name: "Claude pane", Session: "sidecar-claude",
				},
			},
		}},
	}
}
