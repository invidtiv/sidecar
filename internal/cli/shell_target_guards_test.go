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
		{"shell", "delete", "--target", "sidecar-sh-demo-1"},
		// A flag VALUE must not disarm the gate. The first version of this
		// scanned every argument for -h/--help/help, so `--run help` — an
		// ordinary thing to send into a shell — sailed past it and reached
		// tmux. Same for a positional after the `--` terminator.
		{"shell", "send", "--target", "sidecar-sh-demo-1", "--run", "help"},
		{"shell", "send", "--target", "sidecar-sh-demo-1", "--run", "--help"},
		{"shell", "send", "--target", "sidecar-sh-demo-1", "--type", "-h"},
		{"shell", "delete", "--target", "help"},
		{"shell", "rename", "--target", "sidecar-sh-demo-1", "--", "help"},
		{"shell", "rename", "--target", "sidecar-sh-demo-1", "--", "--help"},
		{"create", "worktree", "--base", "help", "wt-proof"},
		{"create", "worktree", "--", "help"},
		{"create", "shell", "--tab", "--name", "help"},
		// `host serve` reaps: it tombstones a shell record whose session is
		// confirmed gone, through the same flocked writer the browser uses. That
		// is state outside this process, so the gate arms before the loop
		// starts — a proof run that forgot to move the state tree must be
		// refused before it observes anything, not after it has tombstoned a
		// record in the developer's real manifest. --cycles bounds the damage if
		// this ever regresses: an unarmed gate then runs one cycle instead of
		// serving until stdin closes.
		{"host", "serve", "--stdio", "--cycles", "1"},
		// And a flag VALUE must not disarm it here either.
		{"host", "serve", "--stdio", "--cycles", "1", "--project", "help"},
		{"host", "serve", "--stdio", "--cycles", "--help"},
		// These three write the notification log and the request bus, which is
		// state outside this process by the same definition the tmux and git
		// verbs meet.
		{"open", "README.md"},
		{"notify", "post", "hello"},
		{"notify", "dismiss", "ntf-000000000000-00000000"},
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
	// 5, not 2: a --project nobody can resolve is a verdict on a value, and
	// exit 2 is what the viewer reads as "update Sidecar on one of these
	// machines" — which is exactly the wrong instruction for a stale entry in
	// the host's own configured project list.
	if !handled || code != exitInputRejected || !strings.Contains(errOut.String(), "unknown project") {
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

	if _, ok := configuredProjectFallback(stateDir, repo, registerProject); !ok {
		t.Fatal("configuredProjectFallback did not resolve a configured project in an isolated tree")
	}
	if _, ok := configuredProjectFallback(config.RealUserStateDir(), repo, registerProject); ok {
		t.Fatal("configuredProjectFallback registered a project inside the real user state tree")
	}
}

