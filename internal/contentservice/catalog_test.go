package contentservice

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/shellstate"
)

func TestCatalogListsHostFilesAndDiffs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	if err := os.WriteFile(filepath.Join(root, "HOST-ONLY.md"), []byte("host\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, root)
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
	run("add", "HOST-ONLY.md")
	run("commit", "-m", "host file")
	id := canonical(root) + ":worktree:" + canonical(root)
	svc := testService(t, root, nil, nil)

	all, err := svc.Catalog(context.Background(), id, "")
	if err != nil {
		t.Fatal(err)
	}
	if !all.ValidRemoteResult() || all.Kind != KindCatalog {
		t.Fatalf("catalog = %+v", all)
	}
	if !containsString(all.Files, "HOST-ONLY.md") {
		t.Fatalf("files = %v, want HOST-ONLY.md", all.Files)
	}
	if len(all.Diffs) == 0 {
		t.Fatal("expected at least one diff ref")
	}

	files, err := svc.Catalog(context.Background(), id, KindFile)
	if err != nil {
		t.Fatal(err)
	}
	if files.KindFilter != KindFile || len(files.Diffs) != 0 {
		t.Fatalf("kind file leaked other lists: %+v", files)
	}
}

func TestCatalogUnknownWorkspaceIsRejected(t *testing.T) {
	t.Parallel()
	svc := testService(t, t.TempDir(), []shellstate.Definition{}, nil)
	_, err := svc.Catalog(context.Background(), "/nope:shell:sidecar-sh-1", KindFile)
	if !IsRejected(err) {
		t.Fatalf("err = %v, want rejected", err)
	}
}

func TestCatalogUnknownKind(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	id := canonical(root) + ":shell:sidecar-sh-1"
	svc := testService(t, root, []shellstate.Definition{{TmuxName: "sidecar-sh-1"}}, nil)
	_, err := svc.Catalog(context.Background(), id, KindResource)
	if err == nil || !strings.Contains(err.Error(), "unknown content kind") {
		t.Fatalf("err = %v", err)
	}
}

func TestCatalogUsesInjectedIssueAndNoteListers(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	id := canonical(root) + ":shell:sidecar-sh-1"
	svc := testService(t, root, []shellstate.Definition{{TmuxName: "sidecar-sh-1"}}, nil)
	svc.ListIssues = func(context.Context, string, int) ([]CatalogIssue, error) {
		return []CatalogIssue{{ID: "td-host01", Title: "host issue", Status: "open"}}, nil
	}
	svc.ListNotes = func(context.Context, string, int) ([]CatalogNote, error) {
		return []CatalogNote{{ID: "nt-host01", Title: "host note"}}, nil
	}
	got, err := svc.Catalog(context.Background(), id, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Issues) != 1 || got.Issues[0].ID != "td-host01" {
		t.Fatalf("issues = %+v", got.Issues)
	}
	if len(got.Notes) != 1 || got.Notes[0].ID != "nt-host01" {
		t.Fatalf("notes = %+v", got.Notes)
	}
}

func TestEncodeCatalogResultTruncatesFiles(t *testing.T) {
	result := CatalogResult{Kind: KindCatalog, Workspace: "p:shell:s1"}
	for i := 0; i < 20000; i++ {
		result.Files = append(result.Files, strings.Repeat("a", 80)+".md")
	}
	raw, err := EncodeCatalogResult(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > MaxEncodedBytes {
		t.Fatalf("encoded %d exceeds cap", len(raw))
	}
	var decoded CatalogResult
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.ValidRemoteResult() || !decoded.Truncated {
		t.Fatalf("decoded = %+v", decoded)
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
