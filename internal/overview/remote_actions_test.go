package overview

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/agentcontrol"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/workspacecreate"
	"github.com/marcus/sidecar/internal/workspaceinventory"
	"github.com/marcus/sidecar/internal/workspaceops"
)

// remoteInvocation is one recorded `sidecar <verb>` the browser sent to a host.
type remoteInvocation struct {
	HostID string
	Args   []string
}

// remoteRunnerStub replaces the request seam so a test can read the argument
// list the surface built without ssh, a network, or a second machine.
type remoteRunnerStub struct {
	calls   []remoteInvocation
	results []any
	errs    []error
}

func (s *remoteRunnerStub) install(t *testing.T) {
	t.Helper()
	original := runRemoteSidecar
	t.Cleanup(func() { runRemoteSidecar = original })
	runRemoteSidecar = func(_ context.Context, _ *hosts.Registry, hostID string, args []string, out any) error {
		index := len(s.calls)
		s.calls = append(s.calls, remoteInvocation{HostID: hostID, Args: append([]string(nil), args...)})
		if index < len(s.errs) && s.errs[index] != nil {
			return s.errs[index]
		}
		if out == nil || index >= len(s.results) || s.results[index] == nil {
			return nil
		}
		encoded, err := json.Marshal(s.results[index])
		if err != nil {
			return err
		}
		return json.Unmarshal(encoded, out)
	}
}

func (s *remoteRunnerStub) argv(t *testing.T, index int) []string {
	t.Helper()
	if index >= len(s.calls) {
		t.Fatalf("no remote invocation at index %d; got %d: %v", index, len(s.calls), s.calls)
	}
	return s.calls[index].Args
}

// localSeamGuard fails the test if any LOCAL mutation seam runs. Every remote
// action must reach the host and nothing else; a local create, a local plan
// resolution, or a local branch listing against a remote path is the failure
// this whole area exists to prevent.
func localSeamGuard(t *testing.T) {
	t.Helper()
	create, resolve, execute, branches := createManagedShell, resolveGlobalWorktree, executeGlobalWorktree, listCreateBranches
	agent := startGlobalAgent
	t.Cleanup(func() {
		createManagedShell, resolveGlobalWorktree, executeGlobalWorktree, listCreateBranches = create, resolve, execute, branches
		startGlobalAgent = agent
	})
	createManagedShell = func(workspaceops.ManagedShellSpec) (workspaceops.ShellResult, error) {
		t.Error("a remote action ran the LOCAL createManagedShell")
		return workspaceops.ShellResult{}, nil
	}
	resolveGlobalWorktree = func(context.Context, string, string, string, string, bool, config.WorktreeSetupConfig) (*workspaceops.WorktreePlan, error) {
		t.Error("a remote action resolved a worktree plan against the LOCAL filesystem")
		return nil, nil
	}
	executeGlobalWorktree = func(context.Context, string, *workspaceops.WorktreePlan) (*workspaceops.WorktreeRecord, error) {
		t.Error("a remote action executed a worktree on the LOCAL filesystem")
		return nil, nil
	}
	listCreateBranches = func(context.Context, string) ([]string, error) {
		t.Error("a remote action listed branches from a LOCAL git repository")
		return nil, nil
	}
	startGlobalAgent = func(context.Context, agentcontrol.StartRequest) (agentcontrol.Agent, error) {
		t.Error("a remote action sent keys to a LOCAL tmux session")
		return agentcontrol.Agent{}, nil
	}
}

// remoteCreateModel is a browser with one local project and one host whose
// project can be created in. The host has no registry and no connection: what
// is under test is which verb the surface sends, not how it travels.
func remoteCreateModel(t *testing.T) (*Model, *remoteRunnerStub) {
	t.Helper()
	// The last-agent and auto-approve preferences are package-global and persist
	// across tests in one run, so an unset choice has to be made unset rather
	// than assumed.
	if err := state.SetLastCreateAgent(""); err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"claude", "codex"} {
		if err := state.SetAgentAutoApprove(agent, false); err != nil {
			t.Fatal(err)
		}
	}
	m := hostModel(t, "mac-mini", hosts.Health{State: hosts.StateOnline}, remoteSnapshot("working"))
	m.projects = []Project{{Name: "sidecar", Path: "/tmp/sidecar", Key: "sidecar"}}
	m.results["sidecar"] = workspaceinventory.ProjectResult{ProjectKey: "sidecar"}
	m.config = config.Default()
	m.config.Plugins.Workspace.Agents = []string{"claude", "codex"}
	m.config.Plugins.Workspace.AgentStart = map[string]string{"claude": "claude"}
	m.width, m.height = 100, 40
	m.syncBoard()
	m.syncWorkspaces()
	stub := &remoteRunnerStub{}
	stub.install(t)
	return m, stub
}

