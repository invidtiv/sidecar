package contentservice

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func treeFixture(t *testing.T) (root, id string, svc *Service) {
	t.Helper()
	root = t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	mkdirAll(t, filepath.Join(root, "internal", "cli"))
	mkdirAll(t, filepath.Join(root, "empty"))
	mkdirAll(t, filepath.Join(root, "build"))
	writeFile(t, filepath.Join(root, "README.md"), "host\n")
	writeFile(t, filepath.Join(root, ".gitignore"), "build/\n*.log\n")
	writeFile(t, filepath.Join(root, "debug.log"), "noise\n")
	writeFile(t, filepath.Join(root, "internal", "cli", "content.go"), "package cli\n")
	initGitRepo(t, root)
	id = canonical(root) + ":worktree:" + canonical(root)
	return root, id, testService(t, root, nil, nil)
}

func mkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func dirByPath(t *testing.T, result TreeResult, path string) TreeDir {
	t.Helper()
	for _, dir := range result.Dirs {
		if dir.Path == path {
			return dir
		}
	}
	t.Fatalf("no listing for %q in %+v", path, result.Dirs)
	return TreeDir{}
}

func entryByName(dir TreeDir, name string) (TreeEntry, bool) {
	for _, entry := range dir.Entries {
		if entry.Name == name {
			return entry, true
		}
	}
	return TreeEntry{}, false
}

func TestTreeListsRootWithMetadata(t *testing.T) {
	t.Parallel()
	_, id, svc := treeFixture(t)

	result, err := svc.Tree(context.Background(), id, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.ValidRemoteResult() || result.Kind != KindTree {
		t.Fatalf("tree = %+v", result)
	}
	if len(result.Dirs) != 1 || result.Dirs[0].Path != "" {
		t.Fatalf("an empty path list listed %+v, want the root alone", result.Dirs)
	}
	root := result.Dirs[0]

	readme, ok := entryByName(root, "README.md")
	if !ok {
		t.Fatalf("root missing README.md: %+v", root.Entries)
	}
	if readme.Dir || readme.Ignored || readme.Size == 0 || readme.Modified.IsZero() {
		t.Fatalf("README.md = %+v, want a sized, dated, unignored file", readme)
	}
	// An empty directory has no file to imply it, which is why the tree cannot
	// be synthesized from the flat catalog list.
	empty, ok := entryByName(root, "empty")
	if !ok || !empty.Dir {
		t.Fatalf("root missing the empty directory: %+v", root.Entries)
	}
}

func TestTreeMarksIgnoredEntries(t *testing.T) {
	t.Parallel()
	_, id, svc := treeFixture(t)

	result, err := svc.Tree(context.Background(), id, []string{""})
	if err != nil {
		t.Fatal(err)
	}
	root := dirByPath(t, result, "")
	log, ok := entryByName(root, "debug.log")
	if !ok || !log.Ignored {
		t.Fatalf("debug.log = %+v, want ignored", log)
	}
	build, ok := entryByName(root, "build")
	if !ok || !build.Dir || !build.Ignored {
		t.Fatalf("build/ = %+v, want an ignored directory", build)
	}
	readme, _ := entryByName(root, "README.md")
	if readme.Ignored {
		t.Fatal("README.md was marked ignored")
	}
}

func TestTreeListsSeveralDirectoriesInRequestOrder(t *testing.T) {
	t.Parallel()
	_, id, svc := treeFixture(t)

	want := []string{"", "internal", "internal/cli"}
	result, err := svc.Tree(context.Background(), id, want)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Dirs) != len(want) {
		t.Fatalf("listed %d directories, want %d", len(result.Dirs), len(want))
	}
	for i, path := range want {
		if result.Dirs[i].Path != path {
			t.Fatalf("dir %d = %q, want %q — answers must pair with requests by position", i, result.Dirs[i].Path, path)
		}
	}
	if _, ok := entryByName(dirByPath(t, result, "internal/cli"), "content.go"); !ok {
		t.Fatalf("internal/cli = %+v", dirByPath(t, result, "internal/cli").Entries)
	}
}

