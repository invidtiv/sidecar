package apiservice

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// defaultServicePath is used when nothing in the installing shell's PATH is
// safe to keep.
const defaultServicePath = "/usr/bin:/bin:/usr/sbin:/sbin"

// ServicePath keeps the PATH entries a background job can trust for as long
// as it stays installed: absolute, existing directories owned by root or uid
// that no other user can write to. It drops empty and relative entries (they
// resolve against the manager's working directory), directories that do not
// exist yet (another user could create them, as under /tmp), world-writable
// directories, and directories owned by another user, any of which would let
// another local user plant a tmux, git or agent binary that the service then
// runs as the owner. Group-writable directories are kept: on macOS
// /usr/local/bin is commonly writable by the admin group, whose members can
// already act as root. Duplicates keep their first position.
func ServicePath(path string, uid int) string {
	var kept []string
	seen := map[string]bool{}
	for _, dir := range filepath.SplitList(path) {
		if dir == "" || !filepath.IsAbs(dir) || seen[dir] {
			continue
		}
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() || info.Mode().Perm()&0o002 != 0 {
			continue
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || (int(stat.Uid) != uid && stat.Uid != 0) {
			continue
		}
		seen[dir] = true
		kept = append(kept, dir)
	}
	if len(kept) == 0 {
		return defaultServicePath
	}
	return strings.Join(kept, string(os.PathListSeparator))
}