// TestCreateShellRejectedNameIsFinal. Beside-the-session placement is only the
// DEFAULT, so a decline falls back to a workspace shell. A verdict on the
// command itself is not a decline: it will be the same verdict wherever the
// shell would have gone.
//
// Exit 5 was missing from the list of codes that stop there, so a rejected
// --name printed its refusal, then "created a workspace shell instead", then
// the refusal again — announcing a shell that was never created.
func TestCreateShellRejectedNameIsFinal(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	workDir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(workDir); err == nil {
		workDir = resolved
	}
	t.Chdir(workDir)
	writeProjectMeta(t, stateDir, "demo", workDir)
	writeProjectShells(t, stateDir, "demo",
		shellstate.Definition{TmuxName: "sidecar-sh-demo-1", DisplayName: "one", Namespace: tmuxenv.Namespace(), WorkDir: workDir},
	)

	tooLong := strings.Repeat("n", 51)
	var out, errOut bytes.Buffer
	handled, code := Run([]string{"create", "shell", "--shell", "sidecar-sh-demo-1", "--name", tooLong, "--wait", "0"}, &out, &errOut)
	if !handled || code != exitInputRejected {
		t.Fatalf("rejected --name = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	if strings.Contains(errOut.String(), "created a workspace shell instead") {
		t.Fatalf("a rejected name was reported as a created shell:\n%s", errOut.String())
	}
	if got := strings.Count(errOut.String(), "too long"); got != 1 {
		t.Fatalf("the refusal was printed %d times:\n%s", got, errOut.String())
	}
	// And nothing was created under either placement.
	defs, err := shellstate.ListAtPath(filepath.Join(stateDir, "projects", "demo", "shells.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 1 {
		t.Fatalf("a rejected name still created a shell: %+v", defs)
	}
}

// TestShellRenameCurrentAcceptsALeadingDashName. The two forms of one verb must
// agree about what a display name is: --target already accepted `-- -wip`, and
// this is the form every agent runs in its own shell.
func TestShellRenameCurrentAcceptsALeadingDashName(t *testing.T) {
	setupIsolatedCLI(t)

	// No tmux identity here, so the rename cannot complete — but it must get
	// as far as resolving one, which means the name parsed. Before `--`, the
	// parser refused first with "unknown option".
	var out, errOut bytes.Buffer
	handled, code := Run([]string{"shell", "rename", "--", "-wip"}, &out, &errOut)
	if !handled {
		t.Fatalf("shell rename -- -wip was not handled")
	}
	if strings.Contains(errOut.String(), "unknown option") {
		t.Fatalf("the terminator did not end flag parsing: %q", errOut.String())
	}
	if code != 1 || !strings.Contains(errOut.String(), "not inside tmux") {
		t.Fatalf("rename -- -wip = code %d stderr %q; want the unchanged current-shell identity refusal", code, errOut.String())
	}

	// The terminator also must not be read as a flag by the form chooser:
	// `rename -- --target` names a shell, it does not pass --target.
	out.Reset()
	errOut.Reset()
	handled, code = Run([]string{"shell", "rename", "--", "--target"}, &out, &errOut)
	if !handled || strings.Contains(errOut.String(), "--target requires") {
		t.Fatalf("`rename -- --target` was routed to the --target form: code %d stderr %q", code, errOut.String())
	}
}

// TestShellListDoesNotRegisterAProjectItOnlyRead. The finding-8 fallback has to
// register for a writer — it is about to need somewhere to write — but a read
// that materialises state is a read with a side effect, and `shell list` is
// exactly the verb an agent runs to find out whether there is anything there.
func TestShellListDoesNotRegisterAProjectItOnlyRead(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	repo, cfgPath := configuredOnlyProject(t)
	t.Chdir(t.TempDir())

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"-config", cfgPath, "shell", "list", "--project", repo, "--json"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("shell list on a never-opened project = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	if !strings.Contains(out.String(), `"shells":[]`) && !strings.Contains(out.String(), `"shells":null`) {
		t.Fatalf("stdout = %q, want an empty shell list", out.String())
	}
	entries, err := os.ReadDir(filepath.Join(stateDir, "projects"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("a read registered %d project directories: %v", len(entries), entries)
	}

	// The writer on the same project still gets its directory.
	out.Reset()
	errOut.Reset()
	if handled, code := Run([]string{"-config", cfgPath, "create", "worktree", "--project", repo, "--plan", "--json", "fix-auth"}, &out, &errOut); !handled || code != 0 {
		t.Fatalf("create worktree = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	if entries, err := os.ReadDir(filepath.Join(stateDir, "projects")); err != nil || len(entries) != 1 {
		t.Fatalf("a writer did not register the project: %v %v", entries, err)
	}
}

// TestShellRenameTargetPrefersTheRegisteredWorktree. WorktreeSessionName is
// "sidecar-ws-" plus the last path element, so two worktrees of one repo in
// differently named parent directories share a session name. Searching git's
// full list for a --target turned a rename that had always resolved uniquely
// against the registered set into an ambiguity refusal.
func TestShellRenameTargetPrefersTheRegisteredWorktree(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	repo := filepath.Join(root, "repo")
	initGitRepo(t, repo)
	registered := filepath.Join(root, "a", "feature")
	discovered := filepath.Join(root, "b", "feature")
	runGit(t, repo, "worktree", "add", "-b", "reg", registered)
	runGit(t, repo, "worktree", "add", "-b", "disc", discovered)
	// writeRegisteredWorktree registers the project as a side effect, exactly
	// as first use does; a second hand-written meta.json would make two
	// project directories for one repo.
	writeRegisteredWorktree(t, stateDir, repo, registered)
	t.Chdir(repo)

	session := workspaceops.WorktreeSessionName(registered, "")
	if session != workspaceops.WorktreeSessionName(discovered, "") {
		t.Fatalf("the fixture does not produce a collision: %q", session)
	}

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"shell", "rename", "--target", session, "--json", "the registered one"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("collision = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	got, err := workspaceops.LookupWorktreeDisplayName(stateDir, repo, registered)
	if err != nil || got != "the registered one" {
		t.Fatalf("registered worktree name = %q, %v", got, err)
	}
}

// TestStaleConfiguredProjectIsARejectedValueNotVersionSkew. `sidecar host
// serve` advertises every config.projects.list entry without checking the path
// still exists, so the commonest way to reach "unknown project" from another
// machine is a stale entry in the host's own configuration.
//
// internal/hosts reads exit 2 as "the two Sidecars disagree about this verb"
// and renders "update Sidecar on whichever machine is older". That is a wrong
// instruction for a directory the user moved: the code has to be 5, which
// hosts renders as the host's own words.
func TestStaleConfiguredProjectIsARejectedValueNotVersionSkew(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	gone := filepath.Join(t.TempDir(), "moved-away")
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	body := `{"projects":{"list":[{"name":"Gone","path":` + quoteJSON(t, gone) + `}]}}`
	if err := os.WriteFile(cfgPath, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())

	for _, name := range []string{gone, "Gone", "moved-away"} {
		var out, errOut bytes.Buffer
		handled, code := Run([]string{"-config", cfgPath, "create", "worktree", "--project", name, "--plan", "--json", "x"}, &out, &errOut)
		if !handled || code != exitInputRejected {
			t.Fatalf("--project %q on a stale entry = handled %v code %d stderr %q, want %d",
				name, handled, code, errOut.String(), exitInputRejected)
		}
		if !strings.Contains(errOut.String(), "unknown project") {
			t.Fatalf("stderr = %q", errOut.String())
		}
	}
	if entries, err := os.ReadDir(filepath.Join(stateDir, "projects")); err != nil || len(entries) != 0 {
		t.Fatalf("a configured path that is not there still registered state: %v %v", entries, err)
	}
}
