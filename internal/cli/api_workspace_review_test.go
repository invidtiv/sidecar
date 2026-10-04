package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/agentremote"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/mobileproto"
	"github.com/marcus/sidecar/internal/uiapi"
	"github.com/marcus/sidecar/internal/workspacewire"
)

func TestWorkspaceCLIIdlePreferenceReachesOwner(t *testing.T) {
	setupIsolatedCLI(t)
	previous := newRemoteRunner
	t.Cleanup(func() { newRemoteRunner = previous })
	newRemoteRunner = func(_ Env, _ string) (agentremote.Runner, error) {
		return func(_ context.Context, _ string, args []string, out any) error {
			if !strings.Contains(strings.Join(args, " "), "--show-idle-sessions false") {
				t.Fatalf("owner args: %v", args)
			}
			return json.Unmarshal([]byte(`{"project":{"key":"proof"},"catalog":{"hub_id":"owner"},"shells":[]}`), out)
		}, nil
	}
	var out, stderr bytes.Buffer
	exit := runWorkspaceList(Env{Stdout: &out, Stderr: &stderr}, []string{"--project", "proof", "--host", "owner", "--json", "--show-idle-sessions", "false"})
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}
}

func TestWorkspaceRemoteInvalidSuccessIsFailure(t *testing.T) {
	setupIsolatedCLI(t)
	previous := newRemoteRunner
	t.Cleanup(func() { newRemoteRunner = previous })
	newRemoteRunner = func(_ Env, _ string) (agentremote.Runner, error) {
		return func(context.Context, string, []string, any) error {
			return &hosts.RunError{Failure: hosts.FailNotResult, ExitCode: 0, Detail: "no valid JSON result"}
		}, nil
	}
	b := &mobileBackend{}
	_, exit, err := b.WorkspaceOperation(context.Background(), "proof", uiapi.WorkspaceCommand{Operation: "shells/delete", Host: "owner", Target: "managed"})
	if err == nil || exit != 0 {
		t.Fatalf("exit=%d error=%v", exit, err)
	}
}

