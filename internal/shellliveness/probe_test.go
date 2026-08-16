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
	if got := ProbeSession("sidecar-sh-demo-1"); got != Alive {
		t.Fatalf("ProbeSession = %v, want Alive", got)
	}
}

func TestProbeSessionReportsGoneWhenTheListingAnswersWithoutIt(t *testing.T) {
	fakeTmux(t, `printf 'other\nsomething-else\n'`)
	if got := ProbeSession("sidecar-sh-demo-1"); got != Gone {
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
	if got := ProbeSession("sidecar-sh-demo-1"); got != Gone {
		t.Fatalf("ProbeSession = %v, want Gone: the name was not in the listing", got)
	}
}

// Every way tmux can fail to answer is Unknown, including the one that means
// every session is gone: a server-wide loss must not delete every shell.
func TestProbeSessionReportsUnknownWhenTmuxCannotAnswer(t *testing.T) {
	fakeTmux(t, `echo "no server running on /tmp/tmux-501/default" >&2; exit 1`)
	if got := ProbeSession("sidecar-sh-demo-1"); got != Unknown {
		t.Fatalf("ProbeSession = %v, want Unknown", got)
	}
}

func TestProbeSessionRefusesAnEmptyName(t *testing.T) {
	fakeTmux(t, `printf 'anything\n'; exit 0`)
	if got := ProbeSession(""); got != Unknown {
		t.Fatalf("ProbeSession(\"\") = %v, want Unknown", got)
	}
}

// A wedged tmux must not pin the caller. In the project plugin the probe *is*
// the shell's poll chain for that round, so a probe that never returns freezes
// the row; in the global browser the throttle would launch replacements
// forever. The deadline turns that into ordinary no-evidence.
func TestProbeSessionTimesOutIntoUnknown(t *testing.T) {
	fakeTmux(t, `sleep 30`)
	done := make(chan Verdict, 1)
	start := time.Now()
	go func() { done <- ProbeSession("sidecar-sh-demo-1") }()
	select {
	case got := <-done:
		if got != Unknown {
			t.Fatalf("ProbeSession = %v, want Unknown after the deadline", got)
		}
		if elapsed := time.Since(start); elapsed > ProbeTimeout*3 {
			t.Fatalf("probe took %s, want it bounded near %s", elapsed, ProbeTimeout)
		}
	case <-time.After(ProbeTimeout * 4):
		t.Fatal("ProbeSession never returned against a wedged tmux")
	}
}
