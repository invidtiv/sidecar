package workspace

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/plugins/gitstatus"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/ui"
	"github.com/marcus/sidecar/internal/workspacediff"
)

func (p *Plugin) resetDiffView() {
	width, mode := p.diff.ListWidth(), p.diff.ViewMode
	p.diff = workspacediff.View{ViewMode: mode, Target: workspacediff.WorkingTreeTarget()}
	p.diff.SetListWidth(width)
	p.clearPaintedFile()
}

func mapPluginCommit(c *gitstatus.Commit) *workspacediff.CommitDetail {
	if c == nil {
		return nil
	}
	d := &workspacediff.CommitDetail{
		Hash: c.Hash, ShortHash: c.ShortHash, Subject: c.Subject,
		IsMerge: c.IsMerge, ParentHashes: append([]string(nil), c.ParentHashes...),
	}
	for _, f := range c.Files {
		d.Files = append(d.Files, workspacediff.CommitFile{
			Path: f.Path, Status: string(f.Status),
			Additions: f.Additions, Deletions: f.Deletions,
		})
	}
	return d
}

func (p *Plugin) bindDiffView() {
	wt := p.selectedWorktree()
	if wt == nil {
		return
	}
	epoch := uint64(0)
	if p.ctx != nil {
		epoch = p.ctx.Epoch
	}
	p.diff.Bind(wt.Path, wt.IdentityKey(), epoch)
	p.diff.Target = workspacediff.WorkingTreeTarget()
	p.attachDiffPaint()
}

func (p *Plugin) attachDiffPaint() {
	p.attachDiffPaintTo(&p.diff)
}

func (p *Plugin) attachDiffPaintTo(view *workspacediff.View) {
	if view == nil {
		return
	}
	view.LoadFullFile = func() tea.Cmd { return p.loadFullFileForView(view) }
	view.JumpChange = p.jumpFullFileChange
	view.PaintedLineCount = p.paintedLineCount
	view.LeavingFullFile = p.mapFullFileScroll
	view.ClearPaintedFile = p.clearPaintedFile
	view.HasFilePicker = true
}

func (p *Plugin) clearPaintedFile() {
	p.fullFileDiff = nil
	p.fullFileKey = ""
}

// fullFileKeyFor names the file a view wants painted in full-file mode:
// commit hash and path for a commit file, the working-tree path otherwise.
// It is the identity the painted slot is held under.
func fullFileKeyFor(view *workspacediff.View) string {
	if view == nil {
		return ""
	}
	if view.Focus == DiffTabFocusCommitDiff || view.Focus == DiffTabFocusCommitFiles {
		detail := view.CommitDetail
		if detail == nil || view.CommitFileCursor < 0 || view.CommitFileCursor >= len(detail.Files) {
			return ""
		}
		return detail.Hash + ":" + detail.Files[view.CommitFileCursor].Path
	}
	if name := view.SelectedFileName(); name != "" {
		return ":" + name
	}
	return ""
}

// fullFileKeyForMsg is fullFileKeyFor for a landed load.
func fullFileKeyForMsg(msg FullFileDiffLoadedMsg) string {
	return msg.CommitHash + ":" + msg.FilePath
}

// fullFileWanted reports that some live Diff view is showing the file this
// load answers: the legacy view or any tab in any Diff leaf.
func (p *Plugin) fullFileWanted(msg FullFileDiffLoadedMsg) bool {
	if fullFileMatchesView(&p.diff, msg) {
		return true
	}
	for _, pane := range p.diffs {
		if pane == nil {
			continue
		}
		for _, item := range pane.tabs.Items {
			if fullFileMatchesView(item.Value, msg) {
				return true
			}
		}
	}
	return false
}

func fullFileMatchesView(view *workspacediff.View, msg FullFileDiffLoadedMsg) bool {
	if view == nil {
		return false
	}
	if view.WorkspaceID != "" && msg.WorkspaceName != "" && view.WorkspaceID != msg.WorkspaceName {
		return false
	}
	if view.Target.Identity() != "" && msg.Identity != "" && view.Target.Identity() != msg.Identity {
		return false
	}
	if msg.FilePath == "" {
		return false
	}
	return fullFileKeyFor(view) == fullFileKeyForMsg(msg)
}

