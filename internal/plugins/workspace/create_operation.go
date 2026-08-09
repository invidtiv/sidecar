package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/projectdir"
	"golang.org/x/sys/unix"
)

// CreateOperationPlan is the immutable contract shown to the user before any
// repository mutation. It is also retained for retry and identity-checked undo.
type CreateOperationPlan struct {
	RepoKey        string
	OperationID    string
	SourceWorktree string
	MainWorktree   string
	SourceRef      string
	SourceOID      string
	Branch         string
	Path           string
	DisplayName    string
	TaskID         string
	TaskTitle      string
	AgentType      AgentType
	SkipPerms      bool
	Prompt         *Prompt
	RemotePolicy   string
	CopyEnv        bool
	EnvFiles       []string
	RunHook        bool
	HookPath       string
	HookRequired   bool
}

type pendingCreationJournal struct {
	Version     int                 `json:"version"`
	RepoKey     string              `json:"repoKey"`
	OperationID string              `json:"operationId"`
	Plan        CreateOperationPlan `json:"plan"`
	Worktree    Worktree            `json:"worktree"`
}

func pendingCreationPath(ctx context.Context, plan *CreateOperationPlan) (string, error) {
	dir, err := projectdir.WorktreeDirContext(ctx, plan.MainWorktree, plan.Path)
	if err != nil {
		return "", err
	}
	key := stablePathKey(plan.OperationID)
	return filepath.Join(dir, "pending-creation-"+key[:12]+".json"), nil
}

func persistPendingCreation(ctx context.Context, plan *CreateOperationPlan, wt *Worktree) error {
	path, err := pendingCreationPath(ctx, plan)
	if err != nil {
		return fmt.Errorf("resolve pending creation journal: %w", err)
	}
	data, err := json.MarshalIndent(pendingCreationJournal{Version: 1, RepoKey: plan.RepoKey, OperationID: plan.OperationID, Plan: *plan, Worktree: *wt}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode pending creation journal: %w", err)
	}
	if err := writeDurableFile(path, append(data, '\n'), 0644); err != nil {
		return fmt.Errorf("write pending creation journal: %w", err)
	}
	return nil
}

func removePendingCreation(plan *CreateOperationPlan) error {
	return removePendingCreationWithOps(plan, os.Remove, func(dir string) error {
		file, err := os.Open(dir)
		if err != nil {
			return err
		}
		defer file.Close()
		return file.Sync()
	})
}

func removePendingCreationWithOps(plan *CreateOperationPlan, remove func(string) error, syncDir func(string) error) error {
	path, err := pendingCreationPath(context.Background(), plan)
	if err != nil {
		return err
	}
	if err := remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := syncDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("sync pending creation journal directory: %w", err)
	}
	return nil
}

func (p *Plugin) clearPendingCreation(plan *CreateOperationPlan) error {
	if p.removePendingCreationFn != nil {
		return p.removePendingCreationFn(plan)
	}
	return removePendingCreation(plan)
}

func loadPendingCreation(ctx context.Context, projectRoot string, worktrees []*Worktree, repoKey string) (*pendingCreationJournal, error) {
	for _, wt := range worktrees {
		dir, err := projectdir.WorktreeDirContext(ctx, projectRoot, wt.Path)
		if err != nil {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasPrefix(entry.Name(), "pending-creation-") || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			data, readErr := os.ReadFile(filepath.Join(dir, entry.Name()))
			if readErr != nil {
				continue
			}
			var journal pendingCreationJournal
			if json.Unmarshal(data, &journal) != nil || journal.Version != 1 || journal.RepoKey != repoKey {
				continue
			}
			if journal.Worktree.IdentityKey() != wt.IdentityKey() || filepath.Clean(journal.Worktree.Path) != filepath.Clean(wt.Path) || journal.Worktree.HEADOID != wt.HEADOID {
				continue
			}
			return &journal, nil
		}
	}
	return nil, nil
}

