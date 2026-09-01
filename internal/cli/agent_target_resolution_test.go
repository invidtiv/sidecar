package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/agentcontrol"
	"github.com/marcus/sidecar/internal/managedtarget"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspaceops"
)

// sharedRepoState is the state tree td-ebd72c was reported against, reduced
// to its parts: one repository, and several registered projects that can all
// see it.
//
//   - "main" is the checkout Sidecar created the worktrees "main-topic" and
//     "main-feature" under.
//   - "main-topic" was later opened as a project of its own, so its state
//     directory names the worktree as ITS checkout. "main-feature" was not.
//   - "aside" is a subdirectory of the main checkout registered as a project
//     (the `.claude` entry in the report): not a worktree, but Git answers
//     `worktree list` from it with the whole inventory.
//   - "_" has no checkout path at all and lists the main checkout as a
//     registered worktree (the `_` entry in the report).
func sharedRepoState(t *testing.T) (stateDir, repo, worktree, feature string) {
	t.Helper()
	_, stateDir = setupIsolatedCLI(t)
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	repo = filepath.Join(root, "main")
	worktree = filepath.Join(root, "main-topic")
	feature = filepath.Join(root, "main-feature")
	initGitRepo(t, repo)
	runGit(t, repo, "worktree", "add", "-q", "-b", "topic", worktree)
	runGit(t, repo, "worktree", "add", "-q", "-b", "feature", feature)
	aside := filepath.Join(repo, "aside")
	if err := os.MkdirAll(aside, 0755); err != nil {
		t.Fatal(err)
	}

	writeProjectMeta(t, stateDir, "main", repo)
	writeRegisteredWorktree(t, stateDir, repo, worktree)
	writeRegisteredWorktree(t, stateDir, repo, feature)
	writeProjectMeta(t, stateDir, "main-topic", worktree)
	writeProjectMeta(t, stateDir, "aside", aside)
	writeProjectMeta(t, stateDir, "_", "")
	writeRegisteredWorktree(t, stateDir, "", repo)
	return stateDir, repo, worktree, feature
}

