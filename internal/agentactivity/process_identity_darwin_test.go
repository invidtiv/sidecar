//go:build darwin

package agentactivity

import (
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

const orphanShellHelperEnv = "SIDECAR_TEST_ORPHAN_SHELL_HELPER"

// TestForegroundShellReadyIgnoresInitAdoptedDaemonLive is both the regression
// and its helper process. The helper starts a non-job-control background
// process, lets init adopt it, then execs zsh without changing PID or process
// group. That reproduces Git fsmonitor's process-table shape on a private tmux
// server: idle shell group leader plus an init-adopted group member.
func TestForegroundShellReadyIgnoresInitAdoptedDaemonLive(t *testing.T) {
	if os.Getenv(orphanShellHelperEnv) == "1" {
		if err := exec.Command("/bin/sh", "-c", "sleep 5 &").Run(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if err := syscall.Exec("/bin/zsh", []string{"zsh", "-df"}, os.Environ()); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}

	// tmux's Unix socket path has a small platform limit; Go's ordinary test
	// temp path includes the full test name and exceeds it on macOS.
	root, err := os.MkdirTemp("/tmp", "sc-fg-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	socket := filepath.Join(root, "tmux.sock")
	session := "orphan-shell-ready"
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("tmux", "-S", socket, "-f", "/dev/null", "new-session", "-d", "-s", session,
		binary, "-test.run=^TestForegroundShellReadyIgnoresInitAdoptedDaemonLive$")
	cmd.Env = append(os.Environ(), "TMUX=", orphanShellHelperEnv+"=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("start private tmux: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", socket, "kill-server").Run() })

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		out, inspectErr := exec.Command("tmux", "-S", socket, "display-message", "-p", "-t", session,
			"#{pane_pid}\t#{pane_current_command}").Output()
		if inspectErr == nil {
			parts := strings.Split(strings.TrimSpace(string(out)), "\t")
			if len(parts) == 2 && parts[1] == "zsh" {
				pid, parseErr := strconv.Atoi(parts[0])
				if parseErr == nil && hasInitAdoptedGroupMember(pid) && len(platformForegroundArgv0s(pid)) == 1 {
					if !ForegroundShellReady(pid, parts[1]) {
						t.Fatalf("idle zsh with an init-adopted group member was refused: pid=%d argv0s=%v", pid, platformForegroundArgv0s(pid))
					}
					return
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("private tmux pane did not reach the orphan-helper shell shape")
}

func hasInitAdoptedGroupMember(group int) bool {
	processes, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return false
	}
	for i := range processes {
		process := &processes[i]
		if int(process.Eproc.Pgid) == group && int(process.Eproc.Ppid) == 1 && int(process.Proc.P_pid) != group {
			return true
		}
	}
	return false
}

func TestDarwinProcessArgvLayoutParserPreservesExecAPath(t *testing.T) {
	data := make([]byte, 4)
	binary.NativeEndian.PutUint32(data, 3)
	data = append(data, []byte("/usr/local/bin/node\x00\x00\x00/Users/test/.local/bin/agent\x00--use-system-ca\x00index.js\x00")...)
	if got := parseDarwinProcessArgv0(data); got != "/Users/test/.local/bin/agent" {
		t.Fatalf("argv0 = %q", got)
	}
}

// TestForegroundAgentLiveProbe verifies a PID from an isolated tmux pane when
// explicitly supplied. Ordinary test runs skip it; the terminal fidelity proof
// harness uses it against installed agent CLIs without touching the main tmux
// server.
func TestForegroundAgentLiveProbe(t *testing.T) {
	raw := os.Getenv("SIDECAR_FOREGROUND_PROBE_PID")
	if raw == "" {
		t.Skip("set SIDECAR_FOREGROUND_PROBE_PID")
	}
	pid, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := os.Getenv("SIDECAR_FOREGROUND_PROBE_WANT")
	if got := ResolveForegroundAgent(pid); got != want {
		t.Fatalf("ResolveForegroundAgent(%d) = %q, want %q", pid, got, want)
	}
}
