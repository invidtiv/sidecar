//go:build linux

package agentactivity

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// statLine builds a /proc/<pid>/stat line with the fields this code reads.
// proc(5) numbering: 1 pid, 2 comm, 3 state, 4 ppid, 5 pgrp, 6 session,
// 7 tty_nr, 8 tpgid.
func statLine(pid int, comm string, ppid, pgrp, tpgid int) string {
	return strconv.Itoa(pid) + " (" + comm + ") S " + strconv.Itoa(ppid) + " " +
		strconv.Itoa(pgrp) + " 1 34816 " + strconv.Itoa(tpgid) +
		" 4194304 1 0 0 0 0 0 0 0 20 0 1 0 100 0 0\n"
}

// TestParseHandlesCommWithSpacesAndParens is the reason this parser cuts at the
// LAST ')' rather than splitting on whitespace. An executable name may contain
// both spaces and parentheses, and a naive parser reads the wrong field —
// which has been used deliberately to defeat exactly this kind of code.
func TestParseHandlesCommWithSpacesAndParens(t *testing.T) {
	for _, comm := range []string{"node", "my (weird) name", "a b c", ")", "((("} {
		stat := []byte(statLine(42, comm, 7, 900, 901))
		if got := parseLinuxPgrp(stat); got != 900 {
			t.Errorf("comm %q: pgrp = %d, want 900", comm, got)
		}
		if got := parseLinuxTpgid(stat); got != 901 {
			t.Errorf("comm %q: tpgid = %d, want 901", comm, got)
		}
		if got := parseLinuxPPID(stat); got != 7 {
			t.Errorf("comm %q: ppid = %d, want 7", comm, got)
		}
	}
}

// TestNoForegroundGroupIsNotAGroup: a terminal with no foreground job reports
// tpgid -1. Returning that as a group would make the caller scan for a group
// that cannot exist.
func TestNoForegroundGroupIsNotAGroup(t *testing.T) {
	if got := parseLinuxTpgid([]byte(statLine(42, "bash", 7, 900, -1))); got != 0 {
		t.Errorf("tpgid = %d, want 0 for a terminal with no foreground group", got)
	}
}

func TestParseRejectsMalformedStat(t *testing.T) {
	for _, bad := range []string{"", "no parens here", "42 (node", "42 (node)"} {
		if got := parseLinuxTpgid([]byte(bad)); got != 0 {
			t.Errorf("%q: tpgid = %d, want 0", bad, got)
		}
	}
}

// TestParseArgv0UsesArgvNotExe is the property that makes this worth doing at
// all: a launcher running node via `exec -a claude` must be reported as claude.
func TestParseArgv0UsesArgvNotExe(t *testing.T) {
	if got := parseLinuxArgv([]byte("claude\x00--resume\x00")); len(got) != 2 || got[0] != "claude" {
		t.Errorf("argv = %q, want claude first", got)
	}
	if got := parseLinuxArgv([]byte("")); len(got) != 0 {
		t.Errorf("empty cmdline (a kernel thread) = %q, want empty", got)
	}
	// A single argument with no trailing NUL still parses.
	if got := parseLinuxArgv([]byte("/usr/bin/node")); len(got) != 1 || got[0] != "/usr/bin/node" {
		t.Errorf("argv = %q", got)
	}
}

