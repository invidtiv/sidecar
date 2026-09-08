package mobile

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentstatus"
	"github.com/marcus/sidecar/internal/mobileproto"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func TestCatalogAttachmentVerdictsFailClosed(t *testing.T) {
	now := time.Date(2026, 9, 7, 18, 0, 0, 0, time.UTC)
	project := CatalogProject{Label: "Sidecar", Result: workspaceinventory.ProjectResult{
		ProjectKey: "/repo", ProjectName: "Sidecar", ObservedAt: now,
		Workspaces: []workspaceinventory.Workspace{
			catalogShell("ready", "Ready", "sidecar-sh-ready", "%1", now),
			catalogShell("duplicate-a", "Duplicate A", "sidecar-sh-duplicate", "%2", now),
			catalogShell("duplicate-b", "Duplicate B", "sidecar-sh-duplicate", "%3", now),
			{ID: "ambiguous", ProjectKey: "/repo", Kind: workspaceinventory.KindShell, Name: "Ambiguous", TmuxName: "sidecar-sh-ambiguous", Ambiguous: true, ObservedAt: now, CreatedAt: now},
			{ID: "unavailable", ProjectKey: "/repo", Kind: workspaceinventory.KindShell, Name: "Unavailable", TmuxName: "sidecar-sh-gone", ObservedAt: now, CreatedAt: now},
			{ID: "unknown", ProjectKey: "/repo", Kind: workspaceinventory.KindShell, Name: "Unknown", TmuxName: "sidecar-sh-unknown", PaneID: "%5", Live: true, ObservedAt: now},
			{ID: "worktree", ProjectKey: "/repo", Kind: workspaceinventory.KindWorktree, Name: "Worktree", PaneID: "%6", TmuxName: "sidecar-wt", Live: true, ObservedAt: now},
		},
	}}
	stale := CatalogProject{Label: "Stale", Stale: true, Result: workspaceinventory.ProjectResult{ProjectKey: "/stale", Workspaces: []workspaceinventory.Workspace{catalogShell("stale", "Stale", "sidecar-sh-stale", "%7", now)}}}
	missing := CatalogProject{Label: "Missing", Result: workspaceinventory.ProjectResult{ProjectKey: "/missing", ProjectName: "Missing", Err: errors.New("configured project missing")}}
	snapshot := mustCatalog(t, CatalogInput{ObservedAt: now, Projects: []CatalogProject{project, stale, missing}}, mobileproto.CatalogQuery{})

	if snapshot.HubID != "hub" || snapshot.OwnerHostID != "local:test" || snapshot.OwnerConfigGeneration != "config" {
		t.Fatalf("catalog authority = %+v", snapshot)
	}
	rows := catalogRowsByID(snapshot)
	assertCatalogVerdict(t, rows["ready"], AttachReady, "", true)
	assertCatalogVerdict(t, rows["ambiguous"], AttachAmbiguous, mobileproto.ErrorAmbiguous, false)
	assertCatalogVerdict(t, rows["unavailable"], AttachUnavailable, mobileproto.ErrorNotFound, false)
	assertCatalogVerdict(t, rows["unknown"], AttachUnknown, mobileproto.ErrorIdentityChanged, false)
	assertCatalogVerdict(t, rows["worktree"], AttachUnsupported, mobileproto.ErrorUnsupported, false)
	assertCatalogVerdict(t, rows["stale"], AttachStale, mobileproto.ErrorIdentityChanged, false)
	for _, id := range []string{"duplicate-a", "duplicate-b"} {
		assertCatalogVerdict(t, rows[id], AttachAmbiguous, mobileproto.ErrorAmbiguous, false)
		if rows[id].Target != "" {
			t.Fatalf("duplicate target leaked for %s: %+v", id, rows[id])
		}
	}
	if len(snapshot.Failures) != 1 || snapshot.Failures[0].ID != "/missing" {
		t.Fatalf("failures = %+v", snapshot.Failures)
	}
	unsupported := mustCatalog(t, CatalogInput{ObservedAt: now, Projects: []CatalogProject{project, stale, missing}}, mobileproto.CatalogQuery{States: []string{AttachUnsupported}})
	if ids := catalogRowIDs(unsupported); len(ids) != 1 || ids[0] != "worktree" {
		t.Fatalf("unsupported filter = %v", ids)
	}
}

