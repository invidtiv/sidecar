package notes

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

func notesListTestPlugin(t *testing.T, count int) *Plugin {
	t.Helper()
	contents := make([]string, count)
	for i := range contents {
		contents[i] = "note body"
	}
	p := layoutTestPlugin(t, contents...)
	return p
}

func notesClickMsg(x, y int) tea.MouseClickMsg {
	return tea.MouseClickMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft})
}

func notesMotionMsg(x, y int) tea.MouseMotionMsg {
	return tea.MouseMotionMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft})
}

func notesReleaseMsg(x, y int) tea.MouseReleaseMsg {
	return tea.MouseReleaseMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft})
}

func findNotesRegion(t *testing.T, p *Plugin, id string) mouse.Rect {
	t.Helper()
	for _, r := range p.mouseHandler.HitMap.Regions() {
		if r.ID == id {
			return r.Rect
		}
	}
	t.Fatalf("no %q region registered", id)
	return mouse.Rect{}
}

// --- Note list ---

func TestNotesListScrollbar_RegionsBeatNoteRows(t *testing.T) {
	p := notesListTestPlugin(t, 30)
	_ = p.View(p.width, p.height)

	thumb := findNotesRegion(t, p, ui.RegionScrollbarThumb)
	if got := p.mouseHandler.HitMap.Test(thumb.X, thumb.Y); got == nil || got.ID != ui.RegionScrollbarThumb {
		t.Fatalf("press on thumb hit %+v, want scrollbar-thumb (note rows must lose)", got)
	}
	track := findNotesRegion(t, p, ui.RegionScrollbarTrack)
	below := track.Y + track.H - 1
	if below >= thumb.Y+thumb.H {
		if got := p.mouseHandler.HitMap.Test(track.X, below); got == nil || got.ID != ui.RegionScrollbarTrack {
			t.Fatalf("press below thumb hit %+v, want scrollbar-track", got)
		}
	}
}

func TestNotesListScrollbar_ThumbDragEndToEnd(t *testing.T) {
	p := notesListTestPlugin(t, 30)
	_ = p.View(p.width, p.height)

	thumb := findNotesRegion(t, p, ui.RegionScrollbarThumb)
	if _, _ = p.handleMouse(notesClickMsg(thumb.X, thumb.Y)); !p.mouseHandler.IsDragging() {
		t.Fatal("thumb press did not start a drag")
	}
	startOffset := p.scrollOff

	if _, _ = p.handleMouse(notesMotionMsg(thumb.X, thumb.Y+8)); p.scrollOff <= startOffset {
		t.Fatalf("dragging down left scrollOff at %d (start %d)", p.scrollOff, startOffset)
	}
	if _, _ = p.handleMouse(notesMotionMsg(thumb.X, thumb.Y+500)); !p.mouseHandler.IsDragging() {
		t.Fatal("dragging past the end lost the gesture")
	}
	params := p.scrollPointer.dragging.params
	if want := params.TotalItems - params.VisibleItems; p.scrollOff != want {
		t.Fatalf("past-end offset = %d, want clamped %d", p.scrollOff, want)
	}
	if _, _ = p.handleMouse(notesReleaseMsg(100, 2)); p.mouseHandler.IsDragging() {
		t.Fatal("release did not end the drag")
	}
	if p.scrollPointer.dragArea != scrollAreaNone {
		t.Fatal("release left scrollbar gesture state behind")
	}
}

func TestNotesListScrollbar_NoRegionsWhenContentFits(t *testing.T) {
	p := notesListTestPlugin(t, 3)
	_ = p.View(p.width, p.height)

	for _, r := range p.mouseHandler.HitMap.Regions() {
		if r.ID == ui.RegionScrollbarThumb || r.ID == ui.RegionScrollbarTrack {
			t.Fatalf("got %q region with all notes visible", r.ID)
		}
	}
}

// --- Note body (preview mode) ---

func TestNotesBodyScrollbar_PreviewDragMovesReadOffset(t *testing.T) {
	content := numberedContent(80)
	p := layoutTestPlugin(t, content)
	p.editorTextarea.SetValue(content)
	_ = p.View(p.width, p.height)

	bar := p.scrollPointer.body
	if !bar.has {
		t.Fatal("long note rendered no body scrollbar")
	}
	start := p.previewScrollOff
	if _, _ = p.handleMouse(notesClickMsg(bar.trackX, bar.trackY+bar.thumbTop+1)); !p.mouseHandler.IsDragging() {
		t.Fatal("body thumb press did not start a drag")
	}
	if _, _ = p.handleMouse(notesMotionMsg(bar.trackX, bar.trackY+bar.params.TrackHeight-2)); p.previewScrollOff <= start {
		t.Fatalf("dragging down left previewScrollOff at %d (start %d)", p.previewScrollOff, start)
	}
	if _, _ = p.handleMouse(notesReleaseMsg(bar.trackX, bar.trackY+bar.params.TrackHeight-2)); p.mouseHandler.IsDragging() {
		t.Fatal("release did not end the drag")
	}
}

// --- Note body (edit mode): the gesture must scroll without touching text
// or caret placement. ---

