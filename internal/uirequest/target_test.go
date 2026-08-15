package uirequest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveTargetIssue(t *testing.T) {
	target, err := ResolveTarget("/some/work/dir", "td-1234abcd", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target.Kind != TargetKindIssue || target.Value != "td-1234abcd" || target.Line != 0 {
		t.Fatalf("unexpected issue target: %+v", target)
	}
}

func TestResolveTargetFile(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "sub")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(subdir, "file.go")
	if err := os.WriteFile(filePath, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// 1. Relative path
	target, err := ResolveTarget(dir, "sub/file.go", 0)
	if err != nil {
		t.Fatalf("relative resolve failed: %v", err)
	}
	if target.Kind != TargetKindFile || target.Value != "sub/file.go" || target.Line != 0 {
		t.Fatalf("unexpected target: %+v", target)
	}

	// 2. Path with line suffix
	target, err = ResolveTarget(dir, "sub/file.go:88", 0)
	if err != nil {
		t.Fatalf("path:line resolve failed: %v", err)
	}
	if target.Kind != TargetKindFile || target.Value != "sub/file.go" || target.Line != 88 {
		t.Fatalf("unexpected target: %+v", target)
	}

	// 3. Explicit line override
	target, err = ResolveTarget(dir, "sub/file.go:88", 42)
	if err != nil {
		t.Fatalf("explicit line override failed: %v", err)
	}
	if target.Line != 42 {
		t.Fatalf("expected explicit line 42, got %d", target.Line)
	}

	// 4. Absolute path inside root
	target, err = ResolveTarget(dir, filePath, 10)
	if err != nil {
		t.Fatalf("absolute path inside root resolve failed: %v", err)
	}
	if target.Value != "sub/file.go" || target.Line != 10 {
		t.Fatalf("unexpected target: %+v", target)
	}

	// 5. Escape attempt (..)
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "secret.txt")
	_ = os.WriteFile(outsideFile, []byte("secret"), 0644)

	_, err = ResolveTarget(dir, filepath.Join("..", filepath.Base(outsideDir), "secret.txt"), 0)
	if err == nil {
		t.Fatal("expected traversal outside root to fail")
	}

	// 6. Absolute path outside root
	_, err = ResolveTarget(dir, outsideFile, 0)
	if err == nil {
		t.Fatal("expected outside file to fail")
	}

	// 7. Non-existent file
	_, err = ResolveTarget(dir, "does-not-exist.txt", 0)
	if err == nil {
		t.Fatal("expected non-existent file to fail")
	}

	// 8. Directory target
	_, err = ResolveTarget(dir, "sub", 0)
	if err == nil {
		t.Fatal("expected directory target to fail")
	}
}