func worktreeCandidates(t *testing.T, stateDir string) map[string][]managedtarget.Target {
	t.Helper()
	projects, err := loadRegisteredProjects(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := managedTargetCandidates(Env{StateDir: stateDir, Ctx: context.Background()}, projects)
	if err != nil {
		t.Fatal(err)
	}
	byRoot := map[string][]managedtarget.Target{}
	for _, c := range candidates {
		if c.Kind == shellTargetKindWorktree {
			byRoot[c.WorktreeRoot] = append(byRoot[c.WorktreeRoot], c)
		}
	}
	return byRoot
}

// TestManagedTargetCandidatesOwnEachWorktreeOnce is td-ebd72c: one live pane
// was listed under six project keys because every registered project that
// could see a worktree emitted it. Each root now has exactly one owner — the
// project that created it, then the project whose checkout it is, then a
// project that merely discovered it — and a project with no checkout path
// owns nothing, rather than claiming the working directory.
func TestManagedTargetCandidatesOwnEachWorktreeOnce(t *testing.T) {
	stateDir, repo, worktree, feature := sharedRepoState(t)
	t.Chdir(repo)
	byRoot := worktreeCandidates(t, stateDir)

	for root, owners := range byRoot {
		if len(owners) != 1 {
			t.Errorf("root %s listed %d times: %+v", root, len(owners), owners)
		}
	}
	if got := byRoot[canonicalOpenPath(worktree)]; len(got) != 1 || got[0].Project != "main" || got[0].Priority != 1 {
		t.Fatalf("created worktree = %+v, want owned by main as a registered worktree", got)
	}
	if got := byRoot[canonicalOpenPath(repo)]; len(got) != 1 || got[0].Project != "main" || got[0].Priority != 1 {
		t.Fatalf("main checkout = %+v, want owned by main", got)
	}
	if got := byRoot[canonicalOpenPath(feature)]; len(got) != 1 || got[0].Project != "main" || got[0].Priority != 1 {
		t.Fatalf("second created worktree = %+v, want owned by main", got)
	}
	for root, owners := range byRoot {
		if owners[0].Project == "_" {
			t.Fatalf("a project with no checkout owns %s", root)
		}
	}

	// And so an explicit worktree target from OUTSIDE any managed shell — the
	// global search — resolves without a --project.
	session := workspaceops.WorktreeSessionName(worktree, "")
	tgt, code, err := findShellTarget(Env{StateDir: stateDir}, session, "", "", true, tmuxenv.Namespace())
	if err != nil || code != 0 || tgt.Project.Key != "main" || tgt.WorktreeRoot != canonicalOpenPath(worktree) {
		t.Fatalf("global resolve = %+v code=%d err=%v", tgt, code, err)
	}
}

// TestAgentListReportsOnePaneOncePerProjectKey drives the same state through
// the discovery command the coordinate-agents skill tells an agent to run
// first, since that is where the duplication was observed.
func TestAgentListReportsOnePaneOncePerProjectKey(t *testing.T) {
	idleScreen := codexIdleFixture(t)
	_, repo, worktree, _ := sharedRepoState(t)
	t.Chdir(repo)
	terminal := &cliAgentTerminal{launched: true, screen: idleScreen}
	useCLIAgentTerminal(t, terminal)

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"--enable-feature=agent_control", "agent", "list", "--json"}, &out, &errOut)
	if !handled || code != 0 || errOut.Len() != 0 {
		t.Fatalf("list = handled=%v code=%d stdout=%q stderr=%q", handled, code, out.String(), errOut.String())
	}
	var listed struct {
		Agents []agentcontrol.Agent `json:"agents"`
	}
	if err := json.Unmarshal(out.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, a := range listed.Agents {
		seen[a.Target.Session]++
		if a.Target.Project == "_" || a.Target.Project == "aside" && a.Target.Session == workspaceops.WorktreeSessionName(worktree, "") {
			t.Fatalf("pane %s reported under project %q", a.Target.Session, a.Target.Project)
		}
	}
	for session, n := range seen {
		if n != 1 {
			t.Fatalf("session %s listed %d times: %+v", session, n, listed.Agents)
		}
	}
	if _, ok := seen[workspaceops.WorktreeSessionName(worktree, "")]; !ok {
		t.Fatalf("the created worktree is missing from %+v", listed.Agents)
	}
}

