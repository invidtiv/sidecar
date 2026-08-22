package overview

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/ui"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// stubTdNote teaches the fake td to answer `note show`: id "nt-long" resolves
// to a body far taller than the preview viewport, anything else to one line.
func stubTdNote(t *testing.T) {
	t.Helper()
	long := fmt.Sprintf(`{"id":"nt-long","title":"A long note","content":%q,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`,
		strings.Repeat("line one of the note\n", 120))
	short := `{"id":"nt-short","title":"Short","content":"one line","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"case \"$*\" in\n" +
		"*nt-long*) printf '%s\\n' '" + long + "' ;;\n" +
		"*nt-short*) printf '%s\\n' '" + short + "' ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(dir, "td"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// openLongPreviewNote opens one note pane holding far more rendered rows than
// its viewport and leaves it on screen.
func openLongPreviewNote(t *testing.T) *Model {
	t.Helper()
	stubPreviewTd(t)
	stubTdNote(t)
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, m.openPreviewNote("nt-long"))
	m.WorkspacesView(previewWide, previewTall)
	note := m.preview.note
	if note == nil || note.view() == nil {
		t.Fatal("the preview opened no note pane")
	}
	if !note.view().HasScrollbar() {
		t.Fatal("fixture note does not overflow its viewport")
	}
	return m
}

// previewNoteBarRect returns the hit rect of the note bar part named by id,
// as the last frame registered it.
func previewNoteBarRect(t *testing.T, m *Model, id string) mouse.Rect {
	t.Helper()
	for _, region := range m.workspacesMouse.HitMap.Regions() {
		if region.ID != id {
			continue
		}
		if kind, ok := regionKind(&region); ok && kind == previewNoteBarKind {
			return region.Rect
		}
	}
	t.Fatalf("the frame registered no %s region for the note bar", id)
	return mouse.Rect{}
}

// The full gesture through this surface's own input path: a bar press arms a
// drag without moving anything, held motion scrolls the note, and a release
// far outside the pane settles it with the offset where the pointer left it.
func TestPreviewNoteScrollbarDragEndToEndThroughHost(t *testing.T) {
	m := openLongPreviewNote(t)
	view := m.preview.note.view()
	before := view.ScrollOffset()

	thumb := previewNoteBarRect(t, m, ui.RegionScrollbarThumb)
	run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{X: thumb.X, Y: thumb.Y, Button: tea.MouseLeft}))
	if got := m.workspacesMouse.DragRegion(); got != ui.RegionScrollbarThumb {
		t.Fatalf("bar press started drag %q, want %s", got, ui.RegionScrollbarThumb)
	}
	if !m.preview.note.bar.active {
		t.Fatal("bar press did not arm the host's gesture")
	}
	if view.ScrollOffset() != before {
		t.Fatalf("thumb grab at rest moved the offset to %d", view.ScrollOffset())
	}

	run(t, m, m.WorkspacesMouse(tea.MouseMotionMsg{X: thumb.X, Y: thumb.Y + 3, Button: tea.MouseLeft}))
	if view.ScrollOffset() == before {
		t.Fatal("held motion did not scroll the note")
	}

	run(t, m, m.WorkspacesMouse(tea.MouseReleaseMsg{X: 1, Y: 1}))
	if m.preview.note.bar.active {
		t.Fatal("release did not settle the gesture")
	}

	// A fresh frame re-registers the bar where the new offset put the thumb,
	// and the machinery still answers there.
	m.WorkspacesView(previewWide, previewTall)
	fresh := previewNoteBarRect(t, m, ui.RegionScrollbarThumb)
	if fresh.Y == thumb.Y {
		t.Fatal("re-render left the thumb where the idle offset put it")
	}
	run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{X: fresh.X, Y: fresh.Y, Button: tea.MouseLeft}))
	if !m.preview.note.bar.active {
		t.Fatal("fresh grab refused after settle")
	}
	run(t, m, m.WorkspacesMouse(tea.MouseReleaseMsg{X: 1, Y: 1}))
}

// A track click jumps so the thumb top anchors at the grabbed row (macOS
// jump-to-spot), and the same gesture keeps dragging from there through the
// press-time snapshot.
func TestPreviewNoteTrackClickAnchorsAndContinues(t *testing.T) {
	m := openLongPreviewNote(t)
	view := m.preview.note.view()

	track := previewNoteBarRect(t, m, ui.RegionScrollbarTrack)
	pressRow := track.H / 2 // a genuine track press below the thumb
	want := ui.OffsetAtRow(view.ScrollbarParams(), pressRow)

	run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{X: track.X, Y: track.Y + pressRow, Button: tea.MouseLeft}))
	if !m.preview.note.bar.active {
		t.Fatal("track press did not arm the gesture")
	}
	if view.ScrollOffset() != want {
		t.Fatalf("track click left offset %d, want anchor %d", view.ScrollOffset(), want)
	}

	// The jump anchors the gesture: continuing motion maps straight onto
	// track rows through the press-time snapshot.
	run(t, m, m.WorkspacesMouse(tea.MouseMotionMsg{X: track.X, Y: track.Y + pressRow - 3, Button: tea.MouseLeft}))
	if view.ScrollOffset() >= want {
		t.Fatalf("anchored drag did not move back up: %d >= %d", view.ScrollOffset(), want)
	}
	run(t, m, m.WorkspacesMouse(tea.MouseReleaseMsg{X: 1, Y: 1}))
}

// The bar wins its column: a point where the pane body is drawn under the bar
// resolves to the bar and never lands as a focus-only body click, and the
// second press of a rapid double-press re-grabs instead of being absorbed.
func TestPreviewNoteBarPressBeatsBodyAndDoublePressReGrabs(t *testing.T) {
	m := openLongPreviewNote(t)

	thumb := previewNoteBarRect(t, m, ui.RegionScrollbarThumb)
	hit := m.workspacesMouse.HitMap.Test(thumb.X, thumb.Y+1)
	if hit == nil {
		t.Fatal("nothing registered under the bar column")
	}
	if kind, _ := regionKind(hit); kind != previewNoteBarKind {
		t.Fatalf("point under the bar resolved to %q, want the note bar", kind)
	}

	click := tea.MouseClickMsg{X: thumb.X, Y: thumb.Y, Button: tea.MouseLeft}
	run(t, m, m.WorkspacesMouse(click))
	run(t, m, m.WorkspacesMouse(tea.MouseReleaseMsg{X: thumb.X, Y: thumb.Y}))
	if m.preview.note.bar.active {
		t.Fatal("release did not settle the first grab")
	}
	// Bubble Tea emits the second press as ActionDoubleClick.
	run(t, m, m.WorkspacesMouse(click))
	if !m.preview.note.bar.active {
		t.Fatal("a rapid second press on the bar did not re-grab it")
	}
	run(t, m, m.WorkspacesMouse(tea.MouseReleaseMsg{X: 1, Y: 1}))
}

// A release lost off-window recovers on the next button-less motion, which is
// where the shared drag machinery ends a stale gesture on every other surface.
func TestPreviewNoteScrollbarLostReleaseRecoversOnHover(t *testing.T) {
	m := openLongPreviewNote(t)
	thumb := previewNoteBarRect(t, m, ui.RegionScrollbarThumb)

	run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{X: thumb.X, Y: thumb.Y, Button: tea.MouseLeft}))
	if !m.preview.note.bar.active {
		t.Fatal("press did not arm the gesture")
	}

	run(t, m, m.WorkspacesMouse(tea.MouseMotionMsg{X: thumb.X, Y: thumb.Y + 2}))
	if m.preview.note.bar.active {
		t.Fatal("lost release left the scrollbar gesture live")
	}
	if m.workspacesMouse.IsDragging() {
		t.Fatal("shared handler still dragging after lost release")
	}
}

// A tab whose content fits draws no interactive bar and registers no regions:
// the reserved column is an anti-jitter spacer, not a control.
func TestPreviewNoteFittingContentRegistersNoBarRegions(t *testing.T) {
	stubPreviewTd(t)
	stubTdNote(t)
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, m.openPreviewNote("nt-short"))
	m.WorkspacesView(previewWide, previewTall)
	note := m.preview.note
	if note == nil || note.view() == nil {
		t.Fatal("the preview opened no note pane")
	}
	for _, region := range m.workspacesMouse.HitMap.Regions() {
		if kind, ok := regionKind(&region); ok && kind == previewNoteBarKind {
			t.Fatalf("fitting note registered a bar region at %#v", region.Rect)
		}
	}
}
