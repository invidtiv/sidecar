package workspaceops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/shellstate"
)

// A shell publishes its own identity into its session environment, which is how
// a pane can name itself without asking Sidecar for it.
func TestShellEnvArgsPublishIdentity(t *testing.T) {
	args := ShellEnvArgs("sidecar-sh-demo-3", "Shell 3")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, shellstate.NameEnv+"=Shell 3") {
		t.Fatalf("args = %v, want display name", args)
	}
	if !strings.Contains(joined, shellstate.SessionEnv+"=sidecar-sh-demo-3") {
		t.Fatalf("args = %v, want session name", args)
	}
	for i := 0; i < len(args); i += 2 {
		if args[i] != "-e" {
			t.Fatalf("args = %v, want each value preceded by -e", args)
		}
	}
}

func TestShellNamesStayProjectScoped(t *testing.T) {
	one := []shellstate.Definition{{TmuxName: "sidecar-sh-one-8"}}
	two := []shellstate.Definition{{TmuxName: "sidecar-sh-two-2"}, {TmuxName: "unrelated-99"}}
	if display, session := ShellNames("/tmp/one", one); display != "Shell 9" || session != "sidecar-sh-one-9" {
		t.Fatalf("one = %q/%q", display, session)
	}
	if display, session := ShellNames("/tmp/two", two); display != "Shell 3" || session != "sidecar-sh-two-3" {
		t.Fatalf("two = %q/%q", display, session)
	}
}

func TestForgetAndRestoreManagedShellTombstone(t *testing.T) {
	root := t.TempDir()
	dir, err := projectdir.Resolve(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "shells.json")
	created := time.Now().UTC().Truncate(time.Second)
	def := shellstate.Definition{
		TmuxName: "sidecar-sh-demo-1", DisplayName: "prior task", Namespace: "/tmp/socket",
		CreatedAt: created, AgentType: "codex", SkipPerms: true, WorkDir: root,
	}
	if err := shellstate.AddAtPath(path, def); err != nil {
		t.Fatal(err)
	}

	if err := ForgetManagedShell(root, def.TmuxName, def.Namespace, time.Time{}); err != nil {
		t.Fatal(err)
	}
	live, err := shellstate.ListAtPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 0 {
		t.Fatalf("live after forget = %+v", live)
	}
	tombs, err := shellstate.ListTombstonesAtPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(tombs) != 1 || tombs[0].DisplayName != "prior task" || tombs[0].AgentType != "codex" || !tombs[0].SkipPerms {
		t.Fatalf("tombstones = %+v", tombs)
	}

	got, err := RestoreManagedShell(root, def.TmuxName, def.Namespace)
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != def.DisplayName || got.AgentType != def.AgentType || got.SkipPerms != def.SkipPerms || got.WorkDir != def.WorkDir {
		t.Fatalf("restored = %+v", got)
	}
	live, err = shellstate.ListAtPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 1 || live[0].TmuxName != def.TmuxName {
		t.Fatalf("live after restore = %+v", live)
	}
	tombs, err = shellstate.ListTombstonesAtPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(tombs) != 0 {
		t.Fatalf("tombstones after restore = %+v", tombs)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}
