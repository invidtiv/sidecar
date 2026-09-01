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
)

// createRepoProject registers one git repository as project "demo" and leaves
// the process inside it, so `create worktree` needs no --project.
func createRepoProject(t *testing.T) (stateDir, repo string) {
	t.Helper()
	_, stateDir = setupIsolatedCLI(t)
	repo = t.TempDir()
	if resolved, err := filepath.EvalSymlinks(repo); err == nil {
		repo = resolved
	}
	initGitRepo(t, repo)
	t.Chdir(repo)
	writeProjectMeta(t, stateDir, "demo", repo)
	return stateDir, repo
}

func worktreeAgentRecord(t *testing.T, stateDir, repo, worktreePath string) string {
	t.Helper()
	dir, err := projectdir.WorktreeDirWithBase(stateDir, repo, worktreePath)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "agent"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// TestCreateWorktreeStartsTheFamilyWithProviderArguments is td-a658ed: the
// only way to launch a catalog family with `--model fable` was --run, which
// recorded no family. `--agent claude -- --model fable` now appends the
// arguments to the catalog command, the way `agent start -- ARGS` does, and
// the family is on the worktree's record.
func TestCreateWorktreeStartsTheFamilyWithProviderArguments(t *testing.T) {
	idleScreen := codexIdleFixture(t)
	stateDir, repo := createRepoProject(t)
	terminal := &cliAgentTerminal{screen: idleScreen}
	useCLIAgentTerminal(t, terminal)

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"--enable-feature=agent_control", "create", "worktree", "orchestrate", "--agent", "codex", "--json", "--wait", "0", "--", "--model", "space value"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("create = handled=%v code=%d stdout=%q stderr=%q", handled, code, out.String(), errOut.String())
	}
	var result createWorktreeResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("json: %v (%q)", err, out.String())
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", result.Shell.Session).Run() })
	want := []string{"codex", "--model", "space value"}
	if !terminal.launched || strings.Join(terminal.argv, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("launch = launched=%v argv=%#v, want %#v", terminal.launched, terminal.argv, want)
	}
	if result.Project != "demo" {
		t.Fatalf("result.project = %q, want the selector --project accepts", result.Project)
	}
	if got := worktreeAgentRecord(t, stateDir, repo, result.Path); got != "codex" {
		t.Fatalf("worktree agent record = %q, want codex", got)
	}
}

// TestCreateWorktreeAgentWithRunRecordsTheFamily: --agent with --run is the
// layering `create shell` already has. The family is recorded and the caller's
// command owns the launch, so nothing else is started into the pane.
func TestCreateWorktreeAgentWithRunRecordsTheFamily(t *testing.T) {
	idleScreen := codexIdleFixture(t)
	stateDir, repo := createRepoProject(t)
	terminal := &cliAgentTerminal{screen: idleScreen}
	useCLIAgentTerminal(t, terminal)

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"create", "worktree", "seeded", "--agent", "codex", "--run", "true", "--json", "--wait", "0"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("create = handled=%v code=%d stdout=%q stderr=%q", handled, code, out.String(), errOut.String())
	}
	var result createWorktreeResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("json: %v (%q)", err, out.String())
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", result.Shell.Session).Run() })
	if terminal.launched {
		t.Fatalf("agent control launched %v behind the caller's own --run", terminal.argv)
	}
	if got := worktreeAgentRecord(t, stateDir, repo, result.Path); got != "codex" {
		t.Fatalf("worktree agent record = %q, want codex", got)
	}
}

