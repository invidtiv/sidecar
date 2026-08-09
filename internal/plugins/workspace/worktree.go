package workspace

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/app"
	"github.com/marcus/sidecar/internal/palette"
	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/tdroot"
)

// WorkDirDeletedMsg signals that the current working directory was deleted.
// This happens when sidecar is running inside a worktree that gets deleted.
type WorkDirDeletedMsg struct {
	MainWorktreePath string
}

// refreshWorktrees returns a command to refresh the worktree list.
func (p *Plugin) refreshWorktrees() tea.Cmd {
	workDir := p.ctx.WorkDir
	ctx, scope := p.newOperationScope(nil)
	p.refreshOperationID = scope.OperationID
	return func() tea.Msg {
		// Check if current WorkDir still exists (may have been a deleted worktree)
		if _, err := os.Stat(workDir); os.IsNotExist(err) {
			// WorkDir was deleted - find main worktree to switch to
			// We need to find the main worktree from a parent directory
			mainPath := findMainWorktreeFromDeleted(workDir)
			if mainPath != "" {
				return WorkDirDeletedMsg{MainWorktreePath: mainPath}
			}
		}

		snapshot, err := BuildRepoSnapshot(ctx, workDir)
		return RefreshDoneMsg{OperationScope: scope, Worktrees: snapshotToWorktrees(snapshot), Snapshot: snapshot, Err: err}
	}
}

// findMainWorktreeFromDeleted finds the main worktree path when the current
// directory has been deleted. It searches parent directories for a git repo
// that owned the deleted worktree by checking .git/worktrees/*/gitdir files.
func findMainWorktreeFromDeleted(deletedPath string) string {
	// Try parent directory first - worktrees are typically siblings of main repo
	parentDir := filepath.Dir(deletedPath)
	if parentDir == deletedPath {
		return "" // reached root
	}

	// Look for directories in parent that are git repos
	entries, err := os.ReadDir(parentDir)
	if err != nil {
		return ""
	}

	// Normalize the deleted path for comparison
	normalizedDeleted := filepath.Clean(deletedPath)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidatePath := filepath.Join(parentDir, entry.Name())
		// Skip the deleted directory
		if candidatePath == deletedPath {
			continue
		}

		// Check if this repo's .git/worktrees contains a reference to the deleted path
		// This is more reliable than just checking if it's any git repo
		gitWorktreesDir := filepath.Join(candidatePath, ".git", "worktrees")
		wtEntries, err := os.ReadDir(gitWorktreesDir)
		if err != nil {
			continue // Not a git repo or no worktrees
		}

		for _, wtEntry := range wtEntries {
			if !wtEntry.IsDir() {
				continue
			}
			gitdirPath := filepath.Join(gitWorktreesDir, wtEntry.Name(), "gitdir")
			content, err := os.ReadFile(gitdirPath)
			if err != nil {
				continue
			}
			// gitdir contains path like "/path/to/worktree/.git\n"
			wtPath := strings.TrimSuffix(strings.TrimSpace(string(content)), "/.git")
			if filepath.Clean(wtPath) == normalizedDeleted {
				// Found the repo that owned this worktree
				return app.GetMainWorktreePath(candidatePath)
			}
		}
	}

	return ""
}

// listWorktrees parses git worktree list --porcelain output.
func (p *Plugin) listWorktrees() ([]*Worktree, error) {
	snapshot, err := BuildRepoSnapshot(context.Background(), p.ctx.WorkDir)
	return snapshotToWorktrees(snapshot), err
}

// parseWorktreeList parses porcelain format output.
func parseWorktreeList(output, mainWorkdir string) ([]*Worktree, error) {
	var worktrees []*Worktree
	var current *Worktree
	var mainWorktree *Worktree // Track main worktree to prepend later

	// Parent directory of main workdir - worktrees are created as siblings
	parentDir := filepath.Dir(mainWorkdir)

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "worktree ") {
			if current != nil {
				if current.IsMain {
					mainWorktree = current
				} else {
					worktrees = append(worktrees, current)
				}
			}
			path := strings.TrimPrefix(line, "worktree ")
			// Mark main worktree (where git repo lives) with IsMain flag
			isMain := path == mainWorkdir
			// Derive name as relative path from parent dir, not just basename.
			// This handles nested worktree directories (e.g., repo-prefix/branch-name)
			// which are created when the branch name contains '/'.
			name := filepath.Base(path)
			if !isMain {
				if relPath, err := filepath.Rel(parentDir, path); err == nil && relPath != "" {
					name = relPath
				}
			}
			current = &Worktree{
				Name:      name,
				Path:      path,
				Status:    StatusPaused,
				CreatedAt: time.Now(), // Will be updated from file stat
				IsMain:    isMain,
			}
		} else if current != nil {
			if strings.HasPrefix(line, "HEAD ") {
				// HEAD commit hash - not storing currently
			} else if strings.HasPrefix(line, "branch ") {
				branch := strings.TrimPrefix(line, "branch refs/heads/")
				current.Branch = branch
			} else if line == "bare" {
				current.IsBare = true
			} else if line == "detached" {
				current.Branch = "(detached)"
				current.IsDetached = true
			} else if line == "locked" || strings.HasPrefix(line, "locked ") {
				current.IsLocked = true
			} else if strings.HasPrefix(line, "prunable ") {
				current.IsMissing = true
			}
		}
	}

	if current != nil {
		if current.IsMain {
			mainWorktree = current
		} else {
			worktrees = append(worktrees, current)
		}
	}

	// Prepend main worktree to the list so it appears first
	if mainWorktree != nil {
		worktrees = append([]*Worktree{mainWorktree}, worktrees...)
	}

	return worktrees, scanner.Err()
}