// TestForegroundProcessesReadAFixtureProcTree exercises the directory scan
// against a fake /proc, so the traversal is covered without depending on what
// happens to be running.
func TestForegroundProcessesReadAFixtureProcTree(t *testing.T) {
	root := t.TempDir()
	original := linuxProcRoot
	linuxProcRoot = root
	t.Cleanup(func() { linuxProcRoot = original })

	write := func(pid int, comm string, ppid, pgrp, tpgid int, argv0 string) {
		dir := filepath.Join(root, strconv.Itoa(pid))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(statLine(pid, comm, ppid, pgrp, tpgid)), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(argv0+"\x00"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write(100, "bash", 10, 100, 200, "bash")    // the shell, a different group
	write(200, "node", 100, 200, 200, "claude") // group leader, exec -a'd
	write(201, "node", 200, 200, 200, "helper") // group member
	// Non-process entries must be skipped rather than crash the scan.
	if err := os.MkdirAll(filepath.Join(root, "sys"), 0o755); err != nil {
		t.Fatal(err)
	}

	if got := platformForegroundProcessGroup(100); got != 200 {
		t.Fatalf("foreground group = %d, want 200", got)
	}
	members := platformForegroundProcesses(200)
	if len(members) != 2 {
		t.Fatalf("members = %v, want 2 entries", members)
	}
	// The leader must come first: it is the process the pane is actually
	// running, and scoring prefers it over every other member.
	if members[0].Argv0 != "claude" {
		t.Errorf("members = %v, want the group leader first", members)
	}
	// comm and argv[0] disagree here on purpose — that is the `exec -a` shape —
	// and both must survive the scan, because processPriority scores exactly
	// that disagreement.
	if members[0].Comm != "node" {
		t.Errorf("leader comm = %q, want node", members[0].Comm)
	}
}

func TestForegroundShellReadyLinuxFixtureMatrix(t *testing.T) {
	original := linuxProcRoot
	t.Cleanup(func() { linuxProcRoot = original })
	type fixtureProcess struct {
		pid         int
		ppid        int
		comm        string
		pgrp, tpgid int
		argv0       string
	}

	tests := []struct {
		name           string
		panePID        int
		currentCommand string
		processes      []fixtureProcess
		want           bool
	}{
		{
			name: "sole foreground interactive shell", panePID: 100, currentCommand: "bash", want: true,
			processes: []fixtureProcess{{100, 10, "bash", 100, 100, "bash"}},
		},
		{
			name: "foreground command group replaces shell", panePID: 100, currentCommand: "bash",
			processes: []fixtureProcess{{100, 10, "bash", 100, 200, "bash"}, {200, 100, "vim", 200, 200, "vim"}},
		},
		{
			name: "helper shares foreground shell group", panePID: 100, currentCommand: "bash",
			processes: []fixtureProcess{{100, 10, "bash", 100, 100, "bash"}, {101, 100, "helper", 100, 100, "helper"}},
		},
		{
			name: "init-adopted daemon retained shell group", panePID: 100, currentCommand: "bash", want: true,
			processes: []fixtureProcess{{100, 10, "bash", 100, 100, "bash"}, {101, 1, "git", 100, 100, "git"}},
		},
		{
			name: "unknown foreground executable", panePID: 100, currentCommand: "bash",
			processes: []fixtureProcess{{100, 10, "mystery", 100, 100, "mystery"}},
		},
		{
			name: "tmux command disagrees with process", panePID: 100, currentCommand: "vim",
			processes: []fixtureProcess{{100, 10, "bash", 100, 100, "bash"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			linuxProcRoot = root
			for _, process := range tt.processes {
				dir := filepath.Join(root, strconv.Itoa(process.pid))
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(statLine(process.pid, process.comm, process.ppid, process.pgrp, process.tpgid)), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(process.argv0+"\x00"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if got := ForegroundShellReady(tt.panePID, tt.currentCommand); got != tt.want {
				t.Fatalf("ForegroundShellReady(%d, %q) = %v, want %v", tt.panePID, tt.currentCommand, got, tt.want)
			}
		})
	}
}

// writeProcFixture lays down one /proc/<pid> directory with the three files the
// adapter reads: stat, cmdline and environ. argv is written NUL-separated and
// NUL-terminated, as the kernel writes it.
func writeProcFixture(t *testing.T, root string, pid int, comm string, ppid, pgrp, tpgid int, argv []string, env []string) {
	t.Helper()
	dir := filepath.Join(root, strconv.Itoa(pid))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(statLine(pid, comm, ppid, pgrp, tpgid)), 0o644); err != nil {
		t.Fatal(err)
	}
	cmdline := ""
	for _, arg := range argv {
		cmdline += arg + "\x00"
	}
	if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(cmdline), 0o644); err != nil {
		t.Fatal(err)
	}
	environ := ""
	for _, entry := range env {
		environ += entry + "\x00"
	}
	if err := os.WriteFile(filepath.Join(dir, "environ"), []byte(environ), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestLinuxParseArgvKeepsEveryArgument: process-tree scoring reads argv[1], not
// only argv[0], so the whole command line has to survive the NUL split — with
// the terminating NUL's empty tail dropped, since an empty trailing argument
// would make the script-argument walk read past the script it was looking for.
func TestLinuxParseArgvKeepsEveryArgument(t *testing.T) {
	argv := parseLinuxArgv([]byte("node\x00/usr/local/bin/qwen\x00--yolo\x00"))
	if len(argv) != 3 || argv[0] != "node" || argv[1] != "/usr/local/bin/qwen" || argv[2] != "--yolo" {
		t.Fatalf("argv = %q, want the three arguments", argv)
	}
	if got := parseLinuxArgv(nil); len(got) != 0 {
		t.Fatalf("a kernel thread's empty cmdline = %q, want nothing", got)
	}
	// A single argument with no trailing NUL still parses.
	if got := parseLinuxArgv([]byte("/usr/bin/node")); len(got) != 1 || got[0] != "/usr/bin/node" {
		t.Fatalf("argv = %q", got)
	}
}

// TestLinuxParseCommHandlesSpacesAndParens: comm is field 2 of stat, wrapped in
// parentheses, and may itself contain both characters. Taking the nearest ')'
// truncates a legal name, which would then differ from the resolved name and
// promote every such process to the top scoring rung.
func TestLinuxParseCommHandlesSpacesAndParens(t *testing.T) {
	for _, comm := range []string{"node", "my (weird) name", "a b c", ")", "((("} {
		if got := parseLinuxComm([]byte(statLine(42, comm, 7, 900, 901))); got != comm {
			t.Errorf("parseLinuxComm(%q) = %q", comm, got)
		}
	}
	if got := parseLinuxComm([]byte("no parens here")); got != "" {
		t.Errorf("malformed stat comm = %q, want empty", got)
	}
}

// TestLinuxForegroundProcessesResolveANodeShim is the measured case on the
// Linux adapter: a pane whose foreground command is `node` running an agent's
// `#!/usr/bin/env node` shim, resolved end to end through the fixture tree.
func TestLinuxForegroundProcessesResolveANodeShim(t *testing.T) {
	root := t.TempDir()
	original := linuxProcRoot
	linuxProcRoot = root
	t.Cleanup(func() { linuxProcRoot = original })
	resetForegroundIdentityCache()

	writeProcFixture(t, root, 100, "bash", 10, 100, 200, []string{"bash"}, nil)
	writeProcFixture(t, root, 200, "node", 100, 200, 200,
		[]string{"node", "/home/user/.local/bin/qwen"}, []string{"PATH=/usr/bin"})

	if got := ResolveForegroundProcess(100); got != "qwen" {
		t.Fatalf("ResolveForegroundProcess = %q, want qwen", got)
	}
	resetForegroundIdentityCache()
	if got := ResolveForegroundAgent(100, "node"); got != "qwen" {
		t.Fatalf("ResolveForegroundAgent = %q, want qwen", got)
	}
}

// TestLinuxAgentHintIsReadFromTheEnvironAndOnlyByTheHintedResolver drives both
// halves of the hint seam against a real fixture tree: the environ file is
// parsed, and ResolveForegroundProcess — which lifecycleenv.OccupantKind calls,
// and which can refuse a hook report — does not consult it.
//
// The pane's command is `sandbox`, which the alias table cannot place, so this
// is also the end-to-end shape of the case the hint exists for: no scan is run
// and the answer comes from the group leader's environment alone.
func TestLinuxAgentHintIsReadFromTheEnvironAndOnlyByTheHintedResolver(t *testing.T) {
	root := t.TempDir()
	original := linuxProcRoot
	linuxProcRoot = root
	t.Cleanup(func() { linuxProcRoot = original })
	resetForegroundIdentityCache()

	// A sandbox wrapper: nothing in the process table names an agent, and the
	// only evidence is the exported hint.
	writeProcFixture(t, root, 100, "bash", 10, 100, 200, []string{"bash"}, nil)
	writeProcFixture(t, root, 200, "sandbox", 100, 200, 200,
		[]string{"sandbox", "--net=none", "run"}, []string{"PATH=/usr/bin", "SIDECAR_AGENT=cline"})

	if got := platformProcessAgentHint(200); got != "cline" {
		t.Fatalf("platformProcessAgentHint = %q, want cline", got)
	}
	if got := ResolveForegroundAgent(100, "sandbox"); got != "cline" {
		t.Fatalf("ResolveForegroundAgent = %q, want cline", got)
	}
	resetForegroundIdentityCache()
	if got := ResolveForegroundProcess(100); got != "" {
		t.Fatalf("ResolveForegroundProcess = %q; process evidence must not see the hint", got)
	}

	// An unknown value is no hint, not an error.
	writeProcFixture(t, root, 200, "sandbox", 100, 200, 200,
		[]string{"sandbox", "run"}, []string{"SIDECAR_AGENT=not-an-agent"})
	resetForegroundIdentityCache()
	if got := ResolveForegroundAgent(100, "sandbox"); got != "" {
		t.Fatalf("ResolveForegroundAgent with an unknown hint = %q, want empty", got)
	}
}

// TestLinuxUnreadableEnvironIsNoHint: /proc/<pid>/environ is unreadable for
// another user's process, and that must be silence rather than a failure — a
// pane Sidecar cannot read is a pane it cannot speak for anyway.
func TestLinuxUnreadableEnvironIsNoHint(t *testing.T) {
	root := t.TempDir()
	original := linuxProcRoot
	linuxProcRoot = root
	t.Cleanup(func() { linuxProcRoot = original })
	if err := os.MkdirAll(filepath.Join(root, "200"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := platformProcessAgentHint(200); got != "" {
		t.Fatalf("missing environ = %q, want empty", got)
	}
	if got := platformProcessAgentHint(0); got != "" {
		t.Fatalf("pid 0 = %q, want empty", got)
	}
}

// TestLinuxReadsAnotherProcessesHintFromTheRealProcTree closes the same gap
// darwin's TestDarwinReadsAnotherProcessesHintUnlessTheBinaryIsRestricted
// closes: every other test of this adapter points linuxProcRoot at a fixture
// tree, which proves the parse and says nothing about whether the kernel hands
// the file over.
//
// That distinction is not academic. On macOS the analogous call returns argv but
// withholds the environment of a restricted binary, and a suite of synthetic
// buffers could not have told anyone. So this one reads real /proc for a child
// it spawned itself.
func TestLinuxReadsAnotherProcessesHintFromTheRealProcTree(t *testing.T) {
	if _, err := os.ReadFile("/proc/self/environ"); err != nil {
		t.Skipf("/proc/self/environ unreadable: %v", err)
	}

	// The fixture tests move linuxProcRoot; this one needs the real thing.
	original := linuxProcRoot
	linuxProcRoot = "/proc"
	t.Cleanup(func() { linuxProcRoot = original })

	child := exec.Command("/bin/sleep", "120")
	child.Env = append(os.Environ(), AgentHintEnv+"=claude")
	if err := child.Start(); err != nil {
		t.Fatalf("spawn child: %v", err)
	}
	t.Cleanup(func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	})

	// environ is empty between fork and exec, so wait for it rather than racing.
	deadline := time.Now().Add(3 * time.Second)
	var got string
	for time.Now().Before(deadline) {
		if got = platformProcessAgentHint(child.Process.Pid); got != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got != "claude" {
		t.Fatalf("platformProcessAgentHint for our own same-uid child = %q, want claude. "+
			"If the kernel has stopped handing over /proc/<pid>/environ, AgentHintEnv answers "+
			"nothing here and this adapter must stop pretending it does", got)
	}
}

// TestLinuxForegroundProcessesWalkTheRealProcTree is the same argument applied
// to the walk rather than to the hint.
//
// Every other test of this adapter reads a fixture tree built by statLine in
// this file, so the parser is only ever checked against a /proc that a test
// author wrote from proc(5). A misreading shared by both would pass. This one
// walks the kernel's own /proc for the group this process is in and expects to
// find this process, with the argv it was started with.
func TestLinuxForegroundProcessesWalkTheRealProcTree(t *testing.T) {
	if _, err := os.ReadFile("/proc/self/stat"); err != nil {
		t.Skipf("/proc/self/stat unreadable: %v", err)
	}
	original := linuxProcRoot
	linuxProcRoot = "/proc"
	t.Cleanup(func() { linuxProcRoot = original })

	group, err := syscall.Getpgid(os.Getpid())
	if err != nil {
		t.Skipf("getpgid: %v", err)
	}
	var self *foregroundProcess
	for _, process := range platformForegroundProcesses(group) {
		if process.PID == os.Getpid() {
			self = &process
			break
		}
	}
	if self == nil {
		t.Fatalf("walking the real /proc for group %d did not find this process (pid %d)", group, os.Getpid())
	}
	if self.Argv0 != os.Args[0] {
		t.Errorf("argv[0] = %q, want %q: /proc/<pid>/cmdline is argv as executed", self.Argv0, os.Args[0])
	}
	if self.Comm == "" || !strings.HasPrefix(filepath.Base(os.Args[0]), self.Comm) {
		// comm is the kernel's short name, truncated to TASK_COMM_LEN-1, so it
		// is a prefix of the executable's basename rather than equal to it.
		t.Errorf("comm = %q, want a prefix of %q", self.Comm, filepath.Base(os.Args[0]))
	}
	if self.ParentPID != os.Getppid() {
		t.Errorf("ppid = %d, want %d", self.ParentPID, os.Getppid())
	}
}
