package cli

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/workspaceops"
)

// TestShellDeleteTombstonesTheRecord is the whole point of the verb: the record
// leaves the live list and lands in the tombstones, so `sidecar shell restore`
// still works afterwards — on a host as locally. A delete that merely dropped
// the entry would take the user's ability to undo it with it.
func TestShellDeleteTombstonesTheRecord(t *testing.T) {
	stateDir, _ := targetProject(t)

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"shell", "delete", "--target", "sidecar-sh-demo-2", "--json"}, &out, &errOut)
	if !handled || code != 0 || errOut.Len() != 0 {
		t.Fatalf("shell delete = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	var result shellDeleteResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("stdout is not one JSON object: %q: %v", out.String(), err)
	}
	if result.Shell != "sidecar-sh-demo-2" || result.Name != "two" || result.Status != shellStatusDeleted || !result.Deleted {
		t.Fatalf("result = %+v", result)
	}
	if !result.ValidRemoteResult() {
		t.Fatalf("a real result was not recognised as one: %+v", result)
	}

	manifest := filepath.Join(stateDir, "projects", "demo", "shells.json")
	live, err := shellstate.ListAtPath(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, def := range live {
		if def.TmuxName == "sidecar-sh-demo-2" {
			t.Fatalf("the deleted shell is still live: %+v", live)
		}
	}
	if len(live) != 1 {
		t.Fatalf("delete took more than its target: %+v", live)
	}
	tombs, err := shellstate.ListTombstonesAtPath(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findTombstone(tombs, "sidecar-sh-demo-2"); !ok {
		t.Fatalf("no tombstone was written, so `shell restore` cannot bring it back: %+v", tombs)
	}

	// And restore is the proof rather than the inference.
	out.Reset()
	errOut.Reset()
	if handled, code := Run([]string{"shell", "restore", "sidecar-sh-demo-2"}, &out, &errOut); !handled || code != 0 {
		t.Fatalf("restore after delete = handled %v code %d stderr %q", handled, code, errOut.String())
	}
}

// TestShellDeleteClosesTheTmuxSession is the half that distinguishes this verb
// from `shell forget`, and the half no unit over shellstate can prove: the
// session is really gone afterwards.
//
// The session lives on this test binary's private tmux server (internal/testenv
// pins TMUX_TMPDIR for the whole package), so nothing here can reach a real
// one — which is the standing rule for every tmux test in this repository.
func TestShellDeleteClosesTheTmuxSession(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	_, workDir := targetProject(t)

	const session = "sidecar-sh-demo-2"
	if out, err := exec.Command("tmux", "new-session", "-d", "-s", session, "-c", workDir).CombinedOutput(); err != nil {
		t.Skipf("cannot start a private tmux session: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", session).Run() })
	if !workspaceops.SessionExists(session) {
		t.Fatal("the fixture session did not start")
	}

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"shell", "delete", "--target", session}, &out, &errOut)
	if !handled || code != 0 || errOut.Len() != 0 {
		t.Fatalf("shell delete = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	if workspaceops.SessionExists(session) {
		t.Fatal("the tmux session outlived its record; this verb is `shell forget` with extra steps")
	}
	if !strings.Contains(out.String(), session) {
		t.Errorf("stdout does not say what was deleted: %q", out.String())
	}
}

// TestShellDeleteRefusesAnUnownedTarget. tmux resolves `-t <name>` against
// whatever answers to it, so the name a caller typed is not identity. This verb
// kills a session, which is the least recoverable thing the CLI does, so an
// unregistered name has to be refused with the distinct code — a caller on
// another machine must be able to tell "you addressed something Sidecar does
// not own" apart from "the state tree failed to read".
func TestShellDeleteRefusesAnUnownedTarget(t *testing.T) {
	targetProject(t)

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"shell", "delete", "--target", "sidecar-sh-someone-else-1", "--json"}, &out, &errOut)
	if !handled || code != shellTargetUnregistered {
		t.Fatalf("unowned target = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "no registered Sidecar shell") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

// TestShellDeleteRefusesAWorktree. A worktree session resolves through the same
// --target vocabulary `shell rename` uses, and this verb must not reinterpret
// it: removing a checkout carries branch-cleanup decisions delete cannot
// express. Exit 5 rather than 2, because a caller on another machine reads 2 as
// version skew and tells its user to update Sidecar.
func TestShellDeleteRefusesAWorktree(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	repo := filepath.Join(root, "repo")
	topic := filepath.Join(root, "feature")
	initGitRepo(t, repo)
	runGit(t, repo, "worktree", "add", "-b", "feature", topic)
	writeProjectMeta(t, stateDir, "demo", repo)
	t.Chdir(repo)

	session := workspaceops.WorktreeSessionName(topic, "")
	var out, errOut bytes.Buffer
	handled, code := Run([]string{"shell", "delete", "--target", session}, &out, &errOut)
	if !handled || code != exitInputRejected {
		t.Fatalf("worktree target = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "worktree") {
		t.Fatalf("stderr = %q, want a refusal that says what it refused", errOut.String())
	}
	// The worktree is untouched: the refusal happens before anything is removed.
	if lines := gitLines(t, repo, "worktree", "list"); len(lines) < 2 {
		t.Fatalf("a refused delete removed a worktree: %v", lines)
	}
}

// TestShellDeleteRefusesARecordOnAnotherTmuxServer is TestShellSendRefuses…'s
// sibling and matters more: a record proved present on one socket says nothing
// about what answers to that session name on another, and here the consequence
// is a killed session rather than a stray keystroke. `shell forget` is the
// record-only operation, and the refusal says so.
func TestShellDeleteRefusesARecordOnAnotherTmuxServer(t *testing.T) {
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
	handled, code := Run([]string{"shell", "delete", "--target", "sidecar-sh-demo-2"}, &out, &errOut)
	if !handled || code != shellTargetUnregistered {
		t.Fatalf("delete across servers = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	for _, want := range []string{elsewhere, tmuxenv.Namespace(), "shell forget"} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("stderr %q does not name %q", errOut.String(), want)
		}
	}
	// Nothing was removed: a refusal that had already edited the manifest would
	// be worse than the failure it prevents.
	live, err := shellstate.ListAtPath(filepath.Join(stateDir, "projects", "demo", "shells.json"))
	if err != nil || len(live) != 1 {
		t.Fatalf("the record was touched by a refusal: %+v %v", live, err)
	}
}

// TestShellDeleteRequiresATarget. There is deliberately no current-shell form:
// deleting the shell you are sitting in kills the session running the command.
func TestShellDeleteRequiresATarget(t *testing.T) {
	stateDir, _ := targetProject(t)

	for _, args := range [][]string{
		{"shell", "delete"},
		{"shell", "delete", "sidecar-sh-demo-2"},
	} {
		var out, errOut bytes.Buffer
		handled, code := Run(args, &out, &errOut)
		if !handled || code != 2 {
			t.Fatalf("Run(%v) = handled %v code %d, want a usage error", args, handled, code)
		}
	}
	// And the records are all still there.
	if names := manifestNames(t, stateDir); len(names) != 2 {
		t.Fatalf("a usage error deleted something: %v", names)
	}
}
