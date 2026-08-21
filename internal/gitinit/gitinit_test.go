package gitinit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitCreatesMainBranchAndGitignore(t *testing.T) {
	dir := t.TempDir()
	root, err := Init(dir)
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if root == "" {
		t.Fatal("Init() returned empty root")
	}

	cmd := exec.Command("git", "symbolic-ref", "--short", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("symbolic-ref: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "main" {
		t.Fatalf("HEAD = %q, want main", got)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	content := string(data)
	for _, entry := range SidecarGitignoreEntries {
		if !strings.Contains(content, entry) {
			t.Errorf(".gitignore missing %q", entry)
		}
	}
}

func TestIsRepository(t *testing.T) {
	empty := t.TempDir()
	if IsRepository(empty) {
		t.Fatal("empty directory reported as a repository")
	}
	if IsRepository("") {
		t.Fatal("empty path reported as a repository")
	}

	repo := t.TempDir()
	if _, err := Init(repo); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if !IsRepository(repo) {
		t.Fatal("initialized directory not reported as a repository")
	}
}

func TestEnsureGitignoreAddAndIdempotent(t *testing.T) {
	tmp := t.TempDir()
	gitignore := filepath.Join(tmp, ".gitignore")
	if err := os.WriteFile(gitignore, []byte("node_modules/\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureGitignore(tmp, SidecarGitignoreEntries); err != nil {
		t.Fatal(err)
	}
	if err := EnsureGitignore(tmp, SidecarGitignoreEntries); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(gitignore)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, entry := range SidecarGitignoreEntries {
		if strings.Count(content, entry) != 1 {
			t.Fatalf("%q count = %d, want 1\n%s", entry, strings.Count(content, entry), content)
		}
	}
}
