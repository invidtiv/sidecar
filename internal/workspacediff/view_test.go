package workspacediff

import "testing"

func TestCommitDetailMatchesListHash(t *testing.T) {
	const short, full = "aaa1111", "aaa1111bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if CommitDetailMatchesListHash(nil, short) {
		t.Fatal("nil detail matched")
	}
	if !CommitDetailMatchesListHash(&CommitDetail{Hash: full, ShortHash: short}, short) {
		t.Fatal("list %h should match ShortHash")
	}
	if !CommitDetailMatchesListHash(&CommitDetail{Hash: full}, short) {
		t.Fatal("list %h should be a prefix of detail %H")
	}
	if CommitDetailMatchesListHash(&CommitDetail{Hash: full, ShortHash: short}, "bbb2222") {
		t.Fatal("different hash matched")
	}
}

func TestApplySnapshotLoadsFirstCommitWithoutCursorMove(t *testing.T) {
	v := &View{Scope: ScopeWorkingTree, Cursor: 0}
	v.Snapshot = &Snapshot{
		State: LoadStateReady,
		Commits: []CommitInfo{
			{Hash: "aaa1111", Subject: "first"},
			{Hash: "bbb2222", Subject: "second"},
		},
	}
	v.ApplySnapshot()
	if v.Cursor != 0 {
		t.Fatalf("cursor = %d, want 0", v.Cursor)
	}
	if v.FileCount() != 0 {
		t.Fatalf("file count = %d, want 0 so cursor sits on first commit", v.FileCount())
	}
	cmd := v.LoadSelectedCommit("/tmp", "wt")
	if cmd == nil {
		t.Fatal("cursor on first commit did not issue LoadSelectedCommit")
	}
	msg := cmd()
	loaded, ok := msg.(CommitDetailMsg)
	if !ok {
		t.Fatalf("cmd produced %T, want CommitDetailMsg", msg)
	}
	if loaded.Hash != "aaa1111" {
		t.Fatalf("loaded hash = %q, want first commit aaa1111", loaded.Hash)
	}
}

func TestLoadSelectedCommitSkipsAlreadyLoadedHash(t *testing.T) {
	v := &View{
		Commits:          []CommitInfo{{Hash: "aaa1111", Subject: "first"}},
		Cursor:           0,
		CommitDetail:     &CommitDetail{Hash: "aaa1111bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", ShortHash: "aaa1111"},
		CommitFileCursor: 2,
	}
	if cmd := v.LoadSelectedCommit("/tmp", "wt"); cmd != nil {
		t.Fatal("already-loaded commit under cursor should not refetch")
	}
	if v.CommitFileCursor != 2 {
		t.Fatalf("skip reset CommitFileCursor to %d, want 2", v.CommitFileCursor)
	}
	v.CommitDetail = &CommitDetail{Hash: "aaa1111bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	if cmd := v.LoadSelectedCommit("/tmp", "wt"); cmd != nil {
		t.Fatal("full-hash prefix of list short hash should skip")
	}
}

func TestApplySnapshotDoesNotLoadCommitWhenCursorOnFile(t *testing.T) {
	v := &View{Scope: ScopeWorkingTree, Cursor: 0}
	v.Snapshot = &Snapshot{
		State:       LoadStateReady,
		WorkingTree: "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -0,0 +1 @@\n+hi\n",
		Commits:     []CommitInfo{{Hash: "aaa1111", Subject: "first"}},
	}
	v.ApplySnapshot()
	if v.FileCount() == 0 {
		t.Fatal("expected working-tree files so cursor sits on a file")
	}
	if cmd := v.LoadSelectedCommit("/tmp", "wt"); cmd != nil {
		t.Fatal("file-under-cursor issued LoadSelectedCommit")
	}
}

func TestTabsVisible(t *testing.T) {
	if TabsVisible(true, false) {
		t.Fatal("project-plugin shell should have no tabs")
	}
	if TabsVisible(false, true) {
		t.Fatal("main worktree should have no tabs")
	}
	if !TabsVisible(false, false) {
		t.Fatal("non-main worktree should have tabs")
	}
}

