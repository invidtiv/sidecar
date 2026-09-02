package reposervice

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/marcus/sidecar/internal/contentservice"
)

// MaxStatusFiles caps the changed-file rows one status answer may carry. A
// working tree with more changes than this is not navigable in a sidebar, and a
// truncated list the viewer can label beats a payload that pushes the call past
// the encoded cap and returns nothing at all.
const MaxStatusFiles = 5000

// StatusResult is the machine contract for `sidecar repo status --json`.
//
// NoRepository is a real answer rather than an error, so a viewer renders "that
// host's project is not a git repository" instead of its own no-repo view,
// which offers to run `git init` on this machine.
type StatusResult struct {
	Kind         string `json:"kind"`
	Workspace    string `json:"workspace"`
	NoRepository bool   `json:"noRepository,omitempty"`

	// Branch is empty exactly when Detached is true.
	Branch      string `json:"branch,omitempty"`
	Detached    bool   `json:"detached,omitempty"`
	Head        string `json:"head,omitempty"`
	HasUpstream bool   `json:"hasUpstream,omitempty"`
	Upstream    string `json:"upstream,omitempty"`
	Ahead       int    `json:"ahead,omitempty"`
	Behind      int    `json:"behind,omitempty"`

	// State names an in-progress operation: merge, rebase, cherry-pick, revert,
	// or bisect. Empty is an ordinary working tree.
	State string `json:"state,omitempty"`

	// RemoteURL is origin's URL. The viewer builds GitHub links from it: the URL
	// is the host's fact, opening it is the viewer's action.
	RemoteURL string `json:"remoteUrl,omitempty"`

	StashCount int          `json:"stashCount,omitempty"`
	Files      []StatusFile `json:"files,omitempty"`
	Truncated  bool         `json:"truncated,omitempty"`
}

// StatusFile is one changed path.
//
// One row per path, carrying all three senses, because that is what the path
// actually is: a file staged and then edited again is one file with two
// patches, and the viewer splits it into its two sidebar rows. The counts are
// per sense for the same reason — they come from two different diffs.
type StatusFile struct {
	Path    string `json:"path"`
	OldPath string `json:"oldPath,omitempty"`
	// Status is the porcelain letter: M, A, D, R, C, U, or ? for untracked.
	// It is the index letter when the path is staged, the worktree letter
	// otherwise, which is the sense the sidebar row means.
	Status    string `json:"status"`
	Staged    bool   `json:"staged,omitempty"`
	Unstaged  bool   `json:"unstaged,omitempty"`
	Untracked bool   `json:"untracked,omitempty"`

	StagedAdditions   int `json:"stagedAdditions,omitempty"`
	StagedDeletions   int `json:"stagedDeletions,omitempty"`
	UnstagedAdditions int `json:"unstagedAdditions,omitempty"`
	UnstagedDeletions int `json:"unstagedDeletions,omitempty"`
}

// ValidRemoteResult reports whether a decoded object is this verb's answer.
// A login banner has neither this kind nor a workspace id.
func (r StatusResult) ValidRemoteResult() bool {
	return r.Kind == KindStatus && strings.TrimSpace(r.Workspace) != ""
}

// Status reads one machine's repository state in a single round trip: branch,
// upstream, ahead/behind, in-progress state, origin's URL, the stash count, and
// the changed-file rows.
func (s *Service) Status(ctx context.Context, workspaceID string) (StatusResult, error) {
	r, ok, err := s.open(ctx, workspaceID)
	if err != nil {
		return StatusResult{}, err
	}
	result := StatusResult{Kind: KindStatus, Workspace: r.ID}
	if !ok {
		result.NoRepository = true
		return result, nil
	}

	// porcelain=v2 --branch answers branch, detachment, upstream, ahead/behind
	// and every changed path in one invocation; asking for them separately
	// would be four more subprocesses reporting four instants.
	out, err := s.git(ctx, r.Root, "status", "--porcelain=v2", "--branch", "-z", "--untracked-files=all")
	if err != nil {
		return StatusResult{}, contentservice.Internal("read repository status", err)
	}
	parseStatus(&result, out)
	result.State = r.state()

	if url, err := s.git(ctx, r.Root, "remote", "get-url", "origin"); err == nil {
		result.RemoteURL = strings.TrimSpace(string(url))
	}
	if stashes, err := s.git(ctx, r.Root, "stash", "list", "--format=%gd%x00%gs"); err == nil {
		result.StashCount = len(parseStashes(stashes, MaxStashes))
	}
	s.attachDiffStats(ctx, r, result.Files)
	return result, nil
}

// parseStatus fills branch headers and changed-file rows from
// `git status --porcelain=v2 --branch -z`.
func parseStatus(result *StatusResult, out []byte) {
	records := splitNUL(out)
	byPath := map[string]*StatusFile{}
	var files []*StatusFile
	add := func(f *StatusFile) {
		if existing, ok := byPath[f.Path]; ok {
			existing.Staged = existing.Staged || f.Staged
			existing.Unstaged = existing.Unstaged || f.Unstaged
			return
		}
		byPath[f.Path] = f
		files = append(files, f)
	}

	for i := 0; i < len(records); i++ {
		line := records[i]
		switch {
		case strings.HasPrefix(line, "# branch."):
			parseBranchHeader(result, line)
		case strings.HasPrefix(line, "1 "):
			if f := parseOrdinary(line); f != nil {
				add(f)
			}
		case strings.HasPrefix(line, "2 "):
			f := parseRenamed(line)
			if f == nil {
				continue
			}
			// In -z form the original path is its own record, which is what
			// makes a path containing a tab survive the wire.
			if i+1 < len(records) {
				i++
				f.OldPath = records[i]
			}
			add(f)
		case strings.HasPrefix(line, "u "):
			if f := parseUnmerged(line); f != nil {
				add(f)
			}
		case strings.HasPrefix(line, "? "):
			add(&StatusFile{
				Path:      strings.TrimPrefix(line, "? "),
				Status:    "?",
				Unstaged:  true,
				Untracked: true,
			})
		}
	}

	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	if len(files) > MaxStatusFiles {
		files = files[:MaxStatusFiles]
		result.Truncated = true
	}
	result.Files = make([]StatusFile, 0, len(files))
	for _, f := range files {
		result.Files = append(result.Files, *f)
	}
}