func TestCatalogUsesSharedSearchSortGroupAndFilters(t *testing.T) {
	now := time.Date(2026, 9, 7, 18, 0, 0, 0, time.UTC)
	working := catalogShell("working", "Zulu", "sidecar-sh-working", "%1", now)
	working.Provider = "codex"
	working.Presentation = agentstatus.Presentation{Lane: agentstatus.LaneWorking, Label: "working", ChangedAt: now.Add(-time.Minute), Semantic: true}
	blocked := catalogShell("blocked", "Beta", "sidecar-sh-blocked", "%2", now)
	blocked.Provider = "claude"
	blocked.Presentation = agentstatus.Presentation{Lane: agentstatus.LaneBlocked, Label: "blocked", ChangedAt: now.Add(-2 * time.Minute), Semantic: true, Attention: true}
	idle := catalogShell("idle", "Alpha", "sidecar-sh-idle", "%3", now)
	idle.Provider = "codex"
	idle.Presentation = agentstatus.Presentation{Lane: agentstatus.LaneIdle, Label: "idle", ChangedAt: now.Add(-3 * time.Minute), Semantic: true}
	live := catalogShell("live", "Gamma", "sidecar-sh-live", "%4", now)
	for _, workspace := range []*workspaceinventory.Workspace{&working, &live} {
		workspace.ProjectKey = "/one"
	}
	for _, workspace := range []*workspaceinventory.Workspace{&blocked, &idle} {
		workspace.ProjectKey = "/two"
	}

	input := CatalogInput{ObservedAt: now, Hosts: []mobileproto.CatalogHost{{ID: "local:test", Name: "test", State: "online", Local: true}}, Projects: []CatalogProject{
		{Label: "One", Order: 0, Result: workspaceinventory.ProjectResult{ProjectKey: "/one", Workspaces: []workspaceinventory.Workspace{working, live}}},
		{Label: "Two", Order: 1, Result: workspaceinventory.ProjectResult{ProjectKey: "/two", Workspaces: []workspaceinventory.Workspace{blocked, idle}}},
	}}
	tests := []struct {
		name  string
		query mobileproto.CatalogQuery
		want  []string
	}{
		{name: "activity", query: mobileproto.CatalogQuery{Sort: "activity"}, want: []string{"blocked", "working", "live", "idle"}},
		{name: "project", query: mobileproto.CatalogQuery{Sort: "project"}, want: []string{"working", "live", "blocked", "idle"}},
		{name: "recent", query: mobileproto.CatalogQuery{Sort: "recent"}, want: []string{"working", "blocked", "idle", "live"}},
		{name: "name", query: mobileproto.CatalogQuery{Sort: "name"}, want: []string{"idle", "blocked", "live", "working"}},
		{name: "search host", query: mobileproto.CatalogQuery{Search: "local:test"}, want: []string{"blocked", "working", "live", "idle"}},
		{name: "provider", query: mobileproto.CatalogQuery{Providers: []string{"CODEX"}}, want: []string{"working", "idle"}},
		{name: "status", query: mobileproto.CatalogQuery{States: []string{"blocked"}}, want: []string{"blocked"}},
		{name: "attachment", query: mobileproto.CatalogQuery{States: []string{"ready"}, Search: "gamma"}, want: []string{"live"}},
		{name: "host", query: mobileproto.CatalogQuery{Hosts: []string{"elsewhere"}}, want: []string{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := mustCatalogInput(t, input, test.query)
			if got := catalogRowIDs(snapshot); strings.Join(got, ",") != strings.Join(test.want, ",") {
				t.Fatalf("rows = %v, want %v; sections=%+v", got, test.want, snapshot.Sections)
			}
		})
	}

	first := mustCatalogInput(t, input, mobileproto.CatalogQuery{Sort: "name"})
	input.ObservedAt = input.ObservedAt.Add(time.Minute)
	for i := range input.Projects {
		for j := range input.Projects[i].Result.Workspaces {
			input.Projects[i].Result.Workspaces[j].ObservedAt = input.ObservedAt
		}
	}
	second := mustCatalogInput(t, input, mobileproto.CatalogQuery{Sort: "project"})
	if first.Generation != second.Generation {
		t.Fatalf("query or observation time churned generation: %s != %s", first.Generation, second.Generation)
	}
}

