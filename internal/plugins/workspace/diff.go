package workspace

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

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/plugins/gitstatus"
)

const (
	// maxUntrackedFileSize is the maximum size of an untracked file to include
	// as a full diff. Files larger than this get a size warning instead.
	maxUntrackedFileSize = 1 << 20 // 1 MB

	// maxUntrackedFiles is the maximum number of untracked files to include
	// as synthetic diffs. This prevents performance issues in repos with many
	// untracked files (e.g., missing .gitignore entries).
	maxUntrackedFiles = 50
)

// DiffSnapshot contains all three explicit diff views resolved from the same
// immutable worktree/base identity.
type DiffSnapshot struct {
	State                 LoadState
	WorkingTree           string
	Commits               []CommitStatusInfo
	AggregateCommitted    string
	AggregateUncommitted  string
	BaseRef               string
	MergeBase             string
	UntrackedShown        int
	UntrackedOmitted      int
	UntrackedBytesOmitted int64
	Truncated             bool
}

// loadSelectedDiff returns a command to load diff for the selected worktree.
// Also loads task details if Task tab is active.
func (p *Plugin) loadSelectedDiff() tea.Cmd {
	wt := p.selectedWorktree()
	if wt == nil {
		return nil
	}
	p.diffState = LoadStateLoading
	p.diffError = ""

	cmds := []tea.Cmd{p.loadDiff(wt)}

	// Also load task details if Task tab is active
	if p.previewTab == PreviewTabTask && wt.TaskID != "" {
		cmds = append(cmds, p.loadTaskDetailsIfNeeded())
	}

	return tea.Batch(cmds...)
}

// loadDiff returns a command to load diff for a worktree.
func (p *Plugin) loadDiff(wt *Worktree) tea.Cmd {
	if wt == nil {
		return nil
	}
	ctx, scope := p.newOperationScope(wt)
	path, name, baseRef, baseOID, headOID := wt.Path, wt.IdentityKey(), wt.BaseBranch, wt.BaseOID, wt.HEADOID
	if baseRef == "" && p.repoSnapshot != nil {
		for _, candidate := range p.repoSnapshot.Worktrees {
			if candidate.Key == wt.IdentityKey() {
				baseRef = candidate.BaseRef
				baseOID, headOID = candidate.BaseOID, candidate.HEADOID
				break
			}
		}
	}
	// The command closure must never consult mutable plugin state. Capture the
	// strategy along with its inputs before returning; RefreshDone/Init may
	// replace or clear repoSnapshot while this command is running.
	usePinnedOIDs := baseOID != "" && headOID != ""
	return func() tea.Msg {
		if err := ctx.Err(); err != nil {
			return DiffErrorMsg{OperationScope: scope, WorkspaceName: name, BaseRef: baseRef,
				Command: "load working tree, commits, and aggregate diff", Err: err}
		}
		var snapshot *DiffSnapshot
		var err error
		if usePinnedOIDs {
			snapshot, err = loadDiffSnapshotPinned(ctx, path, baseRef, baseOID, headOID)
		} else {
			// Compatibility for isolated callers without repository inventory:
			// resolve once at command start, then use only the resulting OIDs.
			snapshot, err = loadDiffSnapshot(ctx, path, baseRef)
		}
		if err != nil {
			return DiffErrorMsg{OperationScope: scope, WorkspaceName: name, BaseRef: baseRef,
				Command: "git diff HEAD / git log <base>..HEAD / git diff <merge-base>..HEAD", Err: err}
		}
		return DiffLoadedMsg{OperationScope: scope, WorkspaceName: name,
			Content: snapshot.WorkingTree, Raw: snapshot.WorkingTree, Snapshot: snapshot}
	}
}

