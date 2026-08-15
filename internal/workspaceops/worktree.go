package workspaceops

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/projectdir"
	"golang.org/x/sys/unix"
)

// WorktreePlan is the presentation-neutral contract confirmed before Git is
// mutated. UI-only choices may be retained by hosts beside this value.
type WorktreePlan struct {
	RepoKey        string   `json:"repoKey"`
	OperationID    string   `json:"operationId"`
	SourceWorktree string   `json:"sourceWorktree"`
	MainWorktree   string   `json:"mainWorktree"`
	SourceRef      string   `json:"sourceRef"`
	SourceOID      string   `json:"sourceOid"`
	Branch         string   `json:"branch"`
	Path           string   `json:"path"`
	DisplayName    string   `json:"displayName"`
	TaskID         string   `json:"taskId,omitempty"`
	TaskTitle      string   `json:"taskTitle,omitempty"`
	AgentType      string   `json:"agentType,omitempty"`
	SkipPerms      bool     `json:"skipPerms,omitempty"`
	RemotePolicy   string   `json:"remotePolicy"`
	CopyEnv        bool     `json:"copyEnv"`
	EnvFiles       []string `json:"envFiles,omitempty"`
	RunHook        bool     `json:"runHook"`
	HookPath       string   `json:"hookPath"`
	HookRequired   bool     `json:"hookRequired"`
}

