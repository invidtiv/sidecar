package workspacediff

import (
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/plugin"
)

// HandleKey applies a Diff-tab key. handled is false when the host should
// keep the key (h/left on the file list, unknown keys).
func (v *View) HandleKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch v.Focus {
	case FocusDiff:
		return v.handleDiffPaneKey(msg)
	case FocusCommitFiles:
		return v.handleCommitFilesKey(msg)
	case FocusCommitDiff:
		return v.handleCommitDiffKey(msg)
	default:
		return v.handleFileListKey(msg)
	}
}

// Commands are the short footer names for a focused Diff surface.
func (v *View) Commands(context string) []plugin.Command {
	viewName := "Split"
	switch v.ViewMode {
	case ViewSideBySide:
		viewName = "Full"
	case ViewFullFile:
		viewName = "Unified"
	}
	cmds := []plugin.Command{
		{ID: "toggle-diff-scope", Name: "Scope", Description: "Cycle working tree, commits, and aggregate", Context: context, Priority: 5},
		{ID: "toggle-diff-view", Name: viewName, Description: "Cycle diff view mode", Context: context, Priority: 6},
	}
	if v.FileCount() > 1 {
		cmds = append(cmds,
			plugin.Command{ID: "next-file", Name: "}", Description: "Next file", Context: context, Priority: 6},
			plugin.Command{ID: "prev-file", Name: "{", Description: "Previous file", Context: context, Priority: 7},
			plugin.Command{ID: "file-picker", Name: "Files", Description: "Open file picker", Context: context, Priority: 8},
		)
	}
	return cmds
}

func (v *View) handleFileListKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	total := v.TotalItems()
	switch msg.String() {
	case "j", "down":
		if v.Cursor < total-1 {
			old := v.Cursor
			v.Cursor++
			v.ClampScroll()
			return v.OnCursorChanged(old), true
		}
		return nil, true
	case "k", "up":
		if v.Cursor > 0 {
			old := v.Cursor
			v.Cursor--
			v.ClampScroll()
			return v.OnCursorChanged(old), true
		}
		return nil, true
	case "g":
		if v.Cursor != 0 {
			old := v.Cursor
			v.Cursor = 0
			v.Scroll = 0
			v.ClampScroll()
			return v.OnCursorChanged(old), true
		}
		return nil, true
	case "G":
		if total > 0 && v.Cursor != total-1 {
			old := v.Cursor
			v.Cursor = total - 1
			v.ClampScroll()
			return v.OnCursorChanged(old), true
		}
		return nil, true
	case "l", "right", "enter":
		if v.Cursor < v.FileCount() {
			v.Focus = FocusDiff
			return nil, true
		}
		if commit, ok := v.SelectedCommit(); ok {
			v.Focus = FocusCommitFiles
			v.CommitDetail = nil
			v.CommitFileCursor = 0
			v.CommitFileScroll = 0
			v.CommitFileDiffRaw = ""
			return v.LoadCommit(commit.Hash), true
		}
		return nil, true
	case "h", "left":
		return nil, false
	case "v", "V":
		return v.CycleViewMode(), true
	case "z":
		return v.CycleScope(), true
	case "{":
		return v.JumpFile(-1), true
	case "}":
		return v.JumpFile(1), true
	case "ctrl+d", "pgdown":
		return v.pageFileList(v.PageSize()), true
	case "ctrl+u", "pgup":
		return v.pageFileList(-v.PageSize()), true
	}
	return nil, false
}

func (v *View) handleDiffPaneKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "j", "down":
		v.scrollDiffBy(1)
		return nil, true
	case "k", "up":
		v.scrollDiffBy(-1)
		return nil, true
	case "g":
		v.DiffScroll = 0
		v.HorizScroll = 0
		return nil, true
	case "G":
		v.DiffScroll = v.maxDiffScroll()
		return nil, true
	case "ctrl+d", "pgdown":
		v.scrollDiffBy(v.PageSize())
		return nil, true
	case "ctrl+u", "pgup":
		v.scrollDiffBy(-v.PageSize())
		return nil, true
	case "esc":
		v.Focus = FocusFileList
		return nil, true
	case "h", "left":
		if v.HorizScroll > 0 {
			v.HorizScroll -= 10
			if v.HorizScroll < 0 {
				v.HorizScroll = 0
			}
			return nil, true
		}
		v.Focus = FocusFileList
		return nil, true
	case "l", "right":
		v.HorizScroll += 10
		return nil, true
	case "n":
		v.jumpPaintedChange(false)
		return nil, true
	case "N":
		v.jumpPaintedChange(true)
		return nil, true
	case "v", "V":
		return v.CycleViewMode(), true
	case "z":
		return v.CycleScope(), true
	case "{":
		return v.JumpFile(-1), true
	case "}":
		return v.JumpFile(1), true
	}
	return nil, false
}

