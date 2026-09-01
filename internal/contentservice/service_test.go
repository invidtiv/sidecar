package contentservice

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/shellstate"
)

type gitRecorder struct {
	calls [][]string
}

func (g *gitRecorder) git(_ context.Context, dir string, args ...string) ([]byte, error) {
	g.calls = append(g.calls, append([]string{dir}, args...))
	cmd := exec.Command("git", append([]string{"--no-optional-locks", "-C", dir}, args...)...)
	return cmd.Output()
}

func testService(t *testing.T, root string, shells []shellstate.Definition, git *gitRecorder) *Service {
	t.Helper()
	cfg := config.Default()
	cfg.Projects.List = []config.ProjectConfig{{Name: "demo", Path: root}}
	svc := &Service{
		LoadConfig: func() (*config.Config, error) { return cfg, nil },
		ListShells: func(string) ([]shellstate.Definition, error) { return shells, nil },
	}
	if git != nil {
		svc.Git = git.git
	}
	return svc
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}
	run("init", "-q")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
}

func TestServiceResolveReadRoundTrip(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	if err := os.WriteFile(filepath.Join(root, "note.md"), []byte("body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, root)
	id := canonical(root) + ":worktree:" + canonical(root)
	git := &gitRecorder{}
	svc := testService(t, root, nil, git)

	resolved, err := svc.Resolve(context.Background(), id, KindFile, "note.md")
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.ValidRemoteResult() || resolved.Display != "note.md" {
		t.Fatalf("resolve = %+v", resolved)
	}
	read, err := svc.Read(context.Background(), id, KindFile, OpDocument, "note.md", "")
	if err != nil {
		t.Fatal(err)
	}
	if !read.ValidRemoteResult() || read.Content != "body\n" || read.Revision != resolved.Revision {
		t.Fatalf("read = %+v", read)
	}
	cached, err := svc.Read(context.Background(), id, KindFile, OpDocument, "note.md", read.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if !cached.NotModified || cached.Content != "" || cached.Revision != read.Revision {
		t.Fatalf("notModified = %+v", cached)
	}
	if !cached.ValidRemoteResult() {
		t.Fatal("notModified failed ValidRemoteResult")
	}
}

func TestServiceRefusesUnconfiguredAndReplacedIdentity(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	initGitRepo(t, root)
	git := &gitRecorder{}
	svc := testService(t, root, []shellstate.Definition{{TmuxName: "sidecar-sh-demo-1"}}, git)

	if _, err := svc.Resolve(context.Background(), "/nope:shell:sidecar-sh-1", KindFile, "a.md"); !IsRejected(err) {
		t.Fatalf("unconfigured: %v", err)
	}
	if _, err := svc.Resolve(context.Background(), canonical(root)+":shell:sidecar-sh-missing", KindFile, "a.md"); !IsRejected(err) {
		t.Fatalf("replaced shell: %v", err)
	}
	if _, err := svc.Resolve(context.Background(), canonical(root)+":worktree:/tmp/gone", KindFile, "a.md"); !IsRejected(err) {
		t.Fatalf("replaced worktree: %v", err)
	}
}

func TestServiceRefusesUnknownKind(t *testing.T) {
	t.Parallel()
	svc := testService(t, t.TempDir(), nil, nil)
	_, err := svc.Resolve(context.Background(), "x:shell:y", "resource", "a.md")
	var coded *Error
	if !errors.As(err, &coded) || coded.Code != CodeUnknownKind {
		t.Fatalf("kind = %v", err)
	}
	_, err = svc.Read(context.Background(), "x:shell:y", KindFile, "exec", "a.md", "")
	if !errors.As(err, &coded) || coded.Code != CodeUsage {
		t.Fatalf("operation = %v", err)
	}
}

func TestServiceGitCallGraphIsReadOnly(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, root)
	git := &gitRecorder{}
	svc := testService(t, root, nil, git)
	id := canonical(root) + ":worktree:" + canonical(root)
	if _, err := svc.Resolve(context.Background(), id, KindFile, "a.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Read(context.Background(), id, KindFile, OpDocument, "a.md", ""); err != nil {
		t.Fatal(err)
	}
	if len(git.calls) == 0 {
		t.Fatal("git was never invoked; the assertion is vacuous")
	}
	for _, call := range git.calls {
		joined := strings.Join(call[1:], " ")
		if joined != "worktree list --porcelain" {
			t.Errorf("git invoked mutating or unknown command: %q", joined)
		}
	}
}

func TestEncodedCapSitsUnderTransportCap(t *testing.T) {
	if MaxEncodedBytes >= transportOutputCap {
		t.Fatalf("MaxEncodedBytes %d must be < transport cap %d", MaxEncodedBytes, transportOutputCap)
	}
	if transportOutputCap != 1<<20 {
		t.Fatalf("transport cap changed to %d; content must not enlarge hosts.MaxRunOutputBytes", transportOutputCap)
	}
	if !encodedFitsUnderCap() {
		t.Fatal("encodedFitsUnderCap is false")
	}
}

func TestServiceDirectMatchesJSON(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	body := "same body on both surfaces\n"
	if err := os.WriteFile(filepath.Join(root, "note.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, root)
	id := canonical(root) + ":worktree:" + canonical(root)
	svc := testService(t, root, nil, &gitRecorder{})

	directResolve, err := svc.Resolve(context.Background(), id, KindFile, "note.md")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeResolveResult(directResolve)
	if err != nil {
		t.Fatal(err)
	}
	var jsonResolve ResolveResult
	if err := json.Unmarshal(encoded, &jsonResolve); err != nil {
		t.Fatal(err)
	}
	if jsonResolve != directResolve {
		t.Fatalf("resolve JSON drifted:\n direct %+v\n json %+v", directResolve, jsonResolve)
	}

	directRead, err := svc.Read(context.Background(), id, KindFile, OpDocument, "note.md", "")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := EncodeReadResult(directRead)
	if err != nil {
		t.Fatal(err)
	}
	var jsonRead ReadResult
	if err := json.Unmarshal(raw, &jsonRead); err != nil {
		t.Fatal(err)
	}
	if jsonRead.Display != directRead.Display || jsonRead.Content != directRead.Content || jsonRead.Revision != directRead.Revision {
		t.Fatalf("read JSON drifted:\n direct %+v\n json %+v", directRead, jsonRead)
	}
}

func TestServiceCancelledLookup(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	svc := testService(t, t.TempDir(), nil, nil)
	if _, err := svc.Resolve(ctx, "x:shell:y", KindFile, "a.md"); err == nil {
		t.Fatal("cancelled resolve succeeded")
	}
}
