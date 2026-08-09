package workspace

import (
	"context"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// Conflict represents a file conflict between worktrees.
type Conflict struct {
	Worktrees []string // Stable keys of worktrees with overlapping dirty files
	Files     []string // List of conflicting files
}

// ConflictsDetectedMsg signals that conflicts have been detected.
type ConflictsDetectedMsg struct {
	OperationScope
	Conflicts []Conflict
	Err       error
}

// detectConflicts scans all worktrees for files modified in multiple worktrees.
// Returns a list of conflicts where each conflict indicates which worktrees
// are modifying the same files.
type conflictCandidate struct{ Key, Name, Path string }

func detectConflicts(worktrees []conflictCandidate) []Conflict {
	if len(worktrees) < 2 {
		return nil // Need at least 2 worktrees for conflicts
	}

	// Build a map of modified files per worktree
	filesByWorktree := make(map[string][]string)
	names := make(map[string]string, len(worktrees))
	for _, wt := range worktrees {
		changes, _ := collectWorktreeChanges(context.Background(), wt.Path, nil)
		if changes.Err != nil {
			continue
		}
		names[wt.Key] = wt.Name
		if len(changes.Dirty) > 0 {
			filesByWorktree[wt.Key] = changes.Dirty
		}
	}

	// Find overlaps between worktrees
	var conflicts []Conflict
	worktreeNames := make([]string, 0, len(filesByWorktree))
	for name := range filesByWorktree {
		worktreeNames = append(worktreeNames, name)
	}

	for i := 0; i < len(worktreeNames); i++ {
		for j := i + 1; j < len(worktreeNames); j++ {
			wt1 := worktreeNames[i]
			wt2 := worktreeNames[j]

			overlap := intersection(filesByWorktree[wt1], filesByWorktree[wt2])
			if len(overlap) > 0 {
				conflicts = append(conflicts, Conflict{
					Worktrees: []string{names[wt1], names[wt2]},
					Files:     overlap,
				})
			}
		}
	}

	return conflicts
}

func detectConflictsFromChanges(worktrees []*Worktree) []Conflict {
	candidates := make([]conflictCandidate, 0, len(worktrees))
	filesByKey := make(map[string][]string, len(worktrees))
	for _, wt := range worktrees {
		key := wt.IdentityKey()
		candidates = append(candidates, conflictCandidate{Key: key, Name: wt.Name, Path: wt.Path})
		if wt.Changes != nil && wt.Changes.Err == nil {
			filesByKey[key] = wt.Changes.Dirty
		}
	}
	var result []Conflict
	for i := 0; i < len(candidates); i++ {
		for j := i + 1; j < len(candidates); j++ {
			overlap := intersection(filesByKey[candidates[i].Key], filesByKey[candidates[j].Key])
			if len(overlap) > 0 {
				result = append(result, Conflict{Worktrees: []string{candidates[i].Key, candidates[j].Key}, Files: overlap})
			}
		}
	}
	return result
}

// loadConflicts returns a command to detect conflicts across worktrees.
func (p *Plugin) loadConflicts() tea.Cmd {
	_, scope := p.newOperationScope(nil)
	candidates := make([]conflictCandidate, 0, len(p.worktrees))
	for _, wt := range p.worktrees {
		candidates = append(candidates, conflictCandidate{Key: wt.IdentityKey(), Name: wt.Name, Path: wt.Path})
	}
	return func() tea.Msg {
		conflicts := detectConflicts(candidates)
		return ConflictsDetectedMsg{OperationScope: scope, Conflicts: conflicts}
	}
}

// getModifiedFiles returns a list of files modified in the worktree.
// This includes both staged and unstaged changes.
func getModifiedFiles(workdir string) ([]string, error) {
	// Get list of modified files (staged + unstaged) relative to HEAD
	cmd := exec.Command("git", "diff", "--name-only", "HEAD")
	cmd.Dir = workdir
	output, err := cmd.Output()
	if err != nil {
		// No HEAD yet or other error, try just diff
		cmd = exec.Command("git", "diff", "--name-only")
		cmd.Dir = workdir
		output, _ = cmd.Output()
	}

	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}

	// Also get untracked files that are new
	cmd = exec.Command("git", "ls-files", "--others", "--exclude-standard")
	cmd.Dir = workdir
	untrackedOutput, err := cmd.Output()
	if err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(untrackedOutput)), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				files = append(files, line)
			}
		}
	}

	return files, nil
}

// intersection returns the common elements between two slices.
func intersection(a, b []string) []string {
	set := make(map[string]bool)
	for _, item := range a {
		set[item] = true
	}

	var result []string
	for _, item := range b {
		if set[item] {
			result = append(result, item)
		}
	}
	return result
}

// hasConflict checks if a worktree has any conflicts.
func (p *Plugin) hasConflict(worktreeKey string, conflicts []Conflict) bool {
	for _, c := range conflicts {
		for _, wt := range c.Worktrees {
			if wt == worktreeKey {
				return true
			}
		}
	}
	return false
}

// getConflictingFiles returns the files that conflict for a specific worktree.
func (p *Plugin) getConflictingFiles(worktreeKey string, conflicts []Conflict) []string {
	var files []string
	fileSet := make(map[string]bool)

	for _, c := range conflicts {
		for _, wt := range c.Worktrees {
			if wt == worktreeKey {
				for _, f := range c.Files {
					if !fileSet[f] {
						fileSet[f] = true
						files = append(files, f)
					}
				}
				break
			}
		}
	}
	return files
}

// getConflictingWorktrees returns the names of other worktrees that conflict.
func (p *Plugin) getConflictingWorktrees(worktreeKey string, conflicts []Conflict) []string {
	var others []string
	otherSet := make(map[string]bool)

	for _, c := range conflicts {
		hasThis := false
		for _, wt := range c.Worktrees {
			if wt == worktreeKey {
				hasThis = true
				break
			}
		}
		if hasThis {
			for _, wt := range c.Worktrees {
				if wt != worktreeKey && !otherSet[wt] {
					otherSet[wt] = true
					others = append(others, wt)
				}
			}
		}
	}
	return others
}
