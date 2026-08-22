package kanban

import (
	"fmt"
	"testing"

	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/ui"
)

// overflowBoard builds a board whose every lane overflows the viewport, so
// every lane draws an interactive bar.
func overflowBoard(lanes, cards int) Board {
	var b Board
	for i := 0; i < lanes; i++ {
		lane := Lane{ID: LaneID(fmt.Sprintf("lane-%d", i)), Label: fmt.Sprintf("L%d", i)}
		for n := 0; n < cards; n++ {
			lane.Cards = append(lane.Cards, Card{ID: fmt.Sprintf("l%d-c%d", i, n), Title: fmt.Sprintf("card %d", n)})
		}
		b.Lanes = append(b.Lanes, lane)
	}
	return b
}

func boardRenderOptions() RenderOptions {
	return RenderOptions{Width: 60, Height: 15, Header: "Board", MinColumnWidth: 16, CardHeight: 4}
}

// hitMapFrom registers a render pass's regions in slice order, exactly as the
// consumers do with RenderResult.Regions.
func hitMapFrom(result RenderResult) *mouse.HitMap {
	hm := mouse.NewHitMap()
	for _, region := range result.Regions {
		hm.AddRect("kanban", region.X, region.Y, region.W, region.H, region)
	}
	return hm
}

func barRegion(t *testing.T, result RenderResult, kind RegionKind, column int) HitRegion {
	t.Helper()
	for _, region := range result.Regions {
		if region.Kind == kind && region.Column == column {
			return region
		}
	}
	t.Fatalf("no %s region for column %d in %#v", kind, column, result.Regions)
	return HitRegion{}
}

func TestLaneBarsCoexistInOneHitMap(t *testing.T) {
	const lanes = 3
	var c Component
	c.SetBoard(overflowBoard(lanes, 9))
	result := c.Render(boardRenderOptions())

	counts := map[RegionKind]int{}
	lastCard, firstBar := -1, len(result.Regions)
	for i, region := range result.Regions {
		switch region.Kind {
		case RegionScrollbarThumb, RegionScrollbarTrack:
			counts[region.Kind]++
			firstBar = min(firstBar, i)
		case RegionCard:
			lastCard = i
		}
	}
	if counts[RegionScrollbarThumb] != lanes || counts[RegionScrollbarTrack] != lanes {
		t.Fatalf("bar regions = %#v, want %d thumbs and %d tracks per board", counts, lanes, lanes)
	}
	if firstBar < lastCard {
		t.Fatalf("bar region at %d registered before card region at %d", firstBar, lastCard)
	}

	hm := hitMapFrom(result)
	for column := 0; column < lanes; column++ {
		thumb := barRegion(t, result, RegionScrollbarThumb, column)
		track := barRegion(t, result, RegionScrollbarTrack, column)
		if thumb.X != track.X || thumb.W != 1 || track.W != 1 {
			t.Fatalf("column %d bars not a single column wide: thumb=%#v track=%#v", column, thumb, track)
		}
		// The thumb answers its own rows, the track answers rows below it,
		// and both answer with their own lane — never a neighbour's.
		for _, probe := range []struct {
			y    int
			kind RegionKind
		}{
			{thumb.Y, RegionScrollbarThumb},
			{thumb.Y + thumb.H - 1, RegionScrollbarThumb},
			{thumb.Y + thumb.H, RegionScrollbarTrack},
			{track.Y + track.H - 1, RegionScrollbarTrack},
		} {
			hit := hm.Test(thumb.X, probe.y)
			if hit == nil {
				t.Fatalf("column %d: nothing hit at (%d,%d)", column, thumb.X, probe.y)
			}
			got, ok := hit.Data.(HitRegion)
			if !ok || got.Kind != probe.kind || got.Column != column {
				t.Fatalf("column %d: hit at (%d,%d) = %#v, want %s of column %d", column, thumb.X, probe.y, hit.Data, probe.kind, column)
			}
		}
	}
}

