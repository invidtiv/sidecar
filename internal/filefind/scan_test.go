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

	want := []string{"main.go", filepath.Join("src", "app.go")}
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
