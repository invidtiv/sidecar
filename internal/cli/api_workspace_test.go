package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentcontrol"
	"github.com/marcus/sidecar/internal/agentremote"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/hostserve"
	"github.com/marcus/sidecar/internal/livewatch"
	"github.com/marcus/sidecar/internal/plugins/workspace"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/testenv"
	"github.com/marcus/sidecar/internal/uiapi"
)

func TestWorkspaceCommandAdapterUsesExactCLIContracts(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	project := t.TempDir()
	cfg := config.Default()
	cfg.Projects.List = []config.ProjectConfig{{Name: "Proof", Path: project}}
	if err := os.MkdirAll(filepath.Dir(config.ConfigPath()), 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(config.ConfigPath(), data, 0600); err != nil {
		t.Fatal(err)
	}
	key := projKey(stateDir, project)
	path := filepath.Join(stateDir, "projects", key, "shells.json")
	writeProjectShells(t, stateDir, key, shellstate.Definition{TmuxName: "sidecar-sh-proof-1", DisplayName: "Before", Namespace: "/private/nonrunning.sock", CreatedAt: time.Now(), WorkDir: project})
	b := &mobileBackend{env: Env{StateDir: stateDir}}
	result, exit, err := b.WorkspaceOperation(context.Background(), key, uiapi.WorkspaceCommand{Operation: "shells/rename", Target: "sidecar-sh-proof-1", Name: "After"})
	if err != nil || exit != 0 {
		t.Fatalf("rename exit=%d err=%v result=%s", exit, err, result)
	}
	var got shellstate.RenameResult
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "After" || !got.Changed {
		t.Fatal(got)
	}
	// The second surface observes the first one's durable mutation and exact DTO.
	var out, stderr bytes.Buffer
	env := Env{StateDir: stateDir, Stdout: &out, Stderr: &stderr}
	if exit := runShellRename(env, []string{"--project", key, "--target", got.Shell, "--json", "--", "After"}); exit != 0 {
		t.Fatalf("cli exit=%d %s", exit, stderr.String())
	}
	if !strings.Contains(out.String(), `"changed":false`) {
		t.Fatal(out.String())
	}
	rows, _ := shellstate.ListAtPath(path)
	if rows[0].DisplayName != "After" {
		t.Fatal(rows)
	}
	result, exit, err = b.WorkspaceOperation(context.Background(), key, uiapi.WorkspaceCommand{Operation: "agents/prompt", Target: got.Shell, Text: "continue"})
	if err == nil || exit == 0 || !strings.Contains(string(result), `"receipt"`) || !strings.Contains(string(result), `"not_submitted"`) {
		t.Fatalf("prompt must retain CLI refusal receipt exit=%d err=%v result=%s", exit, err, result)
	}
}
func TestWorkspaceRemoteOperationsNeverResolveOnViewer(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	previous := newRemoteRunner
	t.Cleanup(func() { newRemoteRunner = previous })
	var calls [][]string
	newRemoteRunner = func(_ Env, host string) (agentremote.Runner, error) {
		if host != "owner" {
			t.Fatalf("host=%q", host)
		}
		return func(_ context.Context, h string, args []string, out any) error {
			calls = append(calls, append([]string(nil), args...))
			return json.Unmarshal([]byte(`{"shell":"remote-session","name":"Remote","status":"deleted","deleted":true}`), out)
		}, nil
	}
	b := &mobileBackend{env: Env{StateDir: stateDir}}
	result, exit, err := b.WorkspaceOperation(context.Background(), "owner-project", uiapi.WorkspaceCommand{Operation: "shells/delete", Host: "owner", Target: "remote-session"})
	if exit != 0 || err != nil || !strings.Contains(string(result), "deleted") {
		t.Fatalf("exit=%d err=%v result=%s", exit, err, result)
	}
	want := []string{"shell", "delete", "--target", "remote-session", "--project", "owner-project", "--json"}
	if !reflect.DeepEqual(calls, [][]string{want}) {
		t.Fatalf("calls=%v", calls)
	}
	for op := range map[string]bool{"agents/start": true, "agents/prompt": true, "worktrees/plan": true} {
		r := workspaceRemoteResult{Operation: op}
		_ = r.UnmarshalJSON([]byte(`{"level":"info","msg":"login banner"}`))
		if r.ValidRemoteResult() {
			t.Fatalf("%s accepted JSON banner", op)
		}
	}
}
func TestAPIWriterAndTUIManifestWatcherPreserveConcurrentEdits(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	project := t.TempDir()
	cfg := config.Default()
	cfg.Projects.List = []config.ProjectConfig{{Name: "Proof", Path: project}}
	if err := os.MkdirAll(filepath.Dir(config.ConfigPath()), 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(config.ConfigPath(), data, 0600); err != nil {
		t.Fatal(err)
	}
	key := projKey(stateDir, project)
	path := filepath.Join(stateDir, "projects", key, "shells.json")
	writeProjectShells(t, stateDir, key, shellstate.Definition{TmuxName: "sidecar-sh-proof-1", DisplayName: "Before", Namespace: "/private/nonrunning.sock", CreatedAt: time.Now(), WorkDir: project})
	manifest, err := workspace.LoadShellManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	watcher, err := livewatch.NewPathWatcher(livewatch.Config{Quiet: 10 * time.Millisecond, MaxLatency: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Stop()
	watcher.Watch(livewatch.File(path))
	b := &mobileBackend{env: Env{StateDir: stateDir}}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _, err := b.WorkspaceOperation(context.Background(), key, uiapi.WorkspaceCommand{Operation: "shells/rename", Target: "sidecar-sh-proof-1", Name: "API edit"})
		errs <- err
	}()
	go func() {
		defer wg.Done()
		errs <- manifest.AddShell(workspace.ShellDefinition{TmuxName: "sidecar-sh-proof-2", DisplayName: "TUI edit", Namespace: "/private/nonrunning.sock", CreatedAt: time.Now(), WorkDir: project})
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-watcher.Signals():
	case <-time.After(2 * time.Second):
		t.Fatal("TUI watcher did not see API mutation")
	}
	refreshed, err := workspace.LoadShellManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(refreshed.Shells) != 2 || refreshed.FindShell("sidecar-sh-proof-1").DisplayName != "API edit" || refreshed.FindShell("sidecar-sh-proof-2").DisplayName != "TUI edit" {
		t.Fatalf("lost concurrent edit: %+v", refreshed.Shells)
	}

}

func TestWorkspaceAgentOperationsUsePinnedServiceAndLiteralText(t *testing.T) {
	testenv.ProviderHelp(t, "codex", "usage: codex (older standalone CLI)")
	idle := codexIdleFixture(t)
	working := agentFixture(t, "working.txt")
	stateDir, _ := targetProject(t)
	terminal := &cliAgentTerminal{screen: idle}
	useCLIAgentTerminal(t, terminal)
	b := &mobileBackend{env: Env{StateDir: stateDir, FeatureOverrides: map[string]bool{"agent_control": true}}}
	result, exit, err := b.WorkspaceOperation(context.Background(), "demo", uiapi.WorkspaceCommand{Operation: "agents/start", Target: "sidecar-sh-demo-2", Kind: "codex", Args: []string{"--model", "space value"}})
	if exit != 0 || err != nil {
		t.Fatalf("exit=%d err=%v result=%s", exit, err, result)
	}
	var agent agentcontrol.Agent
	if err := json.Unmarshal(result, &agent); err != nil || !agent.Agent.InteractiveReady {
		t.Fatalf("%s %v", result, err)
	}
	if !reflect.DeepEqual(terminal.argv, []string{"codex", "--model", "space value"}) {
		t.Fatal(terminal.argv)
	}
	terminal.screens = []string{idle, working}
	terminal.inspects = 0
	result, exit, err = b.WorkspaceOperation(context.Background(), "demo", uiapi.WorkspaceCommand{Operation: "agents/prompt", Target: "sidecar-sh-demo-2", Text: "--help is literal"})
	if exit != 0 || err != nil || !reflect.DeepEqual(terminal.submitted, []string{"--help is literal"}) {
		t.Fatalf("exit=%d err=%v result=%s submitted=%v", exit, err, result, terminal.submitted)
	}
}
func TestWorkspaceRepeatableQueryFlagsReachOwner(t *testing.T) {
	setupIsolatedCLI(t)
	previous := newRemoteRunner
	t.Cleanup(func() { newRemoteRunner = previous })
	newRemoteRunner = func(_ Env, host string) (agentremote.Runner, error) {
		return func(_ context.Context, _ string, args []string, into any) error {
			want := []string{"workspace", "list", "--project", "owner-project", "--json", "--provider", "codex", "--provider", "claude", "--state", "working", "--state", "blocked"}
			if !reflect.DeepEqual(args, want) {
				t.Fatalf("args=%v", args)
			}
			return json.Unmarshal([]byte(`{"project":{"key":"owner-project"},"catalog":{"hub_id":"owner"},"shells":[]}`), into)
		}, nil
	}
	var out, errOut bytes.Buffer
	exit := runWorkspaceList(Env{Stdout: &out, Stderr: &errOut}, []string{"--project", "owner-project", "--host", "owner", "--provider=codex", "--provider", "claude", "--state", "working", "--state=blocked", "--json"})
	if exit != 0 {
		t.Fatalf("exit=%d %s", exit, errOut.String())
	}
}

func TestWorkspaceTombstoneOnlyWritesInvalidateResources(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	root := t.TempDir()
	writeProjectMeta(t, stateDir, "proof", root)
	path := filepath.Join(stateDir, "projects", "proof", "shells.json")
	def := shellstate.Definition{TmuxName: "sidecar-sh-proof-1", Namespace: "private", WorkDir: root}
	writeProjectShells(t, stateDir, "proof", def)
	id := shellstate.Identity{TmuxName: def.TmuxName, Namespace: def.Namespace}
	if err := shellstate.RemoveAtPath(path, id); err != nil {
		t.Fatal(err)
	}
	b := &mobileBackend{env: Env{StateDir: stateDir}}
	watch, err := livewatch.NewPathWatcher(livewatch.Config{Quiet: 10 * time.Millisecond, MaxLatency: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer watch.Stop()
	watch.Watch(b.workspaceWatchTargets([]hostserve.Project{{Path: root}})...)
	// A forgotten shell is absent from the catalog. Restoring only the record
	// must still reach workspace clients, with no API mutation-bus signal.
	if _, err := shellstate.RestoreAtPath(path, id); err != nil {
		t.Fatal(err)
	}
	select {
	case <-watch.Signals():
	case <-time.After(2 * time.Second):
		t.Fatal("record-only restoration did not invalidate the workspace")
	}
}

func TestWorkspaceScopePairingThroughCLI(t *testing.T) {
	stateDir := apiStateTree(t, t.TempDir())
	server, err := uiapi.Start(uiapi.Options{StateDir: stateDir, Port: 0, Backend: staticAPIBackend{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	code, out, stderr := runAPICLI(t, "api", "pair", "--origin", "http://widget.example", "--scope=workspace:write", "--scope", "workspace:write", "--json")
	var registration uiapi.OriginRegistration
	if code != 0 || json.Unmarshal([]byte(out), &registration) != nil || !reflect.DeepEqual(registration.Scopes, []string{uiapi.ScopeWorkspaceWrite}) {
		t.Fatalf("%d %s %s", code, out, stderr)
	}
	code, _, _ = runAPICLI(t, "api", "pair", "--list", "--scope", "full")
	if code != 2 {
		t.Fatalf("scope on list: %d", code)
	}
}
