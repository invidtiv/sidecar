package workspaceops

import (
	"strings"
	"testing"

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
