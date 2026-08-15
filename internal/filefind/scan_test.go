package filefind

import (
	"os"
	"path/filepath"
	"testing"
)

func mkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestScanPaths_CollectsFilesRespectingIgnores(t *testing.T) {
	tmpDir := t.TempDir()
	mkdirAll(t, filepath.Join(tmpDir, "src"))
	mkdirAll(t, filepath.Join(tmpDir, ".git"))
	mkdirAll(t, filepath.Join(tmpDir, "node_modules"))
	mkdirAll(t, filepath.Join(tmpDir, "ignored"))
	writeFile(t, filepath.Join(tmpDir, ".gitignore"), "ignored/\n")
	writeFile(t, filepath.Join(tmpDir, "main.go"), "package main")
	writeFile(t, filepath.Join(tmpDir, ".hidden"), "x")
	writeFile(t, filepath.Join(tmpDir, "src", "app.go"), "package src")
	writeFile(t, filepath.Join(tmpDir, ".git", "config"), "x")
	writeFile(t, filepath.Join(tmpDir, "node_modules", "dep.js"), "x")
	writeFile(t, filepath.Join(tmpDir, "ignored", "skip.go"), "x")

	files, errText := ScanPaths(tmpDir, false)
	if errText != "" {
		t.Fatalf("unexpected scan error: %s", errText)
	}

	// Dotfiles are files: what a scan hides is what .gitignore hides, not what
	// the name starts with. The tree pane shows them, and a finder that did not
	// answered "No matches" about files it had just walked past.
	want := []string{".gitignore", ".hidden", "main.go", filepath.Join("src", "app.go")}
	if len(files) != len(want) {
		t.Fatalf("files = %v, want %v", files, want)
	}
	for i, path := range want {
		if files[i] != path {
			t.Errorf("files[%d] = %q, want %q (sorted)", i, files[i], path)
		}
	}
}

func TestScanPaths_CollectsDirs(t *testing.T) {
	tmpDir := t.TempDir()
	mkdirAll(t, filepath.Join(tmpDir, "src", "inner"))
	mkdirAll(t, filepath.Join(tmpDir, ".git"))
	writeFile(t, filepath.Join(tmpDir, "main.go"), "package main")

	dirs, errText := ScanPaths(tmpDir, true)
	if errText != "" {
		t.Fatalf("unexpected scan error: %s", errText)
	}

	want := []string{"src", filepath.Join("src", "inner")}
	if len(dirs) != len(want) {
		t.Fatalf("dirs = %v, want %v", dirs, want)
	}
	for i, path := range want {
		if dirs[i] != path {
			t.Errorf("dirs[%d] = %q, want %q", i, dirs[i], path)
		}
	}
}

// The reported case, named: a tracked dotfile at the top of the tree is
// findable. `ctrl+p` + "goreleaser" answered "No matches" while
// `.goreleaser.yml` sat in the repo root, and the Files tree showed it.
func TestScanPathsFindsTrackedDotfiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".gitignore"), ".env\n")
	writeFile(t, filepath.Join(root, ".goreleaser.yml"), "x")
	writeFile(t, filepath.Join(root, ".env"), "SECRET=1")
	mkdirAll(t, filepath.Join(root, ".github", "workflows"))
	writeFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"), "x")

	files, errText := ScanPaths(root, false)
	if errText != "" {
		t.Fatalf("scan error: %s", errText)
	}
	found := map[string]bool{}
	for _, f := range files {
		found[f] = true
	}
	for _, want := range []string{".goreleaser.yml", ".gitignore", filepath.Join(".github", "workflows", "ci.yml")} {
		if !found[want] {
			t.Errorf("%q is in the tree but not in the scan: %v", want, files)
		}
	}
	// Ignored is still ignored: the rule is .gitignore, not the leading dot.
	if found[".env"] {
		t.Errorf("a gitignored dotfile was scanned: %v", files)
	}
}
