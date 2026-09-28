package shellliveness

import "testing"

// These tests state which way an ambiguous signal falls. A false orphan is a
// live agent session killed, so every unknown must come out "not an orphan".

const wsPrefix = "sidecar-ws-"

func repoInventory(worktrees ...ListedWorktree) WorktreeInventory {
	all := append([]ListedWorktree{{Path: "/code/repo", Sessions: []string{"sidecar-ws-repo"}}}, worktrees...)
	return WorktreeInventory{ProjectRoot: "/code/repo", Answered: true, Worktrees: all}
}

func registered(root, session string) RegisteredRoot {
	return RegisteredRoot{ProjectKey: "repo", ProjectRoot: "/code/repo", Root: root, Sessions: []string{session}, Missing: true}
}

func TestRemovedWorktreeWithLiveSessionIsAnOrphan(t *testing.T) {
	plan := PlanWorktreeOrphans(OrphanObservation{
		SessionPrefix: wsPrefix,
		Inventories:   []WorktreeInventory{repoInventory()},
		Registered:    []RegisteredRoot{registered("/code/repo-foo", "sidecar-ws-repo-foo")},
		Sessions:      []TmuxSession{{Name: "sidecar-ws-repo-foo", Path: "/code/repo-foo", PathMissing: true, NameFromPath: true}},
	})
	if len(plan.Orphans) != 1 {
		t.Fatalf("orphans = %+v, want the one removed worktree", plan.Orphans)
	}
	got := plan.Orphans[0]
	if got.Session != "sidecar-ws-repo-foo" || got.Reason != OrphanNotListed || got.ProjectKey != "repo" || got.Root != "/code/repo-foo" {
		t.Fatalf("orphan = %+v", got)
	}
	if plan.Roots["/code/repo-foo"] != OrphanNotListed {
		t.Fatalf("root verdicts = %+v", plan.Roots)
	}
}

func TestPrunableWorktreeIsAnOrphan(t *testing.T) {
	plan := PlanWorktreeOrphans(OrphanObservation{
		SessionPrefix: wsPrefix,
		Inventories:   []WorktreeInventory{repoInventory(ListedWorktree{Path: "/code/repo-foo", Prunable: true, Sessions: []string{"sidecar-ws-repo-foo"}})},
		Registered:    []RegisteredRoot{registered("/code/repo-foo", "sidecar-ws-repo-foo")},
		Sessions:      []TmuxSession{{Name: "sidecar-ws-repo-foo", Path: "/code/repo-foo"}},
	})
	if len(plan.Orphans) != 1 || plan.Orphans[0].Reason != OrphanPrunable {
		t.Fatalf("orphans = %+v, want one prunable orphan", plan.Orphans)
	}
}

func TestListedWorktreeIsNeverAnOrphan(t *testing.T) {
	plan := PlanWorktreeOrphans(OrphanObservation{
		SessionPrefix: wsPrefix,
		Inventories:   []WorktreeInventory{repoInventory(ListedWorktree{Path: "/code/repo-foo", Sessions: []string{"sidecar-ws-repo-foo"}})},
		Registered:    []RegisteredRoot{registered("/code/repo-foo", "sidecar-ws-repo-foo")},
		Sessions:      []TmuxSession{{Name: "sidecar-ws-repo-foo", Path: "/code/repo-foo"}},
	})
	if len(plan.Orphans) != 0 || len(plan.Roots) != 0 {
		t.Fatalf("plan = %+v, want nothing", plan)
	}
}

// A failed git listing is what an unreadable repository and an unmounted
// volume both look like. Neither is evidence a worktree was removed.
func TestUnansweredInventoryProducesNoVerdict(t *testing.T) {
	inv := repoInventory()
	inv.Answered = false
	inv.Worktrees = nil
	plan := PlanWorktreeOrphans(OrphanObservation{
		SessionPrefix: wsPrefix,
		Inventories:   []WorktreeInventory{inv},
		Registered:    []RegisteredRoot{registered("/code/repo-foo", "sidecar-ws-repo-foo")},
		Sessions:      []TmuxSession{{Name: "sidecar-ws-repo-foo", Path: "/code/repo-foo", PathMissing: true, NameFromPath: true}},
	})
	if len(plan.Orphans) != 0 || len(plan.Roots) != 0 {
		t.Fatalf("plan = %+v, want nothing from an unanswered inventory", plan)
	}
}

// Another repository still listing the root means it is not gone.
func TestRootListedByAnotherRepositoryIsNotAnOrphan(t *testing.T) {
	other := WorktreeInventory{ProjectRoot: "/code/other", Answered: true, Worktrees: []ListedWorktree{{Path: "/code/repo-foo"}}}
	reason := RootOrphanReason(registered("/code/repo-foo", "sidecar-ws-repo-foo"), []WorktreeInventory{repoInventory(), other})
	if reason != "" {
		t.Fatalf("reason = %q, want none", reason)
	}
}