func TestNotesBodyScrollbar_EditDragLeavesTextAndCaretUntouched(t *testing.T) {
	var lines []string
	for i := 0; i < 60; i++ {
		lines = append(lines, "the quick brown fox jumps over the lazy dog")
	}
	content := strings.Join(lines, "\n")
	p := newEditPlugin(t, content)
	// Park the caret mid-document so any caret corruption is observable.
	parkLine, parkCol := 30, 7
	p.setTextareaCursorPosition(parkLine, parkCol)
	p.trackTextareaScroll()
	_ = p.View(p.width, p.height)

	bar := p.scrollPointer.body
	if !bar.has {
		t.Fatal("edit view rendered no body scrollbar")
	}
	wantValue := p.editorTextarea.Value()

	if _, _ = p.handleMouse(notesClickMsg(bar.trackX, bar.trackY+bar.thumbTop+1)); !p.mouseHandler.IsDragging() {
		t.Fatal("body thumb press did not start a drag")
	}
	dragY := bar.trackY + bar.params.TrackHeight - 3
	if _, _ = p.handleMouse(notesMotionMsg(bar.trackX, dragY)); !p.mouseHandler.IsDragging() {
		t.Fatal("drag motion lost the gesture")
	}
	if _, _ = p.handleMouse(notesReleaseMsg(bar.trackX, dragY)); p.mouseHandler.IsDragging() {
		t.Fatal("release did not end the drag")
	}

	if got := p.editorTextarea.Value(); got != wantValue {
		t.Fatal("scrollbar drag modified the note text")
	}
	if line, col := p.editorTextarea.Line(), p.editorTextarea.Column(); line != parkLine || col != parkCol {
		t.Fatalf("caret moved from (%d,%d) to (%d,%d)", parkLine, parkCol, line, col)
	}
	if p.editorTextarea.ScrollYOffset() == 0 && bar.thumbTop != 0 {
		t.Fatal("drag scrolled nowhere; viewport should have moved")
	}
}

func TestNotesBodyScrollbar_EditTrackClickAnchorsJump(t *testing.T) {
	var lines []string
	for i := 0; i < 60; i++ {
		lines = append(lines, "body line for track click testing")
	}
	content := strings.Join(lines, "\n")
	p := newEditPlugin(t, content)
	// Park the caret mid-document; the textarea never leaves its caret
	// outside the viewport, so a compatible jump moves the view while the
	// caret stays put logically.
	parkLine := 40
	p.setTextareaCursorPosition(parkLine, 0)
	p.trackTextareaScroll()
	_ = p.View(p.width, p.height)

	bar := p.scrollPointer.body
	wantValue := p.editorTextarea.Value()
	// Grab a track row whose anchor offset keeps the parked caret visible.
	grabOffset := bar.params.TotalItems - bar.params.VisibleItems - 6
	if grabOffset < 1 {
		t.Fatal("note too short for a mid-track jump; test premise broken")
	}
	grabY := bar.trackY + ui.RowForOffset(bar.params, grabOffset)
	if _, _ = p.handleMouse(notesClickMsg(bar.trackX, grabY)); !p.mouseHandler.IsDragging() {
		t.Fatal("track click did not continue as a drag")
	}
	if _, _ = p.handleMouse(notesReleaseMsg(bar.trackX, grabY)); p.mouseHandler.IsDragging() {
		t.Fatal("release did not end the drag")
	}
	if got := p.editorTextarea.Value(); got != wantValue {
		t.Fatal("track jump modified the note text")
	}
	if line, col := p.editorTextarea.Line(), p.editorTextarea.Column(); line != parkLine || col != 0 {
		t.Fatalf("caret moved to (%d,%d), want (%d,0)", line, col, parkLine)
	}
	off := p.previewScrollOff
	height := bar.params.VisibleItems
	if off < parkLine-height+1 || off > parkLine {
		t.Fatalf("viewport offset %d does not keep caret %d visible in %d rows", off, parkLine, height)
	}
}

// While an inline terminal editor owns the pane, no body bar regions exist
// and the list bar still routes through the scrollbar gate instead of
// reaching the pane app as clicks or selection drags.
func TestNotesInlineEditor_ScrollbarGatePrecedesEditorFallThrough(t *testing.T) {
	p := notesListTestPlugin(t, 40)
	p.edit.Model = tty.New(nil)
	p.edit.Active = true

	out := p.View(p.width, p.height)
	_ = out

	// The body bar is replaced by the inline pane: none of its regions may
	// linger to intercept presses that belong to the editor.
	if p.scrollPointer.body.has {
		t.Fatal("body scrollbar snapshot alive while inline editor owns the pane")
	}
	for _, r := range p.mouseHandler.HitMap.Regions() {
		if area, ok := r.Data.(scrollAreaID); ok && area == scrollAreaBody {
			t.Fatalf("region %q registered for the body while inline editor active", r.ID)
		}
	}

	// The note-list bar still exists and must be consumed by sidecar: press,
	// drag, release never reach tty forwarding or click-away handling.
	thumb, ok := findOptionalRegion(p, ui.RegionScrollbarThumb)
	if !ok {
		t.Fatal("list scrollbar missing during inline editing")
	}
	if _, _ = p.handleMouse(notesClickMsg(thumb.X, thumb.Y)); !p.mouseHandler.IsDragging() {
		t.Fatal("inline editor swallowed the list-bar press")
	}
	if _, _ = p.handleMouse(notesMotionMsg(thumb.X, thumb.Y+5)); !p.mouseHandler.IsDragging() {
		t.Fatal("inline editor broke the list-bar drag")
	}
	if _, _ = p.handleMouse(notesReleaseMsg(thumb.X, thumb.Y+5)); p.mouseHandler.IsDragging() {
		t.Fatal("release did not end the list-bar drag")
	}
	if p.edit.Dragging {
		t.Fatal("scrollbar gesture leaked into the editor's drag tracking")
	}
}

func findOptionalRegion(p *Plugin, id string) (mouse.Rect, bool) {
	for _, r := range p.mouseHandler.HitMap.Regions() {
		if r.ID == id {
			return r.Rect, true
		}
	}
	return mouse.Rect{}, false
}
