package agentstatus

import (
	"reflect"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
)

// These are characterization tests for Resolve. They pin the lane, icon, label
// and freshness every input combination produces today, so extracting a shared
// authority resolver cannot quietly change what a workspace looks like. Nothing
// here claims the current answers are the right ones.

var resolveNow = time.Date(2026, 8, 30, 17, 0, 0, 0, time.UTC)

// TestLaneAndFreshnessVocabularyIsFrozen pins the values and the order of both
// enums. Lane ids reach notification transition metadata and freshness gates
// attention, so a renamed value changes stored records and live behavior at
// once.
func TestLaneAndFreshnessVocabularyIsFrozen(t *testing.T) {
	lanes := []LaneID{LaneWorking, LaneBlocked, LaneDone, LaneIdle, LanePaused}
	wantLanes := []LaneID{"working", "blocked", "done", "idle", "paused"}
	if !reflect.DeepEqual(lanes, wantLanes) {
		t.Fatalf("lanes = %#v", lanes)
	}
	freshness := []Freshness{FreshnessUnknown, FreshnessCurrent, FreshnessStale, FreshnessUnavailable}
	wantFreshness := []Freshness{"unknown", "current", "stale", "unavailable"}
	if !reflect.DeepEqual(freshness, wantFreshness) {
		t.Fatalf("freshness = %#v", freshness)
	}
}

// TestDefaultDoneTTLIsFrozen pins how long a finished turn keeps reading as
// recently finished. The collector adopts this value by default, so it decides
// when a done row silently becomes an idle one.
func TestDefaultDoneTTLIsFrozen(t *testing.T) {
	if DefaultDoneTTL != 10*time.Minute {
		t.Fatalf("DefaultDoneTTL = %v", DefaultDoneTTL)
	}
}

// TestHealthPrecedenceTable pins the six conditions that answer before any
// semantics are consulted. All of them land in the paused lane with Health set,
// which is the flag the notification lane tracker reads as a session failure.
// Missing and Orphaned additionally overwrite freshness, because a folder or
// session that is gone cannot have a current reading.
func TestHealthPrecedenceTable(t *testing.T) {
	tests := []struct {
		name          string
		in            Input
		wantLabel     string
		wantIcon      string
		wantFreshness Freshness
	}{
		{"ambiguous", Input{Ambiguous: true, CapturedAt: resolveNow, Now: resolveNow}, "ambiguous", "?", FreshnessUnavailable},
		{"unavailable", Input{Unavailable: true, CapturedAt: resolveNow, Now: resolveNow}, "unavailable", "?", FreshnessUnavailable},
		{"missing", Input{Missing: true, CapturedAt: resolveNow, Now: resolveNow}, "folder missing", "✗", FreshnessUnavailable},
		{"orphaned", Input{Orphaned: true, CapturedAt: resolveNow, Now: resolveNow}, "session ended", "⚠", FreshnessUnavailable},
		// Err and Paused keep whatever freshness the capture earned, and both
		// let a legacy icon win over their own default glyph.
		{"error", Input{Err: true, CapturedAt: resolveNow, Now: resolveNow}, "error", "✗", FreshnessCurrent},
		{"error with legacy icon", Input{Err: true, LegacyIcon: "!", CapturedAt: resolveNow, Now: resolveNow}, "error", "!", FreshnessCurrent},
		{"paused", Input{Paused: true, CapturedAt: resolveNow, Now: resolveNow}, "paused", "⏸", FreshnessCurrent},
		{"paused with legacy icon", Input{Paused: true, LegacyIcon: "z", CapturedAt: resolveNow, Now: resolveNow}, "paused", "z", FreshnessCurrent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Resolve(tt.in)
			if got.Lane != LanePaused || !got.Health {
				t.Fatalf("lane/health = %q/%v", got.Lane, got.Health)
			}
			if got.Label != tt.wantLabel || got.Icon != tt.wantIcon {
				t.Fatalf("label/icon = %q/%q, want %q/%q", got.Label, got.Icon, tt.wantLabel, tt.wantIcon)
			}
			if got.Freshness != tt.wantFreshness {
				t.Fatalf("freshness = %q, want %q", got.Freshness, tt.wantFreshness)
			}
			// A health answer never carries semantics, whatever activity says.
			if got.Semantic || got.Evidence != "" || !got.ChangedAt.IsZero() || got.Attention || got.Inferred {
				t.Fatalf("health answer leaked semantics: %#v", got)
			}
		})
	}
}