func loadDiffSnapshot(ctx context.Context, workdir, baseRef string) (*DiffSnapshot, error) {
	if _, err := os.Lstat(workdir); err != nil {
		return nil, fmt.Errorf("inspect worktree %s: %w", workdir, err)
	}
	if baseRef == "" {
		baseRef = detectDefaultBranchContext(ctx, workdir)
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
	return loadDiffSnapshotPinned(ctx, workdir, baseRef, strings.TrimSpace(string(baseOIDBytes)), strings.TrimSpace(string(headOIDBytes)))
}

func loadDiffSnapshotPinned(ctx context.Context, workdir, baseRef, baseOID, headOID string) (*DiffSnapshot, error) {
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
	untracked, meta, err := getUntrackedFileDiffsContext(ctx, workdir)
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
	commits, err := getWorktreeCommitsBetweenContext(ctx, workdir, baseOID, headOID)
	if err != nil {
		return nil, fmt.Errorf("unique commits for base ref %q using git log %s..HEAD: %w", baseRef, baseRef, err)
	}

	state := LoadStateReady
	if working == "" && len(commits) == 0 && len(committed) == 0 {
		state = LoadStateClean
	} else if meta.Truncated {
		state = LoadStateTruncated
	}
	return &DiffSnapshot{State: state, WorkingTree: working, Commits: commits,
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

// getDiff returns the diff for a worktree, including untracked files.
func getDiff(workdir string) (content, raw string, err error) {
	tracked, err := gitOutputBytes(context.Background(), workdir, "diff", "--binary", "HEAD")
	if err != nil {
		return "", "", err
	}
	untracked, _, err := getUntrackedFileDiffsContext(context.Background(), workdir)
	if err != nil {
		return "", "", err
	}
	raw = joinDiffParts(string(tracked), untracked)
	return raw, raw, nil
}

// getUntrackedFileDiffs returns synthetic diff output for untracked files in the worktree.
// Each untracked file is shown as a new file with all lines as additions.
// Respects maxUntrackedFiles and maxUntrackedFileSize limits to avoid performance issues.
func getUntrackedFileDiffs(workdir string) string {
	out, _, _ := getUntrackedFileDiffsContext(context.Background(), workdir)
	return out
}

type untrackedDiffMeta struct {
	Shown, Omitted int
	BytesOmitted   int64
	Truncated      bool
}

func getUntrackedFileDiffsContext(ctx context.Context, workdir string) (string, untrackedDiffMeta, error) {
	return getUntrackedFileDiffsWithLstat(ctx, workdir, os.Lstat)
}

func getUntrackedFileDiffsWithLstat(ctx context.Context, workdir string, lstat func(string) (os.FileInfo, error)) (string, untrackedDiffMeta, error) {
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
		if inspected >= maxUntrackedFiles {
			meta.Omitted++
			meta.Truncated = true
			continue
		}
		// Every candidate consumes the file-count budget before any filesystem
		// operation, including symlinks, directories, errors, and missing races.
		inspected++
		full := filepath.Join(workdir, filepath.FromSlash(file))
		info, statErr := lstat(full)
		if statErr != nil || !info.Mode().IsRegular() {
			meta.Omitted++
			meta.Truncated = true
			continue
		}
		if info.Size() > maxUntrackedFileSize || total+info.Size() > maxUntrackedTotalBytes {
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
		diff, read, readErr := getUntrackedFileDiffBounded(workdir, file)
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

// getUntrackedFileDiff returns a synthetic diff for a single untracked file.
// Files exceeding maxUntrackedFileSize get a size warning instead of full content.
func getUntrackedFileDiff(workdir, file string) (string, error) {
	diff, _, err := getUntrackedFileDiffBounded(workdir, file)
	return diff, err
}

func getUntrackedFileDiffBounded(workdir, file string) (string, int64, error) {
	fullPath := filepath.Join(workdir, file)
	info, err := os.Lstat(fullPath)
	if err != nil {
		return "", 0, err
	}
	if !info.Mode().IsRegular() {
		return "", 0, fmt.Errorf("untracked path is not a regular file: %s", file)
	}
	if info.Size() > maxUntrackedFileSize {
		return truncatedUntrackedDiff(file, info.Size()), 0, nil
	}
	f, err := os.Open(fullPath)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxUntrackedFileSize+1))
	if err != nil {
		return "", 0, err
	}
	if len(data) > maxUntrackedFileSize {
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
	return fmt.Sprintf("diff --git a/%s b/%s\nnew file mode 100644\n--- /dev/null\n+++ b/%s\n@@ -0,0 +1,1 @@\n+[File too large to display: content omitted by %d-byte untracked file cap (%d bytes)]\n", file, file, file, maxUntrackedFileSize, size)
}

// getDiffStatFromBase returns the --stat output compared to the base branch.
func getDiffStatFromBase(workdir, baseBranch string) (string, error) {
	return getDiffStatFromBaseContext(context.Background(), workdir, baseBranch)
}

func getDiffStatFromBaseContext(ctx context.Context, workdir, baseBranch string) (string, error) {
	if baseBranch == "" {
		baseBranch = detectDefaultBranchContext(ctx, workdir)
	}

	// Try to find merge-base first
	mbCmd := exec.CommandContext(ctx, "git", "merge-base", baseBranch, "HEAD")
	mbCmd.Dir = workdir
	mbOutput, err := mbCmd.Output()

	var args []string
	if err == nil {
		mbHash := strings.TrimSpace(string(mbOutput))
		if len(mbHash) >= 40 {
			args = []string{"diff", "--stat", mbHash[:40] + "..HEAD"}
		} else {
			args = []string{"diff", "--stat", baseBranch + "..HEAD"}
		}
	} else {
		args = []string{"diff", "--stat", baseBranch + "..HEAD"}
	}

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = workdir
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(output)), nil
}

// FullFileDiffLoadedMsg is sent when full-file content is loaded for workspace diff view.
type FullFileDiffLoadedMsg struct {
	Epoch         uint64
	WorkspaceName string
	OldContent    string
	NewContent    string
	Parsed        *gitstatus.ParsedDiff
	FilePath      string
	CommitHash    string // Non-empty when loaded for a commit file diff
}

// GetEpoch implements plugin.EpochMessage.
func (m FullFileDiffLoadedMsg) GetEpoch() uint64 { return m.Epoch }

// loadFullFileDiffForWorkspace loads full-file content for the current file in the workspace diff view.
func (p *Plugin) loadFullFileDiffForWorkspace() tea.Cmd {
	wt := p.selectedWorktree()
	if wt == nil || p.multiFileDiff == nil {
		return nil
	}

	// Use diff tab cursor position to determine the selected file
	fileIdx := p.diffTabCursor
	if fileIdx < 0 || fileIdx >= len(p.multiFileDiff.Files) {
		if len(p.multiFileDiff.Files) > 0 {
			fileIdx = 0
		} else {
			return nil
		}
	}

	file := p.multiFileDiff.Files[fileIdx]
	filePath := file.FileName()
	workdir := wt.Path
	epoch := p.ctx.Epoch
	name := wt.IdentityKey()

	return func() tea.Msg {
		// Get old content (HEAD version)
		oldContent, _ := gitstatus.GetFileContentAtRef(workdir, filePath, "HEAD")
		// Get new content (working tree)
		newContent, _ := gitstatus.GetWorkingTreeFileContent(workdir, filePath)

		// Use HEAD-to-working-tree diff to match old/new content sources.
		// This captures both staged and unstaged changes consistently.
		rawDiff, _ := gitstatus.GetDiffFromHead(workdir, filePath)
		if rawDiff == "" {
			// New file (not yet in HEAD) — generate new file diff
			rawDiff, _ = gitstatus.GetNewFileDiff(workdir, filePath)
		}
		parsed, _ := gitstatus.ParseUnifiedDiff(rawDiff)

		return FullFileDiffLoadedMsg{
			Epoch:         epoch,
			WorkspaceName: name,
			OldContent:    oldContent,
			NewContent:    newContent,
			Parsed:        parsed,
			FilePath:      filePath,
		}
	}
}

// loadFullFileDiffForCommit loads full-file content for the currently selected commit file.
func (p *Plugin) loadFullFileDiffForCommit() tea.Cmd {
	wt := p.selectedWorktree()
	if wt == nil || p.commitDetail == nil {
		return nil
	}
	if p.commitFileCursor < 0 || p.commitFileCursor >= len(p.commitDetail.Files) {
		return nil
	}

	file := p.commitDetail.Files[p.commitFileCursor]
	filePath := file.Path
	commitHash := p.commitDetail.Hash
	parentHash := ""
	if p.commitDetail.IsMerge && len(p.commitDetail.ParentHashes) > 0 {
		parentHash = p.commitDetail.ParentHashes[0]
	}
	workdir := wt.Path
	epoch := p.ctx.Epoch
	name := wt.IdentityKey()

	return func() tea.Msg {
		parentRef := commitHash + "~1"
		if parentHash != "" {
			parentRef = parentHash
		}
		oldContent, _ := gitstatus.GetFileContentAtRef(workdir, filePath, parentRef)
		newContent, _ := gitstatus.GetFileContentAtRef(workdir, filePath, commitHash)
		rawDiff, _ := gitstatus.GetCommitDiff(workdir, commitHash, filePath, parentHash)
		parsed, _ := gitstatus.ParseUnifiedDiff(rawDiff)

		return FullFileDiffLoadedMsg{
			Epoch:         epoch,
			WorkspaceName: name,
			OldContent:    oldContent,
			NewContent:    newContent,
			Parsed:        parsed,
			FilePath:      filePath,
			CommitHash:    commitHash,
		}
	}
}

// splitLines splits a string into lines, handling various line endings.
func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			line := s[start:i]
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			lines = append(lines, line)
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// loadCommitStatus returns a command to load commit status for a worktree.
func (p *Plugin) loadCommitStatus(wt *Worktree) tea.Cmd {
	if wt == nil {
		return nil
	}
	epoch := p.ctx.Epoch // Capture epoch for stale detection
	name := wt.IdentityKey()
	path := wt.Path
	baseBranch := wt.BaseBranch

	return func() tea.Msg {
		commits, err := getWorktreeCommits(path, baseBranch)
		if err != nil {
			return CommitStatusLoadedMsg{Epoch: epoch, WorkspaceName: name, Err: err}
		}
		return CommitStatusLoadedMsg{Epoch: epoch, WorkspaceName: name, Commits: commits}
	}
}

// getWorktreeCommits returns commits unique to this branch vs base branch with status.
func getWorktreeCommits(workdir, baseBranch string) ([]CommitStatusInfo, error) {
	return getWorktreeCommitsContext(context.Background(), workdir, baseBranch)
}

func getWorktreeCommitsContext(ctx context.Context, workdir, baseBranch string) ([]CommitStatusInfo, error) {
	// If baseBranch is empty, detect the default branch
	if baseBranch == "" {
		baseBranch = detectDefaultBranchContext(ctx, workdir)
	}
	if baseBranch == "" {
		return nil, fmt.Errorf("base ref is empty")
	}

	// Try to get commits comparing against base branch
	output, err := tryGitLogContext(ctx, workdir, baseBranch)
	if err != nil {
		// Try origin/baseBranch
		output, err = tryGitLogContext(ctx, workdir, "origin/"+baseBranch)
	}
	if err != nil {
		// Last resort: detect default branch fresh (in case baseBranch was stale/wrong)
		detected := detectDefaultBranchContext(ctx, workdir)
		if detected != baseBranch {
			output, err = tryGitLogContext(ctx, workdir, detected)
			if err != nil {
				output, err = tryGitLogContext(ctx, workdir, "origin/"+detected)
			}
		}
	}
	if err != nil {
		return nil, err
	}
	return parseCommitStatusOutput(ctx, workdir, output, "HEAD")
}

func getWorktreeCommitsBetweenContext(ctx context.Context, workdir, baseOID, headOID string) ([]CommitStatusInfo, error) {
	output, err := tryGitLogRangeContext(ctx, workdir, baseOID, headOID)
	if err != nil {
		return nil, err
	}
	return parseCommitStatusOutput(ctx, workdir, output, headOID)
}

func parseCommitStatusOutput(ctx context.Context, workdir string, output []byte, headRef string) ([]CommitStatusInfo, error) {
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return []CommitStatusInfo{}, nil
	}

	// Get remote tracking branch and find unpushed commits in one batch call
	remoteBranch := getRemoteTrackingBranchContext(ctx, workdir)
	unpushed := getUnpushedCommitsToContext(ctx, workdir, remoteBranch, headRef)

	var commits []CommitStatusInfo
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

		// A commit is pushed if remote branch exists and commit is not in the unpushed set
		pushed := remoteBranch != "" && !unpushed[hash]

		commits = append(commits, CommitStatusInfo{
			Hash:    hash,
			Subject: subject,
			Pushed:  pushed,
		})
	}

	return commits, nil
}

// tryGitLog attempts to get commit log comparing HEAD to a base ref.
func tryGitLog(workdir, baseRef string) ([]byte, error) {
	return tryGitLogContext(context.Background(), workdir, baseRef)
}

func tryGitLogContext(ctx context.Context, workdir, baseRef string) ([]byte, error) {
	return tryGitLogRangeContext(ctx, workdir, baseRef, "HEAD")
}

func tryGitLogRangeContext(ctx context.Context, workdir, baseRef, headRef string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", "log", baseRef+".."+headRef, "--oneline", "--format=%h|%s")
	cmd.Dir = workdir
	return cmd.Output()
}

// detectDefaultBranch detects the default branch for a repository.
// Checks remote HEAD first, then falls back to common names.
var (
	defaultBranchCache   = make(map[string]string)
	defaultBranchCacheMu sync.RWMutex
)

func detectDefaultBranch(workdir string) string {
	return detectDefaultBranchContext(context.Background(), workdir)
}

func detectDefaultBranchContext(ctx context.Context, workdir string) string {
	if ctx.Err() != nil {
		return ""
	}
	defaultBranchCacheMu.RLock()
	if branch, ok := defaultBranchCache[workdir]; ok {
		defaultBranchCacheMu.RUnlock()
		return branch
	}
	defaultBranchCacheMu.RUnlock()

	// Try to get the remote HEAD (most reliable)
	recordGitProcess(ctx, nil)
	cmd := exec.CommandContext(ctx, "git", "symbolic-ref", "refs/remotes/origin/HEAD")
	cmd.Dir = workdir
	output, err := cmd.Output()
	if err == nil {
		// Output is like "refs/remotes/origin/main"
		ref := strings.TrimSpace(string(output))
		if branch, found := strings.CutPrefix(ref, "refs/remotes/origin/"); found {
			setDefaultBranchCache(workdir, branch)
			return branch
		}
	}
	if ctx.Err() != nil {
		return ""
	}

	// Fallback: check which common branch exists
	for _, branch := range []string{"main", "master"} {
		if ctx.Err() != nil {
			return ""
		}
		recordGitProcess(ctx, nil)
		cmd := exec.CommandContext(ctx, "git", "rev-parse", "--verify", branch)
		cmd.Dir = workdir
		if err := cmd.Run(); err == nil {
			setDefaultBranchCache(workdir, branch)
			return branch
		}
	}

	// Last resort default
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

// resolveBaseBranch returns the worktree's BaseBranch if set,
// otherwise detects the default branch from the worktree's repo.
func resolveBaseBranch(wt *Worktree) string {
	if wt.BaseBranch != "" {
		return wt.BaseBranch
	}
	return detectDefaultBranch(wt.Path)
}

// getRemoteTrackingBranch returns the remote tracking branch for HEAD.
func getRemoteTrackingBranch(workdir string) string {
	return getRemoteTrackingBranchContext(context.Background(), workdir)
}

func getRemoteTrackingBranchContext(ctx context.Context, workdir string) string {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	cmd.Dir = workdir
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

// getUnpushedCommits returns a set of short commit hashes that are in HEAD but not
// in the remote tracking branch. Uses a single git call instead of per-commit checks.
func getUnpushedCommits(workdir, remoteBranch string) map[string]bool {
	return getUnpushedCommitsContext(context.Background(), workdir, remoteBranch)
}

func getUnpushedCommitsContext(ctx context.Context, workdir, remoteBranch string) map[string]bool {
	return getUnpushedCommitsToContext(ctx, workdir, remoteBranch, "HEAD")
}

func getUnpushedCommitsToContext(ctx context.Context, workdir, remoteBranch, headRef string) map[string]bool {
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

// CommitDetailLoadedMsg is sent when commit detail (file list) is loaded.
type CommitDetailLoadedMsg struct {
	Epoch         uint64
	WorkspaceName string
	CommitHash    string
	Commit        *gitstatus.Commit
	Err           error
}

// GetEpoch implements plugin.EpochMessage.
func (m CommitDetailLoadedMsg) GetEpoch() uint64 { return m.Epoch }

// loadCommitDetail loads the file list for a specific commit.
func (p *Plugin) loadCommitDetail(hash string) tea.Cmd {
	wt := p.selectedWorktree()
	if wt == nil {
		return nil
	}
	epoch := p.ctx.Epoch
	name := wt.IdentityKey()
	workdir := wt.Path
	return func() tea.Msg {
		commit, err := gitstatus.GetCommitDetail(workdir, hash)
		return CommitDetailLoadedMsg{
			Epoch:         epoch,
			WorkspaceName: name,
			CommitHash:    hash,
			Commit:        commit,
			Err:           err,
		}
	}
}

// CommitFileDiffLoadedMsg is sent when a commit file's diff is loaded.
type CommitFileDiffLoadedMsg struct {
	Epoch         uint64
	WorkspaceName string
	CommitHash    string
	FilePath      string
	Raw           string
	Err           error
}

// GetEpoch implements plugin.EpochMessage.
func (m CommitFileDiffLoadedMsg) GetEpoch() uint64 { return m.Epoch }

// loadCommitFileDiff loads the diff for a specific file in a commit.
func (p *Plugin) loadCommitFileDiff(hash, filePath, parentHash string) tea.Cmd {
	wt := p.selectedWorktree()
	if wt == nil {
		return nil
	}
	epoch := p.ctx.Epoch
	name := wt.IdentityKey()
	workdir := wt.Path
	return func() tea.Msg {
		raw, err := gitstatus.GetCommitDiff(workdir, hash, filePath, parentHash)
		return CommitFileDiffLoadedMsg{
			Epoch:         epoch,
			WorkspaceName: name,
			CommitHash:    hash,
			FilePath:      filePath,
			Raw:           raw,
			Err:           err,
		}
	}
}
