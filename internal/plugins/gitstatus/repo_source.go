package gitstatus

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
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

	// Diff is one patch: the change to one path, in the one sense the request
	// names.
	Diff(ctx context.Context, req DiffRequest) (RepoDiff, error)

	// History is one page of the commit log, newest first. A whole log is
	// never asked for: the viewer scrolls and asks for the next page.
	History(ctx context.Context, req HistoryRequest) (RepoHistory, error)

	// CommitDetail is one commit with its file list.
	CommitDetail(ctx context.Context, hash string) (*Commit, error)

	// Refs is the branch list the picker shows and the stash list, read-only.
	Refs(ctx context.Context) (RepoRefs, error)
}

// HistoryRequest is one page of the commit log.
//
// The position in the log is expressed twice because the two machines number
// one differently. A local walk takes an offset; a host takes the previous
// page's last hash, so a commit landing between two pages cannot silently
// repeat or skip a row (decision 9). The caller fills both from the list it
// already holds and neither source sees the other's.
//
// Author and Path are the host's own filters — git narrows the log before it
// is serialized either way. Subject search is not here: it runs in the viewer
// over the rows already in hand, which is what it does for a local project.
type HistoryRequest struct {
	Limit  int
	Cursor string
	Skip   int
	Author string
	Path   string
}

// RepoHistory is one page.
//
// Push is filled only by a source that answered the branch row in the read it
// was already making. A local history load asks git for it, exactly as it
// always has; a host answered it with `repo status` in the same refresh and
// stamps each row's Pushed itself, so a bound pane never pays for it twice.
type RepoHistory struct {
	Commits []*Commit
	Push    *PushStatus
}

// RepoRefs is what `repo refs` names: the branches a picker lists and the
// stash entries. Listing only — switching branches and touching the stash are
// writes, and this seam has none.
type RepoRefs struct {
	Branches []*Branch
	Stashes  []*Stash
}

// DiffRequest names exactly one patch.
//
// Mode is one of reposervice's mode strings and is never inferred: a staged and
// an unstaged change to the same path are two different patches, and answering
// with the wrong one is a quiet, plausible lie about the working tree. Commit
// and Parent apply only to ModeCommit; Parent is the first parent of a merge,
// which a local read needs because it diffs the commit itself, while a host
// resolves parents on its own side.
type DiffRequest struct {
	Path   string
	Mode   string
	Commit string
	Parent string
}

// RepoDiff is one patch as raw unified diff text, plus whether the source had
// to cut it. Nothing here parses: the viewer runs the same parser on a local
// and a host patch, so a host upgrade is never a rendering change.
type RepoDiff struct {
	Patch     string
	Truncated bool
}