// One remembered directory that has gone away must not blank the whole tree.
func TestTreeReportsAMissingDirectoryWithoutFailingTheCall(t *testing.T) {
	t.Parallel()
	_, id, svc := treeFixture(t)

	result, err := svc.Tree(context.Background(), id, []string{"", "gone"})
	if err != nil {
		t.Fatalf("a missing directory failed the whole call: %v", err)
	}
	gone := dirByPath(t, result, "gone")
	if gone.Err == "" {
		t.Fatalf("missing directory reported no error: %+v", gone)
	}
	if len(dirByPath(t, result, "").Entries) == 0 {
		t.Fatal("the root listing was lost with the missing directory")
	}
}

func TestTreeRejectsEscapingAndAbsolutePaths(t *testing.T) {
	t.Parallel()
	_, id, svc := treeFixture(t)

	for _, path := range []string{"../..", "internal/../../..", "/etc", "~/"} {
		if _, err := svc.Tree(context.Background(), id, []string{path}); err == nil {
			t.Fatalf("path %q was accepted", path)
		} else if !IsRejected(err) {
			t.Fatalf("path %q = %v, want a rejection", path, err)
		}
	}
}

func TestTreeRejectsTooManyPaths(t *testing.T) {
	t.Parallel()
	_, id, svc := treeFixture(t)

	paths := make([]string, MaxTreePaths+1)
	for i := range paths {
		paths[i] = "internal"
	}
	if _, err := svc.Tree(context.Background(), id, paths); err == nil || !IsRejected(err) {
		t.Fatalf("err = %v, want a rejection", err)
	}
}

func TestTreeRejectsUnknownWorkspace(t *testing.T) {
	t.Parallel()
	_, _, svc := treeFixture(t)

	if _, err := svc.Tree(context.Background(), "nope:worktree:/nowhere", nil); err == nil || !IsRejected(err) {
		t.Fatalf("err = %v, want a rejection", err)
	}
}

func TestTreeTruncatesAnEnormousDirectory(t *testing.T) {
	t.Parallel()
	root, id, svc := treeFixture(t)
	big := filepath.Join(root, "big")
	mkdirAll(t, big)
	for i := 0; i < MaxTreeEntries+10; i++ {
		writeFile(t, filepath.Join(big, "f"+strings.Repeat("0", 4)+itoa(i)), "x")
	}

	result, err := svc.Tree(context.Background(), id, []string{"big"})
	if err != nil {
		t.Fatal(err)
	}
	dir := dirByPath(t, result, "big")
	if !dir.Truncated || len(dir.Entries) != MaxTreeEntries {
		t.Fatalf("big = %d entries truncated=%t, want %d and truncated", len(dir.Entries), dir.Truncated, MaxTreeEntries)
	}
}

func TestEncodeTreeResultShrinksRatherThanFailing(t *testing.T) {
	t.Parallel()
	// Two directories, one far larger, both over the cap together.
	small := TreeDir{Path: "small"}
	big := TreeDir{Path: "big"}
	for i := 0; i < 200; i++ {
		small.Entries = append(small.Entries, TreeEntry{Name: strings.Repeat("s", 200) + itoa(i)})
	}
	for i := 0; i < 20000; i++ {
		big.Entries = append(big.Entries, TreeEntry{Name: strings.Repeat("b", 200) + itoa(i)})
	}
	raw, err := EncodeTreeResult(TreeResult{Workspace: "p:worktree:p", Dirs: []TreeDir{small, big}})
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > MaxEncodedBytes {
		t.Fatalf("encoded %d bytes, cap is %d", len(raw), MaxEncodedBytes)
	}
	var decoded TreeResult
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("shrunk result is not valid JSON: %v", err)
	}
	if decoded.Kind != KindTree || len(decoded.Dirs) != 2 {
		t.Fatalf("shrinking dropped a directory: %+v", decoded.Dirs)
	}
	shrunk := dirByPath(t, decoded, "big")
	if !shrunk.Truncated {
		t.Fatal("the shrunk directory does not say it was truncated")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
