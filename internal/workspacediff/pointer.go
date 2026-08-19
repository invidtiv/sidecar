package workspacediff

import (
	tea "charm.land/bubbletea/v2"
	sharedscroll "github.com/marcus/sidecar/internal/scroll"
)

// IsBodyRegion reports an inner Diff hit: a file or commit row, a pane, the
// list/hunk divider, or the full-file minimap. Hosts register these from
// FileHits/DividerHit and must dispatch them through HandleClick / HandleWheel
// rather than re-implementing the selection rules. Tab chips are not included.
func IsBodyRegion(id string) bool {
	switch id {
	case RegionFile, RegionCommit, RegionDiffPane, RegionMinimap,
		RegionCommitBack, RegionCommitFile, RegionCommitDiff,
		RegionPreviewFile, RegionFileListPane, RegionDivider:
		return true
	default:
		return false
	}
}

// HandleClick applies a single click on an inner Diff hit. The host still owns
// leaf focus and drag-start; this mutates only the view.
func (v *View) HandleClick(id string, data any) tea.Cmd {
	if v == nil {
		return nil
	}
	switch id {
	case RegionFile, RegionCommit:
		v.Focus = FocusFileList
		idx, ok := data.(int)
		if !ok || idx == v.Cursor {
			return nil
		}
		old := v.Cursor
		v.Cursor = idx
		return v.OnCursorChanged(old)
	case RegionDiffPane:
		if v.Focus == FocusCommitFiles || v.Focus == FocusCommitDiff {
			v.Focus = FocusCommitDiff
			return nil
		}
		v.Focus = FocusDiff
		return nil
	case RegionMinimap:
		if v.Focus == FocusCommitFiles || v.Focus == FocusCommitDiff {
			v.Focus = FocusCommitDiff
			return nil
		}
		v.Focus = FocusDiff
		return nil
	case RegionCommitBack:
		v.Focus = FocusFileList
		v.resetCommitDetail()
		return nil
	case RegionCommitFile:
		idx, ok := data.(int)
		if !ok {
			return nil
		}
		v.Focus = FocusCommitFiles
		if idx == v.CommitFileCursor {
			return nil
		}
		v.CommitFileCursor = idx
		v.clearCommitFileDiff()
		return v.LoadSelectedCommitFile()
	case RegionCommitDiff:
		v.Focus = FocusCommitDiff
		return nil
	case RegionPreviewFile:
		idx, ok := data.(int)
		if !ok {
			return nil
		}
		commit, found := v.SelectedCommit()
		if !found {
			return nil
		}
		v.Focus = FocusCommitFiles
		v.resetCommitDetail()
		v.CommitFileCursor = idx
		v.CommitFileScroll = 0
		return v.LoadCommit(commit.Hash)
	case RegionFileListPane:
		if v.Focus == FocusCommitFiles || v.Focus == FocusCommitDiff {
			v.Focus = FocusCommitFiles
			return nil
		}
		v.Focus = FocusFileList
		return nil
	}
	return nil
}

// HandleDoubleClick drills into the hit: a file opens its hunks, a commit
// opens its file list, a commit file opens its patch.
func (v *View) HandleDoubleClick(id string, data any) tea.Cmd {
	if v == nil {
		return nil
	}
	switch id {
	case RegionFile:
		idx, ok := data.(int)
		if !ok {
			return nil
		}
		old := v.Cursor
		v.Cursor = idx
		v.Focus = FocusDiff
		v.DiffScroll = 0
		v.HorizScroll = 0
		if idx != old {
			return v.OnCursorChanged(old)
		}
		return nil
	case RegionCommit:
		idx, ok := data.(int)
		if !ok {
			return nil
		}
		v.Cursor = idx
		commit, found := v.SelectedCommit()
		if !found {
			return nil
		}
		v.Focus = FocusCommitFiles
		v.resetCommitDetail()
		return v.LoadCommit(commit.Hash)
	case RegionCommitFile:
		idx, ok := data.(int)
		if !ok {
			return nil
		}
		v.CommitFileCursor = idx
		v.Focus = FocusCommitDiff
		v.DiffScroll = 0
		v.HorizScroll = 0
		v.clearCommitFileDiff()
		return v.LoadSelectedCommitFile()
	case RegionPreviewFile:
		return v.HandleClick(id, data)
	}
	return v.HandleClick(id, data)
}

// HandleWheel scrolls the list or body that owns id. File-list notches only
// move the cursor; they do not kick off a commit load.
func (v *View) HandleWheel(id string, delta int) tea.Cmd {
	if v == nil {
		return nil
	}
	switch id {
	case RegionFile, RegionCommit, RegionFileListPane, RegionPreviewFile:
		v.scrollFileList(delta)
		return nil
	case RegionDiffPane, RegionMinimap, RegionCommitDiff:
		v.ScrollContent(delta, v.Height())
		return nil
	case RegionCommitFile, RegionCommitBack:
		v.scrollCommitFileList(delta)
		return nil
	}
	return nil
}

// WheelAtBoundary reports whether a notch on id would move past the rendered
// edge of that inner pane.
func (v *View) WheelAtBoundary(id string, delta int) bool {
	if v == nil {
		return true
	}
	switch id {
	case RegionFile, RegionCommit, RegionFileListPane, RegionPreviewFile:
		return (sharedscroll.Bounds{Position: v.Cursor, Maximum: v.TotalItems() - 1}).AtBoundary(delta)
	case RegionDiffPane, RegionMinimap, RegionCommitDiff:
		return v.ScrollAtBoundary(delta, v.Height())
	case RegionCommitFile, RegionCommitBack:
		maximum := -1
		if v.CommitDetail != nil {
			maximum = len(v.CommitDetail.Files) - 1
		}
		return (sharedscroll.Bounds{Position: v.CommitFileCursor, Maximum: maximum}).AtBoundary(delta)
	default:
		return v.ScrollAtBoundary(delta, v.Height())
	}
}

func (v *View) scrollFileList(delta int) {
	total := v.TotalItems()
	if total == 0 {
		return
	}
	next := v.Cursor + delta
	if next < 0 {
		next = 0
	}
	if next >= total {
		next = total - 1
	}
	if next == v.Cursor {
		return
	}
	v.Cursor = next
	v.DiffScroll = 0
	v.HorizScroll = 0
	if v.Cursor < v.FileCount() {
		v.CommitDetail = nil
	}
	v.ClampScroll()
}

func (v *View) scrollCommitFileList(delta int) {
	if v.CommitDetail == nil || len(v.CommitDetail.Files) == 0 {
		return
	}
	n := len(v.CommitDetail.Files)
	next := v.CommitFileCursor + delta
	if next < 0 {
		next = 0
	}
	if next >= n {
		next = n - 1
	}
	if next == v.CommitFileCursor {
		return
	}
	v.CommitFileCursor = next
	v.clearCommitFileDiff()
}
