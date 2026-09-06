package workspaceops

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentsession"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/tmuxenv"
)

func TestRecordRecoverableSessionUsesSeparateEligibleManifest(t *testing.T) {
	stateDir := t.TempDir()
	config.SetTestStateDir(stateDir)
	t.Cleanup(config.ResetTestStateDir)
	workDir := t.TempDir()
	tmuxDir := filepath.Join("/tmp", "sc-recovery-session-test")
	socket := filepath.Join(tmuxDir, "tmux-"+fmt.Sprint(os.Getuid()), "default")
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TMUX_TMPDIR", tmuxDir)
	_ = exec.Command("tmux", "-S", socket, "kill-server").Run()
	if out, err := exec.Command("tmux", "-S", socket, "new-session", "-d", "-s", "sidecar-ws-topic", "-c", workDir).CombinedOutput(); err != nil {
		t.Fatalf("create isolated worktree session: %v (%s)", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", socket, "kill-server").Run() })
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
