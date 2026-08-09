package workspace

import (
	"io"
	"os"
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

