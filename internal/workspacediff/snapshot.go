package workspacediff

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// LoadSnapshot resolves base and HEAD, then loads the pinned snapshot.
func LoadSnapshot(ctx context.Context, workdir, baseRef string) (*Snapshot, error) {
	if _, err := os.Lstat(workdir); err != nil {
		return nil, fmt.Errorf("inspect worktree %s: %w", workdir, err)
	}
	if baseRef == "" {
		baseRef = DetectDefaultBranch(ctx, workdir)
	}
	if baseRef == "" {
		return nil, fmt.Errorf("resolve base ref for git log and aggregate diff in %s", workdir)
	}
	baseOIDBytes, err := gitOutputBytes(ctx, workdir, "rev-parse", "--verify", baseRef+"^{commit}")
	if err != nil {
		return nil, fmt.Errorf("resolve base ref %q: %w", baseRef, err)
	}
	headOIDBytes, err := gitOutputBytes(ctx, workdir, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return nil, fmt.Errorf("resolve HEAD: %w", err)
	}
	return LoadSnapshotPinned(ctx, workdir, baseRef, strings.TrimSpace(string(baseOIDBytes)), strings.TrimSpace(string(headOIDBytes)))
}

// LoadSnapshotPinned loads working-tree, unique commits, and aggregate diffs
// against already-resolved OIDs.
func LoadSnapshotPinned(ctx context.Context, workdir, baseRef, baseOID, headOID string) (*Snapshot, error) {
	if _, err := os.Lstat(workdir); err != nil {
		return nil, fmt.Errorf("inspect worktree %s: %w", workdir, err)
	}
	if baseOID == "" {
		return nil, fmt.Errorf("resolved base OID is unavailable for %q", baseRef)
	}
	if headOID == "" {
		return nil, fmt.Errorf("resolved HEAD OID is unavailable")
	}

	tracked, err := gitOutputBytes(ctx, workdir, "diff", "--binary", headOID)
	if err != nil {
		return nil, err
	}
	untracked, meta, err := untrackedFileDiffs(ctx, workdir)
	if err != nil {
		return nil, err
	}
	working := joinDiffParts(string(tracked), untracked)

	mergeBaseBytes, err := gitOutputBytes(ctx, workdir, "merge-base", baseOID, headOID)
	if err != nil {
		return nil, fmt.Errorf("resolve merge-base for base ref %q: %w", baseRef, err)
	}
	mergeBase := strings.TrimSpace(string(mergeBaseBytes))
	committed, err := gitOutputBytes(ctx, workdir, "diff", "--binary", mergeBase+".."+headOID)
	if err != nil {
		return nil, fmt.Errorf("aggregate committed diff for %q (%s..HEAD): %w", baseRef, mergeBase, err)
	}
	commits, err := commitsBetween(ctx, workdir, baseOID, headOID)
	if err != nil {
		return nil, fmt.Errorf("unique commits for base ref %q using git log %s..HEAD: %w", baseRef, baseRef, err)
	}

	state := LoadStateReady
	if working == "" && len(commits) == 0 && len(committed) == 0 {
		state = LoadStateClean
	} else if meta.Truncated {
		state = LoadStateTruncated
	}
	return &Snapshot{State: state, WorkingTree: working, Commits: commits,
		AggregateCommitted: string(committed), AggregateUncommitted: working,
		BaseRef: baseRef, MergeBase: mergeBase, UntrackedShown: meta.Shown,
		UntrackedOmitted: meta.Omitted, UntrackedBytesOmitted: meta.BytesOmitted,
		Truncated: meta.Truncated}, nil
}

func gitOutputBytes(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s in %s: %s: %w", strings.Join(args, " "), dir, strings.TrimSpace(string(out)), err)
	}
	return out, nil
}

func joinDiffParts(parts ...string) string {
	var nonempty []string
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			nonempty = append(nonempty, strings.TrimRight(part, "\n"))
		}
	}
	return strings.Join(nonempty, "\n")
}

type untrackedDiffMeta struct {
	Shown, Omitted int
	BytesOmitted   int64
	Truncated      bool
}

func untrackedFileDiffs(ctx context.Context, workdir string) (string, untrackedDiffMeta, error) {
	return untrackedFileDiffsWithLstat(ctx, workdir, os.Lstat)
}

func untrackedFileDiffsWithLstat(ctx context.Context, workdir string, lstat func(string) (os.FileInfo, error)) (string, untrackedDiffMeta, error) {
	output, err := gitOutputBytes(ctx, workdir, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return "", untrackedDiffMeta{}, err
	}
	fields := bytes.Split(output, []byte{0})
	var sb strings.Builder
	var meta untrackedDiffMeta
	var total int64
	inspected := 0
	for _, field := range fields {
		if len(field) == 0 {
			continue
		}
		file := string(field)
		if inspected >= MaxUntrackedFiles {
			meta.Omitted++
			meta.Truncated = true
			continue
		}
		inspected++
		full := filepath.Join(workdir, filepath.FromSlash(file))
		info, statErr := lstat(full)
		if statErr != nil || !info.Mode().IsRegular() {
			meta.Omitted++
			meta.Truncated = true
			continue
		}
		if info.Size() > MaxUntrackedFileSize || total+info.Size() > MaxUntrackedTotalBytes {
			meta.Omitted++
			meta.BytesOmitted += info.Size()
			meta.Truncated = true
			diff := truncatedUntrackedDiff(file, info.Size())
			if sb.Len() > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(diff)
			continue
		}
		diff, read, readErr := untrackedFileDiffBounded(workdir, file)
		if readErr != nil {
			meta.Omitted++
			meta.Truncated = true
			continue
		}
		total += read
		meta.Shown++
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(diff)
	}
	return sb.String(), meta, nil
}

