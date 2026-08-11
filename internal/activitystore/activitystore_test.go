package activitystore

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
)

func TestRoundTripPreservesUnseenIdle(t *testing.T) {
	now := time.Now()
	path := filepath.Join(t.TempDir(), FileName)
	finished := now.Add(-2 * time.Minute)
	in := map[string]agentactivity.Tracker{
		"a": {State: agentactivity.StateIdle, Evidence: "claude.prompt.idle", ChangedAt: finished},
	}
	if err := Save(path, in, now); err != nil {
		t.Fatalf("save: %v", err)
	}
	out := Load(path, now)
	tracker, ok := out["a"]
	if !ok {
		t.Fatalf("entry not restored: %v", out)
	}
	if tracker.Seen {
		t.Fatalf("restored tracker lost its unseen completion")
	}
	if !tracker.ChangedAt.Equal(finished) {
		t.Fatalf("changedAt = %v, want %v", tracker.ChangedAt, finished)
	}
	if tracker.DisplayState() != "done" {
		t.Fatalf("display state = %q, want done", tracker.DisplayState())
	}
}

func TestLoadSkipsLiveStatesAndExpiredEntries(t *testing.T) {
	now := time.Now()
	path := filepath.Join(t.TempDir(), FileName)
	// Working state must never be restored: the pane's process may have been
	// replaced while Sidecar was down, and a restored working state would turn
	// the next idle reading into a completion that never happened.
	in := map[string]agentactivity.Tracker{
		"live": {State: agentactivity.StateWorking, ChangedAt: now},
		"idle": {State: agentactivity.StateIdle, ChangedAt: now, Seen: true},
	}
	if err := Save(path, in, now); err != nil {
		t.Fatalf("save: %v", err)
	}
	out := Load(path, now)
	if _, ok := out["live"]; ok {
		t.Fatalf("working state should not be restored")
	}
	if _, ok := out["idle"]; !ok {
		t.Fatalf("idle state should be restored")
	}
	if aged := Load(path, now.Add(RetainFor+time.Hour)); len(aged) != 0 {
		t.Fatalf("expired entries survived: %v", aged)
	}
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	if got := Load(filepath.Join(t.TempDir(), "absent.json"), time.Now()); len(got) != 0 {
		t.Fatalf("expected empty map, got %v", got)
	}
}
