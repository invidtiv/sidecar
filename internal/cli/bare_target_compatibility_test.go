package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/agentcontrol"
	"github.com/marcus/sidecar/internal/agentremote"
	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/sessionrestore"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/testenv"
	"github.com/marcus/sidecar/internal/tmuxenv"
)

func TestBareDisplayNameCLICompatibility(t *testing.T) {
	for _, name := range []string{"rev U3-c", "Shell 3"} {
		t.Run(name, func(t *testing.T) {
			idle := codexIdleFixture(t)
			stateDir, root := targetProject(t)
			writeProjectShells(t, stateDir, "demo", shellstate.Definition{TmuxName: "sidecar-sh-demo-3", DisplayName: name, Namespace: tmuxenv.Namespace(), WorkDir: root})
			terminal := &cliAgentTerminal{launched: true, screen: idle}
			useCLIAgentTerminal(t, terminal)
			code, out, stderr := runAgentCLI(t, "agent", "get", name, "--project", "demo", "--json")
			var got agentcontrol.Agent
			if code != 0 || json.Unmarshal([]byte(out), &got) != nil || got.Target.Session != "sidecar-sh-demo-3" {
				t.Fatalf("bare get: %d %s %s", code, out, stderr)
			}
			code, out, stderr = runAgentCLI(t, "shell", "rename", "--target", name, "--project", "demo", "--json", "--", "Renamed")
			if code != 0 || manifestNames(t, stateDir)["sidecar-sh-demo-3"] != "Renamed" {
				t.Fatalf("bare rename: %d %s %s", code, out, stderr)
			}
		})
	}
}

func TestRemoteRestoreSelectorsAndOldOwnerRefusal(t *testing.T) {
	for _, selector := range []string{"Shell 3", "name:Shell 3", "session:sidecar-sh-harness-3", "sidecar-sh-harness-3"} {
		t.Run(selector, func(t *testing.T) {
			_, stateDir := setupIsolatedCLI(t)
			def := restorableShell("sidecar-sh-harness-3")
			def.DisplayName = "Shell 3"
			seedRestoreManifest(t, stateDir, def)
			previous := newRemoteRunner
			t.Cleanup(func() { newRemoteRunner = previous })
			oldOwner := false
			newRemoteRunner = func(_ Env, _ string) (agentremote.Runner, error) {
				return func(ctx context.Context, _ string, args []string, out any) error {
					guard := "--exact-shell"
					if selector == "Shell 3" || selector == "name:Shell 3" {
						guard = "--exact-shell=false"
					}
					if !slices.Contains(args, guard) {
						t.Fatalf("missing owner parser guard %s: %v", guard, args)
					}
					if oldOwner {
						return &hosts.RunError{ExitCode: 2, Stderr: "unknown option " + guard, Detail: "unknown option " + guard}
					}
					result, exit, err := workspaceInvoke(Env{StateDir: stateDir, Ctx: ctx}, runSessionRestore, args[2:])
					if exit != 0 || err != nil {
						return fmt.Errorf("owner exit=%d err=%v result=%s", exit, err, result)
					}
					return json.Unmarshal(result, out)
				}, nil
			}
			code, out, stderr := runAgentCLI(t, "session", "restore", "--host", "owner", "--shell", selector, "--dry-run", "--json")
			var doc struct {
				Steps []sessionrestore.Step `json:"steps"`
			}
			if code != 0 || json.Unmarshal([]byte(out), &doc) != nil || len(doc.Steps) != 1 || doc.Steps[0].Action != sessionrestore.ActionRecreateShell {
				t.Fatalf("remote restore: %d %s %s", code, out, stderr)
			}
			oldOwner = true
			code, out, stderr = runAgentCLI(t, "session", "restore", "--host", "owner", "--shell", selector, "--dry-run", "--json")
			if code == 0 || !strings.Contains(stderr, "unknown option") {
				t.Fatalf("old owner must refuse: %d %s %s", code, out, stderr)
			}
		})
	}
}

func TestBareDisplayNameAmbiguityCannotUseImplicitProject(t *testing.T) {
	stateDir, root := targetProject(t)
	other := t.TempDir()
	writeProjectMeta(t, stateDir, "other", other)
	for project, workDir := range map[string]string{"demo": root, "other": other} {
		writeProjectShells(t, stateDir, project, shellstate.Definition{TmuxName: "sidecar-sh-" + project + "-3", DisplayName: "Shell 3", Namespace: tmuxenv.Namespace(), WorkDir: workDir})
	}
	t.Setenv(shellstate.SessionEnv, "sidecar-sh-demo-3")
	_, code, err := findShellTarget(Env{StateDir: stateDir}, "Shell 3", "", "", true, tmuxenv.Namespace())
	if err == nil || code != 1 || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("implicit project hid ambiguity: code=%d err=%v", code, err)
	}
}

func TestRemoteBareDisplayNameCompatibility(t *testing.T) {
	for _, verb := range []string{"get", "start", "prompt"} {
		t.Run(verb, func(t *testing.T) {
			testenv.ProviderHelp(t, "codex", "usage: codex (older standalone CLI)")
			idle, working := codexIdleFixture(t), agentFixture(t, "working.txt")
			stateDir, root := targetProject(t)
			writeProjectShells(t, stateDir, "demo", shellstate.Definition{TmuxName: "sidecar-sh-demo-3", DisplayName: "rev U3-c", Namespace: tmuxenv.Namespace(), WorkDir: root})
			terminal := &cliAgentTerminal{screen: idle, launched: verb != "start"}
			if verb == "prompt" {
				terminal.screens = []string{idle, working}
			}
			useCLIAgentTerminal(t, terminal)
			previous := newRemoteRunner
			t.Cleanup(func() { newRemoteRunner = previous })
			newRemoteRunner = func(_ Env, _ string) (agentremote.Runner, error) {
				return func(ctx context.Context, _ string, args []string, out any) error {
					if !slices.Contains(args, "--exact-target=false") || slices.Contains(args, "--target") {
						t.Fatalf("bare display name forced exact: %v", args)
					}
					run := runAgentGet
					switch verb {
					case "start":
						run = runAgentStart
					case "prompt":
						run = runAgentPrompt
					}
					result, exit, err := workspaceInvoke(Env{StateDir: stateDir, Ctx: ctx, FeatureOverrides: map[string]bool{"agent_control": true}}, run, args[2:])
					if err != nil || exit != 0 {
						return fmt.Errorf("owner exit=%d err=%v result=%s", exit, err, result)
					}
					return json.Unmarshal(result, out)
				}, nil
			}
			args := []string{"agent", verb, "rev U3-c", "--host", "owner", "--project", "demo", "--json"}
			switch verb {
			case "start":
				args = append(args, "--kind", "codex", "--", "--model", "test")
			case "prompt":
				args = append(args, "--", "-")
			}
			code, out, stderr := runAgentCLI(t, args...)
			var got agentcontrol.Agent
			if code != 0 || json.Unmarshal([]byte(out), &got) != nil || got.Target.Session != "sidecar-sh-demo-3" {
				t.Fatalf("remote %s: %d %s %s", verb, code, out, stderr)
			}
			if verb == "start" && !slices.Equal(terminal.argv, []string{"codex", "--model", "test"}) {
				t.Fatalf("provider arguments: %v", terminal.argv)
			}
		})
	}
}
