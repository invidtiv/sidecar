package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentcontrol"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/workspaceops"
)

// TestWorktreePruneSessionsRealLifecycle is td-0b90da's repro end to end: a
// worktree removed with plain git leaves its sidecar-ws-… session running, and
// nothing in Sidecar said so or could close it.
//
// Every tmux call lands on this package's private server (internal/testenv
// pins TMUX_TMPDIR, setupIsolatedCLI clears TMUX), never the developer's.
func TestWorktreePruneSessionsRealLifecycle(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	_, stateDir := setupIsolatedCLI(t)
	root := filepath.Join(t.TempDir(), "repo")
	initGitRepoOnMain(t, root)
	root = canonicalTestPath(t, root)
	gone := filepath.Join(filepath.Dir(root), "repo-gone")
	kept := filepath.Join(filepath.Dir(root), "repo-kept")
	runGit(t, root, "worktree", "add", "-q", "-b", "gone", gone)
	runGit(t, root, "worktree", "add", "-q", "-b", "kept", kept)
	gone = canonicalTestPath(t, gone)
	kept = canonicalTestPath(t, kept)
	writeProjectMeta(t, stateDir, "demo", root)
	writeRegisteredWorktree(t, stateDir, root, gone)
	writeRegisteredWorktree(t, stateDir, root, kept)
	writeProjectShells(t, stateDir, "demo",
		shellstate.Definition{TmuxName: "sidecar-sh-gone", DisplayName: "Gone shell", WorkDir: gone},
		shellstate.Definition{TmuxName: "sidecar-sh-main", DisplayName: "Main shell", WorkDir: root},
	)

	goneSession := workspaceops.WorktreeSessionName(gone, "")
	keptSession := workspaceops.WorktreeSessionName(kept, "")
	sessions := map[string]string{
		goneSession: gone, keptSession: kept, "sidecar-sh-gone": gone, "sidecar-sh-main": root,
	}
	for name, dir := range sessions {
		if out, err := exec.Command("tmux", "new-session", "-d", "-s", name, "-c", dir).CombinedOutput(); err != nil {
			t.Skipf("cannot start a private tmux session: %v: %s", err, out)
		}
		t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", "="+name).Run() })
	}

	// The integrator's cleanup: git only, Sidecar never told.
	runGit(t, root, "worktree", "remove", "--force", gone)
	runGit(t, root, "branch", "-D", "gone")

	// agent list marks it. The listing needs a detected agent to produce rows,
	// so the marking step is exercised directly on the rows it would carry.
	agents := []agentcontrol.Agent{
		{Target: agentcontrol.Target{Session: goneSession}},
		{Target: agentcontrol.Target{Session: keptSession}},
	}
	markOrphanedAgents(Env{StateDir: stateDir, Ctx: t.Context()}, agents)
	if agents[0].Orphan == nil || agents[0].Orphan.Reason != "worktree_not_in_git" || agents[0].Orphan.Root != gone {
		t.Fatalf("removed worktree's agent = %+v, want an orphan of %s", agents[0].Orphan, gone)
	}
	if agents[1].Orphan != nil {
		t.Fatalf("live worktree's agent marked orphaned: %+v", agents[1].Orphan)
	}

	// shell list marks the shell rooted in the removed worktree.
	var out, errOut bytes.Buffer
	if handled, code := Run([]string{"shell", "list", "--project", "demo", "--json"}, &out, &errOut); !handled || code != 0 {
		t.Fatalf("shell list = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	var listed shellListResult
	if err := json.Unmarshal(out.Bytes(), &listed); err != nil {
		t.Fatalf("shell list JSON: %v (%q)", err, out.String())
	}
	for _, shell := range listed.Shells {
		want := ""
		if shell.Shell == "sidecar-sh-gone" {
			want = gone
		}
		if shell.OrphanedRoot != want {
			t.Errorf("shell %s orphanedRoot = %q, want %q", shell.Shell, shell.OrphanedRoot, want)
		}
	}

	// The plan names exactly the one session and the shell it will take along,
	// and changes nothing.
	out.Reset()
	errOut.Reset()
	handled, code := Run([]string{"worktree", "prune-sessions", "--plan", "--json"}, &out, &errOut)
	if !handled || code != 0 || errOut.Len() != 0 {
		t.Fatalf("plan = handled %v code %d stdout %q stderr %q", handled, code, out.String(), errOut.String())
	}
	var planned pruneSessionsDocument
	if err := json.Unmarshal(out.Bytes(), &planned); err != nil {
		t.Fatalf("plan JSON: %v (%q)", err, out.String())
	}
	if planned.Status != pruneStatusPlanned || len(planned.Orphans) != 1 {
		t.Fatalf("plan = %+v, want one orphan", planned)
	}
	orphan := planned.Orphans[0]
	if orphan.Session != goneSession || orphan.Project != "demo" || orphan.Root != gone || orphan.Result != "" {
		t.Fatalf("planned orphan = %+v", orphan)
	}
	if len(orphan.Shells) != 1 || orphan.Shells[0] != "sidecar-sh-gone" {
		t.Fatalf("planned shells = %v", orphan.Shells)
	}
	for name := range sessions {
		if !workspaceops.SessionExists(name) {
			t.Fatalf("planning closed %s", name)
		}
	}

	// Closing requires --yes.
	out.Reset()
	errOut.Reset()
	if _, code := Run([]string{"worktree", "prune-sessions"}, &out, &errOut); code != 2 {
		t.Fatalf("prune without --yes = %d, want usage error", code)
	}

	// A scope that excludes the orphan closes nothing.
	writeProjectMeta(t, stateDir, "other", kept)
	out.Reset()
	errOut.Reset()
	if _, code := Run([]string{"worktree", "prune-sessions", "--project", "other", "--yes", "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("scoped prune = %d stderr %q", code, errOut.String())
	}
	if !workspaceops.SessionExists(goneSession) {
		t.Fatal("a prune scoped to another project closed this one's orphan")
	}

	out.Reset()
	errOut.Reset()
	handled, code = Run([]string{"worktree", "prune-sessions", "--yes", "--json"}, &out, &errOut)
	if !handled || code != 0 || errOut.Len() != 0 {
		t.Fatalf("prune = handled %v code %d stdout %q stderr %q", handled, code, out.String(), errOut.String())
	}
	var pruned pruneSessionsDocument
	if err := json.Unmarshal(out.Bytes(), &pruned); err != nil {
		t.Fatalf("prune JSON: %v (%q)", err, out.String())
	}
	if pruned.Status != pruneStatusPruned || len(pruned.Orphans) != 1 || pruned.Orphans[0].Result != pruneResultClosed {
		t.Fatalf("prune = %+v", pruned)
	}
	for name := range sessions {
		wantAlive := name != goneSession && name != "sidecar-sh-gone"
		if got := workspaceops.SessionExists(name); got != wantAlive {
			t.Errorf("session %s alive = %v, want %v", name, got, wantAlive)
		}
	}
	manifestPath := filepath.Join(stateDir, "projects", "demo", "shells.json")
	tombs, err := shellstate.ListTombstonesAtPath(manifestPath)
	if err != nil || len(tombs) != 1 || tombs[0].TmuxName != "sidecar-sh-gone" {
		t.Fatalf("tombstones = %+v, %v; the closed shell must stay restorable", tombs, err)
	}

	// A second pass finds nothing.
	out.Reset()
	errOut.Reset()
	if _, code := Run([]string{"worktree", "prune-sessions", "--yes"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "No orphaned worktree sessions") {
		t.Fatalf("second prune = %d stdout %q stderr %q", code, out.String(), errOut.String())
	}
}

// A session whose name was reused by a worktree created at another path is
// that worktree's, even though the removed one is still registered.
func TestWorktreePruneSessionsLeavesAReusedNameAlone(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	_, stateDir := setupIsolatedCLI(t)
	root := filepath.Join(t.TempDir(), "repo")
	initGitRepoOnMain(t, root)
	root = canonicalTestPath(t, root)
	original := filepath.Join(filepath.Dir(root), "feature")
	runGit(t, root, "worktree", "add", "-q", "-b", "feature", original)
	original = canonicalTestPath(t, original)
	writeProjectMeta(t, stateDir, "demo", root)
	writeRegisteredWorktree(t, stateDir, root, original)
	runGit(t, root, "worktree", "remove", "--force", original)

	recreated := filepath.Join(filepath.Dir(root), "elsewhere", "feature")
	runGit(t, root, "worktree", "add", "-q", recreated, "feature")
	recreated = canonicalTestPath(t, recreated)
	session := workspaceops.WorktreeSessionName(recreated, "")
	if out, err := exec.Command("tmux", "new-session", "-d", "-s", session, "-c", recreated).CombinedOutput(); err != nil {
		t.Skipf("cannot start a private tmux session: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", "="+session).Run() })

	var out, errOut bytes.Buffer
	if _, code := Run([]string{"worktree", "prune-sessions", "--yes", "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("prune = %d stderr %q", code, errOut.String())
	}
	var doc pruneSessionsDocument
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("prune JSON: %v (%q)", err, out.String())
	}
	if len(doc.Orphans) != 0 || !workspaceops.SessionExists(session) {
		t.Fatalf("prune = %+v; the recreated worktree's session must survive", doc)
	}
}

func startTestSession(t *testing.T, name, dir string) {
	t.Helper()
	if out, err := exec.Command("tmux", "new-session", "-d", "-s", name, "-c", dir).CombinedOutput(); err != nil {
		t.Skipf("cannot start a private tmux session: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", "="+name).Run() })
}

func runPrune(t *testing.T, args ...string) (pruneSessionsDocument, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	_, code := Run(append([]string{"worktree", "prune-sessions", "--json"}, args...), &out, &errOut)
	var doc pruneSessionsDocument
	if out.Len() > 0 {
		if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
			t.Fatalf("prune JSON: %v (%q, stderr %q)", err, out.String(), errOut.String())
		}
	}
	return doc, code
}

// Review findings for td-0b90da, each a way a false orphan or a wrong kill
// could happen. The project is registered under its macOS temp spelling
// (/var/…, a symlink to /private/var/…) on purpose: the prune must find the
// registered project rather than create a second one.
func TestWorktreePruneSessionsSafetyCases(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	_, stateDir := setupIsolatedCLI(t)
	rawBase := t.TempDir()
	root := filepath.Join(rawBase, "repo")
	initGitRepoOnMain(t, root)
	writeProjectMeta(t, stateDir, "demo", root) // deliberately not canonicalized

	add := func(name string) string {
		path := filepath.Join(rawBase, name)
		runGit(t, root, "worktree", "add", "-q", "-b", name, path)
		writeRegisteredWorktree(t, stateDir, root, path)
		return path
	}
	recloned := add("repo-recloned")
	moved := add("repo-moved")
	removed := add("repo-removed")

	// A shell in the removed worktree whose session already died, next to a
	// live shell whose name it prefixes. Closing the first must not reach the
	// second.
	writeProjectShells(t, stateDir, "demo",
		shellstate.Definition{TmuxName: "sidecar-sh-repo-1", DisplayName: "dead", WorkDir: removed},
		shellstate.Definition{TmuxName: "sidecar-sh-repo-10", DisplayName: "sibling", WorkDir: root},
	)
	startTestSession(t, "sidecar-sh-repo-10", root)

	recSession := workspaceops.WorktreeSessionName(recloned, "")
	movedSession := workspaceops.WorktreeSessionName(moved, "")
	removedSession := workspaceops.WorktreeSessionName(removed, "")
	startTestSession(t, recSession, recloned)
	startTestSession(t, movedSession, moved)
	startTestSession(t, removedSession, removed)

	// Removed and then re-created as a plain clone at the same path: git no
	// longer lists it, and it is a live checkout.
	runGit(t, root, "worktree", "remove", "--force", recloned)
	runGit(t, rawBase, "clone", "-q", root, recloned)

	// Moved: the session's start directory is gone, but the agent in it
	// followed the checkout to its new home.
	movedTo := moved + "-new"
	runGit(t, root, "worktree", "move", moved, movedTo)
	if out, err := exec.Command("tmux", "send-keys", "-t", "="+movedSession+":", "cd "+shellQuote(movedTo), "Enter").CombinedOutput(); err != nil {
		t.Fatalf("send-keys: %v: %s", err, out)
	}

	runGit(t, root, "worktree", "remove", "--force", removed)

	plan, code := runPrune(t, "--plan")
	if code != 0 {
		t.Fatalf("plan exit %d", code)
	}
	planned := map[string]bool{}
	for _, orphan := range plan.Orphans {
		planned[orphan.Session] = true
	}
	if planned[recSession] {
		t.Fatal("a re-cloned directory at the removed path was judged an orphan")
	}
	// The moved session is planned (its start directory is gone and git no
	// longer lists that path); only the pre-kill pane check can spare it, which
	// is the point of this case.
	if !planned[removedSession] || !planned[movedSession] {
		t.Fatalf("plan = %+v, want the removed and moved worktrees' sessions", plan.Orphans)
	}

	// The moved agent's pane must be observed in its new directory before the
	// prune runs, or the check has nothing to see.
	waitForPanePath(t, movedSession, canonicalTestPath(t, movedTo))

	pruned, code := runPrune(t, "--yes")
	results := map[string]string{}
	for _, orphan := range pruned.Orphans {
		results[orphan.Session] = orphan.Result
	}
	if results[removedSession] != pruneResultClosed {
		t.Fatalf("removed session result = %q (all: %+v)", results[removedSession], pruned.Orphans)
	}
	if got := results[movedSession]; got != pruneResultChanged {
		t.Fatalf("moved session result = %q, want it left alone as changed", got)
	}
	if code != exitInputRejected {
		t.Fatalf("exit %d, want %d when a session was left alone", code, exitInputRejected)
	}
	for session, wantAlive := range map[string]bool{
		recSession: true, movedSession: true, "sidecar-sh-repo-10": true, removedSession: false,
	} {
		if got := workspaceops.SessionExists(session); got != wantAlive {
			t.Errorf("%s alive = %v, want %v", session, got, wantAlive)
		}
	}

	// The registered project was found, not re-registered under the
	// canonical spelling, and the dead shell's record was tombstoned there.
	entries, err := os.ReadDir(filepath.Join(stateDir, "projects"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("projects = %v, want only demo", names)
	}
	tombs, err := shellstate.ListTombstonesAtPath(filepath.Join(stateDir, "projects", "demo", "shells.json"))
	if err != nil || len(tombs) != 1 || tombs[0].TmuxName != "sidecar-sh-repo-1" {
		t.Fatalf("tombstones = %+v, %v", tombs, err)
	}
}

func waitForPanePath(t *testing.T, session, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := exec.Command("tmux", "display-message", "-p", "-t", "="+session+":", "#{pane_current_path}").Output()
		if got := strings.TrimSpace(string(out)); got != "" && workspaceops.CanonicalWorkPath(got) == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("pane of %s never reached %s", session, want)
}
