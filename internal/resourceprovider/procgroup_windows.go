//go:build windows

package resourceprovider

import "os/exec"

// Windows has no process groups in the POSIX sense, and Sidecar does not run
// there today. Killing the direct child is the honest best effort; a forked
// descendant would survive, which is why this build is not a supported host for
// terminal resource providers.
func setProcessGroup(_ *exec.Cmd) {}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
