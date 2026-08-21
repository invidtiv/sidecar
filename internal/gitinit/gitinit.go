// Package gitinit is the one place Sidecar initializes a Git repository.
// Git status, Configuration's Add Project form, and first-run onboarding all
// call it rather than spawning `git init` themselves.
package gitinit

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SidecarGitignoreEntries lists sidecar state paths that should be ignored
// when the user explicitly initializes a repository from Sidecar. Opening an
// existing repository never mutates its .gitignore.
var SidecarGitignoreEntries = []string{
	".todos/",
	".sidecar/",
	".sidecar-agent",
	".sidecar-task",
	".sidecar-pr",
	".sidecar-start.sh",
	".sidecar-base",
	".td-root",
}

// ReadyMsg reports that a Git repository now exists at Root. Plugins that
// depend on worktrees or status reload from it without an app restart.
type ReadyMsg struct {
	Root string
}

// IsRepository reports whether dir is inside a Git work tree. It is meant for
// a tea.Cmd, not the render path: it spawns git.
func IsRepository(dir string) bool {
	if strings.TrimSpace(dir) == "" {
		return false
	}
	root, err := Root(dir)
	return err == nil && root != ""
}

// Root returns the top-level directory of the repository containing dir.
func Root(dir string) (string, error) {
	cmd := exec.Command("git", "--no-optional-locks", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Init runs `git init -b main` in dir and ensures sidecar local-state paths
// are ignored. Root is set when git init succeeded. A non-nil error with a
// non-empty Root is a non-fatal .gitignore warning; an empty Root is fatal.
func Init(dir string) (root string, err error) {
	cmd := exec.Command("git", "init", "-b", "main")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git init failed: %s", msg)
	}

	root, err = Root(dir)
	if err != nil {
		return "", fmt.Errorf("git init succeeded but repository was not detected: %w", err)
	}

	if err := EnsureGitignore(dir, SidecarGitignoreEntries); err != nil {
		return root, err
	}
	return root, nil
}

// EnsureGitignore appends missing entries to .gitignore in workDir.
func EnsureGitignore(workDir string, entries []string) error {
	gitignorePath := filepath.Join(workDir, ".gitignore")

	var existing string
	if data, err := os.ReadFile(gitignorePath); err == nil {
		existing = string(data)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read .gitignore: %w", err)
	}

	var missing []string
	for _, entry := range entries {
		found := false
		for _, line := range strings.Split(existing, "\n") {
			if strings.TrimSpace(line) == entry {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, entry)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	var toAppend strings.Builder
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		toAppend.WriteString("\n")
	}
	for _, entry := range missing {
		toAppend.WriteString(entry)
		toAppend.WriteString("\n")
	}

	f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open .gitignore: %w", err)
	}
	defer func() {
		_ = f.Close()
	}()

	if _, err := f.WriteString(toAppend.String()); err != nil {
		return fmt.Errorf("write .gitignore: %w", err)
	}
	return nil
}
