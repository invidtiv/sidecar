package terminallink

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestResolveCommitAndGitSpec(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	runGit(t, dir, "config", "user.email", "sidecar@example.test")
	runGit(t, dir, "config", "user.name", "Sidecar Test")
	runGit(t, dir, "commit", "--allow-empty", "-m", "one")
	runGit(t, dir, "commit", "--allow-empty", "-m", "two")
	first := shortRev(t, dir, "HEAD~1")
	second := shortRev(t, dir, "HEAD")

	oid, ok := ResolveCommit(dir, first)
	if !ok || oid == "" {
		t.Fatalf("ResolveCommit(%q) = %q ok=%v", first, oid, ok)
	}
	if _, ok := ResolveCommit(dir, "deadbee"); ok {
		t.Fatal("unknown rev was accepted")
	}
	if _, ok := ResolveCommit(dir, "HEAD"); !ok {
		t.Fatal("HEAD should resolve as a commit even though the scanner will not emit it")
	}

	if _, _, ok := ResolveGitSpec(dir, first); !ok {
		t.Fatalf("ResolveGitSpec(%q) refused a real rev", first)
	}
	if _, extra, ok := ResolveGitSpec(dir, first+".."+second); !ok || extra.Raw != first+".."+second {
		t.Fatalf("two-dot spec refused")
	}
	if _, extra, ok := ResolveGitSpec(dir, first+"..."+second); !ok || extra.Raw != first+"..."+second {
		t.Fatalf("three-dot spec refused")
	}
	if _, extra, ok := ResolveGitSpec(dir, "commit "+first); !ok || extra.Raw != "commit "+first {
		t.Fatalf("commit-word spec refused")
	}
	if _, _, ok := ResolveGitSpec(dir, "HEAD"); ok {
		t.Fatal("HEAD is CLI-only and must not pass ResolveGitSpec")
	}
	if _, _, ok := ResolveGitSpec(dir, "deadbee.."+second); ok {
		t.Fatal("range with unknown left accepted")
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v (%s)", strings.Join(args, " "), err, out)
	}
}

func shortRev(t *testing.T, dir, rev string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--short=7", rev)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(string(out))
	if len(got) < 7 {
		t.Fatalf("short rev %q", got)
	}
	return got
}