// remoteProjectKey is the create-form key for the host project remoteSnapshot
// describes.
func remoteProjectKey() string { return hosts.ScopedKey("mac-mini", "/home/me/api") }

// remoteShellRowID is the ID of the one shell row remoteSnapshot carries.
func remoteShellRowID() string { return hosts.ScopedKey("mac-mini", "/home/me/api:shell:s1") }

func assertCreateProject(t *testing.T, m *Model, key string) {
	t.Helper()
	if m.createForm == nil {
		t.Fatal("no create form is open")
	}
	if got := m.createForm.ProjectKey(); got != key {
		t.Fatalf("form project = %q, want %q", got, key)
	}
}

// TestRemoteProjectsAppearInTheCreatePicker: a host's projects are offered, and
// labelled the way its rows already are.
func TestRemoteProjectsAppearInTheCreatePicker(t *testing.T) {
	m, _ := remoteCreateModel(t)
	items := m.createProjectItems()
	var remote *workspacecreate.ProjectItem
	for i := range items {
		if items[i].Key == remoteProjectKey() {
			remote = &items[i]
		}
	}
	if remote == nil {
		t.Fatalf("the host's project is not offered: %+v", items)
	}
	if remote.Label != "mac-mini"+hostRowPrefix+"api" {
		t.Errorf("label = %q, want the host row's own labelling", remote.Label)
	}
	if items[0].Key != "sidecar" {
		t.Errorf("local projects no longer come first: %+v", items)
	}
}

// TestRemoteCreateShellSendsTheHostVerb is the steel thread. Choosing a host's
// project and creating a shell must reach that machine as its own CLI verb, and
// must not touch anything here.
func TestRemoteCreateShellSendsTheHostVerb(t *testing.T) {
	m, stub := remoteCreateModel(t)
	localSeamGuard(t)
	stub.results = []any{map[string]any{"shell": map[string]any{"session": "sidecar-sh-api-3", "displayName": "Shell 3"}}}

	run(t, m, m.OpenCreateShell(remoteProjectKey()))
	assertCreateProject(t, m, remoteProjectKey())
	cmd := m.submitCreateShell()
	if cmd == nil {
		t.Fatalf("no command; error=%q", m.createError)
	}
	msg := cmd().(globalShellCreatedMsg)

	if len(stub.calls) != 1 {
		t.Fatalf("invocations = %v, want exactly the create", stub.calls)
	}
	if stub.calls[0].HostID != "mac-mini" {
		t.Errorf("addressed host = %q", stub.calls[0].HostID)
	}
	want := []string{"create", "shell", "--project", "/home/me/api", "--json"}
	if got := stub.argv(t, 0); !equalArgs(got, want) {
		t.Errorf("argv = %v, want %v", got, want)
	}
	if msg.HostID != "mac-mini" || msg.Tmux != "sidecar-sh-api-3" || msg.Err != nil {
		t.Fatalf("reply = %+v", msg)
	}

	// The row arrives with the host's next snapshot; nothing is synthesized and
	// no local inventory is taken.
	if refresh := m.Update(msg); refresh != nil {
		t.Errorf("a remote create scheduled local work: %T", refresh())
	}
	if m.CreateOpen() {
		t.Error("the modal stayed open after a successful remote create")
	}
	if m.pendingCreatedHost != "mac-mini" || m.pendingCreatedTmux != "sidecar-sh-api-3" {
		t.Errorf("pending selection = %q on %q, want the host's own session",
			m.pendingCreatedTmux, m.pendingCreatedHost)
	}
}

// TestRemoteCreateShellSendsATypedNameOnly. "Shell N" counted from the rows
// this viewer last saw would name a shell after the wrong machine's numbering.
func TestRemoteCreateShellSendsATypedNameOnly(t *testing.T) {
	m, stub := remoteCreateModel(t)
	localSeamGuard(t)
	stub.results = []any{map[string]any{"shell": map[string]any{"session": "s"}}}

	run(t, m, m.OpenCreateShell(remoteProjectKey()))
	typeCreateName(t, m, "Reviewer")
	cmd := m.submitCreateShell()
	if cmd == nil {
		t.Fatalf("no command; error=%q", m.createError)
	}
	cmd()
	want := []string{"create", "shell", "--project", "/home/me/api", "--name", "Reviewer", "--json"}
	if got := stub.argv(t, 0); !equalArgs(got, want) {
		t.Errorf("argv = %v, want %v", got, want)
	}
}

