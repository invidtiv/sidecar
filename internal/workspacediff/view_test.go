package workspacediff

import (
	"errors"
	"strings"
	"testing"
)

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

func TestApplyCommitDetailInstallsCommitRoot(t *testing.T) {
	v := &View{
		Target: MustParse("abc1234"),
		State:  LoadStateLoading,
	}
	v.Bind("/tmp", "ws", 1)
	cmd := v.ApplyCommitDetail(CommitDetailMsg{
		Epoch: 1, WorkspaceID: "ws", Identity: "c:abc1234",
		Hash: "abc1234",
		Commit: &CommitDetail{
			Hash:      "abc1234bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			ShortHash: "abc1234",
			Subject:   "one",
			Files:     []CommitFile{{Path: "a.go", Status: "M"}},
		},
	})
	_ = cmd
	if v.CommitDetail == nil || v.CommitDetail.Subject != "one" {
		t.Fatalf("CommitDetail = %#v, want installed", v.CommitDetail)
	}
	if v.State == LoadStateLoading {
		t.Fatal("state stayed Loading")
	}
	if v.Focus != FocusCommitFiles {
		t.Fatalf("focus = %v, want FocusCommitFiles", v.Focus)
	}
	got := v.Render(80, 12, RenderOpts{})
	if strings.Contains(got, "Loading diff…") {
		t.Fatalf("render still loading: %q", got)
	}
	if strings.Contains(got, "Working Tree vs HEAD") {
		t.Fatalf("commit-root rendered working-tree chrome: %q", got)
	}
	if !strings.Contains(got, "Commit") || !strings.Contains(got, "abc1234") || !strings.Contains(got, "one") {
		t.Fatalf("commit chrome missing: %q", got)
	}
}

func TestApplyCommitDetailCommitRootErrorLeavesLoading(t *testing.T) {
	v := &View{Target: MustParse("abc1234"), State: LoadStateLoading}
	v.Bind("/tmp", "ws", 1)
	v.ApplyCommitDetail(CommitDetailMsg{
		Epoch: 1, WorkspaceID: "ws", Identity: "c:abc1234",
		Hash: "abc1234", Err: errors.New("missing object"),
	})
	if v.State != LoadStateError {
		t.Fatalf("state = %v, want Error", v.State)
	}
	if v.CommitDetail != nil {
		t.Fatal("error path installed a commit")
	}
	got := v.Render(40, 6, RenderOpts{})
	if strings.Contains(got, "Loading diff…") {
		t.Fatalf("error still renders loading: %q", got)
	}
}

func TestApplyCommitDetailDropsIdentityMismatchOnCommitRoot(t *testing.T) {
	v := &View{Target: MustParse("abc1234"), State: LoadStateLoading}
	v.Bind("/tmp", "ws", 1)
	v.ApplyCommitDetail(CommitDetailMsg{
		Epoch: 1, WorkspaceID: "ws", Identity: "c:deadbee",
		Hash:   "deadbee",
		Commit: &CommitDetail{Hash: "deadbee", ShortHash: "deadbee", Subject: "other"},
	})
	if v.CommitDetail != nil || v.State != LoadStateLoading {
		t.Fatalf("mismatch applied: detail=%#v state=%v", v.CommitDetail, v.State)
	}
}

func TestApplyRangeMsgInstallsFilesAndRefusesSnapshot(t *testing.T) {
	v := &View{Target: MustParse("aaa1111..bbb2222"), State: LoadStateLoading}
	v.Bind("/tmp", "ws", 1)
	v.ApplyRangeMsg(RangeMsg{
		Epoch: 1, WorkspaceID: "ws", Identity: "r:aaa1111..bbb2222",
		Raw: "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -0,0 +1 @@\n+hi\n",
	})
	if v.State == LoadStateLoading {
		t.Fatal("range stayed Loading")
	}
	if len(v.Files) != 1 || v.Files[0].Path != "a.go" {
		t.Fatalf("range files = %#v", v.Files)
	}
	if v.Commits != nil || v.CommitDetail != nil || v.Snapshot != nil {
		t.Fatal("range tab grew a commits list or snapshot")
	}
	got := v.Render(140, 12, RenderOpts{})
	if strings.Contains(got, "Loading diff…") || strings.Contains(got, "Working Tree vs HEAD") {
		t.Fatalf("range chrome wrong: %q", got)
	}
	if !strings.Contains(got, "aaa1111") || !strings.Contains(got, "bbb2222") {
		t.Fatalf("range label missing: %q", got)
	}

	v.ApplySnapshotMsg(SnapshotMsg{
		Epoch: 1, WorkspaceID: "ws", Identity: "r:aaa1111..bbb2222",
		Snapshot: &Snapshot{State: LoadStateReady, WorkingTree: "diff --git a/wt.go b/wt.go\n"},
	}, "/tmp", "ws")
	if len(v.Files) != 1 || v.Files[0].Path != "a.go" {
		t.Fatalf("snapshot applied onto range tab: %#v", v.Files)
	}
}

func TestApplyRangeMsgError(t *testing.T) {
	v := &View{Target: MustParse("aaa1111...bbb2222"), State: LoadStateLoading}
	v.Bind("/tmp", "ws", 1)
	v.ApplyRangeMsg(RangeMsg{
		Epoch: 1, WorkspaceID: "ws", Identity: "r:aaa1111...bbb2222",
		Err: errors.New("bad rev"),
	})
	if v.State != LoadStateError {
		t.Fatalf("state = %v, want Error", v.State)
	}
	if strings.Contains(v.Render(40, 4, RenderOpts{}), "Loading diff…") {
		t.Fatal("range error still loading")
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
