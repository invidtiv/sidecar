package gitstatus

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/reposervice"
)

// RepoSource is the one place this plugin reads repository state, and the whole
// of the local and remote difference.
//
// Everything above it — the file tree, the sidebar, the cursor, the diff
// parser, the renderer — runs on the answer and cannot tell which machine
// produced it. There is no remote FileTree, no second status model, and there
// must not be one.
type RepoSource interface {
	// Status is one repository read: the changed files, and the branch row
	// above them, as of one instant.
	Status(ctx context.Context) (RepoStatus, error)
}

// RepoStatus is that read.
//
// Push and State are filled by whichever source can answer them in the read it
// was already making. A host returns them with `repo status`, so a bound pane
// gets its branch row in the same round trip; locally they still arrive with
// the history load, exactly as they always have, because asking git for them
// here would be two more subprocesses on every refresh.
type RepoStatus struct {
	Tree  *FileTree
	Push  *PushStatus
	State string
}

// localRepoSource reads this machine's checkout.
//
// It runs the status load the plugin has always run, moved behind the seam
// rather than rewritten, and load stays the injectable function the existing
// tests drive.
type localRepoSource struct {
	root string
	load func(string) (*FileTree, error)
}

func (s localRepoSource) Status(context.Context) (RepoStatus, error) {
	load := s.load
	if load == nil {
		load = LoadFileTree
	}
	tree, err := load(s.root)
	if err != nil {
		return RepoStatus{}, err
	}
	return RepoStatus{Tree: tree}, nil
}

// remoteRepoTimeout bounds one status call. A read that outlives the keypress
// that asked for it is how a quit comes to take a minute (td-052329), so this
// is short and the refusal is honest rather than a hang.
const remoteRepoTimeout = 20 * time.Second

// remoteRepoSource reads a registered host's repository through `sidecar repo`,
// over the ssh connection Sessions already holds open. It never receives a path
// on this disk, and there is no code in it that could run git here.
type remoteRepoSource struct {
	hostID      string
	workspaceID string
	run         func(ctx context.Context, hostID string, args []string, out any) error
	timeout     time.Duration
}

