package workspaceinventory

import (
	"fmt"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/agentstatus"
	"github.com/marcus/sidecar/internal/tty"
)

// These are characterization tests for the observation-building path, the one
// place where a pane capture becomes an agentstatus.Input. They drive
// observeContext directly with a fake Capture, so no tmux, git or filesystem is
// involved. They pin what the collector does today so a later extraction of a
// shared authority resolver has to change them deliberately.

const compatWorkspaceID = "proj:shell:sidecar-sh-1"

var compatNow = time.Date(2026, 8, 30, 17, 0, 0, 0, time.UTC)

func compatShell(provider string) Workspace {
	return Workspace{
		ID: compatWorkspaceID, ProjectKey: "proj", ProjectName: "proj", ProjectRoot: "/repos/proj",
		Kind: KindShell, Key: "sidecar-sh-1", Name: "Shell 1", Path: "/repos/proj",
		TmuxName: "sidecar-sh-1", Provider: provider,
	}
}

// compatCollector returns a defaulted collector whose capture always yields
// screen, counting the calls so a test can prove a path never captured at all.
func compatCollector(screen string, calls *int) Collector {
	return Collector{Capture: func(string, int) (string, tty.PaneState, error) {
		if calls != nil {
			*calls++
		}
		return screen, tty.PaneState{}, nil
	}}.WithDefaults()
}

// TestObserveUsesADefaultedDoneTTLAndAlwaysCurrentFreshness pins the two
// timings this path hands the resolver. DoneTTL falls back to the shared
// default, which is what makes a finished turn decay on its own.
//
// StaleAfter is one minute here, but it is not observable through the result:
// observeContext passes the same instant as both CapturedAt and Now, so the
// reading can never be older than the window and every answer is current. A
// resolver extraction that changes the stale window would not be caught by this
// test, and today nothing else would catch it either.
func TestObserveUsesADefaultedDoneTTLAndAlwaysCurrentFreshness(t *testing.T) {
	if got := (Collector{}).WithDefaults().DoneTTL; got != agentstatus.DefaultDoneTTL {
		t.Fatalf("defaulted DoneTTL = %v, want %v", got, agentstatus.DefaultDoneTTL)
	}
	if got := (Collector{DoneTTL: time.Hour}).WithDefaults().DoneTTL; got != time.Hour {
		t.Fatalf("configured DoneTTL = %v", got)
	}

	collector := compatCollector("• Working (1s • esc to interrupt)", nil)
	workspace := compatShell("codex")
	collector.observe(&workspace, []Pane{{ID: "%1", Session: "sidecar-sh-1", Command: "codex"}}, compatNow)
	if workspace.Presentation.Freshness != agentstatus.FreshnessCurrent {
		t.Fatalf("freshness = %q", workspace.Presentation.Freshness)
	}
	if !workspace.Presentation.CapturedAt.Equal(compatNow) {
		t.Fatalf("CapturedAt = %v, want the observation clock", workspace.Presentation.CapturedAt)
	}
}

// TestObserveAppliesTheDoneTTLToASeededTracker pins the observable consequence
// of the defaulted TTL: a completion older than the window is reported as idle
// rather than done, without the tracker itself changing.
func TestObserveAppliesTheDoneTTLToASeededTracker(t *testing.T) {
	tests := []struct {
		name      string
		changedAt time.Time
		wantLane  agentstatus.LaneID
	}{
		{"a recent finish is done", compatNow.Add(-time.Minute), agentstatus.LaneDone},
		{"a finish past the default TTL decays to idle", compatNow.Add(-11 * time.Minute), agentstatus.LaneIdle},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The seeded evidence matches what a quiet Claude screen produces, so
			// the tracker is left untouched and keeps its seeded ChangedAt.
			collector := compatCollector("$ ", nil).SeedTrackers(map[string]agentactivity.Tracker{
				compatWorkspaceID: {State: agentactivity.StateIdle, Evidence: "claude.known-live-fallback", ChangedAt: tt.changedAt},
			})
			workspace := compatShell("claude")
			collector.observe(&workspace, []Pane{{ID: "%1", Session: "sidecar-sh-1", Command: "claude"}}, compatNow)
			if got := workspace.Presentation.Lane; got != tt.wantLane {
				t.Fatalf("lane = %q, want %q", got, tt.wantLane)
			}
			if got := collector.TrackerSnapshot()[compatWorkspaceID]; !got.ChangedAt.Equal(tt.changedAt) {
				t.Fatalf("repeat evidence moved ChangedAt to %v", got.ChangedAt)
			}
		})
	}
}