// TestExplicitTargetNarrowsToTheCallerProject is td-c906c1's first
// refusal. Two projects each have a shell called "reviewer"; from a managed
// shell in one of them the bare name means that project's shell, because
// SIDECAR_SHELL already says which Sidecar the caller is in. Outside any
// managed shell the same name is ambiguous, and the refusal names the
// projects and the flag that picks one.
func TestExplicitTargetNarrowsToTheCallerProject(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	alpha := t.TempDir()
	beta := t.TempDir()
	writeProjectMeta(t, stateDir, "alpha", alpha)
	writeProjectMeta(t, stateDir, "beta", beta)
	ns := tmuxenv.Namespace()
	writeProjectShells(t, stateDir, "alpha",
		shellstate.Definition{TmuxName: "sidecar-sh-alpha-1", DisplayName: "orchestrator", Namespace: ns, WorkDir: alpha},
		shellstate.Definition{TmuxName: "sidecar-sh-alpha-2", DisplayName: "reviewer", Namespace: ns, WorkDir: alpha})
	writeProjectShells(t, stateDir, "beta",
		shellstate.Definition{TmuxName: "sidecar-sh-beta-1", DisplayName: "reviewer", Namespace: ns, WorkDir: beta})
	env := Env{StateDir: stateDir}

	t.Setenv(shellstate.SessionEnv, "")
	_, code, err := findShellTarget(env, "reviewer", "", "", true, ns)
	if err == nil || code != 1 {
		t.Fatalf("outside a shell = code=%d err=%v, want ambiguity", code, err)
	}
	for _, want := range []string{"alpha, beta", "--project", "--shell"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("ambiguity %q does not name %q", err.Error(), want)
		}
	}

	t.Setenv(shellstate.SessionEnv, "sidecar-sh-alpha-1")
	tgt, code, err := findShellTarget(env, "reviewer", "", "", true, ns)
	if err != nil || code != 0 || tgt.Session != "sidecar-sh-alpha-2" {
		t.Fatalf("from alpha = %+v code=%d err=%v", tgt, code, err)
	}
	// Narrowing only breaks ties. A name unique elsewhere still resolves
	// there, so a shell keeps addressing other projects by name.
	tgt, _, err = findShellTarget(env, "sidecar-sh-beta-1", "", "", true, ns)
	if err != nil || tgt.Project.Key != "beta" {
		t.Fatalf("cross-project by session = %+v err=%v", tgt, err)
	}
	// And an explicit --project still wins over the caller's own.
	tgt, _, err = findShellTarget(env, "reviewer", "", "beta", true, ns)
	if err != nil || tgt.Session != "sidecar-sh-beta-1" {
		t.Fatalf("--project beta = %+v err=%v", tgt, err)
	}

	// The same rule through the verb, in its JSON envelope.
	var out, errOut bytes.Buffer
	terminal := &cliAgentTerminal{launched: true, screen: codexIdleFixture(t)}
	useCLIAgentTerminal(t, terminal)
	handled, exit := Run([]string{"--enable-feature=agent_control", "agent", "get", "reviewer", "--json"}, &out, &errOut)
	if !handled || exit != 0 || errOut.Len() != 0 {
		t.Fatalf("get = handled=%v code=%d stdout=%q stderr=%q", handled, exit, out.String(), errOut.String())
	}
	var got agentcontrol.Agent
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Target.Session != "sidecar-sh-alpha-2" {
		t.Fatalf("get resolved %+v", got.Target)
	}
}

// TestCallerShellResolvesWithoutTmuxIdentity: the omitted-target rule reads
// SIDECAR_SHELL, and so does destination resolution now — for `create` and
// for `open` alike, so the two agree about who the caller is — and a harness
// that did not pass TMUX_PANE through still lands on the caller's project.
func TestCallerShellResolvesWithoutTmuxIdentity(t *testing.T) {
	stateDir, workDir := targetProject(t)
	t.Chdir(t.TempDir()) // not inside the project, so cwd cannot answer
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	t.Setenv(shellstate.SessionEnv, "sidecar-sh-demo-2")
	for name, resolve := range map[string]func(context.Context, string, string, string, projectRegistration) (openDestination, error){
		"create": resolveCreateDestination,
		"open":   resolveOpenDestination,
	} {
		dest, err := resolve(context.Background(), stateDir, "", "", resolveProjectOnly)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if dest.Origin.ProjectKey != "demo" || dest.Origin.TmuxSession != "sidecar-sh-demo-2" || dest.Origin.WorkDir != workDir || dest.Resolved != uirequest.ResolvedCurrentShell {
			t.Fatalf("%s dest = %+v", name, dest)
		}
	}
	t.Setenv(shellstate.SessionEnv, "sidecar-sh-nowhere-9")
	if _, err := resolveCreateDestination(context.Background(), stateDir, "", "", resolveProjectOnly); err == nil {
		t.Fatal("an unregistered SIDECAR_SHELL resolved a destination")
	}
}