func (p *Plugin) reconcilePendingCreation() bool {
	if p.repoSnapshot == nil || p.ctx == nil {
		return false
	}
	for i, msg := range p.deferredCreations {
		if msg.RepoKey != p.repoSnapshot.Key {
			continue
		}
		wt := p.findWorktree(msg.Worktree.IdentityKey())
		if wt == nil || wt.HEADOID != msg.Worktree.HEADOID {
			continue
		}
		p.deferredCreations = append(p.deferredCreations[:i], p.deferredCreations[i+1:]...)
		reason := msg.Err
		if reason == nil {
			reason = fmt.Errorf("creation result arrived after its original UI context ended")
		}
		p.surfaceInterruptedCreation(msg.Plan, wt, reason)
		return true
	}
	ctx := p.operationCtx
	if ctx == nil {
		ctx = context.Background()
	}
	journal, _ := loadPendingCreation(ctx, p.repoSnapshot.CanonicalRoot, p.worktrees, p.repoSnapshot.Key)
	if journal == nil {
		return false
	}
	wt := p.findWorktree(journal.Worktree.IdentityKey())
	if wt == nil {
		return false
	}
	plan := journal.Plan
	p.surfaceInterruptedCreation(&plan, wt, fmt.Errorf("creation was interrupted before setup completed"))
	return true
}

func (p *Plugin) surfaceInterruptedCreation(plan *CreateOperationPlan, wt *Worktree, reason error) {
	p.createPlan = plan
	p.createSetupResult = &CreateSetupResult{Worktree: wt, Outcomes: []CreateSetupOutcome{{Kind: CreateOutcomeIdentity, Action: "resume pending creation", Required: true, Err: reason}}}
	p.createBusyStep = ""
	p.createDeleteResult = nil
	p.createOperationModal = nil
	p.createOperationWidth = 0
	p.viewMode = ViewModeCreate
	p.selectCreatedWorktree(wt)
	p.newLifecycleScope(wt)
}

func (p *Plugin) selectCreatedWorktree(wt *Worktree) {
	idx := -1
	for i, existing := range p.worktrees {
		if existing.IdentityKey() == wt.IdentityKey() {
			idx = i
			break
		}
	}
	if idx < 0 {
		p.worktrees = append(p.worktrees, wt)
		idx = len(p.worktrees) - 1
	}
	p.shellSelected = false
	p.selectedIdx = idx
	p.previewOffset = 0
	p.autoScrollOutput = true
	p.saveSelectionState()
	p.ensureVisible()
}

func (p *Plugin) finishCreatedWorktree(plan *CreateOperationPlan, wt *Worktree) []tea.Cmd {
	p.selectCreatedWorktree(wt)
	p.viewMode = ViewModeList
	p.clearCreateModal()
	cmds := []tea.Cmd{p.loadSelectedContent()}
	if plan.AgentType != AgentNone && plan.AgentType != "" {
		cmds = append(cmds, p.StartAgentWithOptions(wt, plan.AgentType, plan.SkipPerms, plan.Prompt))
	} else {
		cmds = append(cmds, p.AttachToWorktreeDir(wt))
	}
	return cmds
}

type CreateOutcomeKind string

const (
	CreateOutcomeIdentity CreateOutcomeKind = "identity"
	CreateOutcomeTDRoot   CreateOutcomeKind = "td-root"
	CreateOutcomeTaskLink CreateOutcomeKind = "task-link"
	CreateOutcomeTDStart  CreateOutcomeKind = "td-start"
	CreateOutcomeAgent    CreateOutcomeKind = "agent-metadata"
	CreateOutcomeEnv      CreateOutcomeKind = "env-copy"
	CreateOutcomeHook     CreateOutcomeKind = "setup-hook"
)

// CreateSetupOutcome records one independently recoverable setup action.
type CreateSetupOutcome struct {
	Kind     CreateOutcomeKind
	Action   string
	Err      error
	Required bool
}

type CreateSetupResult struct {
	Worktree *Worktree
	Outcomes []CreateSetupOutcome
}

