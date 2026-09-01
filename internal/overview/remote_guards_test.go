package overview

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspacecreate"
	"github.com/marcus/sidecar/internal/workspaceinventory"
	"github.com/marcus/sidecar/internal/workspaceops"
)

// TestRemoteResultsRefuseALogLine covers the surface's half of the decoder fix.
//
// internal/hosts refuses an all-zero result for every type; these three say
// more than that, because "not zero" cannot tell a half-filled object from a
// real one. The object in each case is a structured log line a login profile
// or a version-manager wrapper writes to stdout, which is the condition the
// plan names as a required row state.
func TestRemoteResultsRefuseALogLine(t *testing.T) {
	logLine := []byte(`{"level":"info","msg":"loading nvm","name":"nvm","path":"/usr/local/nvm"}`)

	var shell remoteShellResult
	if err := json.Unmarshal(logLine, &shell); err != nil {
		t.Fatal(err)
	}
	if shell.ValidRemoteResult() {
		t.Error("a log line passed for a create shell result")
	}

	var worktree remoteWorktreeResult
	if err := json.Unmarshal(logLine, &worktree); err != nil {
		t.Fatal(err)
	}
	if worktree.ValidRemoteResult() {
		t.Errorf("a log line passed for a create worktree result: %+v", worktree)
	}

	var rename remoteRenameResult
	if err := json.Unmarshal(logLine, &rename); err != nil {
		t.Fatal(err)
	}
	if rename.ValidRemoteResult() {
		t.Errorf("a log line passed for a rename result: %+v", rename)
	}

	// A plan missing either half is the blank confirmation: "Create  at " with
	// a Create button that really runs on the user's machine.
	for _, body := range []string{`{}`, `{"branch":"feature"}`, `{"path":"/home/me/api-feature"}`} {
		var plan remoteWorktreePlan
		if err := json.Unmarshal([]byte(body), &plan); err != nil {
			t.Fatal(err)
		}
		if plan.ValidRemoteResult() {
			t.Errorf("%s passed for a worktree plan", body)
		}
	}

	// And the real answers still are answers, unknown fields included.
	var realShell remoteShellResult
	if err := json.Unmarshal([]byte(`{"shell":{"session":"api-2"},"futureField":1}`), &realShell); err != nil {
		t.Fatal(err)
	}
	if !realShell.ValidRemoteResult() {
		t.Error("a real create shell result was refused")
	}
	var realPlan remoteWorktreePlan
	if err := json.Unmarshal([]byte(`{"branch":"feature","path":"/home/me/api-feature","futureField":1}`), &realPlan); err != nil {
		t.Fatal(err)
	}
	if !realPlan.ValidRemoteResult() {
		t.Error("a real worktree plan was refused")
	}
}

// TestLocalCreateAfterARemoteOneSelectsTheLocalRow is finding 3's reproduction,
// through the real activation path rather than by assigning both fields by hand.
//
// pendingCreatedHost was SET by submitCreateShell and by nothing else, so a
// browser that had created remotely once carried "mac-mini" into the next LOCAL
// creation. honorPendingCreated then matched only rows on that host, set
// results = nil so the local snapshot was not even searched, and returned
// false: nothing was selected and the pending selection never cleared.
func TestLocalCreateAfterARemoteOneSelectsTheLocalRow(t *testing.T) {
	m, _ := remoteCreateModel(t)

	// A remote worktree creation lands, leaving its host on the pending fields.
	m.Update(globalWorktreeCreatedMsg{
		remoteReply: remoteReply{HostID: "mac-mini"},
		RemotePath:  "/home/me/api-feature",
	})
	if m.pendingCreatedHost != "mac-mini" {
		t.Fatalf("pending host = %q, want the machine it was created on", m.pendingCreatedHost)
	}

	// Now a LOCAL worktree finishes launching — the globalWorkspaceLaunchedMsg
	// path, which is where the local flow ends.
	record := &workspaceops.WorktreeRecord{Path: "/tmp/sidecar-local", Name: "local", Branch: "local"}
	m.results["sidecar"] = workspaceinventory.ProjectResult{ProjectKey: "sidecar", Workspaces: []workspaceinventory.Workspace{
		{ID: "local-row", ProjectKey: "sidecar", ProjectName: "sidecar", Kind: workspaceinventory.KindWorktree, Name: "local", Path: "/tmp/sidecar-local"},
	}}
	m.showIdleWorktrees = true
	m.syncBoard()
	m.syncWorkspaces()
	m.Update(globalWorkspaceLaunchedMsg{Project: m.projects[0], Record: record})

	if m.pendingCreatedHost != "" {
		t.Fatalf("a local creation inherited host %q", m.pendingCreatedHost)
	}
	if !m.honorPendingCreated() {
		t.Fatal("the local worktree was never selected after a remote creation")
	}
	if got := m.workspaces.SelectedID(); got != "local-row" {
		t.Fatalf("selected %q, want the local row", got)
	}
	if m.pendingCreatedTmux != "" || m.pendingCreatedPath != "" {
		t.Fatalf("pending selection never cleared: tmux=%q path=%q", m.pendingCreatedTmux, m.pendingCreatedPath)
	}
}

