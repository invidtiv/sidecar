package tty

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Paste is delivered by tmux, so the only honest test of it is a real tmux
// server. Every command here — including teardown — carries an explicit -S
// pointing inside the test's own temp dir and runs with TMUX scrubbed, so
// nothing in this file can reach the developer's live sessions. A bare
// `kill-server` would trust the ambient environment and is never used.

type pasteTmux struct {
	t    *testing.T
	sock string
	root string
	conf string
}

// pasteTmuxConf keeps the throwaway server minimal and predictable.
const pasteTmuxConf = `set -g status off
set -g default-terminal "tmux-256color"
`

func startPasteTmux(t *testing.T) *pasteTmux {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping tmux integration in short mode")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	// t.TempDir's name embeds the test name, which overruns the ~104-byte unix
	// socket path limit; a short private temp dir stays under it.
	root, err := os.MkdirTemp("", "scpaste")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	sock := filepath.Join(root, "s")
	rel, err := filepath.Rel(root, sock)
	if err != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("refusing to use a socket outside the test temp dir: %q", sock)
	}
	if len(sock) > 100 {
		t.Fatalf("socket path too long for a unix socket: %q", sock)
	}
	conf := filepath.Join(root, "tmux.conf")
	if err := os.WriteFile(conf, []byte(pasteTmuxConf), 0o644); err != nil { //nolint:gosec
		t.Fatalf("write tmux.conf: %v", err)
	}
	s := &pasteTmux{t: t, sock: sock, root: root, conf: conf}
	t.Cleanup(func() { _ = s.cmd("kill-server").Run() })
	return s
}

func (s *pasteTmux) cmd(args ...string) *exec.Cmd {
	full := append([]string{"-f", s.conf, "-S", s.sock}, args...)
	c := exec.Command("tmux", full...) //nolint:gosec
	// TMUX is set when the suite itself runs inside tmux; left in place tmux
	// can resolve targets against that outer server.
	c.Env = append(os.Environ(), "TMUX=")
	return c
}

