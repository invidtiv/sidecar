package terminallink

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveFileAcceptsInsideOutsideAndHome(t *testing.T) {
	base := t.TempDir()
	inside := filepath.Join(base, "main.go")
	if err := os.WriteFile(inside, []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	display, abs, ok := ResolveFile(base, "main.go")
	if !ok || display != "main.go" {
		t.Fatalf("inside = %q %q ok=%v", display, abs, ok)
	}

	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "notes.json")
	if err := os.WriteFile(outside, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	display, abs, ok = ResolveFile(base, outside)
	outsideResolved, err := filepath.EvalSymlinks(outside)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || display != outsideResolved || abs != outsideResolved {
		t.Fatalf("outside = %q %q ok=%v", display, abs, ok)
	}

	home := t.TempDir()
	homeFile := filepath.Join(home, "dot.go")
	if err := os.WriteFile(homeFile, []byte("package dot"), 0o644); err != nil {
		t.Fatal(err)
	}
	orig := userHomeDir
	userHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { userHomeDir = orig })
	display, abs, ok = ResolveFile(base, "~/dot.go")
	homeResolved, err := filepath.EvalSymlinks(homeFile)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || abs != homeResolved || display != homeResolved {
		t.Fatalf("home = %q %q ok=%v", display, abs, ok)
	}
}

func TestResolveFileRejectsMissingDirectoryAndControls(t *testing.T) {
	base := t.TempDir()
	if _, _, ok := ResolveFile(base, "missing.go"); ok {
		t.Fatal("missing accepted")
	}
	if _, _, ok := ResolveFile(base, base); ok {
		t.Fatal("directory accepted")
	}
	if _, _, ok := ResolveFile(base, "x.go\x1b"); ok {
		t.Fatal("control accepted")
	}
}

func TestMarkdownExt(t *testing.T) {
	if !Markdown("README.MD") || Markdown("main.go") {
		t.Fatal("markdown extension classification")
	}
}