// TestHealthPrecedenceOrderIsFrozen pins that the six checks are a chain, not a
// set. When several are true at once the first one in the chain wins, so a pane
// that is both ambiguous and orphaned reads as ambiguous.
func TestHealthPrecedenceOrderIsFrozen(t *testing.T) {
	all := Input{Ambiguous: true, Unavailable: true, Missing: true, Orphaned: true, Err: true, Paused: true, CapturedAt: resolveNow, Now: resolveNow}
	tests := []struct {
		name      string
		clear     func(*Input)
		wantLabel string
	}{
		{"ambiguous beats everything", func(*Input) {}, "ambiguous"},
		{"unavailable beats missing", func(in *Input) { in.Ambiguous = false }, "unavailable"},
		{"missing beats orphaned", func(in *Input) { in.Ambiguous, in.Unavailable = false, false }, "folder missing"},
		{"orphaned beats err", func(in *Input) { in.Ambiguous, in.Unavailable, in.Missing = false, false, false }, "session ended"},
		{"err beats paused", func(in *Input) { in.Ambiguous, in.Unavailable, in.Missing, in.Orphaned = false, false, false, false }, "error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := all
			tt.clear(&in)
			if got := Resolve(in); got.Label != tt.wantLabel {
				t.Fatalf("label = %q, want %q", got.Label, tt.wantLabel)
			}
		})
	}

	// Health also outranks a live semantic reading: an ambiguous pane whose
	// tracker says "working" still reports ambiguous.
	working := Input{ProviderSupported: true, Ambiguous: true, Orphaned: true, Activity: agentactivity.Tracker{State: agentactivity.StateWorking, Evidence: "codex.screen.working"}, CapturedAt: resolveNow, Now: resolveNow}
	if got := Resolve(working); got.Label != "ambiguous" || got.Semantic {
		t.Fatalf("ambiguous over working = %#v", got)
	}
}

// TestSemanticLaneTable pins the projection from a tracker's DisplayState to a
// lane, icon and label. This is the mapping the Kanban board, the overview and
// the notification tracker all read, so it is where a silent renaming would do
// the most damage.
func TestSemanticLaneTable(t *testing.T) {
	tests := []struct {
		name          string
		activity      agentactivity.Tracker
		doneTTL       time.Duration
		wantLane      LaneID
		wantIcon      string
		wantLabel     string
		wantAttention bool
		wantInferred  bool
	}{
		{"working", agentactivity.Tracker{State: agentactivity.StateWorking, Evidence: "codex.screen.working"}, 0, LaneWorking, "●", "working", false, false},
		{"blocked without a visible blocker", agentactivity.Tracker{State: agentactivity.StateBlocked, Evidence: "codex.screen.blocked"}, 0, LaneBlocked, "◆", "blocked", false, false},
		{"blocked with a visible blocker earns attention", agentactivity.Tracker{State: agentactivity.StateBlocked, Evidence: "codex.screen.blocked", VisibleBlocker: true}, 0, LaneBlocked, "◆", "blocked", true, false},
		{"done", agentactivity.Tracker{State: agentactivity.StateIdle, Evidence: "codex.screen.idle", ChangedAt: resolveNow.Add(-time.Minute)}, DefaultDoneTTL, LaneDone, "✓", "done", false, false},
		{"done past its TTL decays to idle", agentactivity.Tracker{State: agentactivity.StateIdle, Evidence: "codex.screen.idle", ChangedAt: resolveNow.Add(-11 * time.Minute)}, DefaultDoneTTL, LaneIdle, "○", "idle", false, false},
		{"idle", agentactivity.Tracker{State: agentactivity.StateIdle, Evidence: "codex.screen.idle", Seen: true}, 0, LaneIdle, "○", "idle", false, false},
		{"inferred idle is marked", agentactivity.Tracker{State: agentactivity.StateIdle, Evidence: "codex.known-live-fallback", Seen: true, IdleInferred: true}, 0, LaneIdle, "○", "idle", false, true},
		{"unknown falls to paused", agentactivity.Tracker{State: agentactivity.StateUnknown, Evidence: "live-process-changed"}, 0, LanePaused, "?", "unknown", false, false},
		{"a zero tracker also falls to paused", agentactivity.Tracker{}, 0, LanePaused, "?", "unknown", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Resolve(Input{ProviderSupported: true, Activity: tt.activity, DoneTTL: tt.doneTTL, CapturedAt: resolveNow, Now: resolveNow})
			if got.Lane != tt.wantLane || got.Icon != tt.wantIcon || got.Label != tt.wantLabel {
				t.Fatalf("lane/icon/label = %q/%q/%q, want %q/%q/%q", got.Lane, got.Icon, got.Label, tt.wantLane, tt.wantIcon, tt.wantLabel)
			}
			if got.Attention != tt.wantAttention || got.Inferred != tt.wantInferred {
				t.Fatalf("attention/inferred = %v/%v, want %v/%v", got.Attention, got.Inferred, tt.wantAttention, tt.wantInferred)
			}
			// The semantic branch is the only one that republishes the tracker's
			// evidence and transition time, and the only one that sets Semantic.
			if !got.Semantic || got.Evidence != tt.activity.Evidence || !got.ChangedAt.Equal(tt.activity.ChangedAt) {
				t.Fatalf("semantic carry-through = %#v", got)
			}
			// Nothing in the semantic branch is a health answer.
			if got.Health {
				t.Fatalf("semantic answer set Health: %#v", got)
			}
		})
	}
}

