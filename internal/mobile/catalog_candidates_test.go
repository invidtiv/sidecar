package mobile

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/mobileproto"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func TestWorkspaceCandidatesAreDeterministicAndMembershipBound(t *testing.T) {
	workspace := candidateWorkspace([]workspaceinventory.TerminalCandidate{
		{Session: "two", Pane: "%2", Title: "Shell"},
		{Session: "one", Pane: "%1", Title: "Agent"},
	})
	generation, candidates, err := WorkspaceCandidates(workspace, "local:hub")
	if err != nil {
		t.Fatal(err)
	}
	if generation == "" || len(candidates) != 2 || candidates[0].Session != "one" || !IsCandidateSelector(candidates[0].Selector) {
		t.Fatalf("candidates = generation %q %+v", generation, candidates)
	}
	workspace.TerminalCandidates[0], workspace.TerminalCandidates[1] = workspace.TerminalCandidates[1], workspace.TerminalCandidates[0]
	reorderedGeneration, reordered, err := WorkspaceCandidates(workspace, "local:hub")
	if err != nil {
		t.Fatal(err)
	}
	if reorderedGeneration != generation || reordered[0].Selector != candidates[0].Selector || reordered[1].Selector != candidates[1].Selector {
		t.Fatalf("source order changed authority: %q/%+v -> %q/%+v", generation, candidates, reorderedGeneration, reordered)
	}
	workspace.TerminalCandidates = workspace.TerminalCandidates[:1]
	changedGeneration, changed, err := WorkspaceCandidates(workspace, "local:hub")
	if err != nil {
		t.Fatal(err)
	}
	if changedGeneration == generation || changed[0].Selector == candidates[0].Selector || changed[0].Selector == candidates[1].Selector {
		t.Fatal("candidate membership change retained old selector authority")
	}
}

func TestResolveCatalogCandidateReenumeratesExactCurrentPane(t *testing.T) {
	workspace := candidateWorkspace([]workspaceinventory.TerminalCandidate{{Session: "session", Pane: "%7", Title: "Agent"}})
	input := CatalogInput{Projects: []CatalogProject{{Result: workspaceinventory.ProjectResult{Workspaces: []workspaceinventory.Workspace{workspace}}}}}
	generation, candidates, err := WorkspaceCandidates(workspace, "local:hub")
	if err != nil {
		t.Fatal(err)
	}
	inspect := func(_ context.Context, pane string) (tty.HeadlessTargetIdentity, error) {
		return tty.HeadlessTargetIdentity{ServerPID: 42, SessionID: "$3", SessionCreated: "1700000000", Session: "session", Pane: pane, Width: 80, Height: 24}, nil
	}
	resolved, err := ResolveCatalogCandidate(context.Background(), input, "local:hub", candidates[0].Selector, inspect)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Selector != candidates[0].Selector || resolved.SourceGeneration != generation || resolved.WorkspaceID != workspace.ID || resolved.Session != "session" || resolved.Pane != "%7" {
		t.Fatalf("resolved candidate = %+v", resolved)
	}
	input.Projects[0].Result.Workspaces[0].TerminalCandidates = append(input.Projects[0].Result.Workspaces[0].TerminalCandidates,
		workspaceinventory.TerminalCandidate{Session: "other", Pane: "%8"})
	if _, err := ResolveCatalogCandidate(context.Background(), input, "local:hub", candidates[0].Selector, inspect); !resolveCode(err, mobileproto.ErrorIdentityChanged) {
		t.Fatalf("old selector after membership change = %v", err)
	}
}

