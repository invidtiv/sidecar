package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspaceops"
)

// recordingRunner stands in for tmux so a test can assert both what was sent
// and, more importantly, that nothing was sent at all.
type recordingRunner struct {
	calls [][]string
	err   error
}

func (r *recordingRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	r.calls = append(r.calls, args)
	return nil, r.err
}

func useRecordingRunner(t *testing.T) *recordingRunner {
	t.Helper()
	runner := &recordingRunner{}
	previous := shellSendRunner
	shellSendRunner = runner
	t.Cleanup(func() { shellSendRunner = previous })
	return runner
}

// targetProject registers one project with two shell records and leaves the
// process in its working directory, so resolution finds it without --project.
func targetProject(t *testing.T) (stateDir, workDir string) {
	t.Helper()
	_, stateDir = setupIsolatedCLI(t)
	workDir = t.TempDir()
	if resolved, err := filepath.EvalSymlinks(workDir); err == nil {
		workDir = resolved
	}
	t.Chdir(workDir)
	writeProjectMeta(t, stateDir, "demo", workDir)
	writeProjectShells(t, stateDir, "demo",
		shellstate.Definition{TmuxName: "sidecar-sh-demo-1", DisplayName: "one", Namespace: "/tmp/socket", WorkDir: workDir},
		shellstate.Definition{TmuxName: "sidecar-sh-demo-2", DisplayName: "two", Namespace: "/tmp/socket", WorkDir: workDir},
	)
	return stateDir, workDir
}

func manifestNames(t *testing.T, stateDir string) map[string]string {
	t.Helper()
	defs, err := shellstate.ListAtPath(filepath.Join(stateDir, "projects", "demo", "shells.json"))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	for _, def := range defs {
		names[def.TmuxName] = def.DisplayName
	}
	return names
}

func TestShellRenameTargetRenamesAnotherShell(t *testing.T) {
	stateDir, _ := targetProject(t)

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"shell", "rename", "--target", "sidecar-sh-demo-2", "--json", "  release prep  "}, &out, &errOut)
	if !handled || code != 0 || errOut.Len() != 0 {
		t.Fatalf("rename --target = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	var result shellstate.RenameResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("stdout is not one JSON object: %q: %v", out.String(), err)
	}
	if !result.Changed || result.Shell != "sidecar-sh-demo-2" || result.OldName != "two" || result.Name != "release prep" {
		t.Fatalf("result = %+v", result)
	}

	names := manifestNames(t, stateDir)
	if names["sidecar-sh-demo-2"] != "release prep" {
		t.Fatalf("target not renamed: %v", names)
	}
	if names["sidecar-sh-demo-1"] != "one" {
		t.Fatalf("rename touched another record: %v", names)
	}

	repaint := findRenameRequest(t, stateDir, "-rename-shell.json")
	if repaint.Origin.TmuxSession != "sidecar-sh-demo-2" || repaint.Target.Value != "release prep" {
		t.Fatalf("repaint request = %+v", repaint)
	}
}

func TestShellRenameTargetRefusesUnregisteredSession(t *testing.T) {
	stateDir, _ := targetProject(t)

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"shell", "rename", "--target", "sidecar-sh-someone-else", "--json", "mine now"}, &out, &errOut)
	if !handled || code != shellTargetUnregistered {
		t.Fatalf("rename unregistered = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "no registered Sidecar shell or worktree session named") {
		t.Fatalf("stderr = %q", errOut.String())
	}
	names := manifestNames(t, stateDir)
	if names["sidecar-sh-demo-1"] != "one" || names["sidecar-sh-demo-2"] != "two" {
		t.Fatalf("a refused rename changed the manifest: %v", names)
	}
}