func parseBranchHeader(result *StatusResult, line string) {
	switch {
	case strings.HasPrefix(line, "# branch.oid "):
		oid := strings.TrimPrefix(line, "# branch.oid ")
		if oid != "(initial)" {
			result.Head = oid
		}
	case strings.HasPrefix(line, "# branch.head "):
		head := strings.TrimPrefix(line, "# branch.head ")
		if head == "(detached)" {
			result.Detached = true
			return
		}
		result.Branch = head
	case strings.HasPrefix(line, "# branch.upstream "):
		result.HasUpstream = true
		result.Upstream = strings.TrimPrefix(line, "# branch.upstream ")
	case strings.HasPrefix(line, "# branch.ab "):
		for _, field := range strings.Fields(strings.TrimPrefix(line, "# branch.ab ")) {
			n, err := strconv.Atoi(strings.TrimLeft(field, "+-"))
			if err != nil {
				continue
			}
			if strings.HasPrefix(field, "+") {
				result.Ahead = n
			} else {
				result.Behind = n
			}
		}
	}
}

// parseOrdinary reads "1 <XY> <sub> <mH> <mI> <mW> <hH> <hI> <path>".
func parseOrdinary(line string) *StatusFile {
	fields := strings.SplitN(line, " ", 9)
	if len(fields) < 9 || len(fields[1]) < 2 {
		return nil
	}
	xy := fields[1]
	file := &StatusFile{Path: fields[8]}
	if xy[0] != '.' && xy[0] != '?' {
		file.Staged = true
		file.Status = string(xy[0])
	}
	if xy[1] != '.' && xy[1] != '?' {
		file.Unstaged = true
		if !file.Staged {
			file.Status = string(xy[1])
		}
	}
	if file.Status == "" {
		return nil
	}
	return file
}

// parseRenamed reads "2 <XY> <sub> <mH> <mI> <mW> <hH> <hI> <X><score> <path>".
func parseRenamed(line string) *StatusFile {
	fields := strings.SplitN(line, " ", 10)
	if len(fields) < 10 || len(fields[1]) < 2 || fields[8] == "" {
		return nil
	}
	status := "R"
	if fields[8][0] == 'C' {
		status = "C"
	}
	return &StatusFile{
		Path:     fields[9],
		Status:   status,
		Staged:   fields[1][0] != '.',
		Unstaged: fields[1][1] != '.',
	}
}

// parseUnmerged reads "u <XY> <sub> <m1> <m2> <m3> <mW> <h1> <h2> <h3> <path>".
func parseUnmerged(line string) *StatusFile {
	fields := strings.SplitN(line, " ", 11)
	if len(fields) < 11 {
		return nil
	}
	return &StatusFile{Path: fields[10], Status: "U", Unstaged: true}
}

// attachDiffStats fills the per-sense +/- counts. Two numstat calls rather than
// one per file: a status with two hundred changed paths must still be one round
// trip.
func (s *Service) attachDiffStats(ctx context.Context, r repo, files []StatusFile) {
	byPath := make(map[string]*StatusFile, len(files))
	for i := range files {
		byPath[files[i].Path] = &files[i]
	}
	for _, staged := range []bool{true, false} {
		args := []string{"diff", "--numstat", "-z"}
		if staged {
			args = append(args, "--cached")
		}
		out, err := s.git(ctx, r.Root, args...)
		if err != nil {
			continue
		}
		for _, stat := range parseNumstat(out) {
			file, ok := byPath[stat.Path]
			if !ok {
				continue
			}
			if staged {
				file.StagedAdditions, file.StagedDeletions = stat.Additions, stat.Deletions
			} else {
				file.UnstagedAdditions, file.UnstagedDeletions = stat.Additions, stat.Deletions
			}
		}
	}
}

type numstatRow struct {
	Path      string
	Additions int
	Deletions int
}

// parseNumstat reads `git diff --numstat -z`. A rename or copy leaves the path
// field empty and follows with separate old and new NUL records.
func parseNumstat(out []byte) []numstatRow {
	records := splitNUL(out)
	rows := make([]numstatRow, 0, len(records))
	for i := 0; i < len(records); i++ {
		fields := strings.SplitN(records[i], "\t", 3)
		if len(fields) != 3 {
			continue
		}
		path := fields[2]
		if path == "" {
			if i+2 >= len(records) {
				continue
			}
			i += 2
			path = records[i]
		}
		// A binary file reports "-" for both counts; zero is the honest answer
		// for a line count that does not exist.
		additions, _ := strconv.Atoi(fields[0])
		deletions, _ := strconv.Atoi(fields[1])
		rows = append(rows, numstatRow{Path: path, Additions: additions, Deletions: deletions})
	}
	return rows
}
