//go:build !windows

package resourceprovider

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the child in a new process group of its own, so a kill
// reaches everything it forks and nothing it did not.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcessGroup SIGKILLs the whole group Sidecar created. Sidecar never
// signals a process outside a group it made itself, which is why this refuses
// to fall back to signalling the bare PID: without Setpgid having taken effect
// there is no group to own, and killing a PID whose group is unknown risks
// signalling something else entirely once the PID is recycled.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return
	}
	// Setpgid made the child a group leader, so the group id is the child's
	// own pid — no lookup needed, and no lookup wanted: by the time a forked
	// descendant is holding the invocation open the direct child is often
	// already a zombie, and getpgid on a zombie is not reliably answerable.
	// The pid cannot have been recycled either, because the caller has not
	// reaped it yet.
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}
