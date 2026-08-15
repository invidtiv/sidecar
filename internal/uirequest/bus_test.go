package uirequest

import (
	"os"
	"testing"
	"time"
)

func TestWriteAndReadRequest(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")

	req := Request{
		Origin: Origin{
			TmuxSession: "sidecar-sh-test",
			Namespace:   "/tmp/tmux.sock",
			ProjectKey:  "p1",
			WorkDir:     "/work/p1",
			PID:         1234,
		},
		Action: ActionOpen,
		Target: Target{
			Kind:  TargetKindFile,
			Value: "docs/readme.md",
			Line:  42,
		},
		Options: Options{
			Split: "right",
			Focus: true,
		},
	}

	path, err := WriteRequest(stateDir, req)
	if err != nil {
		t.Fatalf("WriteRequest failed: %v", err)
	}

	read, err := ReadRequest(path)
	if err != nil {
		t.Fatalf("ReadRequest failed: %v", err)
	}

	if read.Origin.TmuxSession != "sidecar-sh-test" || read.Target.Value != "docs/readme.md" || read.Target.Line != 42 {
		t.Fatalf("unexpected request content: %+v", read)
	}
}

func TestAcksAndCleanup(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")

	req := Request{
		Action: ActionOpen,
		Target: Target{Kind: TargetKindFile, Value: "main.go"},
	}
	_, err := WriteRequest(stateDir, req)
	if err != nil {
		t.Fatalf("WriteRequest: %v", err)
	}

	ack1 := Ack{
		Instance: "inst-1",
		Host:     "local",
		PID:      1001,
		Status:   StatusOpened,
		Surface:  "shell:sidecar-sh-test",
		Pane:     2,
	}
	ack2 := Ack{
		Instance: "inst-2",
		Host:     "local",
		PID:      1002,
		Status:   StatusQueued,
		Surface:  "shell:sidecar-sh-test",
	}

	if err := WriteAck(stateDir, req.ID, req.Action, ack1); err != nil {
		t.Fatalf("WriteAck 1: %v", err)
	}
	if err := WriteAck(stateDir, req.ID, req.Action, ack2); err != nil {
		t.Fatalf("WriteAck 2: %v", err)
	}

	acks, err := ReadAcks(stateDir, req.ID, req.Action)
	if err != nil {
		t.Fatalf("ReadAcks: %v", err)
	}
	if len(acks) != 2 {
		t.Fatalf("expected 2 acks, got %d", len(acks))
	}

	if err := Cleanup(stateDir, req.ID, req.Action); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	if _, err := os.Stat(RequestPath(stateDir, req.ID, req.Action)); !os.IsNotExist(err) {
		t.Errorf("request file still exists after cleanup")
	}
	if _, err := os.Stat(AcksDirPath(stateDir, req.ID, req.Action)); !os.IsNotExist(err) {
		t.Errorf("acks dir still exists after cleanup")
	}
}

func TestSweepExpired(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")

	past := time.Now().UTC().Add(-1 * time.Hour)
	req := Request{
		ID:        "expired-req",
		CreatedAt: past,
		TTLMs:     1000,
		Action:    ActionOpen,
		Target:    Target{Kind: TargetKindFile, Value: "a.txt"},
	}

	if _, err := WriteRequest(stateDir, req); err != nil {
		t.Fatalf("WriteRequest: %v", err)
	}

	if err := Sweep(stateDir, time.Now().UTC()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}

	if _, err := os.Stat(RequestPath(stateDir, req.ID, req.Action)); !os.IsNotExist(err) {
		t.Errorf("expired request was not swept")
	}
}

func TestWatcherPicksUpNewRequest(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")

	w, err := NewWatcher(stateDir)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	ch := w.Start()
	defer w.Stop()

	req := Request{
		Action: ActionOpen,
		Target: Target{Kind: TargetKindFile, Value: "b.txt"},
	}
	if _, err := WriteRequest(stateDir, req); err != nil {
		t.Fatalf("WriteRequest: %v", err)
	}

	select {
	case msg := <-ch:
		reqMsg, ok := msg.(RequestMsg)
		if !ok {
			t.Fatalf("unexpected message type: %T", msg)
		}
		if reqMsg.Request.Target.Value != "b.txt" {
			t.Errorf("expected b.txt, got %s", reqMsg.Request.Target.Value)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for RequestMsg from watcher")
	}
}
