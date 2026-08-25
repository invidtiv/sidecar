package shellliveness

import (
	"errors"
	"fmt"
	"os/exec"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/tmuxserver"
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

	if tracker.Confirm(name, Gone, tracker.Incarnation(name), tracker.Server()) {
		t.Fatal("a shell this tracker never saw running was closed on a probe alone")
	}
	tracker.Observe(name)
	if !tracker.SeenAlive(name) {
		t.Fatal("Observe did not record liveness")
	}
	if !tracker.Confirm(name, Gone, tracker.Incarnation(name), tracker.Server()) {
		t.Fatal("a confirmed Gone verdict did not close the shell")
	}
}

func TestTrackerNeverClosesOnUnknown(t *testing.T) {
	tracker := NewTracker()
	const name = "sidecar-sh-demo-1"
	tracker.Observe(name)
	for i := 0; i < 5; i++ {
		if tracker.Confirm(name, Unknown, tracker.Incarnation(name), tracker.Server()) {
			t.Fatalf("Unknown verdict %d closed the shell", i)
		}
	}
	if tracker.Confirm(name, Alive, tracker.Incarnation(name), tracker.Server()) {
		t.Fatal("an Alive verdict closed the shell")
	}
}

// Suspicion must not accumulate across recoveries: two Unknowns either side of
// an Alive are not two thirds of a death.
func TestTrackerResetsSuspicionWhenTheSessionAnswers(t *testing.T) {
	tracker := &Tracker{Confirmations: 2}
	const name = "sidecar-sh-demo-1"
	tracker.Observe(name)
	if tracker.Confirm(name, Gone, tracker.Incarnation(name), tracker.Server()) {
		t.Fatal("closed after one Gone with two confirmations required")
	}
	tracker.Observe(name)
	if tracker.Confirm(name, Gone, tracker.Incarnation(name), tracker.Server()) {
		t.Fatal("an intervening Alive observation did not clear the count")
	}
	if !tracker.Confirm(name, Gone, tracker.Incarnation(name), tracker.Server()) {
		t.Fatal("two consecutive Gone verdicts did not close the shell")
	}
}

func TestTrackerThrottlesRepeatProbesButNeverTheFirst(t *testing.T) {
	tracker := &Tracker{ProbeInterval: time.Minute}
	const name = "sidecar-sh-demo-1"
	start := time.Unix(1000, 0)
	tracker.Observe(name)

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
	if tracker.Confirm(name, Gone, tracker.Incarnation(name), tracker.Server()) {
		t.Fatal("a forgotten name closed on a probe alone")
	}
}

// tmux names are reused. Pressing Enter on an offline row recreates the session
// under exactly its old name, and a verdict taken before that must not be
// allowed to delete the shell that came back (td-6a4100).
func TestVerdictAboutAPreviousLifeIsRefused(t *testing.T) {
	tracker := NewTracker()
	const name = "sidecar-sh-demo-1"
	tracker.Observe(name)

	// A probe starts here and reads the incarnation it is asking about.
	inFlight := tracker.Incarnation(name)

	// While it runs, the session is recreated under the same name.
	tracker.Observe(name)

	if tracker.Confirm(name, Gone, inFlight, tracker.Server()) {
		t.Fatal("a Gone verdict from before the resurrection closed a live shell")
	}
	// The current life can still be confirmed dead on its own evidence.
	if !tracker.Confirm(name, Gone, tracker.Incarnation(name), tracker.Server()) {
		t.Fatal("the resurrected shell can no longer be closed when it does die")
	}
}

// The liveness half of the gate lives in ShouldProbe so both surfaces get the
// same rule; a surface must not be able to probe a name it never saw alive by
// substituting its own notion of liveness.
func TestShouldProbeRequiresAnObservation(t *testing.T) {
	tracker := NewTracker()
	const name = "sidecar-sh-demo-1"
	if tracker.ShouldProbe(name, time.Unix(1000, 0)) {
		t.Fatal("a never-observed shell was probed")
	}
	tracker.Observe(name)
	if !tracker.ShouldProbe(name, time.Unix(1000, 0)) {
		t.Fatal("an observed shell was not probed")
	}
}