// paintedFileIsFor reports that the shared full-file slot holds the file this
// view is showing. An empty key is the legacy single-view case and matches.
func (p *Plugin) paintedFileIsFor(view *workspacediff.View) bool {
	if p.fullFileKey == "" {
		return true
	}
	key := fullFileKeyFor(view)
	return key == "" || key == p.fullFileKey
}

func (p *Plugin) persistDiffViewMode() {
	switch p.diff.ViewMode {
	case DiffViewSideBySide:
		_ = state.SetWorkspaceDiffMode("side-by-side")
	case DiffViewFullFile:
		_ = state.SetWorkspaceDiffMode("full-file")
	default:
		_ = state.SetWorkspaceDiffMode("unified")
	}
}

func (p *Plugin) diffDividerBox() (mouse.Rect, bool) {
	if diff, leaf := p.activeDiffPane(); diff != nil && leaf != nil {
		if box, ok := p.paneLeafBox(leaf.ID); ok {
			return mouse.Rect{X: box.X, Y: box.Y + terminalHeaderRows, W: box.W, H: maxInt(box.H-terminalHeaderRows, 0)}, true
		}
	}
	return mouse.Rect{}, false
}

func (p *Plugin) renderDiffContent(width, height int) string {
	wt := p.selectedWorktree()
	if wt == nil {
		return dimText("No worktree selected")
	}
	p.diff.SetSize(width, height)
	p.attachDiffPaint()
	return p.diff.Render(width, height, workspacediff.RenderOpts{
		Truncate: func(s string, w int, suffix string) string {
			return p.truncateCache.Truncate(s, w, suffix)
		},
		PaintFile: p.paintFileFor(&p.diff),
		Handle:    p.dividerHandleState(regionDiffTabDivider, 0),
	})
}

func (p *Plugin) diffTabFileCount() int { return p.diff.FileCount() }

func (p *Plugin) diffTabTotalItems() int { return p.diff.TotalItems() }

func (p *Plugin) applyDiffScope() {
	p.diff.ApplySnapshot()
}

func (p *Plugin) cycleDiffScope() tea.Cmd {
	p.bindDiffView()
	return p.diff.CycleScope()
}

func (p *Plugin) loadSelectedDiffTabCommit() tea.Cmd {
	p.bindDiffView()
	return p.diff.LoadSelectedCommit(p.diff.WorkDir, p.diff.WorkspaceID)
}

func (p *Plugin) onDiffTabCursorChanged(oldCursor int) tea.Cmd {
	p.bindDiffView()
	return p.diff.OnCursorChanged(oldCursor)
}

func (p *Plugin) loadSelectedCommitFileDiff() tea.Cmd {
	p.bindDiffView()
	return p.diff.LoadSelectedCommitFile()
}

func (p *Plugin) loadCommitDetail(hash string) tea.Cmd {
	p.bindDiffView()
	return p.diff.LoadCommit(hash)
}

// paintFileFor is the PaintFile hook bound to one view, so the painter can tell
// whether the shared full-file slot holds this view's file or another's.
func (p *Plugin) paintFileFor(view *workspacediff.View) func(string, string, workspacediff.ViewMode, int, int, int, int) string {
	return func(name, raw string, mode workspacediff.ViewMode, width, height, scroll, horiz int) string {
		return p.paintDiffFileFor(view, name, raw, mode, width, height, scroll, horiz)
	}
}

func (p *Plugin) paintDiffFile(name, raw string, mode workspacediff.ViewMode, width, height, scroll, horiz int) string {
	return p.paintDiffFileFor(&p.diff, name, raw, mode, width, height, scroll, horiz)
}

