package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspaceops"
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

func TestCreateShellFromSiblingWorktree(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	repo := filepath.Join(root, "repo")
	topic := filepath.Join(root, "repo-topic")
	initGitRepo(t, repo)
	runGit(t, repo, "worktree", "add", "-b", "topic", topic)
	writeProjectMeta(t, stateDir, "demo", repo)
	writeRegisteredWorktree(t, stateDir, repo, topic)
	t.Chdir(topic)

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"create", "shell", "--name", "from-topic", "--json", "--wait", "0"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("from sibling worktree: handled %v code %d stderr %q", handled, code, errOut.String())
	}
	var result createShellResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", result.Shell.Session).Run() })
	if result.Shell.Session == "" {
		t.Fatal("missing session")
	}
}

func TestCreateShellFromSidecarWSIdentity(t *testing.T) {
	stateHome, stateDir := setupIsolatedCLI(t)
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	repo := filepath.Join(root, "repo")
	topic := filepath.Join(root, "repo-topic")
	initGitRepo(t, repo)
	runGit(t, repo, "worktree", "add", "-b", "topic", topic)
	writeProjectMeta(t, stateDir, "demo", repo)
	writeRegisteredWorktree(t, stateDir, repo, topic)
	t.Chdir(topic)

	socket := filepath.Join(t.TempDir(), "tmux.sock")
	binDir := t.TempDir()
	tmux := filepath.Join(binDir, "tmux")
	script := "#!/bin/sh\nprintf '%s\\t%s\\t%s\\n' sidecar-ws-repo-topic " + shellQuote(socket) + " " + shellQuote(topic) + "\n"
	if err := os.WriteFile(tmux, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX", socket+",1,0")
	t.Setenv("TMUX_PANE", "%1")
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"create", "shell", "--name", "from-ws", "--json", "--wait", "0"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("from sidecar-ws identity: handled %v code %d stderr %q stdout %q", handled, code, errOut.String(), out.String())
	}
	var result createShellResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", result.Shell.Session).Run() })
}

func TestCreateShellLookupOriginKindState(t *testing.T) {
	stateHome, socket := setupShellCLI(t, "stale")
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")
	t.Setenv("TMUX", socket+",1,0")
	t.Setenv("TMUX_PANE", "%1")
	projectDir := filepath.Join(stateHome, "sidecar", "projects", "sidecar")
	if err := os.Remove(filepath.Join(projectDir, "shells.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(projectDir, "shells.json"), 0755); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"create", "shell", "--wait", "0"}, &out, &errOut)
	if !handled || code != 1 {
		t.Fatalf("KindState = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "read registered shell manifest") {
		t.Fatalf("stderr = %q, want original KindState message", errOut.String())
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

func TestCreateWorktreeRequiredHookDoesNotLaunch(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{
  "plugins": {"workspace": {"worktreeSetup": {"runHook": true, "hookPath": ".worktree-setup.sh", "hookRequired": true}}}
}`), 0644); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(repo); err == nil {
		repo = resolved
	}
	initGitRepo(t, repo)
	if err := os.WriteFile(filepath.Join(repo, ".worktree-setup.sh"), []byte("#!/bin/bash\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	writeProjectMeta(t, stateDir, "demo", repo)

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"-config", cfgPath, "create", "worktree", "--json", "--wait", "0", "boom"}, &out, &errOut)
	if !handled || code != 1 {
		t.Fatalf("required hook: handled %v code %d stderr %q stdout %q", handled, code, errOut.String(), out.String())
	}
	var result createWorktreeResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("json: %v (%q)", err, out.String())
	}
	if workspaceops.SessionExists(result.Shell.Session) {
		t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", result.Shell.Session).Run() })
		t.Fatalf("required hook failure launched %s", result.Shell.Session)
	}
	head, err := exec.Command("git", "-C", result.Path, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	wtKey, err := projectdir.WorktreeKey(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	repoKey, err := workspaceops.RepoKeyForPath(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := workspaceops.LoadPendingCreation(t.Context(), repo, []workspaceops.WorktreeRecord{{
		Key: wtKey, Path: result.Path, HEADOID: strings.TrimSpace(string(head)),
	}}, repoKey)
	if err != nil {
		t.Fatal(err)
	}
	if journal == nil {
		t.Fatal("CLI journal not visible to LoadPendingCreation with plugin repoKey")
	}
	if journal.RepoKey != repoKey {
		t.Fatalf("journal.RepoKey = %q want %q", journal.RepoKey, repoKey)
	}
}

func initGitRepo(t *testing.T, repo string) {
	t.Helper()
	if err := os.MkdirAll(repo, 0755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hi\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-qm", "init")
}

func writeRegisteredWorktree(t *testing.T, stateDir, projectRoot, worktreePath string) {
	t.Helper()
	if _, err := projectdir.WorktreeDirWithBase(stateDir, projectRoot, worktreePath); err != nil {
		t.Fatal(err)
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
