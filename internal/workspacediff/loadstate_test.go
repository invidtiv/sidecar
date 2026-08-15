package workspacediff

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A commit-file patch that comes back empty is not the same thing as a patch
// that has not come back yet, and the pane must not say "Loading" for it.
func TestCommitFileDiffDistinguishesLoadedEmptyFromLoading(t *testing.T) {
	v := &View{
		State: LoadStateReady,
		Focus: FocusCommitDiff,
		CommitDetail: &CommitDetail{
			Hash:  "abc123",
			Files: []CommitFile{{Path: "a.go", Status: "M"}},
		},
	}
	v.SetSize(60, 10)

	if got := v.commitFileDiffPlaceholder(60, RenderOpts{}); !strings.Contains(got, "Loading") {
		t.Fatalf("before the load lands: %q, want a loading message", got)
	}

	v.ApplyCommitFileDiff(CommitFileDiffMsg{CommitHash: "abc123", FilePath: "a.go", Raw: ""})
	got := v.commitFileDiffPlaceholder(60, RenderOpts{})
	if strings.Contains(got, "Loading") {
		t.Fatalf("after an empty load: %q, still claims to be loading", got)
	}
	if !strings.Contains(got, "No textual diff") {
		t.Fatalf("after an empty load: %q, want an explanation", got)
	}
}

func TestCommitFileDiffExplainsAMergeCommit(t *testing.T) {
	v := &View{
		State: LoadStateReady,
		Focus: FocusCommitDiff,
		CommitDetail: &CommitDetail{
			Hash: "abc123", IsMerge: true, ParentHashes: []string{"p1", "p2"},
			Files: []CommitFile{{Path: "a.go", Status: "M"}},
		},
	}
	v.ApplyCommitFileDiff(CommitFileDiffMsg{CommitHash: "abc123", FilePath: "a.go", Raw: ""})
	got := v.commitFileDiffPlaceholder(80, RenderOpts{})
	if !strings.Contains(got, "Merge commit") || !strings.Contains(got, "parent") {
		t.Fatalf("merge placeholder = %q, want the combined-diff explanation", got)
	}
}

func TestCommitFileDiffReportsAFailedLoad(t *testing.T) {
	v := &View{
		State:        LoadStateReady,
		Focus:        FocusCommitDiff,
		CommitDetail: &CommitDetail{Hash: "abc123", Files: []CommitFile{{Path: "a.go"}}},
	}
	v.ApplyCommitFileDiff(CommitFileDiffMsg{CommitHash: "abc123", FilePath: "a.go", Err: errors.New("exit status 128")})
	got := v.commitFileDiffPlaceholder(80, RenderOpts{})
	if strings.Contains(got, "Loading") {
		t.Fatalf("failed load renders %q, want the failure", got)
	}
	if !strings.Contains(got, "exit status 128") {
		t.Fatalf("failed load renders %q, want the git error", got)
	}
}

// A commit whose detail load failed must say so rather than sit on "Loading
// commit files…" for the rest of the session.
func TestCommitDetailFailureIsRendered(t *testing.T) {
	v := &View{
		State:   LoadStateReady,
		Commits: []CommitInfo{{Hash: "aaa1111", Subject: "first"}},
		Cursor:  0,
	}
	if got := v.commitDetailPlaceholder(60, RenderOpts{}); !strings.Contains(got, "Loading") {
		t.Fatalf("before the load lands: %q", got)
	}
	v.ApplyCommitDetail(CommitDetailMsg{Hash: "aaa1111", Err: errors.New("bad object")})
	got := v.commitDetailPlaceholder(60, RenderOpts{})
	if strings.Contains(got, "Loading") {
		t.Fatalf("after a failed load: %q, still claims to be loading", got)
	}
	if !strings.Contains(got, "bad object") {
		t.Fatalf("after a failed load: %q, want the git error", got)
	}
}

func TestResetCommitDetailForgetsTheFailure(t *testing.T) {
	v := &View{CommitDetailErr: "bad object", CommitFileDiffErr: "boom", CommitFileDiffLoaded: true}
	v.resetCommitDetail()
	if v.CommitDetailErr != "" || v.CommitFileDiffErr != "" || v.CommitFileDiffLoaded {
		t.Fatalf("stale failure survived the reset: %+v", v)
	}
}

// LoadCommitDetail must report a merge's parents: without them the per-file
// load runs `git show <merge> -- path`, which is a combined diff and is empty
// for every path that was not a conflict resolution.
func TestLoadCommitDetailReportsMergeParents(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-b", "main")
	write("base.txt", "base\n")
	run("add", ".")
	run("commit", "-m", "base")
	run("checkout", "-b", "side")
	write("side.txt", "side\n")
	run("add", ".")
	run("commit", "-m", "side")
	run("checkout", "main")
	write("main.txt", "main\n")
	run("add", ".")
	run("commit", "-m", "main")
	run("merge", "--no-ff", "-m", "merge side", "side")

	head := exec.Command("git", "rev-parse", "HEAD")
	head.Dir = dir
	out, err := head.Output()
	if err != nil {
		t.Fatal(err)
	}
	hash := strings.TrimSpace(string(out))

	detail, err := LoadCommitDetail(context.Background(), dir, hash)
	if err != nil {
		t.Fatalf("LoadCommitDetail: %v", err)
	}
	if detail == nil {
		t.Fatal("no detail for the merge commit")
	}
	if !detail.IsMerge {
		t.Fatalf("IsMerge = false for %s", hash)
	}
	if len(detail.ParentHashes) != 2 {
		t.Fatalf("ParentHashes = %v, want two", detail.ParentHashes)
	}
	if detail.Subject != "merge side" {
		t.Fatalf("Subject = %q, want %q", detail.Subject, "merge side")
	}

	// With the parents recorded, the per-file load produces a real patch.
	v := &View{WorkDir: dir, CommitDetail: detail, Focus: FocusCommitDiff}
	v.CommitDetail.Files = []CommitFile{{Path: "side.txt", Status: "A"}}
	cmd := v.LoadSelectedCommitFile()
	if cmd == nil {
		t.Fatal("no load issued")
	}
	msg, ok := cmd().(CommitFileDiffMsg)
	if !ok {
		t.Fatalf("unexpected msg %T", msg)
	}
	if msg.Err != nil {
		t.Fatalf("load failed: %v", msg.Err)
	}
	if !strings.Contains(msg.Raw, "side.txt") {
		t.Fatalf("merge file patch is empty:\n%s", msg.Raw)
	}
}

// A non-merge commit still resolves and still loads its file patch.
func TestLoadCommitDetailOnASingleParentCommit(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "only")

	detail, err := LoadCommitDetail(context.Background(), dir, "HEAD")
	if err != nil {
		t.Fatalf("LoadCommitDetail: %v", err)
	}
	if detail == nil {
		t.Fatal("no detail")
	}
	if detail.IsMerge || len(detail.ParentHashes) != 0 {
		t.Fatalf("root commit reported parents %v (merge=%v)", detail.ParentHashes, detail.IsMerge)
	}
	if detail.Subject != "only" {
		t.Fatalf("Subject = %q", detail.Subject)
	}
	if len(detail.Files) != 1 || detail.Files[0].Path != "a.txt" {
		t.Fatalf("Files = %+v", detail.Files)
	}
}
