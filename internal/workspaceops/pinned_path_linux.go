//go:build linux

package workspaceops

import (
	"fmt"
	"os"
)

func PinnedDirectoryPath(file *os.File) (string, error) {
	return os.Readlink(fmt.Sprintf("/proc/self/fd/%d", file.Fd()))
}
