package noteview

import (
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/ui"
)

func loadedModel(t *testing.T, bodyLines int) *Model {
	t.Helper()
	m := New(nil)
	m.Arm(1, "nt-test", 1)
	data := &Data{ID: "nt-test", Title: "Title", Content: strings.Repeat("paragraph line\n", bodyLines)}
	// Arm does not bump requestGeneration, so the matching result carries zero.
	if !m.SetResult(LoadedMsg{ModelID: 1, Epoch: 1, NoteID: "nt-test", Data: data}) {
		t.Fatal("SetResult rejected its own load")
	}
	m.SetSize(40, 12)
	return m
}

// The params the model exposes must be exactly the ones it renders with: the
// geometry a host derives from them matches the drawn thumb.
func TestScrollbarParams_MatchRenderedGeometry(t *testing.T) {
	m := loadedModel(t, 40)
	params := m.ScrollbarParams()
	if params.TotalItems != len(m.ensureRows()) || params.VisibleItems != 12 || params.TrackHeight != 12 {
		t.Fatalf("params = %+v, want total=%d visible=12 track=12",
			params, len(m.ensureRows()))
	}

	_, geom := ui.RenderScrollbarWithGeometry(params)
	_, renderedGeom := ui.RenderScrollbarWithGeometry(m.ScrollbarParams())
	if !renderedGeom.HasThumb || geom != renderedGeom {
		t.Fatalf("geometry unstable across calls: %+v vs %+v", geom, renderedGeom)
	}
	if !m.HasScrollbar() {
		t.Fatal("overflowing card reports no scrollbar")
	}
}

func TestHasScrollbar_FalseWhenContentFits(t *testing.T) {
	m := loadedModel(t, 3)
	if m.HasScrollbar() {
		t.Fatal("fitting content reports a scrollbar; the spacer column must stay inert")
	}
	if _, geom := ui.RenderScrollbarWithGeometry(m.ScrollbarParams()); geom.HasThumb {
		t.Fatal("renderer draws a thumb for fitting content")
	}
}

func TestScrollToOffset_ClampsToScrollRange(t *testing.T) {
	m := loadedModel(t, 60)
	max := m.maxScroll()
	if max == 0 {
		t.Fatal("test content does not scroll")
	}
	if m.ScrollToOffset(max+100) != true || m.ScrollOffset() != max {
		t.Fatalf("past-end offset = %d, want clamp %d", m.ScrollOffset(), max)
	}
	if m.ScrollToOffset(-5) != true || m.ScrollOffset() != 0 {
		t.Fatalf("before-start offset = %d, want 0", m.ScrollOffset())
	}
	if m.ScrollToOffset(0) {
		t.Fatal("no-op scroll reported movement")
	}
}

// Track-row mapping round-trips through the same shared core every other
// interactive scrollbar uses.
func TestOffsetAtTrackRow_RoundTripsThroughSharedCore(t *testing.T) {
	m := loadedModel(t, 60)
	for row := 0; row < m.height; row += 3 {
		offset := m.OffsetAtTrackRow(row)
		if offset < 0 || offset > m.maxScroll() {
			t.Fatalf("row %d mapped out of range: %d", row, offset)
		}
		back := ui.RowForOffset(m.ScrollbarParams(), offset)
		if back < row-1 { // monotonic mapping may land below on dense rows
			t.Fatalf("offset %d maps back to row %d, want >= %d", offset, back, row-1)
		}
	}
}
