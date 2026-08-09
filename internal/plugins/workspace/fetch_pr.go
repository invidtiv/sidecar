package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/projectdir"
)

// fetchPRList runs gh pr list and returns open PRs.
func (p *Plugin) fetchPRList() tea.Cmd {
	workDir := p.ctx.WorkDir
	ctx, scope := p.newLifecycleScope(nil)
	return func() tea.Msg {
		repository, err := currentGitHubRepositoryContext(ctx, workDir)
		if err != nil {
			return FetchPRListMsg{OperationScope: scope, Err: err}
		}
		cmd := exec.CommandContext(ctx, "gh", "pr", "list",
			"--repo", repository,
			"--json", "number,id,title,headRefName,headRefOid,baseRefName,headRepository,headRepositoryOwner,url,isDraft,createdAt,author",
			"--limit", "30",
		)
		cmd.Dir = workDir
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		output, err := cmd.Output()
		if err != nil {
			errMsg := strings.TrimSpace(stderr.String())
			if errMsg == "" {
				errMsg = err.Error()
			}
			return FetchPRListMsg{OperationScope: scope, Err: fmt.Errorf("gh pr list: %s", errMsg)}
		}

		var prs []PRListItem
		if err := json.Unmarshal(output, &prs); err != nil {
			return FetchPRListMsg{OperationScope: scope, Err: fmt.Errorf("parse pr list: %w", err)}
		}
		for i := range prs {
			prs[i].Repository = repository
		}

		return FetchPRListMsg{OperationScope: scope, PRs: prs}
	}
}

// fetchAndCreateWorktree fetches a PR branch and creates a worktree from it.
func (p *Plugin) fetchAndCreateWorktree(pr PRListItem) tea.Cmd {
	workDir := p.ctx.WorkDir
	projectRoot := p.ctx.ProjectRoot
	dirPrefix := p.ctx.Config != nil && p.ctx.Config.Plugins.Workspace.DirPrefix
	ctx, scope := p.newLifecycleScope(nil)
	mainRepoDir := projectRoot
	if mainRepoDir == "" {
		mainRepoDir = workDir
	}

	return func() tea.Msg {
		if pr.Number <= 0 || pr.Repository == "" || pr.Branch == "" || pr.BaseBranch == "" || pr.HeadOID == "" {
			return FetchPRDoneMsg{OperationScope: scope, Err: fmt.Errorf("pull request identity is incomplete; refresh the PR list and retry")}
		}
		baseRemote, err := resolveRemoteForRepositoryContext(ctx, workDir, pr.Repository)
		if err != nil {
			return FetchPRDoneMsg{OperationScope: scope, Err: err}
		}
		operationRefKey := stablePathKey(scope.OperationID + "|" + pr.NodeID)
		tempRef := fmt.Sprintf("refs/sidecar/pr/%d/%s/head", pr.Number, operationRefKey)
		tempBaseRef := fmt.Sprintf("refs/sidecar/pr/%d/%s/base", pr.Number, operationRefKey)
		refspec := fmt.Sprintf("+refs/pull/%d/head:%s", pr.Number, tempRef)
		baseRefspec := fmt.Sprintf("+refs/heads/%s:%s", pr.BaseBranch, tempBaseRef)
		fetchCmd := exec.CommandContext(ctx, "git", "fetch", baseRemote, refspec, baseRefspec)
		fetchCmd.Dir = workDir
		if output, err := fetchCmd.CombinedOutput(); err != nil {
			return FetchPRDoneMsg{OperationScope: scope, Err: fmt.Errorf("git fetch PR #%d: %s", pr.Number, strings.TrimSpace(string(output)))}
		}
		defer deleteTemporaryPRRef(workDir, tempRef)
		defer deleteTemporaryPRRef(workDir, tempBaseRef)
		fetchedOID, err := gitOutputContext(ctx, workDir, "rev-parse", tempRef)
		if err != nil || fetchedOID != pr.HeadOID {
			return FetchPRDoneMsg{OperationScope: scope, Err: fmt.Errorf("fetched PR head changed: GitHub reported %s, fetched %s", pr.HeadOID, fetchedOID)}
		}
		if _, err := gitOutputContext(ctx, workDir, "rev-parse", tempBaseRef); err != nil {
			return FetchPRDoneMsg{OperationScope: scope, Err: fmt.Errorf("pull request base %q is not present in %s: %w", pr.BaseBranch, baseRemote, err)}
		}
		localBranch, alreadyLocal, err := choosePRImportBranchContext(ctx, workDir, pr.Branch, pr.Number, fetchedOID)
		if err != nil {
			return FetchPRDoneMsg{OperationScope: scope, Err: err}
		}
		if existingPath := findWorktreePathForBranchContext(ctx, workDir, localBranch); existingPath != "" {
			_ = savePRIdentityContext(ctx, projectRoot, existingPath, pr.identity())
			_ = saveBaseBranchContext(ctx, projectRoot, existingPath, pr.BaseBranch)
			return FetchPRDoneMsg{OperationScope: scope, AlreadyLocal: true, Branch: localBranch}
		}

		dirName := localBranch
		if dirPrefix {
			if repoName := repoNameContext(ctx, workDir); repoName != "" {
				dirName = repoName + "-" + localBranch
			}
			if err := ctx.Err(); err != nil {
				return FetchPRDoneMsg{OperationScope: scope, Err: err}
			}
		}
		wtPath := filepath.Join(filepath.Dir(mainRepoDir), dirName)
		newWorktree := func(baseBranch string) *Worktree {
			wt := &Worktree{
				Name: dirName, Path: wtPath, Branch: localBranch, BaseBranch: baseBranch,
				PRURL: pr.URL, Status: StatusPaused, CreatedAt: time.Now(), UpdatedAt: time.Now(),
			}
			wt.Key, _ = projectdir.WorktreeKey(wtPath)
			wt.RepoKey = scope.RepoKey
			return wt
		}
		args := []string{"worktree", "add"}
		if !alreadyLocal {
			args = append(args, "-b", localBranch)
		}
		args = append(args, wtPath)
		if !alreadyLocal {
			args = append(args, tempRef)
		} else {
			args = append(args, localBranch)
		}
		addCmd := exec.CommandContext(ctx, "git", args...)
		addCmd.Dir = workDir
		if output, err := addCmd.CombinedOutput(); err != nil {
			return FetchPRDoneMsg{OperationScope: scope, Err: fmt.Errorf("git worktree add: %s", strings.TrimSpace(string(output)))}
		}
		wt := newWorktree(pr.BaseBranch)
		createdOID, oidErr := gitOutputContext(ctx, wtPath, "rev-parse", "HEAD")
		if oidErr != nil || createdOID != fetchedOID {
			return FetchPRDoneMsg{OperationScope: scope, Worktree: wt, Err: fmt.Errorf("created worktree identity changed: expected %s, found %s", fetchedOID, createdOID)}
		}

		_ = savePRIdentityContext(ctx, projectRoot, wtPath, pr.identity())
		_ = saveBaseBranchContext(ctx, projectRoot, wtPath, pr.BaseBranch)
		if err := ctx.Err(); err != nil {
			return FetchPRDoneMsg{OperationScope: scope, Worktree: wt, Err: err}
		}

		return FetchPRDoneMsg{OperationScope: scope, Worktree: wt}
	}
}

