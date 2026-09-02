package reposervice

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/marcus/sidecar/internal/contentservice"
)

// The machine-contract kinds for the `sidecar repo` verbs. A viewer pairs an
// answer with the request that produced it by kind, never by shape.
const (
	KindStatus  = "repo-status"
	KindDiff    = "repo-diff"
	KindHistory = "repo-history"
	KindCommit  = "repo-commit"
	KindRefs    = "repo-refs"
)

// Diff modes. The staging sense is always explicit: a viewer that asked for the
// unstaged patch and got the staged one would be wrong in the quietest way
// available, so nothing here infers a default.
const (
	ModeStaged    = "staged"
	ModeUnstaged  = "unstaged"
	ModeUntracked = "untracked"
	ModeCommit    = "commit"
)

// Repository states this service reports, in git's own precedence order. An
// empty state means an ordinary working tree.
const (
	StateMerge      = "merge"
	StateRebase     = "rebase"
	StateCherryPick = "cherry-pick"
	StateRevert     = "revert"
	StateBisect     = "bisect"
)

// Service is the host-side repository read service.
//
// Nil function fields use the production defaults. Tests inject fakes so a
// fixture does not have to be a real Sidecar state tree.
type Service struct {
	// Workspaces resolves a durable workspace id to its authoritative root.
	// Nil uses contentservice.Default(): which root a viewer is reading is one
	// rule, and a second resolver is how two verb families start disagreeing
	// about which directory a bound surface is showing.
	Workspaces *contentservice.Service

	// Git runs one read-only git invocation in dir and returns its stdout.
	// Nil uses the process default, which always passes --no-optional-locks.
	Git func(ctx context.Context, dir string, args ...string) ([]byte, error)
}

// Default returns a Service bound to this process's config and state.
func Default() *Service { return &Service{} }

func (s *Service) workspaces() *contentservice.Service {
	if s.Workspaces != nil {
		return s.Workspaces
	}
	return contentservice.Default()
}

// git runs one read-only git invocation. Stdout is returned even when git
// exits non-zero, because `git diff` reports differences with exit 1 and the
// patch is on stdout either way; callers that care inspect err.
func (s *Service) git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	if s.Git != nil {
		return s.Git(ctx, dir, args...)
	}
	return defaultGit(ctx, dir, args...)
}

func defaultGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", gitArgs(dir, args)...)
	return cmd.Output()
}

// gitArgs prefixes every invocation with --no-optional-locks. A viewer's read
// must never take .git/index.lock out from under a human staging a file on the
// host, and the flag only works before the subcommand, so it is applied in one
// place rather than at each call site.
func gitArgs(dir string, args []string) []string {
	return append([]string{"--no-optional-locks", "-C", dir}, args...)
}

// repo is a workspace that has been resolved and found to hold a repository.
type repo struct {
	ID     string
	Root   string
	GitDir string
}

// open resolves a workspace and locates its git directory.
//
// ok is false when the workspace exists but is not a git repository. That is a
// named answer, not an error: the viewer must say "[aerie] is not a git
// repository" rather than offer this machine's no-repo view, which runs
// `git init` here under a label that says aerie.
func (s *Service) open(ctx context.Context, workspaceID string) (r repo, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return repo{}, false, err
	}
	ws, err := s.workspaces().LookupWorkspace(ctx, workspaceID)
	if err != nil {
		return repo{}, false, err
	}
	out, err := s.git(ctx, ws.Root, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return repo{ID: ws.ID, Root: ws.Root}, false, nil
	}
	gitDir := strings.TrimSpace(string(out))
	if gitDir == "" {
		return repo{ID: ws.ID, Root: ws.Root}, false, nil
	}
	return repo{ID: ws.ID, Root: ws.Root, GitDir: gitDir}, true, nil
}

// state reports an in-progress operation, in git's own precedence order: a
// rebase that hit a conflict also leaves MERGE_HEAD behind, and calling that
// "merge" would tell the viewer the wrong way out of it.
func (r repo) state() string {
	exists := func(name string) bool {
		path := name
		if !filepath.IsAbs(path) {
			path = filepath.Join(r.GitDir, name)
		}
		_, err := os.Stat(path)
		return err == nil
	}
	switch {
	case exists("rebase-merge"), exists("rebase-apply"):
		return StateRebase
	case exists("CHERRY_PICK_HEAD"):
		return StateCherryPick
	case exists("REVERT_HEAD"):
		return StateRevert
	case exists("BISECT_LOG"):
		return StateBisect
	case exists("MERGE_HEAD"):
		return StateMerge
	}
	return ""
}

// requireHash refuses anything that is not a plain object name.
//
// The hash reaches git as an argument, so a value that could be read as an
// option (or as a path) is refused rather than escaped. A viewer always has a
// real hash from `repo history`; nothing else is a legitimate caller.
func requireHash(raw string) (string, error) {
	hash := strings.TrimSpace(raw)
	if hash == "" {
		return "", contentservice.Rejected("commit is required")
	}
	if len(hash) < 4 || len(hash) > 64 {
		return "", contentservice.Rejected("commit %q is not an object name", raw)
	}
	for _, r := range hash {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return "", contentservice.Rejected("commit %q is not an object name", raw)
		}
	}
	return hash, nil
}

// requirePath resolves a repository-relative path under the workspace root by
// contentservice's containment rule, and refuses one that could be read as a
// git option.
func requirePath(root, raw string) (string, error) {
	if strings.HasPrefix(strings.TrimSpace(raw), "-") {
		return "", contentservice.Rejected("path %q must be relative to the workspace root", raw)
	}
	rel, _, err := contentservice.ContainedRelative(root, raw)
	if err != nil {
		return "", err
	}
	return rel, nil
}

// splitNUL splits NUL-delimited git output, dropping the trailing empty record
// git leaves after the last one.
func splitNUL(out []byte) []string {
	parts := strings.Split(string(out), "\x00")
	for len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

func splitLines(out []byte) []string {
	text := strings.TrimRight(string(out), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}
