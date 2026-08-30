package overview

import (
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// noLocalShellDelete fails the test if the LOCAL delete seam runs. A remote
// delete that reached workspaceops.DeleteManagedShell would resolve the row's
// shells.json against THIS machine's state tree — and on two machines with the
// same project layout that does not fail, it deletes the wrong shell.
func noLocalShellDelete(t *testing.T) {
	t.Helper()
	original := deleteManagedShell
	t.Cleanup(func() { deleteManagedShell = original })
	deleteManagedShell = func(projectRoot, session, namespace string) error {
		t.Errorf("a remote delete ran the LOCAL deleteManagedShell(%q, %q, %q)", projectRoot, session, namespace)
		return nil
	}
}

// TestRemoteShellDeleteRunsOnItsHost is workstream B end to end from this side:
// the confirmation opens, the host is asked to run its own `sidecar shell
// delete`, and the row goes when the host says it did.
func TestRemoteShellDeleteRunsOnItsHost(t *testing.T) {
	m, stub := remoteCreateModel(t)
	localSeamGuard(t)
	noLocalShellDelete(t)
	stub.results = []any{map[string]any{"shell": "api-claude", "status": "deleted", "deleted": true}}
	m.workspaces.SelectID(remoteShellRowID())

	if cmd := m.OpenDeleteSelectedShell(); cmd != nil {
		t.Fatalf("deleting a remote shell was refused: %v", cmd())
	}
	if !m.DeleteOpen() {
		t.Fatal("the confirmation did not open for a remote shell")
	}

	cmd := m.applyDeleteAction(globalDeleteConfirmID)
	if cmd == nil {
		t.Fatalf("confirming dispatched nothing; error=%q", m.deleteError)
	}
	if !m.deleteBusy {
		t.Error("the confirmation does not show that it is waiting on the host")
	}
	done, ok := cmd().(globalShellDeletedMsg)
	if !ok {
		t.Fatalf("the delete replied with %T", cmd())
	}
	if done.Err != nil {
		t.Fatalf("remote delete failed: %v", done.Err)
	}
	want := []string{"shell", "delete", "--target", "api-claude", "--project", "/home/me/api", "--json"}
	if got := stub.argv(t, 0); !equalArgs(got, want) {
		t.Fatalf("argv = %v, want %v", got, want)
	}
	if stub.calls[0].HostID != "mac-mini" {
		t.Errorf("addressed host = %q", stub.calls[0].HostID)
	}
	// The reply carries the host's incarnation and no path: nothing local may
	// be resolved from a message about another machine.
	if done.HostID != "mac-mini" {
		t.Errorf("reply host = %q", done.HostID)
	}
	if done.Project.Path != "" {
		t.Errorf("the reply carries a remote path: %q", done.Project.Path)
	}

	m.Update(done)
	if m.DeleteOpen() {
		t.Error("a successful remote delete left the confirmation open")
	}
	if _, still := m.catalog[remoteShellRowID()]; still {
		t.Error("the row survived a confirmed delete; the user has to wait for the host to say so again")
	}
}

// TestRemoteShellDeleteNamesTheHost. Deleting something on another machine
// should read as such — the tmux session being closed is not the one in front
// of the user.
func TestRemoteShellDeleteNamesTheHost(t *testing.T) {
	remote := workspaceinventory.Workspace{
		ID: "h\x1fx", HostID: "mac-mini", Name: "Claude pane", Kind: workspaceinventory.KindShell,
	}
	prompt := deleteShellPrompt(remote)
	if !strings.Contains(prompt, "mac-mini") {
		t.Fatalf("the confirmation does not name the host: %q", prompt)
	}
	if !strings.Contains(prompt, "Claude pane") {
		t.Fatalf("the confirmation does not name the shell: %q", prompt)
	}
	local := workspaceinventory.Workspace{ID: "x", Name: "Shell 3", Kind: workspaceinventory.KindShell}
	if strings.Contains(deleteShellPrompt(local), " on ") {
		t.Fatalf("a local confirmation invented a host: %q", deleteShellPrompt(local))
	}
}

// TestRemoteShellDeleteFailureKeepsTheConfirmation. The row is dropped only on
// an answer that says the host did it. A failure leaves the row and says what
// the host said, in the confirmation the user is still looking at.
func TestRemoteShellDeleteFailureKeepsTheConfirmation(t *testing.T) {
	m, stub := remoteCreateModel(t)
	localSeamGuard(t)
	noLocalShellDelete(t)
	stub.errs = []error{&hosts.RunError{
		Failure: hosts.FailUnavailable, HostID: "mac-mini", ExitCode: -1,
		Detail: "ssh: connect to host mac-mini port 22: No route to host",
	}}
	m.workspaces.SelectID(remoteShellRowID())
	m.OpenDeleteSelectedShell()

	cmd := m.applyDeleteAction(globalDeleteConfirmID)
	if cmd == nil {
		t.Fatal("confirming dispatched nothing")
	}
	done := cmd().(globalShellDeletedMsg)
	if done.Err == nil {
		t.Fatal("an unreachable host reported success")
	}
	m.Update(done)
	if !m.DeleteOpen() {
		t.Fatal("a failed delete closed the confirmation, losing the reason")
	}
	if m.deleteBusy {
		t.Error("the confirmation is still waiting on a round trip that is over")
	}
	if !strings.Contains(m.deleteError, "No route to host") {
		t.Errorf("the confirmation does not say what the host said: %q", m.deleteError)
	}
	if _, still := m.catalog[remoteShellRowID()]; !still {
		t.Error("a failed delete dropped the row anyway")
	}
}

// TestRemoteShellDeleteRefusesADisabledHost. A host that is registered but has
// no live client cannot be asked anything. Sending anyway produces an ssh
// failure whose reply reads as "removed or retargeted", which is the wrong
// sentence for a host the user disabled.
func TestRemoteShellDeleteRefusesADisabledHost(t *testing.T) {
	m, stub := remoteCreateModel(t)
	localSeamGuard(t)
	noLocalShellDelete(t)
	m.hostRegistered = map[string]bool{"mac-mini": true}
	m.hostIncarnations = map[string]uint64{}
	m.workspaces.SelectID(remoteShellRowID())
	m.OpenDeleteSelectedShell()

	cmd := m.applyDeleteAction(globalDeleteConfirmID)
	if cmd == nil {
		t.Fatal("confirming dispatched nothing")
	}
	done := cmd().(globalShellDeletedMsg)
	if len(stub.calls) != 0 {
		t.Fatalf("a disabled host was still asked: %v", stub.calls)
	}
	if done.Err == nil || !strings.Contains(done.Err.Error(), "disabled or not connected") {
		t.Fatalf("refusal = %v", done.Err)
	}
	m.Update(done)
	if !m.DeleteOpen() || m.deleteBusy {
		t.Errorf("the confirmation is not showing the refusal: open=%v busy=%v", m.DeleteOpen(), m.deleteBusy)
	}
}

// confirmRemoteDelete opens the confirmation for the one remote shell row and
// runs it, returning the host's answer without applying it. The host is
// registered with a known incarnation so a test can move it afterwards, which is
// what retargeting or removing it in Configuration does.
func confirmRemoteDelete(t *testing.T, m *Model) globalShellDeletedMsg {
	t.Helper()
	m.hostRegistered = map[string]bool{"mac-mini": true}
	m.hostIncarnations = map[string]uint64{"mac-mini": 7}
	m.workspaces.SelectID(remoteShellRowID())
	m.OpenDeleteSelectedShell()
	cmd := m.applyDeleteAction(globalDeleteConfirmID)
	if cmd == nil {
		t.Fatalf("confirming dispatched nothing; error=%q", m.deleteError)
	}
	done, ok := cmd().(globalShellDeletedMsg)
	if !ok {
		t.Fatalf("the delete replied with %T", cmd())
	}
	if done.Incarnation != 7 {
		t.Fatalf("the reply carries incarnation %d; the fence has nothing to check", done.Incarnation)
	}
	return done
}

// TestRemoteDeleteReplyIsDroppedWhenTheHostWasRetargeted. remoteQuickTimeout is
// 30 seconds, and a user can retarget a host in Configuration inside it —
// SyncHosts bumps the incarnation. The answer then describes the PREVIOUS
// machine, and applying it drops a row belonging to the current one, with the
// correcting snapshot coming from a host this configuration no longer watches.
// Every sibling remote reply opens with this fence; delete carried one and
// checked it nowhere.
func TestRemoteDeleteReplyIsDroppedWhenTheHostWasRetargeted(t *testing.T) {
	m, stub := remoteCreateModel(t)
	localSeamGuard(t)
	noLocalShellDelete(t)
	stub.results = []any{map[string]any{"shell": "api-claude", "status": "deleted", "deleted": true}}

	done := confirmRemoteDelete(t, m)
	if done.Err != nil {
		t.Fatalf("remote delete failed: %v", done.Err)
	}
	// Retargeted while the answer was in flight.
	m.hostIncarnations["mac-mini"] = 8

	m.Update(done)
	if _, still := m.catalog[remoteShellRowID()]; !still {
		t.Error("an answer from the previous machine dropped a row belonging to the current one")
	}
	if !m.DeleteOpen() {
		t.Fatal("the confirmation closed on an answer that was thrown away, so the user was told nothing")
	}
	if m.deleteBusy {
		t.Error("the confirmation is still waiting on a round trip that is over")
	}
	if !strings.Contains(m.deleteError, "removed or retargeted") {
		t.Errorf("the confirmation does not say why the answer was dropped: %q", m.deleteError)
	}
}

// TestRemoteDeleteFailureFromARemovedHostSaysWhatHappened. On the failure branch
// the unfenced handler showed the raw ssh error, which describes a machine this
// configuration no longer points at rather than the thing the user just did.
func TestRemoteDeleteFailureFromARemovedHostSaysWhatHappened(t *testing.T) {
	m, stub := remoteCreateModel(t)
	localSeamGuard(t)
	noLocalShellDelete(t)
	stub.errs = []error{&hosts.RunError{
		Failure: hosts.FailUnavailable, HostID: "mac-mini", ExitCode: -1,
		Detail: "ssh: connect to host mac-mini port 22: No route to host",
	}}

	done := confirmRemoteDelete(t, m)
	if done.Err == nil {
		t.Fatal("an unreachable host reported success")
	}
	// Removed from configuration while the answer was in flight.
	m.hostRegistered = map[string]bool{}
	delete(m.hostIncarnations, "mac-mini")

	m.Update(done)
	if !m.DeleteOpen() || m.deleteBusy {
		t.Fatalf("the confirmation is not showing the outcome: open=%v busy=%v", m.DeleteOpen(), m.deleteBusy)
	}
	if strings.Contains(m.deleteError, "No route to host") {
		t.Errorf("the user reads the removed host's ssh error rather than what happened: %q", m.deleteError)
	}
	if !strings.Contains(m.deleteError, "removed or retargeted") {
		t.Errorf("the confirmation does not say why the answer was dropped: %q", m.deleteError)
	}
}

// TestRemoteDeleteReplyForAnotherRowLeavesTheConfirmationAlone. A dropped answer
// un-sticks only the confirmation that asked for it. A user who cancelled and
// started deleting something else has a round trip of their own in flight, and
// clearing its busy flag would let a second confirm land on top of it.
func TestRemoteDeleteReplyForAnotherRowLeavesTheConfirmationAlone(t *testing.T) {
	m, _ := remoteCreateModel(t)
	m.hostRegistered = map[string]bool{"mac-mini": true}
	m.hostIncarnations = map[string]uint64{"mac-mini": 7}
	m.workspaces.SelectID(remoteShellRowID())
	m.OpenDeleteSelectedShell()
	m.deleteBusy = true

	m.Update(globalShellDeletedMsg{
		remoteReply: remoteReply{HostID: "mac-mini", Incarnation: 1},
		WorkspaceID: "some-other-row",
	})
	if !m.deleteBusy {
		t.Error("a stale answer about another row un-stuck a delete that is still running")
	}
	if m.deleteError != "" {
		t.Errorf("a stale answer about another row wrote into an unrelated confirmation: %q", m.deleteError)
	}
}

// TestRemoteWorktreeDeleteStaysRefused. The gate is per-kind, and widening it
// for shells must not widen it for worktrees: removing a checkout resolves a
// path against a git repository here and carries branch-cleanup decisions the
// host verb cannot express.
func TestRemoteWorktreeDeleteStaysRefused(t *testing.T) {
	shell := workspaceinventory.Workspace{
		ID: "h\x1fs", HostID: "mac-mini", Name: "Claude pane", Kind: workspaceinventory.KindShell,
	}
	worktree := workspaceinventory.Workspace{
		ID: "h\x1fw", HostID: "mac-mini", Name: "fix-auth", Kind: workspaceinventory.KindWorktree,
		Path: "/home/me/api-fix",
	}
	if reason := remoteActionRefusal(shell, "delete"); reason != "" {
		t.Fatalf("deleting a remote shell is a host-side verb but was refused: %q", reason)
	}
	for _, verb := range []string{"delete", "merge", "open"} {
		reason := remoteActionRefusal(worktree, verb)
		if reason == "" {
			t.Fatalf("%s was permitted on a remote worktree", verb)
		}
		if !strings.Contains(reason, "mac-mini") || !strings.Contains(reason, "worktree") {
			t.Errorf("%s refusal %q does not say which machine and which kind", verb, reason)
		}
	}
	// And the shared gate the worktree confirmation consults still refuses it,
	// so the footer never offers what the confirmation would take back.
	if reason := deleteRefusal(worktree); !strings.Contains(reason, "mac-mini") {
		t.Errorf("deleteRefusal admitted a remote worktree: %q", reason)
	}
	// open and merge are refused for a remote SHELL too — for them the kind was
	// never the question, and shell delete must not have answered it for them.
	for _, verb := range []string{"merge", "open"} {
		if reason := remoteActionRefusal(shell, verb); reason == "" {
			t.Errorf("%s was permitted on a remote shell", verb)
		}
	}
}

// TestRemoteResultDiscriminatorRejectsALogLine. A host whose login profile
// writes JSON to stdout once had a log line accepted as a verb's result, with a
// nil error and an all-zero value. On delete that would report a shell as
// removed and drop its row while the shell was still running.
func TestRemoteResultDiscriminatorRejectsALogLine(t *testing.T) {
	if (remoteShellDeleteResult{}).ValidRemoteResult() {
		t.Error("an all-zero value was accepted as a delete result")
	}
	if (remoteShellDeleteResult{Shell: "api-claude"}).ValidRemoteResult() {
		t.Error("an object carrying only a shell key was accepted as a delete result")
	}
	if !(remoteShellDeleteResult{Shell: "api-claude", Status: "deleted"}).ValidRemoteResult() {
		t.Error("a real delete result was rejected")
	}
	// A status this verb does not write is not this verb's answer. "Non-empty"
	// would read a future partial failure — session killed, record not
	// tombstoned — as success and optimistically drop the row for a shell whose
	// identity is still on the host.
	if (remoteShellDeleteResult{Shell: "api-claude", Status: "partial"}).ValidRemoteResult() {
		t.Error("a status `shell delete` never writes was accepted as a successful delete")
	}
}

// TestRemoteProjectIsNeverInventoriedLocally. Every remote mutation reply lands
// in a handler shared with the local one, and those handlers end in a local
// re-inventory of the project they name. A host-scoped key must not reach it:
// on two machines with the same checkout layout that call does not fail, it
// answers with the wrong repository.
func TestRemoteProjectIsNeverInventoriedLocally(t *testing.T) {
	m, _ := remoteCreateModel(t)
	remote := Project{Name: "api", Key: hosts.ScopedKey("mac-mini", "/home/me/api"), Path: "/home/me/api"}
	if cmd := m.refreshProjectAfterMutation(remote); cmd != nil {
		t.Fatal("a remote project was queued for a local inventory")
	}
	local := Project{Name: "sidecar", Path: "/tmp/sidecar", Key: "sidecar"}
	if cmd := m.refreshProjectAfterMutation(local); cmd == nil {
		t.Fatal("a local project stopped being refreshed after a mutation")
	}
}
