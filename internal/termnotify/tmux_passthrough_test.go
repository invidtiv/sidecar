package termnotify

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tmux is a real terminal emulator with a real escape parser, which makes it
// the one harness available in a unit test that can answer "would this text
// break out of the sequence carrying it". These tests write encoder output into
// a tmux pane and read the pane grid back: anything that reaches the grid
// escaped the sequence.
//
// Every tmux command here names its socket with -S under a private
// TMUX_TMPDIR, and the only server ever addressed is the one the test created.
// The machine's default tmux server holds live Sidecar sessions and is never
// touched.

const (
	// startSentinel and endSentinel bracket the payload so the reader can tell
	// "the sequence produced nothing" from "the pane has not been written yet".
	startSentinel = "SIDECAR-BEGIN"
	endSentinel   = "SIDECAR-END"
	// leakSentinel appears only inside notification text. Seeing it on the pane
	// grid means the text escaped its sequence and was printed.
	leakSentinel = "TERMNOTIFYLEAK"
)

func TestTmuxNeverPrintsAnEncodedNotification(t *testing.T) {
	server := startIsolatedTmux(t)

	adversarial := Notification{
		ID:    "n-01",
		Title: "Agent needs input \x1b\\" + leakSentinel,
		Body:  "sidecar \x07" + leakSentinel + " \x1b]0;" + leakSentinel + "\x07",
	}
	benign := Notification{ID: "n-02", Title: "Agent needs input", Body: "sidecar · main"}

	for _, term := range Supported() {
		for _, tmux := range []bool{false, true} {
			for _, n := range []Notification{benign, adversarial} {
				sequence, err := Encode(term, n, tmux)
				if err != nil {
					t.Fatalf("%s/tmux=%v: Encode() error = %v", term, tmux, err)
				}
				pane := server.paint(t, sequence)
				if strings.Contains(pane, leakSentinel) {
					t.Errorf("%s/tmux=%v: notification text reached the pane grid: %q", term, tmux, pane)
				}
				if got := strings.TrimSpace(pane); got != startSentinel+endSentinel {
					t.Errorf("%s/tmux=%v: pane grid = %q, want only the sentinels", term, tmux, got)
				}
			}
		}
	}
}

// TestTmuxPrintsAnUnsanitizedBreakout is the control for the test above. Both
// of these hand-built sequences are what the encoder would produce if it
// skipped a step, and tmux prints the payload of both — so a clean pane grid in
// the test above is evidence rather than an artefact of tmux swallowing
// everything it does not recognise.
func TestTmuxPrintsAnUnsanitizedBreakout(t *testing.T) {
	server := startIsolatedTmux(t)

	tests := []struct {
		name     string
		sequence string
	}{
		{
			// A string terminator passed through into an OSC 9 body.
			name:     "unsanitized text",
			sequence: "\x1b]9;title: \x1b\\" + leakSentinel + "\x07",
		},
		{
			// tmux passthrough with the embedded ESC left undoubled, which ends
			// the passthrough at the first one.
			name:     "tmux passthrough without doubled escapes",
			sequence: "\x1bPtmux;\x1b]9;title: \x1b\\" + leakSentinel + "\x07\x1b\\",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if pane := server.paint(t, tt.sequence); !strings.Contains(pane, leakSentinel) {
				t.Errorf("pane grid = %q, want the leaked text; the harness cannot detect a breakout", pane)
			}
		})
	}
}

type tmuxServer struct {
	socket  string
	tmpDir  string
	session int
}

// startIsolatedTmux brings up a private tmux server with passthrough enabled
// and registers its teardown.
func startIsolatedTmux(t *testing.T) *tmuxServer {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	// A short temp dir rather than t.TempDir: a unix socket path is capped at
	// around a hundred bytes, and a path built from the test's own name is
	// already over it on macOS.
	dir, err := os.MkdirTemp("", "termnotify")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	server := &tmuxServer{socket: filepath.Join(dir, "sock"), tmpDir: dir}

	// A holder session keeps the server alive between cases, and exists before
	// allow-passthrough is set so no case can race the option.
	server.run(t, "new-session", "-d", "-s", "holder", "--", "sleep", "600")
	t.Cleanup(func() {
		// -S names the file this test created, so the blast radius cannot leave
		// the temp dir even if the environment has been disturbed. The kill runs
		// before the directory goes, because removing the socket first would
		// leave a surviving server with no path any tmux command can address.
		_ = exec.Command("tmux", "-S", server.socket, "kill-server").Run()
		_ = os.RemoveAll(dir)
	})
	server.run(t, "set", "-g", "allow-passthrough", "on")
	return server
}

// paint writes one sequence into a fresh pane and returns the resulting grid.
func (s *tmuxServer) paint(t *testing.T, sequence string) string {
	t.Helper()
	s.session++
	name := fmt.Sprintf("case%d", s.session)

	payload := filepath.Join(s.tmpDir, name)
	if err := os.WriteFile(payload, []byte(startSentinel+sequence+endSentinel), 0o600); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	// cat rather than send-keys: the bytes reach the pane exactly as written,
	// with no shell prompt or quoting in the way.
	s.run(t, "new-session", "-d", "-x", "80", "-y", "24", "-s", name, "--",
		"sh", "-c", "cat "+payload+"; sleep 300")

	deadline := time.Now().Add(10 * time.Second)
	var pane string
	for {
		pane = s.capture(t, name)
		if strings.Contains(pane, endSentinel) || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(pane, endSentinel) {
		t.Fatalf("pane never received the payload: %q", pane)
	}
	return pane
}

func (s *tmuxServer) capture(t *testing.T, session string) string {
	t.Helper()
	out, err := s.output(t, "capture-pane", "-p", "-t", session)
	if err != nil {
		t.Fatalf("capture-pane: %v", err)
	}
	return out
}

func (s *tmuxServer) run(t *testing.T, args ...string) {
	t.Helper()
	if out, err := s.output(t, args...); err != nil {
		t.Fatalf("tmux %v: %v: %s", args, err, out)
	}
}

func (s *tmuxServer) output(t *testing.T, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tmux", append([]string{"-S", s.socket}, args...)...)
	// TMUX and TMUX_PANE name the developer's own server and pane. Left in
	// place, tmux can resolve a target against the outer server instead of the
	// private one this test created.
	cmd.Env = append(os.Environ(), "TMUX=", "TMUX_PANE=", "TMUX_TMPDIR="+s.tmpDir)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
