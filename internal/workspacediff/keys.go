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
//
// Every key HandleKey answers is named here, and every one of them has a
// matching row in internal/keymap/bindings.go for both Diff contexts. The
// bindings are what put the keys in the footer, the help sheet and the command
// palette; before they existed, moving around a diff was folklore.
//
// The set follows the focused sub-pane, because the same key means different
// things in each: on the list l opens, in the diff body it scrolls sideways.
func (v *View) Commands(context string) []plugin.Command {
	viewName := "Split"
	switch v.ViewMode {
	case ViewSideBySide:
		viewName = "Full"
	case ViewFullFile:
		viewName = "Unified"
	}
	cmd := func(id, name, desc string, priority int) plugin.Command {
		return plugin.Command{ID: id, Name: name, Description: desc, Context: context, Priority: priority}
	}
	var cmds []plugin.Command
	switch v.Focus {
	case FocusDiff, FocusCommitDiff:
		cmds = append(cmds,
			cmd("diff-scroll-down", "Down", "Scroll the diff down", 2),
			cmd("diff-scroll-up", "Up", "Scroll the diff up", 3),
			cmd("diff-back", "List", "Back to the file list", 4),
		)
	case FocusCommitFiles:
		cmds = append(cmds,
			cmd("diff-open", "Open", "Show the selected file's diff", 2),
			cmd("diff-down", "Down", "Next file", 3),
			cmd("diff-up", "Up", "Previous file", 4),
			cmd("diff-back", "List", "Back to the file list", 5),
		)
	default:
		cmds = append(cmds,
			cmd("diff-open", "Open", "Show the selected file or commit", 2),
			cmd("diff-down", "Down", "Next item", 3),
			cmd("diff-up", "Up", "Previous item", 4),
		)
	}
	cmds = append(cmds,
		cmd("toggle-diff-view", viewName, "Cycle diff view mode", 6),
		cmd("toggle-diff-scope", "Scope", "Cycle working tree, commits, and aggregate", 7),
	)
	if v.FileCount() > 1 {
		cmds = append(cmds,
			cmd("next-file", "}", "Next file", 8),
			cmd("prev-file", "{", "Previous file", 9),
		)
		if v.HasFilePicker {
			cmds = append(cmds, cmd("file-picker", "Files", "Open file picker", 10))
		}
	}
	if v.ViewMode == ViewFullFile && (v.Focus == FocusDiff || v.Focus == FocusCommitDiff) {
		cmds = append(cmds, cmd("diff-next-change", "Change", "Jump to the next change", 11))
	}
	return append(cmds,
		cmd("diff-top", "Top", "Jump to the top", 20),
		cmd("diff-bottom", "Bottom", "Jump to the bottom", 21),
		cmd("diff-page-down", "PgDn", "Page down", 22),
		cmd("diff-page-up", "PgUp", "Page up", 23),
	)
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
			v.resetCommitDetail()
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
			if v.Target.Kind == TargetCommit {
				return nil, true
			}
			v.Focus = FocusFileList
			v.resetCommitDetail()
			v.dropPaintedFile()
			return nil, true
		}
		return nil, true
	}
	n := len(v.CommitDetail.Files)
	switch msg.String() {
	case "j", "down":
		if v.CommitFileCursor < n-1 {
			v.CommitFileCursor++
			return v.afterCommitFileMove(), true
		}
		return nil, true
	case "k", "up":
		if v.CommitFileCursor > 0 {
			v.CommitFileCursor--
			return v.afterCommitFileMove(), true
		}
		return nil, true
	case "g":
		if v.CommitFileCursor != 0 {
			v.CommitFileCursor = 0
			v.CommitFileScroll = 0
			return v.afterCommitFileMove(), true
		}
		return nil, true
	case "G":
		if n > 0 && v.CommitFileCursor != n-1 {
			v.CommitFileCursor = n - 1
			return v.afterCommitFileMove(), true
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
		if v.Target.Kind == TargetCommit {
			return nil, true
		}
		v.Focus = FocusFileList
		v.resetCommitDetail()
		v.dropPaintedFile()
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
	return v.afterCommitFileMove()
}

func (v *View) afterCommitFileMove() tea.Cmd {
	v.clearCommitFileDiff()
	v.dropPaintedFile()
	v.ClampScroll()
	load := v.LoadSelectedCommitFile()
	if v.ViewMode == ViewFullFile && v.LoadFullFile != nil {
		return tea.Batch(load, v.LoadFullFile())
	}
	return load
}

func (v *View) dropPaintedFile() {
	if v.ClearPaintedFile != nil {
		v.ClearPaintedFile()
	}
}

func (v *View) clearCommitFileDiff() {
	v.CommitFileDiffRaw = ""
	v.CommitFileDiffLoaded = false
	v.CommitFileDiffErr = ""
}
