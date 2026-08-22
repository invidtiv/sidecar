package workspace

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/noteview"
	"github.com/marcus/sidecar/internal/ui"
)

// notePaneOverflowPlugin opens one note pane and arms its tab with a fetched
// body far taller than any pane viewport, then renders the frame so its bar
// regions are registered.
func notePaneOverflowPlugin(t *testing.T, noteID string) (*Plugin, *noteview.Model) {
	t.Helper()
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	surfaceRoot, surface, ok := p.selectedTerminalSurface()
	if !ok {
		t.Fatal("no selected surface")
	}
	// Opening arms the tab and issues its fetch; deliver an overflowing result
	// through the same seam the loop would, without running the fetch itself.
	if p.openNotePaneForSurface(surfaceRoot, surface, noteID) == nil {
		t.Fatal("opening a note produced no command")
	}
	note, _ := p.activeNotePane()
	if note == nil || note.view() == nil {
		t.Fatal("no active note pane")
	}
	view := note.view()
	content := strings.Repeat("line one of the note\n", 200)
	if noteID == "nt-short" {
		content = "one line"
	}
	if !view.SetResult(noteview.LoadedMsg{
		ModelID: view.ModelID(), RequestGeneration: 1, Epoch: p.ctx.Epoch,
		NoteID: noteID,
		Data:   &noteview.Data{ID: noteID, Title: "A note", Content: content},
	}) {
		t.Fatal("load result rejected: identity mismatch")
	}
	if got := p.View(140, 36); got == "" {
		t.Fatal("plugin rendered nothing")
	}
	return p, view
}

// noteBarRegion returns the hit region of the note pane's bar part named by
// id, as the last frame registered it.
func noteBarRegion(t *testing.T, p *Plugin, id string) mouse.Region {
	t.Helper()
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if region.ID == id {
			if _, ok := region.Data.(noteScrollbarHit); ok {
				return region
			}
		}
	}
	t.Fatalf("the frame registered no %s region for the note bar", id)
	return mouse.Region{}
}

// The full gesture through this plugin's own input path: a bar press arms a
// drag without moving anything, held motion scrolls that pane, and a release
// far away settles it with the offset where the pointer left it.
func TestNotePaneScrollbarDragEndToEndThroughHost(t *testing.T) {
	p, view := notePaneOverflowPlugin(t, "nt-long")
	before := view.ScrollOffset()

	thumb := noteBarRegion(t, p, regionNoteScrollbarThumb)
	p.handleMouse(tea.MouseClickMsg{X: thumb.Rect.X, Y: thumb.Rect.Y, Button: tea.MouseLeft})
	if got := p.mouseHandler.DragRegion(); got != regionNoteScrollbarThumb {
		t.Fatalf("bar press started drag %q, want %s", got, regionNoteScrollbarThumb)
	}
	if !p.noteBar.active {
		t.Fatal("bar press did not arm the host's gesture")
	}
	if view.ScrollOffset() != before {
		t.Fatalf("thumb grab at rest moved the offset to %d", view.ScrollOffset())
	}

	p.handleMouse(tea.MouseMotionMsg{X: thumb.Rect.X, Y: thumb.Rect.Y + 3, Button: tea.MouseLeft})
	if view.ScrollOffset() == before {
		t.Fatal("held motion did not scroll the note")
	}

	p.handleMouse(tea.MouseReleaseMsg{X: 1, Y: 1})
	if p.noteBar.active {
		t.Fatal("release did not settle the gesture")
	}

	// A fresh frame re-registers the bar where the new offset put the thumb.
	if got := p.View(140, 36); got == "" {
		t.Fatal("plugin rendered nothing")
	}
	fresh := noteBarRegion(t, p, regionNoteScrollbarThumb)
	if fresh.Rect.Y == thumb.Rect.Y {
		t.Fatal("re-render left the thumb where the idle offset put it")
	}
	p.handleMouse(tea.MouseClickMsg{X: fresh.Rect.X, Y: fresh.Rect.Y, Button: tea.MouseLeft})
	if !p.noteBar.active {
		t.Fatal("fresh grab refused after settle")
	}
	p.handleMouse(tea.MouseReleaseMsg{X: 1, Y: 1})
}