func TestDraggingOneLaneLeavesOthersAlone(t *testing.T) {
	var c Component
	c.SetBoard(overflowBoard(3, 9))
	c.Select(Selection{Column: 1}) // selection deliberately not in the dragged lane
	result := c.Render(boardRenderOptions())
	thumb := barRegion(t, result, RegionScrollbarThumb, 0)

	if !c.PressScrollbar(thumb, thumb.Y) {
		t.Fatal("thumb press was rejected")
	}
	if !c.DraggingScrollbar() {
		t.Fatal("no gesture after thumb press")
	}
	if !c.DragScrollbar(thumb.Y + 4) {
		t.Fatal("drag did not move the lane")
	}
	if got := c.scroll["lane-0"]; got != 4 {
		t.Fatalf("lane-0 offset = %d, want 4", got)
	}
	if c.scroll["lane-1"] != 0 || c.scroll["lane-2"] != 0 {
		t.Fatalf("other lanes moved: %#v", c.scroll)
	}
	if sel := c.Selection(); sel != (Selection{Column: 1, Row: 0}) {
		t.Fatalf("selection disturbed by lane 0 drag: %#v", sel)
	}

	next := c.Render(boardRenderOptions())
	if got := barRegion(t, next, RegionScrollbarThumb, 0).Y; got != thumb.Y+4 {
		t.Fatalf("re-rendered thumb Y = %d, want %d", got, thumb.Y+4)
	}

	// Past either end clamps without ending the gesture; release settles and
	// holds the offsets.
	c.DragScrollbar(thumb.Y + 999)
	if got := c.scroll["lane-0"]; got != 7 {
		t.Fatalf("past-bottom offset = %d, want clamp at 7", got)
	}
	if !c.DraggingScrollbar() {
		t.Fatal("clamping ended the gesture")
	}
	c.DragScrollbar(thumb.Y - 999)
	if got := c.scroll["lane-0"]; got != 0 {
		t.Fatalf("past-top offset = %d, want clamp at 0", got)
	}
	c.ReleaseScrollbar()
	if c.DraggingScrollbar() {
		t.Fatal("gesture survived release")
	}
	if c.scroll["lane-1"] != 0 || c.scroll["lane-2"] != 0 {
		t.Fatalf("other lanes moved across gesture: %#v", c.scroll)
	}

	// A drag on lane 2 must leave lane 0 wherever it was left.
	c.DragScrollbar(thumb.Y) // inert after release
	c.ScrollLane(0, 3)
	thumb2 := barRegion(t, c.Render(boardRenderOptions()), RegionScrollbarThumb, 2)
	if !c.PressScrollbar(thumb2, thumb2.Y) || !c.DragScrollbar(thumb2.Y+2) {
		t.Fatal("lane 2 gesture failed")
	}
	if got := c.scroll["lane-0"]; got != 3 {
		t.Fatalf("lane-0 offset = %d, want untouched 3", got)
	}
	if got := c.scroll["lane-2"]; got != 2 {
		t.Fatalf("lane-2 offset = %d, want 2", got)
	}
}

func TestTrackClickAnchorsPerLane(t *testing.T) {
	var c Component
	c.SetBoard(overflowBoard(3, 9))
	result := c.Render(boardRenderOptions())
	track := barRegion(t, result, RegionScrollbarTrack, 2)
	thumb := barRegion(t, result, RegionScrollbarThumb, 2)

	// First track row below the thumb: a genuine track press, not a thumb one.
	grabY := thumb.Y + thumb.H
	params := ui.ScrollbarParams{TotalItems: 9, ScrollOffset: 0, VisibleItems: 2, TrackHeight: track.H}
	want := ui.OffsetAtRow(params, grabY-track.Y)
	if !c.PressScrollbar(track, grabY) {
		t.Fatal("track press was rejected")
	}
	if got := c.scroll["lane-2"]; got != want {
		t.Fatalf("jump-to-spot offset = %d, want %d", got, want)
	}
	if c.scroll["lane-0"] != 0 || c.scroll["lane-1"] != 0 {
		t.Fatalf("other lanes moved by lane 2 jump: %#v", c.scroll)
	}

	// The jump anchors the gesture: grabRow is zero, so the continuing drag
	// maps the pointer straight onto track rows.
	if !c.DragScrollbar(track.Y + 5) {
		t.Fatal("post-jump drag did not move the lane")
	}
	if got := c.scroll["lane-2"]; got != ui.OffsetAtRow(params, 5) {
		t.Fatalf("anchored drag offset = %d, want %d", got, ui.OffsetAtRow(params, 5))
	}
	c.ReleaseScrollbar()

	// Re-rendering pins the thumb top back on the grabbed row while travel
	// matches maxOffset, and neighbours stay put.
	next := c.Render(boardRenderOptions())
	if got := barRegion(t, next, RegionScrollbarThumb, 2).Y - track.Y; got != 5 {
		t.Fatalf("re-rendered thumb row = %d, want 5", got)
	}
	if got := barRegion(t, next, RegionScrollbarThumb, 0).Y - track.Y; got != 0 {
		t.Fatalf("lane 0 thumb moved to %d, want 0", got)
	}
}

