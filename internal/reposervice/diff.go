package reposervice

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/marcus/sidecar/internal/contentservice"
)

// MaxPatchBytes caps one patch. It sits well under the encoded cap so a patch
// plus its envelope still fits, and a patch larger than this is beyond what any
// diff pane shows without paging anyway. The cut is labelled rather than
// silent: a viewer that renders a short patch as if it were whole is lying
// about the change.
const MaxPatchBytes = 512 << 10

// DiffResult is the machine contract for `sidecar repo diff --json`.
//
// Patch is raw unified diff text. It is not parsed here: the viewer runs the
// same parser it runs on a local patch, so a host upgrade is never a rendering
// change.
type DiffResult struct {
	Kind         string `json:"kind"`
	Workspace    string `json:"workspace"`
	NoRepository bool   `json:"noRepository,omitempty"`

	Mode      string `json:"mode"`
	Path      string `json:"path"`
	Commit    string `json:"commit,omitempty"`
	Patch     string `json:"patch,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// ValidRemoteResult reports whether a decoded object is this verb's answer.
func (r DiffResult) ValidRemoteResult() bool {
	return r.Kind == KindDiff && strings.TrimSpace(r.Workspace) != ""
}

// Diff returns one working-tree patch for one path in one staging sense.
//
// The sense is always explicit. A staged and an unstaged change to the same
// path are two different patches, and answering with the wrong one would be a
// quiet, plausible lie about what the host's working tree holds.
func (s *Service) Diff(ctx context.Context, workspaceID, path, mode string) (DiffResult, error) {
	switch mode {
	case ModeStaged, ModeUnstaged, ModeUntracked:
	case "":
		return DiffResult{}, contentservice.Usage("mode is required")
	default:
		return DiffResult{}, contentservice.Usage("unknown diff mode %q", mode)
	}
	r, ok, err := s.open(ctx, workspaceID)
	if err != nil {
		return DiffResult{}, err
	}
	result := DiffResult{Kind: KindDiff, Workspace: r.ID, Mode: mode}
	if !ok {
		result.NoRepository = true
		return result, nil
	}
	rel, err := requirePath(r.Root, path)
	if err != nil {
		return DiffResult{}, err
	}
	result.Path = rel

	if mode == ModeUntracked {
		patch, err := s.untrackedPatch(ctx, r, rel)
		if err != nil {
			return DiffResult{}, err
		}
		result.Patch, result.Truncated = capPatch(patch)
		return result, nil
	}

	args := []string{"diff"}
	if mode == ModeStaged {
		args = append(args, "--cached")
	}
	args = append(args, "--", rel)
	// git diff reports differences with exit 1 and writes the patch to stdout
	// either way, so the output is the answer whether or not err is nil.
	out, _ := s.git(ctx, r.Root, args...)
	result.Patch, result.Truncated = capPatch(string(out))
	return result, nil
}

// CommitDiff returns one file's patch inside one commit.
//
// A merge is diffed against its first parent. git's combined diff for a clean
// merge is empty, which a viewer would render as "this commit did not touch
// this file" — the opposite of what happened.
func (s *Service) CommitDiff(ctx context.Context, workspaceID, hash, path string) (DiffResult, error) {
	r, ok, err := s.open(ctx, workspaceID)
	if err != nil {
		return DiffResult{}, err
	}
	result := DiffResult{Kind: KindDiff, Workspace: r.ID, Mode: ModeCommit}
	if !ok {
		result.NoRepository = true
		return result, nil
	}
	commit, err := requireHash(hash)
	if err != nil {
		return DiffResult{}, err
	}
	rel, err := requirePath(r.Root, path)
	if err != nil {
		return DiffResult{}, err
	}
	result.Commit, result.Path = commit, rel

	parents, err := s.parents(ctx, r, commit)
	if err != nil {
		return DiffResult{}, err
	}
	args := []string{"show", commit, "--", rel}
	if len(parents) > 1 {
		args = []string{"diff", parents[0], commit, "--", rel}
	}
	out, _ := s.git(ctx, r.Root, args...)
	result.Patch, result.Truncated = capPatch(normalizeCommitPatch(string(out)))
	return result, nil
}

func (s *Service) parents(ctx context.Context, r repo, hash string) ([]string, error) {
	out, err := s.git(ctx, r.Root, "show", "--format=%P", "-s", hash)
	if err != nil {
		return nil, contentservice.Rejected("commit %q is not in this repository", hash)
	}
	return strings.Fields(strings.TrimSpace(string(out))), nil
}

// untrackedPatch renders a file git is not tracking as the addition it is.
// --no-index against the null device produces a real git patch — including the
// binary verdict — rather than a hand-built one this package would have to keep
// in step with the viewer's parser.
func (s *Service) untrackedPatch(ctx context.Context, r repo, rel string) (string, error) {
	if info, err := os.Stat(filepath.Join(r.Root, rel)); err != nil || !info.Mode().IsRegular() {
		return "", contentservice.Rejected("path %q is not a readable file", rel)
	}
	// --no-index exits 1 when the two inputs differ, which is always.
	out, _ := s.git(ctx, r.Root, "diff", "--no-index", "--", os.DevNull, rel)
	return string(out), nil
}

// normalizeCommitPatch collapses a header-only `git show` result to the empty
// string. `git show <hash> -- <path>` prints the commit header whether or not
// the path matched, so a caller reading emptiness as "nothing changed here"
// would otherwise never see it.
func normalizeCommitPatch(out string) string {
	for _, line := range strings.Split(out, "\n") {
		// Covers every form git emits: diff --git, and diff --cc / --combined
		// for merges. Only a line start counts, which is what keeps a commit
		// message quoting a diff from registering: git indents message bodies.
		if strings.HasPrefix(line, "diff --") {
			return out
		}
	}
	return ""
}

func capPatch(patch string) (string, bool) {
	if len(patch) <= MaxPatchBytes {
		return patch, false
	}
	return shrinkUTF8(patch, MaxPatchBytes), true
}