// The uniqueness guard belongs to RenameAtPath. The verb's job is to surface
// it as a validation error rather than a generic failure.
func TestShellRenameTargetSurfacesNameInUse(t *testing.T) {
	stateDir, _ := targetProject(t)

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"shell", "rename", "--target", "sidecar-sh-demo-2", "one"}, &out, &errOut)
	if !handled || code != 2 {
		t.Fatalf("duplicate name = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "already in use") {
		t.Fatalf("stderr = %q, want the manifest's own refusal", errOut.String())
	}
	if names := manifestNames(t, stateDir); names["sidecar-sh-demo-2"] != "two" {
		t.Fatalf("refused rename still wrote: %v", names)
	}
}

func TestShellRenameTargetRenamesRegisteredWorktree(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	repo := filepath.Join(root, "repo")
	topic := filepath.Join(root, "repo-topic")
	initGitRepo(t, repo)
	runGit(t, repo, "worktree", "add", "-b", "topic", topic)
	writeRegisteredWorktree(t, stateDir, repo, topic)
	t.Chdir(topic)

	session := workspaceops.WorktreeSessionName(topic, "")
	var out, errOut bytes.Buffer
	handled, code := Run([]string{"shell", "rename", "--target", session, "--json", "auth work"}, &out, &errOut)
	if !handled || code != 0 || errOut.Len() != 0 {
		t.Fatalf("rename worktree = handled %v code %d stderr %q stdout %q", handled, code, errOut.String(), out.String())
	}
	var result shellstate.RenameResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("stdout is not one JSON object: %q: %v", out.String(), err)
	}
	if !result.Changed || result.Shell != session || result.Name != "auth work" {
		t.Fatalf("result = %+v", result)
	}
	got, err := workspaceops.LookupWorktreeDisplayName(stateDir, repo, topic)
	if err != nil || got != "auth work" {
		t.Fatalf("persisted worktree name = %q, %v", got, err)
	}
	repaint := findRenameRequest(t, stateDir, "-rename-worktree.json")
	if repaint.Origin.WorkDir != topic || repaint.Target.Value != "auth work" {
		t.Fatalf("repaint request = %+v", repaint)
	}
}

// Without --target the verb must still resolve identity from the local tmux
// environment, with the same message it has always produced.
func TestShellRenameWithoutTargetKeepsCurrentShellPath(t *testing.T) {
	setupIsolatedCLI(t)

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"shell", "rename", "current work"}, &out, &errOut)
	if !handled || code != 1 {
		t.Fatalf("rename with no target = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "not inside tmux; run this command from a Sidecar project shell") {
		t.Fatalf("stderr = %q, want the unchanged current-shell refusal", errOut.String())
	}
}

func TestShellRenameProjectWithoutTargetIsRefused(t *testing.T) {
	setupIsolatedCLI(t)
	for _, flag := range []string{"--project", "--shell"} {
		var out, errOut bytes.Buffer
		handled, code := Run([]string{"shell", "rename", flag, "demo", "new name"}, &out, &errOut)
		if !handled || code != 2 {
			t.Fatalf("%s alone = handled %v code %d stderr %q", flag, handled, code, errOut.String())
		}
		if !strings.Contains(errOut.String(), flag+" only applies with --target") {
			t.Fatalf("stderr = %q", errOut.String())
		}
	}
}

func TestShellSendRefusesUnregisteredSession(t *testing.T) {
	targetProject(t)
	runner := useRecordingRunner(t)

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"shell", "send", "--target", "someone-elses-session", "--run", "rm -rf /"}, &out, &errOut)
	if !handled || code != shellTargetUnregistered {
		t.Fatalf("send unregistered = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "no registered Sidecar shell or worktree session named") {
		t.Fatalf("stderr = %q", errOut.String())
	}
	if len(runner.calls) != 0 {
		t.Fatalf("keys reached tmux for an unregistered session: %v", runner.calls)
	}
}

