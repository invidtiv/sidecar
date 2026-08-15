package app

import (
	"os"
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/uirequest"
)

func TestAnnounceInstanceCmdWritesPresence(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")

	workDir := t.TempDir()
	if _, err := projectdir.Resolve(workDir); err != nil {
		t.Fatal(err)
	}

	cmd := announceInstanceCmd(workDir, workDir)
	if cmd == nil {
		t.Fatal("expected announce cmd")
	}
	if msg := cmd(); msg != nil {
		t.Fatalf("announce cmd returned %T %v", msg, msg)
	}

	live, err := uirequest.ListInstances(config.StateDir())
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	if len(live) != 1 {
		t.Fatalf("live instances = %+v, want one", live)
	}
	if live[0].PID != os.Getpid() || live[0].WorkDir != workDir {
		t.Fatalf("instance = %+v", live[0])
	}
	if live[0].ProjectKey == "" {
		t.Fatal("expected projectKey from registered project")
	}
}