func TestBarRegionsWinOverCardsUnderneath(t *testing.T) {
	var c Component
	c.SetBoard(overflowBoard(3, 9))
	result := c.Render(boardRenderOptions())

	// A card rect spans its whole lane width, including the bar's column;
	// prove the overlap is real before proving the bar wins it.
	bar := barRegion(t, result, RegionScrollbarTrack, 1)
	probeY := bar.Y + 1
	overlaps := false
	for _, region := range result.Regions {
		if region.Kind == RegionCard && region.Column == 1 &&
			bar.X >= region.X && bar.X < region.X+region.W &&
			probeY >= region.Y && probeY < region.Y+region.H {
			overlaps = true
		}
	}
	if !overlaps {
		t.Fatal("card region did not reach the bar column; test lost its subject")
	}

	hm := hitMapFrom(result)
	hit := hm.Test(bar.X, probeY)
	got, ok := hit.Data.(HitRegion)
	if !ok || (got.Kind != RegionScrollbarThumb && got.Kind != RegionScrollbarTrack) {
		t.Fatalf("point under the bar resolved to %#v, want a scrollbar region", hit.Data)
	}
}

func TestFittingLanesRegisterNothingIndependently(t *testing.T) {
	var c Component
	board := overflowBoard(1, 9)
	fitsA := Lane{ID: "fits-a", Label: "A"}
	fitsA.Cards = append(fitsA.Cards, Card{ID: "fa-1"}, Card{ID: "fa-2"})
	fitsB := Lane{ID: "fits-b", Label: "B"}
	fitsB.Cards = append(fitsB.Cards, Card{ID: "fb-1"})
	board.Lanes = append(board.Lanes, fitsA, fitsB)
	c.SetBoard(board)

	result := c.Render(boardRenderOptions())
	for _, region := range result.Regions {
		if region.Kind == RegionScrollbarThumb || region.Kind == RegionScrollbarTrack {
			if region.Column != 0 {
				t.Fatalf("fitting lane %d registered a %s region", region.Column, region.Kind)
			}
		}
	}

	hm := hitMapFrom(result)
	for column := 1; column < 3; column++ {
		body := barRegion(t, result, RegionColumnBody, column)
		hit := hm.Test(body.X+body.W-1, body.Y+1) // the reserved spacer column
		got, ok := hit.Data.(HitRegion)
		if ok && (got.Kind == RegionScrollbarThumb || got.Kind == RegionScrollbarTrack) {
			t.Fatalf("spacer column of fitting lane %d hit as %s", column, got.Kind)
		}
	}

	// A synthesized press against a fitting lane is refused and moves nothing.
	before := c.scroll["fits-a"]
	if c.PressScrollbar(HitRegion{Kind: RegionScrollbarThumb, Column: 1}, 5) {
		t.Fatal("press accepted on a fitting lane")
	}
	if c.DraggingScrollbar() {
		t.Fatal("gesture started on a fitting lane")
	}
	if c.scroll["fits-a"] != before {
		t.Fatal("refused press still moved a lane")
	}
}

func TestIdleBytesStableAcrossHoverRoundTrip(t *testing.T) {
	var c Component
	c.SetBoard(overflowBoard(3, 9))
	c.Select(Selection{Column: 2}) // keep the selection out of every dragged/hovered lane
	idle := c.Render(boardRenderOptions()).View

	// Hover engages the emphasis hook — otherwise the round trip proves
	// nothing.
	thumb := barRegion(t, c.Render(boardRenderOptions()), RegionScrollbarThumb, 1)
	c.HandlePointer(PointerHover, thumb)
	hovered := c.Render(boardRenderOptions()).View
	if hovered == idle {
		t.Fatal("hovering the bar changed nothing; emphasis hooks are dead")
	}

	// Hover moving away restores idle output byte for byte, via a card, via
	// another bar, and via ClearHover.
	card := HitRegion{}
	for _, region := range c.Render(boardRenderOptions()).Regions {
		if region.Kind == RegionCard {
			card = region
			break
		}
	}
	c.HandlePointer(PointerHover, card)
	if got := c.Render(boardRenderOptions()).View; got != idle {
		t.Fatal("idle bytes differ after hovering away onto a card")
	}
	c.HandlePointer(PointerHover, barRegion(t, c.Render(boardRenderOptions()), RegionScrollbarTrack, 2))
	c.ClearHover()
	if got := c.Render(boardRenderOptions()).View; got != idle {
		t.Fatal("idle bytes differ after ClearHover")
	}

	// A full gesture round trip likewise settles back to identical idle bytes.
	thumb = barRegion(t, c.Render(boardRenderOptions()), RegionScrollbarThumb, 0)
	c.PressScrollbar(thumb, thumb.Y)
	dragging := c.Render(boardRenderOptions()).View
	if dragging == idle {
		t.Fatal("dragging the bar changed nothing; drag hooks are dead")
	}
	c.DragScrollbar(thumb.Y + 2)
	c.DragScrollbar(thumb.Y) // return to the starting offset
	c.ReleaseScrollbar()
	if got := c.Render(boardRenderOptions()).View; got != idle {
		t.Fatal("idle bytes differ after gesture round trip")
	}
}