// TestSemanticAttentionNeedsCurrentFreshness pins that attention is a live
// signal. A blocked reading taken too long ago still shows the blocked lane but
// stops asking for the user, because the prompt may already be gone.
func TestSemanticAttentionNeedsCurrentFreshness(t *testing.T) {
	blocked := agentactivity.Tracker{State: agentactivity.StateBlocked, Evidence: "codex.screen.blocked", VisibleBlocker: true}
	stale := Resolve(Input{ProviderSupported: true, Activity: blocked, CapturedAt: resolveNow.Add(-2 * time.Minute), Now: resolveNow, StaleAfter: time.Minute})
	if stale.Freshness != FreshnessStale || stale.Lane != LaneBlocked || stale.Attention {
		t.Fatalf("stale blocked = %#v", stale)
	}
	unknown := Resolve(Input{ProviderSupported: true, Activity: blocked, Now: resolveNow})
	if unknown.Freshness != FreshnessUnknown || unknown.Attention {
		t.Fatalf("unknown-freshness blocked = %#v", unknown)
	}
}

// TestLegacyLaneTable pins the projection used when a provider has no activity
// detector. It manufactures no evidence and no label: the caller's own icon is
// all the presentation there is.
func TestLegacyLaneTable(t *testing.T) {
	tests := []struct {
		status   string
		wantLane LaneID
	}{
		{"active", LaneWorking},
		{"thinking", LaneWorking},
		{"waiting", LaneBlocked},
		{"done", LaneDone},
		{"", LanePaused},
		{"idle", LanePaused},
		{"blocked", LanePaused},
		{"anything else", LanePaused},
	}
	for _, tt := range tests {
		t.Run("legacy/"+tt.status, func(t *testing.T) {
			got := Resolve(Input{ProviderSupported: false, LegacyStatus: tt.status, LegacyIcon: "◇", CapturedAt: resolveNow, Now: resolveNow})
			if got.Lane != tt.wantLane {
				t.Fatalf("lane = %q, want %q", got.Lane, tt.wantLane)
			}
			if got.Icon != "◇" || got.Label != "" {
				t.Fatalf("icon/label = %q/%q, want ◇ and no label", got.Icon, got.Label)
			}
			if got.Semantic || got.Health || got.Evidence != "" || got.Attention || got.Inferred {
				t.Fatalf("legacy answer carried semantics: %#v", got)
			}
		})
	}

	// An unsupported provider's activity tracker is ignored entirely, even when
	// it holds a confident state.
	got := Resolve(Input{ProviderSupported: false, LegacyStatus: "waiting", Activity: agentactivity.Tracker{State: agentactivity.StateWorking, Evidence: "codex.screen.working"}, CapturedAt: resolveNow, Now: resolveNow})
	if got.Lane != LaneBlocked || got.Evidence != "" {
		t.Fatalf("legacy ignored its own status: %#v", got)
	}
}

