package workspacediff

import "github.com/marcus/sidecar/internal/mouse"

// Hit is one mouse target inside the leaf box. The host registers it.
type Hit struct {
	ID   string
	Rect mouse.Rect
	Data any
}

const (
	listMinWidth     = 20
	listMaxReserve   = 30
	dividerHitCols   = 3
	defaultListShare = 25
	// ContentInset is the columns of padding the Diff body keeps on each side,
	// matching the issue pane's horizontal content padding.
	ContentInset = 1
	// minInsetWidth is the narrowest leaf still worth padding. Below it the
	// padding would cost more than the content it frames.
	minInsetWidth = 2*ContentInset + 8
)

// ContentBox is the box the Diff body is drawn in: the leaf inset by
// ContentInset on the left and right. Render and every hit region go through
// this one function, so a click lands on the row that was drawn.
func ContentBox(leaf mouse.Rect) mouse.Rect {
	if leaf.W < minInsetWidth {
		return leaf
	}
	leaf.X += ContentInset
	leaf.W -= 2 * ContentInset
	return leaf
}

// contentWidth is ContentBox's width for a bare width.
func contentWidth(width int) int {
	if width < minInsetWidth {
		return width
	}
	return width - 2*ContentInset
}

// ListWidth is the persisted file-list width. Zero means "use the default 25%".
func (v *View) ListWidth() int { return v.listWidth }

// SetListWidth stores a persisted or drag-start width. Zero restores the default.
func (v *View) SetListWidth(w int) { v.listWidth = w }

// EffectiveListWidth is the width the divider would use for this leaf. The
// argument is the leaf box's width; the padding is taken off here so callers
// hold one meaning of "width" and the drag lands on the drawn divider.
func (v *View) EffectiveListWidth(leafWidth int) int {
	return v.resolvedListWidth(contentWidth(leafWidth))
}

// ApplyListWidthDelta adds dx to the current (or default) list width and clamps
// to min 20 / max leafWidth-30. Hosts that drag from a frozen start should
// SetListWidth(start) before each call with the total dx from that start.
func (v *View) ApplyListWidthDelta(dx, leafWidth int) int {
	base := v.listWidth
	if base <= 0 {
		base = defaultListWidth(contentWidth(leafWidth))
	}
	v.listWidth = clampListWidth(base+dx, contentWidth(leafWidth))
	return v.listWidth
}

// DividerHit is the 3-col drag target on the list/diff split, inside the leaf
// box only. Empty when the layout is collapsed or the box is too narrow.
func (v *View) DividerHit(leaf mouse.Rect) mouse.Rect {
	leaf = ContentBox(leaf)
	if leaf.W < CollapseThreshold || leaf.W < 1 || leaf.H < 1 {
		return mouse.Rect{}
	}
	if v.Scope == ScopeAggregate {
		return mouse.Rect{}
	}
	listW := v.resolvedListWidth(leaf.W)
	return mouse.Rect{X: leaf.X + listW, Y: leaf.Y, W: dividerHitCols, H: leaf.H}
}

// FileHits are the file/commit/pane targets inside the leaf. Collapsed mode
// still reports the visible list or pane. The host registers these.
func (v *View) FileHits(leaf mouse.Rect) []Hit {
	leaf = ContentBox(leaf)
	if leaf.W < 1 || leaf.H < 1 {
		return nil
	}
	if v.State == LoadStateLoading || v.State == LoadStateError {
		return nil
	}
	if v.Scope == ScopeAggregate {
		return nil
	}
	if leaf.W < CollapseThreshold {
		return v.collapsedHits(leaf)
	}
	listW := v.resolvedListWidth(leaf.W)
	diffW := leaf.W - listW - 1
	if diffW < 10 {
		diffW = 10
	}
	listBox := mouse.Rect{X: leaf.X, Y: leaf.Y, W: listW, H: leaf.H}
	diffBox := mouse.Rect{X: leaf.X + listW + 1, Y: leaf.Y, W: diffW, H: leaf.H}
	if v.Focus == FocusCommitFiles || v.Focus == FocusCommitDiff {
		return append(v.commitFileHits(listBox), v.commitDiffHits(diffBox)...)
	}
	return append(v.fileListHits(listBox), v.diffPaneHits(diffBox)...)
}

