package workspacediff

import (
	"context"
	"os/exec"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	sharedscroll "github.com/marcus/sidecar/internal/scroll"
)

// View is the reusable Diff tab model: one snapshot, one cursor, one commit
// detail. The project plugin wraps it; the global preview holds one for the
// selected worktree.
type View struct {
	Snapshot *Snapshot
	State    LoadState
	Error    string
	Scope    Scope

	Content string
	Raw     string
	Files   []File
	Commits []CommitInfo

	Cursor      int
	Scroll      int
	DiffScroll  int
	HorizScroll int
	Focus       Focus
	ViewMode    ViewMode

	CommitDetail      *CommitDetail
	CommitFileCursor  int
	CommitFileScroll  int
	CommitFileDiffRaw string

	ListWidth int
}

// ApplySnapshot rebuilds the working-tree / commits lists from the snapshot
// and clamps the cursor. It does not load commit detail; callers that just
// applied a snapshot should also call LoadSelectedCommit.
func (v *View) ApplySnapshot() {
	v.Content, v.Raw = "", ""
	v.Files = nil
	v.Commits = nil
	if v.Snapshot == nil {
		return
	}
	switch v.Scope {
	case ScopeCommits:
		v.Commits = append([]CommitInfo(nil), v.Snapshot.Commits...)
	case ScopeAggregate:
		// Aggregate is rendered as two labelled raw sections.
	default:
		v.Content, v.Raw = v.Snapshot.WorkingTree, v.Snapshot.WorkingTree
		v.Files = ParseFiles(v.Raw)
		v.Commits = append([]CommitInfo(nil), v.Snapshot.Commits...)
	}
	v.ClampCursor()
}

// FileCount is the number of working-tree files in the current scope.
func (v *View) FileCount() int { return len(v.Files) }

// TotalItems is files + commits, the navigable left-pane length.
func (v *View) TotalItems() int { return v.FileCount() + len(v.Commits) }

// ClampCursor keeps the cursor inside the current item list.
func (v *View) ClampCursor() {
	total := v.TotalItems()
	if total == 0 {
		v.Cursor = 0
		v.Scroll = 0
		return
	}
	if v.Cursor >= total {
		v.Cursor = total - 1
	}
	if v.Cursor < 0 {
		v.Cursor = 0
	}
}

// SelectedCommit is the commit under the cursor, if any.
func (v *View) SelectedCommit() (CommitInfo, bool) {
	idx := v.Cursor - v.FileCount()
	if idx < 0 || idx >= len(v.Commits) {
		return CommitInfo{}, false
	}
	return v.Commits[idx], true
}

// LoadSelectedCommit loads the commit under the cursor. Snapshot/scope
// populate can leave the cursor on a commit without a move, so this does not
// require a cursor-change event. Skip if that commit is already loaded.
func (v *View) LoadSelectedCommit(workdir, workspaceID string) tea.Cmd {
	commit, ok := v.SelectedCommit()
	if !ok {
		return nil
	}
	if CommitDetailMatchesListHash(v.CommitDetail, commit.Hash) {
		return nil
	}
	v.CommitDetail = nil
	v.CommitFileCursor = 0
	v.CommitFileScroll = 0
	v.CommitFileDiffRaw = ""
	hash := commit.Hash
	return func() tea.Msg {
		detail, err := LoadCommitDetail(context.Background(), workdir, hash)
		return CommitDetailMsg{WorkspaceID: workspaceID, Hash: hash, Commit: detail, Err: err}
	}
}

// CommitDetailMsg is the result of LoadSelectedCommit.
type CommitDetailMsg struct {
	WorkspaceID string
	Hash        string
	Commit      *CommitDetail
	Err         error
}

// ApplyCommitDetail installs a loaded commit if it is still the row under the cursor.
func (v *View) ApplyCommitDetail(msg CommitDetailMsg) {
	if msg.Err != nil || msg.Commit == nil {
		return
	}
	commit, ok := v.SelectedCommit()
	if !ok || !CommitDetailMatchesListHash(msg.Commit, commit.Hash) {
		return
	}
	v.CommitDetail = msg.Commit
	v.CommitFileCursor = 0
	v.CommitFileScroll = 0
	v.CommitFileDiffRaw = ""
}

// SnapshotMsg is a completed snapshot load for one worktree.
type SnapshotMsg struct {
	WorkspaceID string
	Snapshot    *Snapshot
	Err         error
	Command     string
	BaseRef     string
}

// LoadSnapshotCmd loads a snapshot for workdir and tags it with workspaceID.
func LoadSnapshotCmd(workdir, baseRef, workspaceID string) tea.Cmd {
	return func() tea.Msg {
		snapshot, err := LoadSnapshot(context.Background(), workdir, baseRef)
		if err != nil {
			return SnapshotMsg{WorkspaceID: workspaceID, Err: err,
				Command: "git diff HEAD / git log <base>..HEAD / git diff <merge-base>..HEAD", BaseRef: baseRef}
		}
		return SnapshotMsg{WorkspaceID: workspaceID, Snapshot: snapshot, BaseRef: baseRef}
	}
}

