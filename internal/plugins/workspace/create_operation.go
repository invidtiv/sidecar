package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/workspaceops"
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
	return workspaceops.PendingCreationPath(ctx, sharedCreatePlan(plan))
}

func persistPendingCreation(ctx context.Context, plan *CreateOperationPlan, wt *Worktree) error {
	return workspaceops.PersistPendingCreation(ctx, sharedCreatePlan(plan), sharedWorktreeRecord(wt))
}

func removePendingCreation(plan *CreateOperationPlan) error {
	return removePendingCreationWithOps(plan, os.Remove, func(dir string) error {
		file, err := os.Open(dir)
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()
		return file.Sync()
	})
}

func removePendingCreationWithOps(plan *CreateOperationPlan, remove func(string) error, syncDir func(string) error) error {
	return workspaceops.RemovePendingCreationWithOps(sharedCreatePlan(plan), remove, syncDir)
}

func (p *Plugin) clearPendingCreation(plan *CreateOperationPlan) error {
	if p.removePendingCreationFn != nil {
		return p.removePendingCreationFn(plan)
	}
	return removePendingCreation(plan)
}

func loadPendingCreation(ctx context.Context, projectRoot string, worktrees []*Worktree, repoKey string) (*pendingCreationJournal, error) {
	candidates := make([]workspaceops.WorktreeRecord, 0, len(worktrees))
	for _, wt := range worktrees {
		if record := sharedWorktreeRecord(wt); record != nil {
			candidates = append(candidates, *record)
		}
	}
	shared, err := workspaceops.LoadPendingCreation(ctx, projectRoot, candidates, repoKey)
	if err != nil || shared == nil {
		return nil, err
	}
	plan := createPlanFromShared(&shared.Plan)
	wt := worktreeFromShared(&shared.Worktree, plan)
	return &pendingCreationJournal{Version: shared.Version, RepoKey: shared.RepoKey, OperationID: shared.OperationID, Plan: *plan, Worktree: *wt}, nil
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
	p.selectWorktreeAt(idx)
	p.resetPreviewScroll()
	p.saveSelectionState()
	p.ensureVisible()
}

