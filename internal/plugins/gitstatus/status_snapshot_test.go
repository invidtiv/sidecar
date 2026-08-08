package gitstatus

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/plugin"
)

func snapshotPlugin(epoch uint64) *Plugin {
	return &Plugin{ctx: &plugin.Context{Epoch: epoch}, repoRoot: "/repo", hasRepo: true, tree: NewFileTree("/repo")}
}

func TestStatusRefreshBuildsThenInstallsSnapshotOnUpdate(t *testing.T) {
	p := snapshotPlugin(4)
	original := p.tree
	want := NewFileTree("/repo")
	want.Modified = []*FileEntry{{Path: "new", Unstaged: true}}
	p.statusLoader = func(workDir string) (*FileTree, error) {
		if workDir != "/repo" {
			t.Fatalf("workDir = %q", workDir)
		}
		return want, nil
	}

	msg := p.refresh()().(StatusSnapshotLoadedMsg)
	if p.tree != original {
		t.Fatal("worker command installed the snapshot outside Update")
	}
	p.Update(msg)
	if p.tree != want {
		t.Fatal("Update did not install the completed snapshot")
	}
}

func TestStatusRefreshSingleFlightCoalescesOneFollowUp(t *testing.T) {
	p := snapshotPlugin(2)
	loads := 0
	p.statusLoader = func(string) (*FileTree, error) {
		loads++
		return NewFileTree("/repo"), nil
	}

	first := p.refresh()
	if p.refresh() != nil || p.refresh() != nil {
		t.Fatal("invalidation started a second concurrent load")
	}
	firstMsg := first().(StatusSnapshotLoadedMsg)
	_, followUp := p.Update(firstMsg)
	if followUp == nil || p.activeStatusRequestID != 2 || p.statusRefreshDirty {
		t.Fatalf("follow-up state: cmd=%v active=%d dirty=%v", followUp != nil, p.activeStatusRequestID, p.statusRefreshDirty)
	}
	secondMsg := runStatusCommand(t, followUp)
	p.Update(secondMsg)
	if loads != 2 || p.activeStatusRequestID != 0 {
		t.Fatalf("loads=%d active=%d, want 2 and idle", loads, p.activeStatusRequestID)
	}
}

func runStatusCommand(t *testing.T, cmd tea.Cmd) StatusSnapshotLoadedMsg {
	t.Helper()
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, child := range batch {
			if child != nil {
				if status, ok := child().(StatusSnapshotLoadedMsg); ok {
					return status
				}
			}
		}
	}
	status, ok := msg.(StatusSnapshotLoadedMsg)
	if !ok {
		t.Fatalf("command returned %T, want status snapshot", msg)
	}
	return status
}

func TestStatusSnapshotRejectsWrongRequestAndPreviousProject(t *testing.T) {
	p := snapshotPlugin(9)
	p.activeStatusRequestID = 3
	original := p.tree
	for _, msg := range []StatusSnapshotLoadedMsg{
		{Epoch: 9, RequestID: 2, Tree: NewFileTree("/wrong-order")},
		{Epoch: 8, RequestID: 3, Tree: NewFileTree("/old-project")},
	} {
		p.Update(msg)
		if p.tree != original || p.activeStatusRequestID != 3 {
			t.Fatalf("stale result changed state: %#v", msg)
		}
	}
}

func TestIndexWriteRefreshDoesNotReloadHistory(t *testing.T) {
	p := snapshotPlugin(1)
	p.activeOperation = &operationRequest{ID: 7, Epoch: 1, Kind: operationStage}
	p.recentCommits = []*Commit{{Hash: "keep"}}
	_, cmd := p.Update(operationResultMsg{ID: 7, Epoch: 1, Kind: operationStage})
	if _, ok := cmd().(StatusSnapshotLoadedMsg); !ok {
		t.Fatal("write follow-up was not a status-only load")
	}
	if len(p.recentCommits) != 1 || p.recentCommits[0].Hash != "keep" {
		t.Fatal("index-only write invalidated commit history")
	}
}

