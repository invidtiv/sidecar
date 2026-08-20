package workspaceops

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// ListLocalBranches returns local branch short names in workDir
// (`git branch --format=%(refname:short)`).
func ListLocalBranches(ctx context.Context, workDir string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "branch", "--format=%(refname:short)")
	cmd.Dir = workDir
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git branch: %w", err)
	}
	var branches []string
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if line != "" {
			branches = append(branches, line)
		}
	}
	return branches, nil
}

// CurrentBranch returns the checked-out branch name
// (`git rev-parse --abbrev-ref HEAD`). Detached HEAD is "HEAD".
func CurrentBranch(ctx context.Context, workDir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = workDir
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}
