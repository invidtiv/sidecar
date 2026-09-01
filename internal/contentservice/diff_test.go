package contentservice

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/workspacediff"
)

func commitContentRepo(t *testing.T, root string) string {
	t.Helper()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "tracked.txt")
	run("commit", "-m", "base")
	cmd := exec.Command("git", "-C", root, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func TestServiceDiffResolveAndWorkingTree(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	initGitRepo(t, root)
	oid := commitContentRepo(t, root)
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("base\nhost-dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	id := canonical(root) + ":worktree:" + canonical(root)
	svc := testService(t, root, nil, &gitRecorder{})

	resolved, err := svc.Resolve(context.Background(), id, KindDiff, "wt")
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.ValidRemoteResult() || resolved.Target != workspacediff.IdentityWorkingTree {
		t.Fatalf("wt resolve = %+v", resolved)
	}

	resolved, err = svc.Resolve(context.Background(), id, KindDiff, oid[:7])
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.ValidRemoteResult() || !strings.HasPrefix(resolved.Target, "c:") {
		t.Fatalf("commit resolve = %+v", resolved)
	}

	read, err := svc.Read(context.Background(), id, KindDiff, OpWorkingTree, "wt", "")
	if err != nil {
		t.Fatal(err)
	}
	if !read.ValidRemoteResult() || read.Diff == nil || read.Diff.Snapshot == nil {
		t.Fatalf("working-tree = %+v", read)
	}
	if !strings.Contains(read.Diff.Snapshot.WorkingTree, "host-dirty") {
		t.Fatalf("working-tree missing dirty change: %+v", read.Diff.Snapshot)
	}

	cached, err := svc.Read(context.Background(), id, KindDiff, OpWorkingTree, "wt", read.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if !cached.NotModified || !cached.ValidRemoteResult() || cached.Revision != read.Revision {
		t.Fatalf("notModified = %+v", cached)
	}

	commit, err := svc.Read(context.Background(), id, KindDiff, OpCommit, oid, "")
	if err != nil {
		t.Fatal(err)
	}
	if !commit.ValidRemoteResult() || commit.Diff == nil || commit.Diff.Commit == nil {
		t.Fatalf("commit = %+v", commit)
	}
	if commit.Diff.Commit.Hash != oid && commit.Diff.Commit.ShortHash != oid[:7] {
		t.Fatalf("commit hash = %+v want %s", commit.Diff.Commit, oid)
	}
}

func TestServiceDiffUnknownOperation(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	initGitRepo(t, root)
	id := canonical(root) + ":worktree:" + canonical(root)
	svc := testService(t, root, nil, &gitRecorder{})
	_, err := svc.Read(context.Background(), id, KindDiff, "exec", "wt", "")
	if err == nil {
		t.Fatal("unknown operation succeeded")
	}
	var coded *Error
	if !errors.As(err, &coded) || coded.ExitCode() != 2 {
		t.Fatalf("err = %v", err)
	}
}

func TestDiffValidRemoteResult(t *testing.T) {
	t.Parallel()
	okResolve := ResolveResult{Kind: KindDiff, Workspace: "p:worktree:/p", Target: "wt"}
	if !okResolve.ValidRemoteResult() {
		t.Fatalf("wt resolve refused: %+v", okResolve)
	}
	okRead := ReadResult{
		Kind: KindDiff, Operation: OpWorkingTree, Workspace: "p:worktree:/p",
		Revision: "v1:abc", Diff: &DiffDTO{Target: "wt", Snapshot: &DiffSnapshotDTO{WorkingTree: "x"}},
	}
	if !okRead.ValidRemoteResult() {
		t.Fatalf("working-tree read refused: %+v", okRead)
	}
	okNM := ReadResult{Kind: KindDiff, NotModified: true, Revision: "v1:abc"}
	if !okNM.ValidRemoteResult() {
		t.Fatal("diff notModified refused")
	}
	logLine := []byte(`{"level":"info","msg":"loading nvm","path":"/usr/local/nvm"}`)
	var read ReadResult
	if err := json.Unmarshal(logLine, &read); err != nil {
		t.Fatal(err)
	}
	if read.ValidRemoteResult() {
		t.Fatalf("log line passed for diff read: %+v", read)
	}
}

func TestEncodeDiffTruncatesBeforeTransportCap(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("\"", 400<<10)
	result := ReadResult{
		Kind: KindDiff, Operation: OpWorkingTree, Workspace: "p:worktree:/p",
		Revision: "v1:abc", Diff: &DiffDTO{
			Target:   "wt",
			Snapshot: &DiffSnapshotDTO{WorkingTree: body},
		},
	}
	raw, err := EncodeReadResult(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > MaxEncodedBytes {
		t.Fatalf("encoded %d bytes, cap %d", len(raw), MaxEncodedBytes)
	}
	var decoded ReadResult
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if !decoded.ValidRemoteResult() {
		t.Fatalf("encoded diff failed ValidRemoteResult: %+v", decoded)
	}
	if !decoded.Truncated && !decoded.Oversize {
		t.Fatal("large diff was not truncated")
	}
}
