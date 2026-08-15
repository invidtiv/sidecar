package workspace

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/startuptrace"
)

type gitProcessCounterKey struct{}

func recordGitProcess(ctx context.Context, explicit *atomic.Int64) {
	if explicit != nil {
		startuptrace.Count("workspace.refresh.git.spawn")
		explicit.Add(1)
		return
	}
	if counter, ok := ctx.Value(gitProcessCounterKey{}).(*atomic.Int64); ok && counter != nil {
		startuptrace.Count("workspace.refresh.git.spawn")
		counter.Add(1)
	}
}

// RepoSnapshot is one immutable inventory used to identify and validate all
// worktree lifecycle operations. Callers replace the whole value after refresh;
// commands never mutate it.
type RepoSnapshot struct {
	Key                string
	CanonicalRoot      string
	CanonicalCommonDir string
	Worktrees          []WorktreeSnapshot
	CheckedOut         map[string]string // local branch -> stable worktree key
}

// OperationScope is copied into every lifecycle command result so Update can
// reject work completed for a prior repository, worktree, or plugin epoch.
type OperationScope struct {
	Epoch       uint64
	OperationID string
	RepoKey     string
	WorktreeKey string
	Lifecycle   bool
}

func (s OperationScope) GetOperationScope() OperationScope { return s }

// WorktreeSnapshot contains Git identity and routing facts, separate from the
// user-facing Worktree.Name.
type WorktreeSnapshot struct {
	Key      string
	RepoKey  string
	Path     string
	Branch   string
	Detached bool
	HEADOID  string
	BaseRef  string
	BaseOID  string
	Remote   string
	Upstream string
	Locked   bool
	Prunable bool
	Bare     bool
	IsMain   bool
}

type branchInventory struct {
	OID      string
	Upstream string
	Remote   string
}

// BuildRepoSnapshot reads one porcelain worktree inventory and enriches it with
// a single branch-ref inventory. This avoids independently rediscovering
// correctness-critical paths and branch ownership during an operation.
func BuildRepoSnapshot(ctx context.Context, repoPath string) (*RepoSnapshot, error) {
	common, err := gitOutputContext(ctx, repoPath, "rev-parse", "--git-common-dir")
	if err != nil {
		return nil, fmt.Errorf("resolve common git directory: %w", err)
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(repoPath, common)
	}
	common = canonicalGitPath(common)
	repoKey := stablePathKey(common)

	out, err := gitOutputContext(ctx, repoPath, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("inventory worktrees: %w", err)
	}
	states, err := parseGitWorktreeStates(out)
	if err != nil {
		return nil, err
	}
	if len(states) == 0 {
		return nil, fmt.Errorf("git returned an empty worktree inventory")
	}
	root := states[0].Path

	refs, err := loadBranchInventory(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	snapshot := &RepoSnapshot{
		Key: repoKey, CanonicalRoot: root, CanonicalCommonDir: common,
		Worktrees:  make([]WorktreeSnapshot, 0, len(states)),
		CheckedOut: make(map[string]string),
	}
	for i, state := range states {
		key, keyErr := projectdir.WorktreeKey(state.Path)
		if keyErr != nil {
			return nil, fmt.Errorf("key worktree %q: %w", state.Path, keyErr)
		}
		wt := WorktreeSnapshot{
			Key: key, RepoKey: repoKey, Path: state.Path, Branch: state.Branch,
			Detached: state.Detached, HEADOID: state.HEAD, Locked: state.Locked,
			Prunable: state.Prunable, Bare: state.Bare, IsMain: i == 0,
		}
		if ref, ok := refs[state.Branch]; ok {
			wt.Upstream, wt.Remote = ref.Upstream, ref.Remote
		}
		wt.BaseRef = loadBaseBranchContext(ctx, root, state.Path)
		if wt.BaseRef == "" && !wt.IsMain {
			wt.BaseRef = detectDefaultBranchContext(ctx, repoPath)
		}
		if wt.BaseRef != "" {
			wt.BaseOID, _ = gitOutputContext(ctx, repoPath, "rev-parse", "--verify", wt.BaseRef+"^{commit}")
		}
		if wt.Branch != "" {
			snapshot.CheckedOut[wt.Branch] = key
		}
		snapshot.Worktrees = append(snapshot.Worktrees, wt)
	}
	return snapshot, nil
}

func mainWorktreePathContext(ctx context.Context, workDir string) string {
	out, err := gitOutputContext(ctx, workDir, "--no-optional-locks", "worktree", "list", "--porcelain")
	if err != nil {
		return ""
	}
	for line := range strings.SplitSeq(out, "\n") {
		if path, ok := strings.CutPrefix(line, "worktree "); ok {
			return filepath.Clean(path)
		}
	}
	return ""
}

func repoNameContext(ctx context.Context, workDir string) string {
	cmd := exec.CommandContext(ctx, "git", "--no-optional-locks", "remote", "get-url", "origin")
	cmd.Dir = workDir
	output, err := cmd.Output()
	if err == nil {
		url := strings.TrimSuffix(strings.TrimSpace(string(output)), ".git")
		if idx := strings.LastIndex(url, ":"); idx != -1 && !strings.Contains(url, "://") {
			url = url[idx+1:]
		}
		if idx := strings.LastIndex(url, "/"); idx != -1 {
			return url[idx+1:]
		}
		return url
	}
	if ctx.Err() != nil {
		return ""
	}
	if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() == 128 {
		return ""
	}
	abs, absErr := filepath.Abs(workDir)
	if absErr != nil {
		return ""
	}
	return filepath.Base(abs)
}

func parseGitWorktreeStates(output string) ([]gitWorktreeState, error) {
	var result []gitWorktreeState
	var current *gitWorktreeState
	flush := func() {
		if current != nil {
			current.Path = canonicalGitPath(current.Path)
			result = append(result, *current)
		}
	}
	for _, line := range strings.Split(output, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			current = &gitWorktreeState{Path: strings.TrimPrefix(line, "worktree ")}
		case current == nil:
			continue
		case strings.HasPrefix(line, "HEAD "):
			current.HEAD = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch refs/heads/"):
			current.Branch = strings.TrimPrefix(line, "branch refs/heads/")
		case line == "bare":
			current.Bare = true
		case line == "detached":
			current.Detached = true
		case line == "locked" || strings.HasPrefix(line, "locked "):
			current.Locked = true
		case line == "prunable" || strings.HasPrefix(line, "prunable "):
			current.Prunable = true
		}
	}
	flush()
	return result, nil
}