// Session names come from a directory's base name. `repo-foo` recreated at a
// different path reuses the removed worktree's session name, and the live
// session is the new checkout's.
func TestSessionNameClaimedByAListedWorktreeIsNotAnOrphan(t *testing.T) {
	plan := PlanWorktreeOrphans(OrphanObservation{
		SessionPrefix: wsPrefix,
		Inventories:   []WorktreeInventory{repoInventory(ListedWorktree{Path: "/elsewhere/repo-foo", Sessions: []string{"sidecar-ws-repo-foo"}})},
		Registered:    []RegisteredRoot{registered("/code/repo-foo", "sidecar-ws-repo-foo")},
		Sessions:      []TmuxSession{{Name: "sidecar-ws-repo-foo", Path: "/elsewhere/repo-foo"}},
	})
	if len(plan.Orphans) != 0 {
		t.Fatalf("orphans = %+v, the session belongs to the recreated worktree", plan.Orphans)
	}
	if plan.Roots["/code/repo-foo"] != OrphanNotListed {
		t.Fatal("the removed root itself should still have a verdict")
	}
}

// A matching name alone is not proof. The session must have been started in
// the removed root.
func TestSessionStartedElsewhereIsNotAnOrphan(t *testing.T) {
	for _, path := range []string{"", "/code/repo-foo-2", "/tmp"} {
		plan := PlanWorktreeOrphans(OrphanObservation{
			SessionPrefix: wsPrefix,
			Inventories:   []WorktreeInventory{repoInventory()},
			Registered:    []RegisteredRoot{registered("/code/repo-foo", "sidecar-ws-repo-foo")},
			Sessions:      []TmuxSession{{Name: "sidecar-ws-repo-foo", Path: path}},
		})
		if len(plan.Orphans) != 0 {
			t.Fatalf("session path %q: orphans = %+v", path, plan.Orphans)
		}
	}
}

func TestSessionStartedInASubdirectoryOfTheRootIsAnOrphan(t *testing.T) {
	plan := PlanWorktreeOrphans(OrphanObservation{
		SessionPrefix: wsPrefix,
		Inventories:   []WorktreeInventory{repoInventory()},
		Registered:    []RegisteredRoot{registered("/code/repo-foo", "sidecar-ws-repo-foo")},
		Sessions:      []TmuxSession{{Name: "sidecar-ws-repo-foo", Path: "/code/repo-foo/sub"}},
	})
	if len(plan.Orphans) != 1 {
		t.Fatalf("orphans = %+v", plan.Orphans)
	}
}

func TestFailedTmuxListingProducesNoOrphans(t *testing.T) {
	plan := PlanWorktreeOrphans(OrphanObservation{
		SessionPrefix: wsPrefix,
		Inventories:   []WorktreeInventory{repoInventory()},
		Registered:    []RegisteredRoot{registered("/code/repo-foo", "sidecar-ws-repo-foo")},
		Sessions:      []TmuxSession{{Name: "sidecar-ws-repo-foo", Path: "/code/repo-foo"}},
		ListingFailed: true,
	})
	if len(plan.Orphans) != 0 || plan.Skipped == "" {
		t.Fatalf("plan = %+v, want a skipped pass", plan)
	}
	// The git half still stands on its own.
	if plan.Roots["/code/repo-foo"] != OrphanNotListed {
		t.Fatal("root verdicts should not depend on tmux")
	}
}

func TestOnlyWorktreeSessionsAreJudged(t *testing.T) {
	plan := PlanWorktreeOrphans(OrphanObservation{
		SessionPrefix: wsPrefix,
		Sessions:      []TmuxSession{{Name: "sidecar-sh-repo-1", Path: "/gone", PathMissing: true, NameFromPath: true}},
	})
	if len(plan.Orphans) != 0 {
		t.Fatalf("orphans = %+v", plan.Orphans)
	}
}

// A worktree that was only ever discovered through git has no registered
// record, so once git forgets it the session's own directory is the only
// identity it has. That is enough when the directory is gone and the name is
// derived from it.
func TestUnattributedSessionWithMissingDirectoryIsAnOrphan(t *testing.T) {
	plan := PlanWorktreeOrphans(OrphanObservation{
		SessionPrefix: wsPrefix,
		Inventories:   []WorktreeInventory{repoInventory()},
		Sessions: []TmuxSession{
			{Name: "sidecar-ws-gone", Path: "/code/gone", PathMissing: true, NameFromPath: true},
			{Name: "sidecar-ws-present", Path: "/code/present"},
			{Name: "sidecar-ws-renamed", Path: "/code/gone-too", PathMissing: true, NameFromPath: false},
		},
	})
	if len(plan.Orphans) != 1 || plan.Orphans[0].Session != "sidecar-ws-gone" || plan.Orphans[0].Reason != OrphanDirectoryMissing {
		t.Fatalf("orphans = %+v, want only sidecar-ws-gone", plan.Orphans)
	}
	if plan.Orphans[0].ProjectKey != "" {
		t.Fatal("an unattributed orphan must not claim a project")
	}
}

