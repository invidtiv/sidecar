package workspaceops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/testenv"
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

func shellArgValue(args []string, key string) string {
	for i := 0; i+1 < len(args); i += 2 {
		if value, ok := strings.CutPrefix(args[i+1], key+"="); ok {
			return value
		}
	}
	return ""
}

func TestShellCommsIdentityIsDerivedFromNamespaceAndSession(t *testing.T) {
	t.Setenv("COMMS_SESSION", "stale-orchestrator")
	first := shellArgValue(ShellEnvArgs("sidecar-sh-comms-one", "Shell 1"), "COMMS_SESSION")
	if first == "" || first == os.Getenv("COMMS_SESSION") {
		t.Fatalf("COMMS_SESSION=%q, want a derived shell identity", first)
	}
	if renamed := shellArgValue(ShellEnvArgs("sidecar-sh-comms-one", "Renamed"), "COMMS_SESSION"); renamed != first {
		t.Fatalf("display-name change changed comms identity: %q != %q", renamed, first)
	}
	if second := shellArgValue(ShellEnvArgs("sidecar-sh-comms-two", "Shell 1"), "COMMS_SESSION"); second == first {
		t.Fatal("distinct shell sessions share a comms identity")
	}
	t.Setenv("TMUX_TMPDIR", filepath.Join(t.TempDir(), "another-server"))
	if otherServer := shellArgValue(ShellEnvArgs("sidecar-sh-comms-one", "Shell 1"), "COMMS_SESSION"); otherServer == first {
		t.Fatal("the same session name on distinct servers shares a comms identity")
	}
	if got := os.Getenv("COMMS_SESSION"); got != "stale-orchestrator" {
		t.Fatalf("creating a shell changed the caller's explicit comms identity to %q", got)
	}
}

func TestManagedShellCommsIdentityReachesFirstProcessAndFuturePanes(t *testing.T) {
	testenv.RequireTmux(t)
	t.Setenv("COMMS_SESSION", "stale-orchestrator")
	seed := "comms-env-seed"
	startThrowawaySession(t, seed, t.TempDir())
	probeDir := t.TempDir()
	probe := filepath.Join(probeDir, "probe.sh")
	script := "#!/bin/sh\nprintf '%s\\n' \"${COMMS_SESSION:-}\" > " + ShellQuote(probeDir) + "/\"$TMUX_PANE\"\nexec /bin/sh -i\n"
	if err := os.WriteFile(probe, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	for option, value := range map[string]string{"default-shell": "/bin/sh", "default-command": ShellQuote(probe)} {
		prior, err := tmuxTest(t, "show-option", "-gv", option)
		if err != nil {
			t.Fatal(err)
		}
		if out, err := tmuxTest(t, "set-option", "-g", option, value); err != nil {
			t.Fatalf("configure probe: %s: %v", out, err)
		}
		t.Cleanup(func() { _, _ = tmuxTest(t, "set-option", "-g", option, prior) })
	}
	readPane := func(pane string) string {
		t.Helper()
		path := filepath.Join(probeDir, pane)
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if data, err := os.ReadFile(path); err == nil && strings.HasSuffix(string(data), "\n") {
				return strings.TrimSuffix(string(data), "\n")
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("first process in pane %s did not publish its environment", pane)
		return ""
	}
	identities := map[string]bool{}
	for _, session := range []string{"sidecar-sh-comms-live-one", "sidecar-sh-comms-live-two"} {
		result, err := CreateShell(ShellSpec{SessionName: session, DisplayName: "Comms proof", WorkDir: probeDir})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = tmuxTest(t, "kill-session", "-t", "="+session) })
		first := readPane(result.PaneID)
		if first == "" || first == "stale-orchestrator" || identities[first] {
			t.Fatalf("first process COMMS_SESSION=%q, want a distinct derived identity", first)
		}
		identities[first] = true
		// Recreating the session environment must use the same contract rather
		// than the stale identity inherited by Sidecar's own process.
		if out, err := tmuxTest(t, "set-environment", "-t", session, "COMMS_SESSION", "stale-recreate"); err != nil {
			t.Fatalf("seed stale recreate environment: %s: %v", out, err)
		}
		SetShellEnv(session, "Renamed")
		pane, err := tmuxTest(t, "split-window", "-d", "-t", session, "-P", "-F", "#{pane_id}")
		if err != nil {
			t.Fatal(err)
		}
		if got := readPane(pane); got != first {
			t.Fatalf("future pane COMMS_SESSION=%q, want %q", got, first)
		}
		// Ask the already-running shell again: changing tmux's environment
		// must not retarget an agent or an explicit comms invocation in it.
		later := "after-" + result.PaneID
		command := "printf '%s\\n' \"$COMMS_SESSION\" > " + ShellQuote(filepath.Join(probeDir, later))
		if out, err := tmuxTest(t, "send-keys", "-l", "-t", result.PaneID, command); err != nil {
			t.Fatalf("read running shell environment: %s: %v", out, err)
		}
		if out, err := tmuxTest(t, "send-keys", "-t", result.PaneID, "Enter"); err != nil {
			t.Fatalf("submit running shell probe: %s: %v", out, err)
		}
		if got := readPane(later); got != first {
			t.Fatalf("running pane identity changed: %q != %q", got, first)
		}
	}
	if got := os.Getenv("COMMS_SESSION"); got != "stale-orchestrator" {
		t.Fatalf("managed shell changed caller identity to %q", got)
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