func (r *CreateSetupResult) HasRequiredFailure() bool {
	if r == nil {
		return false
	}
	for _, outcome := range r.Outcomes {
		if outcome.Required && outcome.Err != nil {
			return true
		}
	}
	return false
}

func (r *CreateSetupResult) Warnings() []CreateSetupOutcome {
	if r == nil {
		return nil
	}
	warnings := make([]CreateSetupOutcome, 0)
	for _, outcome := range r.Outcomes {
		if outcome.Err != nil {
			warnings = append(warnings, outcome)
		}
	}
	return warnings
}

func resolveCreateOperation(ctx context.Context, workDir, projectRoot, name, base string, dirPrefix bool, setup config.WorktreeSetupConfig) (*CreateOperationPlan, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("workspace name is required")
	}
	if _, err := gitOutputContext(ctx, workDir, "check-ref-format", "--branch", name); err != nil {
		return nil, fmt.Errorf("invalid branch name %q: %w", name, err)
	}
	if _, err := gitOutputContext(ctx, workDir, "show-ref", "--verify", "--quiet", "refs/heads/"+name); err == nil {
		return nil, fmt.Errorf("branch %q already exists", name)
	}

	sourceWorktree, err := gitOutputContext(ctx, workDir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("resolve source worktree: %w", err)
	}
	mainWorktree := projectRoot
	if mainWorktree == "" {
		mainWorktree = mainWorktreePathContext(ctx, workDir)
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
	sourceOID, err := gitOutputContext(ctx, workDir, "rev-parse", "--verify", requestedBase+"^{commit}")
	if err != nil {
		return nil, fmt.Errorf("source %q is not a commit: %w", requestedBase, err)
	}
	sourceRef, err := gitOutputContext(ctx, workDir, "rev-parse", "--symbolic-full-name", requestedBase)
	if err != nil || sourceRef == "" {
		sourceRef = requestedBase
	}

	displayName := name
	if dirPrefix {
		if repo := repoNameContext(ctx, workDir); repo != "" {
			displayName = repo + "-" + name
		}
	}
	destination := filepath.Join(filepath.Dir(mainWorktree), displayName)
	if err := ensureRealDirectoryPath(filepath.Dir(mainWorktree), filepath.Dir(destination), false); err != nil {
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
			if !safeSetupRelativePath(rel) {
				return nil, fmt.Errorf("env file path must stay within the main worktree: %q", rel)
			}
			if _, err := containedRegularFile(mainWorktree, rel); err == nil {
				envFiles = append(envFiles, rel)
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("env file %q is unsafe: %w", rel, err)
			}
		}
	}
	hookPath := setup.HookPath
	if hookPath == "" {
		hookPath = setupScriptName
	}
	if !safeSetupRelativePath(hookPath) {
		return nil, fmt.Errorf("setup hook path must stay within the main worktree: %q", hookPath)
	}
	runHook := setup.RunHook
	if runHook {
		if _, err := containedRegularFile(mainWorktree, hookPath); errors.Is(err, os.ErrNotExist) {
			runHook = false
		} else if err != nil {
			return nil, fmt.Errorf("setup hook %q is unsafe: %w", hookPath, err)
		}
	}

	return &CreateOperationPlan{
		SourceWorktree: sourceWorktree, MainWorktree: mainWorktree,
		SourceRef: sourceRef, SourceOID: sourceOID, Branch: name,
		Path: destination, DisplayName: displayName,
		RemotePolicy: "local branch only; no remote push",
		CopyEnv:      len(envFiles) > 0, EnvFiles: envFiles,
		RunHook: runHook, HookPath: hookPath, HookRequired: setup.HookRequired,
	}, nil
}

