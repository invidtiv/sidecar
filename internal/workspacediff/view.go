package workspacediff

import (
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	sharedscroll "github.com/marcus/sidecar/internal/scroll"
)

// View is the reusable Diff pane model: one snapshot, one cursor, one commit
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

	Target      Target
	Epoch       uint64
	WorkspaceID string
	WorkDir     string

	width     int
	height    int
	listWidth int

	// Host paint/load hooks. workspacediff cannot import gitstatus; the
	// project plugin fills these so CycleViewMode, n/N, and paging work.
	LoadFullFile     func() tea.Cmd
	JumpChange       func(scroll int, prev bool) int
	PaintedLineCount func() int
	LeavingFullFile  func(scroll int) int
	ClearPaintedFile func()
}

// Bind records the host identity used to drop stale async results.
func (v *View) Bind(workdir, workspaceID string, epoch uint64) {
	if workdir != "" {
		v.WorkDir = workdir
	}
	if workspaceID != "" {
		v.WorkspaceID = workspaceID
	}
	v.Epoch = epoch
	if v.Target.Identity() == "" {
		v.Target = WorkingTreeTarget()
	}
}

// SetSize records the allocated leaf box and reclamps scroll.
// It must not persist a clamped listWidth: hosts call this from View()
// every frame, and a shrink must not forget the user-dragged width.
func (v *View) SetSize(width, height int) {
	v.width = width
	v.height = height
	v.ClampScroll()
}

// Width and Height are the last SetSize allocation.
func (v *View) Width() int  { return v.width }
func (v *View) Height() int { return v.height }

func (v *View) accepts(epoch uint64, workspaceID, identity string) bool {
	if workspaceID != "" && v.WorkspaceID != "" && workspaceID != v.WorkspaceID {
		return false
	}
	if epoch != 0 && v.Epoch != 0 && epoch != v.Epoch {
		return false
	}
	if identity != "" && v.Target.Identity() != "" && identity != v.Target.Identity() {
		return false
	}
	return true
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
	v.ClampScroll()
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
	return v.loadCommit(workdir, workspaceID, commit.Hash)
}

// LoadCommit fetches one commit's file list, tagged for stale-drop.
func (v *View) LoadCommit(hash string) tea.Cmd {
	return v.loadCommit(v.WorkDir, v.WorkspaceID, hash)
}

func (v *View) loadCommit(workdir, workspaceID, hash string) tea.Cmd {
	if workdir != "" {
		v.WorkDir = workdir
	}
	if workspaceID != "" {
		v.WorkspaceID = workspaceID
	}
	epoch, id, ident := v.Epoch, v.WorkspaceID, v.Target.Identity()
	wd := v.WorkDir
	return func() tea.Msg {
		detail, err := LoadCommitDetail(context.Background(), wd, hash)
		return CommitDetailMsg{
			Epoch: epoch, WorkspaceID: id, Identity: ident,
			Hash: hash, Commit: detail, Err: err,
		}
	}
}

// CommitDetailMsg is the result of LoadSelectedCommit.
type CommitDetailMsg struct {
	Epoch       uint64
	WorkspaceID string
	Identity    string
	Hash        string
	Commit      *CommitDetail
	Err         error
}

// ApplyCommitDetail installs a loaded commit if it is still the row under
// the cursor, or the root of a TargetCommit tab whose Identity matches.
func (v *View) ApplyCommitDetail(msg CommitDetailMsg) tea.Cmd {
	if !v.accepts(msg.Epoch, msg.WorkspaceID, msg.Identity) {
		return nil
	}
	if v.Target.Kind == TargetCommit {
		return v.applyCommitRoot(msg)
	}
	if msg.Err != nil || msg.Commit == nil {
		return nil
	}
	commit, ok := v.SelectedCommit()
	if !ok || !CommitDetailMatchesListHash(msg.Commit, commit.Hash) {
		return nil
	}
	preserve := v.CommitDetail != nil && CommitDetailMatchesListHash(v.CommitDetail, commit.Hash)
	v.CommitDetail = msg.Commit
	if !preserve {
		v.CommitFileCursor = 0
		v.CommitFileScroll = 0
		v.CommitFileDiffRaw = ""
	}
	v.ClampScroll()
	if v.Focus == FocusCommitFiles || v.Focus == FocusCommitDiff {
		return v.LoadSelectedCommitFile()
	}
	return nil
}

