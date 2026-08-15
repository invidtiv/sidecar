package filefind

import (
	"context"
	"fmt"
	"io/fs"
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

// ScanPaths walks root collecting relative paths of either files or
// directories, respecting gitignore and bounded by a time and count limit.
// It returns the sorted paths plus a message describing why the scan stopped
// early, if it did.
func ScanPaths(root string, wantDirs bool) ([]string, string) {
	ctx, cancel := context.WithTimeout(context.Background(), ScanTimeout)
	defer cancel()

	gitIgnore := NewGitIgnore()
	_ = gitIgnore.LoadFile(filepath.Join(root, ".gitignore"))

	limit := MaxFiles
	if wantDirs {
		limit = MaxDirs
	}

	var paths []string
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

		// Skip root
		if rel == "." {
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

	switch {
	case err != nil && err != filepath.SkipAll:
		return paths, "scan error: " + err.Error()
	case limited && ctx.Err() != nil:
		return paths, "scan timed out"
	case limited && !wantDirs:
		return paths, fmt.Sprintf("limited to %d files", MaxFiles)
	case limited:
		return paths, fmt.Sprintf("limited to %d directories", MaxDirs)
	}
	return paths, ""
}
