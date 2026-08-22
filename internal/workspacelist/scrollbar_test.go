package workspacelist

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/ui"
)

// scrollbarSections builds one headed section of n two-line rows, long enough
// to overflow any pane these tests render.
func scrollbarSections(n int) []SidebarSection {
	rows := make([]SidebarRow, 0, n)
	for i := range n {
		id := fmt.Sprintf("row-%02d", i)
		rows = append(rows, testSidebarRow(id, fmt.Sprintf("item %02d", i), id))
	}
	return []SidebarSection{{Title: "Shells", Rows: rows}}
}

// interactiveOpts is a pane with more rows than body and the bar opted in.
func interactiveOpts() SidebarOptions {
	return SidebarOptions{
		Width: 30, Height: 14, Title: "Workspaces",
		SelectedID:           "row-00",
		Sections:             scrollbarSections(24),
		InteractiveScrollbar: true,
	}
}

func barRegions(rendered SidebarRendered) (track, thumb *Region) {
	for i := range rendered.Regions {
		switch r := &rendered.Regions[i]; r.Kind {
		case RegionScrollbarTrack:
			track = r
		case RegionScrollbarThumb:
			thumb = r
		}
	}
	return track, thumb
}

// The opted-in bar reports its geometry and appends its two regions after
// every content region, so the reverse scan that callers inherit prefers the
// bar in the column it owns.
func TestInteractiveSidebarEmitsBarRegionsAfterContent(t *testing.T) {
	rendered := RenderSidebar(interactiveOpts())
	if !rendered.Scrollbar.Has {
		t.Fatal("overflowing list reported no scrollbar")
	}
	var track, thumb *Region
	lastContent := -1
	for i := range rendered.Regions {
		r := &rendered.Regions[i]
		if IsScrollbarRegion(r.Kind) {
			switch r.Kind {
			case RegionScrollbarTrack:
				track = r
			case RegionScrollbarThumb:
				thumb = r
			}
			continue
		}
		lastContent = i
	}
	if track == nil || thumb == nil {
		t.Fatalf("bar regions missing: track=%#v thumb=%#v", track, thumb)
	}
	if lastContent > -1 && (lastContent > indexOfRegion(rendered.Regions, track) || lastContent > indexOfRegion(rendered.Regions, thumb)) {
		t.Fatal("a bar region was registered before a content region")
	}
	bar := rendered.Scrollbar
	if track.X != renderedBodyX(30) || track.W != 1 || track.H != bar.Params.TrackHeight {
		t.Fatalf("track region %#v does not match the drawn bar %#v", track, bar)
	}
	if thumb.Y != track.Y+bar.ThumbTop || thumb.H != bar.ThumbH || thumb.H < 1 {
		t.Fatalf("thumb region %#v does not match the drawn bar %#v", thumb, bar)
	}
	if thumb.Y < track.Y || thumb.Y+thumb.H > track.Y+track.H {
		t.Fatalf("thumb %#v sits outside its track %#v", thumb, track)
	}

	// The rects are where the glyphs are: the registered column is the drawn
	// column, or every coordinate above is fiction.
	lines := strings.Split(ansi.Strip(rendered.View), "\n")
	x := track.X
	for row := 0; row < bar.Params.TrackHeight; row++ {
		got := string([]rune(lines[track.Y+row])[x])
		want := "│"
		if row >= bar.ThumbTop && row < bar.ThumbTop+bar.ThumbH {
			want = "┃"
		}
		if got != want {
			t.Fatalf("bar cell (%d,%d) = %q, want %q", x, track.Y+row, got, want)
		}
	}
}

func indexOfRegion(regions []Region, target *Region) int {
	for i := range regions {
		if &regions[i] == target {
			return i
		}
	}
	return -1
}

// A press on the bar's column resolves to the bar, never to a row underneath —
// at the shared resolver every surface's hit map inherits.
func TestScrollbarRegionsWinOverRows(t *testing.T) {
	rendered := RenderSidebar(interactiveOpts())
	track, _ := barRegions(rendered)
	rowY := rendered.Scrollbar.Params.TrackHeight / 2

	hit, ok := RegionAt(rendered.Regions, track.X, track.Y+rowY)
	if !ok {
		t.Fatal("nothing hit in the bar column")
	}
	if !IsScrollbarRegion(hit.Kind) {
		t.Fatalf("hit %q in the bar column, want a scrollbar region", hit.Kind)
	}

	// The rows painted beside that very row still resolve as themselves one
	// column over, so the win is scoped to the bar's column.
	rowHit, ok := RegionAt(rendered.Regions, 2, track.Y+rowY)
	if !ok || rowHit.Kind != RegionRow {
		t.Fatalf("content column hit %#v, want a row", rowHit)
	}
}

// Content that fits draws only the anti-jitter spacer and registers nothing,
// even when the surface opted in.
func TestNoScrollbarRegionsWhenContentFits(t *testing.T) {
	opts := SidebarOptions{
		Width: 30, Height: 20, Title: "Workspaces",
		Sections:             scrollbarSections(3),
		InteractiveScrollbar: true,
	}
	rendered := RenderSidebar(opts)
	if rendered.Scrollbar.Has {
		t.Fatal("fitting content reported a scrollbar")
	}
	if track, thumb := barRegions(rendered); track != nil || thumb != nil {
		t.Fatalf("regions registered for fitting content: %#v %#v", track, thumb)
	}
}