func (v *View) applyCommitRoot(msg CommitDetailMsg) tea.Cmd {
	if msg.Err != nil || msg.Commit == nil {
		v.CommitDetail = nil
		v.State = LoadStateError
		if msg.Err != nil {
			v.Error = msg.Err.Error()
		} else {
			v.Error = "commit not found"
		}
		return nil
	}
	preserve := v.CommitDetail != nil && CommitDetailMatchesListHash(v.CommitDetail, msg.Commit.Hash)
	v.CommitDetail = msg.Commit
	v.Snapshot = nil
	v.Commits = nil
	v.Files = nil
	v.Content, v.Raw = "", ""
	v.Error = ""
	v.State = LoadStateReady
	v.Focus = FocusCommitFiles
	if !preserve {
		v.CommitFileCursor = 0
		v.CommitFileScroll = 0
		v.CommitFileDiffRaw = ""
	}
	v.ClampScroll()
	return v.LoadSelectedCommitFile()
}

// SnapshotMsg is a completed snapshot load for one worktree.
type SnapshotMsg struct {
	Epoch       uint64
	WorkspaceID string
	Identity    string
	Snapshot    *Snapshot
	Err         error
	Command     string
	BaseRef     string
}

// LoadSnapshotCmd loads a snapshot for workdir and tags it with workspaceID.
func LoadSnapshotCmd(workdir, baseRef, workspaceID string) tea.Cmd {
	return LoadSnapshotCmdAt(workdir, baseRef, workspaceID, 0, IdentityWorkingTree)
}

// LoadSnapshotCmdAt is LoadSnapshotCmd with epoch and target identity.
func LoadSnapshotCmdAt(workdir, baseRef, workspaceID string, epoch uint64, identity string) tea.Cmd {
	if identity == "" {
		identity = IdentityWorkingTree
	}
	return func() tea.Msg {
		snapshot, err := LoadSnapshot(context.Background(), workdir, baseRef)
		if err != nil {
			return SnapshotMsg{Epoch: epoch, WorkspaceID: workspaceID, Identity: identity, Err: err,
				Command: "git diff HEAD / git log <base>..HEAD / git diff <merge-base>..HEAD", BaseRef: baseRef}
		}
		return SnapshotMsg{Epoch: epoch, WorkspaceID: workspaceID, Identity: identity, Snapshot: snapshot, BaseRef: baseRef}
	}
}

// ApplySnapshotMsg installs a loaded snapshot or records the error, dropping stale msgs.
func (v *View) ApplySnapshotMsg(msg SnapshotMsg, workdir, workspaceID string) tea.Cmd {
	if v.Target.Kind != TargetWorkingTree {
		return nil
	}
	if !v.accepts(msg.Epoch, msg.WorkspaceID, msg.Identity) {
		return nil
	}
	if msg.Err != nil {
		v.Snapshot = nil
		v.State = LoadStateError
		v.Error = msg.Err.Error()
		v.Content, v.Raw = "", ""
		v.Files, v.Commits = nil, nil
		return nil
	}
	return v.ApplyLoadedSnapshot(msg.Snapshot, workdir, workspaceID)
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

// RangeMsg is the result of LoadRange for one A..B / A...B tab.
type RangeMsg struct {
	Epoch       uint64
	WorkspaceID string
	Identity    string
	Raw         string
	Files       []File
	Err         error
}

// LoadRange fetches git diff --binary A..B or A...B for this tab.
func (v *View) LoadRange() tea.Cmd {
	if v.Target.Kind != TargetRange || v.Target.A == "" || v.Target.B == "" {
		return nil
	}
	return LoadRangeCmd(v.WorkDir, v.Target, v.Epoch, v.WorkspaceID)
}

// LoadRangeCmd runs one git diff for a range target.
func LoadRangeCmd(workdir string, t Target, epoch uint64, workspaceID string) tea.Cmd {
	if t.Kind != TargetRange || t.A == "" || t.B == "" {
		return nil
	}
	dots := t.Dots
	if dots != "..." {
		dots = ".."
	}
	spec := t.A + dots + t.B
	ident := t.Identity()
	return func() tea.Msg {
		raw, err := loadRangeDiff(context.Background(), workdir, spec)
		if err != nil {
			return RangeMsg{Epoch: epoch, WorkspaceID: workspaceID, Identity: ident, Err: err}
		}
		return RangeMsg{Epoch: epoch, WorkspaceID: workspaceID, Identity: ident, Raw: raw, Files: ParseFiles(raw)}
	}
}

func loadRangeDiff(ctx context.Context, workdir, spec string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "diff", "--binary", spec)
	cmd.Dir = workdir
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return "", err
		}
	}
	return string(out), nil
}

