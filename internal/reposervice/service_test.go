package reposervice

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/contentservice"
	"github.com/marcus/sidecar/internal/shellstate"
)

// gitRunner runs git against a fixture. Every fixture repository lives under
// t.TempDir(): this package must never run git against the checkout it is being
// developed in.
func gitRunner(t *testing.T, dir string) func(args ...string) {
	t.Helper()
	return func(args ...string) {
		t.Helper()
		if _, err := runGit(t, dir, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
}

func runGit(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func tempRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	return root
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// serviceFor builds a Service scoped to root as its only configured project.
func serviceFor(t *testing.T, root string) (*Service, string) {
	t.Helper()
	cfg := config.Default()
	cfg.Projects.List = []config.ProjectConfig{{Name: "demo", Path: root}}
	svc := &Service{Workspaces: &contentservice.Service{
		LoadConfig: func() (*config.Config, error) { return cfg, nil },
	}}
	return svc, root + ":worktree:" + root
}

// serviceForShell scopes a Service to root addressed by a shell workspace id.
//
// A directory that is not a repository can only be reached this way: the
// worktree identity is re-resolved with `git worktree list`, which a non-repo
// has no answer to, so contentservice refuses the id before this package sees
// it. A bound project with no repository has no worktree key either, so a shell
// workspace is the identity a viewer would actually hold for one.
func serviceForShell(t *testing.T, root string) (*Service, string) {
	t.Helper()
	cfg := config.Default()
	cfg.Projects.List = []config.ProjectConfig{{Name: "demo", Path: root}}
	svc := &Service{Workspaces: &contentservice.Service{
		LoadConfig: func() (*config.Config, error) { return cfg, nil },
		ListShells: func(string) ([]shellstate.Definition, error) {
			return []shellstate.Definition{{TmuxName: "sidecar-sh-1", WorkDir: root}}, nil
		},
	}}
	return svc, root + ":shell:sidecar-sh-1"
}

// repoFixture is a repository with one commit on main, one staged change and
// one unstaged change to the SAME path, and one untracked file.
func repoFixture(t *testing.T) (root, id string, svc *Service) {
	t.Helper()
	root = tempRoot(t)
	git := gitRunner(t, root)
	git("init", "-q")
	git("symbolic-ref", "HEAD", "refs/heads/main")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "Test")
	writeFile(t, filepath.Join(root, "tracked.txt"), "base\n")
	git("add", "tracked.txt")
	git("commit", "-qm", "base")

	writeFile(t, filepath.Join(root, "tracked.txt"), "base\nSTAGED\n")
	git("add", "tracked.txt")
	writeFile(t, filepath.Join(root, "tracked.txt"), "base\nSTAGED\nUNSTAGED\n")
	writeFile(t, filepath.Join(root, "untracked.txt"), "UNTRACKED\n")

	svc, id = serviceFor(t, root)
	return root, id, svc
}

// upstreamFixture is a repository whose main branch tracks a bare origin and is
// two commits ahead of it.
func upstreamFixture(t *testing.T) (root, id string, svc *Service) {
	t.Helper()
	parent := tempRoot(t)
	remote := filepath.Join(parent, "origin.git")
	root = filepath.Join(parent, "work")
	if _, err := runGit(t, parent, "init", "-q", "--bare", remote); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	git := gitRunner(t, root)
	git("init", "-q")
	git("symbolic-ref", "HEAD", "refs/heads/main")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "Test")
	writeFile(t, filepath.Join(root, "a.txt"), "1\n")
	git("add", "a.txt")
	git("commit", "-qm", "pushed")
	git("remote", "add", "origin", remote)
	git("push", "-q", "-u", "origin", "main")
	for _, n := range []string{"2", "3"} {
		writeFile(t, filepath.Join(root, "a.txt"), n+"\n")
		git("commit", "-qam", "local "+n)
	}
	svc, id = serviceFor(t, root)
	return root, id, svc
}

func fileByPath(t *testing.T, result StatusResult, path string) StatusFile {
	t.Helper()
	for _, file := range result.Files {
		if file.Path == path {
			return file
		}
	}
	t.Fatalf("no row for %q in %+v", path, result.Files)
	return StatusFile{}
}

func TestStatusReportsBranchStagingAndUntracked(t *testing.T) {
	t.Parallel()
	_, id, svc := repoFixture(t)

	result, err := svc.Status(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if !result.ValidRemoteResult() || result.NoRepository {
		t.Fatalf("status = %+v", result)
	}
	if result.Branch != "main" || result.Detached || result.Head == "" {
		t.Fatalf("branch = %+v", result)
	}
	if result.State != "" {
		t.Fatalf("state = %q, want an ordinary working tree", result.State)
	}

	tracked := fileByPath(t, result, "tracked.txt")
	if !tracked.Staged || !tracked.Unstaged || tracked.Untracked {
		t.Fatalf("tracked.txt = %+v, want both a staged and an unstaged change", tracked)
	}
	if tracked.StagedAdditions == 0 || tracked.UnstagedAdditions == 0 {
		t.Fatalf("tracked.txt counts = %+v, want a count for each sense", tracked)
	}
	untracked := fileByPath(t, result, "untracked.txt")
	if !untracked.Untracked || untracked.Staged || untracked.Status != "?" {
		t.Fatalf("untracked.txt = %+v", untracked)
	}
}

// A branch with no upstream is a normal state, not a failure: the host reports
// no upstream and zero counts rather than refusing to answer at all.
func TestStatusWithoutUpstream(t *testing.T) {
	t.Parallel()
	_, id, svc := repoFixture(t)

	result, err := svc.Status(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if result.HasUpstream || result.Upstream != "" || result.Ahead != 0 || result.Behind != 0 {
		t.Fatalf("status = %+v, want no upstream", result)
	}
	if result.RemoteURL != "" {
		t.Fatalf("remote url = %q, want none", result.RemoteURL)
	}
}

func TestStatusWithUpstreamReportsAheadAndRemoteURL(t *testing.T) {
	t.Parallel()
	_, id, svc := upstreamFixture(t)

	result, err := svc.Status(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if !result.HasUpstream || result.Upstream != "origin/main" {
		t.Fatalf("upstream = %+v", result)
	}
	if result.Ahead != 2 || result.Behind != 0 {
		t.Fatalf("ahead/behind = %d/%d, want 2/0", result.Ahead, result.Behind)
	}
	if !strings.HasSuffix(result.RemoteURL, "origin.git") {
		t.Fatalf("remote url = %q", result.RemoteURL)
	}
}

func TestStatusReportsDetachedHead(t *testing.T) {
	t.Parallel()
	root, id, svc := repoFixture(t)
	gitRunner(t, root)("checkout", "-q", "--detach", "HEAD")

	result, err := svc.Status(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Detached || result.Branch != "" {
		t.Fatalf("status = %+v, want a detached HEAD and no branch", result)
	}
	if result.Head == "" {
		t.Fatal("a detached HEAD still has a commit; head was empty")
	}
}

func TestStatusReportsAnInProgressRebase(t *testing.T) {
	t.Parallel()
	root := tempRoot(t)
	git := gitRunner(t, root)
	git("init", "-q")
	git("symbolic-ref", "HEAD", "refs/heads/main")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "Test")
	writeFile(t, filepath.Join(root, "a.txt"), "base\n")
	git("add", "a.txt")
	git("commit", "-qm", "base")
	git("checkout", "-q", "-b", "topic")
	writeFile(t, filepath.Join(root, "a.txt"), "topic\n")
	git("commit", "-qam", "topic")
	git("checkout", "-q", "main")
	writeFile(t, filepath.Join(root, "a.txt"), "main\n")
	git("commit", "-qam", "main")
	git("checkout", "-q", "topic")
	// The rebase is expected to stop on the conflict; that stop is the fixture.
	if out, err := runGit(t, root, "rebase", "main"); err == nil {
		t.Fatalf("rebase did not conflict: %s", out)
	}
	svc, id := serviceFor(t, root)

	result, err := svc.Status(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != StateRebase {
		t.Fatalf("state = %q, want %q", result.State, StateRebase)
	}
}

// A workspace that is not a repository is a named answer, not an error string a
// viewer has to pattern-match and not silence.
func TestVerbsNameAWorkspaceThatIsNotARepository(t *testing.T) {
	t.Parallel()
	root := tempRoot(t)
	writeFile(t, filepath.Join(root, "notes.txt"), "no repo here\n")
	svc, id := serviceForShell(t, root)
	ctx := context.Background()

	status, err := svc.Status(ctx, id)
	if err != nil || !status.NoRepository || !status.ValidRemoteResult() {
		t.Fatalf("status = %+v (%v)", status, err)
	}
	diff, err := svc.Diff(ctx, id, "notes.txt", ModeUnstaged)
	if err != nil || !diff.NoRepository {
		t.Fatalf("diff = %+v (%v)", diff, err)
	}
	history, err := svc.History(ctx, id, HistoryQuery{})
	if err != nil || !history.NoRepository {
		t.Fatalf("history = %+v (%v)", history, err)
	}
	commit, err := svc.Commit(ctx, id, "abcdef12")
	if err != nil || !commit.NoRepository {
		t.Fatalf("commit = %+v (%v)", commit, err)
	}
	refs, err := svc.Refs(ctx, id)
	if err != nil || !refs.NoRepository {
		t.Fatalf("refs = %+v (%v)", refs, err)
	}
}

func TestVerbsRejectAnUnknownWorkspace(t *testing.T) {
	t.Parallel()
	_, _, svc := repoFixture(t)
	ctx := context.Background()

	for name, err := range map[string]error{
		"status":  errOf(func() error { _, err := svc.Status(ctx, "nope:worktree:/nowhere"); return err }),
		"diff":    errOf(func() error { _, err := svc.Diff(ctx, "nope:worktree:/nowhere", "a.txt", ModeStaged); return err }),
		"history": errOf(func() error { _, err := svc.History(ctx, "nope:worktree:/nowhere", HistoryQuery{}); return err }),
		"commit":  errOf(func() error { _, err := svc.Commit(ctx, "nope:worktree:/nowhere", "abcdef12"); return err }),
		"refs":    errOf(func() error { _, err := svc.Refs(ctx, "nope:worktree:/nowhere"); return err }),
	} {
		if err == nil || !contentservice.IsRejected(err) {
			t.Fatalf("%s = %v, want a rejection", name, err)
		}
	}
}

func errOf(fn func() error) error { return fn() }

// recordingGit delegates to real git and remembers every argv, so "read-only"
// is proven by what the service ran rather than asserted in a comment.
type recordingGit struct {
	mu   sync.Mutex
	argv [][]string
}

func (g *recordingGit) run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	g.mu.Lock()
	g.argv = append(g.argv, append([]string(nil), args...))
	g.mu.Unlock()
	return defaultGit(ctx, dir, args...)
}

func TestEveryGitInvocationIsAReadWithoutOptionalLocks(t *testing.T) {
	t.Parallel()
	root, id, svc := repoFixture(t)
	recorder := &recordingGit{}
	svc.Git = recorder.run
	ctx := context.Background()

	if _, err := svc.Status(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Diff(ctx, id, "tracked.txt", ModeStaged); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Diff(ctx, id, "untracked.txt", ModeUntracked); err != nil {
		t.Fatal(err)
	}
	history, err := svc.History(ctx, id, HistoryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Commits) == 0 {
		t.Fatal("history was empty")
	}
	if _, err := svc.Commit(ctx, id, history.Commits[0].Hash); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Refs(ctx, id); err != nil {
		t.Fatal(err)
	}

	// Every subcommand this service is allowed to reach, and for the two that
	// have mutating forms, the only sub-subcommand it may pass.
	readOnly := map[string]string{
		"rev-parse": "", "status": "", "diff": "", "log": "", "show": "",
		"rev-list": "", "branch": "", "remote": "get-url", "stash": "list",
	}
	for _, argv := range recorder.argv {
		if len(argv) == 0 {
			t.Fatal("empty git invocation")
		}
		want, ok := readOnly[argv[0]]
		if !ok {
			t.Fatalf("git %v is not a read-only subcommand", argv)
		}
		if want != "" && (len(argv) < 2 || argv[1] != want) {
			t.Fatalf("git %v: %q may only be used as %q", argv, argv[0], argv[0]+" "+want)
		}
		if argv[0] == "branch" && len(argv) > 1 && !strings.HasPrefix(argv[1], "--") {
			t.Fatalf("git %v creates or moves a branch", argv)
		}
	}

	// The fixture must be untouched: a read that changed the index would have
	// staged or unstaged something here.
	out, err := runGit(t, root, "status", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "MM tracked.txt") || !strings.Contains(out, "?? untracked.txt") {
		t.Fatalf("the fixture changed under a read: %q", out)
	}
}

// git accepts --no-optional-locks only before the subcommand, so a build that
// dropped it would still run and still work — until it took the index lock from
// a human on the host. Nothing else fails if this regresses, so it is pinned.
func TestDefaultGitAlwaysPassesNoOptionalLocks(t *testing.T) {
	t.Parallel()
	argv := gitArgs("/tmp/example", []string{"status", "--porcelain"})
	want := []string{"--no-optional-locks", "-C", "/tmp/example", "status", "--porcelain"}
	if strings.Join(argv, " ") != strings.Join(want, " ") {
		t.Fatalf("argv = %v, want %v", argv, want)
	}
}