func (v *View) collapsedHits(leaf mouse.Rect) []Hit {
	switch v.Focus {
	case FocusDiff:
		return v.diffPaneHits(leaf)
	case FocusCommitFiles:
		return v.commitFileHits(leaf)
	case FocusCommitDiff:
		return v.commitDiffHits(leaf)
	default:
		return v.fileListHits(leaf)
	}
}

func (v *View) fileListHits(box mouse.Rect) []Hit {
	hits := []Hit{{ID: RegionFileListPane, Rect: box}}
	linesUsed := 1
	files := v.FileCount()
	visible := v.fileListVisibleHeight()
	start := v.Scroll
	if start < 0 {
		start = 0
	}
	end := start + visible
	if end > files {
		end = files
	}
	for i := start; i < end && linesUsed < box.H; i++ {
		hits = append(hits, Hit{
			ID:   RegionFile,
			Rect: mouse.Rect{X: box.X, Y: box.Y + linesUsed, W: box.W, H: 1},
			Data: i,
		})
		linesUsed++
	}
	if len(v.Commits) == 0 {
		return hits
	}
	linesUsed += 2 // divider + "Commits (N)"
	for i := range v.Commits {
		if linesUsed >= box.H {
			break
		}
		hits = append(hits, Hit{
			ID:   RegionCommit,
			Rect: mouse.Rect{X: box.X, Y: box.Y + linesUsed, W: box.W, H: 1},
			Data: files + i,
		})
		linesUsed++
	}
	return hits
}

func (v *View) diffPaneHits(box mouse.Rect) []Hit {
	hits := []Hit{{ID: RegionDiffPane, Rect: box}}
	if v.Cursor >= v.FileCount() {
		if v.CommitDetail != nil {
			y := 4 // title + subject + blank + "Files (N)"
			for i := range v.CommitDetail.Files {
				if y >= box.H {
					break
				}
				hits = append(hits, Hit{
					ID:   RegionPreviewFile,
					Rect: mouse.Rect{X: box.X, Y: box.Y + y, W: box.W, H: 1},
					Data: i,
				})
				y++
			}
		}
		return hits
	}
	return hits
}

func (v *View) commitFileHits(box mouse.Rect) []Hit {
	hits := []Hit{{ID: RegionFileListPane, Rect: box}}
	if v.CommitDetail == nil {
		return hits
	}
	hits = append(hits, Hit{ID: RegionCommitBack, Rect: mouse.Rect{X: box.X, Y: box.Y, W: box.W, H: 1}})
	visible := box.H - commitFileListHeaderLines
	if visible < 1 {
		visible = 1
	}
	start := v.CommitFileScroll
	if start < 0 {
		start = 0
	}
	end := start + visible
	if end > len(v.CommitDetail.Files) {
		end = len(v.CommitDetail.Files)
	}
	for i := start; i < end; i++ {
		hits = append(hits, Hit{
			ID: RegionCommitFile,
			Rect: mouse.Rect{
				X: box.X,
				Y: box.Y + commitFileListHeaderLines + (i - start),
				W: box.W,
				H: 1,
			},
			Data: i,
		})
	}
	return hits
}

func (v *View) commitDiffHits(box mouse.Rect) []Hit {
	return []Hit{{ID: RegionCommitDiff, Rect: box}}
}

func (v *View) resolvedListWidth(total int) int {
	w := v.listWidth
	if w <= 0 {
		w = defaultListWidth(total)
	}
	return clampListWidth(w, total)
}

func defaultListWidth(total int) int {
	w := total * defaultListShare / 100
	return clampListWidth(w, total)
}

func clampListWidth(w, total int) int {
	if w < listMinWidth {
		w = listMinWidth
	}
	maxW := total - listMaxReserve
	if maxW < listMinWidth {
		maxW = listMinWidth
	}
	if w > maxW {
		w = maxW
	}
	return w
}