// TestRemoteCreateShellStartsTheAgentOnTheHost mirrors the local two-step:
// create the shell, then start the agent in the session that came back.
func TestRemoteCreateShellStartsTheAgentOnTheHost(t *testing.T) {
	m, stub := remoteCreateModel(t)
	localSeamGuard(t)
	stub.results = []any{map[string]any{"shell": map[string]any{"session": "api-claude-2"}}, nil}

	run(t, m, m.OpenCreateShell(remoteProjectKey()))
	selectCreateAgent(t, m, "claude")
	cmd := m.submitCreateShell()
	if cmd == nil {
		t.Fatalf("no command; error=%q", m.createError)
	}
	msg := cmd().(globalShellCreatedMsg)
	if msg.Err != nil {
		t.Fatalf("remote create failed: %v", msg.Err)
	}

	if len(stub.calls) != 2 {
		t.Fatalf("invocations = %v, want create then send", stub.calls)
	}
	send := stub.argv(t, 1)
	for i, want := range []string{"shell", "send", "--target", "api-claude-2", "--project", "/home/me/api", "--run"} {
		if i >= len(send) || send[i] != want {
			t.Fatalf("send argv = %v, want %v at %d", send, want, i)
		}
	}
	if !strings.HasPrefix(send[7], "claude") {
		t.Errorf("agent command = %q, want the resolved claude command", send[7])
	}
	if send[len(send)-1] != "--json" {
		t.Errorf("send argv did not ask for JSON: %v", send)
	}
}

// TestRemoteAgentCommandIgnoresThisMachinesAgentFile. Resolving an agent for a
// shell on another machine must not read .sidecar-agent-start from a path that
// happens to exist here too.
func TestRemoteAgentCommandIgnoresThisMachinesAgentFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".sidecar-agent-start"), []byte("wrong-machine-agent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, _ := remoteCreateModel(t)
	m.config.Plugins.Workspace.AgentStart = map[string]string{"claude": "claude --resume"}
	// The remote project's path, as it would be if the two machines shared a
	// layout: a real directory here, holding a file that means something else.
	for i := range m.hostProjects["mac-mini"] {
		m.hostProjects["mac-mini"][i].Path = dir
	}
	got := m.remoteAgentCommand("claude", false)
	if strings.Contains(got, "wrong-machine-agent") {
		t.Fatalf("remote agent command read this machine's file: %q", got)
	}
	if !strings.HasPrefix(got, "claude --resume") {
		t.Fatalf("remote agent command = %q, want the configured one", got)
	}
	// The local resolution is unchanged: it still prefers the checkout's file.
	if local := workspaceops.ResolveAgentCommand(dir, "claude", m.config.Plugins.Workspace.AgentStart, false); !strings.Contains(local, "wrong-machine-agent") {
		t.Fatalf("the LOCAL resolution stopped reading .sidecar-agent-start: %q", local)
	}
}

// TestRemoteWorktreePlansThenCreates: two round trips, in that order, with
// --plan on the first and nothing created until Create is pressed.
func TestRemoteWorktreePlansThenCreates(t *testing.T) {
	m, stub := remoteCreateModel(t)
	localSeamGuard(t)
	stub.results = []any{
		&workspaceops.WorktreePlan{Branch: "feature", Path: "/home/me/api-feature", SourceRef: "main", SourceOID: "abcdef1234", RemotePolicy: "no remote", RunHook: true, HookPath: "setup.sh", HookRequired: true},
		map[string]any{"path": "/home/me/api-feature", "branch": "feature", "shell": map[string]any{"session": "api-feature"}},
	}

	run(t, m, m.OpenCreateWorktree(remoteProjectKey()))
	assertCreateProject(t, m, remoteProjectKey())
	typeCreateName(t, m, "feature")
	selectCreateAgent(t, m, "claude")
	cmd := m.planCreateWorktree()
	if cmd == nil {
		t.Fatalf("no plan command; error=%q", m.createError)
	}
	planned := cmd().(globalWorktreePlannedMsg)
	if planned.Err != nil {
		t.Fatalf("plan failed: %v", planned.Err)
	}
	want := []string{"create", "worktree", "--project", "/home/me/api", "--agent", "claude", "--plan", "--json", "--", "feature"}
	if got := stub.argv(t, 0); !equalArgs(got, want) {
		t.Fatalf("plan argv = %v, want %v", got, want)
	}
	if len(stub.calls) != 1 {
		t.Fatalf("planning already created something: %v", stub.calls)
	}

	m.Update(planned)
	if m.createPlan == nil {
		t.Fatal("the confirmation has no plan to show")
	}
	summary := createPlanSummary(m.createPlan, m.createTargetHost)
	for _, want := range []string{"mac-mini", "feature", "/home/me/api-feature", "setup.sh", "required"} {
		if !strings.Contains(summary, want) {
			t.Errorf("confirmation lost %q:\n%s", want, summary)
		}
	}

	exec := m.executeCreateWorktree()
	if exec == nil {
		t.Fatal("Create produced no command")
	}
	created := exec().(globalWorktreeCreatedMsg)
	if created.Err != nil {
		t.Fatalf("execute failed: %v", created.Err)
	}
	// The execute carries the confirmed plan's SourceOID. Without the pin the
	// host re-resolves the ref from scratch, and a ref that moved between the
	// confirmation and the Create press builds silently at the new head —
	// where the identical local sequence refuses.
	wantExec := []string{"create", "worktree", "--project", "/home/me/api", "--agent", "claude", "--expect-source-oid", "abcdef1234", "--json", "--", "feature"}
	if got := stub.argv(t, 1); !equalArgs(got, wantExec) {
		t.Fatalf("execute argv = %v, want %v (no --plan, pinned source)", got, wantExec)
	}

	if cmd := m.Update(created); cmd != nil {
		t.Errorf("a remote worktree scheduled local work: %T", cmd())
	}
	if m.pendingCreatedHost != "mac-mini" || m.pendingCreatedPath != "/home/me/api-feature" {
		t.Errorf("pending selection = %q on %q", m.pendingCreatedPath, m.pendingCreatedHost)
	}
	if m.createRecord != nil {
		t.Error("a remote creation produced a LOCAL worktree record")
	}
}

