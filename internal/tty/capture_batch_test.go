package tty

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/testenv"
	"github.com/marcus/sidecar/internal/tmuxenv"
)

func TestParseCaptureBatch(t *testing.T) {
	marker := func(i int) string { return "sidecar-capture-n " + strconv.Itoa(i) }
	frame := func(parts ...string) []byte {
		var b strings.Builder
		for i, part := range parts {
			b.WriteString(marker(i) + "\n" + part)
		}
		return []byte(b.String())
	}
	t.Run("complete", func(t *testing.T) {
		data := frame("sidecar-capture-n 1 lookalike\na\n", "", "b\n\n", "") // trailing "" is the closing marker
		got := parseCaptureBatch(data, 3, marker)
		want := map[int]string{0: "sidecar-capture-n 1 lookalike\na\n", 1: "", 2: "b\n\n"}
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i, text := range want {
			if got[i] != text {
				t.Fatalf("capture %d = %q, want %q", i, got[i], text)
			}
		}
	})
	t.Run("truncated at a failing pane", func(t *testing.T) {
		// tmux stops the list at the second capture: its opening marker is
		// printed, nothing follows.
		data := []byte(marker(0) + "\nfirst\n" + marker(1) + "\n")
		got := parseCaptureBatch(data, 3, marker)
		if len(got) != 1 || got[0] != "first\n" {
			t.Fatalf("got %v, want only the first capture", got)
		}
	})
	t.Run("no opening marker", func(t *testing.T) {
		if got := parseCaptureBatch([]byte("noise\n"), 1, marker); len(got) != 0 {
			t.Fatalf("got %v", got)
		}
	})
}

// A batched capture is byte-for-byte the per-pane capture, and a pane that
// vanished leaves it and every later pane to the per-pane fallback.
func TestCapturePaneOutputsMatchesSingleCaptures(t *testing.T) {
	run := privateTmuxServer(t)
	script := `printf '\033[31mred %s\033[0m\n%%3\n\n  padded  \n' "$0"; sleep 120`
	run("new-session", "-d", "-s", "batch", "-x", "60", "-y", "10", "sh", "-c", script, "one")
	panes := []string{run("display-message", "-p", "-t", "batch", "#{pane_id}")}
	for _, name := range []string{"two", "three"} {
		panes = append(panes, run("new-window", "-d", "-P", "-F", "#{pane_id}", "-t", "batch", "sh", "-c", script, name))
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		text, err := CapturePaneOutput(panes[2], 80)
		if err == nil && strings.Contains(text, "red three") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("panes never painted: %q %v", text, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	ctx := context.Background()
	got := CapturePaneOutputs(ctx, panes, 80)
	for _, pane := range panes {
		want, err := CapturePaneOutput(pane, 80)
		if err != nil {
			t.Fatal(err)
		}
		if got[pane] != want {
			t.Fatalf("batched %s = %q, single = %q", pane, got[pane], want)
		}
	}
	got = CapturePaneOutputs(ctx, []string{panes[0], "%999999", panes[1]}, 80)
	if _, ok := got[panes[0]]; !ok || len(got) != 1 {
		t.Fatalf("after a missing pane got %v, want only the pane before it", got)
	}
}

// privateTmuxServer points this test's tmux clients at a throwaway server and
// returns a runner for it. It never addresses the default server.
func privateTmuxServer(t *testing.T) func(args ...string) string {
	t.Helper()
	testenv.RequireTmux(t)
	root, err := os.MkdirTemp("/tmp", "sidecar-batch-capture-")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TMUX_TMPDIR", root)
	socket := tmuxenv.SocketPath()
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-S", socket, "kill-server").Run()
		_ = os.RemoveAll(root)
	})
	return func(args ...string) string {
		t.Helper()
		out, err := exec.Command("tmux", append([]string{"-S", socket}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
}

// The snapshot inspector answers exactly what InspectHeadlessTarget answers:
// the same identity for a one-pane current window (whatever other windows the
// session has), the same refusal for a split current window, and the same
// error for a session that does not exist.
func TestHeadlessTargetSnapshotMatchesInspect(t *testing.T) {
	run := privateTmuxServer(t)
	run("new-session", "-d", "-s", "single", "-x", "80", "-y", "24", "sleep 120")
	run("new-session", "-d", "-s", "windows", "-x", "80", "-y", "24", "sleep 120")
	run("new-window", "-d", "-t", "windows", "sleep 120")
	run("new-session", "-d", "-s", "split", "-x", "80", "-y", "24", "sleep 120")
	run("split-window", "-d", "-t", "split", "sleep 120")
	ctx := context.Background()
	inspect := HeadlessTargetSnapshot()
	for _, session := range []string{"single", "windows", "split", "missing", "sing"} {
		want, wantErr := InspectHeadlessTarget(ctx, session)
		got, gotErr := inspect(ctx, session)
		if got != want || (wantErr == nil) != (gotErr == nil) || (wantErr != nil && wantErr.Error() != gotErr.Error()) {
			t.Fatalf("%s: snapshot = %+v, %v; inspect = %+v, %v", session, got, gotErr, want, wantErr)
		}
	}
}

// A pane in copy mode and a retained dead pane batch to the same bytes a
// single capture reads, including content that imitates the framing.
func TestCapturePaneOutputsMatchesCopyModeAndDeadPanes(t *testing.T) {
	run := privateTmuxServer(t)
	run("new-session", "-d", "-s", "modes", "-x", "80", "-y", "24", "/bin/sh", "-c", "printf 'first\\nsidecar-capture-lookalike 1\\nsecond\\n'; sleep 120")
	live := run("display-message", "-p", "-t", "modes", "#{pane_id}")
	run("set-option", "-g", "remain-on-exit", "on")
	dead := run("new-window", "-d", "-P", "-F", "#{pane_id}", "-t", "modes", "/bin/sh", "-c", "printf 'dead pane\\n'; exit 0")
	deadline := time.Now().Add(3 * time.Second)
	for run("display-message", "-p", "-t", dead, "#{pane_dead}") != "1" {
		if time.Now().After(deadline) {
			t.Fatal("pane did not become dead")
		}
		time.Sleep(10 * time.Millisecond)
	}
	run("copy-mode", "-t", live)
	for _, lines := range []int{0, 80} {
		got := CapturePaneOutputs(context.Background(), []string{live, dead}, lines)
		for _, pane := range []string{live, dead} {
			want, err := CapturePaneOutput(pane, lines)
			if err != nil {
				t.Fatal(err)
			}
			if text, ok := got[pane]; !ok || text != want {
				t.Fatalf("lines=%d pane %s: batch=%q single=%q", lines, pane, text, want)
			}
		}
	}
}
