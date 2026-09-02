package gitstatus

import (
	"context"
	"errors"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/reposervice"
)

func (p *Plugin) nextPreviewID() uint64 {
	p.nextPreviewRequestID++
	return p.nextPreviewRequestID
}

// errNoRepoSource is a patch asked for when there is no repository to read.
// It reaches the pane as a load failure rather than as an empty patch, which
// would render as "nothing changed here".
var errNoRepoSource = errors.New("no repository is available to read")

// fetchPatch is the one place a patch is read.
//
// Every diff surface goes through it, so which machine answers is decided once,
// at the seam, and neither the inline pane nor the full-screen view can drift
// into reading the world directly.
func (p *Plugin) fetchPatch(req DiffRequest) func() (RepoDiff, error) {
	source := p.repoSource()
	return func() (RepoDiff, error) {
		if source == nil {
			return RepoDiff{}, errNoRepoSource
		}
		return source.Diff(context.Background(), req)
	}
}

// loadDiff loads the diff for a file.
func (p *Plugin) loadDiff(path string, staged bool, status FileStatus) tea.Cmd {
	requestID := p.nextPreviewID()
	p.fullScreenPreviewRequestID = requestID
	epoch := p.ctx.Epoch
	fetch := p.fetchPatch(DiffRequest{Path: path, Mode: diffModeForRow(status, staged)})
	return func() tea.Msg {
		diff, err := fetch()
		if err != nil {
			return DiffLoadedMsg{Epoch: epoch, RequestID: requestID, Err: err}
		}

		return DiffLoadedMsg{Epoch: epoch, RequestID: requestID, Content: diff.Patch, Raw: diff.Patch, Truncated: diff.Truncated}
	}
}

// loadInlineDiff loads a diff for inline preview in the three-pane view.
func (p *Plugin) loadInlineDiff(path string, staged bool, status FileStatus) tea.Cmd {
	requestID := p.nextPreviewID()
	p.inlinePreviewRequestID = requestID
	epoch := p.ctx.Epoch
	fetch := p.fetchPatch(DiffRequest{Path: path, Mode: diffModeForRow(status, staged)})
	return func() tea.Msg {
		diff, err := fetch()
		if err != nil {
			return InlineDiffLoadedMsg{Epoch: epoch, RequestID: requestID, File: path, Staged: staged, Raw: "", Parsed: nil}
		}
		parsed, _ := ParseUnifiedDiff(diff.Patch)
		return InlineDiffLoadedMsg{Epoch: epoch, RequestID: requestID, File: path, Staged: staged, Raw: diff.Patch, Parsed: parsed, Truncated: diff.Truncated}
	}
}

// loadRecentCommits loads recent commits for the sidebar with push status.
// Also kicks off a separate total-commit-count load so a slow rev-list on a
// huge monorepo cannot delay the commit list paint.
func (p *Plugin) loadRecentCommits() tea.Cmd {
	if p.ctx != nil && p.ctx.HostID != "" {
		return nil
	}
	if p.repoRoot == "" {
		return nil
	}
	if p.activeHistoryRequestID != 0 {
		p.historyRefreshDirty = true
		return nil
	}
	p.nextHistoryRequestID++
	requestID := p.nextHistoryRequestID
	p.activeHistoryRequestID = requestID
	epoch := p.ctx.Epoch
	workDir := p.repoRoot
	loader := p.historyLoader
	if loader == nil {
		loader = GetCommitHistoryWithPushStatus
	}
	historyCmd := func() tea.Msg {
		commits, pushStatus, err := loader(workDir, commitHistoryPageSize)
		if err != nil {
			return RecentCommitsLoadedMsg{Epoch: epoch, RequestID: requestID, Err: err}
		}
		return RecentCommitsLoadedMsg{Epoch: epoch, RequestID: requestID, Commits: commits, PushStatus: pushStatus}
	}
	return tea.Batch(historyCmd, p.loadCommitCount())
}

