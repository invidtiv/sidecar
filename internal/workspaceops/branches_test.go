package workspaceops

import (
	"context"
	"slices"
	"testing"
)

func TestListLocalBranchesAndCurrentBranch(t *testing.T) {
	root := throwawayRepo(t)
	git(t, root, "branch", "dev")

	got, err := ListLocalBranches(context.Background(), root)
	if err != nil {
		t.Fatalf("ListLocalBranches: %v", err)
	}
	slices.Sort(got)
	want := []string{"dev", "main"}
	if !slices.Equal(got, want) {
		t.Fatalf("branches = %v, want %v", got, want)
	}

	cur, err := CurrentBranch(context.Background(), root)
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	if cur != "main" {
		t.Fatalf("current = %q, want main", cur)
	}

	git(t, root, "checkout", "-q", "dev")
	cur, err = CurrentBranch(context.Background(), root)
	if err != nil {
		t.Fatalf("CurrentBranch after checkout: %v", err)
	}
	if cur != "dev" {
		t.Fatalf("current after checkout = %q, want dev", cur)
	}
}

func TestListLocalBranchesAndCurrentBranchRejectNonRepo(t *testing.T) {
	dir := t.TempDir()
	if _, err := ListLocalBranches(context.Background(), dir); err == nil {
		t.Fatal("ListLocalBranches succeeded on a non-repo")
	}
	if _, err := CurrentBranch(context.Background(), dir); err == nil {
		t.Fatal("CurrentBranch succeeded on a non-repo")
	}
}