// createWorktree returns a command to create a new worktree.
func (p *Plugin) createWorktree() tea.Cmd {
	ctx, scope := p.newLifecycleScope(nil)
	name := p.createNameInput.Value()
	baseBranch := p.createBaseBranchInput.Value()
	taskID := p.createTaskID
	taskTitle := p.createTaskTitle
	agentType := p.createAgentType
	skipPerms := p.createSkipPermissions
	prompt := p.getSelectedPrompt()

	// Debug log to trace taskID flow
	if p.ctx != nil && p.ctx.Logger != nil {
		p.ctx.Logger.Debug("createWorktree: captured modal state", "name", name, "taskID", taskID, "taskTitle", taskTitle, "agentType", agentType, "skipPerms", skipPerms, "hasPrompt", prompt != nil)
	}

	if name == "" {
		return func() tea.Msg {
			return CreateDoneMsg{OperationScope: scope, Err: fmt.Errorf("workspace name is required")}
		}
	}
	ctxCopy := *p.ctx
	runner := *p
	runner.ctx = &ctxCopy

	return func() tea.Msg {
		wt, err := runner.doCreateWorktreeContext(ctx, name, baseBranch, taskID, taskTitle, agentType)
		return CreateDoneMsg{OperationScope: scope, Worktree: wt, AgentType: agentType, SkipPerms: skipPerms, Prompt: prompt, Err: err}
	}
}

// doCreateWorktree performs the actual worktree creation.
func (p *Plugin) doCreateWorktree(name, baseBranch, taskID, taskTitle string, agentType AgentType) (*Worktree, error) {
	return p.doCreateWorktreeContext(context.Background(), name, baseBranch, taskID, taskTitle, agentType)
}