func safeSetupRelativePath(path string) bool {
	clean := filepath.Clean(path)
	return path != "" && clean != "." && clean != ".." && !filepath.IsAbs(path) && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

func containedRegularFile(root, rel string) (string, error) {
	file, err := openContainedRegularFile(root, rel)
	if err != nil {
		return "", err
	}
	defer file.Close()
	return file.Name(), nil
}

func openContainedRegularFile(root, rel string) (*os.File, error) {
	return openContainedRegularFileWithHook(root, rel, nil)
}

func openContainedRegularFileWithHook(root, rel string, beforeWalk func()) (*os.File, error) {
	if !safeSetupRelativePath(rel) {
		return nil, fmt.Errorf("path must remain relative")
	}
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	rootDir, err := openPinnedDirectory(rootReal, ".", false)
	if err != nil {
		return nil, err
	}
	if beforeWalk != nil {
		beforeWalk()
	}
	dir, err := walkPinnedDirectory(rootDir, filepath.Dir(filepath.Clean(rel)), false)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	leaf := filepath.Base(filepath.Clean(rel))
	fd, err := unix.Openat(int(dir.Fd()), leaf, unix.O_RDONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	target := filepath.Join(rootReal, filepath.Clean(rel))
	file := os.NewFile(uintptr(fd), target)
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, fmt.Errorf("artifact is not a regular file: %s", target)
	}
	return file, nil
}

func openPinnedDirectory(root, rel string, create bool) (*os.File, error) {
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	fd, err := unix.Open(rootReal, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	current := os.NewFile(uintptr(fd), rootReal)
	return walkPinnedDirectory(current, rel, create)
}

func walkPinnedDirectory(current *os.File, rel string, create bool) (*os.File, error) {
	clean := filepath.Clean(rel)
	if clean == "." || clean == "" {
		return current, nil
	}
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		current.Close()
		return nil, fmt.Errorf("directory path escapes pinned root")
	}
	for _, component := range strings.Split(clean, string(filepath.Separator)) {
		if component == "" || component == "." {
			continue
		}
		if create {
			if err := unix.Mkdirat(int(current.Fd()), component, 0755); err != nil && !errors.Is(err, unix.EEXIST) {
				current.Close()
				return nil, err
			}
		}
		nextFD, err := unix.Openat(int(current.Fd()), component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
		if err != nil {
			current.Close()
			return nil, err
		}
		next := os.NewFile(uintptr(nextFD), filepath.Join(current.Name(), component))
		_ = current.Close()
		current = next
	}
	return current, nil
}

func copyOpenFile(source *os.File, dst string) error {
	info, err := source.Stat()
	if err != nil {
		return err
	}
	dest, err := os.OpenFile(dst, os.O_RDWR|os.O_CREATE|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(dest, source)
	if syncErr := dest.Sync(); copyErr == nil {
		copyErr = syncErr
	}
	if closeErr := dest.Close(); copyErr == nil {
		copyErr = closeErr
	}
	return copyErr
}

// ensureRealDirectoryPath rejects symlink traversal for every existing path
// component below root. Missing components are allowed only when requested;
// callers re-run this after creation to narrow the remaining TOCTOU window.
func ensureRealDirectoryPath(root, target string, requireExisting bool) error {
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(rootReal, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path escapes allowed root")
	}
	current := rootReal
	for _, component := range strings.Split(rel, string(filepath.Separator)) {
		if component == "" || component == "." {
			continue
		}
		current = filepath.Join(current, component)
		info, statErr := os.Lstat(current)
		if errors.Is(statErr, os.ErrNotExist) {
			if requireExisting {
				return statErr
			}
			continue
		}
		if statErr != nil {
			return statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink path component is not allowed: %s", current)
		}
		if !info.IsDir() {
			return fmt.Errorf("path component is not a directory: %s", current)
		}
	}
	return nil
}

func addCreatedWorktree(ctx context.Context, repoKey string, plan *CreateOperationPlan) (*Worktree, error) {
	return addCreatedWorktreeWithRunner(ctx, repoKey, plan, func(cmd *exec.Cmd) ([]byte, error) { return cmd.CombinedOutput() })
}

func addCreatedWorktreeWithRunner(ctx context.Context, repoKey string, plan *CreateOperationPlan, run func(*exec.Cmd) ([]byte, error)) (*Worktree, error) {
	if _, err := gitOutputContext(ctx, plan.SourceWorktree, "check-ref-format", "--branch", plan.Branch); err != nil {
		return nil, fmt.Errorf("branch is no longer valid: %w", err)
	}
	if current, err := gitOutputContext(ctx, plan.SourceWorktree, "rev-parse", "--verify", plan.SourceRef+"^{commit}"); err != nil || current != plan.SourceOID {
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
	parent, err := openPinnedDirectory(allowedRoot, parentRel, true)
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
	rootDir, err := openPinnedDirectory(allowedRoot, ".", false)
	if err != nil {
		return nil, fmt.Errorf("pin destination root: %w", err)
	}
	defer rootDir.Close()
	stagingName, err := mkdirPinnedTemp(rootDir)
	if err != nil {
		return nil, fmt.Errorf("create pinned staging directory: %w", err)
	}
	rootPath, err := pinnedDirectoryPath(rootDir)
	if err != nil {
		_ = unix.Unlinkat(int(rootDir.Fd()), stagingName, unix.AT_REMOVEDIR)
		return nil, err
	}
	stagingPath := filepath.Join(rootPath, stagingName)
	cmd := exec.CommandContext(ctx, "git", "worktree", "add", "-b", plan.Branch, stagingPath, plan.SourceOID)
	cmd.Dir = plan.SourceWorktree
	output, addRunErr := run(cmd)
	head, stagingErr := gitOutputContext(context.Background(), stagingPath, "rev-parse", "HEAD")
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
		actualParent, pathErr := pinnedDirectoryPath(parent)
		if pathErr != nil {
			return createdWorktree(repoKey, plan, head), pathErr
		}
		actualPath = filepath.Join(actualParent, leaf)
		if _, repairErr := gitOutputContext(context.Background(), plan.SourceWorktree, "worktree", "repair", actualPath); repairErr != nil {
			movedPlan := *plan
			movedPlan.Path = actualPath
			return createdWorktree(repoKey, &movedPlan, head), fmt.Errorf("repair moved worktree metadata: %w", repairErr)
		}
	}
	confirmedPath := filepath.Clean(plan.Path)
	plan.Path = filepath.Clean(actualPath)
	wt := createdWorktree(repoKey, plan, head)
	if addRunErr != nil {
		return wt, fmt.Errorf("git worktree add: %s: %w", strings.TrimSpace(string(output)), addRunErr)
	}
	if moveErr != nil {
		return wt, fmt.Errorf("move staged worktree into pinned destination: %w", moveErr)
	}
	if plan.Path != confirmedPath {
		return wt, fmt.Errorf("destination parent identity changed during creation; worktree was retained at %s", plan.Path)
	}
	return wt, nil
}

func mkdirPinnedTemp(root *os.File) (string, error) {
	for attempt := 0; attempt < 100; attempt++ {
		name := fmt.Sprintf(".sidecar-worktree-%d-%d", os.Getpid(), time.Now().UnixNano()+int64(attempt))
		if err := unix.Mkdirat(int(root.Fd()), name, 0700); err == nil {
			return name, nil
		} else if !errors.Is(err, unix.EEXIST) {
			return "", err
		}
	}
	return "", fmt.Errorf("could not allocate a staging directory")
}

func createdWorktree(repoKey string, plan *CreateOperationPlan, head string) *Worktree {
	wt := &Worktree{Key: stablePathKey(plan.Path), RepoKey: repoKey, Name: plan.DisplayName, Path: plan.Path,
		Branch: plan.Branch, BaseBranch: strings.TrimPrefix(plan.SourceRef, "refs/heads/"), TaskID: plan.TaskID,
		TaskTitle: plan.TaskTitle, ChosenAgentType: plan.AgentType, Status: StatusPaused, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if key, keyErr := projectdir.WorktreeKey(plan.Path); keyErr == nil {
		wt.Key = key
	}
	// Store the exact created identity for recovery even if subsequent metadata fails.
	wt.HEADOID = head
	return wt
}

func runCreateSetup(ctx context.Context, plan *CreateOperationPlan, wt *Worktree) *CreateSetupResult {
	result := &CreateSetupResult{Worktree: wt}
	add := func(kind CreateOutcomeKind, action string, required bool, err error) {
		result.Outcomes = append(result.Outcomes, CreateSetupOutcome{Kind: kind, Action: action, Required: required, Err: err})
	}
	base := strings.TrimPrefix(plan.SourceRef, "refs/heads/")
	add(CreateOutcomeIdentity, "base metadata", true, saveBaseBranchContext(ctx, plan.MainWorktree, plan.Path, base))
	add(CreateOutcomeAgent, "agent metadata", true, saveAgentTypeContext(ctx, plan.MainWorktree, plan.Path, plan.AgentType))
	add(CreateOutcomeTDRoot, ".td-root", false, setupTDRootContext(ctx, plan.SourceWorktree, plan.MainWorktree, plan.Path))

	if plan.TaskID != "" {
		var linkErr error
		if wtDir, err := projectdir.WorktreeDirContext(ctx, plan.MainWorktree, plan.Path); err != nil {
			linkErr = err
		} else {
			linkErr = writeDurableFile(filepath.Join(wtDir, sidecarTaskFile), []byte(plan.TaskID+"\n"), 0644)
		}
		add(CreateOutcomeTaskLink, "task link "+plan.TaskID, true, linkErr)
		if linkErr == nil {
			cmd := exec.CommandContext(ctx, "td", "start", plan.TaskID)
			cmd.Dir = plan.Path
			output, err := cmd.CombinedOutput()
			if err != nil {
				err = fmt.Errorf("td start %s: %s: %w", plan.TaskID, strings.TrimSpace(string(output)), err)
			}
			add(CreateOutcomeTDStart, "td start "+plan.TaskID, false, err)
		}
	}

	if plan.CopyEnv {
		for _, rel := range plan.EnvFiles {
			source, err := openContainedRegularFile(plan.MainWorktree, rel)
			if err == nil {
				err = copyOpenFile(source, filepath.Join(plan.Path, rel))
				_ = source.Close()
			}
			add(CreateOutcomeEnv, "copy "+rel, false, err)
		}
	}
	if plan.RunHook {
		err := runSetupHookContext(ctx, plan)
		add(CreateOutcomeHook, "run "+plan.HookPath, plan.HookRequired, err)
	}
	return result
}

func writeDurableFile(path string, data []byte, mode os.FileMode) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".sidecar-write-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err = tmp.Chmod(mode); err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(tmpName, path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func runSetupHookContext(ctx context.Context, plan *CreateOperationPlan) error {
	return runSetupHookContextWithHook(ctx, plan, nil)
}

func runSetupHookContextWithHook(ctx context.Context, plan *CreateOperationPlan, beforeOpen func()) error {
	hook, err := openContainedRegularFileWithHook(plan.MainWorktree, plan.HookPath, beforeOpen)
	if err != nil {
		return fmt.Errorf("validate setup hook: %w", err)
	}
	defer hook.Close()
	cmd := exec.CommandContext(ctx, "bash", "/dev/fd/3")
	cmd.ExtraFiles = []*os.File{hook}
	cmd.Dir = plan.Path
	isolated := ApplyEnvOverrides(os.Environ(), BuildEnvOverrides(plan.MainWorktree))
	cmd.Env = append(isolated,
		"MAIN_WORKTREE="+plan.MainWorktree,
		"SOURCE_WORKTREE="+plan.SourceWorktree,
		"WORKTREE_PATH="+plan.Path,
		"WORKTREE_BRANCH="+plan.Branch,
	)
	// Hook output may contain secrets. It is deliberately neither logged nor
	// included in the UI error; only the process status crosses this seam.
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("setup hook exited unsuccessfully: %w", err)
	}
	return nil
}
