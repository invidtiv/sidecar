package filefind

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Scan limits. They bound the walk so a huge or slow tree cannot stall the
// surface that asked for it: whatever the walk has by the deadline is what the
// caller gets, along with a message saying why it stopped early.
const (
	// MaxFiles caps the file list (prevents OOM on huge repos).
	MaxFiles = 50000
	// MaxDirs caps the directory list (path auto-complete).
	MaxDirs = 10000
	// ScanTimeout is the most time one scan may spend walking.
	ScanTimeout = 2 * time.Second
)

// DirStamp is a directory a scan walked, with the modification time it had
// then. A directory's mtime moves whenever an entry is created, removed or
// renamed directly inside it, so the set of stamps a walk collected is enough
// to ask later, with one stat per directory and no readdir, whether the file
// list could have changed. Path is relative to the root; the root itself is
// ".". The root's .gitignore is stamped too, when it exists: editing it changes
// what the walk would list without moving any directory.
type DirStamp struct {
	Path    string
	ModTime time.Time
}

// stampSettle is how far a recorded mtime must be from the walk that recorded
// it for the stamp to be trusted. A filesystem with one-second timestamps can
// take a write in the same tick as the walk's stat without moving the mtime
// again, so a stamp that fresh is dropped along with the rest of the set, and
// the cache walks once more when it ages out. On a nanosecond filesystem the
// window is the same second, so a tree that was quiet for a second before the
// walk pays nothing.
const stampSettle = 2 * time.Second

// ScanPaths walks root collecting relative paths of either files or
// directories, respecting gitignore and bounded by a time and count limit.
// It returns the sorted paths plus a message describing why the scan stopped
// early, if it did.
func ScanPaths(root string, wantDirs bool) ([]string, string) {
	paths, _, errText := scanTree(root, wantDirs)
	return paths, errText
}

// scanTree is ScanPaths plus the stamp of every directory the walk descended
// into, which is what lets the cache check for changes without walking again.
func scanTree(root string, wantDirs bool) ([]string, []DirStamp, string) {
	ctx, cancel := context.WithTimeout(context.Background(), ScanTimeout)
	defer cancel()
	walkedAt := time.Now()

	gitIgnore := NewGitIgnore()
	_ = gitIgnore.LoadFile(filepath.Join(root, ".gitignore"))

	limit := MaxFiles
	if wantDirs {
		limit = MaxDirs
	}

	var paths []string
	var stamps []DirStamp
	limited := false

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		// Check timeout
		select {
		case <-ctx.Done():
			limited = true
			return filepath.SkipAll
		default:
		}

		if err != nil {
			return nil // Skip unreadable entries
		}

		// Get relative path
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}

		// The root is walked but not listed.
		if rel == "." {
			if info, err := d.Info(); err == nil {
				stamps = append(stamps, DirStamp{Path: rel, ModTime: info.ModTime()})
			}
			return nil
		}

		name := d.Name()

		if d.IsDir() {
			// Skip common large/irrelevant directories
			if name == ".git" || name == "node_modules" || name == "vendor" ||
				name == ".next" || name == "dist" || name == "build" ||
				name == "__pycache__" || name == ".venv" || name == "venv" ||
				name == ".idea" || name == ".vscode" {
				return filepath.SkipDir
			}
			if gitIgnore.IsIgnored(rel, true) {
				return filepath.SkipDir
			}
			// One stat per directory the walk descends into. Info on a
			// ReadDir entry is an lstat, so this is the only extra syscall the
			// stamps cost the walk; the probe they enable costs the same
			// again and nothing else.
			if info, err := d.Info(); err == nil {
				stamps = append(stamps, DirStamp{Path: rel, ModTime: info.ModTime()})
			}
			if !wantDirs {
				return nil // Directories are not part of the file list
			}
		} else {
			if wantDirs {
				return nil
			}
			// Dotfiles are files. What is worth hiding is what the project has
			// said is not worth keeping — .gitignore — not what its name starts
			// with, and the two are not the same set: `.goreleaser.yml`,
			// `.golangci.yml`, `.env.example` are all tracked, all findable in
			// the Files tree, and were all invisible to the finder, which
			// answered "No matches" about files sitting in the tree beside it.
			//
			// The rule was inconsistent with itself as well: the walk descends
			// into `.claude/` and `.github/` and lists what is inside them, so a
			// dot in a directory name meant nothing while a dot in a file name
			// meant everything.
			if gitIgnore.IsIgnored(rel, false) {
				return nil
			}
		}

		if len(paths) >= limit {
			limited = true
			return filepath.SkipAll
		}

		paths = append(paths, rel)
		return nil
	})

	// Sort paths for consistent ordering
	sort.Strings(paths)

	if info, err := os.Stat(filepath.Join(root, ".gitignore")); err == nil && !info.IsDir() {
		stamps = append(stamps, DirStamp{Path: ".gitignore", ModTime: info.ModTime()})
	}
	// Stamps vouch for the whole tree, so a walk that did not see the whole
	// tree returns none, and so does one whose stamps have not settled: the
	// aged cache then walks rather than trusting a probe.
	if limited || !stampsSettled(stamps, walkedAt) {
		stamps = nil
	}

	switch {
	case err != nil && err != filepath.SkipAll:
		return paths, stamps, "scan error: " + err.Error()
	case limited && ctx.Err() != nil:
		return paths, stamps, "scan timed out"
	case limited && !wantDirs:
		return paths, stamps, fmt.Sprintf("limited to %d files", MaxFiles)
	case limited:
		return paths, stamps, fmt.Sprintf("limited to %d directories", MaxDirs)
	}
	return paths, stamps, ""
}

// stampsSettled reports whether every stamp is old enough, relative to the
// walk that took it, to be trusted (see stampSettle).
func stampsSettled(stamps []DirStamp, walkedAt time.Time) bool {
	for _, stamp := range stamps {
		if walkedAt.Sub(stamp.ModTime) < stampSettle {
			return false
		}
	}
	return true
}

// treeUnchanged reports whether every path in stamps still has the
// modification time the scan recorded, which means no entry has been created,
// removed or renamed anywhere the scan looked and the ignore rules are as they
// were. It stops at the first difference, and gives up (reporting a change, so
// the caller walks) if it runs past ScanTimeout.
func treeUnchanged(root string, stamps []DirStamp) bool {
	if len(stamps) == 0 {
		return false
	}
	deadline := time.Now().Add(ScanTimeout)
	for i, stamp := range stamps {
		if i&63 == 63 && time.Now().After(deadline) {
			return false
		}
		info, err := os.Stat(filepath.Join(root, stamp.Path))
		if err != nil || !info.ModTime().Equal(stamp.ModTime) {
			return false
		}
	}
	return true
}
