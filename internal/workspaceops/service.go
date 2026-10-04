package workspaceops

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/shellstate"
)

// Service is the application boundary for workspace mutations. It holds no
// operation, viewer, or selection state. A confirmed plan and its partial
// result belong to the caller, which can resume the same phases after a failure.
// The zero value uses the local Git, shellstate, setup, and tmux adapters.
type Service struct {
	Execute  func(context.Context, string, *WorktreePlan) (*WorktreeRecord, error)
	Journal  func(context.Context, *WorktreePlan, *WorktreeRecord) error
	Identity func(context.Context, *WorktreePlan) []SetupOutcome
	Setup    func(context.Context, *WorktreePlan) []SetupOutcome
	Finalize func(*WorktreePlan) error
	Launch   func(context.Context, AgentLaunchSpec) (AgentLaunchResult, error)
	Shells   ShellRecords
}

// ShellRecords allows an existing projection to retain its recovery policy
// while the service owns the creation and deletion sequence. Both the default
// adapter and the workspace compatibility handle persist through shellstate.
type ShellRecords interface {
	AddShell(shellstate.Definition) error
	RemoveShell(string) error
}

type CreationResult struct {
	Record   *WorktreeRecord
	Outcomes []SetupOutcome
	Err      error
}

func (Service) PlanWorktree(ctx context.Context, workDir, projectRoot, name, base string, dirPrefix bool, setup config.WorktreeSetupConfig) (*WorktreePlan, error) {
	return ResolveWorktreePlan(ctx, workDir, projectRoot, name, base, dirPrefix, setup)
}

// BeginWorktree creates the confirmed identity and journals even a partial
// success. Cancellation after Git creates it must not lose recovery evidence.
func (s Service) BeginWorktree(ctx context.Context, plan *WorktreePlan) CreationResult {
	if plan == nil {
		return CreationResult{Err: fmt.Errorf("missing worktree plan")}
	}
	execute := s.Execute
	if execute == nil {
		execute = ExecuteWorktree
	}
	record, err := execute(ctx, plan.RepoKey, plan)
	result := CreationResult{Record: record, Err: err, Outcomes: make([]SetupOutcome, 0)}
	if record != nil {
		journal := s.Journal
		if journal == nil {
			journal = PersistPendingCreation
		}
		if journalErr := journal(context.Background(), plan, record); journalErr != nil {
			result.Outcomes = append(result.Outcomes, SetupOutcome{Kind: "journal", Action: "persist recovery", Required: true, Err: journalErr})
		}
	}
	return result
}

// SetupWorktree is also the retry path. Task start is an explicit option: the
// project create form links and starts a task, whereas other callers only link.
func (s Service) SetupWorktree(ctx context.Context, plan *WorktreePlan, startTask bool) []SetupOutcome {
	identity, setup := s.Identity, s.Setup
	if identity == nil {
		identity = PersistWorktreeIdentity
	}
	if setup == nil {
		setup = RunConfiguredSetup
	}
	outcomes := identity(ctx, plan)
	if startTask && plan != nil && plan.TaskID != "" {
		linked := true
		for _, o := range outcomes {
			if o.Kind == "task-link" && o.Err != nil {
				linked = false
			}
		}
		if linked {
			cmd := exec.CommandContext(ctx, "td", "start", plan.TaskID)
			cmd.Dir = plan.Path
			output, err := cmd.CombinedOutput()
			if err != nil {
				err = fmt.Errorf("td start %s: %s: %w", plan.TaskID, strings.TrimSpace(string(output)), err)
			}
			outcomes = append(outcomes, SetupOutcome{Kind: "td-start", Action: "td start " + plan.TaskID, Err: err})
		}
	}
	return append(outcomes, setup(ctx, plan)...)
}

func (s Service) FinalizeWorktree(plan *WorktreePlan) error {
	finalize := s.Finalize
	if finalize == nil {
		finalize = RemovePendingCreation
	}
	return finalize(plan)
}

// CreateWorktree runs the non-interactive sequence. Interactive clients use
// the same phases to display progress and offer recovery before finalization.
func (s Service) CreateWorktree(ctx context.Context, plan *WorktreePlan, startTask bool) CreationResult {
	result := s.BeginWorktree(ctx, plan)
	if result.Record == nil || result.Err != nil {
		return result
	}
	result.Outcomes = append(result.Outcomes, s.SetupWorktree(ctx, plan, startTask)...)
	if len(FailedSetupOutcomes(result.Outcomes, true)) == 0 {
		if err := s.FinalizeWorktree(plan); err != nil {
			result.Outcomes = append(result.Outcomes, SetupOutcome{Kind: "journal", Action: "finalize pending creation", Required: true, Err: err})
		}
	}
	return result
}

func FailedSetupOutcomes(outcomes []SetupOutcome, requiredOnly bool) []SetupOutcome {
	var failed []SetupOutcome
	for _, o := range outcomes {
		if o.Err != nil && (!requiredOnly || o.Required) {
			failed = append(failed, o)
		}
	}
	return failed
}

func (s Service) LaunchWorktree(ctx context.Context, spec AgentLaunchSpec) (AgentLaunchResult, error) {
	launch := s.Launch
	if launch == nil {
		launch = LaunchWorktreeSession
	}
	return launch(ctx, spec)
}

func (s Service) CreateShell(spec ManagedShellSpec) (ShellResult, error) {
	if spec.Allocate {
		return s.createAllocatedShell(spec)
	}
	if s.Shells != nil {
		return createManagedShell(spec, s.Shells.AddShell)
	}
	return createManagedShell(spec, nil)
}
func (s Service) DeleteShell(projectRoot, session, namespace string) error {
	if s.Shells != nil {
		return deleteManagedShell(projectRoot, session, namespace, s.Shells.RemoveShell)
	}
	return deleteManagedShell(projectRoot, session, namespace, nil)
}
func (Service) RestoreShell(projectRoot, session, namespace string) (shellstate.Definition, error) {
	return RestoreManagedShell(projectRoot, session, namespace)
}
func (Service) RenameShell(path string, request shellstate.RenameRequest) (shellstate.RenameResult, error) {
	return shellstate.RenameAtPath(path, request)
}
func (Service) RenameCurrentShell(stateDir string, request shellstate.RenameRequest) (shellstate.RenameResult, error) {
	return shellstate.RenameCurrent(stateDir, request)
}
func (Service) RenameWorktree(ctx context.Context, stateDir, projectRoot, path, name string) (WorktreeDisplayNameResult, error) {
	return RenameWorktreeDisplayName(ctx, stateDir, projectRoot, path, name)
}
func (Service) DeleteWorktree(ctx context.Context, request WorktreeRemoval) error {
	return DeleteWorktree(ctx, request)
}
func (Service) DeleteCreatedWorktree(ctx context.Context, plan *WorktreePlan, record *WorktreeRecord) error {
	return DeleteCreatedWorktree(ctx, plan, record)
}

// CreationError retains the execution error and required setup failures for
// callers whose surface has only one error channel.
func CreationError(result CreationResult) error {
	err := result.Err
	for _, o := range FailedSetupOutcomes(result.Outcomes, true) {
		err = errors.Join(err, fmt.Errorf("%s: %w", o.Action, o.Err))
	}
	return err
}
