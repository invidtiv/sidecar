package shellstate

import (
	"testing"
	"time"
)

func TestLossObservationIsNotTheRecordCreationFence(t *testing.T) {
	birth := time.Now().Add(-2 * time.Hour).UTC()
	def := Definition{TmuxName: "sidecar-sh-loss", DisplayName: "Loss", CreatedAt: birth, AgentType: "claude", Restore: &RestoreState{Eligible: true, LastSeenServer: "pid=1", PrefilledAt: birth}}
	path := t.TempDir() + "/shells.json"
	if err := AddAtPath(path, def); err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC()
	if _, err := ForgetOrPreserveAtPath(path, Identity{TmuxName: def.TmuxName}, birth, ServerGone(), "was running claude, idle since 13:44"); err != nil {
		t.Fatal(err)
	}
	defs, err := ListAtPath(path)
	if err != nil {
		t.Fatal(err)
	}
	got := defs[0].Restore
	if got.ServerLostAt.Before(before) || got.LastAgentActivity != "was running claude, idle since 13:44" || !got.PrefilledAt.IsZero() {
		t.Fatalf("loss = %+v", got)
	}
	first := got.ServerLostAt
	if _, err := ForgetOrPreserveAtPath(path, Identity{TmuxName: def.TmuxName}, birth, ServerGone(), "new evidence"); err != nil {
		t.Fatal(err)
	}
	defs, _ = ListAtPath(path)
	if !defs[0].Restore.ServerLostAt.Equal(first) || defs[0].Restore.LastAgentActivity != got.LastAgentActivity {
		t.Fatal("repeated loss changed original evidence")
	}
}