// TestRemoteWorktreePassesTheChosenAgentToTheHost — the host resolves the agent
// command with its own config and its own checkout, which is why the type
// travels rather than a resolved command.
func TestRemoteWorktreePassesTheChosenAgentToTheHost(t *testing.T) {
	m, stub := remoteCreateModel(t)
	localSeamGuard(t)
	stub.results = []any{&workspaceops.WorktreePlan{Branch: "f", Path: "/home/me/api-f"}}

	run(t, m, m.OpenCreateWorktree(remoteProjectKey()))
	typeCreateName(t, m, "f")
	selectCreateAgent(t, m, "codex")
	cmd := m.planCreateWorktree()
	if cmd == nil {
		t.Fatalf("no plan command; error=%q", m.createError)
	}
	cmd()
	want := []string{"create", "worktree", "--project", "/home/me/api", "--agent", "codex", "--plan", "--json", "--", "f"}
	if got := stub.argv(t, 0); !equalArgs(got, want) {
		t.Errorf("argv = %v, want %v", got, want)
	}
}

// TestRemoteWorktreeArgsCarryEveryChoice pins the argument list itself, which is
// the contract between this surface and the host's CLI.
func TestRemoteWorktreeArgsCarryEveryChoice(t *testing.T) {
	got := remoteWorktreeArgs("/home/me/api", "feature", "main", "claude", "", true, false)
	want := []string{"create", "worktree", "--project", "/home/me/api", "--base", "main", "--agent", "claude", "--skip-permissions", "--json", "--", "feature"}
	if !equalArgs(got, want) {
		t.Fatalf("argv = %v, want %v", got, want)
	}
	if plan := remoteWorktreeArgs("/home/me/api", "feature", "", "", "", false, true); !equalArgs(plan, []string{"create", "worktree", "--project", "/home/me/api", "--plan", "--json", "--", "feature"}) {
		t.Fatalf("plan argv = %v", plan)
	}
	// The confirmed plan's SourceOID travels with the execute, so the host can
	// refuse a ref that moved after the confirmation was shown.
	pinned := remoteWorktreeArgs("/home/me/api", "feature", "", "", "0123456789abcdef", false, false)
	if !equalArgs(pinned, []string{"create", "worktree", "--project", "/home/me/api", "--expect-source-oid", "0123456789abcdef", "--json", "--", "feature"}) {
		t.Fatalf("pinned argv = %v", pinned)
	}
}

