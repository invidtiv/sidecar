package cli

import (
	"testing"

	"github.com/marcus/sidecar/internal/mobilehub"
	"github.com/marcus/sidecar/internal/mobileproto"
)

func ownerWorktreeCatalog(t *testing.T, owner string, panes ...string) mobileproto.CatalogSnapshot {
	t.Helper()
	row := mobileproto.CatalogRow{ID: "/p:worktree:wt", ProjectID: "/p", OwnerHostID: owner, WorkspaceID: "wt", WorkspaceKind: "worktree",
		ContentWorkspaceID: "content-wt", CandidateGeneration: "cg-" + panes[0], Live: true, AttachState: "ready", Session: "s"}
	for _, pane := range panes {
		expected := mobileproto.TargetIdentity{HubID: "aerie", OwnerHostID: owner, OwnerConfigGeneration: "raw", WorkspaceID: "wt",
			WorkspaceKind: "worktree", Session: "s", Pane: pane, ServerIncarnation: "srv", TargetGeneration: "tg" + pane}
		row.Candidates = append(row.Candidates, mobileproto.CatalogCandidate{Selector: "raw" + pane, DisplayName: "Pane " + pane,
			OwnerHostID: owner, WorkspaceID: "wt", WorkspaceKind: "worktree", Session: "s", Pane: pane, ExpectedTarget: expected,
			ContentWorkspaceID: "content-wt"})
	}
	if len(panes) == 1 {
		row.Pane, row.Target, row.AttachmentReady = panes[0], row.Candidates[0].Selector, true
		row.ExpectedTarget = &row.Candidates[0].ExpectedTarget
	} else {
		row.AttachState, row.Ambiguous = "ambiguous", true
	}
	snapshot := mobileproto.CatalogSnapshot{HubID: "aerie", OwnerHostID: owner, OwnerConfigGeneration: "raw", Query: mobileproto.CatalogQuery{Sort: "project"},
		Hosts: []mobileproto.CatalogHost{{ID: owner, State: "online", Local: true}}, Sections: []mobileproto.CatalogSection{{Rows: []mobileproto.CatalogRow{row}}}}
	if err := mobileproto.ValidateCatalogCandidates(snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func hubProjection(t *testing.T, hubOwnerID string, raw mobileproto.CatalogSnapshot) mobileproto.CatalogSnapshot {
	t.Helper()
	public, err := mobilehub.RemapOwnerCatalog(mobilehub.CatalogAuthority{HubID: "aerie", HubConfigGeneration: "hub", OwnerHostID: hubOwnerID, RegistrationFingerprint: "reg"}, raw)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := public.Source.Snapshot
	snapshot.Hosts = []mobileproto.CatalogHost{{ID: hubOwnerID, State: "online", Local: hubOwnerID == "local:aerie"}}
	return snapshot
}

// With remote hosts configured, terminals go through the hub broker, which only
// accepts its own public selectors. A workspace row must be the hub's row for
// it, valid under the candidate contract, with the owner's content selectors.
func TestWorkspaceRowsAreTheHubRowsWithContentSelectors(t *testing.T) {
	for _, panes := range [][]string{{"%1"}, {"%1", "%2"}} {
		local := ownerWorktreeCatalog(t, "local:aerie", panes...)
		public := hubProjection(t, "local:aerie", local)

		useHubRows(public, localCatalogHostID(public), &local)

		if err := mobileproto.ValidateCatalogCandidates(local); err != nil {
			t.Fatalf("%v: workspace rows violate the candidate contract: %v", panes, err)
		}
		got, want := local.Sections[0].Rows[0], public.Sections[0].Rows[0]
		if got.ID != want.ID || got.Target != want.Target || len(got.Candidates) != len(want.Candidates) {
			t.Fatalf("%v: row = %+v; want the hub row %+v", panes, got, want)
		}
		if got.ContentWorkspaceID != "content-wt" {
			t.Fatalf("%v: content_workspace_id = %q; the hub strips it, the workspace must keep it", panes, got.ContentWorkspaceID)
		}
		for i, candidate := range got.Candidates {
			if candidate.Selector != want.Candidates[i].Selector || candidate.ContentWorkspaceID != "content-wt" {
				t.Fatalf("%v: candidate %d = %+v", panes, i, candidate)
			}
		}
	}
}

// The two catalogs are separate observations. When the pane set changed in
// between, every displayed candidate must carry its own authority, never a
// neighbour's, and a pane the owner no longer reported gets no content selector.
func TestWorkspaceRowsNeverPairADisplayedPaneWithAnotherPanesAuthority(t *testing.T) {
	local := ownerWorktreeCatalog(t, "local:aerie", "%1", "%2")
	public := hubProjection(t, "local:aerie", ownerWorktreeCatalog(t, "local:aerie", "%2", "%3"))

	useHubRows(public, "local:aerie", &local)

	for _, candidate := range local.Sections[0].Rows[0].Candidates {
		if candidate.Pane != candidate.ExpectedTarget.Pane || candidate.Session != candidate.ExpectedTarget.Session {
			t.Fatalf("displayed %s %s carries authority for %s %s", candidate.DisplayName, candidate.Pane, candidate.ExpectedTarget.Session, candidate.ExpectedTarget.Pane)
		}
		if candidate.Pane == "%3" && candidate.ContentWorkspaceID != "" {
			t.Fatalf("pane %%3 took a content selector from a different observation")
		}
	}
	if err := mobileproto.ValidateCatalogCandidates(local); err != nil {
		t.Fatal(err)
	}
}

// A remote owner's workspace rows are matched under the hub's ID for that host.
func TestRemoteWorkspaceRowsUseTheHubsHostScope(t *testing.T) {
	remote := ownerWorktreeCatalog(t, "local:book", "%1")
	remote.Hosts[0].Local = false
	public := hubProjection(t, "book", remote)

	useHubRows(public, "book", &remote)

	row := remote.Sections[0].Rows[0]
	if row.OwnerHostID != "book" || !row.AttachmentReady || row.Target != public.Sections[0].Rows[0].Target {
		t.Fatalf("remote row = %+v; want the hub's row for host book", row)
	}
}

func TestWorkspaceRowsTheHubDoesNotListCannotAttach(t *testing.T) {
	local := ownerWorktreeCatalog(t, "local:aerie", "%1")
	public := hubProjection(t, "local:aerie", local)
	public.Sections = nil

	useHubRows(public, "local:aerie", &local)

	row := local.Sections[0].Rows[0]
	if row.AttachmentReady || row.Target != "" || row.ExpectedTarget != nil || len(row.Candidates) != 0 {
		t.Fatalf("a row the hub does not list stayed attachable: %+v", row)
	}
}