// TestObserveRewritesTheProviderFromLiveIdentity pins the demotion and
// promotion rules. A live shell process clears the configured provider outright,
// which is what stops a launch preference painting every zsh pane as an agent;
// any other identified provider overwrites the configured one and its support
// is recomputed from the new name.
func TestObserveRewritesTheProviderFromLiveIdentity(t *testing.T) {
	tests := []struct {
		name         string
		provider     string
		command      string
		screen       string
		wantProvider string
		wantLane     agentstatus.LaneID
		wantSemantic bool
		wantEvidence string
	}{
		{
			name: "a live shell clears the configured provider", provider: "cursor", command: "zsh", screen: "$ ",
			wantProvider: "", wantLane: agentstatus.LanePaused, wantSemantic: false, wantEvidence: "",
		},
		{
			name: "an identified provider overwrites the configured one", provider: "claude", command: "codex",
			screen:       "• Working (1s • esc to interrupt)",
			wantProvider: "codex", wantLane: agentstatus.LaneWorking, wantSemantic: true, wantEvidence: "codex.screen.working",
		},
		{
			// Nothing identifiable leaves the configured provider in place, and
			// that provider's own process gate then refuses the screen.
			name: "an unidentifiable command keeps the configured provider", provider: "claude", command: "vim", screen: "$ ",
			wantProvider: "claude", wantLane: agentstatus.LanePaused, wantSemantic: true, wantEvidence: "claude.process-mismatch",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			collector := compatCollector(tt.screen, nil)
			workspace := compatShell(tt.provider)
			collector.observe(&workspace, []Pane{{ID: "%1", Session: "sidecar-sh-1", Command: tt.command}}, compatNow)
			if workspace.Provider != tt.wantProvider {
				t.Fatalf("provider = %q, want %q", workspace.Provider, tt.wantProvider)
			}
			got := workspace.Presentation
			if got.Lane != tt.wantLane || got.Semantic != tt.wantSemantic || got.Evidence != tt.wantEvidence {
				t.Fatalf("presentation = %#v", got)
			}
			if !tt.wantSemantic && (got.Icon != "" || got.Label != "") {
				t.Fatalf("demoted workspace still carried presentation: %#v", got)
			}
		})
	}
}

// TestObservePaneCorrelationDecidesHealth pins the four correlation outcomes
// that answer before any agent evidence is read, and which of them capture a
// pane at all. Capturing a pane the row does not own is how a workspace ends up
// reporting another workspace's agent.
func TestObservePaneCorrelationDecidesHealth(t *testing.T) {
	tests := []struct {
		name          string
		matches       []Pane
		captureErr    bool
		wantLabel     string
		wantIcon      string
		wantLive      bool
		wantAmbiguous bool
		wantPaneID    string
		wantCaptures  int
	}{
		{name: "no matching pane is orphaned", matches: nil, wantLabel: "session ended", wantIcon: "⚠"},
		{
			name:    "more than one matching pane is ambiguous",
			matches: []Pane{{ID: "%1", Session: "a", Command: "codex"}, {ID: "%2", Session: "b", Command: "codex"}},
			// A row that cannot be told apart from another keeps no pane id: a
			// preview bound to a guess would resize the wrong pane.
			wantLabel: "ambiguous", wantIcon: "?", wantAmbiguous: true,
		},
		{
			name:      "a dead pane is orphaned but still names its pane",
			matches:   []Pane{{ID: "%1", Session: "sidecar-sh-1", Command: "codex", Dead: true}},
			wantLabel: "session ended", wantIcon: "⚠", wantPaneID: "%1",
		},
		{
			name:       "a failed capture is an error",
			matches:    []Pane{{ID: "%1", Session: "sidecar-sh-1", Command: "codex"}},
			captureErr: true, wantLabel: "error", wantIcon: "✗", wantLive: true, wantPaneID: "%1", wantCaptures: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			captures := 0
			collector := Collector{Capture: func(string, int) (string, tty.PaneState, error) {
				captures++
				if tt.captureErr {
					return "", tty.PaneState{}, fmt.Errorf("capture refused")
				}
				return "• Working (1s • esc to interrupt)", tty.PaneState{}, nil
			}}.WithDefaults()
			workspace := compatShell("codex")
			collector.observe(&workspace, tt.matches, compatNow)

			got := workspace.Presentation
			if got.Lane != agentstatus.LanePaused || !got.Health {
				t.Fatalf("lane/health = %q/%v", got.Lane, got.Health)
			}
			if got.Label != tt.wantLabel || got.Icon != tt.wantIcon {
				t.Fatalf("label/icon = %q/%q, want %q/%q", got.Label, got.Icon, tt.wantLabel, tt.wantIcon)
			}
			if workspace.Live != tt.wantLive || workspace.Ambiguous != tt.wantAmbiguous || workspace.PaneID != tt.wantPaneID {
				t.Fatalf("live/ambiguous/paneID = %v/%v/%q", workspace.Live, workspace.Ambiguous, workspace.PaneID)
			}
			if captures != tt.wantCaptures {
				t.Fatalf("captures = %d, want %d", captures, tt.wantCaptures)
			}
		})
	}
}