func (s *remoteRepoSource) Status(ctx context.Context) (RepoStatus, error) {
	timeout := s.timeout
	if timeout <= 0 {
		timeout = remoteRepoTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var result reposervice.StatusResult
	args := []string{"repo", "status", "--workspace", s.workspaceID, "--json"}
	if err := s.run(ctx, s.hostID, args, &result); err != nil {
		return RepoStatus{}, s.classify(err)
	}
	if !result.ValidRemoteResult() {
		return RepoStatus{}, fmt.Errorf("%s did not answer repo status", s.hostID)
	}
	if result.NoRepository {
		return RepoStatus{}, &noRepositoryError{hostID: s.hostID}
	}
	return remoteRepoStatus(result), nil
}

// classify separates the host refusing to serve this workspace from the host
// failing to answer.
//
// Exit 5 is the host's considered answer — it will not open this workspace as a
// repository — and it must read as that rather than as a transport problem. The
// Git plugin composes a `:worktree:` id, which contentservice resolves through
// `git worktree list`, so a host project root without a .git is rejected here
// and never reaches the NoRepository flag.
func (s *remoteRepoSource) classify(err error) error {
	var runErr *hosts.RunError
	if errors.As(err, &runErr) && runErr.Failure == hosts.FailRejected {
		return &noRepositoryError{hostID: s.hostID, detail: runErr.Detail}
	}
	return err
}

// noRepositoryError is the host answering that the bound workspace is not a git
// repository, by either of the two paths that can say so.
//
// It exists so the plugin can tell that answer apart from a host failure and
// paint the host's sentence. What it must never become is this machine's
// no-repo view, which offers to run `git init` here, under a label that names
// another machine.
type noRepositoryError struct {
	hostID string
	detail string
}

func (e *noRepositoryError) Error() string {
	if strings.TrimSpace(e.detail) == "" {
		return fmt.Sprintf("[%s] is not a git repository", e.hostID)
	}
	return fmt.Sprintf("[%s] is not a git repository: %s", e.hostID, e.detail)
}

// remoteRepoStatus turns the host's answer into the model the sidebar already
// renders.
//
// The row split is the local tree's own rule (FileTree.addEntry): a path that is
// staged and then edited again is one file with two patches, so it becomes one
// staged row and one modified row, each carrying the counts for its own sense.
func remoteRepoStatus(result reposervice.StatusResult) RepoStatus {
	tree := &FileTree{}
	for _, file := range result.Files {
		if file.Untracked {
			tree.Untracked = append(tree.Untracked, &FileEntry{
				Path:     file.Path,
				Status:   StatusUntracked,
				Unstaged: true,
			})
			continue
		}
		if file.Staged {
			tree.Staged = append(tree.Staged, &FileEntry{
				Path:      file.Path,
				OldPath:   file.OldPath,
				Status:    FileStatus(file.Status),
				Staged:    true,
				Unstaged:  file.Unstaged,
				DiffStats: DiffStats{Additions: file.StagedAdditions, Deletions: file.StagedDeletions},
			})
		}
		if file.Unstaged {
			tree.Modified = append(tree.Modified, &FileEntry{
				Path:      file.Path,
				OldPath:   file.OldPath,
				Status:    FileStatus(file.Status),
				Unstaged:  true,
				DiffStats: DiffStats{Additions: file.UnstagedAdditions, Deletions: file.UnstagedDeletions},
			})
		}
	}
	sort.Slice(tree.Staged, func(i, j int) bool { return tree.Staged[i].Path < tree.Staged[j].Path })
	sort.Slice(tree.Modified, func(i, j int) bool { return tree.Modified[i].Path < tree.Modified[j].Path })
	sort.Slice(tree.Untracked, func(i, j int) bool { return tree.Untracked[i].Path < tree.Untracked[j].Path })
	tree.groupUntrackedFolders()

	return RepoStatus{
		Tree: tree,
		Push: &PushStatus{
			HasUpstream:    result.HasUpstream,
			UpstreamBranch: result.Upstream,
			Ahead:          result.Ahead,
			Behind:         result.Behind,
			DetachedHead:   result.Detached,
			CurrentBranch:  result.Branch,
		},
		State: result.State,
	}
}

// remoteRepoUnavailable is why a bound Git tab cannot read a host, or "" when it
// can.
//
// The three reasons are distinct on purpose: "not connected" is temporary and
// the user waits, "too old" is a host that needs updating, and "no workspace" is
// this viewer having nothing bound yet. One combined sentence would send the
// user looking in the wrong place for two of the three. The fourth reason — the
// bound workspace is not a repository — is not knowable until the host answers,
// so it arrives as noRepositoryError instead.
func remoteRepoUnavailable(hostID, workspaceID string, verbs hostproto.VerbCapabilities, connected bool) string {
	switch {
	case hostID == "":
		return ""
	case !connected:
		return fmt.Sprintf("[%s] is not connected", hostID)
	case !verbs.RepoReadV1:
		return fmt.Sprintf("[%s] runs a Sidecar that predates the repository contract (sidecar repo status)", hostID)
	case strings.TrimSpace(workspaceID) == "":
		return fmt.Sprintf("no worktree on [%s] is bound yet", hostID)
	}
	return ""
}

// repoStateLabel names an in-progress operation in the sidebar header. Empty for
// an ordinary working tree.
func repoStateLabel(state string) string {
	switch state {
	case reposervice.StateMerge:
		return "merging"
	case reposervice.StateRebase:
		return "rebasing"
	case reposervice.StateCherryPick:
		return "cherry-picking"
	case reposervice.StateRevert:
		return "reverting"
	case reposervice.StateBisect:
		return "bisecting"
	case "":
		return ""
	default:
		return state
	}
}
