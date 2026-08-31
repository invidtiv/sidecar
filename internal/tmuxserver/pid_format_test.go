package tmuxserver

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/testenv"
)

func TestMain(m *testing.M) {
	os.Exit(testenv.Main(m))
}

// TestListSessionsPIDFormatResolves confirms #{pid} expands in a list-sessions
// format string on an isolated socket. The default tmux server is never
// addressed: TestMain uses testenv.IsolateTmux.
func TestListSessionsPIDFormatResolves(t *testing.T) {
	testenv.RequireTmux(t)
	session := "tmuxserver-pid-probe"
	if out, err := exec.Command("tmux", "new-session", "-d", "-s", session).CombinedOutput(); err != nil {
		t.Fatalf("new-session on isolated socket: %v (%s)", err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "kill-session", "-t", session).Run()
	})

	out, err := exec.Command("tmux", "list-sessions", "-F", ListSessionsFormat).Output()
	if err != nil {
		t.Fatalf("list-sessions -F %q: %v", ListSessionsFormat, err)
	}
	line := strings.TrimSpace(string(out))
	name, pidField, ok := strings.Cut(line, "\t")
	if !ok {
		t.Fatalf("list-sessions output %q has no tab; #{pid} did not expand as a separate field", line)
	}
	if name != session {
		t.Fatalf("session name = %q, want %q", name, session)
	}
	pid, ok := ParsePID(pidField)
	if !ok {
		t.Fatalf("#{pid} did not resolve in list-sessions (field %q, line %q)", pidField, line)
	}
	if pid <= 0 {
		t.Fatalf("resolved pid %d, want > 0", pid)
	}
}
