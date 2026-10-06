package workspace

import (
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/marcus/sidecar/internal/testenv"
)

func TestShellDiscoveryWithoutLocale(t *testing.T) {
	testenv.RequireTmux(t)
	// A persistent outer tmux server need not inherit the SSH/mosh client's
	// locale. Its children then replace C0 format separators with underscores.
	for _, name := range []string{"LANG", "LC_ALL", "LC_CTYPE"} {
		t.Setenv(name, "")
	}
	workDir := filepath.Join(t.TempDir(), "locale-discovery")
	session := "sidecar-sh-locale-discovery-1"
	if out, err := exec.Command("tmux", "new-session", "-d", "-s", session, "sleep", "300").CombinedOutput(); err != nil {
		t.Fatalf("create isolated session: %v (%s)", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", "="+session).Run() })

	names, server, err := discoverTmuxSessionNamesForWorkDir(workDir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names, []string{session}) || !server.IsPresent() {
		t.Fatalf("locale-free discovery = %q, server %v; want live session %q", names, server, session)
	}
}
