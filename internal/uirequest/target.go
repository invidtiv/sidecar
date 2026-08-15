package uirequest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/marcus/sidecar/internal/terminallink"
	"github.com/marcus/sidecar/internal/workspacediff"
)

// ResolveOptions controls how ResolveTarget classifies a CLI argument.
type ResolveOptions struct {
	// Diff is sidecar open --diff. A missing positional becomes the working
	// tree (wt). A positional is a git spec, including HEAD and branch names.
	Diff bool
}

// ResolveTarget parses and validates a target string against the shell's workspace root.
//
// Classification, in order:
//  1. td- issue id (even with --diff)
//  2. --diff with no positional → working tree (Value "wt")
//  3. --diff with a positional → ParseSpec + ResolveSpec
//  4. Existing regular file inside workDir (a real file named abc1234 wins)
//  5. ParseSpec + ResolveSpec → TargetKindDiff
//  6. Usage error (a missing file is not fatal until hash resolution also fails)
func ResolveTarget(workDir, raw string, explicitLine int, opts ResolveOptions) (Target, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if opts.Diff {
			return Target{Kind: TargetKindDiff, Value: workspacediff.IdentityWorkingTree}, nil
		}
		return Target{}, fmt.Errorf("target cannot be empty")
	}

	if terminallink.IssueID(raw) {
		return Target{
			Kind:  TargetKindIssue,
			Value: raw,
			Line:  0,
		}, nil
	}

	workDir, err := canonicalizeWorkDir(workDir)
	if err != nil {
		return Target{}, err
	}

	if opts.Diff {
		return resolveDiffTarget(workDir, raw)
	}

	file, fileErr := resolveFileTarget(workDir, raw, explicitLine)
	if fileErr == nil {
		return file, nil
	}
	if !errors.Is(fileErr, os.ErrNotExist) {
		return Target{}, fileErr
	}

	if diff, diffErr := resolveDiffTarget(workDir, raw); diffErr == nil {
		return diff, nil
	}
	return Target{}, fileErr
}

func canonicalizeWorkDir(workDir string) (string, error) {
	if workDir == "" {
		var err error
		workDir, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve current directory: %w", err)
		}
	}
	workDirClean := filepath.Clean(workDir)
	if resolvedRoot, err := filepath.EvalSymlinks(workDirClean); err == nil {
		workDirClean = filepath.Clean(resolvedRoot)
	}
	return workDirClean, nil
}

// DiffTarget turns a request Value into a workspacediff.Target. Hosts re-resolve
// in workDir (no shell) so a crafted request cannot skip rev-parse. An
// unresolvable spec is still returned parsed — the leaf load reports the error.
func DiffTarget(workDir, value string) workspacediff.Target {
	spec, ok := workspacediff.ParseSpec(value)
	if !ok {
		return workspacediff.WorkingTreeTarget()
	}
	if workDir == "" {
		return spec
	}
	resolved, err := workspacediff.ResolveSpec(context.Background(), workDir, spec)
	if err != nil {
		return spec
	}
	return resolved
}

func resolveDiffTarget(workDir, raw string) (Target, error) {
	spec, ok := workspacediff.ParseSpec(raw)
	if !ok {
		return Target{}, fmt.Errorf("not a git spec: %q", raw)
	}
	resolved, err := workspacediff.ResolveSpec(context.Background(), workDir, spec)
	if err != nil {
		return Target{}, fmt.Errorf("unknown git object %q", raw)
	}
	ident := resolved.Identity()
	if ident == "" {
		return Target{}, fmt.Errorf("unknown git object %q", raw)
	}
	return Target{Kind: TargetKindDiff, Value: ident}, nil
}

func resolveFileTarget(workDir, raw string, explicitLine int) (Target, error) {
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
		absPath = filepath.Join(workDir, targetPath)
	}

	resolvedTarget, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return Target{}, fmt.Errorf("file %q does not exist: %w", targetPath, os.ErrNotExist)
		}
		return Target{}, fmt.Errorf("resolve target path %q: %w", targetPath, err)
	}
	resolvedTarget = filepath.Clean(resolvedTarget)

	info, err := os.Stat(resolvedTarget)
	if err != nil {
		if os.IsNotExist(err) {
			return Target{}, fmt.Errorf("file %q does not exist: %w", targetPath, os.ErrNotExist)
		}
		return Target{}, fmt.Errorf("stat target %q: %w", targetPath, err)
	}
	if info.IsDir() {
		return Target{}, fmt.Errorf("target %q is a directory, not a file", targetPath)
	}
	if !info.Mode().IsRegular() {
		return Target{}, fmt.Errorf("target %q is not a regular file", targetPath)
	}

	rel, err := filepath.Rel(workDir, resolvedTarget)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return Target{}, fmt.Errorf("target %q resolves outside workspace root %s", targetPath, workDir)
	}

	return Target{
		Kind:  TargetKindFile,
		Value: filepath.ToSlash(rel),
		Line:  line,
	}, nil
}