// TestLocalAndRemoteConfirmationsSayTheSameThing. The hook line comes from the
// shared renderer, so a plan says the same thing whichever machine resolved it.
func TestLocalAndRemoteConfirmationsSayTheSameThing(t *testing.T) {
	plan := &workspaceops.WorktreePlan{Branch: "b", Path: "/p", SourceRef: "main", SourceOID: "0123456789", RemotePolicy: "push", RunHook: true, HookPath: "setup.sh"}
	local := createPlanSummary(plan, "")
	remote := createPlanSummary(plan, "mac-mini")
	if !strings.Contains(local, "setup.sh") || !strings.Contains(remote, "setup.sh") {
		t.Fatalf("hook line missing:\nlocal:  %s\nremote: %s", local, remote)
	}
	if strings.Contains(local, "mac-mini") {
		t.Error("a local confirmation named a host")
	}
	if !strings.Contains(remote, "mac-mini") {
		t.Error("a remote confirmation does not say which machine")
	}
	if strings.TrimPrefix(remote, "On mac-mini\n\n") != local {
		t.Errorf("the two confirmations differ beyond the host line:\nlocal:  %q\nremote: %q", local, remote)
	}
	if off := createPlanSummary(&workspaceops.WorktreePlan{Branch: "b", Path: "/p"}, ""); !strings.Contains(off, "No setup hook") {
		t.Errorf("a plan with no hook does not say so: %s", off)
	}
	required := createPlanHookLine(&workspaceops.WorktreePlan{RunHook: true, HookPath: "s.sh", HookRequired: true})
	optional := createPlanHookLine(&workspaceops.WorktreePlan{RunHook: true, HookPath: "s.sh"})
	if !strings.Contains(required, "required") || strings.Contains(optional, "required") {
		t.Errorf("hook lines do not distinguish required from optional: %q / %q", required, optional)
	}
}

// TestRemoteBranchListingNeverRunsHere. `git branch` in a remote path answers
// with THIS machine's repository when the layout matches.
func TestRemoteBranchListingNeverRunsHere(t *testing.T) {
	m, _ := remoteCreateModel(t)
	localSeamGuard(t)
	run(t, m, m.OpenCreateWorktree(remoteProjectKey()))
	if cmd := m.loadCreateBranches(); cmd != nil {
		cmd()
		t.Error("branch listing ran for a remote project")
	}
}

// TestRemoteRenameTargetsTheSessionOnItsHost.
func TestRemoteRenameTargetsTheSessionOnItsHost(t *testing.T) {
	m, stub := remoteCreateModel(t)
	localSeamGuard(t)
	stub.results = []any{map[string]any{"name": "Reviewer"}}
	m.workspaces.SelectID(remoteShellRowID())

	m.OpenRenameShell()
	if !m.RenameShellOpen() {
		t.Fatal("renaming a remote shell was refused")
	}
	m.renameInput.SetValue("Reviewer")
	cmd := m.executeRename()
	if cmd == nil {
		t.Fatalf("no rename command; error=%q", m.renameError)
	}
	done := cmd().(renameShellDoneMsg)
	if done.Err != nil {
		t.Fatalf("remote rename failed: %v", done.Err)
	}
	want := []string{"shell", "rename", "--target", "api-claude", "--project", "/home/me/api", "--json", "--", "Reviewer"}
	if got := stub.argv(t, 0); !equalArgs(got, want) {
		t.Fatalf("argv = %v, want %v", got, want)
	}
	if stub.calls[0].HostID != "mac-mini" {
		t.Errorf("addressed host = %q", stub.calls[0].HostID)
	}

	m.Update(done)
	if m.RenameShellOpen() {
		t.Error("a successful remote rename left the modal open")
	}
	if got := m.catalog[remoteShellRowID()].Name; got != "Reviewer" {
		t.Errorf("the row still reads %q", got)
	}
}

// TestRemoteRenameSwallowsInputWhileInFlight. The ssh round trip is long enough
// for a second Enter, which dispatched a second rename that raced the first on
// the host — the loser's reply was silently dropped.
func TestRemoteRenameSwallowsInputWhileInFlight(t *testing.T) {
	m, stub := remoteCreateModel(t)
	localSeamGuard(t)
	stub.results = []any{map[string]any{"shell": "api-claude", "name": "Reviewer"}}
	m.workspaces.SelectID(remoteShellRowID())
	m.OpenRenameShell()
	if !m.RenameShellOpen() {
		t.Fatal("the rename modal did not open")
	}
	m.renameInput.SetValue("Reviewer")

	_, cmd := m.handleRenameShellKey(createKey("enter"))
	if cmd == nil {
		t.Fatalf("the first Enter dispatched nothing; error=%q", m.renameError)
	}
	if !m.renameBusy {
		t.Fatal("the modal does not show that it is waiting")
	}
	if _, second := m.handleRenameShellKey(createKey("enter")); second != nil {
		t.Fatal("a second Enter while the rename was in flight dispatched again")
	}

	done := cmd().(renameShellDoneMsg)
	if len(stub.calls) != 1 {
		t.Fatalf("invocations = %v, want exactly one rename", stub.calls)
	}
	m.Update(done)
	if m.RenameShellOpen() {
		t.Error("the modal stayed open after the reply")
	}
	if m.renameBusy {
		t.Error("the busy guard survived the reply")
	}

	// A failed rename clears the guard too, or the modal that stays open for a
	// retry could never be submitted again.
	m2, _ := remoteCreateModel(t)
	m2.workspaces.SelectID(remoteShellRowID())
	m2.OpenRenameShell()
	m2.renameBusy = true
	m2.Update(renameShellDoneMsg{ID: remoteShellRowID(), Err: errors.New("boom")})
	if !m2.RenameShellOpen() {
		t.Fatal("a failed rename closed the modal, losing the reason")
	}
	if m2.renameBusy {
		t.Error("a failed rename left the modal swallowing input forever")
	}
}