// A free-scrolled offset survives the render instead of being dragged back to
// the selection; the same offset without the latch is re-derived as before.
func TestRenderSidebarHonorsFreeScroll(t *testing.T) {
	base := SidebarOptions{
		Width: 30, Height: 14, Title: "Workspaces",
		SelectedID: "row-00",
		Sections:   scrollbarSections(24),
	}

	free := base
	free.ScrollOffset = 6
	free.FreeScroll = true
	if got := RenderSidebar(free).ScrollOffset; got != 6 {
		t.Errorf("free-scroll offset %d did not survive the render, want 6", got)
	}

	following := base
	following.ScrollOffset = 6
	if got := RenderSidebar(following).ScrollOffset; got != 0 {
		t.Errorf("selection-following offset = %d, want 0 so row-00 stays visible", got)
	}
}

// The Model latches free-scroll for pointer gestures, and any selection move
// clears it again — keyboard and wheel resume following.
func TestModelSetScrollViewportLatchesUntilSelectionMoves(t *testing.T) {
	m := &Model{}
	items := make([]Item, 0, 24)
	for i := range 24 {
		items = append(items, Item{ID: fmt.Sprintf("row-%02d", i), Name: fmt.Sprintf("item %02d", i)})
	}
	m.SetItems(items)
	opts := RenderOptions{Width: 30, Height: 14, Title: "Workspaces"}
	m.Render(opts)

	const dragged = 8
	m.SetScrollViewport(dragged)
	m.Render(opts)
	if got := m.ScrollOffset(); got != dragged {
		t.Fatalf("viewport = %d after a gesture put it at %d", got, dragged)
	}

	// Keyboard movement owns the viewport again.
	if !m.Move(1) {
		t.Fatal("the selection could not move")
	}
	m.Render(opts)
	if got := m.ScrollOffset(); got >= dragged {
		t.Fatalf("viewport = %d after the selection moved, want it following again (<%d)", got, dragged)
	}
}

// Hover lights the bar, and leaving it restores byte-identical idle output.
func TestIdleByteParityAcrossHoverRoundTrip(t *testing.T) {
	opts := interactiveOpts()
	idle := RenderSidebar(opts).View

	hovered := opts
	hovered.ScrollbarHover = true
	lit := RenderSidebar(hovered).View
	if lit == idle {
		t.Fatal("hover produced no visible emphasis")
	}

	again := opts
	again.ScrollbarDrag = true
	dragged := RenderSidebar(again).View
	if dragged == idle {
		t.Fatal("drag produced no visible emphasis")
	}

	if back := RenderSidebar(opts).View; back != idle {
		t.Fatal("idle output drifted after a hover round trip")
	}
}

// The shared gesture: a track press jumps so the grabbed point becomes the
// thumb anchor, the continuing drag maps the pointer straight onto rows, and
// dragging past either end clamps without ending anything.
func TestScrollGestureTrackClickAnchorsAndDrags(t *testing.T) {
	rendered := RenderSidebar(interactiveOpts())
	bar := rendered.Scrollbar
	params := bar.Params

	anchorRow := bar.ThumbTop + bar.ThumbH + 2
	trackY := 5 // arbitrary absolute origin; Press takes it explicitly
	offset := ui.OffsetAtRow(params, anchorRow)
	if offset == 0 {
		t.Fatalf("test setup: anchor row %d maps to offset 0; pick a lower anchor", anchorRow)
	}

	var g ScrollGesture
	got := g.Press(bar, trackY, trackY+anchorRow, false, 0)
	if got != offset {
		t.Errorf("track press scrolled to %d, want %d", got, offset)
	}
	if !g.Active() {
		t.Error("track press did not continue as a drag")
	}
	if row := g.DragTo(trackY + anchorRow); row != ui.OffsetAtRow(params, anchorRow) {
		t.Errorf("motion at the anchor row moved the offset to %d, want it anchored there", row)
	}
	above := g.DragTo(trackY + anchorRow - 3)
	if above >= offset {
		t.Errorf("dragging above the anchor left offset %d, want <%d", above, offset)
	}

	// Past both ends: clamped, still active.
	if top := g.DragTo(trackY - 50); top != 0 {
		t.Errorf("dragging far past the top left offset %d, want 0", top)
	}
	bottom := g.DragTo(trackY + params.TrackHeight + 50)
	if bottom != params.TotalItems-params.VisibleItems {
		t.Errorf("dragging far past the bottom left offset %d, want %d", bottom, params.TotalItems-params.VisibleItems)
	}
	if !g.Active() {
		t.Error("clamping ended the gesture")
	}

	// A thumb press grabs where it landed: no jump, drag preserves the grab.
	g.End()
	pressLocal := bar.ThumbTop + 2
	if got := g.Press(bar, trackY, trackY+pressLocal, true, 4); got != 4 {
		t.Errorf("thumb press moved the offset to %d, want it held at 4", got)
	}
	// Moving the pointer one row past its press point moves the offset by one
	// row's worth from offset 4's anchor — wherever in the thumb that is.
	anchor := ui.RowForOffset(bar.Params, 4)
	if row := g.DragTo(trackY + pressLocal + 1); row != ui.OffsetAtRow(params, anchor+1) {
		t.Errorf("thumb drag landed on %d, want the grabbed mapping %d", row, ui.OffsetAtRow(params, anchor+1))
	}
	g.End()
	if g.Active() {
		t.Error("gesture survived End")
	}
}
