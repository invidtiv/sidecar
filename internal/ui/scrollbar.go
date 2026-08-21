package ui

import (
	"image"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/scroll"
	"github.com/marcus/sidecar/internal/styles"
)

// Region IDs for interactive scrollbar hit testing. Register after content
// regions so the bar wins HitMap.Test's reverse scan; register neither when
// Geometry.HasThumb is false.
const (
	RegionScrollbarThumb = "scrollbar-thumb"
	RegionScrollbarTrack = "scrollbar-track"
)

// ScrollbarParams configures a vertical scrollbar rendering.
type ScrollbarParams struct {
	TotalItems   int // Total logical items in the list
	ScrollOffset int // Index of first visible item (scroll offset)
	VisibleItems int // Number of items that fit in the viewport
	TrackHeight  int // Height of the scrollbar track in terminal rows
}

// Geometry reports where a rendered scrollbar's track and thumb sit, in
// local coordinates with the track origin at (0, 0). TrackRect is always
// one column wide with height == TrackHeight. When HasThumb is false
// (everything fits, or there is no track) both rects are zero and handlers
// must ignore them.
type Geometry struct {
	TrackRect image.Rectangle
	ThumbRect image.Rectangle
	HasThumb  bool
}

// RenderScrollbar returns a single-column string (newline-separated)
// representing a vertical scrollbar track. Returns a column of spaces
// if all content is visible (TotalItems <= VisibleItems) to reserve
// the width and prevent layout jitter.
// Output has exactly TrackHeight lines, each 1 character wide.
func RenderScrollbar(params ScrollbarParams) string {
	rendered, _ := RenderScrollbarWithGeometry(params)
	return rendered
}

// RenderScrollbarWithGeometry renders exactly what RenderScrollbar renders
// and additionally reports the geometry needed to register pointer regions.
func RenderScrollbarWithGeometry(params ScrollbarParams) (string, Geometry) {
	return RenderScrollbarWithState(params, ScrollbarStyle{})
}

// ScrollbarStyle carries the pointer state of each part of a scrollbar,
// following the divider's HandleState convention.
type ScrollbarStyle struct {
	Thumb HandleState
	Track HandleState
}

// Hover/drag variants derive from the theme palette via intensity
// modulation rather than new theme keys, matching the plan default.
const (
	scrollbarHoverLighten = 0.20
	scrollbarDragLighten  = 0.35
)

// RenderScrollbarWithState renders the scrollbar with hover/drag emphasis.
// Idle styling is byte-identical to every other entry point.
func RenderScrollbarWithState(params ScrollbarParams, style ScrollbarStyle) (string, Geometry) {
	if params.TrackHeight < 1 {
		return "", Geometry{}
	}

	loc := scroll.ThumbLocFor(params.TotalItems, params.ScrollOffset, params.VisibleItems, params.TrackHeight)
	if !loc.Has {
		// No scrollbar needed — spacer column to prevent layout jitter.
		lines := make([]string, params.TrackHeight)
		for i := range lines {
			lines[i] = " "
		}
		return strings.Join(lines, "\n"), Geometry{}
	}

	theme := styles.GetCurrentTheme()
	trackChar := scrollbarPartStyle(styles.ScrollbarTrackColor, theme.Colors.ScrollbarTrack, style.Track).Render("│")
	thumbChar := scrollbarPartStyle(styles.ScrollbarThumbColor, theme.Colors.ScrollbarThumb, style.Thumb).Render("┃")

	lines := make([]string, params.TrackHeight)
	for i := range params.TrackHeight {
		if i >= loc.Pos && i < loc.Pos+loc.Size {
			lines[i] = thumbChar
		} else {
			lines[i] = trackChar
		}
	}

	return strings.Join(lines, "\n"), Geometry{
		TrackRect: image.Rect(0, 0, 1, params.TrackHeight),
		ThumbRect: image.Rect(0, loc.Pos, 1, loc.Pos+loc.Size),
		HasThumb:  true,
	}
}

// OffsetAtRow maps a track row to the scroll offset whose thumb top anchors
// there, clamped to [0, TotalItems-VisibleItems]. Returns 0 on a track with
// no thumb. Track clicks pass the pressed row; thumb drags pass row minus
// the grab offset within the thumb.
func OffsetAtRow(params ScrollbarParams, row int) int {
	return scroll.OffsetAtRow(params.TotalItems, params.VisibleItems, params.TrackHeight, row)
}

// RowForOffset maps a scroll offset to the track row where the renderer
// places the thumb top, clamped to [0, TrackHeight-1].
func RowForOffset(params ScrollbarParams, offset int) int {
	return scroll.RowForOffset(params.TotalItems, params.VisibleItems, params.TrackHeight, offset)
}

func scrollbarPartStyle(idle color.Color, baseHex string, state HandleState) lipgloss.Style {
	col := idle
	switch state {
	case HandleHover:
		if baseHex != "" {
			col = lipgloss.Color(styles.Lighten(baseHex, scrollbarHoverLighten))
		}
	case HandleDrag:
		if baseHex != "" {
			col = lipgloss.Color(styles.Lighten(baseHex, scrollbarDragLighten))
		}
	}
	return lipgloss.NewStyle().Foreground(col)
}
