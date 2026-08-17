package app

import (
	"testing"

	"github.com/marcus/sidecar/internal/overview"
)

// Activating a card on the Activity board opens that workspace in the global
// Workspaces browser. The board and the list are two projections of one
// catalog, so the card names a row the global space already has — the project
// underneath is not switched and its Workspaces plugin is not activated
// (td-16b473).
func TestActivityCardOpensInTheGlobalWorkspacesBrowser(t *testing.T) {
	m, p, source := newOverviewRaceModel(t)
	target := newOverviewGitRepo(t, "target")
	workspace := overviewWorkspace(target)
	workspace.ID = "target:worktree:1"
	initialInits := p.inits

	updatedModel, _ := m.Update(overview.RevealMsg{Workspace: workspace})
	updated := updatedModel.(Model)

	if !updated.inGlobalScope() {
		t.Fatal("activating a card left the global space")
	}
	if updated.globalTab != GlobalSessions {
		t.Fatalf("global tab = %v, want the Sessions (Workspaces) tab", updated.globalTab)
	}
	if updated.ui.WorkDir != source || updated.ui.ProjectRoot != source {
		t.Fatalf("the project underneath moved to %q", updated.ui.WorkDir)
	}
	if p.inits != initialInits {
		t.Fatalf("the project's plugins were reinitialized (%d inits)", p.inits)
	}
	if updated.activeContext == workspacePluginID {
		t.Fatal("the project-scoped Workspaces plugin took focus")
	}
}

// A reveal that arrives after the user has left the global space does nothing.
func TestLateRevealAfterLeavingTheGlobalSpaceIsIgnored(t *testing.T) {
	m, _, source := newOverviewRaceModel(t)
	workspace := overviewWorkspace(newOverviewGitRepo(t, "target"))

	m.leaveOverview(false)
	updatedModel, cmd := m.Update(overview.RevealMsg{Workspace: workspace})
	updated := updatedModel.(Model)
	if cmd != nil || updated.inGlobalScope() || updated.ui.WorkDir != source {
		t.Fatalf("a late reveal acted after exit: cmd=%v global=%v work=%q", cmd != nil, updated.inGlobalScope(), updated.ui.WorkDir)
	}
}