// TestObserveGivesAPlainWorktreeNoPresentation pins the early exit. A worktree
// with no recorded agent still earns pane correlation, because that is what
// "live" means, but it is never captured and never given an agentstatus value:
// fabricating one would put a semantic state on the board for a workspace that
// has no agent.
func TestObserveGivesAPlainWorktreeNoPresentation(t *testing.T) {
	captures := 0
	collector := compatCollector("• Working (1s • esc to interrupt)", &captures)

	plain := Workspace{ID: "proj:worktree:/repos/proj", Kind: KindWorktree, Plain: true, Path: "/repos/proj"}
	collector.observe(&plain, []Pane{{ID: "%1", Session: "sidecar-ws-1", Command: "codex"}}, compatNow)
	if !plain.Live || plain.PaneID != "%1" || plain.TmuxName != "sidecar-ws-1" {
		t.Fatalf("plain worktree lost pane correlation: %#v", plain)
	}
	if plain.Presentation != (agentstatus.Presentation{}) {
		t.Fatalf("plain worktree got a presentation: %#v", plain.Presentation)
	}
	if captures != 0 {
		t.Fatalf("plain worktree captured %d panes", captures)
	}

	// The same worktree with a recorded agent is captured and resolved.
	agentBacked := Workspace{ID: "proj:worktree:/repos/proj", Kind: KindWorktree, Path: "/repos/proj", Provider: "codex"}
	collector.observe(&agentBacked, []Pane{{ID: "%1", Session: "sidecar-ws-1", Command: "codex"}}, compatNow)
	if agentBacked.Presentation.Lane != agentstatus.LaneWorking || captures != 1 {
		t.Fatalf("agent worktree = %#v after %d captures", agentBacked.Presentation, captures)
	}
}

// TestTrackersAreKeyedByWorkspaceIDAndSurviveARefresh pins the identity the
// activity store uses and the seed/snapshot pair that carries it across a
// refresh generation. Keying on anything a pane owns instead would reset an
// agent's history every time tmux renumbered it.
func TestTrackersAreKeyedByWorkspaceIDAndSurviveARefresh(t *testing.T) {
	first := compatCollector("• Working (1s • esc to interrupt)", nil)
	one := compatShell("codex")
	two := compatShell("codex")
	two.ID = "proj:shell:sidecar-sh-2"
	// Both rows correlate to the same pane; only the workspace ID separates
	// their activity.
	pane := []Pane{{ID: "%1", Session: "sidecar-sh-1", Command: "codex"}}
	first.observe(&one, pane, compatNow)
	first.observe(&two, pane, compatNow)

	snapshot := first.TrackerSnapshot()
	if len(snapshot) != 2 {
		t.Fatalf("tracker keys = %v", snapshot)
	}
	for _, key := range []string{compatWorkspaceID, "proj:shell:sidecar-sh-2"} {
		tracker, ok := snapshot[key]
		if !ok {
			t.Fatalf("no tracker under %q: %v", key, snapshot)
		}
		if tracker.State != agentactivity.StateWorking || !tracker.ChangedAt.Equal(compatNow) {
			t.Fatalf("tracker %q = %#v", key, tracker)
		}
	}

	// A fresh collector seeded from that snapshot continues the same episode:
	// the repeated evidence does not restamp ChangedAt, so the row still reads
	// as having been working since the first observation.
	second := compatCollector("• Working (1s • esc to interrupt)", nil).SeedTrackers(snapshot)
	later := compatNow.Add(5 * time.Minute)
	resumed := compatShell("codex")
	second.observe(&resumed, pane, later)
	if !resumed.Presentation.ChangedAt.Equal(compatNow) {
		t.Fatalf("ChangedAt after reseed = %v, want %v", resumed.Presentation.ChangedAt, compatNow)
	}
	if got := second.TrackerSnapshot()[compatWorkspaceID]; got.State != agentactivity.StateWorking || !got.ChangedAt.Equal(compatNow) {
		t.Fatalf("reseeded tracker = %#v", got)
	}
}
