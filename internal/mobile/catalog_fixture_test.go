package mobile

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentstatus"
	"github.com/marcus/sidecar/internal/mobileproto"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

type catalogFixture struct {
	Schema     string               `json:"schema"`
	Provenance string               `json:"provenance"`
	Cases      []catalogFixtureCase `json:"cases"`
}

type catalogFixtureCase struct {
	ID       string               `json:"id"`
	Request  mobileproto.Request  `json:"request"`
	Response mobileproto.Response `json:"response"`
}

func TestCatalogProtocolFixtureMatchesProducer(t *testing.T) {
	now := time.Date(2026, 9, 7, 18, 0, 0, 0, time.UTC)
	input := catalogFixtureInput(now)
	queries := []struct {
		id    string
		query mobileproto.CatalogQuery
	}{
		{id: "activity", query: mobileproto.CatalogQuery{Sort: "activity"}},
		{id: "project", query: mobileproto.CatalogQuery{Sort: "project"}},
		{id: "recent", query: mobileproto.CatalogQuery{Sort: "recent"}},
		{id: "name", query: mobileproto.CatalogQuery{Sort: "name"}},
		{id: "provider-codex", query: mobileproto.CatalogQuery{Sort: "activity", Providers: []string{"codex"}}},
		{id: "state-ready", query: mobileproto.CatalogQuery{Sort: "activity", States: []string{"ready"}}},
		{id: "state-ambiguous", query: mobileproto.CatalogQuery{Sort: "activity", States: []string{"ambiguous"}}},
		{id: "state-stale", query: mobileproto.CatalogQuery{Sort: "activity", States: []string{"stale"}}},
		{id: "state-unsupported", query: mobileproto.CatalogQuery{Sort: "activity", States: []string{"unsupported"}}},
		{id: "search-local-shell", query: mobileproto.CatalogQuery{Sort: "name", Search: "local:aerie durable"}},
	}
	fixture := catalogFixture{Schema: "sidecar.mobile.catalog.v0", Provenance: "synthetic workspace inventory projected by internal/mobile.QueryCatalog; no user terminal content"}
	for _, test := range queries {
		requestID := "catalog-" + test.id
		snapshot := mustCatalogWithIdentity(t, input, test.query, CatalogIdentity{HubID: "aerie", OwnerHostID: "local:aerie", OwnerConfigGeneration: "config-7"})
		fixture.Cases = append(fixture.Cases, catalogFixtureCase{
			ID:       test.id,
			Request:  mobileproto.Request{Version: mobileproto.Version, Type: mobileproto.RequestSessions, RequestID: requestID, CatalogQuery: &test.query},
			Response: mobileproto.Response{Version: mobileproto.Version, Type: mobileproto.ResponseSessions, RequestID: requestID, APIInstance: "api_fixture", Catalog: &snapshot},
		})
	}
	want, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, '\n')
	path := filepath.Join("..", "..", "testdata", "mobile-protocol", "v0", "sessions-catalog.json")
	if os.Getenv("UPDATE_MOBILE_CATALOG_FIXTURE") == "1" {
		if err := os.WriteFile(path, want, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s does not match the Go producer; run UPDATE_MOBILE_CATALOG_FIXTURE=1 go test ./internal/mobile -run TestCatalogProtocolFixtureMatchesProducer", path)
	}
}

func catalogFixtureInput(now time.Time) CatalogInput {
	working := catalogShell("one:worktree:working", "Working", "sidecar-wt-working", "%1", now)
	working.Kind, working.ProjectKey, working.ProjectName, working.Branch, working.TaskID, working.Provider = workspaceinventory.KindWorktree, "/one", "One", "feature/mobile", "td-one", "codex"
	working.CreatedAt = time.Time{}
	working.Presentation = agentstatus.Presentation{Lane: agentstatus.LaneWorking, Label: "working", ChangedAt: now.Add(-time.Minute), Semantic: true}
	durable := catalogShell("one:shell:durable", "Durable Shell", "sidecar-sh-durable", "%2", now)
	durable.ProjectKey, durable.ProjectName = "/one", "One"
	ambiguous := catalogShell("one:shell:ambiguous", "Ambiguous Shell", "sidecar-sh-ambiguous", "", now)
	ambiguous.ProjectKey, ambiguous.ProjectName, ambiguous.Live, ambiguous.Ambiguous = "/one", "One", false, true
	plain := workspaceinventory.Workspace{ID: "one:worktree:plain", ProjectKey: "/one", ProjectName: "One", Kind: workspaceinventory.KindWorktree, Name: "Plain Worktree", Branch: "main", Plain: true, IsMain: true, ObservedAt: now}
	unknown := catalogShell("one:shell:unknown", "Unknown Identity", "sidecar-sh-unknown", "%3", now)
	unknown.ProjectKey, unknown.ProjectName, unknown.CreatedAt = "/one", "One", time.Time{}
	duplicateA := catalogShell("one:shell:duplicate", "Duplicate One", "sidecar-sh-collision", "%4", now)
	duplicateA.ProjectKey, duplicateA.ProjectName = "/one", "One"

	blocked := catalogShell("two:worktree:blocked", "Blocked", "sidecar-wt-blocked", "%5", now)
	blocked.Kind, blocked.ProjectKey, blocked.ProjectName, blocked.Provider = workspaceinventory.KindWorktree, "/two", "Two", "claude"
	blocked.CreatedAt = time.Time{}
	blocked.Presentation = agentstatus.Presentation{Lane: agentstatus.LaneBlocked, Label: "blocked", ChangedAt: now.Add(-2 * time.Minute), Semantic: true, Attention: true}
	duplicateB := catalogShell("two:shell:duplicate", "Duplicate Two", "sidecar-sh-collision", "%6", now)
	duplicateB.ProjectKey, duplicateB.ProjectName = "/two", "Two"
	stale := catalogShell("two:shell:stale", "Stale Shell", "sidecar-sh-stale", "%7", now)
	stale.ProjectKey, stale.ProjectName = "/stale", "Stale"

	return CatalogInput{
		ObservedAt: now,
		Hosts:      []mobileproto.CatalogHost{{ID: "local:aerie", Name: "aerie", State: "online", Local: true}},
		Projects: []CatalogProject{
			{Label: "One", Order: 0, Result: workspaceinventory.ProjectResult{ProjectKey: "/one", ProjectName: "One", Workspaces: []workspaceinventory.Workspace{working, durable, ambiguous, plain, unknown, duplicateA}}},
			{Label: "Two", Order: 1, Result: workspaceinventory.ProjectResult{ProjectKey: "/two", ProjectName: "Two", Workspaces: []workspaceinventory.Workspace{blocked, duplicateB}}},
			{Label: "Stale", Order: 2, Stale: true, Result: workspaceinventory.ProjectResult{ProjectKey: "/stale", ProjectName: "Stale", Workspaces: []workspaceinventory.Workspace{stale}}},
			{Label: "Missing", Order: 3, Result: workspaceinventory.ProjectResult{ProjectKey: "/missing", ProjectName: "Missing", Err: os.ErrNotExist}},
		},
	}
}