func TestCatalogRejectsUnboundedOrDuplicateInput(t *testing.T) {
	long := strings.Repeat("x", mobileproto.MaxCatalogQueryBytes+1)
	if _, err := QueryCatalog(context.Background(), func(context.Context) (CatalogInput, error) { return CatalogInput{}, nil }, nil, mobileproto.CatalogQuery{Hosts: []string{long}}, CatalogIdentity{}); err == nil {
		t.Fatal("oversized filter was accepted")
	}
	now := time.Now()
	duplicate := catalogShell("duplicate", "One", "one", "%1", now)
	input := CatalogInput{Projects: []CatalogProject{{Result: workspaceinventory.ProjectResult{Workspaces: []workspaceinventory.Workspace{duplicate, duplicate}}}}}
	if _, err := QueryCatalog(context.Background(), func(context.Context) (CatalogInput, error) { return input, nil }, nil, mobileproto.CatalogQuery{}, CatalogIdentity{}); err == nil {
		t.Fatal("duplicate workspace identity was accepted")
	}

	oversized := catalogShell("oversized", strings.Repeat("\x01", mobileproto.MaxLineBytes/4), "oversized", "%2", now)
	input = CatalogInput{
		ObservedAt: now,
		Hosts:      []mobileproto.CatalogHost{{ID: "local:test", Name: "test", State: "online", Local: true}},
		Projects:   []CatalogProject{{Result: workspaceinventory.ProjectResult{Workspaces: []workspaceinventory.Workspace{oversized}}}},
	}
	_, err := QueryCatalog(context.Background(), func(context.Context) (CatalogInput, error) { return input, nil }, nil, mobileproto.CatalogQuery{}, CatalogIdentity{HubID: "hub", OwnerHostID: "local:test", OwnerConfigGeneration: "config"})
	var resolveErr *ResolveError
	if !errors.As(err, &resolveErr) || resolveErr.Code != mobileproto.ErrorOverflow {
		t.Fatalf("oversized serialized catalog error = %v, want overflow", err)
	}
}

