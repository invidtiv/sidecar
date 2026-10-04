// Package rootfile opens resolved relative paths without following links.
package rootfile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// OpenNoFollow opens a previously resolved path beneath root. Each directory
// remains pinned while its child opens, so replacing any component with a
// symlink cannot redirect a read after its content policy was checked. This
// supplements os.Root containment with a guarantee against in-root redirects.
// Files are nonblocking so special files cannot stall before the caller stats
// the returned handle. The caller owns it and decides which types are allowed.
func OpenNoFollow(root *os.Root, rel string) (*os.File, error) {
	if filepath.IsAbs(rel) {
		return nil, fmt.Errorf("path must be relative to root")
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == ".." {
			return nil, fmt.Errorf("path traversal is refused")
		}
	}
	current, err := root.OpenFile(".", os.O_RDONLY|unix.O_DIRECTORY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	clean := filepath.Clean(rel)
	if clean == "." {
		return current, nil
	}
	parts := strings.Split(clean, string(filepath.Separator))
	for i, part := range parts {
		flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
		if i < len(parts)-1 {
			flags |= unix.O_DIRECTORY
		}
		fd, err := unix.Openat(int(current.Fd()), part, flags, 0)
		path := filepath.Join(current.Name(), part)
		_ = current.Close()
		if err != nil {
			return nil, &os.PathError{Op: "open", Path: rel, Err: err}
		}
		current = os.NewFile(uintptr(fd), path)
	}
	return current, nil
}
