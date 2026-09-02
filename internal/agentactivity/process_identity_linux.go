//go:build linux

package agentactivity

import (
	"bytes"
	"os"
	"strconv"
	"strings"
)

// Linux argv disambiguation of a pane's foreground job, via /proc.
//
// Without this, a pane running a shared runtime — node, bun, python — falls
// back to screen-chrome detection, and several agents that all present as
// "node" become indistinguishable. That degradation was invisible until remote
// hosts made it matter: a Linux host is the common case for a machine you
// observe rather than sit at, and its rows would have been quietly less
// trustworthy than the local ones with nothing saying so.
//
// The facts needed are the same ones the darwin implementation reads from
// sysctl: which process group owns the pane's terminal, and the identity of the
// processes in it — comm, argv[0] and the full argv. On Linux all of them are
// files.

// linuxProcRoot is the procfs mount point. A variable so tests can point it at
// a fixture tree — process identity is exactly the kind of code that is
// otherwise only testable on the machine that happens to be running it.
var linuxProcRoot = "/proc"

func platformForegroundProcessGroup(panePID int) int {
	if panePID <= 0 {
		return 0
	}
	stat, err := os.ReadFile(linuxProcRoot + "/" + strconv.Itoa(panePID) + "/stat")
	if err != nil {
		return 0
	}
	return parseLinuxTpgid(stat)
}

// platformForegroundProcesses walks /proc once and reads two files per matching
// process, which is what it did before process-tree scoring landed: `stat` is
// read for every entry to find the group, and `cmdline` only for the members
// that are in it. comm comes out of the `stat` that was already read, and full
// argv out of the `cmdline` that was already read, so the richer answer costs no
// additional file open.
//
// The environment is not read here. See platformProcessAgentHint.
func platformForegroundProcesses(group int) []foregroundProcess {
	if group <= 0 {
		return nil
	}
	entries, err := os.ReadDir(linuxProcRoot)
	if err != nil {
		return nil
	}
	matches := make([]foregroundProcess, 0, 2)
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			// Not a process directory. /proc holds plenty that is not.
			continue
		}
		stat, err := os.ReadFile(linuxProcRoot + "/" + entry.Name() + "/stat")
		if err != nil {
			// The process exited between ReadDir and here. Routine, not an
			// error: /proc is a live view of a moving target.
			continue
		}
		if parseLinuxPgrp(stat) != group {
			continue
		}
		cmdline, err := os.ReadFile(linuxProcRoot + "/" + entry.Name() + "/cmdline")
		if err != nil {
			continue
		}
		argv := parseLinuxArgv(cmdline)
		if len(argv) == 0 || argv[0] == "" {
			// Kernel threads have an empty cmdline. So does a process caught
			// mid-exec.
			continue
		}
		matches = append(matches, foregroundProcess{
			PID:       pid,
			ParentPID: parseLinuxPPID(stat),
			Comm:      parseLinuxComm(stat),
			Argv0:     argv[0],
			Argv:      argv,
		})
	}
	return foregroundJobMembers(group, matches)
}

// platformProcessAgentHint reads AgentHintEnv from one process's environment.
//
// /proc/<pid>/environ is the Linux counterpart of the environ section of
// macOS's kern.procargs2 buffer, and it has the same shape: NUL-separated
// KEY=VALUE records. It is a third file open per process, which is why it is a
// separate seam read lazily by the hinted resolver rather than a field on
// foregroundProcess filled during the scan.
//
// A read failure is silently no hint. Reading another user's environ is denied
// by the kernel, and a pane owned by someone else is not a pane Sidecar can
// speak for anyway.
func platformProcessAgentHint(pid int) string {
	if pid <= 0 {
		return ""
	}
	environ, err := os.ReadFile(linuxProcRoot + "/" + strconv.Itoa(pid) + "/environ")
	if err != nil {
		return ""
	}
	return parseAgentEnvHint(environ)
}

// linuxStatFields returns the space-separated fields of /proc/<pid>/stat that
// follow comm, with fields numbered as proc(5) does — so index 3 is `state`,
// the first field after comm.
//
// Splitting on whitespace from the start does not work: field 2 is the
// executable name in parentheses, and an executable may contain spaces AND
// parentheses (a process named "foo (bar) baz" is legal and has been used to
// defeat exactly this kind of parser). The reliable cut is the LAST ')' in the
// line, because every field after it is a plain token.
func linuxStatFields(stat []byte) []string {
	closeParen := bytes.LastIndexByte(stat, ')')
	if closeParen < 0 || closeParen+1 >= len(stat) {
		return nil
	}
	return strings.Fields(string(stat[closeParen+1:]))
}

// linuxStatField returns proc(5) field number n (1-based, as documented),
// which is only defined for n >= 3.
func linuxStatField(stat []byte, n int) int {
	fields := linuxStatFields(stat)
	// fields[0] is field 3 (state).
	index := n - 3
	if index < 0 || index >= len(fields) {
		return 0
	}
	value, err := strconv.Atoi(fields[index])
	if err != nil {
		return 0
	}
	return value
}

// parseLinuxTpgid reads field 8, the process group that currently owns the
// terminal — the foreground job. -1 means the terminal has no foreground
// group, which is not a failure and must not be reported as a group.
func parseLinuxTpgid(stat []byte) int {
	tpgid := linuxStatField(stat, 8)
	if tpgid <= 0 {
		return 0
	}
	return tpgid
}

// parseLinuxPgrp reads field 5, the process's own group.
func parseLinuxPgrp(stat []byte) int {
	return linuxStatField(stat, 5)
}

// parseLinuxPPID reads field 4, used to distinguish a live member of the
// shell's job from a double-forked daemon that init has adopted.
func parseLinuxPPID(stat []byte) int {
	return linuxStatField(stat, 4)
}

// parseLinuxComm reads field 2, the kernel's short process name, from between
// the FIRST '(' and the LAST ')'. Both anchors matter for the same reason
// linuxStatFields cuts at the last ')': the name may itself contain either
// character, and taking the nearest one truncates a legal name.
//
// This is the counterpart of darwin's p_comm and is what processPriority scores
// a resolved name against — a name that differs from comm was genuinely
// unwrapped from a runtime.
func parseLinuxComm(stat []byte) string {
	open := bytes.IndexByte(stat, '(')
	closeParen := bytes.LastIndexByte(stat, ')')
	if open < 0 || closeParen <= open {
		return ""
	}
	return string(stat[open+1 : closeParen])
}

// parseLinuxArgv splits a NUL-separated /proc/<pid>/cmdline into argv.
//
// This is argv as the process was executed, not its executable path, which is
// the distinction that makes it useful: a launcher that runs node with
// `exec -a claude` is reported as claude here and as node by anything reading
// /proc/<pid>/exe.
//
// The trailing NUL that terminates the last argument would otherwise produce a
// final empty element, and an empty argv[1] would make the script-argument walk
// read past the script it was looking for, so trailing empties are dropped.
func parseLinuxArgv(cmdline []byte) []string {
	parts := bytes.Split(cmdline, []byte{0})
	for len(parts) > 0 && len(bytes.TrimSpace(parts[len(parts)-1])) == 0 {
		parts = parts[:len(parts)-1]
	}
	argv := make([]string, 0, len(parts))
	for _, part := range parts {
		argv = append(argv, string(part))
	}
	if len(argv) > 0 {
		argv[0] = strings.TrimSpace(argv[0])
	}
	return argv
}

const processIdentitySupported = true
