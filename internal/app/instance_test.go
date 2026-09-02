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

	cmd := announceInstanceCmd(workDir, workDir, "")
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

func TestAnnounceInstanceCmdOverwritesOnProjectSwitch(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")

	projectA := t.TempDir()
	projectB := t.TempDir()
	if _, err := projectdir.Resolve(projectA); err != nil {
		t.Fatal(err)
	}
	if _, err := projectdir.Resolve(projectB); err != nil {
		t.Fatal(err)
	}

	if msg := announceInstanceCmd(projectA, projectA, "")(); msg != nil {
		t.Fatalf("announce A returned %v", msg)
	}
	live, err := uirequest.ListInstances(config.StateDir())
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	if len(live) != 1 {
		t.Fatalf("after A: %+v, want one", live)
	}
	keyA := live[0].ProjectKey
	if keyA == "" {
		t.Fatal("expected projectKey for A")
	}

	if msg := announceInstanceCmd(projectB, projectB, "")(); msg != nil {
		t.Fatalf("announce B returned %v", msg)
	}
	live, err = uirequest.ListInstances(config.StateDir())
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	if len(live) != 1 {
		t.Fatalf("after B: %+v, want one overwritten record", live)
	}
	if live[0].PID != os.Getpid() {
		t.Fatalf("pid = %d, want %d", live[0].PID, os.Getpid())
	}
	if live[0].ProjectKey == keyA {
		t.Fatalf("projectKey still %q after switch", keyA)
	}
	if live[0].WorkDir != projectB {
		t.Fatalf("WorkDir = %q, want %q", live[0].WorkDir, projectB)
	}
}

func TestAnnounceInstanceCmdPublishesBoundHostWithoutRemotePath(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")

	remoteRoot := "/home/me/sidecar"
	if msg := announceInstanceCmd(remoteRoot, remoteRoot, "aerie")(); msg != nil {
		t.Fatalf("announce returned %v", msg)
	}
	live, err := uirequest.ListInstances(config.StateDir())
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	if len(live) != 1 {
		t.Fatalf("live instances = %+v, want one", live)
	}
	if live[0].HostID != "aerie" {
		t.Fatalf("HostID = %q, want aerie", live[0].HostID)
	}
	if live[0].WorkDir != "" {
		t.Fatalf("WorkDir = %q, want empty (not a remote path)", live[0].WorkDir)
	}
	if live[0].ProjectKey != "" || live[0].Project != "" {
		t.Fatalf("advertised remote project as local identity: %+v", live[0])
	}
	if live[0].PID != os.Getpid() {
		t.Fatalf("pid = %d, want %d", live[0].PID, os.Getpid())
	}
}
