// Command holdpane makes a tmux pane look — and identify — like a real agent
// pane, so the Phase 0 spike can exercise the genuine detection stack without
// spending tokens or needing every provider authenticated on the host.
//
// It prints a captured agent screen and then blocks forever on stdin.
//
// Both halves matter. Painting the screen is the obvious half. The other half
// is the process name: every provider detector refuses a pane whose
// #{pane_current_command} is not one of the names it expects — DetectOpenCode
// wants literally "opencode", DetectClaude wants claude/node/bun/<semver> — so
// a pane running bash is reported as a process mismatch no matter what is
// drawn in it. Deploying this binary under the name the fixture recorded is
// what makes the pane identify correctly.
//
// Three things that look like simpler alternatives are not:
//
//   - Copying /bin/sh to the target name fails on Apple Silicon: the copy
//     loses its code signature and macOS refuses to exec it at all.
//   - Ad-hoc re-signing that copy makes it exec, but the pane still reports
//     "bash".
//   - Symlinking resolves to the real binary's name for the same reason.
//
// A distinct binary has none of those problems, and it behaves identically on
// a Linux host, which is what remote hosts will often be.
//
// Blocking on stdin rather than sleeping is also deliberate: a trailing sleep
// would make the pane's foreground process "sleep", undoing the whole point.
package main

import (
	"io"
	"os"
)

func main() {
	if len(os.Args) > 1 {
		if data, err := os.ReadFile(os.Args[1]); err == nil {
			_, _ = os.Stdout.Write(data)
		}
	}
	// A tmux pane's stdin is a tty that never reaches EOF, so this parks the
	// process in the foreground for the life of the pane.
	_, _ = io.Copy(io.Discard, os.Stdin)
}
