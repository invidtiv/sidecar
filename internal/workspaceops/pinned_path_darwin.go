//go:build darwin

package workspaceops

import (
	"bytes"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

func PinnedDirectoryPath(file *os.File) (string, error) {
	buf := make([]byte, 4096)
	_, _, errno := unix.Syscall(unix.SYS_FCNTL, file.Fd(), uintptr(unix.F_GETPATH), uintptr(unsafe.Pointer(&buf[0]))) //nolint:staticcheck
	if errno != 0 {
		return "", fmt.Errorf("resolve pinned directory path: %w", errno)
	}
	if end := bytes.IndexByte(buf, 0); end >= 0 {
		buf = buf[:end]
	}
	return string(buf), nil
}
