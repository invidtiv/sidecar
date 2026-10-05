package cli

import (
	"testing"

	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/mobileproto"
)

// With remote hosts configured, terminals go through the hub broker, which only
// accepts its own public selectors. Workspace rows must attach exactly as the
// same rows in /sessions do, while keeping their local content selectors.
func TestWorkspaceRowsAdoptHubTerminalAuthority(t *testing.T) {
	owner := "local:aerie"
	rawExpected := mobileproto.TargetIdentity{HubID: "aerie", OwnerHostID: owner, OwnerConfigGeneration: "raw", Session: "s1", Pane: "%1"}
	hubExpected := mobileproto.TargetIdentity{HubID: "aerie", OwnerHostID: owner, OwnerConfigGeneration: "public", Session: "s1", Pane: "%1"}
	local := mobileproto.CatalogSnapshot{Sections: []mobileproto.CatalogSection{{Rows: []mobileproto.CatalogRow{
		{ID: "/p:shell:s1", OwnerHostID: owner, ContentWorkspaceID: "wt", Target: "s1", ExpectedTarget: &rawExpected, AttachmentReady: true,
			Candidates: []mobileproto.CatalogCandidate{{Selector: "s1", ContentWorkspaceID: "wt", ExpectedTarget: rawExpected}}},
		{ID: "/p:shell:gone", OwnerHostID: owner, Target: "gone", ExpectedTarget: &rawExpected, AttachmentReady: true},
	}}}}
	public := mobileproto.CatalogSnapshot{Sections: []mobileproto.CatalogSection{{Rows: []mobileproto.CatalogRow{
		{ID: hosts.ScopedKey(owner, "/p:shell:s1"), Target: "hub_target_1", ExpectedTarget: &hubExpected, AttachmentReady: true,
			Candidates: []mobileproto.CatalogCandidate{{Selector: "hub_target_1", ExpectedTarget: hubExpected}}},
	}}}}

	adoptPublicTerminalAuthority(public, &local)

	got := local.Sections[0].Rows[0]
	if got.Target != "hub_target_1" || *got.ExpectedTarget != hubExpected || !got.AttachmentReady {
		t.Fatalf("row authority = %q %+v ready=%v; want the hub's", got.Target, got.ExpectedTarget, got.AttachmentReady)
	}
	if got.Candidates[0].Selector != "hub_target_1" || got.Candidates[0].ExpectedTarget != hubExpected || got.Candidates[0].ContentWorkspaceID != "wt" {
		t.Fatalf("candidate = %+v; want hub authority with the local content selector kept", got.Candidates[0])
	}
	if got.ContentWorkspaceID != "wt" {
		t.Fatalf("content_workspace_id = %q; the hub strips it, the workspace must keep it", got.ContentWorkspaceID)
	}
	missing := local.Sections[0].Rows[1]
	if missing.AttachmentReady || missing.Target != "" || missing.ExpectedTarget != nil {
		t.Fatalf("a row the hub does not list stayed attachable: %+v", missing)
	}
}
