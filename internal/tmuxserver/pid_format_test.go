package tmuxserver

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/testenv"
	"github.com/marcus/sidecar/internal/tmuxformat"
)

func TestMain(m *testing.M) {
	os.Exit(testenv.Main(m))
}

// TestListSessionsPIDFormatResolves confirms #{pid} expands in a list-sessions
// format string on an isolated socket. The default tmux server is never
// addressed: TestMain uses testenv.IsolateTmux.
func TestListSessionsPIDFormatResolves(t *testing.T) {
	testenv.RequireTmux(t)
	for _, name := range []string{"LANG", "LC_ALL", "LC_CTYPE"} {
		t.Setenv(name, "")
	}
	session := "tmuxserver-pid-probe"
	if out, err := exec.Command("tmux", "new-session", "-d", "-s", session).CombinedOutput(); err != nil {
		t.Fatalf("new-session on isolated socket: %v (%s)", err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "kill-session", "-t", session).Run()
	})

	out, err := exec.Command("tmux", tmuxformat.ClientArgs("list-sessions", "-F", ListSessionsFormat)...).Output()
	if err != nil {
		t.Fatalf("list-sessions -F %q: %v", ListSessionsFormat, err)
	}
	line := strings.TrimSpace(string(out))
	fields := tmuxformat.Split(line)
	if len(fields) != 2 {
		t.Fatalf("list-sessions output %q has %d fields; want session and pid", line, len(fields))
	}
	name, pidField := fields[0], fields[1]
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