func TestWatcherIndexInvalidationCoalescesWithoutHistoryReload(t *testing.T) {
	p := snapshotPlugin(1)
	p.activeStatusRequestID = 1
	_, cmd := p.Update(WatchEventMsg{History: false})
	if cmd != nil {
		t.Fatal("index event scheduled work besides the coalesced status follow-up")
	}
	if !p.statusRefreshDirty {
		t.Fatal("index event did not retain a status follow-up")
	}

	p.statusRefreshDirty = false
	_, cmd = p.Update(WatchEventMsg{History: true})
	if cmd == nil {
		t.Fatal("HEAD/ref event did not schedule history refresh")
	}
}

func TestLateSamePathInlinePreviewCannotOverwriteNewerRequest(t *testing.T) {
	p := snapshotPlugin(5)
	p.selectedDiffFile = "same.go"
	p.inlinePreviewRequestID = 2
	newer := &ParsedDiff{NewFile: "newer"}
	older := &ParsedDiff{NewFile: "older"}
	p.Update(InlineDiffLoadedMsg{Epoch: 5, RequestID: 2, File: "same.go", Parsed: newer})
	p.Update(InlineDiffLoadedMsg{Epoch: 5, RequestID: 1, File: "same.go", Parsed: older})
	if p.diffPaneParsedDiff != newer {
		t.Fatal("late same-path preview overwrote the newer request")
	}
}

func TestRecentHistoryRejectsReversedSameEpochCompletion(t *testing.T) {
	p := snapshotPlugin(6)
	p.activeHistoryRequestID = 2
	p.Update(RecentCommitsLoadedMsg{Epoch: 6, RequestID: 2, Commits: []*Commit{{Hash: "newer"}}})
	p.Update(RecentCommitsLoadedMsg{Epoch: 6, RequestID: 1, Commits: []*Commit{{Hash: "older"}}})
	if len(p.recentCommits) != 1 || p.recentCommits[0].Hash != "newer" {
		t.Fatalf("late history completion overwrote newer state: %#v", p.recentCommits)
	}
}

func TestRecentHistorySingleFlightCoalescesFollowUp(t *testing.T) {
	p := snapshotPlugin(3)
	p.historyLoader = func(string, int) ([]*Commit, *PushStatus, error) {
		return []*Commit{{Hash: "loaded"}}, nil, nil
	}
	first := p.loadRecentCommits()
	if p.loadRecentCommits() != nil || !p.historyRefreshDirty {
		t.Fatal("second history invalidation was not coalesced")
	}
	_, followUp := p.Update(first().(RecentCommitsLoadedMsg))
	if followUp == nil || p.activeHistoryRequestID != 2 || p.historyRefreshDirty {
		t.Fatalf("history follow-up state: cmd=%v active=%d dirty=%v", followUp != nil, p.activeHistoryRequestID, p.historyRefreshDirty)
	}
}

func TestFullScreenPreviewErrorSettlesOnlyCurrentRequest(t *testing.T) {
	p := snapshotPlugin(8)
	p.diffLoaded = false
	p.fullScreenPreviewRequestID = 2
	_, cmd := p.Update(DiffLoadedMsg{Epoch: 8, RequestID: 1, Err: errors.New("old")})
	if cmd != nil || p.diffLoaded {
		t.Fatal("stale preview error settled the current request")
	}
	_, cmd = p.Update(DiffLoadedMsg{Epoch: 8, RequestID: 2, Err: errors.New("missing")})
	if cmd == nil || !p.diffLoaded {
		t.Fatal("current preview error did not settle and surface")
	}
}