func (s *pasteTmux) run(args ...string) string {
	s.t.Helper()
	out, err := s.cmd(args...).CombinedOutput()
	if err != nil {
		s.t.Fatalf("tmux %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// sinkScript is the program under the pane: it puts the pty in raw mode (so the
// line discipline neither echoes, buffers, nor rewrites the bytes tmux sends),
// optionally announces bracketed paste, and copies its stdin to a file.
//
// Raw mode matters for the assertion: in canonical mode the trailing
// ESC[201~ would sit unread in the line discipline until a newline arrived,
// and the test would see a truncated paste that a real full-screen app
// (vim, an agent TUI) never sees.
// The READY marker is printed after the mode announcement and is what the test
// waits for: tmux consumes pane output in order, so once READY is on the
// captured screen tmux has already processed the ESC[?2004h ahead of it. There
// is no tmux format that reports bracketed-paste state — that is precisely why
// paste-buffer -p has to own the decision — so the marker is the only sound
// way to know the mode has landed.
const sinkScript = `#!/bin/sh
stty raw -echo 2>/dev/null
[ "$2" = "bracketed" ] && printf '\033[?2004h'
: > "$1"
printf 'READY\r\n'
cat >> "$1"
`

// startSink brings up a pane running sinkScript and returns the file its stdin
// lands in. bracketed selects whether the pane asks tmux for bracketed paste.
func (s *pasteTmux) startSink(session string, bracketed bool) string {
	s.t.Helper()
	out := filepath.Join(s.root, session+".out")
	script := filepath.Join(s.root, session+".sh")
	if err := os.WriteFile(script, []byte(sinkScript), 0o755); err != nil { //nolint:gosec
		s.t.Fatalf("write sink script: %v", err)
	}
	mode := "plain"
	if bracketed {
		mode = "bracketed"
	}
	s.run("new-session", "-d", "-s", session, "-x", "80", "-y", "24",
		"sh", script, out, mode)
	s.t.Cleanup(func() { _ = s.cmd("kill-session", "-t", session).Run() })

	// Wait until READY is on the pane's screen: by then tmux has processed the
	// mode announcement that preceded it, so the paste cannot race the mode.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(out); err == nil {
			screen, err := s.cmd("capture-pane", "-p", "-t", session).Output()
			if err == nil && strings.Contains(string(screen), "READY") {
				return out
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	s.t.Fatalf("sink for session %s never became ready", session)
	return ""
}

func waitForPasteContent(t *testing.T, path, want string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path) //nolint:gosec
		if err == nil {
			last = string(data)
			if last == want {
				return last
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pane never received the paste\n got: %q\nwant: %q", last, want)
	return last
}

func (s *pasteTmux) buffers() string {
	s.t.Helper()
	out, err := s.cmd("list-buffers").Output()
	if err != nil {
		// tmux exits non-zero with "no buffers" on some versions.
		return ""
	}
	return strings.TrimSpace(string(out))
}

// TestSendPasteToTmuxBracketedAndPlain is the acceptance criterion: one paste
// path serves both a bracketed-paste-aware application and a plain one, and
// which brackets appear is tmux's decision, not Sidecar's.
func TestSendPasteToTmuxBracketedAndPlain(t *testing.T) {
	srv := startPasteTmux(t)

	// Embedded LF (not CR) proves paste-buffer -r left line endings alone.
	const text = "line one\nline two"

	cases := []struct {
		name      string
		session   string
		bracketed bool
		want      string
	}{
		{
			name:    "plain shell receives the bytes untouched",
			session: "plain",
			want:    text,
		},
		{
			name:      "bracketed app receives tmux-inserted brackets",
			session:   "brack",
			bracketed: true,
			want:      BracketedPasteStart + text + BracketedPasteEnd,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := srv.startSink(tc.session, tc.bracketed)
			if err := sendPasteToTmuxSocket(srv.sock, tc.session, text); err != nil {
				t.Fatalf("paste: %v", err)
			}
			got := waitForPasteContent(t, out, tc.want)
			if !tc.bracketed && strings.Contains(got, BracketedPasteStart) {
				t.Errorf("plain pane got bracketed-paste codes: %q", got)
			}
			if strings.Contains(got, "\r") {
				t.Errorf("line endings were translated: %q", got)
			}
			if b := srv.buffers(); strings.Contains(b, "sidecar-paste-") {
				t.Errorf("paste buffer was not deleted: %q", b)
			}
		})
	}
}

// TestSendPasteToTmuxCleansUpAfterFailure covers the path -d does not: when
// paste-buffer fails, the buffer loaded a moment earlier must not be left
// behind on the user's server.
func TestSendPasteToTmuxCleansUpAfterFailure(t *testing.T) {
	srv := startPasteTmux(t)
	// Give the server something to exist for; without a session there is no
	// server to hold a leaked buffer at all.
	srv.startSink("alive", false)

	err := sendPasteToTmuxSocket(srv.sock, "no-such-session-9f3a", "hello")
	if err == nil {
		t.Fatal("expected an error pasting to a missing target")
	}
	if !IsSessionDeadError(err) {
		t.Errorf("error should be recognized as a dead session: %v", err)
	}
	if b := srv.buffers(); strings.Contains(b, "sidecar-paste-") {
		t.Errorf("failed paste leaked a buffer: %q", b)
	}
}

// TestNewPasteBufferNameUnique guards the property that keeps concurrent panes
// (and concurrent Sidecar instances) off each other's buffers.
func TestNewPasteBufferNameUnique(t *testing.T) {
	const workers, each = 8, 200
	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := make(map[string]bool, workers*each)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			names := make([]string, 0, each)
			for j := 0; j < each; j++ {
				names = append(names, newPasteBufferName())
			}
			mu.Lock()
			defer mu.Unlock()
			for _, n := range names {
				if seen[n] {
					t.Errorf("duplicate paste buffer name %q", n)
				}
				seen[n] = true
			}
		}()
	}
	wg.Wait()

	name := newPasteBufferName()
	if !strings.HasPrefix(name, "sidecar-paste-") {
		t.Errorf("name %q lost its identifying prefix", name)
	}
	if !strings.Contains(name, fmt.Sprintf("-%d-", os.Getpid())) {
		t.Errorf("name %q is not scoped to this process", name)
	}
	if strings.ContainsAny(name, " \t\"'$;\\") {
		t.Errorf("name %q contains characters tmux would not take plainly", name)
	}
}
