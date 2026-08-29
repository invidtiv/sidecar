//go:build linux

package agentactivity

import (
	"bytes"
	"os"
	"strconv"
	"strings"
)

// Linux argv0 disambiguation of a pane's foreground job, via /proc.
//
// Without this, a pane running a shared runtime — node, bun, python — falls
// back to screen-chrome detection, and several agents that all present as
// "node" become indistinguishable. That degradation was invisible until remote
// hosts made it matter: a Linux host is the common case for a machine you
// observe rather than sit at, and its rows would have been quietly less
// trustworthy than the local ones with nothing saying so.
//
// The two facts needed are the same ones the darwin implementation reads from
// sysctl: which process group owns the pane's terminal, and the argv[0] of the
// processes in it. On Linux both are files.

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

func platformForegroundArgv0s(group int) []string {
	if group <= 0 {
		return nil
	}
	entries, err := os.ReadDir(linuxProcRoot)
	if err != nil {
		return nil
	}
	var leader string
	var members []string
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
		argv0 := parseLinuxArgv0(cmdline)
		if argv0 == "" {
			// Kernel threads have an empty cmdline. So does a process caught
			// mid-exec.
			continue
		}
		if pid == group {
			leader = argv0
		} else {
			members = append(members, argv0)
		}
	}
	if leader != "" {
		return append([]string{leader}, members...)
	}
	return members
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

// parseLinuxArgv0 returns the first NUL-terminated element of a cmdline.
//
// This is argv[0] as the process was executed, not its executable path, which
// is the distinction that makes it useful: a launcher that runs node with
// `exec -a claude` is reported as claude here and as node by anything reading
// /proc/<pid>/exe.
func parseLinuxArgv0(cmdline []byte) string {
	if end := bytes.IndexByte(cmdline, 0); end >= 0 {
		cmdline = cmdline[:end]
	}
	return strings.TrimSpace(string(cmdline))
}

const processIdentitySupported = true
