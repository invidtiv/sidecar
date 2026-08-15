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
	p.fullFileDiff = nil
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
	view.ClearPaintedFile = func() { p.fullFileDiff = nil }
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
	return p.diffTabBox()
}

func (p *Plugin) diffTabBox() (mouse.Rect, bool) {
	content, ok := p.previewContentBox()
	if !ok || content.H <= previewTabRows {
		return mouse.Rect{}, false
	}
	return mouse.Rect{
		X: content.X,
		Y: content.Y + previewTabRows,
		W: content.W,
		H: content.H - previewTabRows,
	}, true
}

func (p *Plugin) registerDiffTabRegions() {
	if p.previewTab != PreviewTabDiff || p.selectingShell() {
		return
	}
	box, ok := p.diffTabBox()
	if !ok {
		return
	}
	p.diff.SetSize(box.W, box.H)
	p.attachDiffPaint()
	for _, hit := range p.diff.FileHits(box) {
		p.mouseHandler.HitMap.AddRect(hit.ID, hit.Rect.X, hit.Rect.Y, hit.Rect.W, hit.Rect.H, hit.Data)
	}
	if d := p.diff.DividerHit(box); d.W > 0 && d.H > 0 {
		p.mouseHandler.HitMap.AddRect(regionDiffTabDivider, d.X, d.Y, d.W, d.H, nil)
	}
	p.registerDiffTabMinimap(box)
}

func (p *Plugin) registerDiffTabMinimap(box mouse.Rect) {
	if p.diff.ViewMode != DiffViewFullFile || p.fullFileDiff == nil || box.W < workspacediff.CollapseThreshold {
		return
	}
	listW := p.diff.EffectiveListWidth(box.W)
	diffW := box.W - listW - 1
	contentH := box.H - 2
	if contentH < 1 {
		contentH = 1
	}
	mmH := contentH
	if total := p.fullFileDiff.TotalLines(); total < mmH {
		mmH = total
	}
	mmX := box.X + listW + 1 + diffW - gitstatus.MinimapWidth
	p.mouseHandler.HitMap.AddRect(regionDiffTabMinimap, mmX, box.Y+2, gitstatus.MinimapWidth, mmH, nil)
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
		PaintFile: p.paintDiffFile,
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

func (p *Plugin) jumpToNextFile() tea.Cmd {
	p.bindDiffView()
	return p.diff.JumpFile(1)
}

func (p *Plugin) jumpToPrevFile() tea.Cmd {
	p.bindDiffView()
	return p.diff.JumpFile(-1)
}

func (p *Plugin) loadSelectedCommitFileDiff() tea.Cmd {
	p.bindDiffView()
	return p.diff.LoadSelectedCommitFile()
}

func (p *Plugin) loadCommitDetail(hash string) tea.Cmd {
	p.bindDiffView()
	return p.diff.LoadCommit(hash)
}

func (p *Plugin) selectedDiffTabFile() string { return p.diff.SelectedFileName() }

func (p *Plugin) paintDiffFile(name, raw string, mode workspacediff.ViewMode, width, height, scroll, horiz int) string {
	parsed, err := gitstatus.ParseUnifiedDiff(raw)
	if (err != nil || parsed == nil) && mode != workspacediff.ViewFullFile {
		return ""
	}
	highlighter := gitstatus.NewSyntaxHighlighter(name)
	switch mode {
	case workspacediff.ViewFullFile:
		if p.fullFileDiff != nil {
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

func (p *Plugin) loadFullFileForCurrent() tea.Cmd {
	return p.loadFullFileForView(p.activeDiffView())
}

func (p *Plugin) loadFullFileForView(view *workspacediff.View) tea.Cmd {
	if view == nil || p.fullFileDiff != nil {
		return nil
	}
	if view.Focus == DiffTabFocusCommitDiff || view.Focus == DiffTabFocusCommitFiles {
		return p.loadFullFileDiffForView(view)
	}
	return p.loadFullFileDiffForWorkspaceView(view)
}

func (p *Plugin) loadFullFileDiffForWorkspace() tea.Cmd {
	return p.loadFullFileDiffForWorkspaceView(p.activeDiffView())
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

func (p *Plugin) loadFullFileDiffForCommit() tea.Cmd {
	return p.loadFullFileDiffForView(p.activeDiffView())
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

func (p *Plugin) diffBaseRef() string {
	if p.diff.Snapshot != nil && p.diff.Snapshot.BaseRef != "" {
		return p.diff.Snapshot.BaseRef
	}
	if wt := p.selectedWorktree(); wt != nil && wt.BaseBranch != "" {
		return wt.BaseBranch
	}
	return "resolved base"
}