func deleteTemporaryPRRef(workDir, ref string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "update-ref", "-d", ref)
	cmd.Dir = workDir
	_ = cmd.Run()
}

func choosePRImportBranchContext(ctx context.Context, workDir, requested string, number int, expectedOID string) (string, bool, error) {
	for i := 0; i <= 100; i++ {
		candidate := requested
		if i == 1 {
			candidate = fmt.Sprintf("%s-pr-%d", requested, number)
		}
		if i > 1 {
			candidate = fmt.Sprintf("%s-pr-%d-%d", requested, number, i)
		}
		if _, err := gitOutputContext(ctx, workDir, "check-ref-format", "--branch", candidate); err != nil {
			return "", false, fmt.Errorf("invalid PR branch %q: %w", candidate, err)
		}
		cmd := exec.CommandContext(ctx, "git", "rev-parse", "--verify", "--quiet", "refs/heads/"+candidate)
		cmd.Dir = workDir
		out, err := cmd.Output()
		if err != nil {
			if ctx.Err() != nil {
				return "", false, ctx.Err()
			}
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
				return candidate, false, nil
			}
			return "", false, fmt.Errorf("inspect local branch %q: %w", candidate, err)
		}
		if strings.TrimSpace(string(out)) == expectedOID {
			return candidate, true, nil
		}
	}
	return "", false, fmt.Errorf("local branches conflict with PR #%d; rename one or choose a different workspace name", number)
}

// findWorktreePathForBranch returns the worktree path for a branch, or "" if not found.
func findWorktreePathForBranch(workDir, branch string) string {
	return findWorktreePathForBranchContext(context.Background(), workDir, branch)
}

func findWorktreePathForBranchContext(ctx context.Context, workDir, branch string) string {
	cmd := exec.CommandContext(ctx, "git", "worktree", "list", "--porcelain")
	cmd.Dir = workDir
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	var currentPath string
	for line := range strings.SplitSeq(string(output), "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			currentPath = p
		} else if b, ok := strings.CutPrefix(line, "branch refs/heads/"); ok {
			if b == branch {
				return currentPath
			}
		}
	}
	return ""
}

// filteredFetchPRItems returns PR items matching the current filter.
func (p *Plugin) filteredFetchPRItems() []PRListItem {
	if p.fetchPRFilter == "" {
		return p.fetchPRItems
	}
	query := strings.ToLower(p.fetchPRFilter)
	var matches []PRListItem
	for _, pr := range p.fetchPRItems {
		if strings.Contains(strings.ToLower(pr.Title), query) ||
			strings.Contains(strings.ToLower(pr.Branch), query) ||
			strings.Contains(strings.ToLower(pr.Author.Login), query) ||
			strings.Contains(fmt.Sprintf("#%d", pr.Number), query) {
			matches = append(matches, pr)
		}
	}
	return matches
}

// adjustFetchPRScroll keeps the cursor visible within the 10-item window.
func (p *Plugin) adjustFetchPRScroll() {
	const maxVisible = 10
	if p.fetchPRCursor < p.fetchPRScrollOffset {
		p.fetchPRScrollOffset = p.fetchPRCursor
	}
	if p.fetchPRCursor >= p.fetchPRScrollOffset+maxVisible {
		p.fetchPRScrollOffset = p.fetchPRCursor - maxVisible + 1
	}
	if p.fetchPRScrollOffset < 0 {
		p.fetchPRScrollOffset = 0
	}
}

// clearFetchPRState resets fetch PR modal state.
func (p *Plugin) clearFetchPRState() {
	p.fetchPRItems = nil
	p.fetchPRFilter = ""
	p.fetchPRCursor = 0
	p.fetchPRScrollOffset = 0
	p.fetchPRLoading = false
	p.fetchPRError = ""
	p.clearFetchPRModal()
}
