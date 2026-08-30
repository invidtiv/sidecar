package notify

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentstatus"
)

// These are characterization tests for the lane trigger path. They pin the
// vocabulary written into stored notification records and the exact set of
// notifications LaneTracker will emit, so a later extraction of a shared
// authority resolver has to change them deliberately. Helper names here are
// deliberately distinct from the ones in triggers_test.go.

func assertLaneJSONFixture(t *testing.T, path string, value any) {
	t.Helper()
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != strings.TrimSpace(string(want)) {
		t.Fatalf("schema drift in %s\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}

func laneRow(key string, lane agentstatus.LaneID, health bool) LaneObservation {
	return LaneObservation{
		Key:          key,
		Label:        "Shell 4",
		Context:      "sidecar",
		Provider:     "codex",
		Presentation: agentstatus.Presentation{Lane: lane, Health: health, Evidence: "codex.screen.blocked"},
		Origin:       Origin{TmuxSession: key, ProjectKey: "sidecar", WorkDir: "/repos/sidecar"},
		ProjectRoot:  "/repos/sidecar",
	}
}

// commitLane baselines prior, then holds next for one full debounce window and
// returns the events from the round that committed.
func commitLane(t *testing.T, tr *LaneTracker, prior, next LaneObservation, start time.Time) LaneEvents {
	t.Helper()
	if ev := tr.Observe([]LaneObservation{prior}, start); !ev.Empty() {
		t.Fatalf("baseline round posted %#v", ev)
	}
	candidateAt := start.Add(time.Second)
	if ev := tr.Observe([]LaneObservation{next}, candidateAt); !ev.Empty() {
		t.Fatalf("candidate round posted %#v", ev)
	}
	return tr.Observe([]LaneObservation{next}, candidateAt.Add(tr.debounce()))
}

// TestDefaultLaneDebounceIsFrozen pins the window a lane must hold before it
// posts. It is the difference between swallowing a prompt's render flicker and
// toasting about it.
func TestDefaultLaneDebounceIsFrozen(t *testing.T) {
	if DefaultLaneDebounce != 3*time.Second {
		t.Fatalf("DefaultLaneDebounce = %v", DefaultLaneDebounce)
	}
	if got := (&LaneTracker{}).debounce(); got != DefaultLaneDebounce {
		t.Fatalf("zero tracker debounce = %v", got)
	}
}

// TestTransitionVocabularyIsFrozen pins every enum that reaches the JSONL
// store. Renaming one makes existing records unreadable by the reconciliation
// that owns them, and Rank decides which unread source the header shows.
func TestTransitionVocabularyIsFrozen(t *testing.T) {
	classes := []TransitionClass{TransitionWaiting, TransitionDone, TransitionFailure}
	wantClasses := []TransitionClass{"waiting", "done", "failure"}
	if !reflect.DeepEqual(classes, wantClasses) {
		t.Fatalf("transition classes = %#v", classes)
	}
	ids := []SourceID{SourceAgent, SourceWaiting, SourceSession, SourceTasks, SourceTD, SourceSystem}
	wantIDs := []SourceID{"agent", "waiting", "session", "tasks", "td", "system"}
	if !reflect.DeepEqual(ids, wantIDs) {
		t.Fatalf("source ids = %#v", ids)
	}
	severities := []Severity{SeverityInfo, SeverityWarning, SeverityError}
	wantSeverities := []Severity{"info", "warning", "error"}
	if !reflect.DeepEqual(severities, wantSeverities) {
		t.Fatalf("severities = %#v", severities)
	}
	ranks := []int{SeverityError.Rank(), SeverityWarning.Rank(), SeverityInfo.Rank(), Severity("").Rank(), Severity("critical").Rank()}
	wantRanks := []int{3, 2, 1, 0, 0}
	if !reflect.DeepEqual(ranks, wantRanks) {
		t.Fatalf("severity ranks = %#v", ranks)
	}
}

// TestTransitionMetadataJSONContract freezes the wire shape of the structured
// record. Restart reconciliation reads these keys off disk, so a rename orphans
// every retained wait.
func TestTransitionMetadataJSONContract(t *testing.T) {
	assertLaneJSONFixture(t, "testdata/transition.json", TransitionMetadata{
		Class:          TransitionWaiting,
		LaneKey:        "sidecar-sh-sidecar-4",
		DedupeKey:      "3f0a1b2c3d4e5f60718293a4:waiting",
		ReplacementKey: "3f0a1b2c3d4e5f60718293a4",
		ProjectRoot:    "/repos/sidecar",
	})

	// Only replacementKey and projectRoot are optional; class, laneKey and
	// dedupeKey are always written.
	minimal, err := json.Marshal(TransitionMetadata{Class: TransitionDone, LaneKey: "a", DedupeKey: "a:done"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(minimal), `{"class":"done","laneKey":"a","dedupeKey":"a:done"}`; got != want {
		t.Fatalf("minimal transition = %s, want %s", got, want)
	}
}

// TestLaneTrackerEmitsExactlyThreeNotifications pins the whole emission matrix:
// which settled lane, reached from which prior lane, produces which
// notification. Anything not listed here posts nothing at all, and that silence
// is as much a part of the contract as the three posts are.
func TestLaneTrackerEmitsExactlyThreeNotifications(t *testing.T) {
	start := time.Unix(1000, 0).UTC()
	tests := []struct {
		name         string
		prior        agentstatus.LaneID
		priorHealth  bool
		next         agentstatus.LaneID
		nextHealth   bool
		wantPost     bool
		wantSource   SourceID
		wantSeverity Severity
		wantClass    TransitionClass
		wantTitle    string
		wantSticky   bool
	}{
		// Blocked posts from every prior lane: a wait is a wait however it was
		// reached.
		{"working to blocked", agentstatus.LaneWorking, false, agentstatus.LaneBlocked, false, true, SourceWaiting, SeverityWarning, TransitionWaiting, "Shell 4 needs input", true},
		{"idle to blocked", agentstatus.LaneIdle, false, agentstatus.LaneBlocked, false, true, SourceWaiting, SeverityWarning, TransitionWaiting, "Shell 4 needs input", true},
		{"done to blocked", agentstatus.LaneDone, false, agentstatus.LaneBlocked, false, true, SourceWaiting, SeverityWarning, TransitionWaiting, "Shell 4 needs input", true},
		{"paused to blocked", agentstatus.LanePaused, false, agentstatus.LaneBlocked, false, true, SourceWaiting, SeverityWarning, TransitionWaiting, "Shell 4 needs input", true},

		// A paused lane carrying health is the failure signal, but only from a
		// lane that was live a moment ago.
		{"working to failed", agentstatus.LaneWorking, false, agentstatus.LanePaused, true, true, SourceSession, SeverityError, TransitionFailure, "Shell 4 session ended", false},
		{"blocked to failed", agentstatus.LaneBlocked, false, agentstatus.LanePaused, true, true, SourceSession, SeverityError, TransitionFailure, "Shell 4 session ended", false},
		{"done to failed", agentstatus.LaneDone, false, agentstatus.LanePaused, true, true, SourceSession, SeverityError, TransitionFailure, "Shell 4 session ended", false},
		{"idle to failed says nothing", agentstatus.LaneIdle, false, agentstatus.LanePaused, true, false, "", "", "", "", false},
		{"paused to failed says nothing", agentstatus.LanePaused, false, agentstatus.LanePaused, true, false, "", "", "", "", false},
		{"paused without health is not a failure", agentstatus.LaneWorking, false, agentstatus.LanePaused, false, false, "", "", "", "", false},

		// Done only counts as a finish when something was running.
		{"working to done", agentstatus.LaneWorking, false, agentstatus.LaneDone, false, true, SourceSession, SeverityInfo, TransitionDone, "Shell 4 finished", false},
		{"blocked to done", agentstatus.LaneBlocked, false, agentstatus.LaneDone, false, true, SourceSession, SeverityInfo, TransitionDone, "Shell 4 finished", false},
		{"idle to done says nothing", agentstatus.LaneIdle, false, agentstatus.LaneDone, false, false, "", "", "", "", false},
		{"paused to done says nothing", agentstatus.LanePaused, false, agentstatus.LaneDone, false, false, "", "", "", "", false},

		// Reaching working or idle is never news on its own.
		{"blocked to working says nothing", agentstatus.LaneBlocked, false, agentstatus.LaneWorking, false, false, "", "", "", "", false},
		{"working to idle says nothing", agentstatus.LaneWorking, false, agentstatus.LaneIdle, false, false, "", "", "", "", false},
		{"done to idle says nothing", agentstatus.LaneDone, false, agentstatus.LaneIdle, false, false, "", "", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := &LaneTracker{Debounce: time.Second}
			ev := commitLane(t, tr, laneRow("a", tt.prior, tt.priorHealth), laneRow("a", tt.next, tt.nextHealth), start)
			if !tt.wantPost {
				if len(ev.Post) != 0 {
					t.Fatalf("posted %#v", ev.Post)
				}
				return
			}
			if len(ev.Post) != 1 {
				t.Fatalf("posted %d notifications, want 1: %#v", len(ev.Post), ev.Post)
			}
			n := ev.Post[0]
			if n.Source != tt.wantSource || n.Severity != tt.wantSeverity || n.Title != tt.wantTitle || n.Sticky != tt.wantSticky {
				t.Fatalf("notification = %#v", n)
			}
			if n.Transition == nil || n.Transition.Class != tt.wantClass || n.Transition.LaneKey != "a" {
				t.Fatalf("transition = %#v", n.Transition)
			}
			if !n.CreatedAt.Equal(start.Add(2 * time.Second)) {
				t.Fatalf("CreatedAt = %v, want the committing round's clock", n.CreatedAt)
			}
			if n.Origin != laneRow("a", tt.next, tt.nextHealth).Origin {
				t.Fatalf("origin not carried: %#v", n.Origin)
			}
		})
	}
}

// TestLaneBodyAndNameComposition pins how a notification identifies its
// workspace. The body is the provider, the place and the resolver's evidence
// joined with a middle dot, with empty parts dropped rather than leaving stray
// separators.
func TestLaneBodyAndNameComposition(t *testing.T) {
	bodies := []struct {
		name string
		o    LaneObservation
		want string
	}{
		{"all three parts", LaneObservation{Provider: "codex", Context: "sidecar", Presentation: agentstatus.Presentation{Evidence: "codex.screen.blocked"}}, "codex · sidecar · codex.screen.blocked"},
		{"missing evidence", LaneObservation{Provider: "codex", Context: "sidecar"}, "codex · sidecar"},
		{"missing provider", LaneObservation{Context: "sidecar", Presentation: agentstatus.Presentation{Evidence: "e"}}, "sidecar · e"},
		{"whitespace counts as missing", LaneObservation{Provider: "  ", Context: "sidecar", Presentation: agentstatus.Presentation{Evidence: " "}}, "sidecar"},
		{"nothing at all", LaneObservation{}, ""},
	}
	for _, tt := range bodies {
		t.Run("body/"+tt.name, func(t *testing.T) {
			if got := laneBody(tt.o); got != tt.want {
				t.Fatalf("laneBody = %q, want %q", got, tt.want)
			}
		})
	}

	names := []struct {
		name string
		o    LaneObservation
		want string
	}{
		{"label wins", LaneObservation{Label: "Shell 4", Provider: "codex"}, "Shell 4"},
		{"provider is the fallback", LaneObservation{Provider: "codex"}, "codex"},
		{"blank label falls through", LaneObservation{Label: "   ", Provider: "codex"}, "codex"},
		{"last resort", LaneObservation{}, "Agent"},
	}
	for _, tt := range names {
		t.Run("name/"+tt.name, func(t *testing.T) {
			if got := laneName(tt.o); got != tt.want {
				t.Fatalf("laneName = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestLaneDedupeKeyShape pins the identity two processes must agree on for the
// same logical event. It is derived from the origin when there is one so a CLI
// post and a lane trigger about the same shell collide on purpose, and falls
// back to the lane key when the origin identifies nobody.
func TestLaneDedupeKeyShape(t *testing.T) {
	start := time.Unix(2000, 0).UTC()

	withOrigin := laneRow("a", agentstatus.LaneBlocked, false)
	tr := &LaneTracker{Debounce: time.Second}
	ev := commitLane(t, tr, laneRow("a", agentstatus.LaneWorking, false), withOrigin, start)
	stable := withOrigin.Origin.StableKey()
	if stable == "" {
		t.Fatal("fixture origin produced no stable key")
	}
	meta := ev.Post[0].Transition
	if meta.DedupeKey != stable+":waiting" || meta.ReplacementKey != stable {
		t.Fatalf("origin-keyed transition = %#v (stable %q)", meta, stable)
	}
	if meta.ProjectRoot != "/repos/sidecar" {
		t.Fatalf("project root = %q", meta.ProjectRoot)
	}

	anonymous := laneRow("b", agentstatus.LaneBlocked, false)
	anonymous.Origin = Origin{}
	prior := laneRow("b", agentstatus.LaneWorking, false)
	prior.Origin = Origin{}
	tr2 := &LaneTracker{Debounce: time.Second}
	ev = commitLane(t, tr2, prior, anonymous, start)
	meta = ev.Post[0].Transition
	if meta.DedupeKey != "b:waiting" || meta.ReplacementKey != "" {
		t.Fatalf("anonymous transition = %#v", meta)
	}
}

// TestLaneLifecycleRulesAreFrozen pins the four rules that decide whether a
// state change is news at all: the first sighting is a baseline, a flapping
// lane never settles, a settled lane fires once, and a workspace that leaves
// the observation set is forgotten entirely.
func TestLaneLifecycleRulesAreFrozen(t *testing.T) {
	t.Run("first sight of a key is a baseline", func(t *testing.T) {
		tr := &LaneTracker{Debounce: time.Second}
		now := time.Unix(3000, 0).UTC()
		ev := tr.Observe([]LaneObservation{
			laneRow("a", agentstatus.LaneBlocked, false),
			laneRow("b", agentstatus.LanePaused, true),
			laneRow("c", agentstatus.LaneDone, false),
		}, now)
		if !ev.Empty() {
			t.Fatalf("baseline posted %#v", ev)
		}
		if !tr.Knows("a") || tr.Knows("d") {
			t.Fatal("Knows did not follow the baseline")
		}
	})

	t.Run("a flapping lane never posts", func(t *testing.T) {
		tr := &LaneTracker{Debounce: 3 * time.Second}
		now := time.Unix(4000, 0).UTC()
		tr.Observe([]LaneObservation{laneRow("a", agentstatus.LaneWorking, false)}, now)
		for i := 1; i <= 10; i++ {
			lane := agentstatus.LaneBlocked
			if i%2 == 0 {
				lane = agentstatus.LaneWorking
			}
			if ev := tr.Observe([]LaneObservation{laneRow("a", lane, false)}, now.Add(time.Duration(i)*time.Second)); !ev.Empty() {
				t.Fatalf("flap at %ds posted %#v", i, ev)
			}
		}
	})

	t.Run("a settled lane posts once", func(t *testing.T) {
		tr := &LaneTracker{Debounce: time.Second}
		now := time.Unix(5000, 0).UTC()
		ev := commitLane(t, tr, laneRow("a", agentstatus.LaneWorking, false), laneRow("a", agentstatus.LaneDone, false), now)
		if len(ev.Post) != 1 {
			t.Fatalf("finish posted %#v", ev.Post)
		}
		for i := 0; i < 5; i++ {
			if ev := tr.Observe([]LaneObservation{laneRow("a", agentstatus.LaneDone, false)}, now.Add(time.Duration(10+i)*time.Second)); !ev.Empty() {
				t.Fatalf("repeat observation posted %#v", ev)
			}
		}
	})

	t.Run("leaving blocked always dismisses the wait", func(t *testing.T) {
		for _, next := range []agentstatus.LaneID{agentstatus.LaneWorking, agentstatus.LaneIdle, agentstatus.LaneDone, agentstatus.LanePaused} {
			tr := &LaneTracker{Debounce: time.Second}
			now := time.Unix(6000, 0).UTC()
			posted := commitLane(t, tr, laneRow("a", agentstatus.LaneWorking, false), laneRow("a", agentstatus.LaneBlocked, false), now)
			waitingID := posted.Post[0].ID

			later := now.Add(10 * time.Second)
			tr.Observe([]LaneObservation{laneRow("a", next, false)}, later)
			ev := tr.Observe([]LaneObservation{laneRow("a", next, false)}, later.Add(time.Second))
			if len(ev.Dismiss) != 1 || ev.Dismiss[0] != waitingID {
				t.Fatalf("leaving blocked for %q dismissed %#v, want %s", next, ev.Dismiss, waitingID)
			}
		}
	})

	t.Run("an absent key is dismissed and forgotten", func(t *testing.T) {
		tr := &LaneTracker{Debounce: time.Second}
		now := time.Unix(7000, 0).UTC()
		posted := commitLane(t, tr, laneRow("a", agentstatus.LaneWorking, false), laneRow("a", agentstatus.LaneBlocked, false), now)
		waitingID := posted.Post[0].ID

		ev := tr.Observe(nil, now.Add(30*time.Second))
		if len(ev.Dismiss) != 1 || ev.Dismiss[0] != waitingID || len(ev.Post) != 0 {
			t.Fatalf("vanish events = %#v", ev)
		}
		if tr.Knows("a") {
			t.Fatal("vanished key was retained")
		}
		// Coming back is a fresh baseline, so the same block does not replay.
		if ev := tr.Observe([]LaneObservation{laneRow("a", agentstatus.LaneBlocked, false)}, now.Add(time.Minute)); !ev.Empty() {
			t.Fatalf("returning workspace posted %#v", ev)
		}
	})

	t.Run("an observation with no key or no lane is ignored", func(t *testing.T) {
		tr := &LaneTracker{Debounce: time.Second}
		now := time.Unix(8000, 0).UTC()
		keyless := laneRow("", agentstatus.LaneBlocked, false)
		laneless := laneRow("b", "", false)
		if ev := tr.Observe([]LaneObservation{keyless, laneless}, now); !ev.Empty() {
			t.Fatalf("degenerate observations posted %#v", ev)
		}
		// A row with a key but no lane still counts as present, so it is not
		// treated as a vanished workspace, but it records no baseline either.
		if tr.Knows("") || tr.Knows("b") {
			t.Fatal("degenerate observation recorded state")
		}
	})
}
