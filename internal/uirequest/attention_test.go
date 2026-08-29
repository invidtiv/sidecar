package uirequest

import (
	"os"
	"testing"
	"time"
)

func TestAttentionRoundTripAndForegroundResolution(t *testing.T) {
	stateDir := t.TempDir()
	record := Attention{
		PID: os.Getpid(), Host: HostName(), Focused: true,
		VisibleOrigin: Origin{TmuxSession: "sidecar-ws-a", ProjectKey: "sidecar", WorkDir: "/tmp/a"},
		UpdatedAt:     time.Now().UTC(),
	}
	if err := PublishAttention(stateDir, record); err != nil {
		t.Fatal(err)
	}
	live, err := ListAttention(stateDir)
	if err != nil || len(live) != 1 {
		t.Fatalf("live attention = %+v, %v", live, err)
	}
	if !OriginForeground(Origin{TmuxSession: "sidecar-ws-a"}, live) {
		t.Fatal("visible matching session was not foreground")
	}
	if OriginForeground(Origin{TmuxSession: "sidecar-ws-b"}, live) {
		t.Fatal("hidden session was foreground")
	}
	live[0].Focused = false
	if OriginForeground(Origin{TmuxSession: "sidecar-ws-a"}, live) {
		t.Fatal("blurred matching session was foreground")
	}
	if OriginForeground(Origin{}, live) {
		t.Fatal("unresolved origin must be background")
	}
	older := record
	older.UpdatedAt = record.UpdatedAt.Add(-time.Second)
	older.VisibleOrigin.TmuxSession = "stale-session"
	if err := PublishAttention(stateDir, older); err != nil {
		t.Fatal(err)
	}
	live, err = ListAttention(stateDir)
	if err != nil || len(live) != 1 || live[0].VisibleOrigin.TmuxSession != "sidecar-ws-a" {
		t.Fatalf("late stale publication won: %+v, %v", live, err)
	}
	if err := WithdrawAttention(stateDir, os.Getpid()); err != nil {
		t.Fatal(err)
	}
	if live, err := ListAttention(stateDir); err != nil || len(live) != 0 {
		t.Fatalf("withdraw left %+v, %v", live, err)
	}
}
