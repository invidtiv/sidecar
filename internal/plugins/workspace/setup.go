package workspace

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Default setup configuration
const (
	setupScriptName = ".worktree-setup.sh"
)

var (
	// Default env files to copy
	defaultEnvFiles = []string{".env", ".env.local", ".env.development", ".env.development.local"}
)

// SetupConfig holds worktree setup configuration.
type SetupConfig struct {
	CopyEnv        bool     // Whether to copy env files (default: true)
	EnvFiles       []string // List of env files to copy
	SymlinkDirs    []string // Directories to symlink (default: empty, opt-in)
	RunSetupScript bool     // Whether to run .worktree-setup.sh (default: true)
}

// DefaultSetupConfig returns the default setup configuration.
func DefaultSetupConfig() *SetupConfig {
	return &SetupConfig{
		CopyEnv:        true,
		EnvFiles:       defaultEnvFiles,
		SymlinkDirs:    nil, // Opt-in, not enabled by default
		RunSetupScript: true,
	}
}

// setupWorktree performs post-creation setup for a new worktree.
// This includes copying env files, creating symlinks, and running setup scripts.
func (p *Plugin) setupWorktree(worktreePath, branchName string) error {
	return p.setupWorktreeContext(context.Background(), worktreePath, branchName)
}

func (p *Plugin) setupWorktreeContext(ctx context.Context, worktreePath, branchName string) error {
	config := DefaultSetupConfig()
	mainRoot := p.ctx.ProjectRoot
	if mainRoot == "" {
		mainRoot = p.ctx.WorkDir
	}

	// 1. Copy environment files
	if config.CopyEnv {
		if err := copyEnvFilesFrom(mainRoot, worktreePath, config.EnvFiles); err != nil {
			p.ctx.Logger.Warn("failed to copy env files", "path", worktreePath, "error", err)
			// Don't fail creation for env file errors
		}
	}

	// 2. Create symlinks for large directories (if configured)
	if len(config.SymlinkDirs) > 0 {
		if err := p.symlinkDirs(worktreePath, config.SymlinkDirs); err != nil {
			p.ctx.Logger.Warn("failed to create symlinks", "path", worktreePath, "error", err)
			// Don't fail creation for symlink errors
		}
	}

	// 3. Run setup script (if exists)
	if config.RunSetupScript {
		plan := &CreateOperationPlan{MainWorktree: mainRoot, SourceWorktree: p.ctx.WorkDir, Path: worktreePath, Branch: branchName, HookPath: setupScriptName}
		if err := runSetupHookContext(ctx, plan); err != nil {
			p.ctx.Logger.Warn("setup script failed", "path", worktreePath, "error", err)
			// Don't fail creation for setup script errors
		}
	}

	return nil
}

// copyEnvFiles copies environment files from the main worktree to the new worktree.
func (p *Plugin) copyEnvFiles(worktreePath string, envFiles []string) error {
	mainRoot := p.ctx.ProjectRoot
	if mainRoot == "" {
		mainRoot = p.ctx.WorkDir
	}
	return copyEnvFilesFrom(mainRoot, worktreePath, envFiles)
}

func copyEnvFilesFrom(mainRoot, worktreePath string, envFiles []string) error {
	for _, envFile := range envFiles {
		src := filepath.Join(mainRoot, envFile)

		// Check if source file exists
		if _, err := os.Stat(src); os.IsNotExist(err) {
			continue // Skip if file doesn't exist
		}

		dst := filepath.Join(worktreePath, envFile)

		// Copy the file
		if err := copyFile(src, dst); err != nil {
			return fmt.Errorf("copy %s: %w", envFile, err)
		}
	}

	return nil
}

// copyFile copies a single file from src to dst, preserving permissions.
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = sourceFile.Close() }()

	// Get source file info for permissions
	sourceInfo, err := sourceFile.Stat()
	if err != nil {
		return err
	}

	// Create destination file with same permissions
	destFile, err := os.OpenFile(dst, os.O_RDWR|os.O_CREATE|os.O_TRUNC, sourceInfo.Mode())
	if err != nil {
		return err
	}
	defer func() { _ = destFile.Close() }()

	// Copy contents
	_, err = io.Copy(destFile, sourceFile)
	return err
}

// symlinkDirs creates symlinks from the main worktree to the new worktree
// for large directories like node_modules to save disk space.
func (p *Plugin) symlinkDirs(worktreePath string, dirs []string) error {
	for _, dir := range dirs {
		src := filepath.Join(p.ctx.WorkDir, dir)

		// Check if source directory exists
		srcInfo, err := os.Stat(src)
		if os.IsNotExist(err) {
			continue // Skip if directory doesn't exist in main worktree
		}
		if err != nil {
			p.ctx.Logger.Warn("failed to stat source dir",
				"dir", dir, "error", err)
			continue
		}

		// Only symlink directories
		if !srcInfo.IsDir() {
			continue
		}

		dst := filepath.Join(worktreePath, dir)

		// Remove existing directory in worktree if present
		// (git checkout might create empty dirs)
		if _, err := os.Lstat(dst); err == nil {
			if err := os.RemoveAll(dst); err != nil {
				p.ctx.Logger.Warn("failed to remove existing dir for symlink",
					"dir", dir, "dst", dst, "error", err)
				continue
			}
		}

		// Create symlink
		if err := os.Symlink(src, dst); err != nil {
			p.ctx.Logger.Warn("failed to create symlink",
				"dir", dir, "src", src, "dst", dst, "error", err)
			continue
		}

		p.ctx.Logger.Debug("created symlink", "dir", dir, "src", src, "dst", dst)
	}

	return nil
}

// runSetupScript executes the .worktree-setup.sh script if it exists in the main repo.
// The script runs with the new worktree as the working directory and receives
// environment variables for the main worktree path, branch name, and worktree path.
func (p *Plugin) runSetupScript(worktreePath, branchName string) error {
	return p.runSetupScriptContext(context.Background(), worktreePath, branchName)
}

func (p *Plugin) runSetupScriptContext(ctx context.Context, worktreePath, branchName string) error {
	mainRoot := p.ctx.ProjectRoot
	if mainRoot == "" {
		mainRoot = p.ctx.WorkDir
	}
	scriptPath := filepath.Join(mainRoot, setupScriptName)

	// Check if setup script exists
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		return nil // No script, that's fine
	}

	// Run the script with the worktree as working directory
	return runSetupHookContext(ctx, &CreateOperationPlan{MainWorktree: mainRoot, SourceWorktree: p.ctx.WorkDir, Path: worktreePath, Branch: branchName, HookPath: setupScriptName})
}
