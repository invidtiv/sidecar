package tabs

import (
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/ui"
)

// CloseHover is the per-tab × the pointer is over. LeafID is 0 on surfaces
// that draw a single strip; the zero value means no × is hovered, so a
// surface that never sets one renders exactly as before.
type CloseHover struct {
	LeafID int
	Index  int
	On     bool
}

// CloseHoverAt names one leaf's tab.
func CloseHoverAt(leafID, index int) CloseHover {
	return CloseHover{LeafID: leafID, Index: index, On: true}
}

// IndexFor is the hovered tab index within leafID's strip, or -1 when the
// pointer is somewhere else. Hosts pass the result straight to HoverClose.
func (h CloseHover) IndexFor(leafID int) int {
	if !h.On || h.LeafID != leafID {
		return -1
	}
	return h.Index
}

// Index0For is IndexFor for a surface that draws one strip and tracks no leaf.
func (h CloseHover) Index0For() int { return h.IndexFor(0) }

// HoverClose repaints one tab's × as hovered. It rewrites only the cells the
// × already occupies, so the row keeps its width and every Hit — including
// the close target the pointer is inside — stays where it was registered.
// An index that is not painted, or a tab too narrow to carry a ×, is a no-op.
func (s Strip) HoverClose(index int) Strip {
	if index < 0 || s.Row == "" {
		return s
	}
	for _, tab := range s.Tabs {
		if tab.Index != index || tab.CloseW < 1 {
			continue
		}
		s.Row = replaceCells(s.Row, tab.CloseCol, tab.CloseW, ui.RenderTabCloseHover(tab.CloseW))
		return s
	}
	return s
}

// replaceCells swaps the cells [col, col+width) of a rendered row for paint,
// which must itself be width columns.
func replaceCells(row string, col, width int, paint string) string {
	if col < 0 || width < 1 {
		return row
	}
	head := ansi.Truncate(row, col, "")
	tail := ansi.TruncateLeft(row, col+width, "")
	return head + paint + tail
}