func TestGlobalTabsFor(t *testing.T) {
	if GlobalTabsFor(true, false) != TabSetOutputDiff {
		t.Fatal("global shell should be Output+Diff")
	}
	if GlobalTabsFor(false, false) != TabSetOutputDiffTask {
		t.Fatal("global topic worktree should keep Task")
	}
	if GlobalTabsFor(false, true) != TabSetNone {
		t.Fatal("global main worktree should stay tabless")
	}
	if GlobalTabsFor(true, false).Contains(TabTask) {
		t.Fatal("global shell must not include Task")
	}
}

func TestCycleTab(t *testing.T) {
	if got := CycleTab(TabDiff, -1); got != TabOutput {
		t.Fatalf("comma from Diff = %v, want Output", got)
	}
	if got := CycleTab(TabOutput, 1); got != TabDiff {
		t.Fatalf("period from Output = %v, want Diff", got)
	}
	if got := CycleTab(TabTask, 1); got != TabOutput {
		t.Fatalf("period from Task wraps to Output, got %v", got)
	}
}

func TestCycleTabInShellSkipsTask(t *testing.T) {
	if got := CycleTabIn(TabOutput, 1, TabSetOutputDiff); got != TabDiff {
		t.Fatalf("period from Output = %v, want Diff", got)
	}
	if got := CycleTabIn(TabDiff, 1, TabSetOutputDiff); got != TabOutput {
		t.Fatalf("period from Diff wrapped to %v, want Output (not Task)", got)
	}
	if got := CycleTabIn(TabTask, 1, TabSetOutputDiff); got != TabDiff {
		t.Fatalf("stale Task tab cycled to %v, want Diff", got)
	}
}

func TestTabChipsMarksActive(t *testing.T) {
	chips := TabChips(TabDiff)
	if len(chips) != 3 {
		t.Fatalf("chips = %d, want 3", len(chips))
	}
	shell := TabChipsFor(TabDiff, TabSetOutputDiff)
	if len(shell) != 2 {
		t.Fatalf("shell chips = %d, want 2", len(shell))
	}
}

func TestRenderTaskOmitsLinkHintWhenEmpty(t *testing.T) {
	view, _ := RenderTask(TaskView{}, TaskRenderOpts{Width: 40, Height: 10})
	if !contains(view, "No linked task") {
		t.Fatalf("empty task view = %q, want No linked task", view)
	}
	if contains(view, "Press") {
		t.Fatalf("global empty hint leaked a link key: %q", view)
	}
	view, _ = RenderTask(TaskView{}, TaskRenderOpts{Width: 40, Height: 10, EmptyHint: "Press 't' to link a task"})
	if !contains(view, "Press 't' to link a task") {
		t.Fatalf("plugin empty hint missing: %q", view)
	}
}

func TestDiffContentScrollClampsToRenderedViewport(t *testing.T) {
	v := View{
		Content: "diff --git a/a b/a\none\ntwo\nthree\nfour\nfive",
		Files:   []File{{Path: "a", Raw: "one\ntwo\nthree\nfour\nfive"}},
	}
	const height = 4 // two body rows after the file header and spacer
	if !v.ScrollAtBoundary(-1, height) {
		t.Fatal("top was not a boundary")
	}
	v.ScrollContent(1000, height)
	if got, want := v.DiffScroll, 3; got != want {
		t.Fatalf("overscroll = %d, want clamped %d", got, want)
	}
	if !v.ScrollAtBoundary(1, height) {
		t.Fatal("bottom was not a boundary")
	}
	v.ScrollContent(-1, height)
	if got := v.DiffScroll; got != 2 {
		t.Fatalf("first reverse step after bottom = %d, want 2", got)
	}
}

func TestTaskScrollUsesVisibleBottomBoundary(t *testing.T) {
	task := TaskView{LineCount: 10}
	task.Scroll(1000, 4)
	if got, want := task.Offset, 6; got != want {
		t.Fatalf("overscroll = %d, want clamped %d", got, want)
	}
	if !task.ScrollAtBoundary(1, 4) {
		t.Fatal("task bottom was not a boundary")
	}
	task.Scroll(-1, 4)
	if got := task.Offset; got != 5 {
		t.Fatalf("first reverse step after bottom = %d, want 5", got)
	}
}

func contains(s, sub string) bool {
	return stringIndex(s, sub) >= 0
}

func stringIndex(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
