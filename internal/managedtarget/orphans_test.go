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

func stubOrphanEvidence(t *testing.T, listing string, listErr error, states map[string][]workspaceops.WorktreeState) *int {
	t.Helper()
	gitCalls := 0
	oldTmux, oldStates := listTmuxSessions, listWorktreeStates
	listTmuxSessions = func(context.Context) (string, error) { return listing, listErr }
	listWorktreeStates = func(_ context.Context, dir string) ([]workspaceops.WorktreeState, error) {
		gitCalls++
		if got, ok := states[dir]; ok {
			return got, nil
		}
		return nil, errors.New("not a git repository")
	}
	t.Cleanup(func() { listTmuxSessions, listWorktreeStates = oldTmux, oldStates })
	return &gitCalls
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
		"sidecar-ws-repo-foo\t"+removed+"\t"+removed+"\nsidecar-ws-repo\t"+repo+"\nprobe\t/tmp\n", nil,
		map[string][]workspaceops.WorktreeState{repo: {{Path: repo, Branch: "main"}}})

	plan := WorktreeOrphans(t.Context(), []Project{{Key: "repo", Path: repo, Worktrees: []string{removed}}}, ObserveOptions{})
	if plan.Skipped != "" || len(plan.Orphans) != 1 {
		t.Fatalf("plan = %+v, want one orphan", plan)
	}
	if got := plan.Orphans[0]; got.Session != "sidecar-ws-repo-foo" || got.Root != removed || got.ProjectKey != "repo" {
		t.Fatalf("orphan = %+v", got)
	}
}

func TestObserveWorktreeOrphansFailedListingIsNoEvidence(t *testing.T) {
	stubOrphanEvidence(t, "", errors.New("tmux: timeout"), nil)
	obs := ObserveWorktreeOrphans(t.Context(), nil, ObserveOptions{})
	if !obs.ListingFailed {
		t.Fatal("a failed tmux listing was treated as an empty one")
	}
}

func TestObserveWorktreeOrphansNoServerIsAnEmptyListing(t *testing.T) {
	exitErr := &exec.ExitError{Stderr: []byte("no server running on /tmp/tmux-501/default\n")}
	stubOrphanEvidence(t, "", exitErr, nil)
	obs := ObserveWorktreeOrphans(t.Context(), nil, ObserveOptions{})
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

// Every verdict needs a directory that is gone, so a pass with no live worktree
// session in a missing directory must not spawn git per project: `agent list`
// runs this on every call.
func TestObserveWorktreeOrphansSkipsGitWithNothingToJudge(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(base, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	removed := filepath.Join(base, "repo-foo")
	calls := stubOrphanEvidence(t, "sidecar-ws-repo\t"+repo+"\n", nil, map[string][]workspaceops.WorktreeState{repo: {{Path: repo}}})
	projects := []Project{{Key: "repo", Path: repo, Worktrees: []string{removed}}}

	if plan := WorktreeOrphans(t.Context(), projects, ObserveOptions{}); len(plan.Orphans) != 0 || *calls != 0 {
		t.Fatalf("plan %+v, git calls %d; want none", plan, *calls)
	}
	// A caller that wants root verdicts gets them, since a root is missing.
	plan := WorktreeOrphans(t.Context(), projects, ObserveOptions{WantRoots: true})
	if *calls != 1 || plan.Roots[removed] == "" {
		t.Fatalf("roots %+v, git calls %d; want a verdict for %s", plan.Roots, *calls, removed)
	}
}

func TestObserveWorktreeOrphansBlankSessionPathProvesNothing(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(base, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	removed := filepath.Join(base, "repo-foo")
	stubOrphanEvidence(t, "sidecar-ws-repo-foo\t\nsidecar-ws-other\t"+filepath.Join(base, "other")+"\n", nil,
		map[string][]workspaceops.WorktreeState{repo: {{Path: repo}}})
	plan := WorktreeOrphans(t.Context(), []Project{{Key: "repo", Path: repo, Worktrees: []string{removed}}}, ObserveOptions{})
	for _, orphan := range plan.Orphans {
		if orphan.Session == "sidecar-ws-repo-foo" {
			t.Fatalf("a session with no start directory was judged: %+v", orphan)
		}
	}
}

// A project the deadline skipped must still appear, unanswered, or the
// decision's "every listing answered" rule reads its absence as an answer.
func TestObserveWorktreeOrphansRecordsProjectsTheDeadlineSkipped(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stubOrphanEvidence(t, "sidecar-ws-gone\t"+filepath.Join(base, "gone")+"\t"+filepath.Join(base, "gone")+"\n", nil, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	obs := ObserveWorktreeOrphans(ctx, []Project{{Key: "a", Path: filepath.Join(base, "a")}, {Key: "b", Path: filepath.Join(base, "b")}}, ObserveOptions{})
	if len(obs.Inventories) != 2 {
		t.Fatalf("inventories = %+v, want both projects recorded", obs.Inventories)
	}
	for _, inv := range obs.Inventories {
		if inv.Answered {
			t.Fatalf("a skipped project was recorded as answered: %+v", inv)
		}
	}
	if plan := WorktreeOrphans(ctx, []Project{{Key: "a", Path: filepath.Join(base, "a")}}, ObserveOptions{}); len(plan.Orphans) != 0 {
		t.Fatalf("orphans = %+v after a cut-short pass", plan.Orphans)
	}
}

func TestObserveWorktreeOrphansUnknownPaneNeverPermitsPrune(t *testing.T) {
	for _, pane := range []string{"", "relative", "malformed"} {
		t.Run(pane, func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			repo := filepath.Join(base, "repo")
			removed := filepath.Join(base, "repo-foo")
			if err := os.Mkdir(repo, 0o755); err != nil {
				t.Fatal(err)
			}
			listing := "sidecar-ws-repo-foo\t" + removed
			if pane != "malformed" {
				listing += "\t" + pane
			}
			stubOrphanEvidence(t, listing+"\n", nil, map[string][]workspaceops.WorktreeState{repo: {{Path: repo}}})
			plan := WorktreeOrphans(t.Context(), []Project{{Key: "repo", Path: repo, Worktrees: []string{removed}}}, ObserveOptions{})
			if len(plan.Orphans) != 0 {
				t.Fatalf("unknown pane directory permitted prune: %+v", plan.Orphans)
			}
		})
	}
}
