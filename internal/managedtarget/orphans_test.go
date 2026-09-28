package managedtarget

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/marcus/sidecar/internal/workspaceops"
)

func stubOrphanEvidence(t *testing.T, listing string, listErr error, states map[string][]workspaceops.WorktreeState) {
	t.Helper()
	oldTmux, oldStates := listTmuxSessions, listWorktreeStates
	listTmuxSessions = func(context.Context) (string, error) { return listing, listErr }
	listWorktreeStates = func(_ context.Context, dir string) ([]workspaceops.WorktreeState, error) {
		if got, ok := states[dir]; ok {
			return got, nil
		}
		return nil, errors.New("not a git repository")
	}
	t.Cleanup(func() { listTmuxSessions, listWorktreeStates = oldTmux, oldStates })
}

func TestObserveWorktreeOrphansFindsTheRemovedWorktreesSession(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(base, "repo")
	removed := filepath.Join(base, "repo-foo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	stubOrphanEvidence(t,
		"sidecar-ws-repo-foo\t"+removed+"\nsidecar-ws-repo\t"+repo+"\nprobe\t/tmp\n", nil,
		map[string][]workspaceops.WorktreeState{repo: {{Path: repo, Branch: "main"}}})

	plan := WorktreeOrphans(t.Context(), []Project{{Key: "repo", Path: repo, Worktrees: []string{removed}}})
	if plan.Skipped != "" || len(plan.Orphans) != 1 {
		t.Fatalf("plan = %+v, want one orphan", plan)
	}
	if got := plan.Orphans[0]; got.Session != "sidecar-ws-repo-foo" || got.Root != removed || got.ProjectKey != "repo" {
		t.Fatalf("orphan = %+v", got)
	}
}

func TestObserveWorktreeOrphansFailedListingIsNoEvidence(t *testing.T) {
	stubOrphanEvidence(t, "", errors.New("tmux: timeout"), nil)
	obs := ObserveWorktreeOrphans(t.Context(), nil)
	if !obs.ListingFailed {
		t.Fatal("a failed tmux listing was treated as an empty one")
	}
}

func TestObserveWorktreeOrphansNoServerIsAnEmptyListing(t *testing.T) {
	exitErr := &exec.ExitError{Stderr: []byte("no server running on /tmp/tmux-501/default\n")}
	stubOrphanEvidence(t, "", exitErr, nil)
	obs := ObserveWorktreeOrphans(t.Context(), nil)
	if obs.ListingFailed || len(obs.Sessions) != 0 {
		t.Fatalf("obs = %+v, want an empty, successful listing", obs)
	}
}

// An unmounted volume makes every path under it missing, parent included. That
// must not read as a removed worktree.
func TestPathMissingRequiresAnExistingParent(t *testing.T) {
	base := t.TempDir()
	if !pathMissingWithParent(filepath.Join(base, "removed")) {
		t.Fatal("a removed leaf under an existing parent should count as missing")
	}
	if pathMissingWithParent(filepath.Join(base, "unmounted", "leaf")) {
		t.Fatal("a missing parent counted as a removed worktree")
	}
	if pathMissingWithParent(base) {
		t.Fatal("an existing directory counted as missing")
	}
}
