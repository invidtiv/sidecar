package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/marcus/sidecar/internal/agentcontrol"
	"github.com/marcus/sidecar/internal/agentremote"
	"github.com/marcus/sidecar/internal/hosts"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/testenv"
	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/uiapi"
)

func TestVanishedSessionNeverMutatesDisplayNameCollision(t *testing.T) {
	for _, missing := range []string{"sidecar-sh-demo-deleted", "deleted-session", "name:deleted", "session:deleted", "deleted-session "} {
		for _, operation := range []string{"shells/rename", "shells/delete", "shells/restore", "agents/prompt", "agents/start"} {
			t.Run(missing+"/"+operation, func(t *testing.T) {
				idle, working := codexIdleFixture(t), agentFixture(t, "working.txt")
				stateDir, workDir := targetProject(t)
				path := filepath.Join(stateDir, "projects", "demo", "shells.json")
				writeProjectShells(t, stateDir, "demo", shellstate.Definition{TmuxName: "sidecar-sh-demo-2", DisplayName: missing, WorkDir: workDir, Namespace: tmuxenv.Namespace()})
				terminal := &cliAgentTerminal{launched: true, screens: []string{idle, working}}
				useCLIAgentTerminal(t, terminal)
				b := &mobileBackend{env: Env{StateDir: stateDir, FeatureOverrides: map[string]bool{"agent_control": true}}}
				c := uiapi.WorkspaceCommand{Operation: operation, Target: missing, Name: "Wrong", Text: "Wrong", Kind: "codex"}
				_, exit, err := b.WorkspaceOperation(context.Background(), "demo", c)
				if exit == 0 || err == nil {
					t.Fatalf("vanished session accepted: exit=%d err=%v", exit, err)
				}
				rows, err := shellstate.ListAtPath(path)
				if err != nil || len(rows) != 1 || rows[0].DisplayName != missing || rows[0].TmuxName != "sidecar-sh-demo-2" {
					t.Fatalf("collision changed: %+v %v", rows, err)
				}
				if terminal.inspects != 0 || len(terminal.submitted) != 0 || len(terminal.argv) != 0 {
					t.Fatalf("collision reached terminal: %+v", terminal)
				}
			})
		}
	}
}

func TestWorkspaceLeadingDashTargetsAreProtected(t *testing.T) {
	testenv.ProviderHelp(t, "codex", "usage: codex (older standalone CLI)")
	for _, operation := range []string{"shells/restore", "agents/start"} {
		args, _, err := workspaceCommandArgs("demo", uiapi.WorkspaceCommand{Operation: operation, Target: "-session", Kind: "codex", Args: []string{"--model", "test"}})
		if err != nil {
			t.Fatal(err)
		}
		// Restore uses the terminator; start uses an explicit target value and
		// keeps the terminator for provider argv. Exercise both parsers below.
		if !strings.Contains(strings.Join(args, " "), "-- -session") && !strings.Contains(strings.Join(args, " "), "--target -session") {
			t.Fatalf("%s unprotected target: %v", operation, args)
		}
	}
	idle := codexIdleFixture(t)
	stateDir, workDir := targetProject(t)
	writeProjectShells(t, stateDir, "demo", shellstate.Definition{TmuxName: "-session", DisplayName: "Dash", Namespace: tmuxenv.Namespace(), WorkDir: workDir})
	b := &mobileBackend{env: Env{StateDir: stateDir, FeatureOverrides: map[string]bool{"agent_control": true}}}
	terminal := &cliAgentTerminal{screen: idle}
	useCLIAgentTerminal(t, terminal)
	for _, operation := range []string{"shells/restore", "agents/start"} {
		result, exit, err := b.WorkspaceOperation(context.Background(), "demo", uiapi.WorkspaceCommand{Operation: operation, Target: "-session", Kind: "codex"})
		if err != nil || exit != 0 {
			t.Fatalf("%s exit=%d err=%v result=%s", operation, exit, err, result)
		}
	}
}

func TestWorkspacePromptDeliversSingleDashLiterally(t *testing.T) {
	idle, working := codexIdleFixture(t), agentFixture(t, "working.txt")
	stateDir, _ := targetProject(t)
	terminal := &cliAgentTerminal{launched: true, screens: []string{idle, working}}
	useCLIAgentTerminal(t, terminal)
	b := &mobileBackend{env: Env{StateDir: stateDir, FeatureOverrides: map[string]bool{"agent_control": true}}}
	_, exit, err := b.WorkspaceOperation(context.Background(), "demo", uiapi.WorkspaceCommand{Operation: "agents/prompt", Target: "sidecar-sh-demo-2", Text: "-"})
	if exit != 0 || err != nil || !reflect.DeepEqual(terminal.submitted, []string{"-"}) {
		t.Fatalf("exit=%d err=%v submitted=%v", exit, err, terminal.submitted)
	}
}

func TestProjectJSONPathCanonicalizesSymlink(t *testing.T) {
	root := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	item := makeProjectJSONItem(t.TempDir(), config.ProjectConfig{Name: "Demo", Path: alias})
	data, _ := json.Marshal(item)
	if item.Path != real {
		t.Fatalf("project path not canonical: %s; want %s", data, real)
	}
}