// A CLI-driven create is on this machine by construction — its request file is
// in this machine's state tree — so it must SET the host to "" rather than
// inherit whatever the last remote create left.
func TestCLICreateClearsTheRemoteHostScope(t *testing.T) {
	focus := true
	for _, tc := range []struct {
		name  string
		apply func(*Model)
	}{
		{"shell", func(m *Model) {
			m.applyCreateShellRequest(uirequest.Request{}, uirequest.CreatePayload{
				Kind: uirequest.CreateKindShell, Session: "sidecar-sh-sidecar-9",
				DisplayName: "New", Focus: &focus,
			}, m.projects[0], "sidecar")
		}},
		{"worktree", func(m *Model) {
			m.applyCreateWorktreeRequest(uirequest.Request{}, uirequest.CreatePayload{
				Kind: uirequest.CreateKindWorktree, Path: "/tmp/sidecar-cli",
				DisplayName: "cli", Focus: &focus,
			}, m.projects[0], "sidecar")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := remoteCreateModel(t)
			m.pendingCreatedHost = "mac-mini"
			tc.apply(m)
			if m.pendingCreatedHost != "" {
				t.Fatalf("a CLI create inherited host %q", m.pendingCreatedHost)
			}
		})
	}
}

// TestDropRemoteReplyLeavesAnUnrelatedFormAlone. Removing a host and starting
// another create is an ordinary sequence, and a late answer from the removed
// machine used to write "mac-mini was removed…" onto whatever form happened to
// be open — including a purely local one the user had just started.
func TestDropRemoteReplyLeavesAnUnrelatedFormAlone(t *testing.T) {
	m, _ := remoteCreateModel(t)
	// A remote create is in flight — createTargetHost records who asked — and
	// the user has since opened a create form on a LOCAL project.
	m.createTargetHost = "mac-mini"
	m.pendingCreatedHost = "mac-mini"
	m.pendingCreatedTmux = "api-2"
	run(t, m, m.OpenCreateShell("sidecar"))
	m.createError = ""

	if cmd := m.dropRemoteCreateReply("mac-mini"); cmd != nil {
		t.Fatal("dropping a reply returned a command")
	}
	if m.createError != "" {
		t.Fatalf("an unrelated form was given another machine's error: %q", m.createError)
	}
	if !m.CreateOpen() {
		t.Fatal("the unrelated form was closed")
	}
	// The pending selection is dropped either way: it named a row on a machine
	// this configuration no longer points at.
	if m.pendingCreatedHost != "" || m.pendingCreatedTmux != "" {
		t.Errorf("a dropped reply left a selection queued: %q on %q", m.pendingCreatedTmux, m.pendingCreatedHost)
	}

	// The form that DID ask still hears about it, including once the host has
	// been removed from configuration and no longer resolves at all.
	m2, _ := remoteCreateModel(t)
	run(t, m2, m2.OpenCreateShell(remoteProjectKey()))
	m2.createTargetHost = "mac-mini"
	m2.createBusy = true
	m2.hostProjects = map[string][]Project{}
	m2.dropRemoteCreateReply("mac-mini")
	if !strings.Contains(m2.createError, "mac-mini") {
		t.Fatalf("the form that asked was left spinning: %q", m2.createError)
	}
	if m2.createBusy {
		t.Error("the form that asked is still busy")
	}
}