func (p *Plugin) doCreateWorktreeContext(ctx context.Context, name, baseBranch, taskID, taskTitle string, agentType AgentType) (*Worktree, error) {
	workDir, projectRoot := p.ctx.WorkDir, p.ctx.ProjectRoot
	dirPrefix := p.ctx.Config != nil && p.ctx.Config.Plugins.Workspace.DirPrefix
	repoKey := ""
	if p.repoSnapshot != nil {
		repoKey = p.repoSnapshot.Key
	}
	// Default base branch to current branch if not specified
	if baseBranch == "" {
		baseBranch = "HEAD"
	}

	// Determine worktree directory name with optional repo prefix
	// When enabled, prefixes directory with repo name (e.g., "myrepo-feature-auth")
	// This helps conversation adapters discover related worktree conversations
	// by matching the directory path pattern after worktree deletion
	dirName := name
	if dirPrefix {
		repoName := repoNameContext(ctx, workDir)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if repoName != "" {
			dirName = repoName + "-" + name
		}
	}

	// Determine worktree path (sibling to main repo).
	// Use ProjectRoot (the main worktree path, resolved from git) rather than
	// WorkDir so that starting sidecar from a subfolder doesn't place the new
	// worktree inside the repository instead of beside it. Fixes #174.
	mainRepoDir := projectRoot
	if mainRepoDir == "" {
		mainRepoDir = workDir
	}
	parentDir := filepath.Dir(mainRepoDir)
	wtPath := filepath.Join(parentDir, dirName)

	// Ensure parent directory exists for paths with slashes (e.g., feat/ui)
	if err := os.MkdirAll(filepath.Dir(wtPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create parent directory: %w", err)
	}

	// Create worktree with new branch (branch name stays simple, just the user-provided name)
	args := []string{"worktree", "add", "-b", name, wtPath, baseBranch}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = workDir
	if output, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("git worktree add: %s: %w", strings.TrimSpace(string(output)), err)
	}

	partial := &Worktree{
		Key:             stablePathKey(wtPath),
		RepoKey:         repoKey,
		Name:            dirName,
		Path:            wtPath,
		Branch:          name,
		BaseBranch:      baseBranch,
		TaskID:          taskID,
		TaskTitle:       taskTitle,
		ChosenAgentType: agentType,
		Status:          StatusPaused,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	if key, err := projectdir.WorktreeKey(wtPath); err == nil {
		partial.Key = key
	}

	// Create .td-root file pointing to main repo for td database sharing.
	if err := setupTDRootContext(ctx, workDir, projectRoot, wtPath); err != nil {
		if ctx.Err() != nil {
			return partial, ctx.Err()
		}
		// Log but don't fail - td integration is optional
		p.ctx.Logger.Warn("failed to setup .td-root", "path", wtPath, "error", err)
	}

	// If task is linked, create task file in centralized dir and start the task
	if taskID != "" {
		wtDir, wtDirErr := projectdir.WorktreeDirContext(ctx, projectRoot, wtPath)
		if wtDirErr != nil {
			if ctx.Err() != nil {
				return partial, ctx.Err()
			}
			p.ctx.Logger.Warn("failed to resolve worktree dir for task", "path", wtPath, "error", wtDirErr)
		} else {
			taskPath := filepath.Join(wtDir, sidecarTaskFile)
			if err := ctx.Err(); err != nil {
				return partial, err
			}
			if err := os.WriteFile(taskPath, []byte(taskID+"\n"), 0644); err != nil {
				p.ctx.Logger.Warn("failed to write task file", "path", taskPath, "error", err)
			}
		}

		// Auto-start the task in td (if td is available)
		startCmd := exec.CommandContext(ctx, "td", "start", taskID)
		startCmd.Dir = wtPath
		if err := startCmd.Run(); err != nil {
			if ctx.Err() != nil {
				return partial, ctx.Err()
			}
			p.ctx.Logger.Warn("failed to start td task", "task", taskID, "error", err)
		}
	}

	// Determine actual base branch name
	actualBase := baseBranch
	if baseBranch == "HEAD" {
		if b, err := getCurrentBranchContext(ctx, workDir); err == nil {
			actualBase = b
		} else if ctx.Err() != nil {
			return partial, ctx.Err()
		}
	}

	wt := partial
	wt.BaseBranch = actualBase

	// Persist agent type to centralized worktree data directory
	if err := saveAgentTypeContext(ctx, projectRoot, wtPath, agentType); err != nil {
		if ctx.Err() != nil {
			return partial, ctx.Err()
		}
		p.ctx.Logger.Warn("failed to save agent type", "path", wtPath, "error", err)
	}

	// Persist base branch to centralized worktree data directory
	if err := saveBaseBranchContext(ctx, projectRoot, wtPath, actualBase); err != nil {
		if ctx.Err() != nil {
			return partial, ctx.Err()
		}
		p.ctx.Logger.Warn("failed to save base branch", "path", wtPath, "error", err)
	}

	// Run post-creation setup (env files, symlinks, setup script)
	if err := p.setupWorktreeContext(ctx, wtPath, name); err != nil {
		if ctx.Err() != nil {
			return partial, ctx.Err()
		}
		p.ctx.Logger.Warn("workspace setup had errors", "path", wtPath, "error", err)
		// Don't fail creation for setup errors
	}

	return wt, nil
}

// doDeleteWorktree removes a worktree. When isMissing is true, uses prune
// instead of remove since the directory no longer exists on disk.
func doDeleteWorktree(workDir, path string, isMissing bool) error {
	return doDeleteWorktreeContext(context.Background(), workDir, path, isMissing)
}

func doDeleteWorktreeContext(ctx context.Context, workDir, path string, isMissing bool) error {
	if isMissing {
		return doWorktreePruneContext(ctx, workDir)
	}

	// First try without force
	cmd := exec.CommandContext(ctx, "git", "worktree", "remove", path)
	cmd.Dir = workDir
	if err := cmd.Run(); err == nil {
		return nil
	}

	// If that fails, try with force
	cmd = exec.CommandContext(ctx, "git", "worktree", "remove", "--force", path)
	cmd.Dir = workDir
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree remove: %s: %w", strings.TrimSpace(string(output)), err)
	}

	return nil
}

// pushSelected returns a command to push the selected worktree's branch.
func (p *Plugin) pushSelected() tea.Cmd {
	wt := p.selectedWorktree()
	if wt == nil {
		return nil
	}
	ctx, scope := p.newLifecycleScope(wt)
	name := wt.Name
	path := wt.Path
	branch := wt.Branch

	return func() tea.Msg {
		err := doPushContext(ctx, path, branch, false, true)
		return PushDoneMsg{OperationScope: scope, WorkspaceName: name, Err: err}
	}
}

