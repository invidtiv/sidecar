package gitstatus

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func writeFixtureFile(t testing.TB, root, name string, data []byte) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func findEntry(entries []*FileEntry, path string) *FileEntry {
	for _, entry := range entries {
		if entry.Path == path {
			return entry
		}
	}
	return nil
}

func TestLoadFileTreeRealGitUnusualPathsAndExactStats(t *testing.T) {
	repo := t.TempDir()
	runGitTest(t, repo, "init", "-b", "main")
	runGitTest(t, repo, "config", "user.name", "Sidecar Test")
	runGitTest(t, repo, "config", "user.email", "sidecar@example.test")
	tracked := map[string][]byte{
		"left/same.txt":  []byte("base\n"),
		"right/same.txt": []byte("base\n"),
		"space name.txt": []byte("base\n"),
		"tab\tname.txt":  []byte("base\n"),
		"line\nname.txt": []byte("base\n"),
		"unicodé-雪.txt":  []byte("base\n"),
		"both.txt":       []byte("base\n"),
		"rename old.txt": []byte("rename\n"),
		"binary.dat":     {0, 1, 2, 0, 3},
	}
	for name, data := range tracked {
		writeFixtureFile(t, repo, name, data)
	}
	runGitTest(t, repo, "add", "-A")
	runGitTest(t, repo, "commit", "-m", "fixture")

	writeFixtureFile(t, repo, "left/same.txt", []byte("base\none\n"))
	writeFixtureFile(t, repo, "right/same.txt", []byte("base\none\ntwo\nthree\n"))
	writeFixtureFile(t, repo, "space name.txt", []byte("base\nspace\n"))
	writeFixtureFile(t, repo, "tab\tname.txt", []byte("base\ntab\n"))
	writeFixtureFile(t, repo, "line\nname.txt", []byte("base\nnewline\n"))
	writeFixtureFile(t, repo, "unicodé-雪.txt", []byte("base\nunicode\n"))
	writeFixtureFile(t, repo, "binary.dat", []byte{0, 9, 8, 0, 7})
	writeFixtureFile(t, repo, "both.txt", []byte("base\nstaged\n"))
	runGitTest(t, repo, "add", "both.txt")
	writeFixtureFile(t, repo, "both.txt", []byte("base\nstaged\nunstaged\n"))
	runGitTest(t, repo, "mv", "rename old.txt", "rename new.txt")
	writeFixtureFile(t, repo, "untracked space.txt", []byte("not eagerly counted\n"))

	tree, err := LoadFileTree(repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"left/same.txt", "right/same.txt", "space name.txt", "tab\tname.txt", "line\nname.txt", "unicodé-雪.txt", "binary.dat", "both.txt"} {
		if findEntry(tree.Modified, path) == nil {
			t.Errorf("missing modified entry %q", path)
		}
	}
	if got := findEntry(tree.Modified, "left/same.txt").DiffStats.Additions; got != 1 {
		t.Errorf("left duplicate-basename additions = %d, want 1", got)
	}
	if got := findEntry(tree.Modified, "right/same.txt").DiffStats.Additions; got != 3 {
		t.Errorf("right duplicate-basename additions = %d, want 3", got)
	}
	if got := findEntry(tree.Staged, "both.txt").DiffStats.Additions; got != 1 {
		t.Errorf("staged half additions = %d, want 1", got)
	}
	if got := findEntry(tree.Modified, "both.txt").DiffStats.Additions; got != 1 {
		t.Errorf("unstaged half additions = %d, want 1", got)
	}
	rename := findEntry(tree.Staged, "rename new.txt")
	if rename == nil || rename.OldPath != "rename old.txt" || rename.Status != StatusRenamed {
		t.Errorf("rename entry = %#v", rename)
	}
	untracked := findEntry(tree.Untracked, "untracked space.txt")
	if untracked == nil || untracked.DiffStats != (DiffStats{}) {
		t.Errorf("untracked entry should load without eager line count: %#v", untracked)
	}
}