func untrackedFileDiffBounded(workdir, file string) (string, int64, error) {
	fullPath := filepath.Join(workdir, file)
	info, err := os.Lstat(fullPath)
	if err != nil {
		return "", 0, err
	}
	if !info.Mode().IsRegular() {
		return "", 0, fmt.Errorf("untracked path is not a regular file: %s", file)
	}
	if info.Size() > MaxUntrackedFileSize {
		return truncatedUntrackedDiff(file, info.Size()), 0, nil
	}
	f, err := os.Open(fullPath)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, MaxUntrackedFileSize+1))
	if err != nil {
		return "", 0, err
	}
	if len(data) > MaxUntrackedFileSize {
		return truncatedUntrackedDiff(file, info.Size()), 0, nil
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return fmt.Sprintf("diff --git a/%s b/%s\nnew file mode 100644\nBinary files /dev/null and b/%s differ\n", file, file, file), int64(len(data)), nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "diff --git a/%s b/%s\nnew file mode 100644\n--- /dev/null\n+++ b/%s\n", file, file, file)
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	fmt.Fprintf(&sb, "@@ -0,0 +1,%d @@\n", len(lines))
	for _, line := range lines {
		sb.WriteByte('+')
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	return sb.String(), int64(len(data)), nil
}

func truncatedUntrackedDiff(file string, size int64) string {
	return fmt.Sprintf("diff --git a/%s b/%s\nnew file mode 100644\n--- /dev/null\n+++ b/%s\n@@ -0,0 +1,1 @@\n+[File too large to display: content omitted by %d-byte untracked file cap (%d bytes)]\n", file, file, file, MaxUntrackedFileSize, size)
}

func commitsBetween(ctx context.Context, workdir, baseOID, headOID string) ([]CommitInfo, error) {
	output, err := gitLogRange(ctx, workdir, baseOID, headOID)
	if err != nil {
		return nil, err
	}
	return parseCommitStatus(ctx, workdir, output, headOID)
}

func parseCommitStatus(ctx context.Context, workdir string, output []byte, headRef string) ([]CommitInfo, error) {
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return []CommitInfo{}, nil
	}

	remoteBranch := remoteTrackingBranch(ctx, workdir)
	unpushed := unpushedCommits(ctx, workdir, remoteBranch, headRef)

	var commits []CommitInfo
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		if len(parts) < 2 {
			continue
		}
		hash := parts[0]
		subject := parts[1]
		pushed := remoteBranch != "" && !unpushed[hash]
		commits = append(commits, CommitInfo{Hash: hash, Subject: subject, Pushed: pushed})
	}
	return commits, nil
}

func gitLogRange(ctx context.Context, workdir, baseRef, headRef string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", "log", baseRef+".."+headRef, "--oneline", "--format=%h|%s")
	cmd.Dir = workdir
	return cmd.Output()
}

func remoteTrackingBranch(ctx context.Context, workdir string) string {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	cmd.Dir = workdir
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func unpushedCommits(ctx context.Context, workdir, remoteBranch, headRef string) map[string]bool {
	if remoteBranch == "" || workdir == "" {
		return nil
	}
	cmd := exec.CommandContext(ctx, "git", "log", remoteBranch+".."+headRef, "--format=%h")
	cmd.Dir = workdir
	output, err := cmd.Output()
	if err != nil {
		return nil
	}
	result := make(map[string]bool)
	for _, h := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if h != "" {
			result[h] = true
		}
	}
	return result
}

var (
	defaultBranchCache   = make(map[string]string)
	defaultBranchCacheMu sync.RWMutex
)

// DetectDefaultBranch finds the repo's default branch (remote HEAD, then main/master).
func DetectDefaultBranch(ctx context.Context, workdir string) string {
	if ctx.Err() != nil {
		return ""
	}
	defaultBranchCacheMu.RLock()
	if branch, ok := defaultBranchCache[workdir]; ok {
		defaultBranchCacheMu.RUnlock()
		return branch
	}
	defaultBranchCacheMu.RUnlock()

	cmd := exec.CommandContext(ctx, "git", "symbolic-ref", "refs/remotes/origin/HEAD")
	cmd.Dir = workdir
	output, err := cmd.Output()
	if err == nil {
		ref := strings.TrimSpace(string(output))
		if branch, found := strings.CutPrefix(ref, "refs/remotes/origin/"); found {
			setDefaultBranchCache(workdir, branch)
			return branch
		}
	}
	if ctx.Err() != nil {
		return ""
	}

	for _, branch := range []string{"main", "master"} {
		if ctx.Err() != nil {
			return ""
		}
		cmd := exec.CommandContext(ctx, "git", "rev-parse", "--verify", branch)
		cmd.Dir = workdir
		if err := cmd.Run(); err == nil {
			setDefaultBranchCache(workdir, branch)
			return branch
		}
	}

	if ctx.Err() != nil {
		return ""
	}
	setDefaultBranchCache(workdir, "main")
	return "main"
}

func setDefaultBranchCache(workdir, branch string) {
	defaultBranchCacheMu.Lock()
	defaultBranchCache[workdir] = branch
	defaultBranchCacheMu.Unlock()
}
