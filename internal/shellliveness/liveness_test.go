package shellliveness

import (
	"errors"
	"fmt"
	"os/exec"
	"testing"
	"time"
)

// The whole point of this package is an asymmetry: a missed death costs one
// stale row, a false death costs a user's shell. Every test below is a
// statement about which way an ambiguous signal must fall.

func TestSuspectsDeathNamesOneMissingSession(t *testing.T) {
	for _, message := range []string{
		"can't find session: sidecar-sh-demo-1",
		"can't find pane: sidecar-sh-demo-1",
		"no such session: sidecar-sh-demo-1",
		"session not found",
	} {
		if !SuspectsDeath(message) {
			t.Errorf("SuspectsDeath(%q) = false, want a probe", message)
		}
	}
}

// These two are the messages the real app actually produced when a shell was
// exited, and both were missing from the matcher that was supposed to notice.
// "no current target" is what tmux says when the target is a pane id that no
// longer resolves, which is every capture the embedded terminal makes after the
// pane dies.
func TestSuspectsDeathCoversTheMessagesTheAppActuallySees(t *testing.T) {
	for _, message := range []string{
		"capture pane range: can't find session: sidecar-sh-demo-1",
		"capture-pane: no current target",
		"pane evidence: no current target: sidecar-sh-demo-1",
	} {
		if !SuspectsDeath(message) {
			t.Errorf("SuspectsDeath(%q) = false, want a probe", message)
		}
	}
}

func TestSuspectsDeathRefusesServerWideFailures(t *testing.T) {
	// A server that is down says nothing about one session, and it is exactly
	// what a tmux restart looks like. Treating it as death would have deleted
	// every shell entry the user has.
	for _, message := range []string{
		"no server running on /private/tmp/tmux-501/default",
		"error connecting to /private/tmp/tmux-501/default (no such file or directory)",
		"lost server",
		"exec: \"tmux\": executable file not found in $PATH",
		"capture-pane: timeout after 2s",
	} {
		if SuspectsDeath(message) {
			t.Errorf("SuspectsDeath(%q) = true, want no probe", message)
		}
	}
}

// tmux's message almost never reaches err.Error(): exec.Cmd.Output() reports
// "exit status 1" and hides the reason in ExitError.Stderr. A matcher that
// forgets this matches nothing, which is how an exited shell used to survive.
func TestSuspectsDeathErrReadsStderrNotJustTheErrorString(t *testing.T) {
	exitErr := &exec.ExitError{Stderr: []byte("can't find pane: sidecar-sh-demo-1\n")}
	wrapped := fmt.Errorf("capture-pane: %w", exitErr)
	if wrapped.Error() == "" || SuspectsDeath(wrapped.Error()) {
		t.Fatalf("precondition: %q should carry no death marker on its own", wrapped.Error())
	}
	if !SuspectsDeathErr(wrapped) {
		t.Fatal("SuspectsDeathErr ignored the stderr tmux actually wrote")
	}
	if SuspectsDeathErr(nil) {
		t.Fatal("SuspectsDeathErr(nil) = true")
	}
	if SuspectsDeathErr(errors.New("capture-pane: no server running")) {
		t.Fatal("a server-wide failure was treated as one session's death")
	}
}

func TestTrackerClosesOnlyAConfirmedDeathOfAShellItSawAlive(t *testing.T) {
	tracker := NewTracker()
	const name = "sidecar-sh-demo-1"

	if tracker.Confirm(name, Gone) {
		t.Fatal("a shell this tracker never saw running was closed on a probe alone")
	}
	tracker.Observe(name)
	if !tracker.SeenAlive(name) {
		t.Fatal("Observe did not record liveness")
	}
	if !tracker.Confirm(name, Gone) {
		t.Fatal("a confirmed Gone verdict did not close the shell")
	}
}

func TestTrackerNeverClosesOnUnknown(t *testing.T) {
	tracker := NewTracker()
	const name = "sidecar-sh-demo-1"
	tracker.Observe(name)
	for i := 0; i < 5; i++ {
		if tracker.Confirm(name, Unknown) {
			t.Fatalf("Unknown verdict %d closed the shell", i)
		}
	}
	if tracker.Confirm(name, Alive) {
		t.Fatal("an Alive verdict closed the shell")
	}
}

// Suspicion must not accumulate across recoveries: two Unknowns either side of
// an Alive are not two thirds of a death.
func TestTrackerResetsSuspicionWhenTheSessionAnswers(t *testing.T) {
	tracker := &Tracker{Confirmations: 2}
	const name = "sidecar-sh-demo-1"
	tracker.Observe(name)
	if tracker.Confirm(name, Gone) {
		t.Fatal("closed after one Gone with two confirmations required")
	}
	tracker.Observe(name)
	if tracker.Confirm(name, Gone) {
		t.Fatal("an intervening Alive observation did not clear the count")
	}
	if !tracker.Confirm(name, Gone) {
		t.Fatal("two consecutive Gone verdicts did not close the shell")
	}
}

func TestTrackerThrottlesRepeatProbesButNeverTheFirst(t *testing.T) {
	tracker := &Tracker{ProbeInterval: time.Minute}
	const name = "sidecar-sh-demo-1"
	start := time.Unix(1000, 0)

	if !tracker.ShouldProbe(name, start) {
		t.Fatal("the first suspicion did not probe; a shell that just exited must close now")
	}
	if tracker.ShouldProbe(name, start.Add(time.Second)) {
		t.Fatal("a second probe one second later spawned tmux again")
	}
	if !tracker.ShouldProbe(name, start.Add(2*time.Minute)) {
		t.Fatal("the throttle never expired")
	}
}

func TestForgetLetsAReusedNameStartClean(t *testing.T) {
	tracker := NewTracker()
	const name = "sidecar-sh-demo-1"
	tracker.Observe(name)
	tracker.Forget(name)
	if tracker.SeenAlive(name) {
		t.Fatal("Forget left liveness behind")
	}
	if tracker.Confirm(name, Gone) {
		t.Fatal("a forgotten name closed on a probe alone")
	}
}