func TestShellSendRunsAndTypes(t *testing.T) {
	targetProject(t)

	runner := useRecordingRunner(t)
	var out, errOut bytes.Buffer
	handled, code := Run([]string{"shell", "send", "--target", "sidecar-sh-demo-2", "--run", "claude", "--json"}, &out, &errOut)
	if !handled || code != 0 || errOut.Len() != 0 {
		t.Fatalf("send --run = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	var result shellSendResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("stdout is not one JSON object: %q: %v", out.String(), err)
	}
	if !result.Sent || result.Shell != "sidecar-sh-demo-2" || result.Name != "two" || result.Kind != shellTargetKindShell || result.Mode != "run" || result.Command != "claude" {
		t.Fatalf("result = %+v", result)
	}
	want := []string{"send-keys", "-t", "sidecar-sh-demo-2", "claude", "Enter"}
	if len(runner.calls) != 1 || strings.Join(runner.calls[0], " ") != strings.Join(want, " ") {
		t.Fatalf("tmux calls = %v, want one %v", runner.calls, want)
	}

	typeRunner := useRecordingRunner(t)
	out.Reset()
	errOut.Reset()
	handled, code = Run([]string{"shell", "send", "--target", "sidecar-sh-demo-1", "--type", "git push"}, &out, &errOut)
	if !handled || code != 0 || errOut.Len() != 0 {
		t.Fatalf("send --type = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	want = []string{"send-keys", "-t", "sidecar-sh-demo-1", "git push"}
	if len(typeRunner.calls) != 1 || strings.Join(typeRunner.calls[0], " ") != strings.Join(want, " ") {
		t.Fatalf("tmux calls = %v, want one %v (no Enter)", typeRunner.calls, want)
	}
	if !strings.Contains(out.String(), "Typed") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestShellSendReachesRegisteredWorktreeSession(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	repo := filepath.Join(root, "repo")
	topic := filepath.Join(root, "repo-topic")
	initGitRepo(t, repo)
	runGit(t, repo, "worktree", "add", "-b", "topic", topic)
	writeRegisteredWorktree(t, stateDir, repo, topic)
	t.Chdir(topic)
	runner := useRecordingRunner(t)

	session := workspaceops.WorktreeSessionName(topic, "")
	var out, errOut bytes.Buffer
	handled, code := Run([]string{"shell", "send", "--target", session, "--run", "go test ./...", "--json"}, &out, &errOut)
	if !handled || code != 0 || errOut.Len() != 0 {
		t.Fatalf("send to worktree = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	var result shellSendResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Kind != shellTargetKindWorktree || result.Shell != session {
		t.Fatalf("result = %+v", result)
	}
	if len(runner.calls) != 1 || runner.calls[0][2] != session {
		t.Fatalf("tmux calls = %v", runner.calls)
	}
}

func TestShellSendValidation(t *testing.T) {
	for _, tt := range []struct {
		name     string
		args     []string
		contains string
	}{
		{"no target", []string{"shell", "send", "--run", "true"}, "requires --target"},
		{"no command", []string{"shell", "send", "--target", "sidecar-sh-demo-1"}, "requires --run or --type"},
		{"both", []string{"shell", "send", "--target", "x", "--run", "a", "--type", "b"}, "mutually exclusive"},
		{"positional", []string{"shell", "send", "--target", "x", "--run", "a", "extra"}, "takes no positional arguments"},
		{"unknown option", []string{"shell", "send", "--bogus"}, "unknown option"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			runner := useRecordingRunner(t)
			var out, errOut bytes.Buffer
			handled, code := Run(tt.args, &out, &errOut)
			if !handled || code != 2 {
				t.Fatalf("Run(%v) = handled %v code %d", tt.args, handled, code)
			}
			if !strings.Contains(errOut.String(), tt.contains) {
				t.Fatalf("stderr = %q, want %q", errOut.String(), tt.contains)
			}
			if len(runner.calls) != 0 {
				t.Fatalf("a usage error still sent keys: %v", runner.calls)
			}
		})
	}
}

func findRenameRequest(t *testing.T, stateDir, suffix string) uirequest.Request {
	t.Helper()
	dir := filepath.Join(stateDir, "requests")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read repaint requests: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), suffix) {
			continue
		}
		req, err := uirequest.ReadRequest(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		return req
	}
	t.Fatalf("no %s request in %v", suffix, entries)
	return uirequest.Request{}
}