func TestPointerEventsOnBarsNeverSelectCards(t *testing.T) {
	var c Component
	c.SetBoard(overflowBoard(2, 9))
	result := c.Render(boardRenderOptions())
	thumb := barRegion(t, result, RegionScrollbarThumb, 1)
	track := barRegion(t, result, RegionScrollbarTrack, 1)

	for _, region := range []HitRegion{thumb, track} {
		for _, kind := range []PointerKind{PointerClick, PointerDoubleClick, PointerHover} {
			if action := c.HandlePointer(kind, region); action.Kind != ActionNone {
				t.Fatalf("%s on %s produced %#v", kind, region.Kind, action)
			}
		}
	}
	if sel := c.Selection(); sel != (Selection{}) {
		t.Fatalf("selection = %#v, want untouched", sel)
	}
	if !c.PressScrollbar(thumb, thumb.Y) || !c.DraggingScrollbar() {
		t.Fatal("thumb press did not begin a gesture")
	}
}

func TestVanishedLaneGestureSettles(t *testing.T) {
	var c Component
	c.SetBoard(overflowBoard(3, 9))
	result := c.Render(boardRenderOptions())
	thumb := barRegion(t, result, RegionScrollbarThumb, 0)
	if !c.PressScrollbar(thumb, thumb.Y) {
		t.Fatal("thumb press was rejected")
	}
	before := cloneScroll(c.scroll)

	// A board refresh removes the grabbed lane mid-gesture.
	shrunk := Board{Lanes: overflowBoard(3, 9).Lanes[1:]}
	c.SetBoard(shrunk)

	if c.DragScrollbar(thumb.Y + 4) {
		t.Fatal("drag reported movement for a vanished lane")
	}
	if c.DraggingScrollbar() {
		t.Fatal("gesture survived its lane vanishing")
	}
	for id, got := range c.scroll {
		if want := before[id]; got != want {
			t.Fatalf("%s offset = %d, want untouched %d", id, got, want)
		}
	}
	if sel := c.Selection(); sel != (Selection{}) {
		t.Fatalf("selection = %#v, want untouched by the vanished lane", sel)
	}

	// The component keeps working: remaining lanes render bars and drag only
	// themselves.
	next := c.Render(boardRenderOptions())
	fresh := barRegion(t, next, RegionScrollbarThumb, 0)
	if !c.PressScrollbar(fresh, fresh.Y) || !c.DragScrollbar(fresh.Y+3) {
		t.Fatal("fresh gesture on surviving lane failed")
	}
	if got := c.scroll["lane-1"]; got != 3 {
		t.Fatalf("lane-1 offset = %d, want 3", got)
	}
	if got := c.scroll["lane-2"]; got != 0 {
		t.Fatalf("lane-2 offset = %d, want untouched 0", got)
	}
	c.ReleaseScrollbar()
}