// loadCommitCount fetches total commits reachable from HEAD in the background.
// Single-flighted with a request ID so out-of-order completions cannot apply a
// stale total; coalesces follow-ups via countRefreshDirty (same pattern as history).
func (p *Plugin) loadCommitCount() tea.Cmd {
	if p.repoRoot == "" {
		return nil
	}
	if p.activeCountRequestID != 0 {
		p.countRefreshDirty = true
		return nil
	}
	p.nextCountRequestID++
	requestID := p.nextCountRequestID
	p.activeCountRequestID = requestID
	epoch := p.ctx.Epoch
	workDir := p.repoRoot
	return func() tea.Msg {
		n, err := GetCommitCount(workDir)
		if err != nil {
			return CommitCountLoadedMsg{Epoch: epoch, RequestID: requestID, OK: false}
		}
		return CommitCountLoadedMsg{Epoch: epoch, RequestID: requestID, Count: n, OK: true}
	}
}

// loadMoreCommits fetches the next batch of commits for infinite scroll.
func (p *Plugin) loadMoreCommits() tea.Cmd {
	if p.loadingMoreCommits {
		return nil
	}
	p.loadingMoreCommits = true

	epoch := p.ctx.Epoch
	workDir := p.repoRoot
	skip := len(p.recentCommits)
	return func() tea.Msg {
		commits, pushStatus, err := GetCommitHistoryWithPushStatusOffset(workDir, commitHistoryPageSize, skip)
		if err != nil {
			return MoreCommitsLoadedMsg{Epoch: epoch, Commits: nil, PushStatus: nil}
		}
		return MoreCommitsLoadedMsg{Epoch: epoch, Commits: commits, PushStatus: pushStatus}
	}
}

// loadFilteredCommits fetches commits with current filter options.
func (p *Plugin) loadFilteredCommits() tea.Cmd {
	epoch := p.ctx.Epoch
	workDir := p.repoRoot
	opts := HistoryFilterOpts{
		Author: p.historyFilterAuthor,
		Path:   p.historyFilterPath,
		Limit:  50,
	}
	return func() tea.Msg {
		commits, pushStatus, err := GetCommitHistoryFilteredWithPushStatus(workDir, opts)
		if err != nil {
			return FilteredCommitsLoadedMsg{Epoch: epoch, Commits: nil, PushStatus: nil}
		}
		return FilteredCommitsLoadedMsg{Epoch: epoch, Commits: commits, PushStatus: pushStatus}
	}
}

// loadFolderDiff loads a concatenated diff for all files in a folder.
//
// A folder row is an aggregate of this machine's files, not one repository
// read, so it stays local. A bound pane says so instead of turning one cursor
// move into one round trip per file in the folder; the files inside still read
// their own patches through the seam.
func (p *Plugin) loadFolderDiff(entry *FileEntry) tea.Cmd {
	if p.remoteBound() {
		return nil
	}
	requestID := p.nextPreviewID()
	p.inlinePreviewRequestID = requestID
	epoch := p.ctx.Epoch
	workDir := p.repoRoot
	folderPath := entry.Path
	children := entry.Children
	return func() tea.Msg {
		rawDiff, err := GetFolderDiff(workDir, children)
		if err != nil {
			return InlineDiffLoadedMsg{Epoch: epoch, RequestID: requestID, File: folderPath, Raw: "", Parsed: nil}
		}
		parsed, _ := ParseUnifiedDiff(rawDiff)
		return InlineDiffLoadedMsg{Epoch: epoch, RequestID: requestID, File: folderPath, Raw: rawDiff, Parsed: parsed}
	}
}

// loadFullFolderDiff loads a concatenated diff for full-screen view. It is the
// same local aggregate loadFolderDiff is, and a bound pane does not open it.
func (p *Plugin) loadFullFolderDiff(entry *FileEntry) tea.Cmd {
	if p.remoteBound() {
		return nil
	}
	requestID := p.nextPreviewID()
	p.fullScreenPreviewRequestID = requestID
	epoch := p.ctx.Epoch
	workDir := p.repoRoot
	children := entry.Children
	return func() tea.Msg {
		rawDiff, err := GetFolderDiff(workDir, children)
		if err != nil {
			return DiffLoadedMsg{Epoch: epoch, RequestID: requestID, Err: err}
		}

		return DiffLoadedMsg{Epoch: epoch, RequestID: requestID, Content: rawDiff, Raw: rawDiff}
	}
}