// TestDisabledHostRefusesMutationsUpFront. A disabled host keeps its last-known
// rows and projects on screen on purpose, and it sits in exactly the state
// hostReplyStale reads as stale: registered, with no incarnation. Dispatching a
// mutation to it therefore failed on ssh and then reported the misleading "was
// removed or retargeted while that was running". The refusal has to happen
// before anything is sent, with the real reason.
func TestDisabledHostRefusesMutationsUpFront(t *testing.T) {
	m, stub := remoteCreateModel(t)
	localSeamGuard(t)
	m.hostRegistered = map[string]bool{"mac-mini": true}
	m.hostIncarnations = map[string]uint64{}
	m.hostHealth["mac-mini"] = hosts.Health{State: hosts.StateDisabled}

	// Create shell.
	run(t, m, m.OpenCreateShell(remoteProjectKey()))
	if cmd := m.submitCreateShell(); cmd != nil {
		t.Fatal("a create was dispatched to a disabled host")
	}
	if !strings.Contains(m.createError, "mac-mini") || !strings.Contains(m.createError, "disabled or not connected") {
		t.Errorf("create error %q does not say the host is disabled", m.createError)
	}

	// Plan worktree.
	run(t, m, m.OpenCreateWorktree(remoteProjectKey()))
	if cmd := m.planCreateWorktree(); cmd != nil {
		t.Fatal("a plan was dispatched to a disabled host")
	}
	if !strings.Contains(m.createError, "disabled or not connected") {
		t.Errorf("plan error %q does not say the host is disabled", m.createError)
	}

	// Execute a previously confirmed plan.
	m.createPlan = &workspaceops.WorktreePlan{Branch: "f", Path: "/home/me/api-f", SourceOID: "abc"}
	m.createTargetHost = "mac-mini"
	m.createBusy = true
	if cmd := m.executeCreateWorktree(); cmd != nil {
		t.Fatal("a confirmed create was dispatched to a disabled host")
	}
	if m.createBusy {
		t.Error("the confirmation is still spinning on a disabled host")
	}
	if !strings.Contains(m.createError, "disabled or not connected") {
		t.Errorf("execute error %q does not say the host is disabled", m.createError)
	}

	// Rename.
	m.closeCreateShell()
	if !m.workspaces.SelectID(remoteShellRowID()) {
		t.Fatal("could not select the remote row")
	}
	cmd := m.OpenRenameShell()
	if m.RenameShellOpen() {
		t.Fatal("the rename modal opened for a disabled host's row")
	}
	if cmd == nil {
		t.Fatal("the refusal was silent")
	}
	post, refused := cmd().(notify.PostMsg)
	if !refused || !strings.Contains(post.Notification.Title, "disabled or not connected") {
		t.Fatalf("rename refusal = %+v", post.Notification)
	}

	if len(stub.calls) != 0 {
		t.Fatalf("a disabled host was invoked: %v", stub.calls)
	}

	// And a connected host — registered, with an incarnation — is not refused.
	m.hostIncarnations = map[string]uint64{"mac-mini": 3}
	run(t, m, m.OpenCreateShell(remoteProjectKey()))
	if cmd := m.submitCreateShell(); cmd == nil {
		t.Fatalf("a connected host was refused: %q", m.createError)
	}
}

// TestVanishedHostIsNamedOnPlanAndSubmit. The user did choose a project; what
// vanished is the machine it lived on. "Choose a project" blamed the choice.
func TestVanishedHostIsNamedOnPlanAndSubmit(t *testing.T) {
	m, _ := remoteCreateModel(t)
	run(t, m, m.OpenCreateWorktree(remoteProjectKey()))
	m.hostProjects = map[string][]Project{}
	if cmd := m.planCreateWorktree(); cmd != nil {
		t.Fatal("a plan with no resolvable target still dispatched work")
	}
	if !strings.Contains(m.createError, "mac-mini") {
		t.Errorf("plan error %q does not name the machine that went away", m.createError)
	}

	m2, _ := remoteCreateModel(t)
	run(t, m2, m2.OpenCreateShell(remoteProjectKey()))
	m2.hostProjects = map[string][]Project{}
	if cmd := m2.submitCreateShell(); cmd != nil {
		t.Fatal("a create with no resolvable target still dispatched work")
	}
	if !strings.Contains(m2.createError, "mac-mini") {
		t.Errorf("create error %q does not name the machine that went away", m2.createError)
	}
}

// TestDropRemoteReplyKeepsAnotherHostsPendingSelection. The pending selection
// names the machine it is waiting on; a stale reply from host B must not wipe
// the selection for a shell just created on host A — or locally.
func TestDropRemoteReplyKeepsAnotherHostsPendingSelection(t *testing.T) {
	m, _ := remoteCreateModel(t)
	m.pendingCreatedHost = "studio"
	m.pendingCreatedTmux = "api-2"
	m.dropRemoteCreateReply("mac-mini")
	if m.pendingCreatedHost != "studio" || m.pendingCreatedTmux != "api-2" {
		t.Fatalf("another host's pending selection was wiped: %q on %q",
			m.pendingCreatedTmux, m.pendingCreatedHost)
	}

	m.pendingCreatedHost = ""
	m.pendingCreatedTmux = ""
	m.pendingCreatedPath = "/tmp/sidecar-local"
	m.dropRemoteCreateReply("mac-mini")
	if m.pendingCreatedPath != "/tmp/sidecar-local" {
		t.Fatal("a local pending selection was wiped by a remote host's stale reply")
	}

	// The host the reply actually came from still loses its pending selection.
	m.pendingCreatedHost = "mac-mini"
	m.pendingCreatedTmux = "api-9"
	m.pendingCreatedPath = ""
	m.dropRemoteCreateReply("mac-mini")
	if m.pendingCreatedHost != "" || m.pendingCreatedTmux != "" {
		t.Fatalf("the addressed host's pending selection survived: %q on %q",
			m.pendingCreatedTmux, m.pendingCreatedHost)
	}
}

