package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/workspaceops"
)

// TestShellSendRefusesARecordOnAnotherTmuxServer is the guard that stops this
// verb typing into a stranger.
//
// resolveShellTarget proves a record exists under the namespace the record
// names; the runner sends keys to the server THIS process resolves. With those
// two different, a session answering to the same name on this server is not the
// session that was proved — and `send-keys` does not ask. The reproduction is
// exactly the reviewer's: record the shell under a namespace that is not this
// process's socket and watch the keys go out anyway.
//
// Deleting the sameTmuxServer call makes this test fail and nothing else.
func TestShellSendRefusesARecordOnAnotherTmuxServer(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	workDir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(workDir); err == nil {
		workDir = resolved
	}
	t.Chdir(workDir)
	writeProjectMeta(t, stateDir, "demo", workDir)
	elsewhere := filepath.Join(t.TempDir(), "tmux-elsewhere", "default")
	writeProjectShells(t, stateDir, "demo",
		shellstate.Definition{TmuxName: "sidecar-sh-demo-2", DisplayName: "two", Namespace: elsewhere, WorkDir: workDir},
	)
	runner := useRecordingRunner(t)

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"shell", "send", "--target", "sidecar-sh-demo-2", "--run", "echo hi"}, &out, &errOut)
	if !handled || code != shellTargetUnregistered {
		t.Fatalf("send across servers = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	if len(runner.calls) != 0 {
		t.Fatalf("keys reached a tmux server the record does not name: %v", runner.calls)
	}
	for _, want := range []string{elsewhere, tmuxenv.Namespace()} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("stderr %q does not name %q", errOut.String(), want)
		}
	}
}

// A rename across tmux servers is still correct — it edits the manifest, not a
// live session — so the namespace guard must not spread to it.
func TestShellRenameStillWorksAcrossTmuxServers(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	workDir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(workDir); err == nil {
		workDir = resolved
	}
	t.Chdir(workDir)
	writeProjectMeta(t, stateDir, "demo", workDir)
	elsewhere := filepath.Join(t.TempDir(), "tmux-elsewhere", "default")
	writeProjectShells(t, stateDir, "demo",
		shellstate.Definition{TmuxName: "sidecar-sh-demo-2", DisplayName: "two", Namespace: elsewhere, WorkDir: workDir},
	)

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"shell", "rename", "--target", "sidecar-sh-demo-2", "--json", "release prep"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("rename across servers = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	if names := manifestNames(t, stateDir); names["sidecar-sh-demo-2"] != "release prep" {
		t.Fatalf("record not renamed: %v", names)
	}
}

// TestShellRenameTargetAcceptsALeadingDashName: NormalizeName accepts "-wip",
// so it is a legal display name here and in the TUI. Without `--` the parser
// answered `unknown option "-wip"` with exit 2, which across a host boundary
// reads as version skew and tells the user to update Sidecar.
func TestShellRenameTargetAcceptsALeadingDashName(t *testing.T) {
	stateDir, _ := targetProject(t)

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"shell", "rename", "--target", "sidecar-sh-demo-2", "--json", "--", "-wip"}, &out, &errOut)
	if !handled || code != 0 || errOut.Len() != 0 {
		t.Fatalf("rename -- -wip = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	var result shellstate.RenameResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("stdout is not one JSON object: %q: %v", out.String(), err)
	}
	if result.Name != "-wip" {
		t.Fatalf("result = %+v, want the leading-dash name", result)
	}
	if names := manifestNames(t, stateDir); names["sidecar-sh-demo-2"] != "-wip" {
		t.Fatalf("manifest = %v", names)
	}
}

// The same terminator on create worktree's positional, for the same reason.
func TestCreateWorktreeAcceptsALeadingDashName(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	repo, cfgPath := planTestRepo(t, stateDir)
	_ = repo

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"-config", cfgPath, "create", "worktree", "--plan", "--json", "--", "-fix"}, &out, &errOut)
	if !handled || code != 0 || errOut.Len() != 0 {
		t.Fatalf("--plan -- -fix = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	var plan struct {
		Branch string `json:"branch"`
	}
	if err := json.Unmarshal(out.Bytes(), &plan); err != nil {
		t.Fatalf("stdout is not one JSON object: %q: %v", out.String(), err)
	}
	if plan.Branch == "" {
		t.Fatalf("plan = %+v, want a resolved branch for a leading-dash name", plan)
	}
	// Without `--` the same value is read as a flag, which is the failure.
	out.Reset()
	errOut.Reset()
	handled, code = Run([]string{"-config", cfgPath, "create", "worktree", "--plan", "--json", "-fix"}, &out, &errOut)
	if !handled || code != 2 || !strings.Contains(errOut.String(), "unknown option") {
		t.Fatalf("bare -fix = handled %v code %d stderr %q; the terminator must be what makes the difference", handled, code, errOut.String())
	}
}