// TestSwitchingToARemoteProjectClearsLocalBranches. The branch list and the
// prefilled base a LOCAL project loaded describe this machine's repository;
// carrying them across a switch to a host's project offers a --base the host
// resolves against a different history.
func TestSwitchingToARemoteProjectClearsLocalBranches(t *testing.T) {
	m, _ := remoteCreateModel(t)
	run(t, m, m.OpenCreateWorktree("sidecar"))
	assertCreateProject(t, m, "sidecar")
	m.applyCreateBranches(globalCreateBranchesMsg{ProjectKey: "sidecar", Branches: []string{"main", "dev"}, Current: "main"})
	if got := m.createForm.BaseBranch(); got != "main" {
		t.Fatalf("base = %q, want the local prefill before the switch", got)
	}

	localSeamGuard(t)
	selectCreateProject(t, m, remoteProjectKey())
	if got := m.createForm.BaseBranch(); got != "" {
		t.Fatalf("base %q survived the switch to a remote project", got)
	}
}

// TestRemoteWorktreeRenameDerivesItsSession. A worktree that is not running
// carries no session in the snapshot, but the host resolves its target by the
// same name derivation, which is pure string work over the path.
func TestRemoteWorktreeRenameDerivesItsSession(t *testing.T) {
	workspace := workspaceinventory.Workspace{
		HostID: "mac-mini", Kind: workspaceinventory.KindWorktree,
		Name: "feature", Path: "/home/me/api-feature",
	}
	got := remoteTargetSession(workspace)
	if got == "" {
		t.Fatal("a dormant remote worktree cannot be addressed at all")
	}
	if got != workspaceops.WorktreeSessionName("/home/me/api-feature", "feature") {
		t.Fatalf("derived session = %q", got)
	}
	live := workspaceinventory.Workspace{HostID: "h", Kind: workspaceinventory.KindShell, TmuxName: "given"}
	if remoteTargetSession(live) != "given" {
		t.Error("a row that names its session was overridden")
	}
}

// TestRemoteMutationFailureNamesItsFix. A failed remote action must say what
// happened and what to do, the way a host health row does.
func TestRemoteMutationFailureNamesItsFix(t *testing.T) {
	m, stub := remoteCreateModel(t)
	localSeamGuard(t)
	stub.errs = []error{&hosts.RunError{
		Failure: hosts.FailNoSidecar, HostID: "mac-mini", ExitCode: 127,
		Detail: "sidecar: command not found",
	}}

	run(t, m, m.OpenCreateShell(remoteProjectKey()))
	cmd := m.submitCreateShell()
	if cmd == nil {
		t.Fatal("no command")
	}
	m.Update(cmd())

	if !m.CreateOpen() {
		t.Fatal("a failed remote create closed the modal, losing the reason")
	}
	if !strings.Contains(m.createError, "command not found") {
		t.Errorf("error %q does not carry the remote's own words", m.createError)
	}
	if !strings.Contains(m.createError, "install Sidecar on that machine") {
		t.Errorf("error %q does not name the fix", m.createError)
	}
	if m.pendingCreatedHost != "" || m.pendingCreatedTmux != "" {
		t.Error("a failed create left a pending selection behind")
	}
}

