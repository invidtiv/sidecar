package reposervice

import (
	"context"
	"regexp"
	"strconv"
	"strings"
)

const (
	// MaxBranches caps each of the local and remote branch lists.
	MaxBranches = 2000
	// MaxStashes caps the stash list.
	MaxStashes = 1000
)

// RefsResult is the machine contract for `sidecar repo refs --json`.
type RefsResult struct {
	Kind         string `json:"kind"`
	Workspace    string `json:"workspace"`
	NoRepository bool   `json:"noRepository,omitempty"`

	Branches       []Branch `json:"branches,omitempty"`
	RemoteBranches []Branch `json:"remoteBranches,omitempty"`
	Stashes        []Stash  `json:"stashes,omitempty"`
	Truncated      bool     `json:"truncated,omitempty"`
}

// Branch is one local or remote-tracking branch.
type Branch struct {
	Name      string `json:"name"`
	Current   bool   `json:"current,omitempty"`
	Remote    bool   `json:"remote,omitempty"`
	Upstream  string `json:"upstream,omitempty"`
	Ahead     int    `json:"ahead,omitempty"`
	Behind    int    `json:"behind,omitempty"`
	ShortHash string `json:"shortHash,omitempty"`
}

// Stash is one stash entry.
type Stash struct {
	Index   int    `json:"index"`
	Ref     string `json:"ref"`
	Branch  string `json:"branch,omitempty"`
	Message string `json:"message,omitempty"`
}

// ValidRemoteResult reports whether a decoded object is this verb's answer.
func (r RefsResult) ValidRemoteResult() bool {
	return r.Kind == KindRefs && strings.TrimSpace(r.Workspace) != ""
}

// branchFormat uses NUL between fields because a branch name may legitimately
// contain any of the punctuation a printable separator could have used.
const branchFormat = "%(refname:short)%00%(HEAD)%00%(upstream:short)%00%(upstream:track)%00%(objectname:short)"

// Refs lists local and remote-tracking branches and the stash. It is read-only:
// selecting a branch in a bound viewer lists it and refuses to switch to it.
func (s *Service) Refs(ctx context.Context, workspaceID string) (RefsResult, error) {
	r, ok, err := s.open(ctx, workspaceID)
	if err != nil {
		return RefsResult{}, err
	}
	result := RefsResult{Kind: KindRefs, Workspace: r.ID}
	if !ok {
		result.NoRepository = true
		return result, nil
	}

	if out, err := s.git(ctx, r.Root, "branch", "--format="+branchFormat); err == nil {
		result.Branches, result.Truncated = parseBranches(out, false, MaxBranches)
	}
	if out, err := s.git(ctx, r.Root, "branch", "--remotes", "--format="+branchFormat); err == nil {
		remotes, truncated := parseBranches(out, true, MaxBranches)
		result.RemoteBranches = remotes
		result.Truncated = result.Truncated || truncated
	}
	if out, err := s.git(ctx, r.Root, "stash", "list", "--format=%gd%x00%gs"); err == nil {
		result.Stashes = parseStashes(out, MaxStashes)
	}
	return result, nil
}

// trackPattern reads git's own tracking summary: [ahead N], [behind N], or
// [ahead N, behind M].
var trackPattern = regexp.MustCompile(`\[(?:ahead (\d+))?(?:, )?(?:behind (\d+))?\]`)

func parseBranches(out []byte, remote bool, max int) ([]Branch, bool) {
	lines := splitLines(out)
	truncated := false
	if len(lines) > max {
		lines = lines[:max]
		truncated = true
	}
	branches := make([]Branch, 0, len(lines))
	for _, line := range lines {
		fields := strings.Split(line, "\x00")
		if len(fields) < 5 || fields[0] == "" {
			continue
		}
		branch := Branch{
			Name:      fields[0],
			Current:   fields[1] == "*",
			Remote:    remote,
			Upstream:  fields[2],
			ShortHash: fields[4],
		}
		if matches := trackPattern.FindStringSubmatch(fields[3]); len(matches) == 3 {
			branch.Ahead, _ = strconv.Atoi(matches[1])
			branch.Behind, _ = strconv.Atoi(matches[2])
		}
		branches = append(branches, branch)
	}
	return branches, truncated
}

// stashMessagePattern pulls the branch out of git's own stash subject, which is
// "WIP on <branch>: <hash> <subject>" or "On <branch>: <message>".
var stashMessagePattern = regexp.MustCompile(`^(?:WIP )?[Oo]n ([^:]+): (.+)$`)

func parseStashes(out []byte, max int) []Stash {
	lines := splitLines(out)
	if len(lines) > max {
		lines = lines[:max]
	}
	stashes := make([]Stash, 0, len(lines))
	for _, line := range lines {
		fields := strings.SplitN(line, "\x00", 2)
		if len(fields) != 2 {
			continue
		}
		ref := fields[0]
		index, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(ref, "stash@{"), "}"))
		if err != nil {
			continue
		}
		stash := Stash{Index: index, Ref: ref, Message: fields[1]}
		if matches := stashMessagePattern.FindStringSubmatch(fields[1]); len(matches) == 3 {
			stash.Branch, stash.Message = matches[1], matches[2]
		}
		stashes = append(stashes, stash)
	}
	return stashes
}
