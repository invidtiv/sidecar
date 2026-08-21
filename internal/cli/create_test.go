package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/uirequest"
)

func TestCreateShellValidation(t *testing.T) {
	for _, tt := range []struct {
		name     string
		args     []string
		code     int
		contains string
	}{
		{"unknown option", []string{"create", "shell", "--bogus"}, 2, "unknown option"},
		{"positional", []string{"create", "shell", "extra"}, 2, "takes no positional"},
		{"run and type", []string{"create", "shell", "--run", "true", "--type", "false"}, 2, "mutually exclusive"},
		{"split bare", []string{"create", "shell", "--split"}, 2, "terminal-splits Phase A"},
		{"split auto", []string{"create", "shell", "--split", "auto"}, 2, "panelayout Terminal"},
		{"split right", []string{"create", "shell", "--split=right"}, 2, "terminal-splits Phase A"},
		{"split invalid", []string{"create", "shell", "--split", "diagonal"}, 2, "invalid split option"},
		{"unknown create kind", []string{"create", "wat"}, 2, "unknown create command"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			handled, code := Run(tt.args, &out, &errOut)
			if !handled || code != tt.code {
				t.Fatalf("Run(%v) = handled %v, code %d; want true, %d (%s)", tt.args, handled, code, tt.code, errOut.String())
			}
			combined := out.String() + errOut.String()
			if !strings.Contains(combined, tt.contains) {
				t.Fatalf("output for %v missing %q; got %q", tt.args, tt.contains, combined)
			}
		})
	}
}

func TestCreateShellUnknownProjectDoesNotInitState(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	orphan := t.TempDir()
	t.Chdir(orphan)

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"create", "shell", "--wait", "0"}, &out, &errOut)
	if !handled || code != 2 {
		t.Fatalf("Run() = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "registered") {
		t.Fatalf("stderr = %q", errOut.String())
	}
	entries, err := os.ReadDir(filepath.Join(stateDir, "projects"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("unknown project initialized state: %v", entries)
	}

	out.Reset()
	errOut.Reset()
	handled, code = Run([]string{"create", "shell", "--project", "nosuch", "--wait", "0"}, &out, &errOut)
	if !handled || code != 2 {
		t.Fatalf("--project unknown = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "unknown project") {
		t.Fatalf("stderr = %q", errOut.String())
	}
	entries, err = os.ReadDir(filepath.Join(stateDir, "projects"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("--project unknown initialized state: %v", entries)
	}
}

func TestCreateShellJSONNoAckNonFatal(t *testing.T) {
	stateHome, stateDir := setupIsolatedCLI(t)
	workDir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(workDir); err == nil {
		workDir = resolved
	}
	t.Chdir(workDir)
	writeProjectMeta(t, stateDir, "demo", workDir)

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"create", "shell", "--name", "dev server", "--json", "--wait", "0"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("Run() = handled %v code %d stderr %q stdout %q", handled, code, errOut.String(), out.String())
	}

	var result createShellResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("json: %v (%q)", err, out.String())
	}
	if result.Acked {
		t.Fatal("expected acked=false with no instance")
	}
	if result.Placement != createPlacementWorkspace {
		t.Fatalf("placement = %q", result.Placement)
	}
	if result.Shell.DisplayName != "dev server" || result.Shell.WorkDir != workDir || result.Shell.Session == "" {
		t.Fatalf("shell = %+v", result.Shell)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "kill-session", "-t", result.Shell.Session).Run()
	})

	listed, err := shellstate.ListAtPath(filepath.Join(stateDir, "projects", "demo", "shells.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].DisplayName != "dev server" || listed[0].TmuxName != result.Shell.Session {
		t.Fatalf("manifest = %+v", listed)
	}

	reqs, err := os.ReadDir(filepath.Join(stateHome, "sidecar", "requests"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range reqs {
		if !strings.Contains(entry.Name(), string(uirequest.ActionCreate)) {
			continue
		}
		found = true
	}
	if !found {
		t.Fatalf("no create request in %v", reqs)
	}
}

func TestCreateListedForAgents(t *testing.T) {
	rendered := RenderAgents(RootCommand())
	if !strings.Contains(rendered, "sidecar create shell") {
		t.Fatalf("sidecar --agents does not list create shell:\n%s", rendered)
	}
	if !strings.Contains(rendered, "sidecar create worktree") {
		t.Fatalf("sidecar --agents does not list create worktree:\n%s", rendered)
	}
}

func TestCreateWorktreeValidation(t *testing.T) {
	for _, tt := range []struct {
		name     string
		args     []string
		code     int
		contains string
	}{
		{"missing name", []string{"create", "worktree"}, 2, "exactly one name"},
		{"no-launch with agent", []string{"create", "worktree", "--no-launch", "--agent", "claude", "x"}, 2, "--no-launch cannot be combined"},
		{"no-launch with run", []string{"create", "worktree", "--run", "true", "--no-launch", "x"}, 2, "--no-launch cannot be combined"},
		{"unknown option", []string{"create", "worktree", "--bogus", "x"}, 2, "unknown option"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			handled, code := Run(tt.args, &out, &errOut)
			if !handled || code != tt.code {
				t.Fatalf("Run(%v) = handled %v, code %d; want true, %d (%s)", tt.args, handled, code, tt.code, errOut.String())
			}
			if !strings.Contains(errOut.String()+out.String(), tt.contains) {
				t.Fatalf("output missing %q; got %q", tt.contains, errOut.String()+out.String())
			}
		})
	}
}

func TestCreateWorktreeUnknownProjectDoesNotInitState(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	t.Chdir(t.TempDir())
	var out, errOut bytes.Buffer
	handled, code := Run([]string{"create", "worktree", "--wait", "0", "topic"}, &out, &errOut)
	if !handled || code != 2 {
		t.Fatalf("Run() = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	entries, err := os.ReadDir(filepath.Join(stateDir, "projects"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("unknown project initialized state: %v", entries)
	}
}

func TestCreateWorktreeNoLaunchHonorsHook(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(repo); err == nil {
		repo = resolved
	}
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hi\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".env"), []byte("SECRET=1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(repo, ".worktree-setup.sh")
	if err := os.WriteFile(hook, []byte("#!/bin/bash\necho hooked > HOOK_RAN\n"), 0755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-qm", "init")
	t.Chdir(repo)
	writeProjectMeta(t, stateDir, "demo", repo)

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"-config", cfgPath, "create", "worktree", "--no-launch", "--json", "--wait", "0", "cli-wt"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("Run() = handled %v code %d stderr %q stdout %q", handled, code, errOut.String(), out.String())
	}
	var result createWorktreeResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("json: %v (%q)", err, out.String())
	}
	if result.Acked {
		t.Fatal("expected acked=false")
	}
	if result.Branch == "" || result.Path == "" || result.Shell.Session == "" {
		t.Fatalf("result = %+v", result)
	}
	if !strings.HasPrefix(result.Shell.Session, "sidecar-ws-") {
		t.Fatalf("session = %q", result.Shell.Session)
	}
	if _, err := os.Stat(filepath.Join(result.Path, "HOOK_RAN")); err != nil {
		t.Fatalf("hook did not run in %s: %v", result.Path, err)
	}
	if _, err := os.Stat(filepath.Join(result.Path, ".env")); err != nil {
		t.Fatalf("env file was not copied: %v", err)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %s: %v", strings.Join(args, " "), out, err)
	}
}
