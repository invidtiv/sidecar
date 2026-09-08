package mobilehub

import (
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/mobile"
	"github.com/marcus/sidecar/internal/mobileproto"
)

func TestRemapOwnerCatalogBindsPublicAndRawIdentity(t *testing.T) {
	authority := CatalogAuthority{HubID: "aerie", HubConfigGeneration: "hub-cfg", OwnerHostID: "book", RegistrationFingerprint: "registration"}
	raw := rawOwnerCatalog("book-local", "same", "candidate_v0_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	remapped, err := RemapOwnerCatalog(authority, raw)
	if err != nil {
		t.Fatal(err)
	}
	row := remapped.Source.Snapshot.Sections[0].Rows[0]
	if row.OwnerHostID != "book" || row.ExpectedTarget == nil || row.ExpectedTarget.HubID != "aerie" || row.ExpectedTarget.OwnerHostID != "book" ||
		row.WorkspaceID != row.ExpectedTarget.WorkspaceID || row.Session != row.ExpectedTarget.Session || row.Pane != row.ExpectedTarget.Pane ||
		row.Target == raw.Sections[0].Rows[0].Target || len(remapped.Bindings) != 1 {
		t.Fatalf("public row = %+v bindings=%+v", row, remapped.Bindings)
	}
	binding := remapped.Bindings[row.Target]
	if binding.PublicExpected != *row.ExpectedTarget || binding.OwnerSelector != raw.Sections[0].Rows[0].Target || binding.OwnerExpected != *raw.Sections[0].Rows[0].ExpectedTarget {
		t.Fatalf("binding = %+v", binding)
	}
	identity := mobile.CatalogIdentity{HubID: "aerie", OwnerHostID: "local:aerie", OwnerConfigGeneration: "hub-cfg"}
	snapshot, err := mobile.ComposeCatalog(mobileproto.CatalogQuery{}, identity, time.Now(),
		[]mobileproto.CatalogHost{{ID: "local:aerie", State: "online", Local: true}, {ID: "book", State: "online"}}, []mobile.CatalogSource{remapped.Source}, nil)
	if err != nil || snapshot.Total != 1 {
		t.Fatalf("composed public catalog total=%d err=%v", snapshot.Total, err)
	}
}

func TestRemapOwnerCatalogKeepsSingleCandidateAsExactPublicRowAuthority(t *testing.T) {
	authority := CatalogAuthority{HubID: "aerie", HubConfigGeneration: "hub-cfg", OwnerHostID: "book", RegistrationFingerprint: "registration"}
	raw := rawOwnerCatalog("book-local", "same", "candidate_v0_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	rawRow := &raw.Sections[0].Rows[0]
	rawRow.CandidateGeneration = "raw-set"
	rawRow.Candidates = []mobileproto.CatalogCandidate{{Selector: rawRow.Target, DisplayName: "Agent pane", OwnerHostID: rawRow.OwnerHostID,
		WorkspaceID: rawRow.WorkspaceID, WorkspaceKind: rawRow.WorkspaceKind, Session: rawRow.Session, Pane: rawRow.Pane, ExpectedTarget: *rawRow.ExpectedTarget}}
	remapped, err := RemapOwnerCatalog(authority, raw)
	if err != nil {
		t.Fatal(err)
	}
	row := remapped.Source.Snapshot.Sections[0].Rows[0]
	if len(row.Candidates) != 1 || row.Target != row.Candidates[0].Selector || row.ExpectedTarget == nil || *row.ExpectedTarget != row.Candidates[0].ExpectedTarget ||
		row.CandidateGeneration == rawRow.CandidateGeneration || row.Candidates[0].WorkspaceID != row.WorkspaceID || len(remapped.Bindings) != 1 {
		t.Fatalf("public single candidate row = %+v bindings=%+v", row, remapped.Bindings)
	}
}

func TestRemapOwnerCatalogNamespacesDuplicateOwnerFactsAndTracksRegistration(t *testing.T) {
	raw := rawOwnerCatalog("owner-local", "same", "shared")
	one, err := RemapOwnerCatalog(CatalogAuthority{HubID: "hub", HubConfigGeneration: "cfg", OwnerHostID: "one", RegistrationFingerprint: "one-reg"}, raw)
	if err != nil {
		t.Fatal(err)
	}
	two, err := RemapOwnerCatalog(CatalogAuthority{HubID: "hub", HubConfigGeneration: "cfg", OwnerHostID: "two", RegistrationFingerprint: "two-reg"}, raw)
	if err != nil {
		t.Fatal(err)
	}
	oneRow, twoRow := one.Source.Snapshot.Sections[0].Rows[0], two.Source.Snapshot.Sections[0].Rows[0]
	if oneRow.ID == twoRow.ID || oneRow.ProjectID == twoRow.ProjectID || oneRow.WorkspaceID == twoRow.WorkspaceID || oneRow.Target == twoRow.Target ||
		oneRow.ExpectedTarget.TargetGeneration == twoRow.ExpectedTarget.TargetGeneration {
		t.Fatalf("owner namespaces collided one=%+v two=%+v", oneRow, twoRow)
	}
	changed, err := RemapOwnerCatalog(CatalogAuthority{HubID: "hub", HubConfigGeneration: "cfg", OwnerHostID: "one", RegistrationFingerprint: "changed-reg"}, raw)
	if err != nil {
		t.Fatal(err)
	}
	changedRow := changed.Source.Snapshot.Sections[0].Rows[0]
	if changedRow.Target == oneRow.Target || changedRow.ExpectedTarget.OwnerConfigGeneration == oneRow.ExpectedTarget.OwnerConfigGeneration ||
		changedRow.ExpectedTarget.TargetGeneration == oneRow.ExpectedTarget.TargetGeneration {
		t.Fatal("registration change retained public target authority")
	}
}

func TestRemapOwnerCatalogRefusesRawExpectedIdentityMismatch(t *testing.T) {
	raw := rawOwnerCatalog("owner-local", "same", "shared")
	raw.Sections[0].Rows[0].ExpectedTarget.OwnerHostID = "other"
	_, err := RemapOwnerCatalog(CatalogAuthority{HubID: "hub", HubConfigGeneration: "cfg", OwnerHostID: "one", RegistrationFingerprint: "reg"}, raw)
	if err == nil {
		t.Fatal("raw expected owner mismatch was accepted")
	}
}

func TestRemapOwnerCatalogRefusesAuthorityFactsThatPublicRewritingWouldHide(t *testing.T) {
	authority := CatalogAuthority{HubID: "hub", HubConfigGeneration: "cfg", OwnerHostID: "book", RegistrationFingerprint: "reg"}
	for _, tc := range []struct {
		name   string
		mutate func(*mobileproto.CatalogSnapshot)
	}{
		{name: "owner offline", mutate: func(snapshot *mobileproto.CatalogSnapshot) { snapshot.Hosts[0].State = "stale" }},
		{name: "owner absent", mutate: func(snapshot *mobileproto.CatalogSnapshot) { snapshot.Hosts = nil }},
		{name: "row target hub mismatch", mutate: func(snapshot *mobileproto.CatalogSnapshot) {
			snapshot.Sections[0].Rows[0].ExpectedTarget.HubID = "other"
		}},
		{name: "row target config mismatch", mutate: func(snapshot *mobileproto.CatalogSnapshot) {
			snapshot.Sections[0].Rows[0].ExpectedTarget.OwnerConfigGeneration = "other"
		}},
		{name: "candidate target config mismatch", mutate: func(snapshot *mobileproto.CatalogSnapshot) {
			row := &snapshot.Sections[0].Rows[0]
			row.CandidateGeneration = "set"
			row.Candidates = []mobileproto.CatalogCandidate{{Selector: row.Target, DisplayName: "Pane", OwnerHostID: row.OwnerHostID,
				WorkspaceID: row.WorkspaceID, WorkspaceKind: row.WorkspaceKind, Session: row.Session, Pane: row.Pane, ExpectedTarget: *row.ExpectedTarget}}
			row.Candidates[0].ExpectedTarget.OwnerConfigGeneration = "other"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := rawOwnerCatalog("owner-local", "same", "shared")
			tc.mutate(&raw)
			if remapped, err := RemapOwnerCatalog(authority, raw); err == nil {
				t.Fatalf("invalid raw authority was remapped: %+v", remapped)
			}
		})
	}
}

func rawOwnerCatalog(rawOwner, projectID, selector string) mobileproto.CatalogSnapshot {
	expected := mobileproto.TargetIdentity{HubID: "owner-hub", OwnerHostID: rawOwner, OwnerConfigGeneration: "owner-cfg", WorkspaceID: "workspace",
		WorkspaceKind: "shell", Session: "session", Pane: "%1", ServerIncarnation: "pid=1", TargetGeneration: "generation"}
	row := mobileproto.CatalogRow{ID: "row", OwnerHostID: rawOwner, ProjectID: projectID, ProjectName: "Repo", WorkspaceID: expected.WorkspaceID,
		WorkspaceKind: expected.WorkspaceKind, DisplayName: "Terminal", Status: "idle", Group: "Idle", Session: expected.Session, Pane: expected.Pane,
		Target: selector, ExpectedTarget: &expected, AttachState: mobile.AttachReady, Live: true, AttachmentReady: true}
	return mobileproto.CatalogSnapshot{HubID: "owner-hub", OwnerHostID: rawOwner, OwnerConfigGeneration: "owner-cfg",
		Query: mobileproto.CatalogQuery{Sort: "project"}, Hosts: []mobileproto.CatalogHost{{ID: rawOwner, State: "online", Local: true}},
		Sections: []mobileproto.CatalogSection{{Key: projectID, Rows: []mobileproto.CatalogRow{row}}}, Total: 1}
}
