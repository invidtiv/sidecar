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
				if parseErr == nil && hasInitAdoptedGroupMember(pid) && len(platformForegroundProcesses(pid)) == 1 {
					if !ForegroundShellReady(pid, parts[1]) {
						t.Fatalf("idle zsh with an init-adopted group member was refused: pid=%d job=%v", pid, platformForegroundProcesses(pid))
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
	argv := parseDarwinProcessArgv(data)
	if len(argv) == 0 || argv[0] != "/Users/test/.local/bin/agent" {
		t.Fatalf("argv = %q, want the exec -a argv[0] first", argv)
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

// buildProcargs2 assembles the kern.procargs2 layout the kernel produces:
// [argc][exec path\0][NUL padding][argv...][env...]. It mirrors upstream's
// `build_procargs2` test helper (src/platform/macos.rs at d08e4468).
func buildProcargs2(execPath string, argv, env []string) []byte {
	buf := make([]byte, 4)
	binary.NativeEndian.PutUint32(buf, uint32(len(argv)))
	buf = append(buf, execPath...)
	buf = append(buf, 0, 0, 0)
	for _, arg := range argv {
		buf = append(buf, arg...)
		buf = append(buf, 0)
	}
	for _, entry := range env {
		buf = append(buf, entry...)
		buf = append(buf, 0)
	}
	return buf
}

// TestDarwinProcargs2ArgvExcludesTheEnvironment is upstream's
// procargs2_argv_excludes_environment_entries. argv and the environment are
// adjacent in the buffer with nothing between them but argc, so a parser that
// read to the end would report every environment variable as an argument — and
// PATH on a stock macOS contains the string "codex.system", which would then
// name a provider that is not running.
func TestDarwinProcargs2ArgvExcludesTheEnvironment(t *testing.T) {
	buf := buildProcargs2("/usr/bin/node",
		[]string{"node", "/Users/can/.local/bin/pi"},
		[]string{
			"PATH=/usr/bin:/var/run/com.apple.security.cryptexd/codex.system/bootstrap/usr/bin",
			"TERM=tmux-256color",
		})

	argv := parseDarwinProcessArgv(buf)
	if len(argv) != 2 || argv[0] != "node" || argv[1] != "/Users/can/.local/bin/pi" {
		t.Fatalf("argv = %q, want the two arguments and nothing else", argv)
	}
	if strings.Contains(strings.Join(argv, " "), "codex.system") {
		t.Fatalf("argv leaked the environment: %q", argv)
	}
}

// TestDarwinProcargs2ArgvSurvivesAPathWithSpaces is why this parser exists at
// all rather than a `ps` command-text split.
func TestDarwinProcargs2ArgvSurvivesAPathWithSpaces(t *testing.T) {
	buf := buildProcargs2("/usr/bin/node",
		[]string{"/Applications/My Tools/node", "/Users/can/My Scripts/qwen"},
		[]string{"PATH=/usr/bin"})
	argv := parseDarwinProcessArgv(buf)
	if len(argv) != 2 || argv[1] != "/Users/can/My Scripts/qwen" {
		t.Fatalf("argv = %q, want the spaced path intact", argv)
	}
}

// TestDarwinProcargs2EnvironReadsTheHintAfterArgv is upstream's
// procargs2_env_reads_agent_hint_after_argv and
// procargs2_env_does_not_treat_argv_as_environment, together. The pair is the
// point: a wrapper whose *arguments* mention SIDECAR_AGENT has not exported
// anything, and reading argv as environment would let any command line claim to
// be any agent.
func TestDarwinProcargs2EnvironReadsTheHintAfterArgv(t *testing.T) {
	exported := buildProcargs2("/opt/homebrew/bin/sandbox",
		[]string{"sandbox", "run", "SIDECAR_AGENT=codex", "--", "claude"},
		[]string{"PATH=/usr/bin", "SIDECAR_AGENT=claude", "TERM=xterm-256color"})
	if got := parseAgentEnvHint(parseDarwinProcessEnviron(exported)); got != "claude" {
		t.Fatalf("exported hint = %q, want claude (the environment, not the argument)", got)
	}

	argumentOnly := buildProcargs2("/opt/homebrew/bin/sandbox",
		[]string{"sandbox", "run", "SIDECAR_AGENT=claude"},
		[]string{"PATH=/usr/bin"})
	if got := parseAgentEnvHint(parseDarwinProcessEnviron(argumentOnly)); got != "" {
		t.Fatalf("argument-only hint = %q, want no hint", got)
	}
}

// TestDarwinProcargs2RejectsTruncatedBuffers: sysctl can hand back a short or
// malformed buffer for a process that exits mid-read, and neither parser may
// index past it.
func TestDarwinProcargs2RejectsTruncatedBuffers(t *testing.T) {
	full := buildProcargs2("/usr/bin/node", []string{"node", "/usr/local/bin/pi"}, []string{"PATH=/usr/bin"})
	for _, bad := range [][]byte{nil, {}, {1, 2, 3}, full[:4], full[:6], full[:len(full)-40]} {
		if argv := parseDarwinProcessArgv(bad); len(argv) == 2 && argv[1] == "/usr/local/bin/pi" {
			t.Fatalf("a truncated buffer parsed as a complete argv: %q", argv)
		}
		if hint := parseAgentEnvHint(parseDarwinProcessEnviron(bad)); hint != "" {
			t.Fatalf("a truncated buffer produced a hint: %q", hint)
		}
	}
	// A zero argc is a process the kernel has nothing to say about, not an
	// empty argv.
	zero := make([]byte, 4)
	if argv := parseDarwinProcessArgv(zero); argv != nil {
		t.Fatalf("argc 0 = %q, want nil", argv)
	}
}

// TestDarwinCommIsTheNulTerminatedPrefix: p_comm is a fixed-width field padded
// with NULs, and carrying the padding into processPriority would make every
// comparison against a resolved name fail, silently promoting every process to
// the top rung.
func TestDarwinCommIsTheNulTerminatedPrefix(t *testing.T) {
	padded := make([]byte, 17)
	copy(padded, "node")
	if got := darwinComm(padded); got != "node" {
		t.Fatalf("darwinComm = %q, want node", got)
	}
	if got := darwinComm(make([]byte, 17)); got != "" {
		t.Fatalf("darwinComm of an empty field = %q, want empty", got)
	}
}