// ApplyLoadedSnapshot installs a snapshot, applies the default working-tree
// scope, and loads the commit under the cursor if that is the current item.
func (v *View) ApplyLoadedSnapshot(snapshot *Snapshot, workdir, workspaceID string) tea.Cmd {
	v.Snapshot = snapshot
	v.Error = ""
	v.State = LoadStateClean
	if snapshot != nil {
		v.State = snapshot.State
	}
	v.ApplySnapshot()
	return v.LoadSelectedCommit(workdir, workspaceID)
}

// ContentMaxScroll returns the exact vertical bound for the content currently
// rendered in the right pane (or collapsed view).
func (v *View) ContentMaxScroll(height int) int {
	content := v.Content
	visible := height
	switch {
	case v.Scope == ScopeAggregate:
		content = v.aggregateContent()
	case len(v.Files) > 0 || len(v.Commits) > 0:
		if v.Cursor < 0 || v.Cursor >= len(v.Files) {
			return 0 // commit preview is not scrollable
		}
		content = v.Files[v.Cursor].Raw
		visible = max(1, height-2) // filename + spacer
	}
	return max(len(splitLines(content))-visible, 0)
}

// ScrollAtBoundary reports whether delta points farther past the rendered
// content boundary.
func (v *View) ScrollAtBoundary(delta, height int) bool {
	return (sharedscroll.Bounds{Position: v.DiffScroll, Maximum: v.ContentMaxScroll(height)}).AtBoundary(delta)
}

// ScrollContent moves the visible right-pane (or collapsed) content.
func (v *View) ScrollContent(delta, height int) {
	v.DiffScroll, _ = (sharedscroll.Bounds{
		Position: v.DiffScroll,
		Maximum:  v.ContentMaxScroll(height),
	}).Move(delta)
}

// TaskView is the Task tab model for one worktree.
type TaskView struct {
	TaskID    string
	Task      *Task
	Loading   bool
	Offset    int
	LineCount int
	Error     string
}

func (t *TaskView) Scroll(delta, height int) {
	if t.LineCount <= 0 {
		t.Offset = 0
		return
	}
	t.Offset, _ = (sharedscroll.Bounds{Position: t.Offset, Maximum: t.MaxScroll(height)}).Move(delta)
}

func (t *TaskView) MaxScroll(height int) int {
	return max(t.LineCount-max(1, height), 0)
}

func (t *TaskView) ScrollAtBoundary(delta, height int) bool {
	return (sharedscroll.Bounds{Position: t.Offset, Maximum: t.MaxScroll(height)}).AtBoundary(delta)
}

// ParseFiles splits a unified multi-file diff into named file entries.
func ParseFiles(raw string) []File {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	chunks := splitFileDiffs(raw)
	var files []File
	for _, chunk := range chunks {
		path := filePathFromDiff(chunk)
		if path == "" {
			continue
		}
		adds, dels := countDiffStats(chunk)
		files = append(files, File{Path: path, Raw: chunk, Additions: adds, Deletions: dels})
	}
	return files
}

func splitFileDiffs(diff string) []string {
	var chunks []string
	var current strings.Builder
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "diff --git ") && current.Len() > 0 {
			chunks = append(chunks, current.String())
			current.Reset()
		}
		if current.Len() > 0 {
			current.WriteByte('\n')
		}
		current.WriteString(line)
	}
	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}
	return chunks
}

func filePathFromDiff(chunk string) string {
	for _, line := range strings.Split(chunk, "\n") {
		rest, ok := strings.CutPrefix(line, "diff --git ")
		if !ok {
			continue
		}
		a, b, found := strings.Cut(rest, " b/")
		if !found {
			return strings.TrimPrefix(rest, "a/")
		}
		_ = a
		return b
	}
	return ""
}

func countDiffStats(chunk string) (adds, dels int) {
	for _, line := range strings.Split(chunk, "\n") {
		if strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "+"):
			adds++
		case strings.HasPrefix(line, "-"):
			dels++
		}
	}
	return adds, dels
}

// LoadCommitDetail fetches %H/%h/subject and the commit's name-status files.
func LoadCommitDetail(ctx context.Context, workdir, hash string) (*CommitDetail, error) {
	cmd := exec.CommandContext(ctx, "git", "show", "--format=%H%n%h%n%s", "-s", hash)
	cmd.Dir = workdir
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) < 3 {
		return nil, nil
	}
	detail := &CommitDetail{
		Hash:      strings.TrimSpace(lines[0]),
		ShortHash: strings.TrimSpace(lines[1]),
		Subject:   strings.TrimSpace(lines[2]),
	}
	stat := exec.CommandContext(ctx, "git", "show", "--numstat", "--format=", hash)
	stat.Dir = workdir
	statOut, _ := stat.Output()
	for _, line := range strings.Split(strings.TrimSpace(string(statOut)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		add, _ := strconv.Atoi(fields[0])
		del, _ := strconv.Atoi(fields[1])
		path := fields[len(fields)-1]
		status := "M"
		if fields[0] == "0" && fields[1] != "0" {
			status = "D"
		} else if fields[1] == "0" && fields[0] != "0" {
			status = "A"
		}
		detail.Files = append(detail.Files, CommitFile{Path: path, Status: status, Additions: add, Deletions: del})
	}
	return detail, nil
}