func TestWorkspaceStaleDeletePlanPreservesNewWork(t *testing.T) {
	for _, scenario := range []string{"new file", "changed dirty file", "new ignored file", "recreated checkout"} {
		t.Run(scenario, func(t *testing.T) {
			stateHome, stateDir := setupIsolatedCLI(t)
			t.Setenv("TMUX_TMPDIR", stateHome)
			root := filepath.Join(t.TempDir(), "repo")
			initGitRepoOnMain(t, root)
			root = canonicalTestPath(t, root)
			writeProjectMeta(t, stateDir, "demo", root)
			path := filepath.Join(filepath.Dir(root), "topic")
			runGit(t, root, "worktree", "add", "-q", "-b", "topic", path)
			if scenario == "changed dirty file" {
				if err := os.WriteFile(filepath.Join(path, "new-work.txt"), []byte("old work"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "new ignored file" {
				if err := os.WriteFile(filepath.Join(path, ".gitignore"), []byte("new-work.txt\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			b := &mobileBackend{env: Env{StateDir: stateDir}}
			data, exit, err := b.WorkspaceOperation(context.Background(), "demo", uiapi.WorkspaceCommand{Operation: "worktrees/delete-plan", Target: path})
			if err != nil || exit != 0 {
				t.Fatalf("plan: %s %d %v", data, exit, err)
			}
			var planned workspacewire.WorktreeDeleted
			if err := json.Unmarshal(data, &planned); err != nil {
				t.Fatal(err)
			}
			if planned.Plan.DeleteState == "" {
				t.Fatalf("plan has no delete state: %s", data)
			}
			if scenario == "recreated checkout" {
				runGit(t, root, "worktree", "remove", path)
				runGit(t, root, "worktree", "add", "-q", path, "topic")
			}
			if scenario != "recreated checkout" {
				if err := os.WriteFile(filepath.Join(path, "new-work.txt"), []byte("unsaved work"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			// Decode the additive state field without depending on its implementation.
			var fields struct {
				Plan struct {
					DeleteState string `json:"deleteState"`
				}
			}
			_ = json.Unmarshal(data, &fields)
			commandData, _ := json.Marshal(map[string]any{"target": path, "confirm": true, "expect_head_oid": planned.Plan.HeadOID, "expect_branch": planned.Plan.Branch, "expect_delete_state": fields.Plan.DeleteState})
			var command uiapi.WorkspaceCommand
			_ = json.Unmarshal(commandData, &command)
			command.Operation = "worktrees/delete"
			data, exit, err = b.WorkspaceOperation(context.Background(), "demo", command)
			if err == nil || exit == 0 {
				t.Fatalf("stale plan deleted new work: %s %d %v", data, exit, err)
			}
			preserved := path
			if scenario != "recreated checkout" {
				preserved = filepath.Join(path, "new-work.txt")
			}
			if _, err := os.Stat(preserved); err != nil {
				t.Fatalf("new work lost: %v", err)
			}
		})
	}
}

func TestRemotePromptLiteralSurvivesOwnerParser(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	args := (agentremote.Client{Project: "proof"}).PromptArgs("managed", "--help", false, nil, 0)
	var out, stderr bytes.Buffer
	exit := runAgentPrompt(Env{StateDir: stateDir, Stdout: &out, Stderr: &stderr, FeatureOverrides: map[string]bool{"agent_control": false}}, args[2:])
	if exit != 5 || !strings.Contains(stderr.String(), `"not_submitted"`) || out.Len() != 0 {
		t.Fatalf("args=%v exit=%d stdout=%s stderr=%s", args, exit, out.String(), stderr.String())
	}
}

func TestWorkspaceRemotePartialCreationReceipt(t *testing.T) {
	setupIsolatedCLI(t)
	previous := newRemoteRunner
	t.Cleanup(func() { newRemoteRunner = previous })
	newRemoteRunner = func(_ Env, _ string) (agentremote.Runner, error) {
		return func(_ context.Context, _ string, _ []string, out any) error {
			if err := json.Unmarshal([]byte(`{"path":"/owner/created","branch":"topic","setup":[]}`), out); err != nil {
				return err
			}
			return &hosts.RunError{Failure: hosts.FailRefused, ExitCode: 1, Stderr: "setup failed"}
		}, nil
	}
	b := &mobileBackend{}
	data, exit, err := b.WorkspaceOperation(context.Background(), "proof", uiapi.WorkspaceCommand{Operation: "worktrees/create", Host: "owner", Name: "topic", Confirm: true, ExpectSourceOID: "abc"})
	if err == nil || exit != 1 || !strings.Contains(string(data), "/owner/created") {
		t.Fatalf("receipt=%s exit=%d error=%v", data, exit, err)
	}
}

func TestWorkspaceRemotePromptTransportKeepsUnknownReceipt(t *testing.T) {
	setupIsolatedCLI(t)
	previous := newRemoteRunner
	t.Cleanup(func() { newRemoteRunner = previous })
	newRemoteRunner = func(_ Env, _ string) (agentremote.Runner, error) {
		return func(context.Context, string, []string, any) error {
			return &hosts.RunError{Failure: hosts.FailTimeout, ExitCode: -1, Detail: "connection lost after possible submission"}
		}, nil
	}
	b := &mobileBackend{}
	data, exit, err := b.WorkspaceOperation(context.Background(), "proof", uiapi.WorkspaceCommand{Operation: "agents/prompt", Host: "owner", Target: "managed", Text: "--help"})
	if err == nil || exit == 0 || !strings.Contains(string(data), `"submission":"unknown"`) {
		t.Fatalf("receipt=%s exit=%d error=%v", data, exit, err)
	}
}

func TestWorkspaceRemovedConfiguredProjectRefusesRead(t *testing.T) {
	stateHome, stateDir := setupIsolatedCLI(t)
	t.Setenv("TMUX_TMPDIR", stateHome)
	project := filepath.Join(t.TempDir(), "repo")
	initGitRepoOnMain(t, project)
	if err := os.MkdirAll(filepath.Dir(config.ConfigPath()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.ConfigPath(), []byte(`{"projects":{"list":[]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	writeProjectMeta(t, stateDir, "removed", project)
	b := &mobileBackend{env: Env{StateDir: stateDir}}
	result, err := b.Workspace(context.Background(), "removed", "", mobileproto.CatalogQuery{})
	var refusal *uiapi.OperationError
	if !errors.As(err, &refusal) || refusal.ExitCode != 3 {
		t.Fatalf("removed project read must return not-found: result=%+v error=%v", result, err)
	}
}

func TestWorkspaceRemotePromptLiteralAndOwnerReceipt(t *testing.T) {
	setupIsolatedCLI(t)
	previous := newRemoteRunner
	t.Cleanup(func() { newRemoteRunner = previous })
	text := "--host=other; $(touch /tmp/forbidden)\n--help"
	newRemoteRunner = func(_ Env, host string) (agentremote.Runner, error) {
		if host != "owner" {
			t.Fatalf("host=%q", host)
		}
		return func(_ context.Context, h string, args []string, out any) error {
			want := []string{"agent", "prompt", "--json", "--exact-target", "--project", "proof", "--", "managed", text}
			if h != "owner" || !reflect.DeepEqual(args, want) {
				t.Fatalf("owner=%q args=%v", h, args)
			}
			return json.Unmarshal([]byte(`{"target":{"host":"local","project":"proof","session":"managed"},"agent":{"kind":"codex"},"receipt":{"submission":"submitted","wait":"not_requested","target":{"host":"local","project":"proof","session":"managed"}}}`), out)
		}, nil
	}
	b := &mobileBackend{}
	data, exit, err := b.WorkspaceOperation(context.Background(), "proof", uiapi.WorkspaceCommand{Operation: "agents/prompt", Host: "owner", Target: "managed", Text: text})
	if err != nil || exit != 0 || !strings.Contains(string(data), `"host":"owner"`) {
		t.Fatalf("result=%s exit=%d error=%v", data, exit, err)
	}
}
