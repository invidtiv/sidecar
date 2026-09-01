//go:build darwin

package agentactivity

import (
	"bytes"
	"encoding/binary"

	"golang.org/x/sys/unix"
)

func platformForegroundProcessGroup(panePID int) int {
	process, err := unix.SysctlKinfoProc("kern.proc.pid", panePID)
	if err != nil || process == nil || process.Eproc.Tpgid <= 0 {
		return 0
	}
	return int(process.Eproc.Tpgid)
}

func platformForegroundArgv0s(group int) []string {
	processes, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil
	}
	matches := make([]foregroundProcess, 0, 2)
	for i := range processes {
		process := &processes[i]
		if int(process.Eproc.Pgid) != group {
			continue
		}
		pid := int(process.Proc.P_pid)
		argv0 := darwinProcessArgv0(pid)
		if argv0 == "" {
			continue
		}
		matches = append(matches, foregroundProcess{PID: pid, ParentPID: int(process.Eproc.Ppid), Argv0: argv0})
	}
	return foregroundProcessArgv0s(group, matches)
}

// darwinProcessArgv0 parses sysctl(KERN_PROCARGS2)'s native layout:
// argc, executable path, NUL padding, then argv strings. Unlike ps command
// text, this preserves a path containing spaces and the argv[0] installed by
// exec -a.
func darwinProcessArgv0(pid int) string {
	data, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return ""
	}
	return parseDarwinProcessArgv0(data)
}

func parseDarwinProcessArgv0(data []byte) string {
	if len(data) < 4 || int32(binary.NativeEndian.Uint32(data[:4])) < 1 {
		return ""
	}
	rest := data[4:]
	execEnd := bytes.IndexByte(rest, 0)
	if execEnd < 0 {
		return ""
	}
	pos := execEnd
	for pos < len(rest) && rest[pos] == 0 {
		pos++
	}
	if pos >= len(rest) {
		return ""
	}
	end := bytes.IndexByte(rest[pos:], 0)
	if end < 0 {
		end = len(rest) - pos
	}
	return string(rest[pos : pos+end])
}

const processIdentitySupported = true
