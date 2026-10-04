package contentservice

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectDiffPathsRoundTripThroughGit(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	initGitRepo(t, root)
	projectGit(t, root, "config", "core.quotePath", "true")
	paths := []string{`docs/"><img src=x onerror=window.pwned=1>.md`, "tab\tname.md", "line\nname.md", "café.md", "space name.md", "a b/file.md"}
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, path), []byte("old\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	projectGit(t, root, "add", ".")
	projectGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "base")
	base := projectGit(t, root, "rev-parse", "HEAD")
	for _, path := range paths {
		if err := os.WriteFile(filepath.Join(root, path), []byte("new\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	svc := testService(t, root, nil, nil)
	ctx := context.Background()
	checkRows := func(t *testing.T, rows []DiffFileRowDTO) {
		t.Helper()
		if len(rows) != len(paths) {
			t.Fatalf("got %d rows, want %d: %+v", len(rows), len(paths), rows)
		}
		for _, path := range paths {
			var found bool
			for _, row := range rows {
				if row.Path != path {
					continue
				}
				found = true
				if row.Additions != 1 || row.Deletions != 1 {
					t.Errorf("counts = %+v", row)
				}
				file, err := svc.ReadProject(ctx, "demo", "", ReadParams{Kind: KindFile, Operation: OpDocument, Target: row.Path})
				if strings.ContainsAny(path, "\t\n") {
					if !IsRejected(err) {
						t.Errorf("control-character path no longer refused: %v", err)
					}
					continue
				}
				if err != nil || file.Content != "new\n" {
					t.Errorf("row path %q cannot open source: %+v %v", row.Path, file, err)
				}
			}
			if !found {
				t.Errorf("missing repository path %q: %+v", path, rows)
			}
		}
	}
	working, err := svc.ReadProject(ctx, "demo", "", ReadParams{Kind: KindDiff, Operation: OpWorkingTree, Target: "wt"})
	if err != nil {
		t.Fatal(err)
	}
	t.Run("working tree", func(t *testing.T) { checkRows(t, working.Diff.Snapshot.Files) })
	projectGit(t, root, "add", ".")
	projectGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "change")
	head := projectGit(t, root, "rev-parse", "HEAD")
	rangeDoc, err := svc.ReadProject(ctx, "demo", "", ReadParams{Kind: KindDiff, Operation: OpRange, Target: base + ".." + head})
	if err != nil {
		t.Fatal(err)
	}
	t.Run("range", func(t *testing.T) { checkRows(t, rangeDoc.Diff.Range.Files) })
	commit, err := svc.ReadProject(ctx, "demo", "", ReadParams{Kind: KindDiff, Operation: OpCommit, Target: head})
	if err != nil {
		t.Fatal(err)
	}
	t.Run("commit", func(t *testing.T) {
		rows := make([]DiffFileRowDTO, len(commit.Diff.Commit.Files))
		for i, row := range commit.Diff.Commit.Files {
			rows[i] = DiffFileRowDTO{Path: row.Path, Additions: row.Additions, Deletions: row.Deletions}
		}
		checkRows(t, rows)
	})
	// NUL-delimited numstat renames have separate old/new name records.
	renamed := `new " name.md`
	projectGit(t, root, "mv", "space name.md", renamed)
	projectGit(t, root, "commit", "-qm", "rename")
	head = projectGit(t, root, "rev-parse", "HEAD")
	commit, err = svc.ReadProject(ctx, "demo", "", ReadParams{Kind: KindDiff, Operation: OpCommit, Target: head})
	if err != nil {
		t.Fatal(err)
	}
	if len(commit.Diff.Commit.Files) != 1 || commit.Diff.Commit.Files[0].Path != renamed {
		t.Fatalf("rename destination = %+v", commit.Diff.Commit.Files)
	}
}