func (p *Plugin) paintDiffFileFor(view *workspacediff.View, name, raw string, mode workspacediff.ViewMode, width, height, scroll, horiz int) string {
	parsed, err := gitstatus.ParseUnifiedDiff(raw)
	if (err != nil || parsed == nil) && mode != workspacediff.ViewFullFile {
		return ""
	}
	highlighter := gitstatus.NewSyntaxHighlighter(name)
	switch mode {
	case workspacediff.ViewFullFile:
		if p.fullFileDiff != nil && p.paintedFileIsFor(view) {
			diffW := width - gitstatus.MinimapWidth
			mmStr := gitstatus.RenderMinimap(p.fullFileDiff, scroll, height, height)
			if mmStr != "" && diffW >= 30 {
				body := gitstatus.RenderFullFileSideBySide(p.fullFileDiff, diffW, scroll, height, horiz, highlighter, false)
				return lipgloss.JoinHorizontal(lipgloss.Top, body, mmStr)
			}
			return gitstatus.RenderFullFileSideBySide(p.fullFileDiff, width, scroll, height, horiz, highlighter, false)
		}
		return dimText("Loading full file...")
	case workspacediff.ViewSideBySide:
		if parsed == nil {
			return ""
		}
		return gitstatus.RenderSideBySide(parsed, width, scroll, height, horiz, highlighter, false)
	default:
		if parsed == nil {
			return ""
		}
		return gitstatus.RenderLineDiff(parsed, width, scroll, height, horiz, highlighter, false)
	}
}

func (p *Plugin) loadFullFileForView(view *workspacediff.View) tea.Cmd {
	if view == nil {
		return nil
	}
	// The painted slot is shared. Skipping the load only because something is
	// painted is what left a second Diff view showing the first view's file.
	if key := fullFileKeyFor(view); p.fullFileDiff != nil && key != "" && key == p.fullFileKey {
		return nil
	}
	if view.Focus == DiffTabFocusCommitDiff || view.Focus == DiffTabFocusCommitFiles {
		return p.loadFullFileDiffForView(view)
	}
	return p.loadFullFileDiffForWorkspaceView(view)
}

func (p *Plugin) loadFullFileDiffForWorkspaceView(view *workspacediff.View) tea.Cmd {
	if view == nil {
		return nil
	}
	wt := p.selectedWorktree()
	filePath := view.SelectedFileName()
	if filePath == "" {
		return nil
	}
	workdir := view.WorkDir
	if workdir == "" && wt != nil {
		workdir = wt.Path
	}
	if workdir == "" {
		return nil
	}
	epoch := uint64(0)
	if p.ctx != nil {
		epoch = p.ctx.Epoch
	}
	name := view.WorkspaceID
	if name == "" && wt != nil {
		name = wt.IdentityKey()
	}
	ident := view.Target.Identity()
	return func() tea.Msg {
		oldContent, _ := gitstatus.GetFileContentAtRef(workdir, filePath, "HEAD")
		newContent, _ := gitstatus.GetWorkingTreeFileContent(workdir, filePath)
		rawDiff, _ := gitstatus.GetDiffFromHead(workdir, filePath)
		if rawDiff == "" {
			rawDiff, _ = gitstatus.GetNewFileDiff(workdir, filePath)
		}
		parsed, _ := gitstatus.ParseUnifiedDiff(rawDiff)
		return FullFileDiffLoadedMsg{
			Epoch: epoch, WorkspaceName: name, Identity: ident,
			OldContent: oldContent, NewContent: newContent, Parsed: parsed, FilePath: filePath,
		}
	}
}

func (p *Plugin) loadFullFileDiffForView(view *workspacediff.View) tea.Cmd {
	if view == nil || view.CommitDetail == nil {
		return nil
	}
	if view.CommitFileCursor < 0 || view.CommitFileCursor >= len(view.CommitDetail.Files) {
		return nil
	}
	file := view.CommitDetail.Files[view.CommitFileCursor]
	commitHash := view.CommitDetail.Hash
	parentHash := ""
	if view.CommitDetail.IsMerge && len(view.CommitDetail.ParentHashes) > 0 {
		parentHash = view.CommitDetail.ParentHashes[0]
	}
	workdir := view.WorkDir
	if workdir == "" {
		if wt := p.selectedWorktree(); wt != nil {
			workdir = wt.Path
		}
	}
	if workdir == "" {
		return nil
	}
	epoch := uint64(0)
	if p.ctx != nil {
		epoch = p.ctx.Epoch
	}
	name := view.WorkspaceID
	ident := view.Target.Identity()
	return func() tea.Msg {
		parentRef := commitHash + "~1"
		if parentHash != "" {
			parentRef = parentHash
		}
		oldContent, _ := gitstatus.GetFileContentAtRef(workdir, file.Path, parentRef)
		newContent, _ := gitstatus.GetFileContentAtRef(workdir, file.Path, commitHash)
		rawDiff, _ := gitstatus.GetCommitDiff(workdir, commitHash, file.Path, parentHash)
		parsed, _ := gitstatus.ParseUnifiedDiff(rawDiff)
		return FullFileDiffLoadedMsg{
			Epoch: epoch, WorkspaceName: name, Identity: ident,
			OldContent: oldContent, NewContent: newContent, Parsed: parsed,
			FilePath: file.Path, CommitHash: commitHash,
		}
	}
}