func TestCatalogGenerationAndStateFilterUseFinalResolvedVerdict(t *testing.T) {
	now := time.Date(2026, 9, 7, 18, 0, 0, 0, time.UTC)
	workspace := catalogShell("repo:shell:one", "One", "sidecar-sh-one", "%1", now)
	input := CatalogInput{ObservedAt: now, Hosts: []mobileproto.CatalogHost{{ID: "local:test", Name: "test", State: "online", Local: true}}, Projects: []CatalogProject{{Result: workspaceinventory.ProjectResult{Workspaces: []workspaceinventory.Workspace{workspace}}}}}
	identity := CatalogIdentity{HubID: "hub", OwnerHostID: "local:test", OwnerConfigGeneration: "config"}
	resolved := func(pid int) Resolver {
		return func(context.Context, string) (ResolvedTarget, error) {
			return ResolvedTarget{WorkspaceID: filepath.Base(workspace.ProjectKey), WorkspaceKind: "shell", ProjectRoot: workspace.ProjectKey, Session: workspace.TmuxName, Pane: workspace.PaneID,
				ServerPID: pid, SessionID: "$1", SessionCreated: "1700000000", DurableSessionCreated: formatCatalogTime(workspace.CreatedAt), Width: 80, Height: 24}, nil
		}
	}
	query := func(resolver Resolver, catalogQuery mobileproto.CatalogQuery) mobileproto.CatalogSnapshot {
		t.Helper()
		snapshot, err := QueryCatalog(context.Background(), func(context.Context) (CatalogInput, error) { return input, nil }, resolver, catalogQuery, identity)
		if err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	first, replacement := query(resolved(42), mobileproto.CatalogQuery{}), query(resolved(43), mobileproto.CatalogQuery{})
	if first.Generation == replacement.Generation {
		t.Fatal("server-incarnation replacement did not change catalog generation")
	}
	ambiguousResolver := func(context.Context, string) (ResolvedTarget, error) {
		return ResolvedTarget{}, &ResolveError{Code: mobileproto.ErrorAmbiguous, Message: "replacement is ambiguous"}
	}
	ambiguous := query(ambiguousResolver, mobileproto.CatalogQuery{States: []string{AttachAmbiguous}})
	if ambiguous.Total != 1 || catalogRowIDs(ambiguous)[0] != workspace.ID {
		t.Fatalf("final ambiguous filter = %+v", ambiguous)
	}
	if ready := query(ambiguousResolver, mobileproto.CatalogQuery{States: []string{AttachReady}}); ready.Total != 0 {
		t.Fatalf("final ready filter retained failed resolver: %+v", ready)
	}
	changedResolver := func(context.Context, string) (ResolvedTarget, error) {
		return ResolvedTarget{WorkspaceID: "repo", WorkspaceKind: "shell", ProjectRoot: "/replacement", Session: workspace.TmuxName, Pane: workspace.PaneID,
			ServerPID: 42, SessionID: "$1", SessionCreated: "1700000000", DurableSessionCreated: formatCatalogTime(workspace.CreatedAt), Width: 80, Height: 24}, nil
	}
	if stale := query(changedResolver, mobileproto.CatalogQuery{States: []string{AttachStale}}); stale.Total != 1 || catalogRowIDs(stale)[0] != workspace.ID {
		t.Fatalf("final stale filter = %+v", stale)
	}
}

func catalogShell(id, name, session, pane string, now time.Time) workspaceinventory.Workspace {
	return workspaceinventory.Workspace{ID: id, ProjectKey: "/repo", ProjectName: "Repo", Kind: workspaceinventory.KindShell, Name: name, TmuxName: session, PaneID: pane, Live: true, CreatedAt: now.Add(-time.Hour), ObservedAt: now}
}

func mustCatalog(t *testing.T, input CatalogInput, query mobileproto.CatalogQuery) mobileproto.CatalogSnapshot {
	t.Helper()
	return mustCatalogWithIdentity(t, input, query, CatalogIdentity{HubID: "hub", OwnerHostID: "local:test", OwnerConfigGeneration: "config"})
}

func mustCatalogInput(t *testing.T, input CatalogInput, query mobileproto.CatalogQuery) mobileproto.CatalogSnapshot {
	t.Helper()
	return mustCatalogWithIdentity(t, input, query, CatalogIdentity{HubID: "hub", OwnerHostID: "local:test", OwnerConfigGeneration: "config"})
}

func mustCatalogWithIdentity(t *testing.T, input CatalogInput, query mobileproto.CatalogQuery, identity CatalogIdentity) mobileproto.CatalogSnapshot {
	t.Helper()
	if len(input.Hosts) == 0 {
		input.Hosts = []mobileproto.CatalogHost{{ID: identity.OwnerHostID, Name: "test", State: "online", Local: true}}
	}
	snapshot, err := QueryCatalog(context.Background(), func(context.Context) (CatalogInput, error) { return input, nil }, func(_ context.Context, target string) (ResolvedTarget, error) {
		for _, project := range input.Projects {
			for _, workspace := range project.Result.Workspaces {
				if workspace.TmuxName != target || !workspace.Live || workspace.Ambiguous {
					continue
				}
				return ResolvedTarget{
					WorkspaceID: filepath.Base(workspace.ProjectKey), WorkspaceKind: string(workspace.Kind), ProjectRoot: workspace.ProjectKey, Session: workspace.TmuxName, Pane: workspace.PaneID,
					ServerPID: 42, SessionID: "$1", SessionCreated: "1700000000", DurableSessionCreated: formatCatalogTime(workspace.CreatedAt), Width: 80, Height: 24,
				}, nil
			}
		}
		return ResolvedTarget{}, &ResolveError{Code: mobileproto.ErrorNotFound, Message: "target not found"}
	}, query, identity)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func catalogRowsByID(snapshot mobileproto.CatalogSnapshot) map[string]mobileproto.CatalogRow {
	rows := make(map[string]mobileproto.CatalogRow)
	for _, section := range snapshot.Sections {
		for _, row := range section.Rows {
			rows[row.ID] = row
		}
	}
	return rows
}

func catalogRowIDs(snapshot mobileproto.CatalogSnapshot) []string {
	var ids []string
	for _, section := range snapshot.Sections {
		for _, row := range section.Rows {
			ids = append(ids, row.ID)
		}
	}
	return ids
}

func assertCatalogVerdict(t *testing.T, row mobileproto.CatalogRow, state, code string, ready bool) {
	t.Helper()
	if row.AttachState != state || row.RefusalCode != code || row.AttachmentReady != ready || row.OwnerHostID != "local:test" {
		t.Fatalf("verdict = %+v, want state=%q code=%q ready=%t", row, state, code, ready)
	}
}