func TestCLIExactSessionRefusesCollisionAcrossMutatingVerbs(t *testing.T) {
	for _, args := range [][]string{
		{"shell", "rename", "--target", "deleted-session", "Wrong"},
		{"shell", "delete", "--target", "deleted-session"},
		{"shell", "restore", "deleted-session"},
		{"shell", "send", "--target", "deleted-session", "--type", "Wrong"},
		{"agent", "prompt", "deleted-session", "Wrong"},
		{"agent", "start", "deleted-session", "--kind", "codex"},
	} {
		t.Run(strings.Join(args[:2], "/"), func(t *testing.T) {
			idle := codexIdleFixture(t)
			stateDir, workDir := targetProject(t)
			writeProjectShells(t, stateDir, "demo", shellstate.Definition{TmuxName: "live", DisplayName: "deleted-session", WorkDir: workDir, Namespace: tmuxenv.Namespace()})
			terminal := &cliAgentTerminal{launched: true, screen: idle}
			useCLIAgentTerminal(t, terminal)
			args = append(args, "--project", "demo", "--json")
			var out, stderr bytes.Buffer
			handled, exit := Run(append([]string{"-enable-feature", "agent_control"}, args...), &out, &stderr)
			if !handled || exit == 0 {
				t.Fatalf("stale target accepted: %d %s %s", exit, out.String(), stderr.String())
			}
			if names := manifestNames(t, stateDir); names["live"] != "deleted-session" || len(names) != 1 {
				t.Fatalf("changed collision: %v", names)
			}
			if terminal.inspects != 0 || len(terminal.submitted) != 0 {
				t.Fatalf("reached collision: %+v", terminal)
			}
		})
	}
}

func TestAgentStartKeepsCurrentShellProviderArguments(t *testing.T) {
	testenv.ProviderHelp(t, "codex", "usage: codex (older standalone CLI)")
	idle := codexIdleFixture(t)
	targetProject(t)
	t.Setenv(shellstate.SessionEnv, "sidecar-sh-demo-1")
	terminal := &cliAgentTerminal{screen: idle}
	useCLIAgentTerminal(t, terminal)
	code, out, stderr := runAgentCLI(t, "agent", "start", "--project", "demo", "--kind", "codex", "--json", "--", "--model", "test")
	if code != 0 || !reflect.DeepEqual(terminal.argv, []string{"codex", "--model", "test"}) {
		t.Fatalf("%d %s %s argv=%v", code, out, stderr, terminal.argv)
	}
}

func TestRemoteAgentStartPreservesLiteralIdentity(t *testing.T) {
	for _, session := range []string{"foo", "name:foo", "session:foo", "foo "} {
		t.Run(session, func(t *testing.T) {
			testenv.ProviderHelp(t, "codex", "usage: codex (older standalone CLI)")
			idle := codexIdleFixture(t)
			stateDir, root := targetProject(t)
			writeProjectShells(t, stateDir, "demo",
				shellstate.Definition{TmuxName: session, DisplayName: "Right", WorkDir: root, Namespace: tmuxenv.Namespace()},
				shellstate.Definition{TmuxName: "session:" + session, DisplayName: "Wrong", WorkDir: root, Namespace: tmuxenv.Namespace()})
			terminal := &cliAgentTerminal{screen: idle}
			useCLIAgentTerminal(t, terminal)
			previous := newRemoteRunner
			t.Cleanup(func() { newRemoteRunner = previous })
			newRemoteRunner = func(_ Env, _ string) (agentremote.Runner, error) {
				return func(ctx context.Context, _ string, args []string, out any) error {
					if !slices.Contains(args, "--exact-target") {
						t.Fatalf("owner lacked exact guard: %v", args)
					}
					result, exit, err := workspaceInvoke(Env{StateDir: stateDir, Ctx: ctx, FeatureOverrides: map[string]bool{"agent_control": true}}, runAgentStart, args[2:])
					if err != nil || exit != 0 {
						return fmt.Errorf("owner exit=%d err=%v result=%s", exit, err, result)
					}
					return json.Unmarshal(result, out)
				}, nil
			}
			code, out, stderr := runAgentCLI(t, "agent", "start", "--host", "owner", "--project", "demo", "--target", session, "--kind", "codex", "--json")
			var got agentcontrol.Agent
			if code != 0 || json.Unmarshal([]byte(out), &got) != nil || got.Target.Session != session {
				t.Fatalf("%d %s %s want session=%q", code, out, stderr, session)
			}
		})
	}
}

func TestRemotePlainSessionTargetsRequireExactOwnerSupport(t *testing.T) {
	setupIsolatedCLI(t)
	previous := newRemoteRunner
	t.Cleanup(func() { newRemoteRunner = previous })
	called := false
	newRemoteRunner = func(_ Env, _ string) (agentremote.Runner, error) {
		return func(_ context.Context, _ string, args []string, _ any) error {
			called = true
			if !slices.Contains(args, "--exact-target") || args[len(args)-2] != "deleted-session" {
				t.Fatalf("unsafe owner argv: %v", args)
			}
			return &hosts.RunError{ExitCode: 2, Stderr: "unknown option --exact-target"}
		}, nil
	}
	code, out, stderr := runAgentCLI(t, "agent", "prompt", "--host", "owner", "--project", "demo", "--json", "--", "deleted-session", "-")
	if !called || code == 0 {
		t.Fatalf("older owner must refuse: called=%v code=%d %s %s", called, code, out, stderr)
	}
}

func TestAPISessionWhitespaceNeverSelectsTrimmedIdentity(t *testing.T) {
	stateDir, root := targetProject(t)
	writeProjectShells(t, stateDir, "demo", shellstate.Definition{TmuxName: "live", DisplayName: "Live", WorkDir: root, Namespace: tmuxenv.Namespace()})
	b := &mobileBackend{env: Env{StateDir: stateDir}}
	_, exit, err := b.WorkspaceOperation(context.Background(), "demo", uiapi.WorkspaceCommand{Operation: "shells/rename", Target: "live ", Name: "Wrong"})
	if exit != 3 || err == nil {
		t.Fatalf("trimmed a literal target: exit=%d err=%v", exit, err)
	}
	if got := manifestNames(t, stateDir); got["live"] != "Live" {
		t.Fatalf("changed trimmed target: %v", got)
	}
}