// diffModeForRow is the one place a sidebar row's staging sense becomes a diff
// mode.
//
// The row decides, and only the row: a path that is staged and then edited
// again is two rows, each meaning its own patch, and a mode guessed from the
// path would answer half of them with the other one's change.
func diffModeForRow(status FileStatus, staged bool) string {
	switch {
	case status == StatusUntracked:
		return reposervice.ModeUntracked
	case staged:
		return reposervice.ModeStaged
	default:
		return reposervice.ModeUnstaged
	}
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
	root    string
	load    func(string) (*FileTree, error)
	history func(string, int) ([]*Commit, *PushStatus, error)
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

// Diff runs the patch read the plugin has always run, routed through the seam
// rather than rewritten: the same three functions, chosen by the same rule the
// call sites used, so a local pane renders the bytes it always did.
func (s localRepoSource) Diff(_ context.Context, req DiffRequest) (RepoDiff, error) {
	var (
		patch string
		err   error
	)
	switch req.Mode {
	case reposervice.ModeCommit:
		patch, err = GetCommitDiff(s.root, req.Commit, req.Path, req.Parent)
	case reposervice.ModeUntracked:
		patch, err = GetNewFileDiff(s.root, req.Path)
	case reposervice.ModeStaged, reposervice.ModeUnstaged:
		patch, err = GetDiff(s.root, req.Path, req.Mode == reposervice.ModeStaged)
	default:
		return RepoDiff{}, fmt.Errorf("unknown diff mode %q", req.Mode)
	}
	if err != nil {
		return RepoDiff{}, err
	}
	return RepoDiff{Patch: patch}, nil
}

// History runs the log read the plugin has always run: the first page, a later
// page by offset, and a filtered page are the same three functions chosen by
// the same rule the call sites used, so a local sidebar lists the commits it
// always did.
func (s localRepoSource) History(_ context.Context, req HistoryRequest) (RepoHistory, error) {
	var (
		commits []*Commit
		push    *PushStatus
		err     error
	)
	switch {
	case req.Author != "" || req.Path != "":
		commits, push, err = GetCommitHistoryFilteredWithPushStatus(s.root, HistoryFilterOpts{
			Author: req.Author,
			Path:   req.Path,
			Limit:  req.Limit,
			Skip:   req.Skip,
		})
	case req.Skip > 0:
		commits, push, err = GetCommitHistoryWithPushStatusOffset(s.root, req.Limit, req.Skip)
	default:
		load := s.history
		if load == nil {
			load = GetCommitHistoryWithPushStatus
		}
		commits, push, err = load(s.root, req.Limit)
	}
	if err != nil {
		return RepoHistory{}, err
	}
	return RepoHistory{Commits: commits, Push: push}, nil
}

func (s localRepoSource) CommitDetail(_ context.Context, hash string) (*Commit, error) {
	return GetCommitDetail(s.root, hash)
}

func (s localRepoSource) Refs(context.Context) (RepoRefs, error) {
	branches, err := GetBranches(s.root)
	if err != nil {
		return RepoRefs{}, err
	}
	// A repository with no stashes is not an error, and GetStashList already
	// answers one that way.
	stashes, err := GetStashList(s.root)
	if err != nil {
		return RepoRefs{}, err
	}
	return RepoRefs{Branches: branches, Stashes: stashes.Stashes}, nil
}

// remoteRepoTimeout bounds one host read. A read that outlives the keypress
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
	ctx, cancel := context.WithTimeout(ctx, s.callTimeout())
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

// Diff asks the host for one patch, in the sense the row the cursor is on
// means.
//
// --mode is required by the verb and carried from the row rather than derived
// here, and the answer's mode is checked against the request: a host that
// replied with the other side of an MM path would be wrong in exactly the way
// no test would notice, because both answers are plausible patches for the
// path.
func (s *remoteRepoSource) Diff(ctx context.Context, req DiffRequest) (RepoDiff, error) {
	ctx, cancel := context.WithTimeout(ctx, s.callTimeout())
	defer cancel()

	args := []string{"repo", "diff", "--workspace", s.workspaceID, "--path", req.Path, "--mode", req.Mode}
	if req.Mode == reposervice.ModeCommit {
		args = append(args, "--commit", req.Commit)
	}
	args = append(args, "--json")

	var result reposervice.DiffResult
	if err := s.run(ctx, s.hostID, args, &result); err != nil {
		// A rejected patch is about this path or this commit, not about the
		// repository: the status read is what answers whether the workspace is
		// one, and a refused row must not be reported as if it were not.
		var runErr *hosts.RunError
		if errors.As(err, &runErr) && runErr.Failure == hosts.FailRejected {
			return RepoDiff{}, fmt.Errorf("[%s] will not serve the %s patch for %s: %s", s.hostID, req.Mode, req.Path, runErr.Detail)
		}
		return RepoDiff{}, err
	}
	if !result.ValidRemoteResult() {
		return RepoDiff{}, fmt.Errorf("%s did not answer repo diff", s.hostID)
	}
	if result.NoRepository {
		return RepoDiff{}, &noRepositoryError{hostID: s.hostID}
	}
	if result.Mode != req.Mode {
		return RepoDiff{}, fmt.Errorf("%s answered the %s patch for a %s row", s.hostID, result.Mode, req.Mode)
	}
	return RepoDiff{Patch: result.Patch, Truncated: result.Truncated}, nil
}

// History asks the host for one page of its log.
//
// --cursor rather than an offset is the host's contract and the reason paging
// is honest across a commit landing mid-scroll; the viewer sends the last hash
// it holds and gets the rows after it. Each row carries the host's own pushed
// state, so nothing here asks a second time what the upstream already knows.
func (s *remoteRepoSource) History(ctx context.Context, req HistoryRequest) (RepoHistory, error) {
	ctx, cancel := context.WithTimeout(ctx, s.callTimeout())
	defer cancel()

	args := []string{"repo", "history", "--workspace", s.workspaceID}
	if req.Limit > 0 {
		args = append(args, "--limit", strconv.Itoa(req.Limit))
	}
	if req.Cursor != "" {
		args = append(args, "--cursor", req.Cursor)
	}
	if req.Author != "" {
		args = append(args, "--author", req.Author)
	}
	if req.Path != "" {
		args = append(args, "--path", req.Path)
	}
	args = append(args, "--json")

	var result reposervice.HistoryResult
	if err := s.run(ctx, s.hostID, args, &result); err != nil {
		// A rejected page is about this query — a filter or a cursor the host
		// will not take — not about the repository. The status read is what
		// answers whether the workspace is one.
		var runErr *hosts.RunError
		if errors.As(err, &runErr) && runErr.Failure == hosts.FailRejected {
			return RepoHistory{}, fmt.Errorf("[%s] will not serve this page of history: %s", s.hostID, runErr.Detail)
		}
		return RepoHistory{}, err
	}
	if !result.ValidRemoteResult() {
		return RepoHistory{}, fmt.Errorf("%s did not answer repo history", s.hostID)
	}
	if result.NoRepository {
		return RepoHistory{}, &noRepositoryError{hostID: s.hostID}
	}
	// Push stays nil: `repo status` answered the branch row in this same
	// refresh, and a second answer here would be a second round trip that could
	// disagree with the one already on screen.
	return RepoHistory{Commits: remoteCommitRows(result.Commits)}, nil
}

func (s *remoteRepoSource) CommitDetail(ctx context.Context, hash string) (*Commit, error) {
	ctx, cancel := context.WithTimeout(ctx, s.callTimeout())
	defer cancel()

	args := []string{"repo", "commit", "--workspace", s.workspaceID, "--commit", hash, "--json"}
	var result reposervice.CommitResult
	if err := s.run(ctx, s.hostID, args, &result); err != nil {
		var runErr *hosts.RunError
		if errors.As(err, &runErr) && runErr.Failure == hosts.FailRejected {
			return nil, fmt.Errorf("[%s] will not serve commit %s: %s", s.hostID, hash, runErr.Detail)
		}
		return nil, err
	}
	if !result.ValidRemoteResult() {
		return nil, fmt.Errorf("%s did not answer repo commit", s.hostID)
	}
	if result.NoRepository {
		return nil, &noRepositoryError{hostID: s.hostID}
	}
	return remoteCommitDetail(result.Commit), nil
}

// Refs lists the host's branches and stashes. A rejection here is about the
// workspace rather than about a query, because this verb takes none.
func (s *remoteRepoSource) Refs(ctx context.Context) (RepoRefs, error) {
	ctx, cancel := context.WithTimeout(ctx, s.callTimeout())
	defer cancel()

	args := []string{"repo", "refs", "--workspace", s.workspaceID, "--json"}
	var result reposervice.RefsResult
	if err := s.run(ctx, s.hostID, args, &result); err != nil {
		return RepoRefs{}, s.classify(err)
	}
	if !result.ValidRemoteResult() {
		return RepoRefs{}, fmt.Errorf("%s did not answer repo refs", s.hostID)
	}
	if result.NoRepository {
		return RepoRefs{}, &noRepositoryError{hostID: s.hostID}
	}
	return remoteRepoRefs(result), nil
}

func (s *remoteRepoSource) callTimeout() time.Duration {
	if s.timeout > 0 {
		return s.timeout
	}
	return remoteRepoTimeout
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

// remoteCommitRows turns the host's log page into the rows the sidebar and the
// graph already draw.
//
// An empty page stays nil rather than becoming an empty slice: the loaded
// handler reads nil as "the source said nothing new", which is what a local
// page with no rows means too.
func remoteCommitRows(rows []reposervice.CommitRow) []*Commit {
	var commits []*Commit
	for _, row := range rows {
		commits = append(commits, &Commit{
			Hash:        row.Hash,
			ShortHash:   row.ShortHash,
			Author:      row.Author,
			AuthorEmail: row.AuthorEmail,
			// The instant is the host's; showing it in this viewer's zone is
			// what the local path does with its own commits.
			Date:         row.Date.Local(),
			Subject:      row.Subject,
			ParentHashes: row.Parents,
			IsMerge:      row.Merge,
			Pushed:       row.Pushed,
		})
	}
	return commits
}

// remoteCommitDetail turns one host commit into the model the preview pane
// renders. The aggregate stats are summed here because the host answers the
// file rows and the viewer is what displays a total.
func remoteCommitDetail(detail *reposervice.CommitDetail) *Commit {
	if detail == nil {
		return nil
	}
	commit := &Commit{
		Hash:         detail.Hash,
		ShortHash:    detail.ShortHash,
		Author:       detail.Author,
		AuthorEmail:  detail.AuthorEmail,
		Date:         detail.Date.Local(),
		Subject:      detail.Subject,
		Body:         detail.Body,
		ParentHashes: detail.Parents,
		IsMerge:      detail.Merge,
	}
	for _, file := range detail.Files {
		commit.Files = append(commit.Files, CommitFile{
			Path:      file.Path,
			OldPath:   file.OldPath,
			Status:    FileStatus(file.Status),
			Additions: file.Additions,
			Deletions: file.Deletions,
		})
		commit.Stats.FilesChanged++
		commit.Stats.Additions += file.Additions
		commit.Stats.Deletions += file.Deletions
	}
	return commit
}

// remoteRepoRefs keeps the branch picker's list to the host's local branches,
// which is the list a local picker shows. Remote-tracking branches are the
// host's own remotes and belong to a surface that offers to check one out.
func remoteRepoRefs(result reposervice.RefsResult) RepoRefs {
	refs := RepoRefs{}
	for _, branch := range result.Branches {
		refs.Branches = append(refs.Branches, &Branch{
			Name:       branch.Name,
			IsCurrent:  branch.Current,
			IsRemote:   branch.Remote,
			Upstream:   branch.Upstream,
			Ahead:      branch.Ahead,
			Behind:     branch.Behind,
			LastCommit: branch.ShortHash,
		})
	}
	for _, stash := range result.Stashes {
		refs.Stashes = append(refs.Stashes, &Stash{
			Index:   stash.Index,
			Ref:     stash.Ref,
			Branch:  stash.Branch,
			Message: stash.Message,
		})
	}
	return refs
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
