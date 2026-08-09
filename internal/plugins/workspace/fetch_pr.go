package workspace

import (
	"bytes"
	"context"
	"encoding/json"
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
		cmd := exec.CommandContext(ctx, "gh", "pr", "list",
			"--json", "number,title,headRefName,url,isDraft,createdAt,author",
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

		return FetchPRListMsg{OperationScope: scope, PRs: prs}
	}
}

// fetchAndCreateWorktree fetches a PR branch and creates a worktree from it.
func (p *Plugin) fetchAndCreateWorktree(pr PRListItem) tea.Cmd {
	workDir := p.ctx.WorkDir
	projectRoot := p.ctx.ProjectRoot
	dirPrefix := p.ctx.Config != nil && p.ctx.Config.Plugins.Workspace.DirPrefix
	ctx, scope := p.newLifecycleScope(nil)
	branch := pr.Branch
	mainRepoDir := projectRoot
	if mainRepoDir == "" {
		mainRepoDir = workDir
	}

	return func() tea.Msg {
		dirName := branch
		if dirPrefix {
			if repoName := repoNameContext(ctx, workDir); repoName != "" {
				dirName = repoName + "-" + branch
			}
			if err := ctx.Err(); err != nil {
				return FetchPRDoneMsg{OperationScope: scope, Err: err}
			}
		}
		wtPath := filepath.Join(filepath.Dir(mainRepoDir), dirName)
		newWorktree := func(baseBranch string) *Worktree {
			wt := &Worktree{
				Name: dirName, Path: wtPath, Branch: branch, BaseBranch: baseBranch,
				PRURL: pr.URL, Status: StatusPaused, CreatedAt: time.Now(), UpdatedAt: time.Now(),
			}
			wt.Key, _ = projectdir.WorktreeKey(wtPath)
			wt.RepoKey = scope.RepoKey
			return wt
		}
		// Fetch the remote branch
		fetchCmd := exec.CommandContext(ctx, "git", "fetch", "origin", branch)
		fetchCmd.Dir = workDir
		if output, err := fetchCmd.CombinedOutput(); err != nil {
			return FetchPRDoneMsg{OperationScope: scope, Err: fmt.Errorf("git fetch: %s", strings.TrimSpace(string(output)))}
		}

		// Create worktree tracking the remote branch
		addCmd := exec.CommandContext(ctx, "git", "worktree", "add", "-b", branch, wtPath, "origin/"+branch)
		addCmd.Dir = workDir
		if output, err := addCmd.CombinedOutput(); err != nil {
			outStr := strings.TrimSpace(string(output))
			if strings.Contains(outStr, "already exists") {
				// Branch exists locally. Try creating worktree without -b.
				addCmd2 := exec.CommandContext(ctx, "git", "worktree", "add", wtPath, branch)
				addCmd2.Dir = workDir
				if output2, err2 := addCmd2.CombinedOutput(); err2 != nil {
					outStr2 := strings.TrimSpace(string(output2))
					// Worktree already checked out — find and focus it
					if strings.Contains(outStr2, "already") {
						existingPath := findWorktreePathForBranchContext(ctx, workDir, branch)
						if existingPath != "" {
							_ = savePRURLContext(ctx, projectRoot, existingPath, pr.URL)
							_ = saveBaseBranchContext(ctx, projectRoot, existingPath, detectDefaultBranchContext(ctx, workDir))
						}
						if err := ctx.Err(); err != nil {
							return FetchPRDoneMsg{OperationScope: scope, Err: err}
						}
						return FetchPRDoneMsg{OperationScope: scope, AlreadyLocal: true, Branch: branch}
					}
					return FetchPRDoneMsg{OperationScope: scope, Err: fmt.Errorf("git worktree add: %s", outStr2)}
				}
				// Worktree created from existing local branch
				wt := newWorktree("")
				_ = savePRURLContext(ctx, projectRoot, wtPath, pr.URL)
				baseBranch := detectDefaultBranchContext(ctx, workDir)
				wt.BaseBranch = baseBranch
				_ = saveBaseBranchContext(ctx, projectRoot, wtPath, baseBranch)
				if err := ctx.Err(); err != nil {
					return FetchPRDoneMsg{OperationScope: scope, Worktree: wt, Err: err}
				}
				return FetchPRDoneMsg{OperationScope: scope, Worktree: wt, AlreadyLocal: true}
			}
			return FetchPRDoneMsg{OperationScope: scope, Err: fmt.Errorf("git worktree add: %s", outStr)}
		}

		// Write PR URL to centralized worktree data directory (non-fatal)
		wt := newWorktree("")
		_ = savePRURLContext(ctx, projectRoot, wtPath, pr.URL)

		// Detect base branch for diff
		baseBranch := detectDefaultBranchContext(ctx, workDir)
		wt.BaseBranch = baseBranch

		// Persist base branch to centralized worktree data directory (non-fatal)
		_ = saveBaseBranchContext(ctx, projectRoot, wtPath, baseBranch)
		if err := ctx.Err(); err != nil {
			return FetchPRDoneMsg{OperationScope: scope, Worktree: wt, Err: err}
		}

		return FetchPRDoneMsg{OperationScope: scope, Worktree: wt}
	}
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
