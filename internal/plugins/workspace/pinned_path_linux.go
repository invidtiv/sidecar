//go:build linux

package workspace

import (
	"fmt"
	"os"
)

func pinnedDirectoryPath(file *os.File) (string, error) {
	return os.Readlink(fmt.Sprintf("/proc/self/fd/%d", file.Fd()))
}
