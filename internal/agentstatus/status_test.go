package agentstatus

import (
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
)

func TestResolveSemanticAndLegacyMatrix(t *testing.T) {
	now := time.Unix(42, 0)
	tests := []struct {
		name string
		in   Input
		lane LaneID
		text string
	}{
		{"working", Input{ProviderSupported: true, Activity: agentactivity.Tracker{State: agentactivity.StateWorking, Evidence: "busy", ChangedAt: now}}, LaneWorking, "working"},
		{"blocked", Input{ProviderSupported: true, Activity: agentactivity.Tracker{State: agentactivity.StateBlocked}}, LaneBlocked, "blocked"},
		{"unseen idle", Input{ProviderSupported: true, Activity: agentactivity.Tracker{State: agentactivity.StateIdle}}, LaneDone, "done"},
		{"seen idle", Input{ProviderSupported: true, Activity: agentactivity.Tracker{State: agentactivity.StateIdle, Seen: true}}, LaneIdle, "idle"},
		{"unknown", Input{ProviderSupported: true, Activity: agentactivity.Tracker{State: agentactivity.StateUnknown}}, LanePaused, "unknown"},
		{"legacy active", Input{LegacyStatus: "active", LegacyIcon: "a"}, LaneWorking, ""},
		{"legacy waiting", Input{LegacyStatus: "waiting", LegacyIcon: "w"}, LaneBlocked, ""},
		{"legacy done", Input{LegacyStatus: "done", LegacyIcon: "d"}, LaneDone, ""},
		{"legacy paused", Input{LegacyStatus: "paused", LegacyIcon: "p"}, LanePaused, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Resolve(tt.in)
			if got.Lane != tt.lane || got.Label != tt.text {
				t.Fatalf("Resolve() = %#v, want lane %q label %q", got, tt.lane, tt.text)
			}
		})
	}
}

func TestResolveHealthOverridesStaleActivity(t *testing.T) {
	base := Input{ProviderSupported: true, Activity: agentactivity.Tracker{State: agentactivity.StateWorking}}
	for name, mutate := range map[string]func(*Input){
		"missing":  func(in *Input) { in.Missing = true },
		"orphaned": func(in *Input) { in.Orphaned = true },
		"error":    func(in *Input) { in.Err = true },
		"paused":   func(in *Input) { in.Paused = true },
	} {
		t.Run(name, func(t *testing.T) {
			in := base
			mutate(&in)
			got := Resolve(in)
			if got.Lane != LanePaused || !got.Health || got.Label == "working" {
				t.Fatalf("Resolve() = %#v", got)
			}
		})
	}
}

func TestResolveFreshnessGatesAttention(t *testing.T) {
	now := time.Unix(100, 0)
	input := Input{
		ProviderSupported: true,
		Activity:          agentactivity.Tracker{State: agentactivity.StateBlocked, VisibleBlocker: true},
		CapturedAt:        now.Add(-time.Second),
		Now:               now,
		StaleAfter:        time.Minute,
	}
	if got := Resolve(input); got.Freshness != FreshnessCurrent || !got.Attention {
		t.Fatalf("fresh blocker = %#v", got)
	}
	input.CapturedAt = now.Add(-2 * time.Minute)
	if got := Resolve(input); got.Freshness != FreshnessStale || got.Attention {
		t.Fatalf("stale blocker = %#v", got)
	}
	input.Unavailable = true
	if got := Resolve(input); got.Freshness != FreshnessUnavailable || got.Lane != LanePaused || got.Attention {
		t.Fatalf("unavailable = %#v", got)
	}
}
