package apiservice

import (
	"fmt"
	"strconv"

	"golang.org/x/sys/unix"
)

// systemd 259 optionally pins activation to the process's pidfd inode too.
func matchesPIDFD(value string) (bool, error) {
	expected, err := strconv.ParseUint(value, 10, 64)
	if err != nil || expected == 0 {
		return false, fmt.Errorf("socket activation: invalid LISTEN_PIDFDID")
	}
	fd, err := unix.PidfdOpen(unix.Getpid(), 0)
	if err != nil {
		return false, fmt.Errorf("socket activation: pidfd_open: %w", err)
	}
	defer func() { _ = unix.Close(fd) }()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return false, err
	}
	return stat.Ino == expected, nil
}