// TestRemoteReplyAfterHostRemovalIsDropped. A config save reconciles hosts
// live, so an answer can arrive after its host is gone. It must not be applied,
// and it must not leave a modal spinning on a machine that no longer exists.
func TestRemoteReplyAfterHostRemovalIsDropped(t *testing.T) {
	m, stub := remoteCreateModel(t)
	localSeamGuard(t)
	m.hostRegistered = map[string]bool{"mac-mini": true}
	m.hostIncarnations = map[string]uint64{"mac-mini": 4}
	stub.results = []any{map[string]any{"shell": map[string]any{"session": "api-2"}}}

	run(t, m, m.OpenCreateShell(remoteProjectKey()))
	cmd := m.submitCreateShell()
	if cmd == nil {
		t.Fatal("no command")
	}
	reply := cmd().(globalShellCreatedMsg)
	if reply.Incarnation != 4 {
		t.Fatalf("the request did not carry the host incarnation: %+v", reply)
	}

	// The host is removed while the create is in flight.
	m.hostRegistered = map[string]bool{}
	delete(m.hostIncarnations, "mac-mini")
	m.Update(reply)

	if m.pendingCreatedHost != "" || m.pendingCreatedTmux != "" {
		t.Errorf("a dropped reply still queued a selection: %q on %q", m.pendingCreatedTmux, m.pendingCreatedHost)
	}
	if m.createBusy {
		t.Error("the modal is still waiting on a host that is gone")
	}
	if !strings.Contains(m.createError, "mac-mini") {
		t.Errorf("the drop was silent: %q", m.createError)
	}

	// Same fence for a retarget: the host is registered again, under a new
	// incarnation, and an answer from the old machine is not this one's.
	m.hostRegistered = map[string]bool{"mac-mini": true}
	m.hostIncarnations = map[string]uint64{"mac-mini": 9}
	if !m.hostReplyStale("mac-mini", 4) {
		t.Error("a reply from a replaced host incarnation was accepted")
	}
	if m.hostReplyStale("mac-mini", 9) {
		t.Error("the current incarnation's reply was rejected")
	}
	if m.hostReplyStale("", 0) {
		t.Error("a local reply was treated as a remote one")
	}
}

// TestRemoteRenameReplyAfterHostRemovalIsDropped: the rename modal has the same
// fence, and must not paint a name that came from a machine this configuration
// no longer points at.
func TestRemoteRenameReplyAfterHostRemovalIsDropped(t *testing.T) {
	m, _ := remoteCreateModel(t)
	id := remoteShellRowID()
	m.workspaces.SelectID(id)
	m.OpenRenameShell()
	if !m.RenameShellOpen() {
		t.Fatal("the rename modal did not open")
	}
	m.hostRegistered = map[string]bool{}

	m.Update(renameShellDoneMsg{
		remoteReply: remoteReply{HostID: "mac-mini", Incarnation: 1},
		ID:          id, NewName: "Ghost",
	})

	if !m.RenameShellOpen() {
		t.Fatal("a dropped rename closed the modal as if it had succeeded")
	}
	if got := m.catalog[id].Name; got == "Ghost" {
		t.Error("a dropped rename was applied to the row anyway")
	}
	if !strings.Contains(m.renameError, "mac-mini") {
		t.Errorf("the drop was silent: %q", m.renameError)
	}
}

// TestRemoteRowRefusesTheGitJump. OpenSelectedInGit had no remote guard: the
// app answers OpenInGitMsg with a LOCAL SwitchWorktree, which on a machine laid
// out like the other one silently opens the wrong repository.
func TestRemoteRowRefusesTheGitJump(t *testing.T) {
	m, _ := remoteCreateModel(t)
	m.workspaces.SelectID(remoteShellRowID())
	if ws, ok := m.SelectedWorkspace(); !ok || !ws.Remote() {
		t.Fatalf("no remote row is selected: %+v ok=%v", ws, ok)
	}

	cmd := m.OpenSelectedInGit()
	if cmd == nil {
		t.Fatal("O on a remote row did nothing at all, not even a refusal")
	}
	if _, isJump := cmd().(OpenInGitMsg); isJump {
		t.Fatal("O on a remote row asked the app to open a remote path locally")
	}
	if m.CanOpenInGit() {
		t.Error("the footer still offers Git on a remote row")
	}
	if path, ok := m.openInGitPath(); ok {
		t.Errorf("a remote row resolved a local checkout: %q", path)
	}
}

// TestLocalCreateAfterARemoteOneStaysLocal is the Phase A defect in this
// surface's shape: a model that went remote once and stayed remote. The target
// is resolved from the form on every submission, never remembered.
func TestLocalCreateAfterARemoteOneStaysLocal(t *testing.T) {
	m, stub := remoteCreateModel(t)
	stub.results = []any{map[string]any{"shell": map[string]any{"session": "api-2"}}}

	run(t, m, m.OpenCreateShell(remoteProjectKey()))
	if cmd := m.submitCreateShell(); cmd != nil {
		m.Update(cmd())
	}
	if m.createTargetHost != "" {
		t.Fatalf("the create flow stayed pointed at %q after it closed", m.createTargetHost)
	}

	original := createManagedShell
	t.Cleanup(func() { createManagedShell = original })
	var localSpec workspaceops.ManagedShellSpec
	createManagedShell = func(spec workspaceops.ManagedShellSpec) (workspaceops.ShellResult, error) {
		localSpec = spec
		return workspaceops.ShellResult{SessionName: spec.SessionName}, nil
	}
	before := len(stub.calls)

	run(t, m, m.OpenCreateShell("sidecar"))
	assertCreateProject(t, m, "sidecar")
	cmd := m.submitCreateShell()
	if cmd == nil {
		t.Fatalf("no local command; error=%q", m.createError)
	}
	msg := cmd().(globalShellCreatedMsg)

	if len(stub.calls) != before {
		t.Fatalf("a LOCAL create was sent to a host: %v", stub.calls[before:])
	}
	if localSpec.ProjectRoot != "/tmp/sidecar" {
		t.Errorf("local create ran against %q", localSpec.ProjectRoot)
	}
	if msg.HostID != "" || m.pendingCreatedHost != "" {
		t.Errorf("a local create carried a host: msg=%q pending=%q", msg.HostID, m.pendingCreatedHost)
	}
}