// WorktreeRecord is the lifecycle identity returned by creation. It
// deliberately owns no panes, output buffers, trackers, or plugin state.
type WorktreeRecord struct {
	Key        string    `json:"key"`
	RepoKey    string    `json:"repoKey"`
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	Branch     string    `json:"branch"`
	BaseBranch string    `json:"baseBranch"`
	HEADOID    string    `json:"headOid"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// ResolveWorktreePlan validates names, source identity, destination
// containment, and configured setup without changing the repository.
func ResolveWorktreePlan(ctx context.Context, workDir, projectRoot, name, base string, dirPrefix bool, setup config.WorktreeSetupConfig) (*WorktreePlan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	displayName := strings.TrimSpace(name)
	if displayName == "" {
		return nil, fmt.Errorf("workspace name is required")
	}
	slug := SlugifyWorktreeName(displayName)
	if slug == "" {
		return nil, fmt.Errorf("invalid branch name %q", displayName)
	}
	if _, err := gitOutput(ctx, workDir, "check-ref-format", "--branch", slug); err != nil {
		return nil, fmt.Errorf("invalid branch name %q: %w", slug, err)
	}
	if _, err := gitOutput(ctx, workDir, "show-ref", "--verify", "--quiet", "refs/heads/"+slug); err == nil {
		return nil, fmt.Errorf("branch %q already exists", slug)
	}
	sourceWorktree, err := gitOutput(ctx, workDir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("resolve source worktree: %w", err)
	}
	mainWorktree := projectRoot
	if mainWorktree == "" {
		mainWorktree = MainWorktreePath(ctx, workDir)
	}
	if mainWorktree == "" {
		mainWorktree = sourceWorktree
	}
	mainWorktree, _ = filepath.Abs(mainWorktree)
	if resolved, resolveErr := filepath.EvalSymlinks(mainWorktree); resolveErr == nil {
		mainWorktree = resolved
	}
	requestedBase := strings.TrimSpace(base)
	if requestedBase == "" {
		requestedBase = "HEAD"
	}
	sourceOID, err := gitOutput(ctx, workDir, "rev-parse", "--verify", requestedBase+"^{commit}")
	if err != nil {
		return nil, fmt.Errorf("source %q is not a commit: %w", requestedBase, err)
	}
	sourceRef, err := gitOutput(ctx, workDir, "rev-parse", "--symbolic-full-name", requestedBase)
	if err != nil || sourceRef == "" {
		sourceRef = requestedBase
	}
	dirName := slug
	if dirPrefix {
		if repo := RepoName(ctx, workDir); repo != "" {
			dirName = repo + "-" + slug
		}
	}
	destination := filepath.Join(filepath.Dir(mainWorktree), dirName)
	if err := EnsureRealDirectoryPath(filepath.Dir(mainWorktree), filepath.Dir(destination), false); err != nil {
		return nil, fmt.Errorf("destination parent is unsafe: %w", err)
	}
	if _, err := os.Lstat(destination); err == nil {
		return nil, fmt.Errorf("destination path already exists: %s", destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect destination path: %w", err)
	}
	envFiles := make([]string, 0, len(setup.EnvFiles))
	if setup.CopyEnvFiles {
		for _, rel := range setup.EnvFiles {
			if !SafeRelativePath(rel) {
				return nil, fmt.Errorf("env file path must stay within the main worktree: %q", rel)
			}
			if _, err := ContainedRegularFile(mainWorktree, rel); err == nil {
				envFiles = append(envFiles, rel)
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("env file %q is unsafe: %w", rel, err)
			}
		}
	}
	hookPath := setup.HookPath
	if hookPath == "" {
		hookPath = ".sidecar-setup"
	}
	if !SafeRelativePath(hookPath) {
		return nil, fmt.Errorf("setup hook path must stay within the main worktree: %q", hookPath)
	}
	runHook := setup.RunHook
	if runHook {
		if _, err := ContainedRegularFile(mainWorktree, hookPath); errors.Is(err, os.ErrNotExist) {
			runHook = false
		} else if err != nil {
			return nil, fmt.Errorf("setup hook %q is unsafe: %w", hookPath, err)
		}
	}
	return &WorktreePlan{SourceWorktree: sourceWorktree, MainWorktree: mainWorktree, SourceRef: sourceRef, SourceOID: sourceOID,
		Branch: slug, Path: destination, DisplayName: displayName, RemotePolicy: "local branch only; no remote push",
		CopyEnv: len(envFiles) > 0, EnvFiles: envFiles, RunHook: runHook, HookPath: hookPath, HookRequired: setup.HookRequired}, nil
}

// ExecuteWorktree revalidates the confirmed plan, creates into a pinned
// staging directory, and moves/repairs it into the confirmed destination.
func ExecuteWorktree(ctx context.Context, repoKey string, plan *WorktreePlan) (*WorktreeRecord, error) {
	return ExecuteWorktreeWithRunner(ctx, repoKey, plan, func(cmd *exec.Cmd) ([]byte, error) { return cmd.CombinedOutput() })
}

func ExecuteWorktreeWithRunner(ctx context.Context, repoKey string, plan *WorktreePlan, run func(*exec.Cmd) ([]byte, error)) (*WorktreeRecord, error) {
	if plan == nil {
		return nil, fmt.Errorf("missing worktree plan")
	}
	if _, err := gitOutput(ctx, plan.SourceWorktree, "check-ref-format", "--branch", plan.Branch); err != nil {
		return nil, fmt.Errorf("branch is no longer valid: %w", err)
	}
	if current, err := gitOutput(ctx, plan.SourceWorktree, "rev-parse", "--verify", plan.SourceRef+"^{commit}"); err != nil || current != plan.SourceOID {
		return nil, fmt.Errorf("source changed since confirmation (expected %s, got %s)", shortOID(plan.SourceOID), shortOID(current))
	}
	if _, err := os.Lstat(plan.Path); err == nil {
		return nil, fmt.Errorf("destination path now exists: %s", plan.Path)
	}
	allowedRoot := filepath.Dir(plan.MainWorktree)
	parentRel, err := filepath.Rel(allowedRoot, filepath.Dir(plan.Path))
	if err != nil {
		return nil, fmt.Errorf("resolve destination parent: %w", err)
	}
	parent, err := OpenPinnedDirectory(allowedRoot, parentRel, true)
	if err != nil {
		return nil, fmt.Errorf("pin destination parent: %w", err)
	}
	defer parent.Close()
	leaf := filepath.Base(plan.Path)
	if fd, openErr := unix.Openat(int(parent.Fd()), leaf, unix.O_RDONLY|unix.O_NOFOLLOW, 0); openErr == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("destination path already exists: %s", plan.Path)
	} else if !errors.Is(openErr, unix.ENOENT) {
		return nil, fmt.Errorf("inspect pinned destination: %w", openErr)
	}
	rootDir, err := OpenPinnedDirectory(allowedRoot, ".", false)
	if err != nil {
		return nil, fmt.Errorf("pin destination root: %w", err)
	}
	defer rootDir.Close()
	stagingName, err := MkdirPinnedTemp(rootDir)
	if err != nil {
		return nil, fmt.Errorf("create pinned staging directory: %w", err)
	}
	rootPath, err := PinnedDirectoryPath(rootDir)
	if err != nil {
		_ = unix.Unlinkat(int(rootDir.Fd()), stagingName, unix.AT_REMOVEDIR)
		return nil, err
	}
	stagingPath := filepath.Join(rootPath, stagingName)
	cmd := exec.CommandContext(ctx, "git", "worktree", "add", "-b", plan.Branch, stagingPath, plan.SourceOID)
	cmd.Dir = plan.SourceWorktree
	output, addRunErr := run(cmd)
	head, stagingErr := gitOutput(context.Background(), stagingPath, "rev-parse", "HEAD")
	if stagingErr != nil {
		_ = unix.Unlinkat(int(rootDir.Fd()), stagingName, unix.AT_REMOVEDIR)
		if addRunErr != nil {
			return nil, fmt.Errorf("git worktree add: %s: %w", strings.TrimSpace(string(output)), addRunErr)
		}
		return nil, fmt.Errorf("verify staged worktree: %w", stagingErr)
	}
	if head != plan.SourceOID {
		return nil, fmt.Errorf("verify created worktree identity: got %s want %s", head, plan.SourceOID)
	}
	moveErr := unix.Renameat(int(rootDir.Fd()), stagingName, int(parent.Fd()), leaf)
	actualPath := stagingPath
	if moveErr == nil {
		actualParent, pathErr := PinnedDirectoryPath(parent)
		if pathErr != nil {
			return createdRecord(repoKey, plan, head), pathErr
		}
		actualPath = filepath.Join(actualParent, leaf)
		if _, repairErr := gitOutput(context.Background(), plan.SourceWorktree, "worktree", "repair", actualPath); repairErr != nil {
			moved := *plan
			moved.Path = actualPath
			return createdRecord(repoKey, &moved, head), fmt.Errorf("repair moved worktree metadata: %w", repairErr)
		}
	}
	confirmedPath := filepath.Clean(plan.Path)
	plan.Path = filepath.Clean(actualPath)
	record := createdRecord(repoKey, plan, head)
	if addRunErr != nil {
		return record, fmt.Errorf("git worktree add: %s: %w", strings.TrimSpace(string(output)), addRunErr)
	}
	if moveErr != nil {
		return record, fmt.Errorf("move staged worktree into pinned destination: %w", moveErr)
	}
	if plan.Path != confirmedPath {
		return record, fmt.Errorf("destination parent identity changed during creation; worktree was retained at %s", plan.Path)
	}
	return record, nil
}

func createdRecord(repoKey string, plan *WorktreePlan, head string) *WorktreeRecord {
	key := StablePathKey(plan.Path)
	if projectKey, err := projectdir.WorktreeKey(plan.Path); err == nil {
		key = projectKey
	}
	now := time.Now()
	return &WorktreeRecord{Key: key, RepoKey: repoKey, Name: plan.DisplayName, Path: plan.Path, Branch: plan.Branch, BaseBranch: strings.TrimPrefix(plan.SourceRef, "refs/heads/"), HEADOID: head, CreatedAt: now, UpdatedAt: now}
}

func StablePathKey(path string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(path)))
	return fmt.Sprintf("%x", sum[:])
}
func shortOID(oid string) string {
	if len(oid) > 8 {
		return oid[:8]
	}
	return oid
}

func MainWorktreePath(ctx context.Context, workDir string) string {
	out, err := gitOutput(ctx, workDir, "--no-optional-locks", "worktree", "list", "--porcelain")
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

func RepoName(ctx context.Context, workDir string) string {
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
	abs, absErr := filepath.Abs(workDir)
	if absErr != nil {
		return ""
	}
	return filepath.Base(abs)
}

const maxWorktreeSlugRunes = 63

func SlugifyWorktreeName(name string) string {
	result := strings.ToLower(strings.TrimSpace(name))
	result = strings.ReplaceAll(result, " ", "-")
	result = strings.ReplaceAll(result, "@{", "")
	for _, char := range []string{"~", "^", ":", "?", "*", "[", "\\"} {
		result = strings.ReplaceAll(result, char, "")
	}
	var cleaned strings.Builder
	for _, r := range result {
		if r >= 32 && r != 127 {
			cleaned.WriteRune(r)
		}
	}
	result = collapseSlugSeparators(cleaned.String())
	result = strings.Trim(result, "-./")
	result = truncateSlugRunes(result, maxWorktreeSlugRunes)
	result = strings.Trim(result, "-./")
	if result == "" || result == "@" || strings.HasSuffix(result, ".lock") {
		return ""
	}
	return result
}
func collapseSlugSeparators(s string) string {
	var b strings.Builder
	var prev rune
	for i, r := range s {
		if i > 0 && (r == '-' || r == '/' || r == '.') && r == prev {
			continue
		}
		b.WriteRune(r)
		prev = r
	}
	return b.String()
}
func truncateSlugRunes(s string, max int) string {
	runes := []rune(s)
	if max <= 0 || len(runes) <= max {
		return s
	}
	runes = runes[:max]
	for i := len(runes) - 1; i > 0; i-- {
		if runes[i] == '-' {
			return string(runes[:i])
		}
	}
	return string(runes)
}
