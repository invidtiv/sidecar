package shellliveness

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// ProbeSession is the only thing in this package that can cause a deletion, and
// every other test stubs it out. These exercise the real function against a
// fake tmux on PATH.
//
// No test here starts, contacts, or needs a tmux server: the fake is a shell
// script, so nothing can reach the developer's live sessions.

// The deadline every test that is not about the deadline uses. It is far past
// anything a fake tmux script needs, so a loaded machine cannot turn a test
// about tmux's answer into a test about the clock. These tests still finish as
// fast as the script does — nothing here waits for this bound.
const answerTimeout = 10 * time.Minute

// The deadline the timeout tests use. The fake tmux they run against never
// finishes on its own, so returning at all is the property under test and the
// exact value only decides how fast the test is.
const wedgedTimeout = 50 * time.Millisecond

// fakeTmux puts a script named tmux at the front of PATH for one test. body is
// the script after the shebang; it can inspect "$@" and must exit with the
// status it wants to report.
func fakeTmux(t *testing.T, body string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake tmux script needs a POSIX shell")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake tmux: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestProbeSessionReportsAliveWhenTheNameIsListed(t *testing.T) {
	fakeTmux(t, `printf 'other\nsidecar-sh-demo-1\nmore\n'`)
	if got := probeSessionWithin("sidecar-sh-demo-1", answerTimeout); got != Alive {
		t.Fatalf("ProbeSession = %v, want Alive", got)
	}
}

func TestProbeSessionReportsGoneWhenTheListingAnswersWithoutIt(t *testing.T) {
	fakeTmux(t, `printf 'other\nsomething-else\n'`)
	if got := probeSessionWithin("sidecar-sh-demo-1", answerTimeout); got != Gone {
		t.Fatalf("ProbeSession = %v, want Gone", got)
	}
}

// The trap the implementation exists to avoid. tmux target resolution falls
// back to prefix matching, so `has-session -t sidecar-sh-demo-1` answers yes
// while only sidecar-sh-demo-11 exists — and a probe that says Alive for a
// session the caller did not name is a shell that never closes. The listing
// compare is exact, so this must be Gone.
func TestProbeSessionDoesNotMatchAPrefixOfAnotherSession(t *testing.T) {
	fakeTmux(t, `printf 'sidecar-sh-demo-11\nsidecar-sh-demo-12\n'`)
	if got := probeSessionWithin("sidecar-sh-demo-1", answerTimeout); got != Gone {
		t.Fatalf("ProbeSession = %v, want Gone: the name was not in the listing", got)
	}
}

// Every way tmux can fail to answer is Unknown, including the one that means
// every session is gone: a server-wide loss must not delete every shell.
func TestProbeSessionReportsUnknownWhenTmuxCannotAnswer(t *testing.T) {
	fakeTmux(t, `echo "no server running on /tmp/tmux-501/default" >&2; exit 1`)
	if got := probeSessionWithin("sidecar-sh-demo-1", answerTimeout); got != Unknown {
		t.Fatalf("ProbeSession = %v, want Unknown", got)
	}
}

func TestProbeSessionRefusesAnEmptyName(t *testing.T) {
	fakeTmux(t, `printf 'anything\n'; exit 0`)
	if got := probeSessionWithin("", answerTimeout); got != Unknown {
		t.Fatalf("ProbeSession(\"\") = %v, want Unknown", got)
	}
}

// A wedged tmux must not pin the caller. In the project plugin the probe *is*
// the shell's poll chain for that round, so a probe that never returns freezes
// the row; in the global browser the throttle would launch replacements
// forever. The deadline turns that into ordinary no-evidence.
//
// The fake tmux here never exits on its own, so the only way this test can
// finish is the deadline: returning at all is the whole assertion, and no
// amount of load can make a probe that works look like one that does not.
func TestProbeSessionTimesOutIntoUnknown(t *testing.T) {
	fakeTmux(t, `sleep 3600`)
	assertProbeReturnsUnknown(t, "sidecar-sh-demo-1")
}

// The reason the wait delay exists, and the property that must survive any
// change to how the deadline is applied. Cancelling the context kills tmux, but
// Output() waits on the pipes, and a grandchild that inherited them keeps them
// open long after its parent is reaped. The fake below is exactly that shape —
// tmux exits immediately, its background child holds stdout for an hour — so
// without cmd.WaitDelay the context deadline is advisory and this probe never
// returns.
func TestProbeSessionReturnsWhenAGrandchildHoldsThePipes(t *testing.T) {
	fakeTmux(t, "sleep 3600 &\nexit 0")
	assertProbeReturnsUnknown(t, "sidecar-sh-demo-1")
}

// assertProbeReturnsUnknown runs a probe against a fake tmux that will not
// finish and requires it to come back Unknown. The guard is a generous
// backstop against a hang, not a measurement: it is never reached unless the
// probe is genuinely unbounded.
func assertProbeReturnsUnknown(t *testing.T, session string) {
	t.Helper()
	done := make(chan Verdict, 1)
	go func() { done <- probeSessionWithin(session, wedgedTimeout) }()
	select {
	case got := <-done:
		if got != Unknown {
			t.Fatalf("probe = %v, want Unknown after the deadline", got)
		}
	case <-time.After(2 * time.Minute):
		t.Fatal("probe never returned against a wedged tmux")
	}
}