func (v *View) handleCommitFilesKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if v.CommitDetail == nil {
		if msg.String() == "esc" || msg.String() == "h" || msg.String() == "left" {
			v.Focus = FocusFileList
			v.CommitDetail = nil
			return nil, true
		}
		return nil, true
	}
	n := len(v.CommitDetail.Files)
	switch msg.String() {
	case "j", "down":
		if v.CommitFileCursor < n-1 {
			v.CommitFileCursor++
			v.clearCommitFileDiff()
			v.ClampScroll()
			return v.LoadSelectedCommitFile(), true
		}
		return nil, true
	case "k", "up":
		if v.CommitFileCursor > 0 {
			v.CommitFileCursor--
			v.clearCommitFileDiff()
			v.ClampScroll()
			return v.LoadSelectedCommitFile(), true
		}
		return nil, true
	case "g":
		if v.CommitFileCursor != 0 {
			v.CommitFileCursor = 0
			v.CommitFileScroll = 0
			v.clearCommitFileDiff()
			v.ClampScroll()
			return v.LoadSelectedCommitFile(), true
		}
		return nil, true
	case "G":
		if n > 0 && v.CommitFileCursor != n-1 {
			v.CommitFileCursor = n - 1
			v.clearCommitFileDiff()
			v.ClampScroll()
			return v.LoadSelectedCommitFile(), true
		}
		return nil, true
	case "l", "right", "enter":
		if n > 0 {
			v.Focus = FocusCommitDiff
			v.DiffScroll = 0
			v.HorizScroll = 0
		}
		return nil, true
	case "h", "left", "esc":
		v.Focus = FocusFileList
		v.CommitDetail = nil
		v.clearCommitFileDiff()
		return nil, true
	case "v", "V":
		return v.CycleViewMode(), true
	case "ctrl+d", "pgdown":
		return v.pageCommitFiles(v.PageSize()), true
	case "ctrl+u", "pgup":
		return v.pageCommitFiles(-v.PageSize()), true
	}
	return nil, false
}

func (v *View) handleCommitDiffKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "j", "down":
		v.scrollDiffBy(1)
		return nil, true
	case "k", "up":
		v.scrollDiffBy(-1)
		return nil, true
	case "g":
		v.DiffScroll = 0
		v.HorizScroll = 0
		return nil, true
	case "G":
		v.DiffScroll = v.maxDiffScroll()
		return nil, true
	case "ctrl+d", "pgdown":
		v.scrollDiffBy(v.PageSize())
		return nil, true
	case "ctrl+u", "pgup":
		v.scrollDiffBy(-v.PageSize())
		return nil, true
	case "h", "left":
		if v.HorizScroll > 0 {
			v.HorizScroll -= 10
			if v.HorizScroll < 0 {
				v.HorizScroll = 0
			}
			return nil, true
		}
		v.Focus = FocusCommitFiles
		v.DiffScroll = 0
		v.HorizScroll = 0
		return nil, true
	case "l", "right":
		v.HorizScroll += 10
		return nil, true
	case "esc":
		v.Focus = FocusCommitFiles
		v.DiffScroll = 0
		v.HorizScroll = 0
		return nil, true
	case "n":
		v.jumpPaintedChange(false)
		return nil, true
	case "N":
		v.jumpPaintedChange(true)
		return nil, true
	case "{":
		if v.CommitDetail != nil && v.CommitFileCursor > 0 {
			v.CommitFileCursor--
			v.DiffScroll, v.HorizScroll = 0, 0
			v.clearCommitFileDiff()
			return v.LoadSelectedCommitFile(), true
		}
		return nil, true
	case "}":
		if v.CommitDetail != nil && v.CommitFileCursor < len(v.CommitDetail.Files)-1 {
			v.CommitFileCursor++
			v.DiffScroll, v.HorizScroll = 0, 0
			v.clearCommitFileDiff()
			return v.LoadSelectedCommitFile(), true
		}
		return nil, true
	case "v", "V":
		return v.CycleViewMode(), true
	}
	return nil, false
}

func (v *View) jumpPaintedChange(prev bool) {
	if v.ViewMode != ViewFullFile || v.JumpChange == nil {
		return
	}
	if next := v.JumpChange(v.DiffScroll, prev); next >= 0 {
		v.DiffScroll = next
	}
}

func (v *View) scrollDiffBy(delta int) {
	v.DiffScroll += delta
	v.clampDiffScroll()
}

func (v *View) pageFileList(delta int) tea.Cmd {
	old := v.Cursor
	v.Cursor += delta
	v.ClampScroll()
	return v.OnCursorChanged(old)
}

func (v *View) pageCommitFiles(delta int) tea.Cmd {
	if v.CommitDetail == nil {
		return nil
	}
	v.CommitFileCursor += delta
	if v.CommitFileCursor < 0 {
		v.CommitFileCursor = 0
	}
	if n := len(v.CommitDetail.Files); n > 0 && v.CommitFileCursor >= n {
		v.CommitFileCursor = n - 1
	}
	v.clearCommitFileDiff()
	v.ClampScroll()
	return v.LoadSelectedCommitFile()
}

func (v *View) clearCommitFileDiff() {
	v.CommitFileDiffRaw = ""
}