// TestFreshnessResolutionTable pins how a capture time becomes a freshness.
// Staleness needs both a configured window and a clock; with either missing the
// reading counts as current, which is how a caller that passes no clock still
// gets an actionable answer.
func TestFreshnessResolutionTable(t *testing.T) {
	tests := []struct {
		name string
		in   Input
		want Freshness
	}{
		{"unavailable wins over a fresh capture", Input{Unavailable: true, CapturedAt: resolveNow, Now: resolveNow}, FreshnessUnavailable},
		{"ambiguous wins over a fresh capture", Input{Ambiguous: true, CapturedAt: resolveNow, Now: resolveNow}, FreshnessUnavailable},
		{"no capture time is unknown", Input{Now: resolveNow, StaleAfter: time.Minute}, FreshnessUnknown},
		{"beyond the window is stale", Input{CapturedAt: resolveNow.Add(-2 * time.Minute), Now: resolveNow, StaleAfter: time.Minute}, FreshnessStale},
		{"exactly at the window is still current", Input{CapturedAt: resolveNow.Add(-time.Minute), Now: resolveNow, StaleAfter: time.Minute}, FreshnessCurrent},
		{"no window configured is current", Input{CapturedAt: resolveNow.Add(-time.Hour), Now: resolveNow}, FreshnessCurrent},
		{"no clock is current", Input{CapturedAt: resolveNow.Add(-time.Hour), StaleAfter: time.Minute}, FreshnessCurrent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Resolve(tt.in).Freshness; got != tt.want {
				t.Fatalf("freshness = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestDoneExpiryNeedsATTLAClockAndATransitionTime pins the three ways done
// decay is disabled. Any of them missing keeps a finished turn in the done lane
// forever, which is what a caller relying on the default TTL must not
// accidentally opt out of.
func TestDoneExpiryNeedsATTLAClockAndATransitionTime(t *testing.T) {
	unseenIdle := agentactivity.Tracker{State: agentactivity.StateIdle, Evidence: "codex.screen.idle", ChangedAt: resolveNow.Add(-time.Hour)}
	tests := []struct {
		name string
		in   Input
		want LaneID
	}{
		{"no TTL never expires", Input{ProviderSupported: true, Activity: unseenIdle, Now: resolveNow}, LaneDone},
		{"negative TTL never expires", Input{ProviderSupported: true, Activity: unseenIdle, DoneTTL: -time.Minute, Now: resolveNow}, LaneDone},
		{"no clock never expires", Input{ProviderSupported: true, Activity: unseenIdle, DoneTTL: DefaultDoneTTL}, LaneDone},
		{"no transition time never expires", Input{ProviderSupported: true, Activity: agentactivity.Tracker{State: agentactivity.StateIdle, Evidence: "codex.screen.idle"}, DoneTTL: DefaultDoneTTL, Now: resolveNow}, LaneDone},
		{"all three present expires", Input{ProviderSupported: true, Activity: unseenIdle, DoneTTL: DefaultDoneTTL, Now: resolveNow}, LaneIdle},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Resolve(tt.in).Lane; got != tt.want {
				t.Fatalf("lane = %q, want %q", got, tt.want)
			}
		})
	}

	// Expiry is measured strictly: exactly at the TTL the turn is still done.
	atTTL := Input{ProviderSupported: true, Activity: agentactivity.Tracker{State: agentactivity.StateIdle, ChangedAt: resolveNow.Add(-DefaultDoneTTL)}, DoneTTL: DefaultDoneTTL, Now: resolveNow}
	if got := Resolve(atTTL).Lane; got != LaneDone {
		t.Fatalf("lane at exactly the TTL = %q", got)
	}
}
