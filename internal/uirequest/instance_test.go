package uirequest

import (
	"os"
	"syscall"
	"testing"
	"time"
)

func TestListInstancesIgnoresDeadPID(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")

	dead := Instance{
		PID:        unusedPID(t),
		Host:       HostName(),
		ProjectKey: "gone",
		Project:    "gone",
		WorkDir:    "/tmp/gone",
		StartedAt:  time.Now().UTC(),
	}
	if err := Announce(stateDir, dead); err != nil {
		t.Fatalf("Announce: %v", err)
	}
	if _, err := os.Stat(InstancePath(stateDir, dead.PID)); err != nil {
		t.Fatalf("presence file missing after Announce: %v", err)
	}

	live, err := ListInstances(stateDir)
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	if len(live) != 0 {
		t.Fatalf("ListInstances = %+v, want none", live)
	}
	if _, err := os.Stat(InstancePath(stateDir, dead.PID)); !os.IsNotExist(err) {
		t.Fatal("dead presence file was not swept")
	}
}

func TestListInstancesReturnsLiveAnnounce(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")

	inst := Instance{
		PID:        os.Getpid(),
		Host:       HostName(),
		ProjectKey: "sidecar",
		Project:    "sidecar",
		WorkDir:    "/tmp/sidecar",
	}
	if err := Announce(stateDir, inst); err != nil {
		t.Fatalf("Announce: %v", err)
	}

	live, err := ListInstances(stateDir)
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	if len(live) != 1 {
		t.Fatalf("ListInstances = %+v, want one live record", live)
	}
	if live[0].PID != os.Getpid() || live[0].ProjectKey != "sidecar" || live[0].WorkDir != "/tmp/sidecar" {
		t.Fatalf("live instance = %+v", live[0])
	}
}

func unusedPID(t *testing.T) int {
	t.Helper()
	for pid := 1<<22 - 1; pid > 10; pid-- {
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			return pid
		}
	}
	t.Fatal("could not find an unused pid")
	return 0
}
