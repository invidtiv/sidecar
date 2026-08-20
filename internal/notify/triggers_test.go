package notify

import (
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentstatus"
)

func obs(key string, lane agentstatus.LaneID, health bool) LaneObservation {
	return LaneObservation{
		Key:          key,
		Label:        "Shell 1",
		Context:      "sidecar",
		Provider:     "claude",
		Presentation: agentstatus.Presentation{Lane: lane, Health: health},
		Origin:       Origin{TmuxSession: key, ProjectKey: "sidecar"},
	}
}

// settle drives the tracker until the debounce window has passed, returning the
// events from the round that committed.
func settle(t *testing.T, tr *LaneTracker, o LaneObservation, start time.Time) LaneEvents {
	t.Helper()
	if ev := tr.Observe([]LaneObservation{o}, start); !ev.Empty() {
		t.Fatalf("first sight of a new lane posted immediately: %#v", ev)
	}
	return tr.Observe([]LaneObservation{o}, start.Add(tr.debounce()))
}

func TestFirstSightIsABaselineNotANotification(t *testing.T) {
	tr := &LaneTracker{}
	now := time.Unix(1000, 0)
	// Three agents already blocked when sidecar starts must not produce three
	// toasts about states the user already knows about.
	ev := tr.Observe([]LaneObservation{
		obs("a", agentstatus.LaneBlocked, false),
		obs("b", agentstatus.LaneWorking, false),
		obs("c", agentstatus.LaneDone, false),
	}, now)
	if !ev.Empty() {
		t.Fatalf("baseline round posted %#v", ev)
	}
}

func TestWaitingPostsStickyAndSelfDismisses(t *testing.T) {
	tr := &LaneTracker{Debounce: time.Second}
	now := time.Unix(2000, 0)
	tr.Observe([]LaneObservation{obs("a", agentstatus.LaneWorking, false)}, now)

	ev := settle(t, tr, obs("a", agentstatus.LaneBlocked, false), now.Add(time.Second))
	if len(ev.Post) != 1 {
		t.Fatalf("blocked posted %d notifications, want 1", len(ev.Post))
	}
	waiting := ev.Post[0]
	if waiting.Source != SourceWaiting || !waiting.Sticky {
		t.Fatalf("waiting notification = %#v, want sticky waiting source", waiting)
	}
	if waiting.Title != "Shell 1 needs input" || waiting.Body != "claude · sidecar" {
		t.Fatalf("waiting identity = %q / %q", waiting.Title, waiting.Body)
	}
	if waiting.Origin.TmuxSession != "a" {
		t.Fatalf("origin not carried: %#v", waiting.Origin)
	}

	// Answering the prompt withdraws the notification the tracker posted.
	back := now.Add(10 * time.Second)
	ev = settle(t, tr, obs("a", agentstatus.LaneWorking, false), back)
	if len(ev.Dismiss) != 1 || ev.Dismiss[0] != waiting.ID {
		t.Fatalf("leaving blocked dismissed %#v, want %s", ev.Dismiss, waiting.ID)
	}
	if len(ev.Post) != 0 {
		t.Fatalf("working transition posted %#v", ev.Post)
	}
}

func TestFlappingLaneNeverPosts(t *testing.T) {
	tr := &LaneTracker{Debounce: 3 * time.Second}
	now := time.Unix(3000, 0)
	tr.Observe([]LaneObservation{obs("a", agentstatus.LaneWorking, false)}, now)
	for i := 1; i <= 10; i++ {
		lane := agentstatus.LaneBlocked
		if i%2 == 0 {
			lane = agentstatus.LaneWorking
		}
		at := now.Add(time.Duration(i) * time.Second)
		if ev := tr.Observe([]LaneObservation{obs("a", lane, false)}, at); !ev.Empty() {
			t.Fatalf("flap at %ds posted %#v", i, ev)
		}
	}
}

func TestSettledLanePostsOnlyOnce(t *testing.T) {
	tr := &LaneTracker{Debounce: time.Second}
	now := time.Unix(4000, 0)
	tr.Observe([]LaneObservation{obs("a", agentstatus.LaneWorking, false)}, now)
	ev := settle(t, tr, obs("a", agentstatus.LaneDone, false), now.Add(time.Second))
	if len(ev.Post) != 1 || ev.Post[0].Source != SourceSession || ev.Post[0].Severity != SeverityInfo {
		t.Fatalf("finish = %#v", ev.Post)
	}
	if ev.Post[0].Title != "Shell 1 finished" {
		t.Fatalf("title = %q", ev.Post[0].Title)
	}
	for i := 0; i < 5; i++ {
		at := now.Add(time.Duration(10+i) * time.Second)
		if ev := tr.Observe([]LaneObservation{obs("a", agentstatus.LaneDone, false)}, at); !ev.Empty() {
			t.Fatalf("repeat observation posted %#v", ev)
		}
	}
}

