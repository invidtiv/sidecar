//go:build darwin

package agentactivity

import (
	"bytes"
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
//
// SIDECAR_FOREGROUND_PROBE_COMMAND carries the pane's pane_current_command,
// because that is what decides how hard the resolver is allowed to look. It
// defaults to `node`, the shape the harness probes; pass the pane's real command
// (tmux display-message -p '#{pane_current_command}') to probe a wrapper.
func TestForegroundAgentLiveProbe(t *testing.T) {
	raw := os.Getenv("SIDECAR_FOREGROUND_PROBE_PID")
	if raw == "" {
		t.Skip("set SIDECAR_FOREGROUND_PROBE_PID")
	}
	pid, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatal(err)
	}
	command := os.Getenv("SIDECAR_FOREGROUND_PROBE_COMMAND")
	if command == "" {
		command = "node"
	}
	want := os.Getenv("SIDECAR_FOREGROUND_PROBE_WANT")
	if got := ResolveForegroundAgent(pid, command); got != want {
		t.Fatalf("ResolveForegroundAgent(%d, %q) = %q, want %q", pid, command, got, want)
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

// TestDarwinArgvSurvivesAProcessTitleRewrite pins the one place this port
// deliberately diverges from upstream's `procargs2_argv`.
//
// It is a live finding, not a hypothetical. Pi 0.84.3 runs on Node and sets
// process.title, which on macOS writes into the same argv memory kern.procargs2
// reports and blanks the slots it no longer needs. The kernel still reports the
// original argc, so the buffer reads argc=2 with argv[0]="pi" and argv[1]="".
// Upstream returns None for that and loses argv[0] with it; the argv[0]-only
// parser this replaced returned "pi" happily. Voiding the vector was therefore a
// regression that took Pi from identified to unidentified — measured on a live
// pane, on the exact provider Slice 3 exists to reach.
func TestDarwinArgvSurvivesAProcessTitleRewrite(t *testing.T) {
	// argc=2, exec path, padding, then "pi" and a blanked second slot.
	data := buildProcargs2("/Users/x/.local/bin/pi", []string{"pi", ""}, nil)
	argv := parseDarwinProcessArgv(data)
	if len(argv) != 1 || argv[0] != "pi" {
		t.Fatalf("parseDarwinProcessArgv = %q, want the [pi] prefix kept rather than the whole vector voided", argv)
	}
	if got := identifyAgentName(argv[0]); got != "pi" {
		t.Fatalf("identifyAgentName(%q) = %q, want pi", argv[0], got)
	}
}

// TestDarwinArgvStillStopsAtArgc is the other half: shortening the read on a
// blank slot must not become a licence to run into the environment, which is
// what bounding by argc is for.
func TestDarwinArgvStillStopsAtArgc(t *testing.T) {
	data := buildProcargs2("/usr/bin/node", []string{"node"}, []string{"SIDECAR_AGENT=claude", "PATH=/bin"})
	if argv := parseDarwinProcessArgv(data); len(argv) != 1 || argv[0] != "node" {
		t.Fatalf("parseDarwinProcessArgv = %q, want exactly [node] with no environment", argv)
	}
}

// TestDarwinAnEmptyArgv0IsIndistinguishableFromPadding records a known bound of
// this layout, not behaviour anyone wants.
//
// procargs2ArgvStart skips every NUL after the exec path, and an empty argv[0]
// is a bare NUL, so its terminator is swallowed as padding and both readers
// start one string late: argv takes its last element from the environment
// block, and the environment reader skips its first record. Upstream's
// `procargs2_argv_start` (src/platform/macos.rs:807 at d08e4468) has the same
// slip, as did the argv[0]-only parser this port replaced, so it is inherited
// rather than introduced by the blank-slot change.
//
// It is pinned rather than fixed because padding and an empty argv[0] are the
// same bytes. The only rules that could tell them apart are content rules —
// "that string looks like KEY=value" — and a real `env FOO=bar` argument would
// fail them. If this ever bites in practice, the honest fix is a different
// source for argv (proc_pidinfo), not a guess about these bytes.
//
// The blast radius is bounded and this test states it: an agent identified from
// argv[0] is still identified, because argv[0] is not the element that slips.
func TestDarwinAnEmptyArgv0IsIndistinguishableFromPadding(t *testing.T) {
	data := buildProcargs2("/usr/bin/node", []string{"", "pi"}, []string{"SIDECAR_AGENT=claude", "PATH=/bin"})

	argv := parseDarwinProcessArgv(data)
	if len(argv) != 2 || argv[0] != "pi" || argv[1] != "SIDECAR_AGENT=claude" {
		t.Fatalf("parseDarwinProcessArgv = %q; the known slip is [pi SIDECAR_AGENT=claude]. If this now "+
			"reads [\"\" pi], the slip is fixed and this test should be replaced by one asserting that", argv)
	}
	if got := string(parseDarwinProcessEnviron(data)); got != "PATH=/bin\x00" {
		t.Fatalf("parseDarwinProcessEnviron = %q; the same slip drops the first record", got)
	}
	// The consequence to keep in view: a hint published in the first record of
	// such a process is not read. It is not misread as some other agent.
	if got := parseAgentEnvHint(parseDarwinProcessEnviron(data)); got != "" {
		t.Fatalf("parseAgentEnvHint = %q, want empty for the skipped record", got)
	}
	// And identification, which reads argv[0], is unaffected.
	if got := identifyAgentName(argv[0]); got != "pi" {
		t.Fatalf("identifyAgentName(%q) = %q, want pi", argv[0], got)
	}
}

// TestDarwinReadsAnotherProcessesHintUnlessTheBinaryIsRestricted pins what
// kern.procargs2 actually hands over, as a measured fact with the reproduction
// in it.
//
// It exists because this is the one place the port's cost/benefit was taken on
// trust: every other test of this parser feeds it a synthetic buildProcargs2
// buffer, which proves the parse and says nothing about what the kernel puts in
// the buffer. A live proof that used `/bin/sleep` as its sandbox stand-in
// concluded from an empty read that the hint could never work on macOS. It can,
// and does; `/bin/sleep` is a SIP-protected platform binary, and the kernel
// withholds a restricted process's environment from everyone.
//
// Both directions are here on purpose, because either one alone reads as the
// wrong general rule. The pair is the rule: same uid is not sufficient, and a
// restricted target is the only observed exception.
func TestDarwinReadsAnotherProcessesHintUnlessTheBinaryIsRestricted(t *testing.T) {
	// Our own buffer first, so a failure below is about the target process and
	// not a sysctl that is unavailable here (a sandboxed or hardened runner may
	// deny kern.procargs2 outright).
	self, err := unix.SysctlRaw("kern.procargs2", os.Getpid())
	if err != nil {
		t.Skipf("kern.procargs2 unavailable: %v", err)
	}
	if !bytes.Contains(self, []byte("PATH=")) {
		t.Skipf("kern.procargs2 returned no environment for our own process (%d bytes); "+
			"nothing can be concluded about another process's", len(self))
	}

	// An ordinary binary: this test binary, re-executed in helper mode. It is
	// not signed by Apple and carries no restricted entitlement, which is the
	// property under test — the same reason
	// lifecycleenv.TestAnAgentHintCannotChangeTheOccupant re-execs itself
	// rather than running /bin/sleep in its pane.
	ordinary, ordinaryRaw := darwinHintOfChild(t, testBinary(t), "-test.run=^"+hintChildHelperTest+"$")
	if ordinary != "claude" {
		t.Fatalf("platformProcessAgentHint for an ordinary same-uid child = %q, want claude "+
			"(%d bytes of kern.procargs2, hint present in the buffer: %v).\n"+
			"If macOS has stopped returning another process's environment at all, AgentHintEnv is "+
			"dead on this platform and process_identity_darwin.go must say so instead of reading it",
			ordinary, len(ordinaryRaw), bytes.Contains(ordinaryRaw, []byte(AgentHintEnv)))
	}

	// A restricted binary, spawned identically with the same variable exported.
	// The call succeeds and the buffer is well formed; it simply ends after
	// argv, so there is nothing for parseDarwinProcessEnviron to find.
	restricted, restrictedRaw := darwinHintOfChild(t, "/bin/sleep", "120")
	if argv := parseDarwinProcessArgv(restrictedRaw); len(argv) != 2 || argv[0] != "/bin/sleep" {
		t.Fatalf("argv for the restricted child = %q, want [/bin/sleep 120]: argv does cross the "+
			"process boundary even here, and process identity depends on that", argv)
	}
	if restricted != "" {
		t.Fatalf("platformProcessAgentHint for a SIP-protected child = %q (%d bytes). macOS now "+
			"returns a restricted process's environment; the bound documented in "+
			"platformProcessAgentHint has changed and the proof advice there is stale",
			restricted, len(restrictedRaw))
	}
}

const hintChildEnv = "SIDECAR_TEST_HINT_CHILD"

// hintChildHelperTest is the test the helper child runs: it does nothing but
// stay alive long enough to be read.
const hintChildHelperTest = "TestDarwinHintChildHelper"

func TestDarwinHintChildHelper(t *testing.T) {
	if os.Getenv(hintChildEnv) != "1" {
		t.Skip("helper for TestDarwinReadsAnotherProcessesHintUnlessTheBinaryIsRestricted")
	}
	time.Sleep(30 * time.Second)
}

func testBinary(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return self
}

// darwinHintOfChild spawns one child with AgentHintEnv in its environment and
// reads it back through the production seam, returning the hint and the raw
// buffer the answer came from.
//
// It waits for argv to appear rather than racing it: kern.procargs2 is empty
// between fork and exec. The child is this process's own, same uid and same
// session — the most permissive cross-process case macOS offers, so anything it
// refuses is refused for a reason other than ownership.
func darwinHintOfChild(t *testing.T, name string, args ...string) (string, []byte) {
	t.Helper()
	child := exec.Command(name, args...)
	child.Env = append(os.Environ(), AgentHintEnv+"=claude", hintChildEnv+"=1")
	if err := child.Start(); err != nil {
		t.Fatalf("spawn %s: %v", name, err)
	}
	t.Cleanup(func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	})

	var raw []byte
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		buf, err := unix.SysctlRaw("kern.procargs2", child.Process.Pid)
		if err == nil {
			if argv := parseDarwinProcessArgv(buf); len(argv) > 0 && argv[0] == name {
				raw = buf
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(raw) == 0 {
		t.Fatalf("kern.procargs2 for our own %s child never reported argv", name)
	}
	return platformProcessAgentHint(child.Process.Pid), raw
}
