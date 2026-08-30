package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentcontrol"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/workspaceops"
)

type cliAgentTerminal struct {
	launched bool
	argv     []string
	screen   string
	// screens, when set, is consumed one entry per Inspect and its last entry
	// repeats, which is how a CLI test drives a lifecycle transition without
	// reimplementing the service.
	screens   []string
	inspects  int
	submitted []string
	keys      []string
	captured  []agentcontrol.ReadRequest
}

func (t *cliAgentTerminal) Inspect(_ context.Context, target agentcontrol.Target) (agentcontrol.Snapshot, error) {
	target.Host = "local"
	target.Namespace = tmuxenv.Namespace()
	target.PaneID = "%11"
	target.PanePID = 111
	target.ServerPID = 99
	target.ServerIncarnation = "server-fixture"
	t.inspects++
	snapshot := agentcontrol.Snapshot{Target: target, PaneCount: 1, CurrentCommand: "zsh", ProcessIdentity: "shell", ShellReady: true, CapturedAt: time.Unix(1000, int64(t.inspects))}
	if t.launched {
		snapshot.CurrentCommand = "codex"
		snapshot.ProcessIdentity = "codex"
		snapshot.ShellReady = false
		snapshot.Screen = t.screen
		if len(t.screens) > 0 {
			snapshot.Screen = t.screens[min(t.inspects-1, len(t.screens)-1)]
		}
	}
	return snapshot, nil
}

func (t *cliAgentTerminal) Launch(_ context.Context, _ agentcontrol.Snapshot, argv []string) error {
	t.launched = true
	t.argv = append([]string(nil), argv...)
	return nil
}

func (t *cliAgentTerminal) Submit(_ context.Context, _ agentcontrol.Snapshot, text string) error {
	t.submitted = append(t.submitted, text)
	return nil
}

func (t *cliAgentTerminal) SendKeys(_ context.Context, _ agentcontrol.Snapshot, names []string) error {
	if err := agentcontrol.ValidateKeys(names); err != nil {
		return err
	}
	t.keys = append(t.keys, names...)
	return nil
}

func (t *cliAgentTerminal) Capture(_ context.Context, _ agentcontrol.Snapshot, req agentcontrol.ReadRequest) (string, error) {
	t.captured = append(t.captured, req)
	return string(req.Source) + " capture\n", nil
}

func useCLIAgentTerminal(t *testing.T, terminal *cliAgentTerminal) {
	t.Helper()
	previous := newAgentTerminal
	newAgentTerminal = func() agentcontrol.Terminal { return terminal }
	t.Cleanup(func() { newAgentTerminal = previous })
}

func codexIdleFixture(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "agentactivity", "testdata", "codex", "startup_idle.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestAgentHelpIsDiscoverableWhileFeatureDisabled(t *testing.T) {
	setupIsolatedCLI(t)
	var out, errOut bytes.Buffer
	handled, code := Run([]string{"agent", "start", "--help"}, &out, &errOut)
	if !handled || code != 0 || errOut.Len() != 0 || !strings.Contains(out.String(), "sidecar agent start") {
		t.Fatalf("help = handled=%v code=%d stdout=%q stderr=%q", handled, code, out.String(), errOut.String())
	}
}

