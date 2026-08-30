//go:build linux

package agentactivity

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// statLine builds a /proc/<pid>/stat line with the fields this code reads.
// proc(5) numbering: 1 pid, 2 comm, 3 state, 4 ppid, 5 pgrp, 6 session,
// 7 tty_nr, 8 tpgid.
func statLine(pid int, comm string, pgrp, tpgid int) string {
	return strconv.Itoa(pid) + " (" + comm + ") S 1 " +
		strconv.Itoa(pgrp) + " 1 34816 " + strconv.Itoa(tpgid) +
		" 4194304 1 0 0 0 0 0 0 0 20 0 1 0 100 0 0\n"
}

// TestParseHandlesCommWithSpacesAndParens is the reason this parser cuts at the
// LAST ')' rather than splitting on whitespace. An executable name may contain
// both spaces and parentheses, and a naive parser reads the wrong field —
// which has been used deliberately to defeat exactly this kind of code.
func TestParseHandlesCommWithSpacesAndParens(t *testing.T) {
	for _, comm := range []string{"node", "my (weird) name", "a b c", ")", "((("} {
		stat := []byte(statLine(42, comm, 900, 901))
		if got := parseLinuxPgrp(stat); got != 900 {
			t.Errorf("comm %q: pgrp = %d, want 900", comm, got)
		}
		if got := parseLinuxTpgid(stat); got != 901 {
			t.Errorf("comm %q: tpgid = %d, want 901", comm, got)
		}
	}
}

// TestNoForegroundGroupIsNotAGroup: a terminal with no foreground job reports
// tpgid -1. Returning that as a group would make the caller scan for a group
// that cannot exist.
func TestNoForegroundGroupIsNotAGroup(t *testing.T) {
	if got := parseLinuxTpgid([]byte(statLine(42, "bash", 900, -1))); got != 0 {
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
	cmdline := []byte("claude\x00--resume\x00")
	if got := parseLinuxArgv0(cmdline); got != "claude" {
		t.Errorf("argv0 = %q, want claude", got)
	}
	if got := parseLinuxArgv0([]byte("")); got != "" {
		t.Errorf("empty cmdline (a kernel thread) = %q, want empty", got)
	}
	// A single argument with no trailing NUL still parses.
	if got := parseLinuxArgv0([]byte("/usr/bin/node")); got != "/usr/bin/node" {
		t.Errorf("argv0 = %q", got)
	}
}

// TestForegroundArgv0sReadsAFixtureProcTree exercises the directory scan
// against a fake /proc, so the traversal is covered without depending on what
// happens to be running.
func TestForegroundArgv0sReadsAFixtureProcTree(t *testing.T) {
	root := t.TempDir()
	original := linuxProcRoot
	linuxProcRoot = root
	t.Cleanup(func() { linuxProcRoot = original })

	write := func(pid int, comm string, pgrp, tpgid int, argv0 string) {
		dir := filepath.Join(root, strconv.Itoa(pid))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(statLine(pid, comm, pgrp, tpgid)), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(argv0+"\x00"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write(100, "bash", 100, 200, "bash")   // the shell, a different group
	write(200, "node", 200, 200, "claude") // group leader, exec -a'd
	write(201, "node", 200, 200, "helper") // group member
	// Non-process entries must be skipped rather than crash the scan.
	if err := os.MkdirAll(filepath.Join(root, "sys"), 0o755); err != nil {
		t.Fatal(err)
	}

	if got := platformForegroundProcessGroup(100); got != 200 {
		t.Fatalf("foreground group = %d, want 200", got)
	}
	argv0s := platformForegroundArgv0s(200)
	if len(argv0s) != 2 {
		t.Fatalf("argv0s = %v, want 2 entries", argv0s)
	}
	// The leader must come first: it is the process the pane is actually
	// running, and the caller takes the first recognisable name.
	if argv0s[0] != "claude" {
		t.Errorf("argv0s = %v, want the group leader first", argv0s)
	}
}

func TestForegroundShellReadyLinuxFixtureMatrix(t *testing.T) {
	original := linuxProcRoot
	t.Cleanup(func() { linuxProcRoot = original })
	type fixtureProcess struct {
		pid         int
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
			processes: []fixtureProcess{{100, "bash", 100, 100, "bash"}},
		},
		{
			name: "foreground command group replaces shell", panePID: 100, currentCommand: "bash",
			processes: []fixtureProcess{{100, "bash", 100, 200, "bash"}, {200, "vim", 200, 200, "vim"}},
		},
		{
			name: "helper shares foreground shell group", panePID: 100, currentCommand: "bash",
			processes: []fixtureProcess{{100, "bash", 100, 100, "bash"}, {101, "helper", 100, 100, "helper"}},
		},
		{
			name: "unknown foreground executable", panePID: 100, currentCommand: "bash",
			processes: []fixtureProcess{{100, "mystery", 100, 100, "mystery"}},
		},
		{
			name: "tmux command disagrees with process", panePID: 100, currentCommand: "vim",
			processes: []fixtureProcess{{100, "bash", 100, 100, "bash"}},
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
				if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(statLine(process.pid, process.comm, process.pgrp, process.tpgid)), 0o644); err != nil {
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
