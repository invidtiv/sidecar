package mobile

import (
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/mobileproto"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func TestCatalogContentWorkspaceSelectors(t *testing.T) {
	now := time.Now()
	for _, path := range []string{"", "/repo", "/linked"} {
		want := ""
		if path == "/linked" {
			want = "/repo:worktree:/linked"
		}
		for _, kind := range []workspaceinventory.Kind{workspaceinventory.KindShell, workspaceinventory.KindWorktree} {
			workspace := catalogShell("opaque-row", "Shell", "shell", "%1", now)
			workspace.ProjectKey, workspace.ProjectRoot, workspace.Path, workspace.Kind = "/repo", "/repo", path, kind
			workspace.IsMain = kind == workspaceinventory.KindWorktree && path != "/linked"
			snapshot := mustCatalog(t, CatalogInput{ObservedAt: now, Projects: []CatalogProject{{Result: workspaceinventory.ProjectResult{ProjectKey: "/repo", Workspaces: []workspaceinventory.Workspace{workspace}}}}}, mobileproto.CatalogQuery{})
			row := catalogRowsByID(snapshot)["opaque-row"]
			if row.ContentWorkspaceID != want {
				t.Fatalf("%s %q: selector %q, want %q", kind, path, row.ContentWorkspaceID, want)
			}
			if row.MainCheckout == nil || *row.MainCheckout != workspace.IsMain {
				t.Fatalf("%s %q: main checkout metadata disagrees with content scope: %+v", kind, path, row)
			}
			if kind == workspaceinventory.KindShell && (row.ExpectedTarget == nil || row.ExpectedTarget.WorkspaceID != "repo" || row.WorkspaceID != "repo") {
				t.Fatal("content selector changed terminal identity")
			}
			if kind == workspaceinventory.KindWorktree {
				workspace.TerminalCandidates = []workspaceinventory.TerminalCandidate{{Session: "one", Pane: "%1"}, {Session: "two", Pane: "%2"}}
				_, candidates, err := WorkspaceCandidates(workspace, "local:test")
				if err != nil || len(candidates) != 2 {
					t.Fatalf("candidates: %+v %v", candidates, err)
				}
				for _, c := range candidates {
					if c.ContentWorkspaceID != want || c.WorkspaceID != workspace.ID {
						t.Fatalf("candidate scope disagrees with row: %+v", c)
					}
				}
			}
		}
	}
}
