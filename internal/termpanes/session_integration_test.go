package termpanes

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/testenv"
	"github.com/marcus/sidecar/internal/workspaceops"
)

func TestMain(m *testing.M) { os.Exit(testenv.Main(m)) }

func TestEnsureSessionAllocatesAvailableRecoveryNames(t *testing.T) {
	testenv.RequireTmux(t)
	t.Setenv("COMMS_SESSION", "stale-orchestrator")
	stateDir := t.TempDir()
	config.SetTestStateDir(stateDir)
	t.Cleanup(config.ResetTestStateDir)
	workDir := t.TempDir()
	sessions := []string{SessionName("first"), SessionName("second"), SessionName("another-project")}
	panes := make(map[string]string)
	commsIdentities := make(map[string]bool)
	for i, session := range sessions {
		dir := workDir
		if i == 2 {
			dir = t.TempDir()
		}
		pane, err := EnsureSession(session, dir)
		if err != nil || pane == "" {
			t.Fatalf("open split %s: pane=%q err=%v", session, pane, err)
		}
		panes[session] = pane
		t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", "="+session).Run() })
		out, err := exec.Command("tmux", "show-environment", "-t", session, "COMMS_SESSION").CombinedOutput()
		identity := strings.TrimSpace(strings.TrimPrefix(string(out), "COMMS_SESSION="))
		if err != nil || !strings.HasPrefix(identity, "sidecar:") || commsIdentities[identity] {
			t.Fatalf("split %s comms identity=%q err=%v, want its own comms identity", session, identity, err)
		}
		commsIdentities[identity] = true
		for _, key := range []string{shellstate.SessionEnv, shellstate.ManagedEnv} {
			if value, err := exec.Command("tmux", "show-environment", "-t", session, key).Output(); err == nil {
				t.Fatalf("split %s claims project-managed identity %q", session, value)
			}
		}
		if again, err := EnsureSession(session, dir); err != nil || again != pane {
			t.Fatalf("reopen split %s: pane=%q err=%v, want %q", session, again, err, pane)
		}
		again, err := exec.Command("tmux", "show-environment", "-t", session, "COMMS_SESSION").Output()
		if err != nil || strings.TrimSpace(string(again)) != "COMMS_SESSION="+identity {
			t.Fatalf("reopening split %s changed its comms identity: %q %v", session, again, err)
		}
	}
	defs, err := shellstate.ListAtPath(workspaceops.RecoverySessionsPath(stateDir))
	if err != nil || len(defs) != len(sessions) {
		t.Fatalf("recovery definitions=%+v err=%v", defs, err)
	}
	for i, want := range []string{"Terminal", "Terminal 2", "Terminal 3"} {
		if defs[i].DisplayName != want || defs[i].Restore == nil || !defs[i].Restore.Eligible {
			t.Errorf("recovery definition=%+v, want eligible %q", defs[i], want)
		}
		if got := workspaceops.PaneID(sessions[i]); got != panes[sessions[i]] {
			t.Errorf("opening another split replaced %s: got %q", sessions[i], got)
		}
	}
}
