package workspace

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
)

const maxUntrackedTotalBytes int64 = 4 << 20

func collectWorktreeChanges(ctx context.Context, workdir string, processes *atomic.Int64) (*WorktreeChanges, *GitStats) {
	changes := &WorktreeChanges{State: LoadStateLoading}
	stats := &GitStats{}
	status, err := refreshGitOutput(ctx, workdir, processes, "status", "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		changes.State, changes.Err = LoadStateError, err
		return changes, nil
	}
	parsePorcelainStatus(status, changes)

	numstat, err := refreshGitOutput(ctx, workdir, processes, "numstat", "diff", "--numstat", "HEAD")
	if err != nil {
		changes.State, changes.Err = LoadStateError, err
		return changes, nil
	}
	parseNumstat(numstat, stats)
	countUntrackedStats(workdir, changes, stats)
	if len(changes.Dirty) == 0 {
		changes.State = LoadStateClean
	} else if changes.Truncated {
		changes.State = LoadStateTruncated
	} else {
		changes.State = LoadStateReady
	}
	return changes, stats
}

func refreshGitOutput(ctx context.Context, dir string, processes *atomic.Int64, label string, args ...string) ([]byte, error) {
	recordGitProcess(ctx, processes)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s in %s: %s: %w", strings.Join(args, " "), dir, strings.TrimSpace(string(out)), err)
	}
	_ = label // retained for call-site readability and future per-command metrics
	return out, nil
}

func parsePorcelainStatus(output []byte, changes *WorktreeChanges) {
	seen := make(map[string]struct{})
	addDirty := func(path string) {
		if path == "" {
			return
		}
		if _, ok := seen[path]; !ok {
			seen[path] = struct{}{}
			changes.Dirty = append(changes.Dirty, path)
		}
	}
	fields := bytes.Split(output, []byte{0})
	for i := 0; i < len(fields); i++ {
		entry := fields[i]
		if len(entry) < 4 {
			continue
		}
		x, y := entry[0], entry[1]
		path := string(entry[3:])
		// Porcelain v1 -z never C-quotes paths. Rename/copy records are
		// "XY destination\0source\0" (regardless of score); preserve both
		// identities so a rename overlaps another worktree editing the source.
		if x == 'R' || x == 'C' || y == 'R' || y == 'C' {
			if i+1 < len(fields) {
				i++
				addDirty(string(fields[i]))
			}
		}
		if x == '?' && y == '?' {
			changes.Untracked = append(changes.Untracked, path)
		} else {
			if x != ' ' {
				changes.Staged = append(changes.Staged, path)
			}
			if y != ' ' {
				changes.Unstaged = append(changes.Unstaged, path)
			}
		}
		addDirty(path)
	}
}

func parseNumstat(output []byte, stats *GitStats) {
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		if n, err := strconv.Atoi(parts[0]); err == nil {
			stats.Additions += n
		}
		if n, err := strconv.Atoi(parts[1]); err == nil {
			stats.Deletions += n
		}
		stats.FilesChanged++ // binary files use '-' and still count
	}
}

// countUntrackedStats uses Lstat and bounded reads. It never follows symlinks
// and never passes repository-controlled paths to an external utility.
func countUntrackedStats(workdir string, changes *WorktreeChanges, stats *GitStats) {
	var total int64
	for i, rel := range changes.Untracked {
		if i >= maxUntrackedFiles {
			changes.Truncated = true
			changes.TruncatedFiles += len(changes.Untracked) - i
			break
		}
		full := filepath.Join(workdir, filepath.FromSlash(rel))
		info, err := os.Lstat(full)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if info.Size() > maxUntrackedFileSize || total+info.Size() > maxUntrackedTotalBytes {
			changes.Truncated = true
			changes.TruncatedFiles++
			changes.TruncatedBytes += info.Size()
			stats.FilesChanged++
			continue
		}
		f, err := os.Open(full)
		if err != nil {
			continue
		}
		n, readErr := countLinesBounded(f, maxUntrackedFileSize+1)
		_ = f.Close()
		if readErr != nil {
			continue
		}
		total += info.Size()
		stats.Additions += n
		stats.FilesChanged++
	}
}

func countLinesBounded(r io.Reader, limit int64) (int, error) {
	s := bufio.NewScanner(io.LimitReader(r, limit))
	// Lines can legitimately be large; the byte limit is the actual bound.
	s.Buffer(make([]byte, 32*1024), int(limit))
	n := 0
	for s.Scan() {
		n++
	}
	return n, s.Err()
}

