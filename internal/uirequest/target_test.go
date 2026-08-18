package uirequest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/workspacediff"
)

func TestResolveTargetIssue(t *testing.T) {
	target, err := ResolveTarget("/some/work/dir", "td-1234abcd", 0, ResolveOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target.Kind != TargetKindIssue || target.Value != "td-1234abcd" || target.Line != 0 {
		t.Fatalf("unexpected issue target: %+v", target)
	}
}

func TestResolveTargetIssueWinsOverDiff(t *testing.T) {
	target, err := ResolveTarget("/some/work/dir", "td-1234abcd", 0, ResolveOptions{Diff: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target.Kind != TargetKindIssue || target.Value != "td-1234abcd" {
		t.Fatalf("--diff td-… must stay an issue: %+v", target)
	}
}

// A bare "~" is a target an agent can plausibly type; it must be refused, not
// panic on the way to being refused.
func TestResolveTargetBareTilde(t *testing.T) {
	if _, err := ResolveTarget(t.TempDir(), "~", 0, ResolveOptions{}); err == nil {
		t.Errorf("expected an error resolving bare \"~\"")
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
	target, err := ResolveTarget(dir, "sub/file.go", 0, ResolveOptions{})
	if err != nil {
		t.Fatalf("relative resolve failed: %v", err)
	}
	if target.Kind != TargetKindFile || target.Value != "sub/file.go" || target.Line != 0 {
		t.Fatalf("unexpected target: %+v", target)
	}

	// 2. Path with line suffix
	target, err = ResolveTarget(dir, "sub/file.go:88", 0, ResolveOptions{})
	if err != nil {
		t.Fatalf("path:line resolve failed: %v", err)
	}
	if target.Kind != TargetKindFile || target.Value != "sub/file.go" || target.Line != 88 {
		t.Fatalf("unexpected target: %+v", target)
	}

	// 3. Explicit line override
	target, err = ResolveTarget(dir, "sub/file.go:88", 42, ResolveOptions{})
	if err != nil {
		t.Fatalf("explicit line override failed: %v", err)
	}
	if target.Line != 42 {
		t.Fatalf("expected explicit line 42, got %d", target.Line)
	}

	// 4. Absolute path inside root
	target, err = ResolveTarget(dir, filePath, 10, ResolveOptions{})
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

	_, err = ResolveTarget(dir, filepath.Join("..", filepath.Base(outsideDir), "secret.txt"), 0, ResolveOptions{})
	if err == nil {
		t.Fatal("expected traversal outside root to fail")
	}

	// 6. Absolute path outside root
	_, err = ResolveTarget(dir, outsideFile, 0, ResolveOptions{})
	if err == nil {
		t.Fatal("expected outside file to fail")
	}

	// 7. Non-existent file
	_, err = ResolveTarget(dir, "does-not-exist.txt", 0, ResolveOptions{})
	if err == nil {
		t.Fatal("expected non-existent file to fail")
	}

	// 8. Directory target
	_, err = ResolveTarget(dir, "sub", 0, ResolveOptions{})
	if err == nil {
		t.Fatal("expected directory target to fail")
	}
}

func TestResolveTargetDiffWorkingTree(t *testing.T) {
	target, err := ResolveTarget(t.TempDir(), "", 0, ResolveOptions{Diff: true})
	if err != nil {
		t.Fatalf("--diff with no spec: %v", err)
	}
	if target.Kind != TargetKindDiff || target.Value != workspacediff.IdentityWorkingTree {
		t.Fatalf("--diff = %+v, want kind=diff value=wt", target)
	}
}

func TestResolveTargetDiffHEADIsCommitNotWorkingTree(t *testing.T) {
	dir, oid := initGitRepo(t)
	target, err := ResolveTarget(dir, "HEAD", 0, ResolveOptions{Diff: true})
	if err != nil {
		t.Fatalf("--diff HEAD: %v", err)
	}
	if target.Kind != TargetKindDiff {
		t.Fatalf("kind = %q, want diff", target.Kind)
	}
	if target.Value != "c:"+oid {
		t.Fatalf("--diff HEAD = %q, want c:%s", target.Value, oid)
	}
	if target.Value == workspacediff.IdentityWorkingTree {
		t.Fatal("--diff HEAD must not be wt")
	}
}

func TestResolveTargetFileNamedHashWins(t *testing.T) {
	dir, oid := initGitRepo(t)
	name := oid[:7]
	if err := os.WriteFile(filepath.Join(dir, name), []byte("not a hash\n"), 0644); err != nil {
		t.Fatal(err)
	}
	target, err := ResolveTarget(dir, name, 0, ResolveOptions{})
	if err != nil {
		t.Fatalf("file %s: %v", name, err)
	}
	if target.Kind != TargetKindFile || target.Value != name {
		t.Fatalf("file named %s = %+v, want a file", name, target)
	}
}

func TestResolveTargetMissingFileThenHash(t *testing.T) {
	dir, oid := initGitRepo(t)
	short := oid[:7]
	target, err := ResolveTarget(dir, short, 0, ResolveOptions{})
	if err != nil {
		t.Fatalf("missing file that is a commit: %v", err)
	}
	if target.Kind != TargetKindDiff || target.Value != "c:"+oid {
		t.Fatalf("got %+v, want diff c:%s", target, oid)
	}
}

func TestResolveTargetMissingFileAndUnknownHash(t *testing.T) {
	dir, _ := initGitRepo(t)
	_, err := ResolveTarget(dir, "deadbee", 0, ResolveOptions{})
	if err == nil {
		t.Fatal("expected missing file + unknown hash to fail")
	}
}

func TestResolveTargetDiffUnknownRev(t *testing.T) {
	dir, _ := initGitRepo(t)
	_, err := ResolveTarget(dir, "not-a-rev-zzzz", 0, ResolveOptions{Diff: true})
	if err == nil {
		t.Fatal("expected unknown rev with --diff to fail")
	}
}

func TestResolveTargetEmptyWithoutDiff(t *testing.T) {
	if _, err := ResolveTarget(t.TempDir(), "", 0, ResolveOptions{}); err == nil {
		t.Fatal("empty target without --diff must fail")
	}
}

func TestDiffTargetParsesIdentity(t *testing.T) {
	if got := DiffTarget("", "wt"); got.Identity() != workspacediff.IdentityWorkingTree {
		t.Fatalf("wt: %+v", got)
	}
	if got := DiffTarget("", "c:abc1234"); got.Identity() != "c:abc1234" {
		t.Fatalf("commit: %+v", got)
	}
}

func initGitRepo(t *testing.T) (dir, oid string) {
	t.Helper()
	dir = t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t.example",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t.example",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "t@t.example")
	run("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "f")
	run("commit", "-m", "init")
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return dir, strings.TrimSpace(string(out))
}

func TestResolveResourceTargetRequiresBothParts(t *testing.T) {
	if _, err := ResolveResourceTarget("", "CASH-1"); err == nil {
		t.Error("a locator with no provider instance must be refused, not guessed at")
	}
	if _, err := ResolveResourceTarget("jira-work", ""); err == nil {
		t.Error("a provider with no locator must be refused")
	}
}

func TestResolveResourceTargetCarriesTheProviderAndLocator(t *testing.T) {
	got, err := ResolveResourceTarget("jira-work", "  CASH-1245  ")
	if err != nil {
		t.Fatalf("ResolveResourceTarget: %v", err)
	}
	if got.Kind != TargetKindResource {
		t.Errorf("kind = %q, want %q", got.Kind, TargetKindResource)
	}
	if got.Provider != "jira-work" || got.Value != "CASH-1245" {
		t.Errorf("target = %+v, want provider jira-work and locator CASH-1245", got)
	}
	// The matcher is deliberately absent: only the running app has a live
	// snapshot to choose one, and the CLI must start no provider.
}

func TestResolveResourceTargetBoundsAndRejectsControls(t *testing.T) {
	long := strings.Repeat("x", 400)
	if _, err := ResolveResourceTarget("jira-work", long); err == nil {
		t.Error("an oversize locator must be refused")
	}
	if _, err := ResolveResourceTarget(long, "CASH-1"); err == nil {
		t.Error("an oversize provider instance must be refused")
	}
	if _, err := ResolveResourceTarget("jira-work", "CASH\x07-1"); err == nil {
		t.Error("a locator with a control byte must be refused")
	}
}

func TestProviderOptionWinsBeforeTheFilesystemIsConsulted(t *testing.T) {
	// A locator that also names a real file must still be a resource: the
	// explicit flag is the user's statement of intent.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "CASH-1"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveTarget(dir, "CASH-1", 0, ResolveOptions{Provider: "jira-work"})
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if got.Kind != TargetKindResource {
		t.Fatalf("kind = %q, want a resource even though a file of that name exists", got.Kind)
	}
}

func TestWithoutProviderALocatorIsNotAResource(t *testing.T) {
	// v1 is deliberately explicit: a bare key must not start provider
	// discovery or guess among instances in a short-lived CLI process.
	_, err := ResolveTarget(t.TempDir(), "CASH-1245", 0, ResolveOptions{})
	if err == nil {
		t.Error("a bare locator should not resolve to anything without --provider")
	}
}