// ApplyRangeMsg installs a range patch when Identity matches this r: tab.
func (v *View) ApplyRangeMsg(msg RangeMsg) tea.Cmd {
	if v.Target.Kind != TargetRange {
		return nil
	}
	if !v.accepts(msg.Epoch, msg.WorkspaceID, msg.Identity) {
		return nil
	}
	if msg.Err != nil {
		v.State = LoadStateError
		v.Error = msg.Err.Error()
		v.Files = nil
		v.Commits = nil
		v.Snapshot = nil
		v.CommitDetail = nil
		v.Content, v.Raw = "", ""
		return nil
	}
	v.Error = ""
	v.State = LoadStateReady
	v.Snapshot = nil
	v.Commits = nil
	v.CommitDetail = nil
	v.Raw = msg.Raw
	v.Content = msg.Raw
	if msg.Files != nil {
		v.Files = msg.Files
	} else {
		v.Files = ParseFiles(msg.Raw)
	}
	v.Focus = FocusFileList
	v.ClampScroll()
	return nil
}

// CycleScope walks working-tree → commits → aggregate. No-op on commit/range targets.
func (v *View) CycleScope() tea.Cmd {
	if v.Target.Kind != TargetWorkingTree {
		return nil
	}
	v.Scope = (v.Scope + 1) % 3
	v.Cursor, v.Scroll, v.DiffScroll, v.HorizScroll = 0, 0, 0, 0
	v.Focus = FocusFileList
	if v.Scope == ScopeAggregate {
		v.Focus = FocusDiff
	}
	v.CommitDetail = nil
	v.clearCommitFileDiff()
	v.dropPaintedFile()
	v.ApplySnapshot()
	cmd := v.LoadSelectedCommit(v.WorkDir, v.WorkspaceID)
	if v.ViewMode == ViewFullFile && v.LoadFullFile != nil {
		return tea.Batch(cmd, v.LoadFullFile())
	}
	return cmd
}

// CycleViewMode walks unified → side-by-side → full-file.
// Entering full-file returns the host LoadFullFile cmd.
func (v *View) CycleViewMode() tea.Cmd {
	switch v.ViewMode {
	case ViewUnified:
		v.ViewMode = ViewSideBySide
	case ViewSideBySide:
		v.ViewMode = ViewFullFile
		v.HorizScroll = 0
		v.ClampScroll()
		if v.LoadFullFile != nil {
			return v.LoadFullFile()
		}
		return nil
	default:
		if v.LeavingFullFile != nil && v.DiffScroll > 0 {
			v.DiffScroll = v.LeavingFullFile(v.DiffScroll)
		}
		if v.ClearPaintedFile != nil {
			v.ClearPaintedFile()
		}
		v.ViewMode = ViewUnified
	}
	v.HorizScroll = 0
	v.ClampScroll()
	return nil
}

// JumpFile moves to the next or previous file in this tab's list.
func (v *View) JumpFile(delta int) tea.Cmd {
	if v.Focus == FocusCommitDiff || v.Focus == FocusCommitFiles {
		if v.CommitDetail == nil {
			return nil
		}
		n := len(v.CommitDetail.Files)
		next := v.CommitFileCursor + delta
		if next < 0 || next >= n {
			return nil
		}
		v.CommitFileCursor = next
		v.DiffScroll, v.HorizScroll = 0, 0
		v.clearCommitFileDiff()
		if v.ClearPaintedFile != nil {
			v.ClearPaintedFile()
		}
		v.ClampScroll()
		load := v.LoadSelectedCommitFile()
		if v.ViewMode == ViewFullFile && v.LoadFullFile != nil {
			return tea.Batch(load, v.LoadFullFile())
		}
		return load
	}
	n := v.FileCount()
	if n <= 1 {
		return nil
	}
	next := v.Cursor + delta
	if next < 0 || next >= n {
		return nil
	}
	old := v.Cursor
	v.Cursor = next
	v.DiffScroll, v.HorizScroll = 0, 0
	v.ClampScroll()
	return v.OnCursorChanged(old)
}

