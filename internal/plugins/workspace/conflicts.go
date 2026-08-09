package workspace

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

// conflictCandidate identifies a worktree for conflict detection.
type conflictCandidate struct{ Key, Name, Path string }

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