// TestShellRenameTargetRenamesADiscoveredWorktree: a worktree the user made
// with `git worktree add` is a row like any other, and renaming it locally
// works because RenameWorktreeDisplayName creates the state directory on
// demand. Resolving --target only against the worktrees Sidecar CREATED made
// the same operation exit 3 from another machine, for a row the user could see.
func TestShellRenameTargetRenamesADiscoveredWorktree(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	repo := filepath.Join(root, "repo")
	topic := filepath.Join(root, "repo-byhand")
	initGitRepo(t, repo)
	runGit(t, repo, "worktree", "add", "-b", "byhand", topic)
	// Deliberately NOT writeRegisteredWorktree: this worktree exists in git and
	// nowhere in Sidecar's state, which is what "discovered" means.
	writeProjectMeta(t, stateDir, "demo", repo)
	t.Chdir(repo)

	session := workspaceops.WorktreeSessionName(topic, "")
	var out, errOut bytes.Buffer
	handled, code := Run([]string{"shell", "rename", "--target", session, "--json", "by hand"}, &out, &errOut)
	if !handled || code != 0 || errOut.Len() != 0 {
		t.Fatalf("rename discovered worktree = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	got, err := workspaceops.LookupWorktreeDisplayName(stateDir, repo, topic)
	if err != nil || got != "by hand" {
		t.Fatalf("persisted worktree name = %q, %v", got, err)
	}
}

// TestMutatingVerbsRefuseAMisconfiguredProofBeforeTouchingAnything is the
// guarantee internal/hosts/run.go describes.
//
// cli.Run dispatches and os.Exit()s before cmd/sidecar/main.go's
// CheckStateIsolation is ever reached, so on a CLI verb that check did not run
// at all. What failed closed was the per-write AssertIsolatedPath, which fires
// AFTER `tmux new-session` and after `git worktree add` — a misconfigured proof
// left a real session or a real branch behind on the host and only then
// refused.
func TestMutatingVerbsRefuseAMisconfiguredProofBeforeTouchingAnything(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	repo, _ := planTestRepo(t, stateDir)
	writeProjectMeta(t, stateDir, "demo", repo)
	t.Chdir(repo)
	runner := useRecordingRunner(t)

	// Point the config axis back at the real user tree, which is precisely the
	// half-isolated proof run td-8d18de was.
	config.SetConfigPath(filepath.Join(config.RealUserConfigDir(), "config.json"))

	for _, args := range [][]string{
		{"create", "worktree", "wt-proof"},
		{"create", "shell", "--tab"},
		{"shell", "send", "--target", "sidecar-sh-demo-1", "--run", "echo hi"},
		{"shell", "rename", "--target", "sidecar-sh-demo-1", "renamed"},
	} {
		var out, errOut bytes.Buffer
		handled, code := Run(args, &out, &errOut)
		if !handled || code != 1 {
			t.Fatalf("Run(%v) = handled %v code %d, want a refusal", args, handled, code)
		}
		if !strings.Contains(errOut.String(), config.IsolationEnv) {
			t.Fatalf("Run(%v) stderr = %q, want the isolation refusal", args, errOut.String())
		}
	}
	if len(runner.calls) != 0 {
		t.Fatalf("a refused proof still reached tmux: %v", runner.calls)
	}
	// Nothing git-side happened either: the branch the first case asked for
	// does not exist and no worktree was added.
	for _, line := range gitLines(t, repo, "branch", "--format=%(refname:short)") {
		if strings.TrimSpace(line) == "wt-proof" {
			t.Fatal("a refused proof created branch wt-proof")
		}
	}

	// And the non-mutating verbs are untouched by the gate: reading is what a
	// misconfigured proof is still allowed to do.
	var out, errOut bytes.Buffer
	if handled, code := Run([]string{"shell", "list", "--json"}, &out, &errOut); !handled || code != 0 {
		t.Fatalf("shell list = handled %v code %d stderr %q; a read must not be gated", handled, code, errOut.String())
	}
	if handled, code := Run([]string{"create", "worktree", "--help"}, &out, &errOut); !handled || code != 0 {
		t.Fatalf("create worktree --help = handled %v code %d; help must not be gated", handled, code)
	}
}

// configuredOnlyProject writes a repo and a config that lists it, and
// deliberately registers no project state directory: this is a project the user
// has configured on a machine but never opened there.
func configuredOnlyProject(t *testing.T) (repo, cfgPath string) {
	t.Helper()
	repo = filepath.Join(t.TempDir(), "api-server")
	initGitRepo(t, repo)
	if resolved, err := filepath.EvalSymlinks(repo); err == nil {
		repo = resolved
	}
	cfgPath = filepath.Join(t.TempDir(), "config.json")
	body := `{"projects":{"list":[{"name":"API Server","path":` + quoteJSON(t, repo) + `}]}}`
	if err := os.WriteFile(cfgPath, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return repo, cfgPath
}

// TestConfiguredProjectResolvesBeforeItHasBeenOpened is the first-use path for
// remote hosts.
//
// `sidecar host serve` advertises config.projects.list, so a host's projects
// are in the picker as soon as it is reachable. --project resolved only
// $STATE/sidecar/projects/<slug>, which exists once a project has been OPENED
// on that machine — so a configured project that had never been opened there
// was visible, selectable, and refused by every mutation with `unknown
// project`, with no hint that the fix was to go and open it over there.
func TestConfiguredProjectResolvesBeforeItHasBeenOpened(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	repo, cfgPath := configuredOnlyProject(t)
	t.Chdir(t.TempDir())

	if entries, err := os.ReadDir(filepath.Join(stateDir, "projects")); err == nil && len(entries) != 0 {
		t.Fatalf("the fixture pre-registered the project: %v", entries)
	}

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"-config", cfgPath, "create", "worktree", "--project", repo, "--plan", "--json", "fix-auth"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("--project on a never-opened project = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	entries, err := os.ReadDir(filepath.Join(stateDir, "projects"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("the project was not registered on demand: %v %v", entries, err)
	}

	// By basename and by configured display name too, and a repeat must reuse
	// the same directory rather than allocate a -2 slug.
	for _, name := range []string{filepath.Base(repo), "API Server"} {
		out.Reset()
		errOut.Reset()
		handled, code := Run([]string{"-config", cfgPath, "create", "worktree", "--project", name, "--plan", "--json", "fix-auth"}, &out, &errOut)
		if !handled || code != 0 {
			t.Fatalf("--project %q = handled %v code %d stderr %q", name, handled, code, errOut.String())
		}
	}
	if entries, err := os.ReadDir(filepath.Join(stateDir, "projects")); err != nil || len(entries) != 1 {
		t.Fatalf("repeated resolution allocated more directories: %v %v", entries, err)
	}

	// An unknown project is still unknown, and still initializes nothing.
	out.Reset()
	errOut.Reset()
	handled, code = Run([]string{"-config", cfgPath, "create", "worktree", "--project", "not-a-project", "--plan", "x"}, &out, &errOut)
	if !handled || code != 2 || !strings.Contains(errOut.String(), "unknown project") {
		t.Fatalf("unknown project = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	if entries, err := os.ReadDir(filepath.Join(stateDir, "projects")); err != nil || len(entries) != 1 {
		t.Fatalf("an unknown project created state: %v %v", entries, err)
	}
}

// The on-demand registration is a write, so it obeys the same isolation
// discipline as every other writer rather than being a hole beside them.
func TestConfiguredProjectFallbackRefusesTheRealStateTree(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	repo, cfgPath := configuredOnlyProject(t)
	t.Chdir(t.TempDir())
	config.SetConfigPath(cfgPath)

	if _, ok := configuredProjectFallback(stateDir, repo); !ok {
		t.Fatal("configuredProjectFallback did not resolve a configured project in an isolated tree")
	}
	if _, ok := configuredProjectFallback(config.RealUserStateDir(), repo); ok {
		t.Fatal("configuredProjectFallback registered a project inside the real user state tree")
	}
}