// TestProjectFlagAcceptsACreatedWorktree is td-c906c1's second refusal:
// `create worktree --json` returns the worktree's path and display name, and
// neither was a value --project accepted. Both now resolve to the project
// that created the worktree, and the create result names that project too.
func TestProjectFlagAcceptsACreatedWorktree(t *testing.T) {
	idleScreen := codexIdleFixture(t)
	_, repo, worktree, feature := sharedRepoState(t)
	t.Chdir(t.TempDir())
	terminal := &cliAgentTerminal{launched: true, screen: idleScreen}
	useCLIAgentTerminal(t, terminal)
	session := workspaceops.WorktreeSessionName(feature, "")

	for _, selector := range []string{filepath.Base(feature), feature, "main", repo} {
		var out, errOut bytes.Buffer
		handled, code := Run([]string{"--enable-feature=agent_control", "agent", "get", session, "--project", selector, "--json"}, &out, &errOut)
		if !handled || code != 0 || errOut.Len() != 0 {
			t.Fatalf("--project %s = handled=%v code=%d stdout=%q stderr=%q", selector, handled, code, out.String(), errOut.String())
		}
		var got agentcontrol.Agent
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Target.Session != session || got.Target.Project != "main" {
			t.Fatalf("--project %s resolved %+v", selector, got.Target)
		}
	}
	// A registered project's slug outranks a worktree basename: the worktree
	// that was opened as its own project answers to its slug as that project.
	var out, errOut bytes.Buffer
	handled, code := Run([]string{"--enable-feature=agent_control", "agent", "get", workspaceops.WorktreeSessionName(worktree, ""), "--project", filepath.Base(worktree), "--json"}, &out, &errOut)
	if !handled || code != 0 || !strings.Contains(out.String(), `"project":"main-topic"`) {
		t.Fatalf("slug over basename = handled=%v code=%d stdout=%q stderr=%q", handled, code, out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	handled, code = Run([]string{"--enable-feature=agent_control", "agent", "get", session, "--project", "no-such-worktree", "--json"}, &out, &errOut)
	if !handled || code != 3 || !strings.Contains(errOut.String(), `"code":"agent_not_found"`) || !strings.Contains(errOut.String(), "unknown project") {
		t.Fatalf("unknown = handled=%v code=%d stderr=%q", handled, code, errOut.String())
	}
}

// TestProjectFlagIgnoresHowManyInstancesShowTheProject is td-c906c1's third
// refusal. Two running Sidecars showing one project is a question for a verb
// that must land a request on one of them; a lookup reads the project's
// manifest, which is one file however many instances have it open.
func TestProjectFlagIgnoresHowManyInstancesShowTheProject(t *testing.T) {
	idleScreen := codexIdleFixture(t)
	stateDir, workDir := targetProject(t)
	t.Chdir(t.TempDir())
	for range 2 {
		if err := uirequest.Announce(stateDir, uirequest.Instance{PID: startDummyProcess(t), ProjectKey: "demo", Project: "demo", WorkDir: workDir}); err != nil {
			t.Fatal(err)
		}
	}
	instances, err := uirequest.ListInstances(stateDir)
	if err != nil || len(instances) != 2 {
		t.Fatalf("instances = %+v, %v", instances, err)
	}
	terminal := &cliAgentTerminal{launched: true, screen: idleScreen}
	useCLIAgentTerminal(t, terminal)

	for _, verb := range [][]string{
		{"agent", "get", "sidecar-sh-demo-2", "--project", "demo", "--json"},
		{"agent", "list", "--project", "demo", "--json"},
		{"agent", "read", "sidecar-sh-demo-2", "--project", "demo", "--json"},
	} {
		var out, errOut bytes.Buffer
		handled, code := Run(append([]string{"--enable-feature=agent_control"}, verb...), &out, &errOut)
		if !handled || code != 0 || errOut.Len() != 0 {
			t.Fatalf("%v = handled=%v code=%d stdout=%q stderr=%q", verb, handled, code, out.String(), errOut.String())
		}
	}
	// `open` still has to pick an instance, and still says so.
	dest, err := resolveExplicitDestination(stateDir, "", "demo", resolveProjectOnly)
	if err == nil || !strings.Contains(err.Error(), "several Sidecar instances") {
		t.Fatalf("open destination = %+v err=%v, want the instance refusal kept", dest, err)
	}
}

// TestCwdProjectFollowsTheOwnershipOrder: the working directory resolves to
// the project the managed-target scan would name as the root's owner, so a
// rename resolved from inside a worktree writes the display name a global
// listing reads back. A state directory with no checkout that lists the main
// checkout as a worktree used to tie with the project whose checkout it is,
// leaving the working directory unresolvable.
func TestCwdProjectFollowsTheOwnershipOrder(t *testing.T) {
	stateDir, repo, worktree, _ := sharedRepoState(t)
	projects, err := loadRegisteredProjects(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	proj, root, ok := uniqueProjectContaining(projects, filepath.Join(repo, "internal"))
	if !ok || proj.Key != "main" || root != repo {
		t.Fatalf("cwd project = %+v root=%q ok=%v, want main", proj, root, ok)
	}
	// The worktree opened as its own project is still owned by the project
	// that created it, as the candidate scan says.
	proj, root, ok = uniqueProjectContaining(projects, filepath.Join(worktree, "internal"))
	if !ok || proj.Key != "main" || root != worktree {
		t.Fatalf("worktree cwd project = %+v root=%q ok=%v, want main", proj, root, ok)
	}
	byRoot := worktreeCandidates(t, stateDir)
	if owners := byRoot[canonicalOpenPath(worktree)]; len(owners) != 1 || owners[0].Project != proj.Key {
		t.Fatalf("scan owner %+v disagrees with cwd owner %q", owners, proj.Key)
	}
	// Two projects that both merely list a directory stay ambiguous.
	two := []registeredProject{
		{Key: "x", Path: "/elsewhere/x", Worktrees: []string{repo}},
		{Key: "y", Path: "/elsewhere/y", Worktrees: []string{repo}},
	}
	if _, _, ok := uniqueProjectContaining(two, repo); ok {
		t.Fatal("two non-owners resolved as unique")
	}
}

// TestExplicitTargetNarrowsFromAWorktreeSession: a worktree session
// (sidecar-ws-…) exports no SIDECAR_SHELL, and it is exactly where
// `create worktree --agent` puts an agent. There the session tmux reports is
// resolved to the project owning the worktree, and the same tie-break applies.
func TestExplicitTargetNarrowsFromAWorktreeSession(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	repo := filepath.Join(root, "repo")
	topic := filepath.Join(root, "repo-topic")
	initGitRepo(t, repo)
	runGit(t, repo, "worktree", "add", "-q", "-b", "topic", topic)
	writeProjectMeta(t, stateDir, "alpha", repo)
	writeRegisteredWorktree(t, stateDir, repo, topic)
	beta := t.TempDir()
	writeProjectMeta(t, stateDir, "beta", beta)

	// tmux reports the worktree session; SIDECAR_SHELL says nothing.
	socket := filepath.Join(t.TempDir(), "tmux.sock")
	binDir := t.TempDir()
	script := "#!/bin/sh\nprintf '%s\\t%s\\t%s\\n' sidecar-ws-repo-topic " + shellQuote(socket) + " " + shellQuote(topic) + "\n"
	if err := os.WriteFile(filepath.Join(binDir, "tmux"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX", socket+",1,0")
	t.Setenv("TMUX_PANE", "%1")
	t.Setenv(shellstate.SessionEnv, "")
	t.Chdir(t.TempDir())

	ns := tmuxenv.Namespace()
	writeProjectShells(t, stateDir, "alpha", shellstate.Definition{TmuxName: "sidecar-sh-alpha-1", DisplayName: "reviewer", Namespace: ns, WorkDir: repo})
	writeProjectShells(t, stateDir, "beta", shellstate.Definition{TmuxName: "sidecar-sh-beta-1", DisplayName: "reviewer", Namespace: ns, WorkDir: beta})

	tgt, code, err := findShellTarget(Env{StateDir: stateDir}, "reviewer", "", "", true, ns)
	if err != nil || code != 0 || tgt.Session != "sidecar-sh-alpha-1" {
		t.Fatalf("from the worktree session = %+v code=%d err=%v, want alpha's reviewer", tgt, code, err)
	}
}