// OnCursorChanged resets the right pane after a file-list move.
func (v *View) OnCursorChanged(oldCursor int) tea.Cmd {
	if v.Cursor == oldCursor {
		return nil
	}
	v.DiffScroll = 0
	v.HorizScroll = 0
	if v.ClearPaintedFile != nil {
		v.ClearPaintedFile()
	}
	v.ClampScroll()
	if v.Cursor < v.FileCount() {
		v.CommitDetail = nil
		if v.ViewMode == ViewFullFile && v.LoadFullFile != nil {
			return v.LoadFullFile()
		}
		return nil
	}
	return v.LoadSelectedCommit(v.WorkDir, v.WorkspaceID)
}

func (v *View) selectedFileName() string {
	if v.Cursor >= 0 && v.Cursor < len(v.Files) {
		return v.Files[v.Cursor].Path
	}
	return ""
}

func (v *View) selectedFileRaw() string {
	if v.Cursor >= 0 && v.Cursor < len(v.Files) {
		return v.Files[v.Cursor].Raw
	}
	return ""
}

type fileRow struct {
	Path      string
	Additions int
	Deletions int
}

func (v *View) fileRows() []fileRow {
	rows := make([]fileRow, len(v.Files))
	for i, f := range v.Files {
		rows[i] = fileRow{Path: f.Path, Additions: f.Additions, Deletions: f.Deletions}
	}
	return rows
}

// SelectedFileName is the working-tree path under the cursor, if any.
func (v *View) SelectedFileName() string { return v.selectedFileName() }

// FileNames is the working-tree list for the host file picker.
func (v *View) FileNames() []string {
	names := make([]string, len(v.Files))
	for i, f := range v.Files {
		names[i] = f.Path
	}
	return names
}

// CommitFileDiffMsg is a completed commit-file patch load.
type CommitFileDiffMsg struct {
	Epoch       uint64
	WorkspaceID string
	Identity    string
	CommitHash  string
	FilePath    string
	Raw         string
	Err         error
}

// ApplyCommitFileDiff installs a commit file patch if the cursor still matches.
func (v *View) ApplyCommitFileDiff(msg CommitFileDiffMsg) tea.Cmd {
	if !v.accepts(msg.Epoch, msg.WorkspaceID, msg.Identity) {
		return nil
	}
	if msg.Err != nil || v.CommitDetail == nil || v.CommitDetail.Hash != msg.CommitHash {
		return nil
	}
	if v.CommitFileCursor < 0 || v.CommitFileCursor >= len(v.CommitDetail.Files) {
		return nil
	}
	if v.CommitDetail.Files[v.CommitFileCursor].Path != msg.FilePath {
		return nil
	}
	v.CommitFileDiffRaw = msg.Raw
	return nil
}

// LoadSelectedCommitFile loads the patch for the commit file under the cursor.
func (v *View) LoadSelectedCommitFile() tea.Cmd {
	if v.CommitDetail == nil || v.CommitFileCursor < 0 || v.CommitFileCursor >= len(v.CommitDetail.Files) {
		return nil
	}
	file := v.CommitDetail.Files[v.CommitFileCursor]
	parentHash := ""
	if v.CommitDetail.IsMerge && len(v.CommitDetail.ParentHashes) > 0 {
		parentHash = v.CommitDetail.ParentHashes[0]
	}
	hash := v.CommitDetail.Hash
	workdir, epoch, id, ident := v.WorkDir, v.Epoch, v.WorkspaceID, v.Target.Identity()
	return func() tea.Msg {
		raw, err := loadCommitFileDiff(workdir, hash, file.Path, parentHash)
		return CommitFileDiffMsg{
			Epoch: epoch, WorkspaceID: id, Identity: ident,
			CommitHash: hash, FilePath: file.Path, Raw: raw, Err: err,
		}
	}
}

func loadCommitFileDiff(workdir, hash, path, parentHash string) (string, error) {
	args := []string{"show", hash, "--", path}
	if parentHash != "" {
		args = []string{"diff", parentHash, hash, "--", path}
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = workdir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// ContentMaxScroll returns the exact vertical bound for the content currently
// rendered in the right pane (or collapsed view).
func (v *View) ContentMaxScroll(height int) int {
	content := v.Content
	visible := height
	switch {
	case v.Scope == ScopeAggregate:
		content = v.aggregateText()
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
