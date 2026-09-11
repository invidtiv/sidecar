package termpanes

import (
	"os"
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/testenv"
	"github.com/marcus/sidecar/internal/workspaceops"
)

func TestMain(m *testing.M) { os.Exit(testenv.Main(m)) }

func TestEnsureSessionAllocatesAvailableRecoveryNames(t *testing.T) {
	testenv.RequireTmux(t)
	stateDir := t.TempDir()
	config.SetTestStateDir(stateDir)
	t.Cleanup(config.ResetTestStateDir)
	workDir := t.TempDir()
	sessions := []string{SessionName("first"), SessionName("second"), SessionName("another-project")}
	panes := make(map[string]string)
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
		if again, err := EnsureSession(session, dir); err != nil || again != pane {
			t.Fatalf("reopen split %s: pane=%q err=%v, want %q", session, again, err, pane)
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