func (p *Plugin) finishCreatedWorktree(plan *CreateOperationPlan, wt *Worktree) []tea.Cmd {
	p.persistCreateLastAgent()
	if p.createForm == nil && plan != nil && plan.AgentType != "" {
		_ = state.SetLastCreateAgent(string(plan.AgentType))
	}
	p.selectCreatedWorktree(wt)
	p.viewMode = ViewModeList
	p.clearCreateModal()
	cmds := []tea.Cmd{p.loadSelectedContent()}
	if plan.AgentType != AgentNone && plan.AgentType != "" {
		cmds = append(cmds, p.StartAgentWithOptions(wt, plan.AgentType, plan.SkipPerms))
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
	shared, err := workspaceops.ResolveWorktreePlan(ctx, workDir, projectRoot, name, base, dirPrefix, setup)
	if err != nil {
		return nil, err
	}
	return createPlanFromShared(shared), nil
}

func createPlanFromShared(plan *workspaceops.WorktreePlan) *CreateOperationPlan {
	if plan == nil {
		return nil
	}
	return &CreateOperationPlan{
		RepoKey: plan.RepoKey, OperationID: plan.OperationID,
		SourceWorktree: plan.SourceWorktree, MainWorktree: plan.MainWorktree,
		SourceRef: plan.SourceRef, SourceOID: plan.SourceOID, Branch: plan.Branch,
		Path: plan.Path, DisplayName: plan.DisplayName, RemotePolicy: plan.RemotePolicy,
		TaskID: plan.TaskID, TaskTitle: plan.TaskTitle, AgentType: AgentType(plan.AgentType), SkipPerms: plan.SkipPerms,
		CopyEnv: plan.CopyEnv, EnvFiles: append([]string(nil), plan.EnvFiles...),
		RunHook: plan.RunHook, HookPath: plan.HookPath, HookRequired: plan.HookRequired,
	}
}

func sharedCreatePlan(plan *CreateOperationPlan) *workspaceops.WorktreePlan {
	if plan == nil {
		return nil
	}
	return &workspaceops.WorktreePlan{
		RepoKey: plan.RepoKey, OperationID: plan.OperationID,
		SourceWorktree: plan.SourceWorktree, MainWorktree: plan.MainWorktree,
		SourceRef: plan.SourceRef, SourceOID: plan.SourceOID, Branch: plan.Branch,
		Path: plan.Path, DisplayName: plan.DisplayName, RemotePolicy: plan.RemotePolicy,
		TaskID: plan.TaskID, TaskTitle: plan.TaskTitle, AgentType: string(plan.AgentType), SkipPerms: plan.SkipPerms,
		CopyEnv: plan.CopyEnv, EnvFiles: append([]string(nil), plan.EnvFiles...),
		RunHook: plan.RunHook, HookPath: plan.HookPath, HookRequired: plan.HookRequired,
	}
}

func sharedWorktreeRecord(wt *Worktree) *workspaceops.WorktreeRecord {
	if wt == nil {
		return nil
	}
	return &workspaceops.WorktreeRecord{Key: wt.Key, RepoKey: wt.RepoKey, Name: wt.Name, Path: wt.Path,
		Branch: wt.Branch, BaseBranch: wt.BaseBranch, HEADOID: wt.HEADOID, CreatedAt: wt.CreatedAt, UpdatedAt: wt.UpdatedAt}
}

func worktreeFromShared(record *workspaceops.WorktreeRecord, plan *CreateOperationPlan) *Worktree {
	if record == nil {
		return nil
	}
	wt := &Worktree{Key: record.Key, RepoKey: record.RepoKey, Name: record.Name, Path: record.Path,
		Branch: record.Branch, BaseBranch: record.BaseBranch, HEADOID: record.HEADOID,
		Status: StatusPaused, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}
	if plan != nil {
		wt.TaskID, wt.TaskTitle, wt.ChosenAgentType = plan.TaskID, plan.TaskTitle, plan.AgentType
	}
	return wt
}

// The path-containment and durable-write layer now lives in
// internal/workspaceops, so the global browser can reach the same
// implementation without importing this plugin. These are the plugin's names
// for it; there is one implementation, not two.
var (
	openContainedRegularFile         = workspaceops.OpenContainedRegularFile
	openContainedRegularFileWithHook = workspaceops.OpenContainedRegularFileWithHook
	writeDurableFile                 = workspaceops.WriteDurableFile
)

// containmentPathError is the plugin's name for the shared refusal type, kept
// so existing errors.As call sites read unchanged.
type containmentPathError = workspaceops.PathError

func addCreatedWorktree(ctx context.Context, repoKey string, plan *CreateOperationPlan) (*Worktree, error) {
	return addCreatedWorktreeWithRunner(ctx, repoKey, plan, func(cmd *exec.Cmd) ([]byte, error) { return cmd.CombinedOutput() })
}

func addCreatedWorktreeWithRunner(ctx context.Context, repoKey string, plan *CreateOperationPlan, run func(*exec.Cmd) ([]byte, error)) (*Worktree, error) {
	shared := sharedCreatePlan(plan)
	record, err := workspaceops.ExecuteWorktreeWithRunner(ctx, repoKey, shared, run)
	if shared != nil {
		plan.Path = shared.Path
	}
	if record == nil {
		return nil, err
	}
	wt := &Worktree{Key: record.Key, RepoKey: record.RepoKey, Name: record.Name, Path: record.Path,
		Branch: record.Branch, BaseBranch: record.BaseBranch, HEADOID: record.HEADOID,
		TaskID: plan.TaskID, TaskTitle: plan.TaskTitle, ChosenAgentType: plan.AgentType,
		Status: StatusPaused, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}
	return wt, err
}

func runCreateSetup(ctx context.Context, plan *CreateOperationPlan, wt *Worktree) *CreateSetupResult {
	result := &CreateSetupResult{Worktree: wt}
	add := func(kind CreateOutcomeKind, action string, required bool, err error) {
		result.Outcomes = append(result.Outcomes, CreateSetupOutcome{Kind: kind, Action: action, Required: required, Err: err})
	}
	base := strings.TrimPrefix(plan.SourceRef, "refs/heads/")
	add(CreateOutcomeIdentity, "base metadata", true, saveBaseBranchContext(ctx, plan.MainWorktree, plan.Path, base))
	add(CreateOutcomeIdentity, "display name", true, saveDisplayNameContext(ctx, plan.MainWorktree, plan.Path, plan.DisplayName))
	add(CreateOutcomeAgent, "agent metadata", true, saveAgentTypeContext(ctx, plan.MainWorktree, plan.Path, plan.AgentType))

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

	for _, outcome := range workspaceops.RunConfiguredSetup(ctx, sharedCreatePlan(plan)) {
		add(CreateOutcomeKind(outcome.Kind), outcome.Action, outcome.Required, outcome.Err)
	}
	return result
}

func runSetupHookContextWithHook(ctx context.Context, plan *CreateOperationPlan, beforeOpen func()) error {
	return workspaceops.RunSetupHookWithHook(ctx, sharedCreatePlan(plan), beforeOpen)
}