func TestCompactModeInvalidatesBarsBetweenPressAndDrag(t *testing.T) {
	var c Component
	c.SetBoard(overflowBoard(3, 9))
	wide := boardRenderOptions()
	result := c.Render(wide)
	thumb := barRegion(t, result, RegionScrollbarThumb, 0)
	hovered := barRegion(t, result, RegionScrollbarThumb, 1)
	c.HandlePointer(PointerHover, hovered)
	if !c.PressScrollbar(thumb, thumb.Y) {
		t.Fatal("thumb press was rejected")
	}

	// Resize below the compact threshold between press and drag: snapshots
	// and hover are dropped with the bars they describe.
	narrow := wide
	narrow.Width = 40 // below MinimumWidth(3,16,4)=54
	if compact := c.Render(narrow); !compact.Compact {
		t.Fatalf("expected compact render, got %#v", compact.Regions)
	}
	if len(c.bars) != 0 {
		t.Fatalf("compact render kept %d bar snapshots", len(c.bars))
	}
	if c.barHover != "" {
		t.Fatalf("compact render kept hover on %q", c.barHover)
	}
	// Stale rects resolve to nothing while the snapshots are gone: replaying
	// them must not arm a gesture against whichever lane now owns that index.
	for _, region := range []HitRegion{thumb, hovered} {
		if c.PressScrollbar(region, region.Y+1) {
			t.Fatalf("stale %s press accepted in compact mode", region.Kind)
		}
	}

	// The live gesture itself survives the crossing, still bound to its lane.
	if !c.DragScrollbar(thumb.Y + 4) {
		t.Fatal("live gesture died crossing compact mode")
	}
	if got := c.scroll["lane-0"]; got != 4 {
		t.Fatalf("lane-0 offset = %d, want 4", got)
	}
	if c.scroll["lane-1"] != 0 || c.scroll["lane-2"] != 0 {
		t.Fatalf("other lanes moved across compact mode: %#v", c.scroll)
	}

	// Leaving compact rebuilds valid bars; mapping stays continuous and fresh
	// presses bind by the current layout, not stale state.
	exit := c.Render(wide)
	if exit.Compact || len(c.bars) != 3 {
		t.Fatalf("bars not rebuilt after compact: len=%d", len(c.bars))
	}
	params := ui.ScrollbarParams{TotalItems: 9, ScrollOffset: 4, VisibleItems: 2, TrackHeight: 8}
	if got, want := barRegion(t, exit, RegionScrollbarThumb, 0).Y, trackTopY()+ui.RowForOffset(params, 4); got != want {
		t.Fatalf("rebuilt thumb Y = %d, want %d", got, want)
	}
	c.ReleaseScrollbar()
	fresh := barRegion(t, exit, RegionScrollbarThumb, 1)
	if !c.PressScrollbar(fresh, fresh.Y) {
		t.Fatal("fresh press rejected after leaving compact")
	}
	if !c.DragScrollbar(fresh.Y + 2) {
		t.Fatal("fresh drag did not move its lane")
	}
	if got := c.scroll["lane-1"]; got != 2 {
		t.Fatalf("lane-1 offset = %d, want 2", got)
	}
	if got := c.scroll["lane-0"]; got != 4 {
		t.Fatalf("lane-0 offset = %d, want untouched 4", got)
	}
	c.ReleaseScrollbar()
}

func trackTopY() int { return 5 }

func TestDragMapsThroughPressTimeSnapshot(t *testing.T) {
	var c Component
	board := overflowBoard(3, 9)
	board.Lanes[0].Cards = board.Lanes[0].Cards[:5] // thumb is 3 rows tall here
	c.SetBoard(board)
	result := c.Render(boardRenderOptions())
	track := barRegion(t, result, RegionScrollbarTrack, 0)

	// Grab the middle of the fat thumb: one row of grab offset.
	midThumb := track.Y + 1
	if !c.PressScrollbar(barRegion(t, result, RegionScrollbarThumb, 0), midThumb) {
		t.Fatal("thumb press was rejected")
	}

	// Content changes under the pointer mid-gesture: the lane grows and an
	// external writer moves its live offset. Mapping must keep using the
	// params captured at press.
	grown := overflowBoard(3, 12)
	c.SetBoard(grown)
	c.ScrollLane(0, 3)
	if got := c.scroll["lane-0"]; got != 3 {
		t.Fatalf("setup: live offset = %d, want 3", got)
	}

	pressParams := ui.ScrollbarParams{TotalItems: 5, ScrollOffset: 0, VisibleItems: 2, TrackHeight: track.H}
	if !c.DragScrollbar(track.Y + 3) {
		t.Fatal("drag did not move the lane")
	}
	want := ui.OffsetAtRow(pressParams, 2) // pointer row minus the grabbed row
	if got := c.scroll["lane-0"]; got != want {
		t.Fatalf("offset = %d, want snapshot-mapped %d (live board would say 3)", got, want)
	}

	// Clamping still holds at both ends of the press-time track, without
	// ending the gesture.
	c.DragScrollbar(track.Y + 999)
	if got := c.scroll["lane-0"]; got != ui.OffsetAtRow(pressParams, track.H) {
		t.Fatalf("past-bottom offset = %d, want snapshot clamp", got)
	}
	if !c.DraggingScrollbar() {
		t.Fatal("clamping ended the gesture")
	}
	c.DragScrollbar(track.Y - 999)
	if got := c.scroll["lane-0"]; got != 0 {
		t.Fatalf("past-top offset = %d, want 0", got)
	}
	c.ReleaseScrollbar()
}

func cloneScroll(scroll map[LaneID]int) map[LaneID]int {
	out := make(map[LaneID]int, len(scroll))
	for id, value := range scroll {
		out[id] = value
	}
	return out
}
