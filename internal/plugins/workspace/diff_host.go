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
	for _, hit := range p.diff.FileHits(box) {
		p.mouseHandler.HitMap.AddRect(hit.ID, hit.Rect.X, hit.Rect.Y, hit.Rect.W, hit.Rect.H, hit.Data)
	}
	if d := p.diff.DividerHit(box); d.W > 0 && d.H > 0 {
		p.mouseHandler.HitMap.AddRect(regionDiffTabDivider, d.X, d.Y, d.W, d.H, nil)
	}
}

func (p *Plugin) renderDiffContent(width, height int) string {
	wt := p.selectedWorktree()
	if wt == nil {
		return dimText("No worktree selected")
	}
	p.diff.SetSize(width, height)
	return p.diff.Render(width, height, workspacediff.RenderOpts{
		Truncate: func(s string, w int, suffix string) string {
			return p.truncateCache.Truncate(s, w, suffix)
		},
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

func (p *Plugin) openFilePicker() tea.Cmd {
	if p.diff.FileCount() <= 1 {
		return nil
	}
	p.filePickerIdx = p.diff.Cursor
	maxIdx := p.diff.FileCount() - 1
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
	names := p.diff.FileNames()
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