// TestCreateUsageRefusalsAreJSONEnvelopes is td-a658ed's secondary: a usage
// refusal under --json used to be a prose line followed by the full help text,
// so a caller doing `| tail` saw help and never the reason.
func TestCreateUsageRefusalsAreJSONEnvelopes(t *testing.T) {
	setupIsolatedCLI(t)
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"create", "worktree", "x", "--no-launch", "--agent", "claude", "--json"}, "--no-launch cannot be combined"},
		{[]string{"create", "worktree", "x", "--json", "--", "--model", "fable"}, "require --agent"},
		{[]string{"create", "worktree", "--agent", "claude", "--json", "--", "--model", "fable"}, "name must come before --"},
		{[]string{"create", "worktree", "--agent", "claude", "--json", "--", "-fix", "--model", "fable"}, "name must come before --"},
		{[]string{"create", "worktree", "x", "--agent", "claude", "--run", "claude", "--json", "--", "--model", "fable"}, "--run command instead"},
		{[]string{"create", "worktree", "x", "--agent", "claude", "--plan", "--json", "--", "--model", "fable"}, "--plan cannot be combined"},
		{[]string{"create", "worktree", "--json", "--bogus", "x"}, "unknown option"},
		{[]string{"create", "worktree", "--json"}, "exactly one name"},
		{[]string{"create", "shell", "--json", "--", "--model", "fable"}, "require --agent"},
		{[]string{"create", "shell", "--agent", "claude", "--run", "claude", "--json", "--", "--model", "fable"}, "--run or --type command instead"},
	}
	for _, tc := range cases {
		var out, errOut bytes.Buffer
		handled, code := Run(tc.args, &out, &errOut)
		if !handled || code != 2 || out.Len() != 0 {
			t.Fatalf("%v = handled=%v code=%d stdout=%q stderr=%q", tc.args, handled, code, out.String(), errOut.String())
		}
		var envelope struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(errOut.Bytes(), &envelope); err != nil {
			t.Fatalf("%v stderr = %q, want one JSON error envelope: %v", tc.args, errOut.String(), err)
		}
		if envelope.Error.Code != "usage" || !strings.Contains(envelope.Error.Message, tc.want) {
			t.Fatalf("%v envelope = %+v, want code usage mentioning %q", tc.args, envelope.Error, tc.want)
		}
	}
	// Without --json the reason and the help are still what a human reads.
	var out, errOut bytes.Buffer
	handled, code := Run([]string{"create", "worktree", "x", "--no-launch", "--agent", "claude"}, &out, &errOut)
	if !handled || code != 2 || !strings.Contains(errOut.String(), "--no-launch cannot be combined") || !strings.Contains(errOut.String(), "Usage:") {
		t.Fatalf("prose refusal = handled=%v code=%d stderr=%q", handled, code, errOut.String())
	}
}

// TestCreateShellStartsTheFamilyWithProviderArguments: the same `--` vocabulary
// on create shell, and the same refusal when nothing here would perform the
// launch the arguments describe.
func TestCreateShellStartsTheFamilyWithProviderArguments(t *testing.T) {
	idleScreen := codexIdleFixture(t)
	targetProject(t)
	terminal := &cliAgentTerminal{screen: idleScreen}
	useCLIAgentTerminal(t, terminal)

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"--enable-feature=agent_control", "create", "shell", "--tab", "--name", "orchestrator", "--agent", "codex", "--json", "--wait", "0", "--", "--model", "space value"}, &out, &errOut)
	if !handled || code != 0 || errOut.Len() != 0 {
		t.Fatalf("create = handled=%v code=%d stdout=%q stderr=%q", handled, code, out.String(), errOut.String())
	}
	var result createShellResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("json: %v (%q)", err, out.String())
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", result.Shell.Session).Run() })
	want := []string{"codex", "--model", "space value"}
	if !terminal.launched || strings.Join(terminal.argv, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("launch = launched=%v argv=%#v, want %#v", terminal.launched, terminal.argv, want)
	}
	if result.Project != "demo" {
		t.Fatalf("result.project = %q", result.Project)
	}

	// With agent control off, --agent records and starts nothing — so the
	// arguments would be dropped. Refused, with the feature named.
	out.Reset()
	errOut.Reset()
	handled, code = Run([]string{"--disable-feature=agent_control", "create", "shell", "--tab", "--agent", "codex", "--json", "--wait", "0", "--", "--model", "fable"}, &out, &errOut)
	if !handled || code != 5 || !strings.Contains(errOut.String(), `"code":"feature_disabled"`) {
		t.Fatalf("feature off = handled=%v code=%d stdout=%q stderr=%q", handled, code, out.String(), errOut.String())
	}
}