// loadCommitFileDiff loads diff for a file in a commit.
// parentHash should be the first parent hash for merge commits, or "" for regular commits.
func (p *Plugin) loadCommitFileDiff(hash, path, parentHash string) tea.Cmd {
	requestID := p.nextPreviewID()
	p.fullScreenPreviewRequestID = requestID
	epoch := p.ctx.Epoch
	fetch := p.fetchPatch(DiffRequest{Path: path, Mode: reposervice.ModeCommit, Commit: hash, Parent: parentHash})
	return func() tea.Msg {
		diff, err := fetch()
		if err != nil {
			return DiffLoadedMsg{Epoch: epoch, RequestID: requestID, Err: err}
		}

		return DiffLoadedMsg{Epoch: epoch, RequestID: requestID, Content: diff.Patch, Raw: diff.Patch, Truncated: diff.Truncated}
	}
}

// loadFullFileDiff loads the full file content (old + new) for full-file diff view.
// forInline indicates whether this is for the inline diff pane or the full-screen diff view.
//
// A full file is not a patch: it needs the file's contents on both sides of the
// change, and no `sidecar repo` verb answers those. A bound pane therefore does
// not load one, and the diff pane says why rather than waiting on a read that
// will never arrive.
func (p *Plugin) loadFullFileDiff(path string, staged bool, status FileStatus, commitHash string, forInline bool) tea.Cmd {
	if p.remoteBound() {
		return nil
	}
	requestID := p.nextPreviewID()
	if forInline {
		p.inlineFullFileRequestID = requestID
	} else {
		p.fullScreenFileRequestID = requestID
	}
	epoch := p.ctx.Epoch
	workDir := p.repoRoot
	return func() tea.Msg {
		var oldContent, newContent, rawDiff string

		if commitHash != "" {
			// Viewing a file in a commit: old = parent, new = commit
			oldContent, _ = GetFileContentAtRef(workDir, path, commitHash+"~1")
			newContent, _ = GetFileContentAtRef(workDir, path, commitHash)
			rawDiff, _ = GetCommitDiff(workDir, commitHash, path, "")
		} else if status == StatusUntracked {
			// Untracked file: old is empty, new is working tree
			oldContent = ""
			newContent, _ = GetWorkingTreeFileContent(workDir, path)
			rawDiff, _ = GetNewFileDiff(workDir, path)
		} else if staged {
			// Staged file: old = HEAD, new = index
			oldContent, _ = GetFileContentAtRef(workDir, path, "HEAD")
			newContent, _ = GetFileContentFromIndex(workDir, path)
			rawDiff, _ = GetDiff(workDir, path, true)
		} else {
			// Modified file: old = index (or HEAD if not staged), new = working tree
			oldContent, _ = GetFileContentFromIndex(workDir, path)
			if oldContent == "" {
				oldContent, _ = GetFileContentAtRef(workDir, path, "HEAD")
			}
			newContent, _ = GetWorkingTreeFileContent(workDir, path)
			rawDiff, _ = GetDiff(workDir, path, false)
		}

		parsed, _ := ParseUnifiedDiff(rawDiff)

		return FullFileDiffLoadedMsg{
			Epoch:      epoch,
			RequestID:  requestID,
			File:       path,
			Staged:     staged,
			OldContent: oldContent,
			NewContent: newContent,
			Parsed:     parsed,
			ForInline:  forInline,
		}
	}
}

// loadCommitDetailForPreview loads commit detail for inline preview.
func (p *Plugin) loadCommitDetailForPreview(hash string) tea.Cmd {
	requestID := p.nextPreviewID()
	p.commitPreviewRequestID = requestID
	epoch := p.ctx.Epoch
	workDir := p.repoRoot
	return func() tea.Msg {
		commit, err := GetCommitDetail(workDir, hash)
		if err != nil {
			return CommitPreviewLoadedMsg{Epoch: epoch, RequestID: requestID, Err: err}
		}
		return CommitPreviewLoadedMsg{Epoch: epoch, RequestID: requestID, Commit: commit}
	}
}
