package reposervice

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/contentservice"
)

// The whole point of the staging axis: one path, two changes, two patches. A
// viewer that got the staged patch where it asked for the unstaged one would be
// wrong in the quietest way available.
func TestDiffAnswersTheStagedAndUnstagedSensesOfOnePathSeparately(t *testing.T) {
	t.Parallel()
	_, id, svc := repoFixture(t)
	ctx := context.Background()

	staged, err := svc.Diff(ctx, id, "tracked.txt", ModeStaged)
	if err != nil {
		t.Fatal(err)
	}
	unstaged, err := svc.Diff(ctx, id, "tracked.txt", ModeUnstaged)
	if err != nil {
		t.Fatal(err)
	}
	if !staged.ValidRemoteResult() || staged.Mode != ModeStaged || staged.Path != "tracked.txt" {
		t.Fatalf("staged = %+v", staged)
	}
	if !strings.Contains(staged.Patch, "+STAGED") || strings.Contains(staged.Patch, "+UNSTAGED") {
		t.Fatalf("staged patch carried the wrong change:\n%s", staged.Patch)
	}
	if !strings.Contains(unstaged.Patch, "+UNSTAGED") || strings.Contains(unstaged.Patch, "+STAGED") {
		t.Fatalf("unstaged patch carried the wrong change:\n%s", unstaged.Patch)
	}
	if staged.Patch == unstaged.Patch {
		t.Fatal("the two senses of one path returned the same patch")
	}
}

func TestDiffRendersAnUntrackedFileAsAnAddition(t *testing.T) {
	t.Parallel()
	_, id, svc := repoFixture(t)

	result, err := svc.Diff(context.Background(), id, "untracked.txt", ModeUntracked)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result.Patch, "diff --git") || !strings.Contains(result.Patch, "+UNTRACKED") {
		t.Fatalf("untracked patch = %q", result.Patch)
	}
	if !strings.Contains(result.Patch, "new file") {
		t.Fatalf("untracked patch does not say the file is new:\n%s", result.Patch)
	}
}

func TestDiffRequiresAnExplicitMode(t *testing.T) {
	t.Parallel()
	_, id, svc := repoFixture(t)

	for _, mode := range []string{"", "both", "head"} {
		_, err := svc.Diff(context.Background(), id, "tracked.txt", mode)
		var coded *contentservice.Error
		if err == nil || !asError(err, &coded) || coded.ExitCode() != 2 {
			t.Fatalf("mode %q = %v, want a usage error", mode, err)
		}
	}
}

func TestDiffRejectsPathsThatLeaveTheWorkspace(t *testing.T) {
	t.Parallel()
	_, id, svc := repoFixture(t)

	for _, path := range []string{"../secret.txt", "/etc/passwd", "~/.ssh/id_rsa", "--output=/tmp/x"} {
		if _, err := svc.Diff(context.Background(), id, path, ModeUnstaged); err == nil || !contentservice.IsRejected(err) {
			t.Fatalf("path %q = %v, want a rejection", path, err)
		}
	}
}

func TestDiffTruncatesAPatchLargerThanTheCapAndSaysSo(t *testing.T) {
	t.Parallel()
	root, id, svc := repoFixture(t)
	// One added line per row, comfortably past MaxPatchBytes once each carries
	// its leading "+".
	var big strings.Builder
	for i := 0; i < 12000; i++ {
		big.WriteString(strings.Repeat("x", 60))
		big.WriteString("\n")
	}
	writeFile(t, filepath.Join(root, "big.txt"), big.String())
	git := gitRunner(t, root)
	git("add", "big.txt")

	result, err := svc.Diff(context.Background(), id, "big.txt", ModeStaged)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Truncated {
		t.Fatalf("a %d byte patch was not marked truncated", len(result.Patch))
	}
	if len(result.Patch) > MaxPatchBytes {
		t.Fatalf("patch = %d bytes, cap is %d", len(result.Patch), MaxPatchBytes)
	}
}

func TestCommitDiffReadsOnePathInOneCommit(t *testing.T) {
	t.Parallel()
	root, id, svc := repoFixture(t)
	git := gitRunner(t, root)
	writeFile(t, filepath.Join(root, "second.txt"), "second\n")
	git("add", "second.txt")
	// A pathspec commit, so the fixture's already-staged tracked.txt stays out
	// of this commit and "a commit that did not touch the path" is true of it.
	git("commit", "-qm", "add second", "--", "second.txt")
	hash := strings.TrimSpace(mustGit(t, root, "rev-parse", "HEAD"))

	result, err := svc.CommitDiff(context.Background(), id, hash, "second.txt")
	if err != nil {
		t.Fatal(err)
	}
	if result.Mode != ModeCommit || result.Commit != hash || result.Path != "second.txt" {
		t.Fatalf("commit diff = %+v", result)
	}
	if !strings.Contains(result.Patch, "+second") {
		t.Fatalf("patch = %q", result.Patch)
	}

	// A commit that did not touch the path is empty, not a header-only blob a
	// viewer would render as a change.
	untouched, err := svc.CommitDiff(context.Background(), id, hash, "tracked.txt")
	if err != nil {
		t.Fatal(err)
	}
	if untouched.Patch != "" {
		t.Fatalf("a commit that did not touch the path returned %q", untouched.Patch)
	}
}

func TestCommitDiffRejectsSomethingThatIsNotAnObjectName(t *testing.T) {
	t.Parallel()
	_, id, svc := repoFixture(t)

	for _, hash := range []string{"", "HEAD", "--upload-pack=touch", "zzzz", "main"} {
		if _, err := svc.CommitDiff(context.Background(), id, hash, "tracked.txt"); err == nil || !contentservice.IsRejected(err) {
			t.Fatalf("commit %q = %v, want a rejection", hash, err)
		}
	}
}

func TestEncodeDiffResultShrinksRatherThanFailing(t *testing.T) {
	t.Parallel()
	result := DiffResult{
		Kind:      KindDiff,
		Workspace: "p:worktree:p",
		Mode:      ModeUnstaged,
		Path:      "big.txt",
		Patch:     strings.Repeat("+line\n", MaxEncodedBytes/3),
	}
	raw, err := EncodeDiffResult(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > MaxEncodedBytes {
		t.Fatalf("encoded %d bytes, cap is %d", len(raw), MaxEncodedBytes)
	}
	var decoded DiffResult
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("shrunk result is not valid JSON: %v", err)
	}
	if !decoded.ValidRemoteResult() || !decoded.Truncated {
		t.Fatalf("shrunk result does not say it was truncated: %+v", decoded)
	}
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := runGit(t, dir, args...)
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
	return out
}

func asError(err error, target **contentservice.Error) bool {
	coded, ok := err.(*contentservice.Error)
	if ok {
		*target = coded
	}
	return ok
}