// TestPendingSelectionIsScopedToItsMachine. Two machines with the same layout
// produce the same worktree path and the same session name; without the host
// scope a remote creation is answered by the local row.
func TestPendingSelectionIsScopedToItsMachine(t *testing.T) {
	m, _ := remoteCreateModel(t)
	m.results["sidecar"] = workspaceinventory.ProjectResult{ProjectKey: "sidecar", Workspaces: []workspaceinventory.Workspace{
		{ID: "local-twin", ProjectKey: "sidecar", ProjectName: "sidecar", Kind: workspaceinventory.KindWorktree, Name: "feature", Path: "/home/me/api-feature"},
	}}
	m.hostResults["mac-mini"] = append(m.hostResults["mac-mini"], workspaceinventory.ProjectResult{
		ProjectKey: remoteProjectKey() + "-2", ProjectName: "api2", ProjectRoot: "/home/me/api2",
		Workspaces: []workspaceinventory.Workspace{{
			ID: "remote-twin", HostID: "mac-mini", ProjectKey: remoteProjectKey() + "-2",
			Kind: workspaceinventory.KindWorktree, Name: "feature", Path: "/home/me/api-feature",
		}},
	})
	m.hostProjects["mac-mini"] = append(m.hostProjects["mac-mini"], Project{Name: "api2", Path: "/home/me/api2", Key: remoteProjectKey() + "-2", Index: 1})
	m.showIdleWorktrees = true
	m.syncBoard()
	m.syncWorkspaces()

	m.pendingCreatedHost = "mac-mini"
	m.pendingCreatedPath = "/home/me/api-feature"
	if !m.honorPendingCreated() {
		t.Fatal("the remote worktree was never selected")
	}
	if got := m.workspaces.SelectedID(); got != "remote-twin" {
		t.Fatalf("selected %q, want the row on the machine it was created on", got)
	}

	// And the reverse: a local pending must not be answered by the remote twin.
	m.pendingCreatedHost = ""
	m.pendingCreatedPath = "/home/me/api-feature"
	if !m.honorPendingCreated() {
		t.Fatal("the local worktree was never selected")
	}
	if got := m.workspaces.SelectedID(); got != "local-twin" {
		t.Fatalf("selected %q, want the local row", got)
	}
}

// TestRemoteInvocationsAreNotRunOnTheUpdateLoop. Four review cycles in Phase B
// were about exactly this: a remote round trip inside Update blocks every
// frame, every keypress and the quit behind it.
func TestRemoteInvocationsAreNotRunOnTheUpdateLoop(t *testing.T) {
	m, stub := remoteCreateModel(t)
	localSeamGuard(t)
	stub.results = []any{map[string]any{"shell": map[string]any{"session": "s"}}}

	run(t, m, m.OpenCreateShell(remoteProjectKey()))
	cmd := m.submitCreateShell()
	if cmd == nil {
		t.Fatal("no command")
	}
	if len(stub.calls) != 0 {
		t.Fatalf("the host was invoked from Update itself: %v", stub.calls)
	}
	if !m.createBusy {
		t.Error("the modal does not show that it is waiting")
	}
	cmd()
	if len(stub.calls) != 1 {
		t.Fatalf("the command did not run the invocation: %v", stub.calls)
	}
}

// TestRemoteRunnerReportsNoRegistry. A remote action attempted with no host
// registry must fail as an unavailable host, not panic.
func TestRemoteRunnerReportsNoRegistry(t *testing.T) {
	err := runRemoteSidecarProduction(context.Background(), nil, "mac-mini", []string{"create", "shell"}, nil)
	if hosts.RunFailure(err) != hosts.FailUnavailable {
		t.Fatalf("failure = %q, want unavailable", hosts.RunFailure(err))
	}
	var runErr *hosts.RunError
	if !errors.As(err, &runErr) || runErr.HostID != "mac-mini" {
		t.Fatalf("error does not name the host: %v", err)
	}
}

func equalArgs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