// A tmux server restart is not evidence that any one shell exited. Clearing
// seenAlive on the transition is what makes ShouldProbe and Confirm refuse
// without any other change to the per-shell rule (td-388929).
func TestObserveServerClearsLivenessOnIncarnationChange(t *testing.T) {
	tracker := &Tracker{Confirmations: 2}
	const name = "sidecar-sh-demo-1"
	first := tmuxserver.Present(1, 2, 3)
	second := tmuxserver.Present(9, 10, 11)

	tracker.ObserveServer(first)
	tracker.Observe(name)
	if tracker.Confirm(name, Gone, tracker.Incarnation(name), first) {
		t.Fatal("closed after one Gone with two confirmations required")
	}
	if !tracker.SeenAlive(name) {
		t.Fatal("precondition: the shell was seen alive on the first server")
	}

	tracker.ObserveServer(second)
	if tracker.SeenAlive(name) {
		t.Fatal("seenAlive survived a server restart")
	}
	if tracker.ShouldProbe(name, time.Unix(1000, 0)) {
		t.Fatal("ShouldProbe was true after a restart; no probe may be taken")
	}
	if tracker.Confirm(name, Gone, tracker.Incarnation(name), second) {
		t.Fatal("Confirm fired without a sighting under the new server")
	}

	// A gone count from the previous server must not complete a death on the
	// new one: the next life needs its own consecutive Gone verdicts.
	tracker.Observe(name)
	if tracker.Confirm(name, Gone, tracker.Incarnation(name), second) {
		t.Fatal("a gone count leaked across server incarnations")
	}
	if !tracker.Confirm(name, Gone, tracker.Incarnation(name), second) {
		t.Fatal("two Gone verdicts under the new server should still close")
	}
}

// Socket()/FromFileInfo leave pid 0 ("not observed yet"); discovery fills
// #{pid}. Those two observations are one server. Using == here would wipe
// every sighting on every discovery pass.
func TestObserveServerUsesEqualNotOperatorEqual(t *testing.T) {
	tracker := NewTracker()
	const name = "sidecar-sh-demo-1"
	sock := tmuxserver.Present(1, 2, 0)
	withPID := tmuxserver.Present(1, 2, 99)

	tracker.ObserveServer(sock)
	tracker.Observe(name)
	tracker.ObserveServer(withPID)
	if !tracker.SeenAlive(name) {
		t.Fatal("ObserveServer treated an unspecified pid as a new server; Equal is the predicate, not ==")
	}
	if sock == withPID {
		t.Fatal("precondition: == is field-wise and must stay so")
	}
}

// Sidecar running outside tmux survives a restart and sees the new server
// while the tracker already exists. The reset must fire on that call, not
// only if the tracker is constructed against the new incarnation.
func TestObserveServerResetsOnLiveTransitionNotOnlyConstruction(t *testing.T) {
	tracker := NewTracker()
	const name = "sidecar-sh-demo-1"
	first := tmuxserver.Present(1, 2, 3)
	second := tmuxserver.Present(9, 10, 11)

	tracker.ObserveServer(first)
	tracker.Observe(name)
	if !tracker.SeenAlive(name) {
		t.Fatal("precondition: observed under the first server")
	}

	tracker.ObserveServer(second)
	if tracker.SeenAlive(name) {
		t.Fatal("a live ObserveServer transition left seenAlive set; Sidecar outside tmux would still auto-close")
	}
	if tracker.Confirm(name, Gone, tracker.Incarnation(name), first) {
		t.Fatal("a verdict from the previous server closed a shell after the transition")
	}
}

// Confirm refuses a verdict tagged with a different server even when the
// name-life matches and the shell has been seen alive under the current
// server. That is the in-flight probe that listed an empty new server
// while still carrying the old incarnation (td-388929).
func TestConfirmRefusesVerdictFromAnotherServer(t *testing.T) {
	tracker := NewTracker()
	const name = "sidecar-sh-demo-1"
	first := tmuxserver.Present(1, 2, 3)
	second := tmuxserver.Present(9, 10, 11)

	tracker.ObserveServer(first)
	tracker.Observe(name)
	tracker.ObserveServer(second)
	tracker.Observe(name)

	if tracker.Confirm(name, Gone, tracker.Incarnation(name), first) {
		t.Fatal("a Gone verdict taken under a previous server closed a shell on the current one")
	}
	if !tracker.Confirm(name, Gone, tracker.Incarnation(name), second) {
		t.Fatal("a Gone verdict taken under the current server did not close the shell")
	}
}