// A track click jumps so the thumb top anchors at the grabbed row, and the
// same gesture keeps dragging from there through the press-time snapshot.
func TestNotePaneTrackClickAnchorsAndContinues(t *testing.T) {
	p, view := notePaneOverflowPlugin(t, "nt-long")

	track := noteBarRegion(t, p, regionNoteScrollbarTrack)
	pressRow := track.Rect.H / 2 // below the thumb: a genuine track press
	want := ui.OffsetAtRow(view.ScrollbarParams(), pressRow)

	p.handleMouse(tea.MouseClickMsg{X: track.Rect.X, Y: track.Rect.Y + pressRow, Button: tea.MouseLeft})
	if !p.noteBar.active {
		t.Fatal("track press did not arm the gesture")
	}
	if view.ScrollOffset() != want {
		t.Fatalf("track click left offset %d, want anchor %d", view.ScrollOffset(), want)
	}

	p.handleMouse(tea.MouseMotionMsg{X: track.Rect.X, Y: track.Rect.Y + pressRow - 3, Button: tea.MouseLeft})
	if view.ScrollOffset() >= want {
		t.Fatalf("anchored drag did not move back up: %d >= %d", view.ScrollOffset(), want)
	}
	p.handleMouse(tea.MouseReleaseMsg{X: 1, Y: 1})
}

// The bar wins its column over the leaf body drawn under it, and the second
// press of a rapid double-press re-grabs instead of being absorbed as a
// focus-only body answer.
func TestNotePaneBarPressBeatsBodyAndDoublePressReGrabs(t *testing.T) {
	p, _ := notePaneOverflowPlugin(t, "nt-long")

	thumb := noteBarRegion(t, p, regionNoteScrollbarThumb)
	hit := p.mouseHandler.HitMap.Test(thumb.Rect.X, thumb.Rect.Y+1)
	if hit == nil {
		t.Fatal("nothing registered under the bar column")
	}
	if _, ok := hit.Data.(noteScrollbarHit); !ok {
		t.Fatalf("point under the bar resolved to %#v, want the note bar", hit.Data)
	}

	click := tea.MouseClickMsg{X: thumb.Rect.X, Y: thumb.Rect.Y, Button: tea.MouseLeft}
	p.handleMouse(click)
	p.handleMouse(tea.MouseReleaseMsg{X: thumb.Rect.X, Y: thumb.Rect.Y})
	if p.noteBar.active {
		t.Fatal("release did not settle the first grab")
	}
	// Bubble Tea emits the second press as ActionDoubleClick.
	p.handleMouse(click)
	if !p.noteBar.active {
		t.Fatal("a rapid second press on the bar did not re-grab it")
	}
	if got := p.mouseHandler.DragRegion(); got != regionNoteScrollbarThumb {
		t.Fatalf("second press started drag %q, want %s", got, regionNoteScrollbarThumb)
	}
	p.handleMouse(tea.MouseReleaseMsg{X: 1, Y: 1})
}

// A release lost off-window recovers on the next button-less motion, which is
// where the shared drag machinery ends a stale gesture on every other surface.
func TestNotePaneScrollbarLostReleaseSettlesOnHover(t *testing.T) {
	p, _ := notePaneOverflowPlugin(t, "nt-long")
	thumb := noteBarRegion(t, p, regionNoteScrollbarThumb)

	p.handleMouse(tea.MouseClickMsg{X: thumb.Rect.X, Y: thumb.Rect.Y, Button: tea.MouseLeft})
	if !p.noteBar.active {
		t.Fatal("press did not arm the gesture")
	}

	p.handleMouse(tea.MouseMotionMsg{X: thumb.Rect.X, Y: thumb.Rect.Y + 2})
	if p.noteBar.active {
		t.Fatal("lost release left the scrollbar gesture live")
	}
}

// A tab whose content fits draws no interactive bar and registers no regions:
// the reserved column is an anti-jitter spacer, not a control.
func TestNotePaneFittingContentRegistersNoBarRegions(t *testing.T) {
	p, _ := notePaneOverflowPlugin(t, "nt-short")
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if isNoteScrollbarDragID(region.ID) {
			t.Fatalf("fitting note registered a bar region at %#v", region.Rect)
		}
	}
}
