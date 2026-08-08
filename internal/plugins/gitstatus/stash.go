package gitstatus

import (
	"bufio"
	"bytes"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Stash represents a single stash entry.
type Stash struct {
	Index   int    // stash index (0 = most recent)
	Ref     string // stash@{0}, stash@{1}, etc.
	Branch  string // Branch the stash was created on
	Message string // Stash message
}

// StashList represents the list of stashes.
type StashList struct {
	Stashes []*Stash
}

// GetStashList retrieves the list of stashes.
func GetStashList(workDir string) (*StashList, error) {
	cmd := gitReadOnly("stash", "list", "--format=%gd|%gs")
	cmd.Dir = workDir
	output, err := cmd.Output()
	if err != nil {
		// No stashes is not an error
		return &StashList{}, nil
	}

	list := &StashList{}
	scanner := bufio.NewScanner(bytes.NewReader(output))

	// Pattern: stash@{n}|message
	// Message format: "WIP on branch: hash message" or "On branch: message"
	re := regexp.MustCompile(`^stash@\{(\d+)\}\|(.+)$`)
	branchRe := regexp.MustCompile(`^(?:WIP )?[Oo]n ([^:]+): (.+)$`)

	for scanner.Scan() {
		line := scanner.Text()
		matches := re.FindStringSubmatch(line)
		if len(matches) != 3 {
			continue
		}

		idx, err := strconv.Atoi(matches[1])
		if err != nil {
			continue
		}

		stash := &Stash{
			Index: idx,
			Ref:   "stash@{" + matches[1] + "}",
		}

		// Parse the message for branch name
		msgPart := matches[2]
		branchMatches := branchRe.FindStringSubmatch(msgPart)
		if len(branchMatches) == 3 {
			stash.Branch = branchMatches[1]
			stash.Message = branchMatches[2]
		} else {
			stash.Message = msgPart
		}

		list.Stashes = append(list.Stashes, stash)
	}

	return list, nil
}

// StashPush creates a new stash with all changes.
func StashPush(workDir string) error {
	cmd := exec.Command("git", "stash", "push")
	cmd.Dir = workDir
	return cmd.Run()
}

// StashPop pops the most recent stash.
func StashPop(workDir string) error {
	cmd := exec.Command("git", "stash", "pop")
	cmd.Dir = workDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return &StashError{Output: string(output), Err: err}
	}
	return nil
}

// StashApply applies a stash without removing it.
func StashApply(workDir, ref string) error {
	cmd := exec.Command("git", "stash", "apply", ref)
	cmd.Dir = workDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return &StashError{Output: string(output), Err: err}
	}
	return nil
}

// StashError wraps a git stash error with its output.
type StashError struct {
	Output string
	Err    error
}

func (e *StashError) Error() string {
	return strings.TrimSpace(e.Output)
}

// Count returns the number of stashes.
func (l *StashList) Count() int {
	if l == nil {
		return 0
	}
	return len(l.Stashes)
}