func TestCommitPreviewErrorSettlesOnlyCurrentRequest(t *testing.T) {
	p := snapshotPlugin(8)
	p.diffPaneWidth = 80
	p.recentCommits = []*Commit{{Hash: "selected"}}
	p.commitPreviewRequestID = 2
	p.previewCommit = &Commit{Hash: "previous"}
	_, cmd := p.Update(CommitPreviewLoadedMsg{Epoch: 8, RequestID: 1, Err: errors.New("old")})
	if cmd != nil || p.previewCommit == nil || p.previewCommitError != "" {
		t.Fatal("stale commit error changed the current preview")
	}
	_, cmd = p.Update(CommitPreviewLoadedMsg{Epoch: 8, RequestID: 2, Err: errors.New("missing")})
	if cmd == nil || p.previewCommit != nil || p.previewCommitError != "missing" {
		t.Fatal("current commit error did not clear and surface")
	}
	rendered := p.renderDiffPane(20)
	if !strings.Contains(rendered, "Unable to load commit") || !strings.Contains(rendered, "missing") || strings.Contains(rendered, "Loading commit") {
		t.Fatalf("terminal commit error rendered incorrectly: %q", rendered)
	}

	// A stale completion cannot replace the terminal state.
	p.Update(CommitPreviewLoadedMsg{Epoch: 8, RequestID: 1, Commit: &Commit{Hash: "old"}})
	if got := p.renderDiffPane(20); !strings.Contains(got, "missing") {
		t.Fatalf("stale success replaced terminal error: %q", got)
	}

	// A new request clears the terminal error, and success clears it permanently.
	if cmd := p.autoLoadCommitPreview(); cmd == nil || p.previewCommitError != "" {
		t.Fatal("new commit request did not clear terminal error")
	}
	currentID := p.commitPreviewRequestID
	p.Update(CommitPreviewLoadedMsg{Epoch: 8, RequestID: currentID, Commit: &Commit{Hash: "selected", ShortHash: "abc123"}})
	if p.previewCommitError != "" || p.previewCommit == nil {
		t.Fatal("successful commit preview did not clear error state")
	}
}

func TestInitClearsCommitPreviewSelectionAndTerminalState(t *testing.T) {
	repo := t.TempDir()
	runGitTest(t, repo, "init")
	p := snapshotPlugin(1)
	p.cursor = 4
	p.previewCommit = &Commit{Hash: "old"}
	p.previewCommitError = "old error"
	p.commitPreviewRequestID = 9
	if err := p.Init(&plugin.Context{Epoch: 2, WorkDir: repo}); err != nil {
		t.Fatal(err)
	}
	if p.cursor != 0 || p.previewCommit != nil || p.previewCommitError != "" || p.commitPreviewRequestID != 0 {
		t.Fatalf("project initialization retained preview selection state: cursor=%d preview=%#v err=%q request=%d", p.cursor, p.previewCommit, p.previewCommitError, p.commitPreviewRequestID)
	}
}

func TestDiffPaneDoesNotRenderCommitLoadingForEmptyRepository(t *testing.T) {
	p := snapshotPlugin(1)
	p.diffPaneWidth = 80
	rendered := p.renderDiffPane(20)
	if strings.Contains(rendered, "Loading commit") || strings.Contains(rendered, "Unable to load commit") {
		t.Fatalf("empty repository rendered a commit preview state: %q", rendered)
	}
	if !strings.Contains(rendered, "Select a file") {
		t.Fatalf("empty repository did not render the empty diff prompt: %q", rendered)
	}
}

func TestCommitPreviewRoutingRequiresInRangeSelection(t *testing.T) {
	p := snapshotPlugin(1)
	p.diffPaneWidth = 80
	p.recentCommits = []*Commit{{Hash: "one"}}

	p.cursor = 0
	if got := p.renderDiffPane(20); !strings.Contains(got, "Loading commit") {
		t.Fatalf("valid pending commit did not render loading state: %q", got)
	}

	p.cursor = 1
	if got := p.renderDiffPane(20); strings.Contains(got, "Loading commit") {
		t.Fatalf("out-of-range commit boundary rendered loading state: %q", got)
	}
}

func TestPreviewLoadersReturnTaggedErrors(t *testing.T) {
	p := snapshotPlugin(11)
	p.repoRoot = t.TempDir()
	diff := p.loadDiff("missing", false, StatusModified)().(DiffLoadedMsg)
	if diff.Err == nil || diff.Epoch != 11 || diff.RequestID == 0 {
		t.Fatalf("diff error result = %#v", diff)
	}
	commit := p.loadCommitDetailForPreview("missing")().(CommitPreviewLoadedMsg)
	if commit.Err == nil || commit.Epoch != 11 || commit.RequestID == 0 {
		t.Fatalf("commit error result = %#v", commit)
	}
}