// A missing directory does not overrule a registered root whose owner could
// not answer: that is exactly the unmounted-volume case.
func TestMissingDirectoryInsideAnUnjudgedRegisteredRootIsNotAnOrphan(t *testing.T) {
	inv := repoInventory()
	inv.Answered = false
	plan := PlanWorktreeOrphans(OrphanObservation{
		SessionPrefix: wsPrefix,
		Inventories:   []WorktreeInventory{inv},
		Registered:    []RegisteredRoot{registered("/code/repo-foo", "sidecar-ws-repo-foo-old")},
		Sessions:      []TmuxSession{{Name: "sidecar-ws-repo-foo", Path: "/code/repo-foo", PathMissing: true, NameFromPath: true}},
	})
	if len(plan.Orphans) != 0 {
		t.Fatalf("orphans = %+v", plan.Orphans)
	}
}

func TestPathWithinComparesComponents(t *testing.T) {
	cases := []struct {
		path, root string
		want       bool
	}{
		{"/a/feature", "/a/feature", true},
		{"/a/feature/x", "/a/feature", true},
		{"/a/feature-2", "/a/feature", false},
		{"/a", "/a/feature", false},
		{"", "/a", false},
	}
	for _, c := range cases {
		if got := PathWithin(c.path, c.root); got != c.want {
			t.Errorf("PathWithin(%q, %q) = %v, want %v", c.path, c.root, got, c.want)
		}
	}
}

// A directory re-cloned over a removed worktree is not in git's worktree list
// and is very much alive.
func TestRootThatStillExistsIsNeverAnOrphan(t *testing.T) {
	root := registered("/code/repo-foo", "sidecar-ws-repo-foo")
	root.Missing = false
	plan := PlanWorktreeOrphans(OrphanObservation{
		SessionPrefix: wsPrefix,
		Inventories:   []WorktreeInventory{repoInventory()},
		Registered:    []RegisteredRoot{root},
		Sessions:      []TmuxSession{{Name: "sidecar-ws-repo-foo", Path: "/code/repo-foo"}},
	})
	if len(plan.Orphans) != 0 || len(plan.Roots) != 0 {
		t.Fatalf("plan = %+v, want nothing for an existing root", plan)
	}
}

// A project that lost its .git inside a parent repository answers with the
// parent's listing, which does not include the project itself.
func TestOwnerListingWithoutItsOwnRootIsNotAnAnswer(t *testing.T) {
	parent := WorktreeInventory{ProjectRoot: "/code/repo", Answered: true, Worktrees: []ListedWorktree{{Path: "/home"}}}
	if reason := RootOrphanReason(registered("/code/repo-foo", "sidecar-ws-repo-foo"), []WorktreeInventory{parent}); reason != "" {
		t.Fatalf("reason = %q, want none", reason)
	}
}

// WorktreeSessionNames yields two spellings for a name like My_Feature; either
// one live in the removed root is an orphan.
func TestEitherSessionSpellingIsJudged(t *testing.T) {
	root := registered("/code/My_Feature", "sidecar-ws-my-feature")
	root.Sessions = append(root.Sessions, "sidecar-ws-My_Feature")
	plan := PlanWorktreeOrphans(OrphanObservation{
		SessionPrefix: wsPrefix,
		Inventories:   []WorktreeInventory{repoInventory()},
		Registered:    []RegisteredRoot{root},
		Sessions:      []TmuxSession{{Name: "sidecar-ws-My_Feature", Path: "/code/My_Feature"}},
	})
	if len(plan.Orphans) != 1 || plan.Orphans[0].Session != "sidecar-ws-My_Feature" {
		t.Fatalf("orphans = %+v", plan.Orphans)
	}
}

// A moved worktree: the start directory is gone, but the agent followed the
// checkout and is working somewhere real.
func TestSessionWithAPaneInAnExistingDirectoryIsNotAnOrphan(t *testing.T) {
	plan := PlanWorktreeOrphans(OrphanObservation{
		SessionPrefix: wsPrefix,
		Inventories:   []WorktreeInventory{repoInventory()},
		Registered:    []RegisteredRoot{registered("/code/repo-foo", "sidecar-ws-repo-foo")},
		Sessions: []TmuxSession{
			{Name: "sidecar-ws-repo-foo", Path: "/code/repo-foo", PathMissing: true, NameFromPath: true, PaneInExistingDir: true},
			{Name: "sidecar-ws-loose", Path: "/code/loose", PathMissing: true, NameFromPath: true, PaneInExistingDir: true},
		},
	})
	if len(plan.Orphans) != 0 {
		t.Fatalf("orphans = %+v", plan.Orphans)
	}
}

// With one listing missing, nobody can say which names that repository's live
// worktrees claim, so an unattributed session is not judged.
func TestUnattributedSessionNeedsEveryInventoryToAnswer(t *testing.T) {
	failed := WorktreeInventory{ProjectRoot: "/code/other"}
	plan := PlanWorktreeOrphans(OrphanObservation{
		SessionPrefix: wsPrefix,
		Inventories:   []WorktreeInventory{repoInventory(), failed},
		Sessions:      []TmuxSession{{Name: "sidecar-ws-gone", Path: "/code/gone", PathMissing: true, NameFromPath: true}},
	})
	if len(plan.Orphans) != 0 {
		t.Fatalf("orphans = %+v", plan.Orphans)
	}
}
