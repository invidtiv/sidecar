package workspaceops

import (
	"os"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentsession"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/testenv"
	"github.com/marcus/sidecar/internal/tmuxenv"
)

// The session lives on the throwaway server TestMain pins, like every other
// tmux test in this package. It used to build its own TMUX_TMPDIR under a
// fixed /tmp path and never created that directory: tmux does not mkdir a
// missing parent for an explicit -S socket, and it reports that failure while
// still exiting 0, so the fixture looked like it had worked. No server existed
// on the path it named, and the ambient `tmux display-message` inside
// ServerPID then resolved to whatever server the environment did have — on a
// developer's machine, their real default one, which is how this passed
// locally while asserting against live sessions it must never touch. A bare
// runner has no such server, so the marker came back nil and CI failed
// (td-b6edab).
func TestRecordRecoverableSessionUsesSeparateEligibleManifest(t *testing.T) {
	stateDir := t.TempDir()
	config.SetTestStateDir(stateDir)
	t.Cleanup(config.ResetTestStateDir)
	workDir := t.TempDir()
	startThrowawaySession(t, "sidecar-ws-topic", workDir)
	if err := RecordRecoverableSession("sidecar-ws-topic", workDir, "Topic", "codex"); err != nil {
		t.Fatal(err)
	}
	defs, err := shellstate.ListAtPath(RecoverySessionsPath(stateDir))
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 1 {
		t.Fatalf("definitions = %+v, want one", defs)
	}
	got := defs[0]
	if got.TmuxName != "sidecar-ws-topic" || got.WorkDir != workDir || got.AgentType != "codex" || got.Namespace != tmuxenv.Namespace() {
		t.Fatalf("definition = %+v", got)
	}
	if got.Restore == nil || !got.Restore.Eligible || got.Restore.LastSeenServer == "" {
		t.Fatalf("restore marker = %+v, want eligible current server", got.Restore)
	}
	currentServer := got.Restore.LastSeenServer
	// The marker must name the throwaway server, not whichever server the
	// developer happens to be running. This is the assertion the old fixture
	// could not make, and the one that keeps the test honest about isolation.
	isolated, err := tmuxTest(t, "-S", testenv.SocketPath(os.Getenv("TMUX_TMPDIR")), "display-message", "-p", "#{pid}")
	if err != nil {
		t.Fatalf("read the isolated server pid: %v (%s)", err, isolated)
	}
	if want := "pid=" + isolated; currentServer != want {
		t.Fatalf("marker names server %q, want the isolated server %q", currentServer, want)
	}
	got.Restore.LastSeenServer = "pid=stale"
	got.Restore.ServerLostAt = time.Now().Add(-time.Minute)
	got.Restore.PrefillClaimedAt = time.Now().Add(-30 * time.Second)
	got.Agent = &shellstate.AgentBinding{Kind: "codex", Candidate: &agentsession.Candidate{
		Ref: agentsession.Ref{Kind: agentsession.RefID, Value: "stale-candidate"},
	}}
	if err := shellstate.AddAtPath(RecoverySessionsPath(stateDir), got); err != nil {
		t.Fatal(err)
	}
	if err := recordRecoverableSession(got.TmuxName, workDir, "Topic", "codex", true); err != nil {
		t.Fatal(err)
	}
	defs, err = shellstate.ListAtPath(RecoverySessionsPath(stateDir))
	if err != nil {
		t.Fatal(err)
	}
	if defs[0].Agent == nil || defs[0].Agent.Candidate == nil || defs[0].Restore.ServerLostAt.IsZero() {
		t.Fatalf("restore creation discarded incident evidence: %+v", defs[0])
	}
	if err := RecordRecoverableSession(got.TmuxName, workDir, "Topic", "codex"); err != nil {
		t.Fatal(err)
	}
	defs, err = shellstate.ListAtPath(RecoverySessionsPath(stateDir))
	if err != nil {
		t.Fatal(err)
	}
	got = defs[0]
	if got.Restore.ServerLostAt.IsZero() == false || !got.Restore.PrefillClaimedAt.IsZero() || got.Agent.Candidate != nil {
		t.Fatalf("ordinary recreation retained stale incident state: %+v", got)
	}
	if got.Restore.LastSeenServer != currentServer {
		t.Fatalf("ordinary recreation server = %q, want current %q", got.Restore.LastSeenServer, currentServer)
	}
	if err := ForgetRecoverableSession(got.TmuxName); err != nil {
		t.Fatal(err)
	}
	defs, err = shellstate.ListAtPath(RecoverySessionsPath(stateDir))
	if err != nil || len(defs) != 0 {
		t.Fatalf("after forget definitions=%+v err=%v", defs, err)
	}
}
