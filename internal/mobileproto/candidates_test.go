package mobileproto

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestProductionCandidateCorpusPinsZeroOneManyAuthority(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "mobile-protocol", "v0", "terminal-candidates.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Schema string `json:"schema"`
		Limits struct {
			PerRow                 int `json:"per_row"`
			PerCatalog             int `json:"per_catalog"`
			SerializedCatalogBytes int `json:"serialized_catalog_bytes"`
		} `json:"limits"`
		Cases []struct {
			ID         string          `json:"id"`
			Catalog    CatalogSnapshot `json:"catalog"`
			Selections []Request       `json:"selections"`
			Error      *Error          `json:"error,omitempty"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Schema != "sidecar.mobile.terminal-candidates.v0" || len(fixture.Cases) != 4 ||
		fixture.Limits.PerRow != MaxCandidatesPerRow || fixture.Limits.PerCatalog != MaxCatalogCandidates || fixture.Limits.SerializedCatalogBytes != MaxLineBytes {
		t.Fatalf("candidate fixture header=%q cases=%d", fixture.Schema, len(fixture.Cases))
	}
	want := map[string]bool{"zero": true, "one": true, "many": true, "stale-selection": true}
	for _, test := range fixture.Cases {
		delete(want, test.ID)
		if err := ValidateCatalogCandidates(test.Catalog); err != nil {
			t.Fatalf("%s row: %v", test.ID, err)
		}
		if test.Catalog.HubID == "" || test.Catalog.OwnerHostID == "" || test.Catalog.Generation == "" || len(test.Catalog.Hosts) != 2 || len(test.Catalog.Sections) != 1 || len(test.Catalog.Sections[0].Rows) != 1 {
			t.Fatalf("%s incomplete catalog envelope: %+v", test.ID, test.Catalog)
		}
		row := test.Catalog.Sections[0].Rows[0]
		for _, request := range test.Selections {
			if request.Version != Version || request.Type != RequestResolve || request.Target == "" || request.ExpectedTarget == nil {
				t.Fatalf("%s selection lacks exact authority: %+v", test.ID, request)
			}
			found := false
			for _, candidate := range row.Candidates {
				found = found || request.Target == candidate.Selector && *request.ExpectedTarget == candidate.ExpectedTarget
			}
			if !found && test.Error == nil {
				t.Fatalf("%s selection is not server supplied: %+v", test.ID, request)
			}
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing candidate cases %v", want)
	}
}

func TestValidateCatalogCandidatesRefusesInconsistentAuthority(t *testing.T) {
	identity := TargetIdentity{HubID: "aerie", OwnerHostID: "remote:book", OwnerConfigGeneration: "config-book",
		WorkspaceID: "workspace", WorkspaceKind: "worktree", Session: "session", Pane: "%1", ServerIncarnation: "pid=1", TargetGeneration: "target"}
	candidate := CatalogCandidate{Selector: "selector", DisplayName: "Pane 1", OwnerHostID: identity.OwnerHostID,
		WorkspaceID: identity.WorkspaceID, WorkspaceKind: identity.WorkspaceKind, Session: identity.Session, Pane: identity.Pane, ExpectedTarget: identity}
	row := CatalogRow{ID: "row", OwnerHostID: identity.OwnerHostID, WorkspaceID: identity.WorkspaceID, WorkspaceKind: identity.WorkspaceKind,
		Session: identity.Session, Pane: identity.Pane, Target: candidate.Selector, CandidateGeneration: "set", Candidates: []CatalogCandidate{candidate},
		ExpectedTarget: &identity, AttachState: "ready", Live: true, AttachmentReady: true}
	for _, mutate := range []func(*CatalogRow){
		func(row *CatalogRow) { row.Candidates[0].OwnerHostID = "remote:other" },
		func(row *CatalogRow) { row.Candidates[0].ExpectedTarget.Pane = "%2" },
		func(row *CatalogRow) { row.Target = "client-derived" },
		func(row *CatalogRow) { row.AttachmentReady = false },
		func(row *CatalogRow) { row.Ambiguous = true },
		func(row *CatalogRow) { row.Stale = true },
		func(row *CatalogRow) { row.Live = false },
		func(row *CatalogRow) { row.Candidates[0].ExpectedTarget.HubID = "other-hub" },
	} {
		changed := row
		changed.Candidates = append([]CatalogCandidate(nil), row.Candidates...)
		mutate(&changed)
		if err := ValidateCatalogCandidates(CatalogSnapshot{HubID: "aerie", Hosts: []CatalogHost{{ID: "remote:book"}}, Sections: []CatalogSection{{Rows: []CatalogRow{changed}}}}); err == nil {
			t.Fatalf("inconsistent row accepted: %+v", changed)
		}
	}
}

func TestValidateCatalogCandidatesRefusesUnavailableOwnerHost(t *testing.T) {
	identity := TargetIdentity{HubID: "aerie", OwnerHostID: "remote:book", OwnerConfigGeneration: "config-book",
		WorkspaceID: "workspace", WorkspaceKind: "worktree", Session: "session", Pane: "%1", ServerIncarnation: "pid=1", TargetGeneration: "target"}
	candidate := CatalogCandidate{Selector: "selector", DisplayName: "Pane 1", OwnerHostID: identity.OwnerHostID,
		WorkspaceID: identity.WorkspaceID, WorkspaceKind: identity.WorkspaceKind, Session: identity.Session, Pane: identity.Pane, ExpectedTarget: identity}
	row := CatalogRow{ID: "row", OwnerHostID: identity.OwnerHostID, WorkspaceID: identity.WorkspaceID, WorkspaceKind: identity.WorkspaceKind,
		Session: identity.Session, Pane: identity.Pane, Target: candidate.Selector, CandidateGeneration: "set", Candidates: []CatalogCandidate{candidate},
		ExpectedTarget: &identity, AttachState: "ready", Live: true, AttachmentReady: true}
	for _, state := range []string{"stale", "offline", "unsupported"} {
		snapshot := CatalogSnapshot{HubID: "aerie", Hosts: []CatalogHost{{ID: "remote:book", State: state}}, Sections: []CatalogSection{{Rows: []CatalogRow{row}}}}
		if err := ValidateCatalogCandidates(snapshot); err == nil {
			t.Fatalf("candidate on %s owner accepted", state)
		}
	}
}
