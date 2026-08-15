package workspacediff

// PageSize is the height-aware page step: max(1, allocatedHeight/2).
func (v *View) PageSize() int {
	if v.height/2 < 1 {
		return 1
	}
	return v.height / 2
}

// ClampScroll keeps list and content offsets inside the allocated box.
// Call from HandleKey, ApplySnapshot, and SetSize — never while painting.
func (v *View) ClampScroll() {
	v.ClampCursor()
	v.clampFileListScroll()
	v.clampCommitFileScroll()
	v.clampDiffScroll()
	if v.HorizScroll < 0 {
		v.HorizScroll = 0
	}
}

func (v *View) clampFileListScroll() {
	if v.Scroll < 0 {
		v.Scroll = 0
	}
	files := v.FileCount()
	if files == 0 {
		v.Scroll = 0
		return
	}
	visible := v.fileListVisibleHeight()
	if visible < 1 {
		visible = 1
	}
	if v.Cursor < files {
		if v.Cursor < v.Scroll {
			v.Scroll = v.Cursor
		}
		if v.Cursor >= v.Scroll+visible {
			v.Scroll = v.Cursor - visible + 1
		}
	}
	maxScroll := files - visible
	if maxScroll < 0 {
		maxScroll = 0
	}
	if v.Scroll > maxScroll {
		v.Scroll = maxScroll
	}
}

func (v *View) clampCommitFileScroll() {
	if v.CommitFileScroll < 0 {
		v.CommitFileScroll = 0
	}
	n := 0
	if v.CommitDetail != nil {
		n = len(v.CommitDetail.Files)
	}
	if n == 0 {
		v.CommitFileScroll = 0
		return
	}
	visible := v.height - commitFileListHeaderLines
	if visible < 1 {
		visible = 1
	}
	if v.CommitFileCursor < v.CommitFileScroll {
		v.CommitFileScroll = v.CommitFileCursor
	}
	if v.CommitFileCursor >= v.CommitFileScroll+visible {
		v.CommitFileScroll = v.CommitFileCursor - visible + 1
	}
	maxScroll := n - visible
	if maxScroll < 0 {
		maxScroll = 0
	}
	if v.CommitFileScroll > maxScroll {
		v.CommitFileScroll = maxScroll
	}
}

func (v *View) clampDiffScroll() {
	if v.DiffScroll < 0 {
		v.DiffScroll = 0
	}
	maxScroll := v.maxDiffScroll()
	if v.DiffScroll > maxScroll {
		v.DiffScroll = maxScroll
	}
}

func (v *View) maxDiffScroll() int {
	lines := v.countDiffLines()
	h := v.height
	if h < 1 {
		h = 1
	}
	maxScroll := lines - h
	if maxScroll < 0 {
		return 0
	}
	return maxScroll
}

func (v *View) countDiffLines() int {
	if v.Scope == ScopeAggregate {
		return len(splitLines(v.aggregateText()))
	}
	if v.Focus == FocusCommitDiff || v.Focus == FocusCommitFiles {
		if v.CommitFileDiffRaw != "" {
			return len(splitLines(v.CommitFileDiffRaw))
		}
	}
	if v.Cursor < v.FileCount() {
		if raw := v.selectedFileRaw(); raw != "" {
			return len(splitLines(raw))
		}
	}
	if v.Content != "" {
		return len(splitLines(v.Content))
	}
	return 0
}

func (v *View) fileListVisibleHeight() int {
	height := v.height
	if height < 1 {
		return 1
	}
	linesUsed := 1 // header
	commitLines := 0
	if n := len(v.Commits); n > 0 {
		commitLines = 2 + n
		if commitLines > height/3 {
			commitLines = height / 3
			if commitLines < 3 {
				commitLines = 3
			}
		}
	}
	filesHeight := height - linesUsed - commitLines
	if filesHeight < 3 {
		filesHeight = 3
	}
	return filesHeight
}

const commitFileListHeaderLines = 4
