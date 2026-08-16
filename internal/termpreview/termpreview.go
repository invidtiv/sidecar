// Package termpreview is the presentation layer shared by every Sidecar
// surface that puts a list beside an embedded terminal.
//
// It sits *beneath* the pane tree, not beside it. What lives here is the
// arithmetic the surfaces were doing inline — the outer sidebar/preview split,
// the one header row above every terminal, and the rendering of a pane's body
// into a box, captured or live.
//
// "A second independent geometry computation" is the failure mode the doc-panes
// plan names, and it is guarded structurally rather than by keeping surfaces
// simple: both the project Workspaces plugin and the global Workspaces browser
// ("Sessions") instantiate a pane tree, and both place it with
// internal/panelayout and draw it with internal/paneframe. Nothing computes a
// pane's chrome, border, handle or hit box for itself.
//
// Nothing here reads tmux, owns a buffer, or forwards input. A consumer hands in
// an immutable Snapshot, or a body some terminal component already rendered, and
// gets back rows of text.
package termpreview

import "github.com/marcus/sidecar/internal/mouse"

// HeaderRows is the single row every embedded terminal reserves above its
// viewport for identity chips and hints. It is one row for every surface, so a
// terminal always begins on the row immediately below its box's first row.
const HeaderRows = 1

// Box is a rectangle in surface-local coordinates. It is the currency between
// the pane tree and this package: `LayoutPanes` produces boxes, and everything
// here consumes them.
//
// It is an alias of mouse.Rect, not a separate struct: a box and a hit region
// are the same rectangle, so the layout that draws a pane and the hit map that
// receives its clicks cannot disagree about the geometry.
type Box = mouse.Rect

// Surface locates one embedded terminal inside a box and reports the size of
// its viewport. It is the box minus its header row — no more, no less, so a
// caller cannot disagree with the layout about which row the terminal starts on.
type Surface struct {
	X int // first content column
	Y int // first content row, one below HeaderY
	// HeaderY is the row the chips-and-hints header is drawn on.
	HeaderY int
	Width   int // terminal content columns
	Height  int // terminal content rows, header row excluded
	OK      bool
}

// SurfaceIn places a terminal inside a leaf box. The box includes the header
// row; the viewport is everything below it.
func SurfaceIn(box Box) Surface {
	if box.W <= 0 || box.H <= 0 {
		return Surface{}
	}
	return Surface{
		X:       box.X,
		Y:       box.Y + HeaderRows,
		HeaderY: box.Y,
		Width:   box.W,
		Height:  box.H - HeaderRows,
		OK:      true,
	}
}

// SplitConfig describes the outer two-pane split: a list on the left and a
// preview region on the right. It is deliberately the *outer* split only — how
// the preview region is subdivided afterwards (a pane tree in the project
// plugin, nothing at all in the global browser) is the consumer's business.
type SplitConfig struct {
	// SidebarVisible is false when the list is hidden and the preview owns the
	// whole width.
	SidebarVisible bool
	// SidebarPercent is the requested sidebar share of the available columns.
	SidebarPercent int
	// DividerWidth is the columns between the two panes.
	DividerWidth int
	// PanelOverhead is the columns a panel's border and padding consume, and
	// ContentInset the columns of that overhead on its left. A consumer that
	// draws no panel chrome passes zero for both.
	PanelOverhead int
	ContentInset  int
	// SidebarMin / PreviewMin are the floors: the sidebar never shrinks past
	// SidebarMin and never grows so far that the preview drops below PreviewMin.
	SidebarMin, PreviewMin int
}

// Split is the horizontal split of a viewport into sidebar, divider, and
// preview panel, in surface-local columns.
type Split struct {
	SidebarWidth        int // outer width of the sidebar panel; 0 when hidden
	SidebarContentWidth int // sidebar width inside border + padding
	PreviewX            int // outer x of the preview panel
	PreviewWidth        int // outer width of the preview panel
	ContentX            int // x of the preview panel's first content column
	ContentWidth        int // preview width inside border + padding
}

// SplitFor computes the split for an explicit viewport width.
func SplitFor(width int, cfg SplitConfig) Split {
	if !cfg.SidebarVisible {
		return Split{
			PreviewWidth: width,
			ContentX:     cfg.ContentInset,
			ContentWidth: width - cfg.PanelOverhead,
		}
	}

	// Panel borders are handled by the renderer, so only the divider comes off
	// the available space here.
	available := width - cfg.DividerWidth
	sidebar := (available * cfg.SidebarPercent) / 100
	if sidebar < cfg.SidebarMin {
		sidebar = cfg.SidebarMin
	}
	if sidebar > available-cfg.PreviewMin {
		sidebar = available - cfg.PreviewMin
	}
	preview := available - sidebar
	if preview < cfg.PreviewMin {
		preview = cfg.PreviewMin
	}
	previewX := sidebar + cfg.DividerWidth
	return Split{
		SidebarWidth:        sidebar,
		SidebarContentWidth: sidebar - cfg.PanelOverhead,
		PreviewX:            previewX,
		PreviewWidth:        preview,
		ContentX:            previewX + cfg.ContentInset,
		ContentWidth:        preview - cfg.PanelOverhead,
	}
}