func TestAgentFeatureGateHonorsConfigAndLeadingOverrides(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	configPath := filepath.Join(filepath.Dir(stateDir), "config", "config.json")
	t.Cleanup(func() { config.SetConfigPath(defaultTestConfigPath) })
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{"features":{"flags":{"agent_control":true}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"-config", configPath, "agent", "list", "--json"}, &out, &errOut)
	if !handled || code != 0 || errOut.Len() != 0 || !strings.Contains(out.String(), `"agents"`) {
		t.Fatalf("config enabled = handled=%v code=%d stdout=%q stderr=%q", handled, code, out.String(), errOut.String())
	}

	for _, args := range [][]string{
		{"--disable-feature=agent_control", "-config", configPath, "agent", "list", "--json"},
		{"-config", configPath, "--disable-feature", "agent_control", "agent", "list", "--json"},
	} {
		out.Reset()
		errOut.Reset()
		handled, code = Run(args, &out, &errOut)
		if !handled || code != 5 || !strings.Contains(errOut.String(), `"code":"feature_disabled"`) {
			t.Fatalf("override disabled for %v = handled=%v code=%d stdout=%q stderr=%q", args, handled, code, out.String(), errOut.String())
		}
	}

	if err := os.WriteFile(configPath, []byte(`{"features":{"flags":{"agent_control":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"--enable-feature=agent_control", "-config", configPath, "agent", "list", "--json"},
		{"-config", configPath, "--enable-feature", "agent_control", "agent", "list", "--json"},
	} {
		out.Reset()
		errOut.Reset()
		handled, code = Run(args, &out, &errOut)
		if !handled || code != 0 || errOut.Len() != 0 || !strings.Contains(out.String(), `"agents"`) {
			t.Fatalf("override enabled for %v = handled=%v code=%d stdout=%q stderr=%q", args, handled, code, out.String(), errOut.String())
		}
	}
}

func TestAgentListGetAndStartUseStableJSONAndPinnedTarget(t *testing.T) {
	idleScreen := codexIdleFixture(t)
	stateDir, _ := targetProject(t)
	terminal := &cliAgentTerminal{launched: true, screen: idleScreen}
	useCLIAgentTerminal(t, terminal)

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"--enable-feature=agent_control", "agent", "list", "--project", "demo", "--json"}, &out, &errOut)
	if !handled || code != 0 || errOut.Len() != 0 {
		t.Fatalf("list = handled=%v code=%d stdout=%q stderr=%q", handled, code, out.String(), errOut.String())
	}
	var listed struct {
		Agents []agentcontrol.Agent `json:"agents"`
	}
	if err := json.Unmarshal(out.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Agents) < 2 || listed.Agents[0].Agent.Kind != "codex" {
		t.Fatalf("agents = %+v", listed.Agents)
	}

	out.Reset()
	errOut.Reset()
	handled, code = Run([]string{"--enable-feature=agent_control", "agent", "get", "sidecar-sh-demo-2", "--json"}, &out, &errOut)
	if !handled || code != 0 || errOut.Len() != 0 {
		t.Fatalf("get = handled=%v code=%d stdout=%q stderr=%q", handled, code, out.String(), errOut.String())
	}
	var got agentcontrol.Agent
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Target.Session != "sidecar-sh-demo-2" || got.Target.PaneID != "%11" || got.Agent.Kind != "codex" {
		t.Fatalf("get = %+v", got)
	}

	terminal.launched = false
	out.Reset()
	errOut.Reset()
	handled, code = Run([]string{"--enable-feature=agent_control", "agent", "start", "sidecar-sh-demo-2", "--kind", "codex", "--timeout", "1s", "--json", "--", "--model", "space value", "--provider-json"}, &out, &errOut)
	if !handled || code != 0 || errOut.Len() != 0 {
		t.Fatalf("start = handled=%v code=%d stdout=%q stderr=%q", handled, code, out.String(), errOut.String())
	}
	var started agentcontrol.Agent
	if err := json.Unmarshal(out.Bytes(), &started); err != nil {
		t.Fatalf("start JSON = %q: %v", out.String(), err)
	}
	if started.Target.PaneID != "%11" || started.Agent.Kind != "codex" || !started.Agent.InteractiveReady {
		t.Fatalf("started = %+v", started)
	}
	wantArgv := []string{"codex", "--model", "space value", "--provider-json"}
	if strings.Join(terminal.argv, "\x00") != strings.Join(wantArgv, "\x00") {
		t.Fatalf("launch argv = %#v, want %#v", terminal.argv, wantArgv)
	}

	// A worktree session is a first-class get target, not a shell-manifest-only
	// special case.
	repoRoot := t.TempDir()
	repo := filepath.Join(repoRoot, "repo")
	worktree := filepath.Join(repoRoot, "repo-topic")
	initGitRepo(t, repo)
	runGit(t, repo, "worktree", "add", "-b", "topic", worktree)
	writeRegisteredWorktree(t, stateDir, repo, worktree)
	terminal.launched = true
	out.Reset()
	errOut.Reset()
	session := workspaceops.WorktreeSessionName(worktree, "")
	handled, code = Run([]string{"--enable-feature=agent_control", "agent", "get", session, "--project", repo, "--json"}, &out, &errOut)
	if !handled || code != 0 || errOut.Len() != 0 {
		t.Fatalf("worktree get = handled=%v code=%d stdout=%q stderr=%q", handled, code, out.String(), errOut.String())
	}
}

func TestAgentJSONErrorEnvelope(t *testing.T) {
	targetProject(t)
	var out, errOut bytes.Buffer
	handled, code := Run([]string{"--enable-feature=agent_control", "agent", "get", "missing", "--json"}, &out, &errOut)
	if !handled || code != 3 || out.Len() != 0 || !strings.Contains(errOut.String(), `"code":"agent_not_found"`) {
		t.Fatalf("missing = handled=%v code=%d stdout=%q stderr=%q", handled, code, out.String(), errOut.String())
	}
}

// TestCreateShellAgentWithARunCommandStartsNothingItself pins the layering the
// one --agent flag rests on.
//
// The flag's floor is the durable record, and that is ungated: the family is
// written into shells.json whether or not agent control is available, because
// that record is what keeps the shell on the Activity board while the agent
// boots. Starting the provider is the layer on top, and it applies only when
// the caller named no command of their own.
//
// A caller that passes --run has said it owns the launch. Starting a provider
// as well would put two agents in one pane — which is exactly what a remote
// create would have done, since the viewer resolves the command itself and
// sends both.
func TestCreateShellAgentWithARunCommandStartsNothingItself(t *testing.T) {
	idleScreen := codexIdleFixture(t)
	stateDir, _ := targetProject(t)
	terminal := &cliAgentTerminal{screen: idleScreen}
	useCLIAgentTerminal(t, terminal)
	var out, errOut bytes.Buffer
	handled, code := Run([]string{"--enable-feature=agent_control", "create", "shell", "--tab", "--name", "seeded", "--agent", "codex", "--run", "codex --search", "--json"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("create = handled=%v code=%d stderr=%q", handled, code, errOut.String())
	}
	var result createShellResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("json: %v (%q)", err, out.String())
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", result.Shell.Session).Run() })
	if terminal.launched {
		t.Fatalf("agent control launched %v behind the caller's own --run", terminal.argv)
	}
	listed, err := shellstate.ListAtPath(filepath.Join(stateDir, "projects", "demo", "shells.json"))
	if err != nil {
		t.Fatal(err)
	}
	var record *shellstate.Definition
	for i := range listed {
		if listed[i].TmuxName == result.Shell.Session {
			record = &listed[i]
		}
	}
	if record == nil || record.AgentType != "codex" {
		t.Fatalf("manifest = %+v, want the created shell recorded as codex", listed)
	}
}

func TestCreateShellAgentRoutesThroughReadyService(t *testing.T) {
	idleScreen := codexIdleFixture(t)
	targetProject(t)
	terminal := &cliAgentTerminal{screen: idleScreen}
	useCLIAgentTerminal(t, terminal)
	var out, errOut bytes.Buffer
	handled, code := Run([]string{"--enable-feature=agent_control", "create", "shell", "--tab", "--name", "reviewer", "--agent", "codex", "--json"}, &out, &errOut)
	if !handled || code != 0 || errOut.Len() != 0 {
		t.Fatalf("create shell agent = handled=%v code=%d stdout=%q stderr=%q", handled, code, out.String(), errOut.String())
	}
	if !terminal.launched || len(terminal.argv) == 0 || terminal.argv[0] != "codex" {
		t.Fatalf("launch = launched=%v argv=%#v", terminal.launched, terminal.argv)
	}
	var result createShellResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Shell.DisplayName != "reviewer" || result.Shell.Session == "" {
		t.Fatalf("result = %+v", result)
	}
}
