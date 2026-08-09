package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/projectdir"
)

// CreateOperationPlan is the immutable contract shown to the user before any
// repository mutation. It is also retained for retry and identity-checked undo.
type CreateOperationPlan struct {
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
			if info, err := os.Stat(filepath.Join(mainWorktree, rel)); err == nil && info.Mode().IsRegular() {
				envFiles = append(envFiles, rel)
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
	if info, err := os.Stat(filepath.Join(mainWorktree, hookPath)); err != nil || !info.Mode().IsRegular() {
		runHook = false
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

func addCreatedWorktree(ctx context.Context, repoKey string, plan *CreateOperationPlan) (*Worktree, error) {
	if _, err := gitOutputContext(ctx, plan.SourceWorktree, "check-ref-format", "--branch", plan.Branch); err != nil {
		return nil, fmt.Errorf("branch is no longer valid: %w", err)
	}
	if current, err := gitOutputContext(ctx, plan.SourceWorktree, "rev-parse", "--verify", plan.SourceRef+"^{commit}"); err != nil || current != plan.SourceOID {
		return nil, fmt.Errorf("source changed since confirmation (expected %s, got %s)", shortOID(plan.SourceOID), shortOID(current))
	}
	if _, err := os.Lstat(plan.Path); err == nil {
		return nil, fmt.Errorf("destination path now exists: %s", plan.Path)
	}
	if err := os.MkdirAll(filepath.Dir(plan.Path), 0755); err != nil {
		return nil, fmt.Errorf("create destination parent: %w", err)
	}
	cmd := exec.CommandContext(ctx, "git", "worktree", "add", "-b", plan.Branch, plan.Path, plan.SourceOID)
	cmd.Dir = plan.SourceWorktree
	if output, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("git worktree add: %s: %w", strings.TrimSpace(string(output)), err)
	}
	head, err := gitOutputContext(ctx, plan.Path, "rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("verify created worktree: %w", err)
	}
	wt := &Worktree{Key: stablePathKey(plan.Path), RepoKey: repoKey, Name: plan.DisplayName, Path: plan.Path,
		Branch: plan.Branch, BaseBranch: strings.TrimPrefix(plan.SourceRef, "refs/heads/"), TaskID: plan.TaskID,
		TaskTitle: plan.TaskTitle, ChosenAgentType: plan.AgentType, Status: StatusPaused, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if key, keyErr := projectdir.WorktreeKey(plan.Path); keyErr == nil {
		wt.Key = key
	}
	// Store the exact created identity for recovery even if subsequent metadata fails.
	wt.HEADOID = head
	return wt, nil
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
			err := copyFile(filepath.Join(plan.MainWorktree, rel), filepath.Join(plan.Path, rel))
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
	cmd := exec.CommandContext(ctx, "bash", filepath.Join(plan.MainWorktree, plan.HookPath))
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