func loadBranchInventory(ctx context.Context, repoPath string) (map[string]branchInventory, error) {
	out, err := gitOutputContext(ctx, repoPath, "for-each-ref", "--format=%(refname:short)%00%(objectname)%00%(upstream:short)%00%(upstream:remotename)", "refs/heads")
	if err != nil {
		return nil, fmt.Errorf("inventory branches: %w", err)
	}
	result := make(map[string]branchInventory)
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Split(line, "\x00")
		if len(parts) != 4 || parts[0] == "" {
			continue
		}
		result[parts[0]] = branchInventory{OID: parts[1], Upstream: parts[2], Remote: parts[3]}
	}
	return result, nil
}

func gitOutputContext(ctx context.Context, dir string, args ...string) (string, error) {
	recordGitProcess(ctx, nil)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func stablePathKey(path string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(path)))
	return fmt.Sprintf("%x", sum[:])
}

func snapshotToWorktrees(snapshot *RepoSnapshot) []*Worktree {
	if snapshot == nil {
		return nil
	}
	parent := filepath.Dir(snapshot.CanonicalRoot)
	result := make([]*Worktree, 0, len(snapshot.Worktrees))
	for _, item := range snapshot.Worktrees {
		name := filepath.Base(item.Path)
		if !item.IsMain {
			if rel, err := filepath.Rel(parent, item.Path); err == nil && rel != "" {
				name = rel
			}
		}
		if display := loadDisplayName(snapshot.CanonicalRoot, item.Path); display != "" {
			name = display
		}
		info, statErr := os.Stat(item.Path)
		// The row has always had an age column and it has always been blank for
		// discovered worktrees, because nothing on this path ever set UpdatedAt.
		// This stat is already being made to decide IsMissing, so the directory's
		// modification time is free: it is the last time anything happened in the
		// worktree that Sidecar can see without a session attached. A worktree
		// that does have an agent reports its last output instead, which is both
		// fresher and more truthful about work happening deep in the tree.
		updatedAt := time.Time{}
		if statErr == nil && info != nil {
			updatedAt = info.ModTime()
		}
		result = append(result, &Worktree{
			Key: item.Key, RepoKey: item.RepoKey, Name: name, Path: item.Path,
			Branch: item.Branch, BaseBranch: item.BaseRef, HEADOID: item.HEADOID,
			BaseOID: item.BaseOID, Remote: item.Remote, Upstream: item.Upstream,
			Status: StatusPaused, IsMain: item.IsMain, IsBare: item.Bare,
			IsDetached: item.Detached, IsLocked: item.Locked,
			IsPrunable: item.Prunable, IsMissing: item.Prunable || os.IsNotExist(statErr),
			UpdatedAt: updatedAt,
		})
	}
	return result
}
