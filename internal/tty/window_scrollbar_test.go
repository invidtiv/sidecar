package tty

import (
	"testing"

	"github.com/marcus/sidecar/internal/scroll"
)

func TestWindowScrollbarReadsTheDrawnWindow(t *testing.T) {
	layout := Viewport{EffectiveCount: 100, DisplayHeight: 20, Start: 35, AbsoluteStart: 135}
	sb := WindowScrollbarFor(layout, 250)
	if sb.Total != 250 || sb.Offset != 135 || sb.Visible != 20 || sb.Track != 20 {
		t.Fatalf("got %+v", sb)
	}
	// AbsoluteStart 135 with buffer-relative Start 35 means the base is 100:
	// the translation back to window coordinates must land on 35 again.
	if sb.Base != 100 {
		t.Fatalf("Base = %d, want 100", sb.Base)
	}
}

func TestWindowScrollbarHasThumbFollowsTheRenderer(t *testing.T) {
	fits := WindowScrollbarFor(Viewport{EffectiveCount: 10, DisplayHeight: 20}, 10)
	if fits.HasThumb() {
		t.Fatal("a window that fits must report no thumb")
	}
	overflows := WindowScrollbarFor(Viewport{EffectiveCount: 40, DisplayHeight: 20}, 40)
	if !overflows.HasThumb() {
		t.Fatal("an overflowing window must report a thumb")
	}
}

func TestWindowScrollbarStartAtTrackRowAnchorsLikeTheRenderer(t *testing.T) {
	layout := Viewport{EffectiveCount: 200, DisplayHeight: 25, Start: 0, AbsoluteStart: 50}
	// The buffer covers absolute rows 50..249, so the history summary must claim
	// at least that much scrollback.
	sb := WindowScrollbarFor(layout, 250)

	// The shared core quantizes many offsets onto one track row and maps a row
	// back to the smallest offset whose thumb top covers it, so the round trip
	// anchors at or below the queried start — never above it — and is exact at
	// both ends of the travel. That is the same macOS jump-to-spot behaviour
	// every list surface inherits from this math.
	for _, start := range []int{0, 1, 87, 175} {
		row := sb.RowForStart(start)
		got := sb.StartAtTrackRow(row)
		if got > start {
			t.Fatalf("start %d -> row %d -> start %d (anchored above)", start, row, got)
		}
	}
	if got := sb.StartAtTrackRow(sb.RowForStart(0)); got != 0 {
		t.Fatalf("live edge anchored at %d, want 0", got)
	}
	bottom := sb.StartAtTrackRow(sb.Track - 1)
	if bottom < 174 || bottom > 175 {
		t.Fatalf("bottom track row = %d, want the oldest start", bottom)
	}
}

func TestWindowScrollbarStartAtTrackRowClampsInsideTheBuffer(t *testing.T) {
	// A base of 50 says rows 0..49 belong to history tmux has not handed over
	// yet; a track row mapping into them must floor at buffer-relative zero.
	layout := Viewport{EffectiveCount: 60, DisplayHeight: 20, Start: 0, AbsoluteStart: 50}
	sb := WindowScrollbarFor(layout, 110)
	if got := sb.StartAtTrackRow(0); got != 0 {
		t.Fatalf("top track row = %d, want 0", got)
	}
	loc := scroll.ThumbLocFor(sb.Total, sb.Offset, sb.Visible, sb.Track)
	if got := sb.StartAtTrackRow(loc.Pos); got < 0 {
		t.Fatalf("thumb-top row mapped below the buffer: %d", got)
	}
}