func TestFinishOnlyCountsFromALiveLane(t *testing.T) {
	tr := &LaneTracker{Debounce: time.Second}
	now := time.Unix(5000, 0)
	// idle → done is bookkeeping (the done TTL expiring in reverse), not a turn
	// that just finished.
	tr.Observe([]LaneObservation{obs("a", agentstatus.LaneIdle, false)}, now)
	if ev := settle(t, tr, obs("a", agentstatus.LaneDone, false), now.Add(time.Second)); !ev.Empty() {
		t.Fatalf("idle→done posted %#v", ev)
	}
}

func TestSessionDeathPostsAnError(t *testing.T) {
	tr := &LaneTracker{Debounce: time.Second}
	now := time.Unix(6000, 0)
	tr.Observe([]LaneObservation{obs("a", agentstatus.LaneWorking, false)}, now)
	ev := settle(t, tr, obs("a", agentstatus.LanePaused, true), now.Add(time.Second))
	if len(ev.Post) != 1 {
		t.Fatalf("death posted %d, want 1", len(ev.Post))
	}
	if ev.Post[0].Source != SourceSession || ev.Post[0].Severity != SeverityError {
		t.Fatalf("death = %#v", ev.Post[0])
	}
	if ev.Post[0].Title != "Shell 1 session ended" {
		t.Fatalf("title = %q", ev.Post[0].Title)
	}
}

func TestPausedWithoutHealthIsNotADeath(t *testing.T) {
	tr := &LaneTracker{Debounce: time.Second}
	now := time.Unix(6500, 0)
	tr.Observe([]LaneObservation{obs("a", agentstatus.LaneWorking, false)}, now)
	if ev := settle(t, tr, obs("a", agentstatus.LanePaused, false), now.Add(time.Second)); !ev.Empty() {
		t.Fatalf("plain paused posted %#v", ev)
	}
}

func TestVanishedWorkspaceWithdrawsItsWaiting(t *testing.T) {
	tr := &LaneTracker{Debounce: time.Second}
	now := time.Unix(7000, 0)
	tr.Observe([]LaneObservation{obs("a", agentstatus.LaneWorking, false)}, now)
	ev := settle(t, tr, obs("a", agentstatus.LaneBlocked, false), now.Add(time.Second))
	waitingID := ev.Post[0].ID

	// The shell is closed: it is no longer in the observation set at all.
	ev = tr.Observe(nil, now.Add(30*time.Second))
	if len(ev.Dismiss) != 1 || ev.Dismiss[0] != waitingID {
		t.Fatalf("vanish dismissed %#v, want %s", ev.Dismiss, waitingID)
	}
	if len(ev.Post) != 0 {
		t.Fatalf("vanish posted %#v", ev.Post)
	}
	// And it is forgotten, so coming back is a fresh baseline.
	if ev := tr.Observe([]LaneObservation{obs("a", agentstatus.LaneBlocked, false)}, now.Add(time.Minute)); !ev.Empty() {
		t.Fatalf("returning workspace posted %#v", ev)
	}
}

func TestObservationsAreIndependentPerWorkspace(t *testing.T) {
	tr := &LaneTracker{Debounce: time.Second}
	now := time.Unix(8000, 0)
	base := []LaneObservation{obs("a", agentstatus.LaneWorking, false), obs("b", agentstatus.LaneWorking, false)}
	tr.Observe(base, now)

	moved := []LaneObservation{obs("a", agentstatus.LaneBlocked, false), obs("b", agentstatus.LaneWorking, false)}
	tr.Observe(moved, now.Add(time.Second))
	ev := tr.Observe(moved, now.Add(2*time.Second))
	if len(ev.Post) != 1 || ev.Post[0].Origin.TmuxSession != "a" {
		t.Fatalf("only a should have posted: %#v", ev.Post)
	}
}

func TestLabelFallsBackToProvider(t *testing.T) {
	o := LaneObservation{Provider: "codex"}
	if got := laneName(o); got != "codex" {
		t.Fatalf("laneName = %q", got)
	}
	if got := laneName(LaneObservation{}); got != "Agent" {
		t.Fatalf("laneName = %q", got)
	}
}
