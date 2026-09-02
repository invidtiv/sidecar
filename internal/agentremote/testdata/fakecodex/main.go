// Command fakecodex is an agent-shaped fixture process for the loopback proof.
//
// It is built as a binary literally named `codex` so a tmux pane running it
// reports `pane_current_command: codex` and `argv[0]: codex`, which is what
// agentactivity.Identify keys a positive provider identity on. Nothing here
// contacts a network or a paid provider: it prints the Codex chrome the real
// detector's rules already match, and treats each line on stdin as one turn.
//
// The screen it paints is deliberately taller than the widest detector window
// (the read window is the pane's own height, and the deepest region any codex
// rule reads inside it is twenty lines). Every repaint pushes the previous
// state's chrome out of every rule's region, so the pane's most recent state is
// the only one any rule can see. Without that, the working marker from turn one
// would still be inside the working region during turn two's idle, and the pane
// would read as permanently working.
//
// The chrome is Herdr's, not an approximation of it: since the Phase 2 manifest
// cutover the detector runs the vendored codex.toml, whose working rule is
// column-anchored as `^[•◦]\s+Working \([^)]*esc to interrupt\)(?: · .*)?$`.
// Real Codex separates the task from the timer with " · ", and so does this.
package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"
)

// fillerLines is comfortably more than the deepest region any codex rule reads.
const fillerLines = 24

func paint(lines ...string) {
	var b strings.Builder
	for i := 0; i < fillerLines; i++ {
		b.WriteString(".\n")
	}
	for _, line := range lines {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	_, _ = os.Stdout.WriteString(b.String())
}

func main() {
	// The startup header is what codexScreenIdentity matches when a pane's
	// command name is a shared runtime. It costs nothing to be identifiable by
	// both routes.
	paint("OpenAI Codex (v0.0.0-fake)", "› ")

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		paint(fmt.Sprintf("• Working (0s • esc to interrupt) · %s", line))
		// Long enough that a poller observes the working state before the turn
		// settles, short enough that a wait is not the slowest thing in the run.
		time.Sleep(400 * time.Millisecond)
		switch line {
		case "block":
			paint("Would you like to run the following command?", "  1. Yes", "  2. No")
		case "quit":
			return
		default:
			paint("› ")
		}
	}
}