func TestParseNumstatNULPathsRenameCopyAndBinary(t *testing.T) {
	input := []byte("1\t2\tspace name\x001\t0\ttab\tname\x003\t4\tline\nname\x00-\t-\tbinary\x005\t6\t\x00old name\x00new name\x00")
	got := parseNumstat(input)
	wantPaths := []string{"space name", "tab\tname", "line\nname", "binary", "new name"}
	if len(got) != len(wantPaths) {
		t.Fatalf("entries = %#v", got)
	}
	for i, path := range wantPaths {
		if got[i].Path != path {
			t.Errorf("entry %d path = %q, want %q", i, got[i].Path, path)
		}
	}
	if got[3].Stats != (DiffStats{}) {
		t.Errorf("binary stats = %#v, want zero display stats", got[3].Stats)
	}
	copyEntry := (&FileTree{}).parseRenamedEntry("2 C. N... 100644 100644 100644 abc def C100 copied.txt")
	if copyEntry == nil || copyEntry.Status != StatusCopied {
		t.Errorf("copy entry = %#v", copyEntry)
	}
}

func TestParseStatusUnmergedUnusualPath(t *testing.T) {
	tree := &FileTree{}
	path := "conflict\t雪\n.txt"
	record := "u UU N... 100644 100644 100644 100644 a b c " + path + "\x00"
	if err := tree.parseStatus([]byte(record)); err != nil {
		t.Fatal(err)
	}
	if len(tree.Modified) != 1 || tree.Modified[0].Path != path || tree.Modified[0].Status != StatusUnmerged {
		t.Fatalf("unmerged entries = %#v", tree.Modified)
	}
}

func TestLoadFileTreeRealGitConflict(t *testing.T) {
	repo := t.TempDir()
	runGitTest(t, repo, "init", "-b", "main")
	runGitTest(t, repo, "config", "user.name", "Sidecar Test")
	runGitTest(t, repo, "config", "user.email", "sidecar@example.test")
	path := "conflict space.txt"
	writeFixtureFile(t, repo, path, []byte("base\n"))
	runGitTest(t, repo, "add", path)
	runGitTest(t, repo, "commit", "-m", "base")
	runGitTest(t, repo, "checkout", "-b", "other")
	writeFixtureFile(t, repo, path, []byte("other\n"))
	runGitTest(t, repo, "commit", "-am", "other")
	runGitTest(t, repo, "checkout", "main")
	writeFixtureFile(t, repo, path, []byte("master\n"))
	runGitTest(t, repo, "commit", "-am", "master")
	cmd := exec.Command("git", "merge", "other")
	cmd.Dir = repo
	if err := cmd.Run(); err == nil {
		t.Fatal("merge unexpectedly succeeded; fixture did not create a conflict")
	}
	tree, err := LoadFileTree(repo)
	if err != nil {
		t.Fatal(err)
	}
	entry := findEntry(tree.Modified, path)
	if entry == nil || entry.Status != StatusUnmerged {
		t.Fatalf("conflict entry = %#v", entry)
	}
}

func TestAllEntriesReflectsDirectSliceReassignment(t *testing.T) {
	tree := &FileTree{Modified: []*FileEntry{{Path: "old"}}}
	if got := tree.AllEntries(); len(got) != 1 || got[0].Path != "old" {
		t.Fatalf("initial entries = %#v", got)
	}
	tree.Modified = []*FileEntry{{Path: "new"}}
	if got := tree.AllEntries(); len(got) != 1 || got[0].Path != "new" {
		t.Fatalf("entries after direct reassignment = %#v", got)
	}
}

func BenchmarkParseNumstat(b *testing.B) {
	var input bytes.Buffer
	for i := 0; i < 10_000; i++ {
		fmt.Fprintf(&input, "1\t2\tdir/%05d file.txt%c", i, byte(0))
	}
	data := input.Bytes()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = parseNumstat(data)
	}
}

var benchmarkEntries []*FileEntry

func BenchmarkAllEntriesRebuild(b *testing.B) {
	tree := &FileTree{}
	for i := 0; i < 10_000; i++ {
		tree.Modified = append(tree.Modified, &FileEntry{Path: fmt.Sprintf("file-%05d", i)})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkEntries = tree.AllEntries()
	}
}

func BenchmarkLoadFileTree(b *testing.B) {
	repo := b.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = repo
	if output, err := cmd.CombinedOutput(); err != nil {
		b.Fatalf("git init: %v: %s", err, output)
	}
	for i := 0; i < 1_000; i++ {
		writeFixtureFile(b, repo, fmt.Sprintf("untracked/%05d.txt", i), []byte("content\n"))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := LoadFileTree(repo); err != nil {
			b.Fatal(err)
		}
	}
}