// doPush pushes a branch to remote.
func doPush(workdir, branch string, force, setUpstream bool) error {
	return doPushContext(context.Background(), workdir, branch, force, setUpstream)
}

func doPushContext(ctx context.Context, workdir, branch string, force, setUpstream bool) error {
	args := []string{"push"}
	if force {
		args = append(args, "--force-with-lease")
	}
	if setUpstream {
		args = append(args, "-u", "origin", branch)
	}

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = workdir
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git push: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

// getCurrentBranch returns the current branch name.
func getCurrentBranch(workdir string) (string, error) {
	return getCurrentBranchContext(context.Background(), workdir)
}

func getCurrentBranchContext(ctx context.Context, workdir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = workdir
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

// checkRemoteBranchExists checks if a remote branch exists for the given branch name.
func checkRemoteBranchExists(workdir, branch string) bool {
	return checkRemoteBranchExistsContext(context.Background(), workdir, branch)
}

func checkRemoteBranchExistsContext(ctx context.Context, workdir, branch string) bool {
	cmd := exec.CommandContext(ctx, "git", "ls-remote", "--heads", "origin", branch)
	cmd.Dir = workdir
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	return len(strings.TrimSpace(string(output))) > 0
}

// branchExists checks if a local branch exists using git rev-parse.
func branchExists(workdir, branch string) bool {
	cmd := exec.Command("git", "rev-parse", "--verify", "refs/heads/"+branch)
	cmd.Dir = workdir
	return cmd.Run() == nil
}

// doWorktreePrune runs git worktree prune to clean up stale worktree references.
func doWorktreePrune(workDir string) error {
	return doWorktreePruneContext(context.Background(), workDir)
}

func doWorktreePruneContext(ctx context.Context, workDir string) error {
	cmd := exec.CommandContext(ctx, "git", "worktree", "prune")
	cmd.Dir = workDir
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree prune: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

// isMainBranch returns true if the given branch is the repository's primary branch
// (e.g., main, master). This is used as a universal guard to prevent accidental
// deletion of the main branch.
func isMainBranch(workdir, branch string) bool {
	return branch == detectDefaultBranch(workdir)
}

func isMainBranchContext(ctx context.Context, workdir, branch string) bool {
	return branch == detectDefaultBranchContext(ctx, workdir)
}

// deleteBranch deletes a local branch, trying safe delete first then force.
func deleteBranch(workdir, branch string) error {
	return deleteBranchContext(context.Background(), workdir, branch)
}

func deleteBranchContext(ctx context.Context, workdir, branch string) error {
	if isMainBranchContext(ctx, workdir, branch) {
		return fmt.Errorf("refusing to delete main branch %q", branch)
	}
	// Try safe delete first
	cmd := exec.CommandContext(ctx, "git", "branch", "-d", branch)
	cmd.Dir = workdir
	if err := cmd.Run(); err == nil {
		return nil
	}

	// Try force delete
	cmd = exec.CommandContext(ctx, "git", "branch", "-D", branch)
	cmd.Dir = workdir
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("delete branch: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

// deleteRemoteBranchCmd deletes the remote branch from origin.
func deleteRemoteBranchCmd(workdir, branch string) error {
	return deleteRemoteBranchCmdContext(context.Background(), workdir, branch)
}

func deleteRemoteBranchCmdContext(ctx context.Context, workdir, branch string) error {
	if isMainBranchContext(ctx, workdir, branch) {
		return fmt.Errorf("refusing to delete remote main branch %q", branch)
	}
	cmd := exec.CommandContext(ctx, "git", "push", "origin", "--delete", branch)
	cmd.Dir = workdir
	output, err := cmd.CombinedOutput()
	if err != nil {
		outputStr := string(output)
		// Check if branch was already deleted (GitHub auto-delete)
		if strings.Contains(outputStr, "remote ref does not exist") ||
			strings.Contains(outputStr, "unable to delete") ||
			strings.Contains(outputStr, "couldn't find remote ref") {
			return nil // Not an error - branch already gone
		}
		return fmt.Errorf("delete remote branch: %s", strings.TrimSpace(outputStr))
	}
	return nil
}

// checkRemoteBranch returns a command to check if a remote branch exists.
func (p *Plugin) checkRemoteBranch(wt *Worktree) tea.Cmd {
	ctx, scope := p.newLifecycleScope(wt)
	workDir, branch, name := p.ctx.WorkDir, wt.Branch, wt.Name
	return func() tea.Msg {
		exists := checkRemoteBranchExistsContext(ctx, workDir, branch)
		return RemoteCheckDoneMsg{
			OperationScope: scope,
			WorkspaceName:  name,
			Branch:         branch,
			Exists:         exists,
		}
	}
}

// loadBranches returns a command to fetch all local branches.
func (p *Plugin) loadBranches() tea.Cmd {
	ctx, scope := p.newContextScope(nil)
	workDir := p.ctx.WorkDir
	return func() tea.Msg {
		cmd := exec.CommandContext(ctx, "git", "branch", "--format=%(refname:short)")
		cmd.Dir = workDir
		output, err := cmd.Output()
		if err != nil {
			return BranchListMsg{OperationScope: scope, Err: fmt.Errorf("git branch: %w", err)}
		}

		var branches []string
		for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
			if line != "" {
				branches = append(branches, line)
			}
		}
		return BranchListMsg{OperationScope: scope, Branches: branches}
	}
}

// filterBranches filters branches based on a search query.
func filterBranches(query string, allBranches []string) []string {
	if query == "" {
		return allBranches
	}

	query = strings.ToLower(query)
	var matches []string
	for _, branch := range allBranches {
		if strings.Contains(strings.ToLower(branch), query) {
			matches = append(matches, branch)
		}
	}
	return matches
}

// setupTDRoot creates a .td-root file in the worktree pointing to the main repo.
// This allows td commands in the worktree to use the main repo's database.
func (p *Plugin) setupTDRoot(worktreePath string) error {
	return setupTDRootContext(context.Background(), p.ctx.WorkDir, p.ctx.ProjectRoot, worktreePath)
}

func setupTDRootContext(ctx context.Context, workDir, projectRoot, worktreePath string) error {
	mainPath := mainWorktreePathContext(ctx, workDir)
	if err := ctx.Err(); err != nil {
		return err
	}
	if mainPath == "" {
		mainPath = workDir
	}
	return tdroot.CreateTDRoot(projectRoot, worktreePath, mainPath)
}

const sidecarTaskFile = "task"
const sidecarAgentFile = "agent"

// sidecarAgentStartFile is intentionally a dotfile in the worktree root (not centralized storage)
// so users can check it in or add it to .gitignore per-repo. It overrides the agent launch command
// for that specific worktree/branch.
const sidecarAgentStartFile = ".sidecar-agent-start"
const sidecarPRFile = "pr"
const sidecarBaseFile = "base"

// saveBaseBranch persists the base branch to the centralized worktree data directory.
func saveBaseBranch(projectRoot, worktreePath string, branch string) error {
	return saveBaseBranchContext(context.Background(), projectRoot, worktreePath, branch)
}

func saveBaseBranchContext(ctx context.Context, projectRoot, worktreePath string, branch string) error {
	wtDir, err := projectdir.WorktreeDirContext(ctx, projectRoot, worktreePath)
	if err != nil {
		return fmt.Errorf("resolve worktree dir: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if branch == "" {
		basePath := filepath.Join(wtDir, sidecarBaseFile)
		_ = os.Remove(basePath)
		return nil
	}
	basePath := filepath.Join(wtDir, sidecarBaseFile)
	return os.WriteFile(basePath, []byte(branch+"\n"), 0644)
}

// loadBaseBranch reads the base branch from the centralized worktree data directory.
func loadBaseBranch(projectRoot, worktreePath string) string {
	return loadBaseBranchContext(context.Background(), projectRoot, worktreePath)
}

func loadBaseBranchContext(ctx context.Context, projectRoot, worktreePath string) string {
	wtDir, err := projectdir.WorktreeDirContext(ctx, projectRoot, worktreePath)
	if err != nil {
		return ""
	}
	basePath := filepath.Join(wtDir, sidecarBaseFile)
	content, err := os.ReadFile(basePath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(content))
}

// loadTaskLink reads the linked task ID from the centralized worktree data directory.
func loadTaskLink(projectRoot, worktreePath string) string {
	wtDir, err := projectdir.WorktreeDir(projectRoot, worktreePath)
	if err != nil {
		return ""
	}
	taskPath := filepath.Join(wtDir, sidecarTaskFile)
	content, err := os.ReadFile(taskPath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(content))
}

// saveAgentType persists the chosen agent type to the centralized worktree data directory.
func saveAgentType(projectRoot, worktreePath string, agentType AgentType) error {
	return saveAgentTypeContext(context.Background(), projectRoot, worktreePath, agentType)
}

func saveAgentTypeContext(ctx context.Context, projectRoot, worktreePath string, agentType AgentType) error {
	wtDir, err := projectdir.WorktreeDirContext(ctx, projectRoot, worktreePath)
	if err != nil {
		return fmt.Errorf("resolve worktree dir: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if agentType == AgentNone || agentType == "" {
		// Remove file if None selected
		agentPath := filepath.Join(wtDir, sidecarAgentFile)
		_ = os.Remove(agentPath)
		return nil
	}
	agentPath := filepath.Join(wtDir, sidecarAgentFile)
	return os.WriteFile(agentPath, []byte(string(agentType)+"\n"), 0644)
}

// loadAgentType reads the chosen agent type from the centralized worktree data directory.
func loadAgentType(projectRoot, worktreePath string) AgentType {
	wtDir, err := projectdir.WorktreeDir(projectRoot, worktreePath)
	if err != nil {
		return AgentNone
	}
	agentPath := filepath.Join(wtDir, sidecarAgentFile)
	content, err := os.ReadFile(agentPath)
	if err != nil {
		return AgentNone
	}
	return AgentType(strings.TrimSpace(string(content)))
}

// savePRURL persists the PR URL to the centralized worktree data directory.
func savePRURL(projectRoot, worktreePath string, prURL string) error {
	return savePRURLContext(context.Background(), projectRoot, worktreePath, prURL)
}

func savePRURLContext(ctx context.Context, projectRoot, worktreePath string, prURL string) error {
	wtDir, err := projectdir.WorktreeDirContext(ctx, projectRoot, worktreePath)
	if err != nil {
		return fmt.Errorf("resolve worktree dir: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if prURL == "" {
		// Remove file if empty
		prPath := filepath.Join(wtDir, sidecarPRFile)
		_ = os.Remove(prPath)
		return nil
	}
	prPath := filepath.Join(wtDir, sidecarPRFile)
	return os.WriteFile(prPath, []byte(prURL+"\n"), 0644)
}

// loadPRURL reads the PR URL from the centralized worktree data directory.
func loadPRURL(projectRoot, worktreePath string) string {
	wtDir, err := projectdir.WorktreeDir(projectRoot, worktreePath)
	if err != nil {
		return ""
	}
	prPath := filepath.Join(wtDir, sidecarPRFile)
	content, err := os.ReadFile(prPath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(content))
}

// linkTask returns a command to link a td task to a worktree.
func (p *Plugin) linkTask(wt *Worktree, taskID string) tea.Cmd {
	ctx, scope := p.newLifecycleScope(wt)
	projectRoot := p.ctx.ProjectRoot
	workDir, path, name := p.ctx.WorkDir, wt.Path, wt.Name
	return func() tea.Msg {
		// Validate task exists by running td show
		cmd := exec.CommandContext(ctx, "td", "show", taskID)
		cmd.Dir = workDir
		if err := cmd.Run(); err != nil {
			return TaskLinkedMsg{
				OperationScope: scope,
				WorkspaceName:  name,
				Err:            fmt.Errorf("task not found: %s", taskID),
			}
		}

		// Write task link file to centralized worktree data directory
		wtDir, err := projectdir.WorktreeDirContext(ctx, projectRoot, path)
		if err != nil {
			return TaskLinkedMsg{
				OperationScope: scope,
				WorkspaceName:  name,
				Err:            fmt.Errorf("resolve worktree dir: %w", err),
			}
		}
		taskPath := filepath.Join(wtDir, sidecarTaskFile)
		if err := ctx.Err(); err != nil {
			return TaskLinkedMsg{OperationScope: scope, WorkspaceName: name, Err: err}
		}
		if err := os.WriteFile(taskPath, []byte(taskID+"\n"), 0644); err != nil {
			return TaskLinkedMsg{
				OperationScope: scope,
				WorkspaceName:  name,
				Err:            fmt.Errorf("write task file: %w", err),
			}
		}

		return TaskLinkedMsg{
			OperationScope: scope,
			WorkspaceName:  name,
			TaskID:         taskID,
		}
	}
}

// unlinkTask returns a command to unlink a td task from a worktree.
func (p *Plugin) unlinkTask(wt *Worktree) tea.Cmd {
	ctx, scope := p.newLifecycleScope(wt)
	projectRoot := p.ctx.ProjectRoot
	path, name := wt.Path, wt.Name
	return func() tea.Msg {
		wtDir, err := projectdir.WorktreeDirContext(ctx, projectRoot, path)
		if err != nil {
			return TaskLinkedMsg{
				OperationScope: scope,
				WorkspaceName:  name,
				Err:            fmt.Errorf("resolve worktree dir: %w", err),
			}
		}
		taskPath := filepath.Join(wtDir, sidecarTaskFile)
		if err := ctx.Err(); err != nil {
			return TaskLinkedMsg{OperationScope: scope, WorkspaceName: name, Err: err}
		}
		if err := os.Remove(taskPath); err != nil && !os.IsNotExist(err) {
			return TaskLinkedMsg{
				OperationScope: scope,
				WorkspaceName:  name,
				Err:            fmt.Errorf("remove task file: %w", err),
			}
		}

		return TaskLinkedMsg{
			OperationScope: scope,
			WorkspaceName:  name,
			TaskID:         "", // Empty means unlinked
		}
	}
}

// loadOpenTasks fetches all non-closed tasks from td.
func (p *Plugin) loadOpenTasks() tea.Cmd {
	ctx, scope := p.newContextScope(nil)
	workDir := p.ctx.WorkDir
	return func() tea.Msg {
		// Use --limit 500 to fetch more items (td defaults to 50)
		// Include all statuses except closed so users can link tasks in_review, etc.
		cmd := exec.CommandContext(ctx, "td", "list", "--json", "--status", "open,in_progress,in_review", "--limit", "500")
		cmd.Dir = workDir
		output, err := cmd.Output()
		if err != nil {
			return TaskSearchResultsMsg{OperationScope: scope, Err: fmt.Errorf("td list: %w", err)}
		}

		tasks, err := parseTDJSON(output)
		return TaskSearchResultsMsg{OperationScope: scope, Tasks: tasks, Err: err}
	}
}

// parseTDJSON parses JSON output from td list command.
func parseTDJSON(data []byte) ([]Task, error) {
	// Handle empty response
	if len(data) == 0 {
		return []Task{}, nil
	}

	// td outputs a JSON array of issues
	type tdIssue struct {
		ID          string `json:"id"`
		Title       string `json:"title"`
		Status      string `json:"status"`
		Description string `json:"description"`
		Type        string `json:"type"`
		ParentID    string `json:"parent_id"`
	}

	var issues []tdIssue
	if err := json.Unmarshal(data, &issues); err != nil {
		return nil, fmt.Errorf("parse td json: %w", err)
	}

	// Build map of epic IDs to titles for lookup
	epicTitles := make(map[string]string)
	for _, issue := range issues {
		if issue.Type == "epic" {
			epicTitles[issue.ID] = issue.Title
		}
	}

	tasks := make([]Task, len(issues))
	for i, issue := range issues {
		tasks[i] = Task{
			ID:          issue.ID,
			Title:       issue.Title,
			Status:      issue.Status,
			Description: issue.Description,
			EpicTitle:   epicTitles[issue.ParentID], // Populate epic title if task has parent
		}
	}
	return tasks, nil
}

// filterTasks filters tasks using fuzzy matching and returns results sorted by relevance.
// Scores against Title (3x weight), ID (2x), and EpicTitle (1x).
// When query is empty, returns all tasks unmodified.
func filterTasks(query string, allTasks []Task) []Task {
	if query == "" {
		return allTasks
	}

	type scoredTask struct {
		task  Task
		score int
	}

	var scored []scoredTask

	for _, task := range allTasks {
		titleScore, _ := palette.FuzzyMatch(query, task.Title)
		idScore, _ := palette.FuzzyMatch(query, task.ID)
		epicScore, _ := palette.FuzzyMatch(query, task.EpicTitle)

		total := titleScore*3 + idScore*2 + epicScore

		if total > 0 {
			scored = append(scored, scoredTask{task: task, score: total})
		}
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	result := make([]Task, len(scored))
	for i, s := range scored {
		result[i] = s.task
	}

	return result
}

// ValidateBranchName validates a git branch name and returns validation state.
// Returns: (valid, errors, sanitized suggestion)
// Based on git-check-ref-format rules.
func ValidateBranchName(name string) (bool, []string, string) {
	var errors []string

	if name == "" {
		return false, []string{}, ""
	}

	// Invalid characters in git branch names
	invalidChars := []string{" ", "~", "^", ":", "?", "*", "[", "\\", "@{"}
	for _, char := range invalidChars {
		if strings.Contains(name, char) {
			errors = append(errors, fmt.Sprintf("contains '%s'", char))
		}
	}

	// Cannot start with dash, dot, or slash
	if strings.HasPrefix(name, "-") {
		errors = append(errors, "starts with '-'")
	}
	if strings.HasPrefix(name, ".") {
		errors = append(errors, "starts with '.'")
	}
	if strings.HasPrefix(name, "/") {
		errors = append(errors, "starts with '/'")
	}

	// Cannot end with .lock
	if strings.HasSuffix(name, ".lock") {
		errors = append(errors, "ends with '.lock'")
	}

	// Cannot contain consecutive dots
	if strings.Contains(name, "..") {
		errors = append(errors, "contains '..'")
	}

	// Cannot end with dot
	if strings.HasSuffix(name, ".") {
		errors = append(errors, "ends with '.'")
	}

	// Cannot end with slash
	if strings.HasSuffix(name, "/") {
		errors = append(errors, "ends with '/'")
	}

	// Cannot contain double slash
	if strings.Contains(name, "//") {
		errors = append(errors, "contains '//'")
	}

	// Cannot contain slash followed by dot (e.g., "feature/.hidden")
	if strings.Contains(name, "/.") {
		errors = append(errors, "contains '/.'")
	}

	// Cannot be exactly "@"
	if name == "@" {
		errors = append(errors, "cannot be '@'")
	}

	// Cannot contain control characters (ASCII < 32) or DEL (ASCII 127)
	for _, r := range name {
		if r < 32 || r == 127 {
			errors = append(errors, "contains control character")
			break
		}
	}

	// Generate sanitized suggestion
	sanitized := SanitizeBranchName(name)

	return len(errors) == 0, errors, sanitized
}

// SanitizeBranchName converts a string to a valid git branch name.
// The output should always pass ValidateBranchName.
func SanitizeBranchName(name string) string {
	// Replace spaces with dashes
	result := strings.ReplaceAll(name, " ", "-")

	// Remove invalid characters
	invalidChars := []string{"~", "^", ":", "?", "*", "[", "\\", "@{"}
	for _, char := range invalidChars {
		result = strings.ReplaceAll(result, char, "")
	}

	// Remove control characters (ASCII < 32 and DEL 127)
	var cleaned strings.Builder
	for _, r := range result {
		if r >= 32 && r != 127 {
			cleaned.WriteRune(r)
		}
	}
	result = cleaned.String()

	// Remove leading dashes, dots, and slashes
	for len(result) > 0 && (result[0] == '-' || result[0] == '.' || result[0] == '/') {
		result = result[1:]
	}

	// Collapse consecutive dots to single dot
	for strings.Contains(result, "..") {
		result = strings.ReplaceAll(result, "..", ".")
	}

	// Collapse double slashes to single slash
	for strings.Contains(result, "//") {
		result = strings.ReplaceAll(result, "//", "/")
	}

	// Remove /. sequences (slash followed by dot)
	result = strings.ReplaceAll(result, "/.", "/")

	// Remove trailing .lock
	result = strings.TrimSuffix(result, ".lock")

	// Remove trailing dots, slashes, and dashes
	for len(result) > 0 {
		last := result[len(result)-1]
		if last == '.' || last == '/' || last == '-' {
			result = result[:len(result)-1]
		} else {
			break
		}
	}

	// Final check: remove .lock suffix if exposed by previous cleanup steps
	// (e.g., "foo.lock-" -> "foo.lock" after dash trim -> "foo")
	for strings.HasSuffix(result, ".lock") {
		result = strings.TrimSuffix(result, ".lock")
	}

	// Handle special case of "@"
	if result == "@" {
		result = ""
	}

	// Convert to lowercase (common convention)
	result = strings.ToLower(result)

	return result
}

// loadTaskDetails fetches full task details from td.
func (p *Plugin) loadTaskDetails(taskID string) tea.Cmd {
	ctx, scope := p.newOperationScope(p.selectedWorktree())
	workDir := p.ctx.WorkDir
	return func() tea.Msg {
		cmd := exec.CommandContext(ctx, "td", "show", taskID, "--json")
		cmd.Dir = workDir
		output, err := cmd.Output()
		if err != nil {
			return TaskDetailsLoadedMsg{OperationScope: scope, TaskID: taskID, Err: fmt.Errorf("td show: %w", err)}
		}

		var details struct {
			ID          string `json:"id"`
			Title       string `json:"title"`
			Status      string `json:"status"`
			Priority    string `json:"priority"`
			Type        string `json:"type"`
			Description string `json:"description"`
			Acceptance  string `json:"acceptance"`
			CreatedAt   string `json:"created_at"`
			UpdatedAt   string `json:"updated_at"`
		}

		if err := json.Unmarshal(output, &details); err != nil {
			return TaskDetailsLoadedMsg{OperationScope: scope, TaskID: taskID, Err: fmt.Errorf("parse task json: %w", err)}
		}

		return TaskDetailsLoadedMsg{
			OperationScope: scope,
			TaskID:         taskID,
			Details: &TaskDetails{
				ID:          details.ID,
				Title:       details.Title,
				Status:      details.Status,
				Priority:    details.Priority,
				Type:        details.Type,
				Description: details.Description,
				Acceptance:  details.Acceptance,
				CreatedAt:   details.CreatedAt,
				UpdatedAt:   details.UpdatedAt,
			},
		}
	}
}
