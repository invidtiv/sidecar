package uirequest

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/marcus/sidecar/internal/terminallink"
)

// ResolveTarget parses and validates a target string against the shell's workspace root.
//
// Target can be:
// 1. "td-xxxxxx" (a td issue id)
// 2. "path" or "path:line" (a file within workDir)
//
// If explicitLine > 0, it overrides any :line suffix.
func ResolveTarget(workDir, raw string, explicitLine int) (Target, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Target{}, fmt.Errorf("target cannot be empty")
	}

	// 1. Issue ID detection
	if terminallink.IssueID(raw) {
		return Target{
			Kind:  TargetKindIssue,
			Value: raw,
			Line:  0,
		}, nil
	}

	// 2. File path with optional :line suffix
	targetPath := raw
	line := explicitLine
	if colonIdx := strings.LastIndex(raw, ":"); colonIdx > 0 && colonIdx < len(raw)-1 {
		suffix := raw[colonIdx+1:]
		if lineNum, err := strconv.Atoi(suffix); err == nil && lineNum > 0 {
			targetPath = raw[:colonIdx]
			if explicitLine <= 0 {
				line = lineNum
			}
		}
	}

	if targetPath == "" {
		return Target{}, fmt.Errorf("file path cannot be empty")
	}

	// If workDir is empty, use current working directory
	if workDir == "" {
		var err error
		workDir, err = os.Getwd()
		if err != nil {
			return Target{}, fmt.Errorf("resolve current directory: %w", err)
		}
	}

	// Canonicalize workDir
	workDirClean := filepath.Clean(workDir)
	if resolvedRoot, err := filepath.EvalSymlinks(workDirClean); err == nil {
		workDirClean = filepath.Clean(resolvedRoot)
	}

	// Resolve absolute path for candidate file
	var absPath string
	if filepath.IsAbs(targetPath) {
		absPath = filepath.Clean(targetPath)
	} else if strings.HasPrefix(targetPath, "~/") || targetPath == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Target{}, fmt.Errorf("resolve home dir: %w", err)
		}
		absPath = filepath.Join(home, strings.TrimPrefix(targetPath, "~"))
	} else {
		absPath = filepath.Join(workDirClean, targetPath)
	}

	// Eval symlinks of the target file
	resolvedTarget, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return Target{}, fmt.Errorf("file %q does not exist", targetPath)
		}
		return Target{}, fmt.Errorf("resolve target path %q: %w", targetPath, err)
	}
	resolvedTarget = filepath.Clean(resolvedTarget)

	// Stat to ensure it is a regular file
	info, err := os.Stat(resolvedTarget)
	if err != nil {
		return Target{}, fmt.Errorf("stat target %q: %w", targetPath, err)
	}
	if info.IsDir() {
		return Target{}, fmt.Errorf("target %q is a directory, not a file", targetPath)
	}
	if !info.Mode().IsRegular() {
		return Target{}, fmt.Errorf("target %q is not a regular file", targetPath)
	}

	// Verify target sits inside workDir
	rel, err := filepath.Rel(workDirClean, resolvedTarget)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return Target{}, fmt.Errorf("target %q resolves outside workspace root %s", targetPath, workDirClean)
	}

	// Return clean forward-slash relative path
	return Target{
		Kind:  TargetKindFile,
		Value: filepath.ToSlash(rel),
		Line:  line,
	}, nil
}