func (p *Plugin) jumpFullFileChange(scroll int, prev bool) int {
	if p.fullFileDiff == nil {
		return -1
	}
	if prev {
		return p.fullFileDiff.PrevChange(scroll)
	}
	return p.fullFileDiff.NextChange(scroll)
}

func (p *Plugin) paintedLineCount() int {
	if p.diff.ViewMode == DiffViewFullFile && p.fullFileDiff != nil {
		return p.fullFileDiff.TotalLines()
	}
	return 0
}

func (p *Plugin) mapFullFileScroll(scroll int) int {
	if p.fullFileDiff == nil {
		return scroll
	}
	raw := p.currentPaintRaw()
	parsed, _ := gitstatus.ParseUnifiedDiff(raw)
	if parsed == nil {
		return scroll
	}
	return p.fullFileDiff.FullFileLineToHunkLine(scroll, parsed)
}

func (p *Plugin) currentPaintRaw() string {
	if p.diff.Focus == DiffTabFocusCommitDiff {
		return p.diff.CommitFileDiffRaw
	}
	if p.diff.Cursor >= 0 && p.diff.Cursor < len(p.diff.Files) {
		return p.diff.Files[p.diff.Cursor].Raw
	}
	return p.diff.Raw
}

func (p *Plugin) openFilePicker() tea.Cmd {
	view := p.activeDiffView()
	if view.FileCount() <= 1 {
		return nil
	}
	p.filePickerIdx = view.Cursor
	maxIdx := view.FileCount() - 1
	if p.filePickerIdx > maxIdx {
		p.filePickerIdx = maxIdx
	}
	if p.filePickerIdx < 0 {
		p.filePickerIdx = 0
	}
	p.viewMode = ViewModeFilePicker
	return nil
}

func (p *Plugin) renderFilePickerModal(background string) string {
	names := p.activeDiffView().FileNames()
	if len(names) == 0 {
		return background
	}
	var sb strings.Builder
	sb.WriteString(styles.ModalTitle.Render("Jump to File"))
	sb.WriteString("\n\n")
	for i, name := range names {
		line := name
		if i == p.filePickerIdx {
			sb.WriteString(styles.ListItemSelected.Render("▸ " + line))
		} else {
			sb.WriteString("  " + line)
		}
		if i < len(names)-1 {
			sb.WriteString("\n")
		}
	}
	modalWidth := 50
	for _, name := range names {
		if w := lipgloss.Width(name) + 6; w > modalWidth {
			modalWidth = w
		}
	}
	if modalWidth > p.width-10 {
		modalWidth = p.width - 10
	}
	modalStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(styles.Primary).
		Padding(1, 2).
		Width(modalWidth)
	return ui.OverlayModal(background, modalStyle.Render(sb.String()), p.width, p.height)
}

func (p *Plugin) colorStatLine(line string, width int) string {
	if len(line) == 0 {
		return line
	}
	if lipgloss.Width(line) > width {
		line = p.truncateCache.Truncate(line, width, "")
	}
	pipeIdx := strings.LastIndex(line, "|")
	if pipeIdx == -1 {
		return line
	}
	prefix := line[:pipeIdx+1]
	bar := line[pipeIdx+1:]
	var colored strings.Builder
	colored.WriteString(prefix)
	for _, ch := range bar {
		switch ch {
		case '+':
			colored.WriteString(styles.DiffAdd.Render("+"))
		case '-':
			colored.WriteString(styles.DiffRemove.Render("-"))
		default:
			colored.WriteRune(ch)
		}
	}
	return colored.String()
}