func TestResolveCatalogCandidateRefusesStaleOrFailedProjectObservation(t *testing.T) {
	workspace := candidateWorkspace([]workspaceinventory.TerminalCandidate{{Session: "session", Pane: "%7", Title: "Agent"}})
	_, candidates, err := WorkspaceCandidates(workspace, "local:hub")
	if err != nil {
		t.Fatal(err)
	}
	inspect := func(context.Context, string) (tty.HeadlessTargetIdentity, error) {
		t.Fatal("stale candidate was inspected")
		return tty.HeadlessTargetIdentity{}, nil
	}
	for _, project := range []CatalogProject{
		{Stale: true, Result: workspaceinventory.ProjectResult{Workspaces: []workspaceinventory.Workspace{workspace}}},
		{Result: workspaceinventory.ProjectResult{Err: errors.New("inventory failed"), Workspaces: []workspaceinventory.Workspace{workspace}}},
	} {
		if _, err := ResolveCatalogCandidate(context.Background(), CatalogInput{Projects: []CatalogProject{project}}, "local:hub", candidates[0].Selector, inspect); !resolveCode(err, mobileproto.ErrorIdentityChanged) {
			t.Fatalf("stale project candidate = %v", err)
		}
	}
}

func TestRevalidateCatalogCandidateBracketsPaneInspectionWithCurrentMembership(t *testing.T) {
	workspace := candidateWorkspace([]workspaceinventory.TerminalCandidate{{Session: "session", Pane: "%7", Title: "Agent"}})
	input := CatalogInput{Projects: []CatalogProject{{Result: workspaceinventory.ProjectResult{Workspaces: []workspaceinventory.Workspace{workspace}}}}}
	_, candidates, err := WorkspaceCandidates(workspace, "local:hub")
	if err != nil {
		t.Fatal(err)
	}
	previous, err := ResolveCatalogCandidate(context.Background(), input, "local:hub", candidates[0].Selector, func(context.Context, string) (tty.HeadlessTargetIdentity, error) {
		return candidateIdentity("session", "%7"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	reads := 0
	current := func(context.Context, ResolvedTarget) (workspaceinventory.Workspace, error) {
		reads++
		return workspace, nil
	}
	got, err := RevalidateCatalogCandidate(context.Background(), previous, "local:hub", current, func(context.Context, string) (tty.HeadlessTargetIdentity, error) {
		return candidateIdentity("session", "%7"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if reads != 2 || got.SourceGeneration != previous.SourceGeneration || got.ServerPID != 42 {
		t.Fatalf("revalidation reads=%d target=%+v", reads, got)
	}

	reads = 0
	changed := workspace
	changed.TerminalCandidates = append(changed.TerminalCandidates, workspaceinventory.TerminalCandidate{Session: "new", Pane: "%8"})
	current = func(context.Context, ResolvedTarget) (workspaceinventory.Workspace, error) {
		reads++
		if reads == 1 {
			return workspace, nil
		}
		return changed, nil
	}
	if _, err := RevalidateCatalogCandidate(context.Background(), previous, "local:hub", current, func(context.Context, string) (tty.HeadlessTargetIdentity, error) {
		return candidateIdentity("session", "%7"), nil
	}); !resolveCode(err, mobileproto.ErrorIdentityChanged) {
		t.Fatalf("membership change during inspection = %v", err)
	}
}

func TestRevalidateCatalogCandidateRefusesRemovedSelectedPane(t *testing.T) {
	workspace := candidateWorkspace([]workspaceinventory.TerminalCandidate{{Session: "session", Pane: "%7"}})
	input := CatalogInput{Projects: []CatalogProject{{Result: workspaceinventory.ProjectResult{Workspaces: []workspaceinventory.Workspace{workspace}}}}}
	_, candidates, err := WorkspaceCandidates(workspace, "local:hub")
	if err != nil {
		t.Fatal(err)
	}
	previous, err := ResolveCatalogCandidate(context.Background(), input, "local:hub", candidates[0].Selector, func(context.Context, string) (tty.HeadlessTargetIdentity, error) {
		return candidateIdentity("session", "%7"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	workspace.TerminalCandidates = nil
	if _, err := RevalidateCatalogCandidate(context.Background(), previous, "local:hub", func(context.Context, ResolvedTarget) (workspaceinventory.Workspace, error) {
		return workspace, nil
	}, func(context.Context, string) (tty.HeadlessTargetIdentity, error) {
		t.Fatal("removed candidate was inspected")
		return tty.HeadlessTargetIdentity{}, nil
	}); !resolveCode(err, mobileproto.ErrorIdentityChanged) {
		t.Fatalf("removed selected candidate = %v", err)
	}
}

func TestQueryCatalogAuthorizesOneAndManyWorktreeCandidates(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	one := candidateWorkspace([]workspaceinventory.TerminalCandidate{{Session: "one", Pane: "%1", Title: "Agent"}})
	one.ID, one.Name, one.ObservedAt, one.Live = "worktree-one", "One", now, true
	many := candidateWorkspace([]workspaceinventory.TerminalCandidate{{Session: "many", Pane: "%2", Title: "Agent"}, {Session: "many", Pane: "%3", Title: "Shell"}})
	many.ID, many.Name, many.ObservedAt, many.Ambiguous = "worktree-many", "Many", now, true
	input := CatalogInput{ObservedAt: now, Hosts: []mobileproto.CatalogHost{{ID: "local:hub", State: "online", Local: true}},
		Projects: []CatalogProject{{Label: "Demo", Result: workspaceinventory.ProjectResult{ProjectKey: "/demo", Workspaces: []workspaceinventory.Workspace{one, many}}}}}
	resolver := func(ctx context.Context, selector string) (ResolvedTarget, error) {
		return ResolveCatalogCandidate(ctx, input, "local:hub", selector, func(_ context.Context, pane string) (tty.HeadlessTargetIdentity, error) {
			session := "one"
			if pane != "%1" {
				session = "many"
			}
			return tty.HeadlessTargetIdentity{ServerPID: 42, SessionID: "$3", SessionCreated: "1700000000", Session: session, Pane: pane, Width: 80, Height: 24}, nil
		})
	}
	input.CandidateResolver = resolver
	snapshot, err := QueryCatalog(context.Background(), func(context.Context) (CatalogInput, error) { return input, nil }, func(context.Context, string) (ResolvedTarget, error) {
		t.Fatal("candidate authorization rebuilt the global resolver path")
		return ResolvedTarget{}, nil
	},
		mobileproto.CatalogQuery{Sort: "name"}, CatalogIdentity{HubID: "hub", OwnerHostID: "local:hub", OwnerConfigGeneration: "cfg"})
	if err != nil {
		t.Fatal(err)
	}
	rows := make(map[string]mobileproto.CatalogRow)
	for _, section := range snapshot.Sections {
		for _, row := range section.Rows {
			rows[row.ID] = row
		}
	}
	if row := rows[one.ID]; !row.AttachmentReady || len(row.Candidates) != 1 || row.Target != row.Candidates[0].Selector || row.ExpectedTarget == nil {
		t.Fatalf("one candidate row = %+v", row)
	}
	if row := rows[many.ID]; row.AttachmentReady || !row.Ambiguous || row.Target != "" || row.ExpectedTarget != nil || len(row.Candidates) != 2 {
		t.Fatalf("many candidate row = %+v", row)
	}
	if err := mobileproto.ValidateCatalogCandidates(snapshot); err != nil {
		t.Fatal(err)
	}
}

func candidateWorkspace(candidates []workspaceinventory.TerminalCandidate) workspaceinventory.Workspace {
	return workspaceinventory.Workspace{ID: "worktree", ProjectKey: "/demo", ProjectName: "Demo", ProjectRoot: "/demo", Path: "/demo/worktree",
		Kind: workspaceinventory.KindWorktree, Name: "Worktree", TerminalCandidates: candidates}
}

func candidateIdentity(session, pane string) tty.HeadlessTargetIdentity {
	return tty.HeadlessTargetIdentity{ServerPID: 42, SessionID: "$3", SessionCreated: "1700000000", Session: session, Pane: pane, Width: 80, Height: 24}
}

func resolveCode(err error, code string) bool {
	var resolveErr *ResolveError
	return errors.As(err, &resolveErr) && resolveErr.Code == code
}
