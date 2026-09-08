package mobile

import (
	"errors"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/mobileproto"
)

func TestComposeCatalogUsesSharedFiltersAndPreservesOwnerProjectOrder(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	identity := CatalogIdentity{HubID: "hub", OwnerHostID: "local:hub", OwnerConfigGeneration: "cfg"}
	hosts := []mobileproto.CatalogHost{{ID: "local:hub", State: "online", Local: true}, {ID: "book", State: "online"}}
	localFirst := composedReadyRow("local-first", "local:hub", "same-project", "Local first", now)
	localSecond := composedReadyRow("local-second", "local:hub", "later", "Local second", now.Add(-time.Minute))
	remoteSame := composedReadyRow("book\x1fremote-same", "book", "same-project", "Remote same", now.Add(-time.Hour))
	sources := []CatalogSource{
		composedSource("local:hub", "hub", localFirst, localSecond),
		composedSource("book", "hub", remoteSame),
	}
	snapshot, err := ComposeCatalog(mobileproto.CatalogQuery{Sort: "project"}, identity, now, hosts, sources, nil)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Total != 3 || len(snapshot.Sections) != 3 {
		t.Fatalf("composed project sections = %+v", snapshot.Sections)
	}
	if got := []string{snapshot.Sections[0].Rows[0].ID, snapshot.Sections[1].Rows[0].ID, snapshot.Sections[2].Rows[0].ID}; got[0] != "local-first" || got[1] != "local-second" || got[2] != "book\x1fremote-same" {
		t.Fatalf("composed project order = %q", got)
	}
	filtered, err := ComposeCatalog(mobileproto.CatalogQuery{Sort: "activity", Hosts: []string{"book"}}, identity, now, hosts, sources, nil)
	if err != nil {
		t.Fatal(err)
	}
	if filtered.Total != 1 || filtered.Sections[0].Rows[0].ID != "book\x1fremote-same" {
		t.Fatalf("filtered catalog = %+v", filtered)
	}
}

func TestComposeCatalogRefusesNonAuthoritativeSourcesAndRows(t *testing.T) {
	now := time.Now().UTC()
	identity := CatalogIdentity{HubID: "hub", OwnerHostID: "local:hub", OwnerConfigGeneration: "cfg"}
	hosts := []mobileproto.CatalogHost{{ID: "local:hub", State: "online"}, {ID: "book", State: "online"}}
	valid := composedSource("book", "hub", composedReadyRow("book\x1frow", "book", "repo", "Remote", now))
	for _, tc := range []struct {
		name    string
		hosts   []mobileproto.CatalogHost
		sources []CatalogSource
		mutate  func(*CatalogSource)
	}{
		{name: "filtered owner source", hosts: hosts, sources: []CatalogSource{valid}, mutate: func(source *CatalogSource) { source.Snapshot.Query.Search = "partial" }},
		{name: "wrong public hub", hosts: hosts, sources: []CatalogSource{valid}, mutate: func(source *CatalogSource) { source.Snapshot.Sections[0].Rows[0].ExpectedTarget.HubID = "owner" }},
		{name: "stale ready row", hosts: hosts, sources: []CatalogSource{valid}, mutate: func(source *CatalogSource) { source.Snapshot.Sections[0].Rows[0].Stale = true }},
		{name: "offline source", hosts: []mobileproto.CatalogHost{{ID: "local:hub", State: "online"}, {ID: "book", State: "stale"}}, sources: []CatalogSource{valid}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sources := append([]CatalogSource(nil), tc.sources...)
			if tc.mutate != nil {
				sources[0].Snapshot.Sections = cloneCatalogSections(sources[0].Snapshot.Sections)
				tc.mutate(&sources[0])
			}
			_, err := ComposeCatalog(mobileproto.CatalogQuery{}, identity, now, tc.hosts, sources, nil)
			var resolveErr *ResolveError
			if !errors.As(err, &resolveErr) || resolveErr.Code != mobileproto.ErrorIdentityChanged {
				t.Fatalf("compose error = %v", err)
			}
		})
	}
}

func TestComposeCatalogGenerationIgnoresObservationTimeAndTracksOwnerAuthority(t *testing.T) {
	now := time.Now().UTC()
	identity := CatalogIdentity{HubID: "hub", OwnerHostID: "local:hub", OwnerConfigGeneration: "cfg"}
	hosts := []mobileproto.CatalogHost{{ID: "local:hub", State: "online"}}
	row := composedReadyRow("row", "local:hub", "repo", "Local", now)
	first, err := ComposeCatalog(mobileproto.CatalogQuery{}, identity, now, hosts, []CatalogSource{composedSource("local:hub", "hub", row)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	row.ObservedAt = now.Add(time.Minute).Format(time.RFC3339Nano)
	second, err := ComposeCatalog(mobileproto.CatalogQuery{}, identity, now.Add(time.Minute), hosts, []CatalogSource{composedSource("local:hub", "hub", row)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.Generation != second.Generation {
		t.Fatalf("observation time changed generation %s -> %s", first.Generation, second.Generation)
	}
	row.ExpectedTarget.TargetGeneration = "replacement"
	third, err := ComposeCatalog(mobileproto.CatalogQuery{}, identity, now, hosts, []CatalogSource{composedSource("local:hub", "hub", row)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if third.Generation == first.Generation {
		t.Fatal("changed owner authority retained generation")
	}
}

func composedSource(owner, hub string, rows ...mobileproto.CatalogRow) CatalogSource {
	sections := make([]mobileproto.CatalogSection, 0, len(rows))
	for _, row := range rows {
		sections = append(sections, mobileproto.CatalogSection{Key: row.ProjectID, Rows: []mobileproto.CatalogRow{row}})
	}
	return CatalogSource{OwnerHostID: owner, Snapshot: mobileproto.CatalogSnapshot{HubID: hub, OwnerHostID: owner,
		OwnerConfigGeneration: "owner-cfg", Query: mobileproto.CatalogQuery{Sort: "project"}, Sections: sections}}
}

func composedReadyRow(id, owner, projectID, name string, changed time.Time) mobileproto.CatalogRow {
	expected := mobileproto.TargetIdentity{HubID: "hub", OwnerHostID: owner, OwnerConfigGeneration: "owner-cfg", WorkspaceID: "workspace-" + id,
		WorkspaceKind: "shell", Session: "session-" + id, Pane: "%1", ServerIncarnation: "pid=1", TargetGeneration: "target-" + id}
	return mobileproto.CatalogRow{ID: id, OwnerHostID: owner, ProjectID: projectID, ProjectName: projectID,
		WorkspaceID: expected.WorkspaceID, WorkspaceKind: expected.WorkspaceKind, DisplayName: name, Status: "idle", Group: "Idle",
		Session: expected.Session, Pane: expected.Pane, Target: "target-" + id, ExpectedTarget: &expected,
		AttachState: AttachReady, ObservedAt: changed.Format(time.RFC3339Nano), ChangedAt: changed.Format(time.RFC3339Nano),
		Live: true, AttachmentReady: true}
}

func cloneCatalogSections(sections []mobileproto.CatalogSection) []mobileproto.CatalogSection {
	out := append([]mobileproto.CatalogSection(nil), sections...)
	for i := range out {
		out[i].Rows = append([]mobileproto.CatalogRow(nil), out[i].Rows...)
		for j := range out[i].Rows {
			if out[i].Rows[j].ExpectedTarget != nil {
				copy := *out[i].Rows[j].ExpectedTarget
				out[i].Rows[j].ExpectedTarget = &copy
			}
		}
	}
	return out
}