// TestExecuteCreateWorktreeSaysSoWhenTheHostIsGone. Every other host-removal
// path in this area says something; this one answered the Create keypress with
// nil, which from the user's side is indistinguishable from a hang.
func TestExecuteCreateWorktreeSaysSoWhenTheHostIsGone(t *testing.T) {
	m, _ := remoteCreateModel(t)
	run(t, m, m.OpenCreateWorktree(remoteProjectKey()))
	m.createPlan = &workspaceops.WorktreePlan{Branch: "feature", Path: "/home/me/api-feature"}
	m.createTargetHost = "mac-mini"
	m.createBusy = true
	// The host goes away between the plan and the confirmation.
	m.hostProjects = map[string][]Project{}

	if cmd := m.executeCreateWorktree(); cmd != nil {
		t.Fatal("a create with no resolvable target still dispatched work")
	}
	if m.createError == "" {
		t.Fatal("the Create press was answered with nothing at all")
	}
	if !strings.Contains(m.createError, "mac-mini") {
		t.Errorf("error %q does not name the machine that went away", m.createError)
	}
	if m.createBusy {
		t.Error("the modal is still spinning on a host that is gone")
	}
}

// TestCreatePickersAnswerNothingForARemoteRow. localSelectedRoot returns "" for
// both "nothing selected" and "selected elsewhere", and this caller had a local
// fallback for the first — so a remote row filled the diff, issue and note
// pickers with THIS machine's commits and issues while the form targeted a host.
// Its two siblings already answer nothing; so must this.
func TestCreatePickersAnswerNothingFromThisMachineForARemoteRow(t *testing.T) {
	m, stub := remoteCreateModel(t)
	if !m.workspaces.SelectID(remoteShellRowID()) {
		t.Fatalf("could not select the remote row")
	}
	run(t, m, m.OpenCreate(""))
	if m.createForm == nil {
		t.Fatal("create form did not open")
	}
	m.createForm.SetKind(workspacecreate.KindFile)
	m.createForm.AdvanceToTarget()
	cmd := m.loadCreatePickerData()
	if cmd == nil {
		t.Fatal("remote picker catalog was not requested")
	}
	if m.loadCreateFileCandidates() != nil {
		t.Error("the file picker was populated from this machine for a remote row")
	}
	run(t, m, cmd)
	if len(stub.calls) != 1 || !strings.Contains(strings.Join(stub.argv(t, 0), " "), "content catalog") {
		t.Fatalf("expected a host catalog invocation, got %v", stub.calls)
	}
}

// TestRemoteActionErrorKeepsTheActionableHalf. The host's own sentence and the
// Fix() that follows it are one message, and the modal is where the second half
// was being lost. Both halves must be in the string the surface stores, and the
// error section must wrap rather than let the modal cut it.
func TestRemoteActionErrorKeepsTheActionableHalf(t *testing.T) {
	err := &hosts.RunError{
		Failure: hosts.FailNoTarget, HostID: "mac-mini",
		Args: []string{"shell", "rename", "--target", "x"}, ExitCode: 3,
		Detail: `no registered Sidecar shell or worktree session named "x"`,
	}
	message := remoteActionError(err)
	for _, want := range []string{"no registered Sidecar shell", "refresh that host's workspaces"} {
		if !strings.Contains(message, want) {
			t.Errorf("message %q is missing %q", message, want)
		}
	}

	// A rejected value must not be dressed up as version skew.
	rejected := &hosts.RunError{
		Failure: hosts.FailRejected, HostID: "mac-mini",
		Args: []string{"create", "worktree"}, ExitCode: 5,
		Detail: `branch "phase-c-wt" already exists`,
	}
	got := remoteActionError(rejected)
	if !strings.Contains(got, "already exists") {
		t.Errorf("message %q lost the host's own words", got)
	}
	if strings.Contains(got, "update Sidecar") {
		t.Errorf("a rejected value was reported as version skew: %q", got)
	}
}
